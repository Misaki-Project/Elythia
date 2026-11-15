// SPDX-FileCopyrightText: syuilo and misskey-project
// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	publishedMediaTTL           = 10 * time.Minute
	publishedMediaMaxEntries    = 128
	publishedMediaMaxBytes      = 128 << 20
	publishedMediaMaxObjectSize = 50 << 20
	publishedMediaMaxActiveGets = 32
)

var (
	errPublishedMediaTooLarge = errors.New("published media exceeds its size limit")
	errPublishedMediaFull     = errors.New("published media registry is full")
	errPublishedMediaType     = errors.New("published media type is not allowed")
	errPublishedMediaEmpty    = errors.New("published media body is empty")
)

type publishedMediaLookupState uint8

const (
	publishedMediaMissing publishedMediaLookupState = iota
	publishedMediaFound
	publishedMediaBusy
)

type publishedMedia struct {
	Token       string
	Extension   string
	ContentType string
	Size        int64
	ExpiresAt   time.Time
	Body        []byte
}

type publishedMediaStats struct {
	Entries       int
	Bytes         int64
	Active        int
	SourceAliases int
}

type publishedMediaEntry struct {
	publishedMedia
	fingerprint [32]byte
	sourceKeys  map[[32]byte]uint64
	lastUsed    uint64
	active      int
}

type publishedMediaRegistry struct {
	mu               sync.Mutex
	entries          map[string]*publishedMediaEntry
	byFingerprint    map[[32]byte]string
	bySource         map[[32]byte]publishedMediaSourceAlias
	maxBytes         int64
	maxEntries       int
	maxObjectBytes   int64
	ttl              time.Duration
	maxActiveGets    int
	maxSourceAliases int
	bytes            int64
	activeGets       int
	accessSequence   uint64
	sourceSequence   uint64
}

type publishedMediaSourceAlias struct {
	token string
	used  uint64
}

func newPublishedMediaRegistry(maxBytes int64, maxEntries int, ttl time.Duration, maxObjectBytes int64, maxActiveGets int) *publishedMediaRegistry {
	if maxBytes < 0 {
		maxBytes = 0
	}
	if maxEntries < 0 {
		maxEntries = 0
	}
	if maxObjectBytes < 0 {
		maxObjectBytes = 0
	}
	if maxActiveGets < 0 {
		maxActiveGets = 0
	}
	return &publishedMediaRegistry{
		entries: make(map[string]*publishedMediaEntry), byFingerprint: make(map[[32]byte]string),
		bySource: make(map[[32]byte]publishedMediaSourceAlias), maxBytes: maxBytes, maxEntries: maxEntries,
		maxObjectBytes: maxObjectBytes, ttl: ttl, maxActiveGets: maxActiveGets,
		maxSourceAliases: maxEntries * 4,
	}
}

var processPublishedMediaRegistry = newPublishedMediaRegistry(
	publishedMediaMaxBytes, publishedMediaMaxEntries, publishedMediaTTL,
	publishedMediaMaxObjectSize, publishedMediaMaxActiveGets,
)

// publishedMediaSourceKey identifies a validated upstream descriptor without
// retaining or exposing its source URL in the registry key.
func publishedMediaSourceKey(parts ...string) [32]byte {
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	var key [32]byte
	copy(key[:], h.Sum(nil))
	return key
}

func (r *publishedMediaRegistry) Publish(contentType string, body []byte, now time.Time) (publishedMedia, error) {
	return r.PublishForSource(contentType, body, [32]byte{}, now)
}

func (r *publishedMediaRegistry) PublishForSource(contentType string, body []byte, sourceKey [32]byte, now time.Time) (publishedMedia, error) {
	extension, ok := publishedMediaExtension(contentType)
	if !ok {
		return publishedMedia{}, errPublishedMediaType
	}
	if len(body) == 0 {
		return publishedMedia{}, errPublishedMediaEmpty
	}
	if int64(len(body)) > r.maxObjectBytes {
		return publishedMedia{}, errPublishedMediaTooLarge
	}
	if r.ttl <= 0 || r.maxBytes <= 0 || r.maxEntries <= 0 {
		return publishedMedia{}, errPublishedMediaFull
	}
	fingerprint := publishedMediaFingerprint(contentType, body)
	ownedBody := append([]byte(nil), body...)
	now = now.UTC()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expireLocked(now)

	if sourceKey != ([32]byte{}) {
		if alias := r.bySource[sourceKey]; alias.token != "" {
			token := alias.token
			if entry := r.entries[token]; entry != nil && residualPublicCacheControl(entry.ExpiresAt, now) != "" {
				r.touchSourceLocked(entry, sourceKey)
				r.touchLocked(entry)
				return entry.publishedMedia, nil
			}
			delete(r.bySource, sourceKey)
			if entry := r.entries[token]; entry != nil {
				delete(entry.sourceKeys, sourceKey)
			}
		}
	}
	if token := r.byFingerprint[fingerprint]; token != "" {
		if entry := r.entries[token]; entry != nil && residualPublicCacheControl(entry.ExpiresAt, now) != "" {
			if sourceKey != ([32]byte{}) {
				r.addSourceAliasLocked(entry, sourceKey, token)
			}
			r.touchLocked(entry)
			return entry.publishedMedia, nil
		}
		if r.byFingerprint[fingerprint] == token {
			delete(r.byFingerprint, fingerprint)
		}
	}

	if int64(len(body)) > r.maxBytes {
		return publishedMedia{}, errPublishedMediaTooLarge
	}
	for len(r.entries) >= r.maxEntries || r.bytes+int64(len(body)) > r.maxBytes {
		if !r.evictLRULocked() {
			return publishedMedia{}, errPublishedMediaFull
		}
	}
	token, err := newPublishedMediaToken(r.entries)
	if err != nil {
		return publishedMedia{}, err
	}
	media := publishedMedia{
		Token: token, Extension: extension, ContentType: contentType,
		Size: int64(len(ownedBody)), ExpiresAt: now.Add(r.ttl), Body: ownedBody,
	}
	entry := &publishedMediaEntry{publishedMedia: media, fingerprint: fingerprint}
	if sourceKey != ([32]byte{}) {
		entry.sourceKeys = make(map[[32]byte]uint64)
		r.addSourceAliasLocked(entry, sourceKey, token)
	}
	r.touchLocked(entry)
	r.entries[token] = entry
	r.byFingerprint[fingerprint] = token
	r.bytes += media.Size
	return media, nil
}

func (r *publishedMediaRegistry) FindBySource(sourceKey [32]byte, now time.Time) (publishedMedia, bool) {
	if sourceKey == ([32]byte{}) {
		return publishedMedia{}, false
	}
	now = now.UTC()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expireLocked(now)
	alias := r.bySource[sourceKey]
	token := alias.token
	entry := r.entries[token]
	if entry == nil || !entry.ExpiresAt.After(now) {
		if alias.token != "" {
			r.removeSourceAliasLocked(sourceKey, alias)
		}
		return publishedMedia{}, false
	}
	if residualPublicCacheControl(entry.ExpiresAt, now) == "" {
		return publishedMedia{}, false
	}
	r.touchSourceLocked(entry, sourceKey)
	r.touchLocked(entry)
	media := entry.publishedMedia
	media.Body = nil
	return media, true
}

func (r *publishedMediaRegistry) Acquire(token, extension string, now time.Time) (publishedMedia, func(), publishedMediaLookupState) {
	now = now.UTC()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expireLocked(now)
	entry := r.entries[token]
	if entry == nil || entry.Extension != extension || !entry.ExpiresAt.After(now) {
		return publishedMedia{}, nil, publishedMediaMissing
	}
	if r.activeGets >= r.maxActiveGets {
		return publishedMedia{}, nil, publishedMediaBusy
	}
	r.activeGets++
	entry.active++
	r.touchLocked(entry)
	var once sync.Once
	release := func() {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			if entry.active > 0 {
				entry.active--
			}
			if r.activeGets > 0 {
				r.activeGets--
			}
			r.expireLocked(time.Now().UTC())
		})
	}
	return entry.publishedMedia, release, publishedMediaFound
}

func (r *publishedMediaRegistry) Stats() publishedMediaStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return publishedMediaStats{Entries: len(r.entries), Bytes: r.bytes, Active: r.activeGets, SourceAliases: len(r.bySource)}
}

func (r *publishedMediaRegistry) touchLocked(entry *publishedMediaEntry) {
	r.accessSequence++
	entry.lastUsed = r.accessSequence
}

func (r *publishedMediaRegistry) touchSourceLocked(entry *publishedMediaEntry, sourceKey [32]byte) {
	if entry.sourceKeys == nil {
		return
	}
	r.sourceSequence++
	entry.sourceKeys[sourceKey] = r.sourceSequence
	if alias, ok := r.bySource[sourceKey]; ok {
		alias.used = r.sourceSequence
		r.bySource[sourceKey] = alias
	}
}

func (r *publishedMediaRegistry) addSourceAliasLocked(entry *publishedMediaEntry, sourceKey [32]byte, token string) {
	if sourceKey == ([32]byte{}) || r.maxSourceAliases < 1 {
		return
	}
	if existing, ok := r.bySource[sourceKey]; ok {
		if existing.token != token {
			if oldEntry := r.entries[existing.token]; oldEntry != nil {
				delete(oldEntry.sourceKeys, sourceKey)
			}
		}
	} else if len(r.bySource) >= r.maxSourceAliases {
		var oldestKey [32]byte
		var oldest publishedMediaSourceAlias
		found := false
		for candidate, alias := range r.bySource {
			if !found || alias.used < oldest.used {
				oldestKey, oldest, found = candidate, alias, true
			}
		}
		if found {
			r.removeSourceAliasLocked(oldestKey, oldest)
		}
	}
	r.sourceSequence++
	if entry.sourceKeys == nil {
		entry.sourceKeys = make(map[[32]byte]uint64)
	}
	entry.sourceKeys[sourceKey] = r.sourceSequence
	r.bySource[sourceKey] = publishedMediaSourceAlias{token: token, used: r.sourceSequence}
}

func (r *publishedMediaRegistry) removeSourceAliasLocked(sourceKey [32]byte, alias publishedMediaSourceAlias) {
	delete(r.bySource, sourceKey)
	if entry := r.entries[alias.token]; entry != nil {
		delete(entry.sourceKeys, sourceKey)
	}
}

func (r *publishedMediaRegistry) expireLocked(now time.Time) {
	for token, entry := range r.entries {
		if !entry.ExpiresAt.After(now) && entry.active == 0 {
			r.removeLocked(token, entry)
		}
	}
}

func (r *publishedMediaRegistry) evictLRULocked() bool {
	var token string
	var oldest *publishedMediaEntry
	for candidate, entry := range r.entries {
		if entry.active != 0 {
			continue
		}
		if oldest == nil || entry.lastUsed < oldest.lastUsed {
			token, oldest = candidate, entry
		}
	}
	if oldest == nil {
		return false
	}
	r.removeLocked(token, oldest)
	return true
}

func (r *publishedMediaRegistry) removeLocked(token string, entry *publishedMediaEntry) {
	delete(r.entries, token)
	if r.byFingerprint[entry.fingerprint] == token {
		delete(r.byFingerprint, entry.fingerprint)
	}
	for sourceKey := range entry.sourceKeys {
		if alias := r.bySource[sourceKey]; alias.token == token {
			delete(r.bySource, sourceKey)
		}
	}
	r.bytes -= entry.Size
}

func publishedMediaFingerprint(contentType string, body []byte) [32]byte {
	h := sha256.New()
	_, _ = h.Write([]byte(contentType))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(body)
	var fingerprint [32]byte
	copy(fingerprint[:], h.Sum(nil))
	return fingerprint
}

func newPublishedMediaToken(entries map[string]*publishedMediaEntry) (string, error) {
	var raw [16]byte
	for attempts := 0; attempts < 4; attempts++ {
		if _, err := rand.Read(raw[:]); err != nil {
			return "", fmt.Errorf("generate public media token: %w", err)
		}
		token := hex.EncodeToString(raw[:])
		if entries[token] == nil {
			return token, nil
		}
	}
	return "", errors.New("could not allocate unique public media token")
}

func publishedMediaExtension(contentType string) (string, bool) {
	switch strings.ToLower(contentType) {
	case "image/jpeg":
		return "jpg", true
	case "image/png":
		return "png", true
	case "image/gif":
		return "gif", true
	case "image/webp":
		return "webp", true
	case "image/avif":
		return "avif", true
	case "video/mp4":
		return "mp4", true
	default:
		return "", false
	}
}

func residualPublicCacheControl(expiresAt, now time.Time) string {
	seconds := int64(expiresAt.Sub(now) / time.Second)
	if seconds < 1 {
		return ""
	}
	return fmt.Sprintf("public, max-age=%d, s-maxage=%d, must-revalidate, stale-if-error=0, no-transform", seconds, seconds)
}
