// SPDX-FileCopyrightText: syuilo and misskey-project
// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import (
	"context"
	"sync"
	"time"

	"github.com/elythia-network/elythia/plugin"
)

const (
	mediaUserRequestsPerMinute   = 120
	mediaGlobalRequestsPerMinute = 6000
	mediaUserActiveLimit         = 4
	mediaUserWaitLimit           = 32
	mediaGlobalWaitLimit         = 256
	mediaQueueWaitTimeout        = 30 * time.Second
	mediaResponseCacheTTL        = 30 * time.Second
	mediaResponseCacheEntries    = 64
	mediaResponseCacheBytes      = 32 << 20
)

// mediaRequestLimiter bounds both repeated requests and the number of user IDs
// retained in its fixed-size minute window.
type mediaRequestLimiter struct {
	mu          sync.Mutex
	perUser     int
	globalLimit int
	window      time.Time
	global      int
	users       map[string]int
}

func newMediaRequestLimiter(perUser, global int) *mediaRequestLimiter {
	return &mediaRequestLimiter{perUser: perUser, globalLimit: global, users: make(map[string]int)}
}

func (l *mediaRequestLimiter) Allow(userID string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	minute := now.UTC().Truncate(time.Minute)
	if minute.After(l.window) {
		l.window = minute
		l.global = 0
		clear(l.users)
	}
	if userID == "" || l.global >= l.globalLimit || l.users[userID] >= l.perUser {
		return false
	}
	if _, exists := l.users[userID]; !exists && len(l.users) >= l.globalLimit {
		return false
	}
	l.users[userID]++
	l.global++
	return true
}

func mediaQueueWaitBudget(requestCapacity, bufferCapacity, userActiveLimit, maxBytes int, requestTimeout, minimum time.Duration) time.Duration {
	if maxBytes < 1 || bufferCapacity <= maxBytes+1 || requestCapacity < 1 || userActiveLimit < 1 {
		return minimum
	}
	sharedSlots := bufferCapacity / (maxBytes + 1)
	if sharedSlots > requestCapacity {
		sharedSlots = requestCapacity
	}
	perUserSlots := sharedSlots - 1
	if perUserSlots > userActiveLimit {
		perUserSlots = userActiveLimit
	}
	if perUserSlots < 1 {
		return minimum
	}
	factor := (sharedSlots + perUserSlots - 1) / perUserSlots
	budget := time.Duration(factor) * requestTimeout
	if budget < minimum {
		return minimum
	}
	return budget
}

type mediaGateUser struct {
	active  int
	waiting int
}

type mediaGateWaiter struct {
	userID  string
	limit   int
	ready   chan struct{}
	granted bool
}

type mediaRequestGate struct {
	mu             sync.Mutex
	activeLimit    int
	globalWaitMax  int
	userWaitMax    int
	bufferCapacity int
	active         int
	users          map[string]*mediaGateUser
	waiters        []*mediaGateWaiter
}

func newMediaRequestGate(activeLimit, globalWaitMax, userWaitMax, bufferCapacity int) *mediaRequestGate {
	return &mediaRequestGate{
		activeLimit: activeLimit, globalWaitMax: globalWaitMax, userWaitMax: userWaitMax,
		bufferCapacity: bufferCapacity, users: make(map[string]*mediaGateUser),
	}
}

func (g *mediaRequestGate) userLimit(maxBytes int) int {
	if maxBytes < 1 || g.bufferCapacity <= maxBytes+1 {
		return 0
	}
	limit := g.bufferCapacity/(maxBytes+1) - 1
	if limit > mediaUserActiveLimit {
		limit = mediaUserActiveLimit
	}
	return limit
}

func (g *mediaRequestGate) acquire(ctx context.Context, userID string, maxBytes int) (func(), error) {
	limit := g.userLimit(maxBytes)
	if userID == "" || limit < 1 {
		return nil, errMediaBusy
	}
	g.mu.Lock()
	state := g.users[userID]
	if state == nil {
		state = &mediaGateUser{}
		g.users[userID] = state
	}
	if g.active < g.activeLimit && state.active < limit && !g.hasEligibleWaiterLocked() {
		state.active++
		g.active++
		g.mu.Unlock()
		return g.releaseFunc(userID), nil
	}
	if len(g.waiters) >= g.globalWaitMax || state.waiting >= g.userWaitMax {
		g.cleanupUserLocked(userID)
		g.mu.Unlock()
		return nil, errMediaBusy
	}
	w := &mediaGateWaiter{userID: userID, limit: limit, ready: make(chan struct{})}
	state.waiting++
	g.waiters = append(g.waiters, w)
	g.scheduleLocked()
	g.mu.Unlock()

	select {
	case <-w.ready:
		return g.releaseFunc(userID), nil
	case <-ctx.Done():
		g.mu.Lock()
		if w.granted {
			g.mu.Unlock()
			g.releaseFunc(userID)()
			return nil, ctx.Err()
		}
		for i, queued := range g.waiters {
			if queued == w {
				g.waiters = append(g.waiters[:i], g.waiters[i+1:]...)
				break
			}
		}
		if state := g.users[userID]; state != nil {
			state.waiting--
		}
		g.cleanupUserLocked(userID)
		g.scheduleLocked()
		g.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (g *mediaRequestGate) hasEligibleWaiterLocked() bool {
	if g.active >= g.activeLimit {
		return false
	}
	for _, w := range g.waiters {
		if state := g.users[w.userID]; state != nil && state.active < w.limit {
			return true
		}
	}
	return false
}

func (g *mediaRequestGate) releaseFunc(userID string) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			if state := g.users[userID]; state != nil {
				state.active--
			}
			g.active--
			g.cleanupUserLocked(userID)
			g.scheduleLocked()
			g.mu.Unlock()
		})
	}
}

func (g *mediaRequestGate) scheduleLocked() {
	for g.active < g.activeLimit {
		selected := -1
		for i, w := range g.waiters {
			state := g.users[w.userID]
			if state != nil && state.active < w.limit {
				selected = i
				break
			}
		}
		if selected < 0 {
			return
		}
		w := g.waiters[selected]
		g.waiters = append(g.waiters[:selected], g.waiters[selected+1:]...)
		state := g.users[w.userID]
		state.waiting--
		state.active++
		g.active++
		w.granted = true
		close(w.ready)
	}
}

func (g *mediaRequestGate) cleanupUserLocked(userID string) {
	if state := g.users[userID]; state != nil && state.active == 0 && state.waiting == 0 {
		delete(g.users, userID)
	}
}

type mediaCacheKey struct {
	PostID    string
	FetchedAt time.Time
	SourceURL string
	MaxBytes  int
}

type mediaCacheEntry struct {
	blob      plugin.Blob
	expiresAt time.Time
	used      uint64
}

type mediaResponseCache struct {
	mu         sync.Mutex
	maxBytes   int
	maxEntries int
	ttl        time.Duration
	bytes      int
	sequence   uint64
	entries    map[mediaCacheKey]mediaCacheEntry
}

func newMediaResponseCache(maxBytes, maxEntries int, ttl time.Duration) *mediaResponseCache {
	return &mediaResponseCache{maxBytes: maxBytes, maxEntries: maxEntries, ttl: ttl, entries: make(map[mediaCacheKey]mediaCacheEntry)}
}

func cloneMediaBlob(blob plugin.Blob) plugin.Blob {
	body := make([]byte, len(blob.Body))
	copy(body, blob.Body)
	blob.Body = body
	return blob
}

func (c *mediaResponseCache) Get(key mediaCacheKey, now time.Time) (plugin.Blob, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return plugin.Blob{}, false
	}
	if !entry.expiresAt.After(now) {
		c.removeLocked(key)
		return plugin.Blob{}, false
	}
	c.sequence++
	entry.used = c.sequence
	c.entries[key] = entry
	return cloneMediaBlob(entry.blob), true
}

func (c *mediaResponseCache) Set(key mediaCacheKey, blob plugin.Blob, now time.Time) {
	if len(blob.Body) == 0 || len(blob.Body) > c.maxBytes || c.maxEntries < 1 || c.ttl <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.removeLocked(key)
	for len(c.entries) >= c.maxEntries || c.bytes+len(blob.Body) > c.maxBytes {
		var oldestKey mediaCacheKey
		oldest := ^uint64(0)
		for candidate, entry := range c.entries {
			if entry.used < oldest {
				oldest, oldestKey = entry.used, candidate
			}
		}
		if oldest == ^uint64(0) {
			return
		}
		c.removeLocked(oldestKey)
	}
	c.sequence++
	copy := cloneMediaBlob(blob)
	c.entries[key] = mediaCacheEntry{blob: copy, expiresAt: now.Add(c.ttl), used: c.sequence}
	c.bytes += len(copy.Body)
}

func (c *mediaResponseCache) removeLocked(key mediaCacheKey) {
	if entry, ok := c.entries[key]; ok {
		c.bytes -= len(entry.blob.Body)
		delete(c.entries, key)
	}
}

func releaseMediaWhenRequestEnds(ctx context.Context, releases ...func()) {
	var once sync.Once
	release := func() {
		once.Do(func() {
			for _, fn := range releases {
				if fn != nil {
					fn()
				}
			}
		})
	}
	if ctx.Done() == nil {
		release()
		return
	}
	context.AfterFunc(ctx, release)
}

var processMediaRequestLimiter = newMediaRequestLimiter(mediaUserRequestsPerMinute, mediaGlobalRequestsPerMinute)
var processMediaRequestGate = newMediaRequestGate(mediaRequestCapacity, mediaGlobalWaitLimit, mediaUserWaitLimit, mediaBufferCapacity)
var processMediaResponseCache = newMediaResponseCache(mediaResponseCacheBytes, mediaResponseCacheEntries, mediaResponseCacheTTL)
