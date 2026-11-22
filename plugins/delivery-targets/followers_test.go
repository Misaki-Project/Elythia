package deliverytargets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elythia-network/elythia/plugin"
	"github.com/elythia-network/elythia/plugin/plugintest"
)

// fakeFollowersAPI serves users/followers from a fixed list (newest first)
// and federation/show-instance from a map of host states.
type fakeFollowersAPI struct {
	// hosts lists each follower's host, newest first; "" is a local follower.
	hosts      []string
	states     map[string]any
	stateErr   map[string]bool
	followErr  error
	calls      map[string]int
	asUser     []string
	pageParams []map[string]any
}

func (f *fakeFollowersAPI) Anonymous() plugin.Caller { return f }

func (f *fakeFollowersAPI) AsUser(id string) plugin.Caller {
	f.asUser = append(f.asUser, id)
	return f
}

func (f *fakeFollowersAPI) Call(_ context.Context, endpoint string, params any) (json.RawMessage, error) {
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[endpoint]++
	p := params.(map[string]any)
	switch endpoint {
	case "users/followers":
		f.pageParams = append(f.pageParams, p)
		if f.followErr != nil {
			return nil, f.followErr
		}
		start := 0
		if until, ok := p["untilId"].(string); ok {
			n, _ := strconv.Atoi(until)
			start = n + 1
		}
		limit := p["limit"].(int)
		var page []map[string]any
		for i := start; i < len(f.hosts) && len(page) < limit; i++ {
			var host any
			if f.hosts[i] != "" {
				host = f.hosts[i]
			}
			page = append(page, map[string]any{"id": strconv.Itoa(i), "follower": map[string]any{"host": host}})
		}
		if page == nil {
			page = []map[string]any{}
		}
		return json.Marshal(page)
	case "federation/show-instance":
		host := p["host"].(string)
		if f.stateErr[host] {
			return nil, errors.New("boom")
		}
		st, ok := f.states[host]
		if !ok {
			return json.RawMessage("null"), nil
		}
		return json.Marshal(st)
	}
	return nil, fmt.Errorf("unexpected endpoint %s", endpoint)
}

func repeatHost(host string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = host
	}
	return out
}

func callFollowerHosts(t *testing.T, api *fakeFollowersAPI, userID string) (FollowerHosts, error) {
	t.Helper()
	handlers := plugintest.New(t).WithName(Name).WithAPI(api).Routes(Plugin)
	got, err := handlers.Call(t, "POST /follower-hosts", plugintest.Request{UserID: userID})
	if err != nil {
		return FollowerHosts{}, err
	}
	v, ok := got.(FollowerHosts)
	require.True(t, ok, "response is %T", got)
	return v, nil
}

func TestFollowerHosts_CountsRemoteFollowersPerServer(t *testing.T) {
	hosts := append(repeatHost("b.example", 3), "", "A.Example", "a.example", "c.example")
	api := &fakeFollowersAPI{hosts: hosts}
	got, err := callFollowerHosts(t, api, "me")
	require.NoError(t, err)
	assert.Equal(t, []FollowerHost{
		{Host: "b.example", Count: 3},
		{Host: "a.example", Count: 2},
		{Host: "c.example", Count: 1},
	}, got.Hosts)
	assert.False(t, got.Truncated)
	assert.Equal(t, []string{"me"}, api.asUser, "calls as the signed-in user")
	assert.Equal(t, "me", api.pageParams[0]["userId"])
}

func TestFollowerHosts_LeavesOutUnreachableServers(t *testing.T) {
	api := &fakeFollowersAPI{
		hosts: []string{"ok.example", "down.example", "suspended.example", "blocked.example", "unknown.example", "err.example"},
		states: map[string]any{
			"ok.example":        map[string]any{"isNotResponding": false, "isSuspended": false, "isBlocked": false},
			"down.example":      map[string]any{"isNotResponding": true},
			"suspended.example": map[string]any{"isSuspended": true},
			"blocked.example":   map[string]any{"isBlocked": true},
		},
		stateErr: map[string]bool{"err.example": true},
	}
	got, err := callFollowerHosts(t, api, "me")
	require.NoError(t, err)
	var names []string
	for _, h := range got.Hosts {
		names = append(names, h.Host)
	}
	// 状態を読めないもの (unknown / err) は残す。
	assert.ElementsMatch(t, []string{"ok.example", "unknown.example", "err.example"}, names)
}

func TestFollowerHosts_PagesAndStopsAtTheCap(t *testing.T) {
	// ちょうど 1 ページ分 (followerPageSize) だと、次のページを読んで空で終わる。
	api := &fakeFollowersAPI{hosts: repeatHost("a.example", followerPageSize)}
	got, err := callFollowerHosts(t, api, "me")
	require.NoError(t, err)
	assert.Equal(t, []FollowerHost{{Host: "a.example", Count: followerPageSize}}, got.Hosts)
	assert.False(t, got.Truncated)
	assert.Equal(t, 2, api.calls["users/followers"])
	assert.Equal(t, strconv.Itoa(followerPageSize-1), api.pageParams[1]["untilId"])

	api = &fakeFollowersAPI{hosts: repeatHost("a.example", maxFollowersRead+1)}
	got, err = callFollowerHosts(t, api, "me")
	require.NoError(t, err)
	assert.Equal(t, maxFollowersRead, got.Hosts[0].Count)
	assert.True(t, got.Truncated)
	assert.Equal(t, maxFollowersRead/followerPageSize, api.calls["users/followers"])
}

func TestFollowerHosts_CapsTheServers(t *testing.T) {
	hosts := make([]string, 0, maxFollowerHosts+1)
	for i := 0; i <= maxFollowerHosts; i++ {
		hosts = append(hosts, fmt.Sprintf("h%03d.example", i))
	}
	hosts = append(hosts, "h000.example") // h000 が 2 人で先頭に来る
	api := &fakeFollowersAPI{hosts: hosts}
	got, err := callFollowerHosts(t, api, "me")
	require.NoError(t, err)
	require.Len(t, got.Hosts, maxFollowerHosts)
	assert.Equal(t, FollowerHost{Host: "h000.example", Count: 2}, got.Hosts[0])
	assert.Equal(t, "h001.example", got.Hosts[1].Host, "ties are ordered by host")
	assert.True(t, got.Truncated)
}

func TestFollowerHosts_RequiresSignIn(t *testing.T) {
	_, err := callFollowerHosts(t, &fakeFollowersAPI{}, "")
	var se *plugin.StatusError
	require.ErrorAs(t, err, &se)
	assert.Equal(t, http.StatusUnauthorized, se.Status)
}

func TestFollowerHosts_ReportsAPIErrors(t *testing.T) {
	_, err := callFollowerHosts(t, &fakeFollowersAPI{followErr: errors.New("boom")}, "me")
	require.ErrorContains(t, err, "users/followers")
}

func TestFollowerHosts_RejectsAnUnexpectedBody(t *testing.T) {
	_, err := listFollowerHosts(context.Background(), rawCaller(`{"not":"a list"}`), "me")
	require.ErrorContains(t, err, "users/followers")
}

func TestFollowerHosts_KeepsAServerWhoseStateIsUnreadable(t *testing.T) {
	got := dropUnreachable(context.Background(), rawCaller(`[1]`), slog.Default(), []FollowerHost{{Host: "a.example", Count: 1}})
	assert.Equal(t, []FollowerHost{{Host: "a.example", Count: 1}}, got)
}

func TestFollowerHosts_KeepsAServerTheInstanceDoesNotKnow(t *testing.T) {
	// federation/show-instance は、知らないサーバーには本文を返さない。
	got := dropUnreachable(context.Background(), rawCaller(""), slog.Default(), []FollowerHost{{Host: "a.example", Count: 1}})
	assert.Equal(t, []FollowerHost{{Host: "a.example", Count: 1}}, got)
}

func TestFollowerHosts_SkipsHostsThatDoNotNormalize(t *testing.T) {
	got, err := listFollowerHosts(context.Background(), rawCaller(`[{"id":"1","follower":{"host":"a b"}},{"id":"2","follower":null}]`), "me")
	require.NoError(t, err)
	assert.Empty(t, got.Hosts)
}

type rawCaller string

func (r rawCaller) Call(context.Context, string, any) (json.RawMessage, error) {
	return json.RawMessage(r), nil
}

func TestFollowerHosts_ReusesTheListPerUser(t *testing.T) {
	api := &fakeFollowersAPI{hosts: []string{"a.example"}}
	handlers := plugintest.New(t).WithName(Name).WithAPI(api).Routes(Plugin)
	for range 2 {
		_, err := handlers.Call(t, "POST /follower-hosts", plugintest.Request{UserID: "me"})
		require.NoError(t, err)
	}
	assert.Equal(t, 1, api.calls["users/followers"], "the second call is served from the cache")
	_, err := handlers.Call(t, "POST /follower-hosts", plugintest.Request{UserID: "other"})
	require.NoError(t, err)
	assert.Equal(t, 2, api.calls["users/followers"], "each user has their own list")
}

func TestFollowerHostsCache_ExpiresAndStaysBounded(t *testing.T) {
	now := time.Unix(0, 0)
	c := newFollowerHostsCache()
	c.now = func() time.Time { return now }
	c.put("u", FollowerHosts{Truncated: true})
	got, ok := c.get("u")
	require.True(t, ok)
	assert.True(t, got.Truncated)

	now = now.Add(followerHostsTTL)
	_, ok = c.get("u")
	assert.False(t, ok, "expired")

	// 上限に達したら、期限切れを捨てる。
	for i := range maxCachedUsers {
		c.put(strconv.Itoa(i), FollowerHosts{})
	}
	now = now.Add(followerHostsTTL)
	c.put("fresh", FollowerHosts{})
	assert.Len(t, c.entries, 1)

	// 期限切れが無くても上限は超えない。
	for i := range maxCachedUsers {
		c.put(strconv.Itoa(i), FollowerHosts{})
	}
	assert.LessOrEqual(t, len(c.entries), maxCachedUsers)
	_, ok = c.get(strconv.Itoa(maxCachedUsers - 1))
	assert.True(t, ok)
}
