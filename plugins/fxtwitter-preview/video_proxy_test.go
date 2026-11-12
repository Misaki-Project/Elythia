// SPDX-FileCopyrightText: syuilo and misskey-project
// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/elythia-network/elythia/plugin"
	"github.com/elythia-network/elythia/plugin/plugintest"
)

func TestParseFxResponseKeepsVideoSourcesPositionalAndPrivate(t *testing.T) {
	body := []byte(`{"code":200,"status":{"id":"1234567890","text":"main","author":{"name":"a","screen_name":"b"},"media":{"videos":[{"thumbnail_url":"https://pbs.twimg.com/0.jpg","duration":8,"formats":[{"url":"https://video.twimg.com/amplify_video/a/vid/avc1/320x568/clip.mp4?tag=14","container":"mp4","codec":"h264","bitrate":632000},{"url":"https://video.twimg.com/amplify_video/a/vid/avc1/playlist.m3u8","container":"m3u8"}]},{"thumbnail_url":"https://pbs.twimg.com/1.jpg","duration":9,"formats":[{"url":"https://evil.example/clip.mp4","container":"mp4","bitrate":100000}]}]},"quote":{"id":"2234567890","text":"quote","author":{"name":"q","screen_name":"c"},"media":{"videos":[{"thumbnail_url":"https://pbs.twimg.com/q.jpg","duration":7,"formats":[{"url":"https://video.twimg.com/amplify_video/q/vid/avc1/480x852/clip.mp4","container":"mp4","bitrate":950000}]}]}}}}`)
	post, err := parseFxResponse(body, "1234567890")
	if err != nil {
		t.Fatal(err)
	}
	if len(post.privateVideoSources.Main) != 2 || post.privateVideoSources.Main[0].URL == "" || post.privateVideoSources.Main[1].URL != "" {
		t.Fatalf("main source slots shifted or accepted unsafe URL: %#v", post.privateVideoSources.Main)
	}
	if post.Quote == nil || len(post.privateVideoSources.Quote) != 1 || post.privateVideoSources.Quote[0].URL == "" {
		t.Fatalf("quoted source slots missing: %#v", post.privateVideoSources.Quote)
	}
	publicJSON, err := json.Marshal(post)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"video.twimg.com", "evil.example", "_fxVideoSources"} {
		if strings.Contains(string(publicJSON), secret) {
			t.Fatalf("private source %q leaked to public model: %s", secret, publicJSON)
		}
	}
}

func TestNativeVideoProxyCacheKeepsSourcesPrivateAndSelectsExactMainQuoteIndex(t *testing.T) {
	s, now := testStore(t)
	s.cfg.VideoPlaybackMode = "nativeProxy"
	l, err := s.AcquireFetch(context.Background(), "1234567890", "u1", now)
	if err != nil || l == nil {
		t.Fatalf("lease = %#v, %v", l, err)
	}
	post := PublicPost{
		ID: l.PostID, Text: "two videos", Author: PublicAuthor{ID: "111111111", Name: "A", ScreenName: "a"}, HasVideo: true,
		Video:               &PublicVideo{Provider: "x"},
		Videos:              []PublicVideoMetadata{{Provider: "x"}, {Provider: "x"}},
		Quote:               &PublicQuotedPost{ID: "2234567890", Text: "quoted", Author: PublicAuthor{ID: "222222222", Name: "Q", ScreenName: "q"}, HasVideo: true, Video: &PublicVideo{Provider: "x"}, Videos: []PublicVideoMetadata{{Provider: "x"}}},
		privateVideoSources: privateVideoSources{Version: 1, Main: []privateVideoSource{{URL: "https://video.twimg.com/main-0.mp4"}, {URL: "https://video.twimg.com/main-1.mp4"}}, Quote: []privateVideoSource{{URL: "https://video.twimg.com/quote-0.mp4"}}},
	}
	if _, err := s.CompleteReady(context.Background(), *l, post, now); err != nil {
		t.Fatal(err)
	}
	entry, err := s.ReadPublic(context.Background(), l.PostID)
	if err != nil || entry == nil || entry.Post == nil || entry.Post.Video == nil || entry.Post.Video.EmbedToken == "" {
		t.Fatalf("public cache/capability = %#v, %v", entry, err)
	}
	if !entry.Post.Videos[0].PlaybackAvailable || !entry.Post.Videos[1].PlaybackAvailable || !entry.Post.Quote.Videos[0].PlaybackAvailable {
		t.Fatalf("playback availability not aligned: %#v %#v", entry.Post.Videos, entry.Post.Quote.Videos)
	}
	for _, secret := range []string{"video.twimg.com", "_fxVideoSources"} {
		if strings.Contains(mustJSON(t, entry.Post), secret) {
			t.Fatalf("private source %q leaked in DTO: %s", secret, mustJSON(t, entry.Post))
		}
	}
	for _, tc := range []struct {
		target string
		index  int
		want   string
	}{
		{"main", 1, "https://video.twimg.com/main-1.mp4"},
		{"quote", 0, "https://video.twimg.com/quote-0.mp4"},
	} {
		got, err := s.ResolveNativeVideoSource(context.Background(), l.PostID, entry.Post.Video.EmbedToken, tc.target, tc.index, now)
		if err != nil || got != tc.want {
			t.Fatalf("ResolveNativeVideoSource(%s,%d) = %q, %v; want %q", tc.target, tc.index, got, err, tc.want)
		}
	}
	for _, tc := range []struct {
		target string
		index  int
	}{{"main", 2}, {"other", 0}, {"quote", 1}} {
		if _, err := s.ResolveNativeVideoSource(context.Background(), l.PostID, entry.Post.Video.EmbedToken, tc.target, tc.index, now); err == nil {
			t.Fatalf("accepted target/index %q/%d", tc.target, tc.index)
		}
	}
	if _, err := s.ResolveNativeVideoSource(context.Background(), l.PostID, strings.Repeat("0", 32), "main", 0, now); err == nil {
		t.Fatal("accepted mismatched token")
	}
	if _, err := s.ResolveNativeVideoSource(context.Background(), l.PostID, entry.Post.Video.EmbedToken, "main", 0, now.Add(time.Hour)); err == nil {
		t.Fatal("accepted expired capability/cache")
	}
	var normalized string
	if err := s.db.QueryRow(`SELECT payload::text FROM x_post WHERE post_id='1234567890'`).Scan(&normalized); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(normalized, "video.twimg.com") || strings.Contains(normalized, "_fxVideoSources") {
		t.Fatalf("private source entered normalized x_post: %s", normalized)
	}
}

func TestFetchVideoRejectsRedirectOversizeWrongMIMEAndInvalidFTYP(t *testing.T) {
	cases := []struct {
		name     string
		response *http.Response
	}{
		{"redirect", &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://evil.example/video.mp4"}}, Body: http.NoBody}},
		{"oversize", response(http.StatusOK, "video/mp4", string(append(mp4FTYP(), make([]byte, nativeVideoMaxBytes)...)))},
		{"wrong-mime", response(http.StatusOK, "application/octet-stream", string(mp4FTYP()))},
		{"invalid-ftyp", response(http.StatusOK, "video/mp4", "not-an-mp4-file")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Header.Get("Range") != "" {
					t.Fatalf("unexpected Range header: %q", r.Header.Get("Range"))
				}
				return tc.response, nil
			})}
			_, release, err := fetchVideoWithAdmission(context.Background(), client, "https://video.twimg.com/amplify_video/a/vid/avc1/720x1280/clip.mp4?tag=14", nativeVideoMaxBytes, newMediaAdmission(1, 1, nativeVideoMaxBytes+1))
			if err == nil {
				t.Fatal("invalid response accepted")
			}
			if release != nil {
				release()
			}
			if calls != 1 {
				t.Fatalf("outbound request count = %d", calls)
			}
		})
	}
}

func TestFetchVideoReturnsBoundedNoStoreMP4(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "video.twimg.com" || r.Header.Get("Range") != "" {
			t.Fatalf("request leaked range or used wrong host: %#v", r)
		}
		return response(http.StatusOK, "video/mp4", string(mp4FTYP())), nil
	})}
	blob, release, err := fetchVideoWithAdmission(context.Background(), client, "https://video.twimg.com/clip.mp4?tag=14", nativeVideoMaxBytes, newMediaAdmission(1, 1, nativeVideoMaxBytes+1))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if calls != 1 || blob.ContentType != "video/mp4" || blob.CacheControl != "private, no-store" || string(blob.Body) != string(mp4FTYP()) {
		t.Fatalf("video blob = %#v, calls=%d", blob, calls)
	}
}

func TestNativeMetadataRefreshUsesColdBudgetAndMarksUnsupportedSlots(t *testing.T) {
	s, now := testStore(t)
	first, err := s.AcquireFetch(context.Background(), "1234567890", "u1", now)
	if err != nil || first == nil {
		t.Fatalf("initial lease=%#v %v", first, err)
	}
	old := PublicPost{ID: first.PostID, Text: "kept", Author: PublicAuthor{Name: "A", ScreenName: "a"}, Quote: &PublicQuotedPost{ID: "2234567890", Text: "quoted video", Author: PublicAuthor{Name: "Q", ScreenName: "q"}, HasVideo: true, Video: &PublicVideo{Provider: "x"}, Videos: []PublicVideoMetadata{{Provider: "x"}}}}
	if _, err := s.CompleteReady(context.Background(), *first, old, now); err != nil {
		t.Fatal(err)
	}
	if needed, err := s.NeedsNativeVideoMetadata(context.Background(), first.PostID); err != nil || !needed {
		t.Fatalf("legacy cache metadata missing=%v err=%v", needed, err)
	}
	s.cfg.VideoPlaybackMode = "nativeProxy"
	budget := coldFetchBudget{PerHour: 10, PerDay: 10}
	refresh, err := s.AcquireNativeMetadataRefreshWithBudget(context.Background(), first.PostID, "u1", budget, now.Add(time.Minute))
	if err != nil || refresh == nil {
		t.Fatalf("budgeted upgrade lease=%#v %v", refresh, err)
	}
	unsupported := PublicPost{ID: first.PostID, Text: "kept", Author: old.Author, Quote: &PublicQuotedPost{ID: "2234567890", Text: "quoted video", Author: old.Quote.Author, HasVideo: true, Video: &PublicVideo{Provider: "x"}, Videos: []PublicVideoMetadata{{Provider: "x"}}}, privateVideoSources: privateVideoSources{Version: 1, Quote: []privateVideoSource{{}}}}
	if _, err := s.CompleteReady(context.Background(), *refresh, unsupported, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if needed, err := s.NeedsNativeVideoMetadata(context.Background(), first.PostID); err != nil || needed {
		t.Fatalf("unsupported fresh cache requeued forever: needed=%v err=%v", needed, err)
	}
	entry, err := s.ReadPublic(context.Background(), first.PostID)
	if err != nil || entry.Post.Quote.Videos[0].PlaybackAvailable || entry.Post.Quote.Video.EmbedToken != "" {
		t.Fatalf("unsupported video exposed a capability: %#v %v", entry, err)
	}
	// A nativeProxy configuration must not spend quota to upgrade YouTube-only
	// caches, which continue to use the official YouTube iframe path.
	youtube, err := s.AcquireFetch(context.Background(), "3234567890", "u1", now.Add(2*time.Minute))
	if err != nil || youtube == nil {
		t.Fatalf("youtube seed lease=%#v %v", youtube, err)
	}
	yt := PublicPost{ID: youtube.PostID, Text: "yt", Author: PublicAuthor{Name: "Y", ScreenName: "y"}, HasVideo: true, Video: &PublicVideo{Provider: "youtube", ProviderVideoID: "dQw4w9WgXcQ"}}
	if _, err := s.CompleteReady(context.Background(), *youtube, yt, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if needed, err := s.NeedsNativeVideoMetadata(context.Background(), youtube.PostID); err != nil || needed {
		t.Fatalf("YouTube cache queued native source upgrade: needed=%v err=%v", needed, err)
	}
	if upgrade, err := s.AcquireNativeMetadataRefreshWithBudget(context.Background(), youtube.PostID, "u1", budget, now.Add(3*time.Minute)); err != nil || upgrade != nil {
		t.Fatalf("YouTube cache got an upgrade lease: %#v %v", upgrade, err)
	}
	var budgetCount int
	if err := s.db.QueryRow(`SELECT count FROM cold_fetch_budget WHERE scope='user-hour:u1' AND bucket_start=$1`, now.Add(time.Minute).Truncate(time.Hour)).Scan(&budgetCount); err != nil || budgetCount != 3 {
		t.Fatalf("cold budget count=%d err=%v; want initial fetch, authorized refresh, and separate YouTube cache seed", budgetCount, err)
	}
}

func TestPreviewBudgetedlyUpgradesFreshQuoteOnlyXCache(t *testing.T) {
	db := migratedDB(t)
	now := time.Now().UTC()
	cfg := testConfig()
	oldStore := newStore(db, cfg)
	l, err := oldStore.AcquireFetch(context.Background(), "1234567890", "seed", now)
	if err != nil || l == nil {
		t.Fatalf("seed lease=%#v %v", l, err)
	}
	old := PublicPost{ID: l.PostID, Text: "quoted", Author: PublicAuthor{Name: "A", ScreenName: "a"}, Quote: &PublicQuotedPost{ID: "2234567890", Text: "x video", Author: PublicAuthor{Name: "Q", ScreenName: "q"}, HasVideo: true, Video: &PublicVideo{Provider: "x"}, Videos: []PublicVideoMetadata{{Provider: "x"}}}}
	if _, err := oldStore.CompleteReady(context.Background(), *l, old, now); err != nil {
		t.Fatal(err)
	}
	a := &stubAPI{body: `{"id":"note1","text":"https://x.com/a/status/1234567890"}`}
	h := plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).WithAPI(a).WithConfig(map[string]any{"externalFetchEnabled": true, "videoPlaybackMode": "nativeProxy"})
	routes := h.Routes(Plugin)
	result, err := routes.Call(t, "POST /preview", plugintest.Request{UserID: "u1", Body: `{"noteId":"note1","postId":"1234567890"}`, CheckedRolePolicies: allowPreviewPolicies()})
	if err != nil {
		t.Fatal(err)
	}
	if result.(map[string]any)["state"] != "pending" || len(h.Enqueues()) != 1 {
		t.Fatalf("fresh quote-only X cache was not budgetedly queued: result=%#v enqueues=%v", result, h.Enqueues())
	}
	var count int
	if err := db.QueryRow(`SELECT count FROM cold_fetch_budget WHERE scope='user-hour:u1' AND bucket_start=$1`, now.Truncate(time.Hour)).Scan(&count); err != nil || count != 1 {
		t.Fatalf("cold fetch budget count=%d err=%v; want one authorized metadata refresh", count, err)
	}
}

func TestFailedNativeMetadataRefreshRetainsOnlyTrustedStalePublicData(t *testing.T) {
	s, now := testStore(t)
	first, err := s.AcquireFetch(context.Background(), "1234567890", "u1", now)
	if err != nil || first == nil {
		t.Fatalf("initial lease=%#v %v", first, err)
	}
	old := PublicPost{ID: first.PostID, Text: "trusted old text", Author: PublicAuthor{Name: "A", ScreenName: "a"}, HasVideo: true, Video: &PublicVideo{Provider: "x"}, Videos: []PublicVideoMetadata{{Provider: "x"}}}
	if _, err := s.CompleteReady(context.Background(), *first, old, now); err != nil {
		t.Fatal(err)
	}
	s.cfg.VideoPlaybackMode = "nativeProxy"
	refresh, err := s.AcquireNativeMetadataRefreshWithBudget(context.Background(), first.PostID, "u1", coldFetchBudget{PerHour: 10, PerDay: 10}, now.Add(time.Minute))
	if err != nil || refresh == nil {
		t.Fatalf("metadata refresh lease=%#v %v", refresh, err)
	}
	if _, err := s.CompleteUnavailable(context.Background(), *refresh, "network", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	entry, err := s.ReadPublic(context.Background(), first.PostID)
	if err != nil || entry.State != "stale" || entry.Post == nil || entry.Post.Text != "trusted old text" {
		t.Fatalf("stale public fallback=%#v err=%v", entry, err)
	}
	if strings.Contains(mustJSON(t, entry.Post), "video.twimg.com") || strings.Contains(mustJSON(t, entry.Post), "_fxVideoSources") {
		t.Fatalf("private source leaked after failed metadata fetch: %s", mustJSON(t, entry.Post))
	}
	var trusted string
	if err := s.db.QueryRow(`SELECT last_success_payload::text FROM public_post_cache WHERE post_id=$1`, first.PostID).Scan(&trusted); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(trusted, `"_fxTrustedSuccess": 1`) {
		t.Fatalf("trusted success marker lost: %s", trusted)
	}
}

func TestNativeVideoProxyRouteRechecksPolicyLinkCapabilityAndFreshness(t *testing.T) {
	db, now, postID, token := nativeVideoRouteFixture(t)
	api := &stubAPI{body: `{"id":"note1","text":"https://x.com/a/status/1234567890"}`}
	fetches := make(map[string]int)
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		fetches[r.URL.String()]++
		if r.URL.Host != "video.twimg.com" {
			t.Fatalf("selected URL=%s", r.URL)
		}
		body := mp4FTYP()
		body = append(body, byte(len(fetches)))
		return response(http.StatusOK, "video/mp4", string(body)), nil
	})}
	h := plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).WithAPI(api).WithHTTPClient(httpClient).WithConfig(map[string]any{"externalFetchEnabled": true, "videoPlaybackMode": "nativeProxy"})
	routes := h.Routes(Plugin)
	request := func(user, link, suppliedToken, target string, index int, policies map[string]any) (any, error) {
		api.body = `{"id":"note1","text":"` + link + `"}`
		body := `{"noteId":"note1","postId":"` + postID + `","target":"` + target + `","index":` + strconv.Itoa(index) + `,"token":"` + suppliedToken + `"}`
		return routes.Call(t, "POST /media/video", plugintest.Request{UserID: user, Body: body, CheckedRolePolicies: policies})
	}
	if _, err := request("u1", "https://x.com/a/status/9999999999", token, "main", 0, allowPreviewPolicies()); err == nil {
		t.Fatal("mismatched note link accepted")
	}
	if _, err := request("u1", "https://x.com/a/status/1234567890", token, "main", 0, map[string]any{}); err == nil {
		t.Fatal("policy denial accepted")
	}
	if _, err := request("", "https://x.com/a/status/1234567890", token, "main", 0, allowPreviewPolicies()); err == nil {
		t.Fatal("anonymous request accepted")
	}
	if _, err := request("u1", "https://x.com/a/status/1234567890", strings.Repeat("0", 32), "main", 0, allowPreviewPolicies()); err == nil {
		t.Fatal("wrong capability accepted")
	}
	if _, err := request("u1", "https://x.com/a/status/1234567890", token, "main", 2, allowPreviewPolicies()); err == nil {
		t.Fatal("invalid index accepted")
	}
	if len(fetches) != 0 {
		t.Fatalf("unauthorized request made upstream fetches: %v", fetches)
	}
	result, err := request("u1", "https://x.com/a/status/1234567890", token, "main", 0, allowPreviewPolicies())
	if err != nil {
		t.Fatal(err)
	}
	blob, ok := result.(plugin.Blob)
	if !ok || blob.ContentType != "video/mp4" || blob.CacheControl != "private, no-store" || len(blob.Body) != len(mp4FTYP())+1 || blob.Body[len(blob.Body)-1] != 1 {
		t.Fatalf("response=%#v", result)
	}
	if len(fetches) != 1 || fetches["https://video.twimg.com/main-0.mp4"] != 1 {
		t.Fatalf("fetches=%v; want selected main index 0", fetches)
	}
	for _, tc := range []struct {
		target string
		index  int
		url    string
	}{{"main", 1, "https://video.twimg.com/main-1.mp4"}, {"quote", 0, "https://video.twimg.com/quote-0.mp4"}} {
		result, err := request("u1", "https://x.com/a/status/1234567890", token, tc.target, tc.index, allowPreviewPolicies())
		if err != nil {
			t.Fatal(err)
		}
		blob, ok := result.(plugin.Blob)
		if !ok || len(blob.Body) != len(mp4FTYP())+1 || blob.Body[len(blob.Body)-1] != byte(len(fetches)) {
			t.Fatalf("selected %s/%d returned wrong body: %#v", tc.target, tc.index, result)
		}
		if fetches[tc.url] != 1 {
			t.Fatalf("selected %s/%d did not fetch its own URL: %v", tc.target, tc.index, fetches)
		}
	}
	if _, err := db.Exec(`UPDATE public_post_cache SET next_fetch_at=$1 WHERE post_id=$2`, now.Add(-time.Second), postID); err != nil {
		t.Fatal(err)
	}
	if _, err := request("u1", "https://x.com/a/status/1234567890", token, "main", 0, allowPreviewPolicies()); err == nil {
		t.Fatal("expired ready cache accepted")
	}
	if len(fetches) != 3 {
		t.Fatalf("expired cache triggered outbound fetch: %v", fetches)
	}
}

func TestNativeVideoProxyRouteRejectsGlobalDisableAndRateLimitsValidRequests(t *testing.T) {
	db, _, postID, token := nativeVideoRouteFixture(t)
	api := &stubAPI{body: `{"id":"note1","text":"https://x.com/a/status/1234567890"}`}
	fetches := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		fetches++
		return response(http.StatusOK, "video/mp4", string(mp4FTYP())), nil
	})}
	body := `{"noteId":"note1","postId":"` + postID + `","target":"main","index":0,"token":"` + token + `"}`
	globalOff := plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).WithAPI(api).WithHTTPClient(client).WithConfig(map[string]any{"videoPlaybackMode": "nativeProxy"}).Routes(Plugin)
	if _, err := globalOff.Call(t, "POST /media/video", plugintest.Request{UserID: "off-user", Body: body, CheckedRolePolicies: allowPreviewPolicies()}); err == nil {
		t.Fatal("global external-fetch disable was ignored")
	}
	if fetches != 0 || api.endpoint != "" {
		t.Fatalf("disabled route made requests: fetches=%d noteEndpoint=%q", fetches, api.endpoint)
	}

	oldLimiter := processNativeVideoLimiter
	processNativeVideoLimiter = newVideoProxyLimiter(1, 1)
	defer func() { processNativeVideoLimiter = oldLimiter }()
	active := plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).WithAPI(api).WithHTTPClient(client).WithConfig(map[string]any{"externalFetchEnabled": true, "videoPlaybackMode": "nativeProxy"}).Routes(Plugin)
	for i := 0; i < 2; i++ {
		_, err := active.Call(t, "POST /media/video", plugintest.Request{UserID: "limited-user", Body: body, CheckedRolePolicies: allowPreviewPolicies()})
		if i == 0 && err != nil {
			t.Fatalf("first request failed: %v", err)
		}
		if i == 1 {
			status, code := plugin.ExtractStatusError(err)
			if status == nil || status.Status != http.StatusTooManyRequests || code != "VIDEO_RATE_LIMITED" {
				t.Fatalf("rate-limit response status=%#v code=%q err=%v", status, code, err)
			}
		}
	}
	if fetches != 1 {
		t.Fatalf("rate-limited request reached upstream; fetches=%d", fetches)
	}
}

func nativeVideoRouteFixture(t *testing.T) (*sql.DB, time.Time, string, string) {
	t.Helper()
	db := migratedDB(t)
	cfg := testConfig()
	cfg.AuthenticatedBudget, cfg.GlobalBudget = 10, 10
	now := time.Now().UTC()
	s := newStore(db, cfg)
	s.cfg.VideoPlaybackMode = "nativeProxy"
	l, err := s.AcquireFetch(context.Background(), "1234567890", "seed", now)
	if err != nil || l == nil {
		t.Fatalf("seed lease=%#v %v", l, err)
	}
	post := PublicPost{ID: l.PostID, Text: "video", Author: PublicAuthor{Name: "A", ScreenName: "a"}, HasVideo: true, Video: &PublicVideo{Provider: "x"}, Videos: []PublicVideoMetadata{{Provider: "x"}, {Provider: "x"}}, Quote: &PublicQuotedPost{ID: "2234567890", Text: "quote", Author: PublicAuthor{Name: "Q", ScreenName: "q"}, HasVideo: true, Video: &PublicVideo{Provider: "x"}, Videos: []PublicVideoMetadata{{Provider: "x"}}}, privateVideoSources: privateVideoSources{Version: 1, Main: []privateVideoSource{{URL: "https://video.twimg.com/main-0.mp4"}, {URL: "https://video.twimg.com/main-1.mp4"}}, Quote: []privateVideoSource{{URL: "https://video.twimg.com/quote-0.mp4"}}}}
	if _, err := s.CompleteReady(context.Background(), *l, post, now); err != nil {
		t.Fatal(err)
	}
	e, err := s.ReadPublic(context.Background(), l.PostID)
	if err != nil || e.Post == nil || e.Post.Video == nil || e.Post.Video.EmbedToken == "" {
		t.Fatalf("seed public=%#v err=%v", e, err)
	}
	return db, now, l.PostID, e.Post.Video.EmbedToken
}

func TestVideoProxyLimiterHasSeparatePerUserAndProcessMinuteLimits(t *testing.T) {
	limiter := newVideoProxyLimiter(2, 3)
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	if !limiter.Allow("a", now) || !limiter.Allow("a", now) || limiter.Allow("a", now) {
		t.Fatal("per-user limit not enforced")
	}
	if !limiter.Allow("b", now) || limiter.Allow("c", now) {
		t.Fatal("process-wide limit not enforced")
	}
	if !limiter.Allow("c", now.Add(time.Minute)) {
		t.Fatal("minute window did not reset")
	}
}

func mp4FTYP() []byte { return []byte{0, 0, 0, 16, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 2, 0} }
