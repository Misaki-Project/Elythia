// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/elythia-network/elythia/plugin"
	"github.com/elythia-network/elythia/plugin/plugintest"
)

func allowPreviewPolicies() map[string]any {
	return map[string]any{
		"canUseFxTwitterPreview":      true,
		"fxTwitterColdFetchesPerHour": 100,
		"fxTwitterColdFetchesPerDay":  600,
	}
}

func testVideoMP4FTYP() []byte {
	return []byte{0, 0, 0, 16, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 0, 0}
}

func TestSelectNativeVideoSourceChoosesSafeBoundedMP4(t *testing.T) {
	got := selectVideoSource(fxVideo{
		Duration: 120,
		Formats: []fxVideoFormat{
			{URL: "https://video.twimg.com/low.mp4", Container: "mp4", Codec: "h264", Bitrate: 400_000},
			{URL: "https://video.twimg.com/high.mp4?tag=14", Container: "mp4", Codec: "avc1", Bitrate: 900_000},
			{URL: "https://video.twimg.com/list.m3u8", Container: "m3u8", Codec: "h264", Bitrate: 1_000_000},
			{URL: "https://evil.example/evil.mp4", Container: "mp4", Codec: "h264", Bitrate: 1_100_000},
			{URL: "https://video.twimg.com/too-large.mp4", Container: "mp4", Codec: "h264", Bitrate: nativeVideoMaxBitrate},
		},
	})
	if got.URL != "https://video.twimg.com/high.mp4?tag=14" {
		t.Fatalf("selected source = %q", got.URL)
	}
	for _, raw := range []string{
		"http://video.twimg.com/a.mp4", "https://video.twimg.com.evil.example/a.mp4",
		"https://user@video.twimg.com/a.mp4", "https://video.twimg.com:443/a.mp4",
		"https://video.twimg.com/a.m3u8", "https://evil.example/a.mp4",
	} {
		if _, ok := safeNativeVideoURL(raw); ok {
			t.Errorf("accepted unsafe video URL %q", raw)
		}
	}
}

func TestNativeVideoCacheKeepsSourcesPrivateAndResolvesOneMainOrQuoteIndex(t *testing.T) {
	s, now := testStore(t)
	s.cfg.VideoPlaybackMode = "nativeProxy"
	l, err := s.AcquireFetch(context.Background(), "1234567890", "u1", now)
	if err != nil || l == nil {
		t.Fatalf("lease = %#v, %v", l, err)
	}
	post := PublicPost{
		ID: l.PostID, Text: "main", Author: PublicAuthor{ID: "111111111", Name: "A", ScreenName: "a"}, HasVideo: true,
		Video:  &PublicVideo{Provider: "x"},
		Videos: []PublicVideoMetadata{{Provider: "x"}, {Provider: "x"}},
		Quote:  &PublicQuotedPost{ID: "2234567890", Text: "quote", Author: PublicAuthor{ID: "222222222", Name: "Q", ScreenName: "q"}, HasVideo: true, Video: &PublicVideo{Provider: "x"}, Videos: []PublicVideoMetadata{{Provider: "x"}}},
		privateVideoSources: privateVideoSources{Version: 1,
			Main:  []privateVideoSource{{URL: "https://video.twimg.com/main-0.mp4"}, {URL: "https://video.twimg.com/main-1.mp4"}},
			Quote: []privateVideoSource{{URL: "https://video.twimg.com/quote-0.mp4"}},
		},
	}
	if _, err := s.CompleteReady(context.Background(), *l, post, now); err != nil {
		t.Fatal(err)
	}
	entry, err := s.ReadPublic(context.Background(), l.PostID)
	if err != nil || entry == nil || entry.Post == nil || entry.PlaybackToken == "" {
		t.Fatalf("ready entry/capability = %#v, %v", entry, err)
	}
	if !entry.Post.Videos[0].PlaybackAvailable || !entry.Post.Videos[1].PlaybackAvailable || !entry.Post.Quote.Videos[0].PlaybackAvailable {
		t.Fatalf("video availability did not preserve positions: %#v / %#v", entry.Post.Videos, entry.Post.Quote.Videos)
	}
	for _, tc := range []struct {
		target string
		index  int
		want   string
	}{
		{"main", 1, "https://video.twimg.com/main-1.mp4"},
		{"quote", 0, "https://video.twimg.com/quote-0.mp4"},
	} {
		got, err := s.ResolveNativeVideoSource(context.Background(), l.PostID, entry.PlaybackToken, tc.target, tc.index, now)
		if err != nil || got != tc.want {
			t.Fatalf("ResolveNativeVideoSource(%s,%d) = %q, %v; want %q", tc.target, tc.index, got, err, tc.want)
		}
	}
	if _, err := s.ResolveNativeVideoSource(context.Background(), l.PostID, strings.Repeat("0", 32), "main", 0, now); err == nil {
		t.Fatal("accepted mismatched playback capability")
	}
	if _, err := s.ResolveNativeVideoSource(context.Background(), l.PostID, entry.PlaybackToken, "main", 4, now); err == nil {
		t.Fatal("accepted out-of-range video index")
	}
	if _, err := s.ResolveNativeVideoSource(context.Background(), l.PostID, entry.PlaybackToken, "quote", 1, now); err == nil {
		t.Fatal("accepted out-of-range quote video index")
	}
	if _, err := s.ResolveNativeVideoSource(context.Background(), l.PostID, entry.PlaybackToken, "main", 0, now.Add(time.Hour)); err == nil {
		t.Fatal("accepted expired cache capability")
	}
	publicEntry := publicEntryForMode(entry, "note1", "nativeProxy")
	if len(publicEntry.Post.Videos) != 2 || len(publicEntry.Post.Quote.Videos) != 1 || publicEntry.Post.Videos[0].PlaybackURL == "" || publicEntry.Post.Videos[1].PlaybackURL == "" || publicEntry.Post.Quote.Videos[0].PlaybackURL == "" {
		t.Fatalf("same-origin playback URLs missing: %#v", publicEntry.Post)
	}
	raw, err := json.Marshal(publicEntry)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"video.twimg.com", "_fxVideoSources", `"playbackToken"`} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("private source or standalone token leaked: %q in %s", secret, raw)
		}
	}
	var normalized string
	if err := s.db.QueryRow(`SELECT payload::text FROM x_post WHERE post_id='1234567890'`).Scan(&normalized); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(normalized, "video.twimg.com") || strings.Contains(normalized, "_fxVideoSources") {
		t.Fatalf("private video source entered normalized X post: %s", normalized)
	}
}

func TestVideoProxyLimiterDoesNotResetWindowForOutOfOrderTimestamp(t *testing.T) {
	limiter := newVideoProxyLimiter(3, 3)
	base := time.Date(2026, time.November, 14, 10, 0, 0, 0, time.UTC)
	for _, at := range []time.Time{base.Add(time.Minute), base, base.Add(time.Minute)} {
		if !limiter.Allow("same-user", at) {
			t.Fatalf("request at %s was rejected before quota exhausted", at)
		}
	}
	if limiter.Allow("same-user", base.Add(time.Minute)) {
		t.Fatal("out-of-order timestamp reset the user's current window")
	}
	if limiter.Allow("other-user", base.Add(time.Minute)) {
		t.Fatal("out-of-order timestamp reset the global window")
	}
}

func TestNativeVideoRouteRechecksPolicyAndVisibleNoteBeforeReturningBoundedMP4(t *testing.T) {
	db := migratedDB(t)
	now := time.Now().UTC()
	cfg := testConfig()
	cfg.VideoPlaybackMode = "nativeProxy"
	s := newStore(db, cfg)
	lease, err := s.AcquireFetch(context.Background(), "1234567890", "u1", now)
	if err != nil || lease == nil {
		t.Fatalf("lease=%#v err=%v", lease, err)
	}
	post := PublicPost{
		ID: lease.PostID, Text: "video", Author: PublicAuthor{ID: "111111111", Name: "A", ScreenName: "a"}, HasVideo: true,
		Video: &PublicVideo{Provider: "x"}, Videos: []PublicVideoMetadata{{Provider: "x"}},
		privateVideoSources: privateVideoSources{Version: 1, Main: []privateVideoSource{{URL: "https://video.twimg.com/clip.mp4?tag=14"}}},
	}
	if _, err := s.CompleteReady(context.Background(), *lease, post, now); err != nil {
		t.Fatal(err)
	}
	entry, err := s.ReadPublic(context.Background(), post.ID)
	if err != nil || entry == nil || entry.PlaybackToken == "" {
		t.Fatalf("cache entry=%#v err=%v", entry, err)
	}
	api := &stubAPI{body: `{"id":"note1","text":"https://x.com/a/status/1234567890"}`}
	var outbound int
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		outbound++
		if r.URL.Host != "video.twimg.com" {
			t.Fatalf("video fetch escaped allowlisted host: %s", r.URL.Host)
		}
		return response(http.StatusOK, "video/mp4", string(testVideoMP4FTYP())), nil
	})}
	config := map[string]any{"externalFetchEnabled": true, "videoPlaybackMode": "nativeProxy"}
	routes := plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).WithAPI(api).WithHTTPClient(client).WithConfig(config).Routes(Plugin)
	request := plugintest.Request{
		UserID:              "u1",
		Params:              map[string]string{"postId": post.ID, "target": "main", "index": "0"},
		Query:               map[string]string{"noteId": "note1", "token": entry.PlaybackToken},
		CheckedRolePolicies: allowPreviewPolicies(),
	}
	result, err := routes.Call(t, "GET /media/video/:postId/:target/:index", request)
	if err != nil {
		t.Fatal(err)
	}
	blob, ok := result.(plugin.Blob)
	if !ok || blob.ContentType != "video/mp4" || blob.CacheControl != "private, no-store" || blob.WriteTimeout != mediaResponseWriteTimeout || string(blob.Body) != string(testVideoMP4FTYP()) {
		t.Fatalf("video response = %#v", result)
	}
	request.CheckedRolePolicies = map[string]any{"canUseFxTwitterPreview": false, "fxTwitterColdFetchesPerHour": 100, "fxTwitterColdFetchesPerDay": 600}
	if _, err := routes.Call(t, "GET /media/video/:postId/:target/:index", request); err == nil {
		t.Fatal("denied checked role policy still served video")
	}
	request.CheckedRolePolicies = allowPreviewPolicies()
	request.Params["target"] = "quote"
	request.Params["index"] = "1"
	if _, err := routes.Call(t, "GET /media/video/:postId/:target/:index", request); err == nil {
		t.Fatal("video route accepted an unavailable quote index")
	}
	request.Params["target"] = "main"
	request.Params["index"] = "0"
	api.body = `{"id":"note1","text":"https://x.com/a/status/9999999999"}`
	if _, err := routes.Call(t, "GET /media/video/:postId/:target/:index", request); err == nil {
		t.Fatal("video route accepted a mismatched visible-note link")
	}
	api.body = `{"id":"other-note","text":"https://x.com/a/status/1234567890"}`
	if _, err := routes.Call(t, "GET /media/video/:postId/:target/:index", request); err == nil {
		t.Fatal("video route accepted a note ID mismatch")
	}
	api.body = `{"id":"note1","text":"https://x.com/a/status/1234567890"}`
	request.Query["token"] = strings.Repeat("0", 32)
	if _, err := routes.Call(t, "GET /media/video/:postId/:target/:index", request); err == nil {
		t.Fatal("video route accepted a mismatched capability")
	}
	request.Query["token"] = entry.PlaybackToken
	api.err = errors.New("private note")
	if _, err := routes.Call(t, "GET /media/video/:postId/:target/:index", request); err == nil {
		t.Fatal("video route accepted an inaccessible note")
	}
	if outbound != 1 {
		t.Fatalf("unauthorized playback made outbound requests: %d", outbound-1)
	}
}
