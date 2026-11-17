package deliverytargets

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/elythia-network/elythia/plugin"
)

const (
	// followerPageSize is the most users/followers returns in one call.
	followerPageSize = 100
	// maxFollowersRead caps the followers read for one list. フォロワーが多い
	// 利用者でも、一覧を開くたびに本体の API を際限なく呼ばない。
	maxFollowersRead = 5000
	// maxFollowerHosts caps the servers returned (most followers first). 1 台
	// ごとに federation/show-instance を呼ぶので、ここでも上限を置く。
	maxFollowerHosts = 200
	// followerHostsTTL is how long a user's list is reused. プラグインのルート
	// には本体のレート制限が掛からないので、結果を持って呼び出しを抑える。
	followerHostsTTL = 5 * time.Minute
	// maxCachedUsers bounds the cache. 超えたら期限切れを捨て、それでも多ければ
	// 全部捨てる (作り直すだけなので、正しさには関わらない)。
	maxCachedUsers = 1000
)

// FollowerHost is one remote server where the user has followers.
type FollowerHost struct {
	Host  string `json:"host"`
	Count int    `json:"count"`
}

// FollowerHosts is the response of POST /follower-hosts.
type FollowerHosts struct {
	Hosts []FollowerHost `json:"hosts"`
	// Truncated is true when the list stopped at maxFollowersRead followers
	// or maxFollowerHosts servers, so some servers may be missing.
	Truncated bool `json:"truncated"`
}

type followerHostsEntry struct {
	value   FollowerHosts
	expires time.Time
}

// followerHostsCache keeps each user's list for followerHostsTTL.
type followerHostsCache struct {
	mu      sync.Mutex
	entries map[string]followerHostsEntry
	now     func() time.Time
}

func newFollowerHostsCache() *followerHostsCache {
	return &followerHostsCache{entries: map[string]followerHostsEntry{}, now: time.Now}
}

func (c *followerHostsCache) get(userID string) (FollowerHosts, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[userID]
	if !ok || !c.now().Before(e.expires) {
		return FollowerHosts{}, false
	}
	return e.value, true
}

func (c *followerHostsCache) put(userID string, v FollowerHosts) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if len(c.entries) >= maxCachedUsers {
		for id, e := range c.entries {
			if !now.Before(e.expires) {
				delete(c.entries, id)
			}
		}
		if len(c.entries) >= maxCachedUsers {
			c.entries = map[string]followerHostsEntry{}
		}
	}
	c.entries[userID] = followerHostsEntry{value: v, expires: now.Add(followerHostsTTL)}
}

// followerHostsHandler returns the remote servers where the caller has
// followers, most followers first, leaving out servers that do not respond,
// are suspended or are blocked.
//
// 配送が届くのはフォロワーのいるサーバーだけなので、配送先を選ぶときの一覧に
// する。本体の`users/followers`をその利用者として呼んで数える (本体のコードには
// 手を入れない。可視性もその利用者のものが効く)。
func followerHostsHandler(api plugin.API, logger *slog.Logger, cache *followerHostsCache) plugin.Handler {
	return func(req plugin.Request) (any, error) {
		userID := req.UserID()
		if userID == "" {
			return nil, plugin.Errorf(http.StatusUnauthorized, "sign in to list the servers of your followers")
		}
		if v, ok := cache.get(userID); ok {
			return v, nil
		}
		caller := api.AsUser(userID)
		v, err := listFollowerHosts(req.Context(), caller, userID)
		if err != nil {
			return nil, err
		}
		v.Hosts = dropUnreachable(req.Context(), caller, logger, v.Hosts)
		cache.put(userID, v)
		return v, nil
	}
}

type followerItem struct {
	ID       string `json:"id"`
	Follower *struct {
		Host *string `json:"host"`
	} `json:"follower"`
}

// listFollowerHosts counts the caller's remote followers per server.
func listFollowerHosts(ctx context.Context, caller plugin.Caller, userID string) (FollowerHosts, error) {
	counts := map[string]int{}
	read := 0
	untilID := ""
	truncated := false
	for {
		params := map[string]any{"userId": userID, "limit": followerPageSize}
		if untilID != "" {
			params["untilId"] = untilID
		}
		raw, err := caller.Call(ctx, "users/followers", params)
		if err != nil {
			return FollowerHosts{}, fmt.Errorf("users/followers: %w", err)
		}
		var page []followerItem
		if err := json.Unmarshal(raw, &page); err != nil {
			return FollowerHosts{}, fmt.Errorf("users/followers: %w", err)
		}
		for _, item := range page {
			read++
			if item.Follower == nil || item.Follower.Host == nil {
				continue // ローカルのフォロワーには配送しない
			}
			host, err := NormalizeHost(*item.Follower.Host)
			if err != nil {
				continue
			}
			counts[host]++
		}
		if len(page) < followerPageSize {
			break
		}
		if read >= maxFollowersRead {
			truncated = true
			break
		}
		untilID = page[len(page)-1].ID
	}

	hosts := make([]FollowerHost, 0, len(counts))
	for h, n := range counts {
		hosts = append(hosts, FollowerHost{Host: h, Count: n})
	}
	sort.Slice(hosts, func(i, j int) bool {
		if hosts[i].Count != hosts[j].Count {
			return hosts[i].Count > hosts[j].Count
		}
		return hosts[i].Host < hosts[j].Host
	})
	if len(hosts) > maxFollowerHosts {
		hosts = hosts[:maxFollowerHosts]
		truncated = true
	}
	return FollowerHosts{Hosts: hosts, Truncated: truncated}, nil
}

type instanceState struct {
	IsNotResponding bool `json:"isNotResponding"`
	IsSuspended     bool `json:"isSuspended"`
	IsBlocked       bool `json:"isBlocked"`
}

// unreachable reports whether a server should be left out of the choices.
func (s instanceState) unreachable() bool {
	return s.IsNotResponding || s.IsSuspended || s.IsBlocked
}

// dropUnreachable leaves out servers that do not respond, are suspended or
// are blocked.
//
// 一覧は選ぶのを助けるためのもので、配送の可否は本体が決める。状態を読めない
// ときは外さずに残す (外すと、届く相手を選べなくなる)。
func dropUnreachable(ctx context.Context, caller plugin.Caller, logger *slog.Logger, hosts []FollowerHost) []FollowerHost {
	out := make([]FollowerHost, 0, len(hosts))
	for _, h := range hosts {
		raw, err := caller.Call(ctx, "federation/show-instance", map[string]any{"host": h.Host})
		if err != nil {
			logger.Warn("delivery-targets: federation/show-instance failed", "host", h.Host, "error", err)
			out = append(out, h)
			continue
		}
		// 知らないサーバーには本文が空で返る (null と同じ)。
		if len(bytes.TrimSpace(raw)) == 0 {
			out = append(out, h)
			continue
		}
		var st *instanceState
		if err := json.Unmarshal(raw, &st); err != nil {
			logger.Warn("delivery-targets: federation/show-instance returned an unexpected body", "host", h.Host, "error", err)
			out = append(out, h)
			continue
		}
		if st != nil && st.unreachable() {
			continue
		}
		out = append(out, h)
	}
	return out
}
