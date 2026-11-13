// SPDX-FileCopyrightText: syuilo and misskey-project
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

func TestPreviewRequiresLoginBeforeNativeOrExternalCall(t *testing.T) {
	a := &stubAPI{body: `{"id":"note1","text":"https://x.com/a/status/1234567890"}`}
	externalCalls := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		externalCalls++
		return response(200, "application/json", `{}`), nil
	})}
	h := plugintest.New(t).WithName("fxtwitter-preview").WithAPI(a).WithHTTPClient(client).WithConfig(map[string]any{"externalFetchEnabled": true})
	routes := h.Routes(Plugin)
	_, err := routes.Call(t, "POST /preview", plugintest.Request{Body: `{"noteId":"note1","postId":"1234567890"}`})
	if err == nil || a.endpoint != "" || externalCalls != 0 || len(h.Enqueues()) != 0 {
		t.Fatalf("anonymous preview = %v, native endpoint = %q, external=%d, enqueues=%v", err, a.endpoint, externalCalls, h.Enqueues())
	}
	_, err = routes.Call(t, "GET /media/:postId/:kind/:index", plugintest.Request{
		Params: map[string]string{"postId": "1234567890", "kind": "avatar", "index": "0"},
		Query:  map[string]string{"noteId": "note1"},
	})
	if err == nil || a.endpoint != "" || externalCalls != 0 {
		t.Fatalf("anonymous media = %v, native endpoint = %q, external=%d", err, a.endpoint, externalCalls)
	}
	_, err = routes.Call(t, "POST /media/prepare", plugintest.Request{Body: `{"noteId":"note1","postId":"1234567890","kind":"image","index":0}`})
	if err == nil || a.endpoint != "" || externalCalls != 0 {
		t.Fatalf("anonymous media preparation = %v, native endpoint = %q, external=%d", err, a.endpoint, externalCalls)
	}
}

func TestPublicEntryRewritesQuotedMediaToAuthorizedSameOriginRoutes(t *testing.T) {
	entry := &CacheEntry{State: "ready", Post: &PublicPost{
		ID: "1234567890", Author: PublicAuthor{AvatarURL: "https://pbs.twimg.com/a.png"},
		Quote: &PublicQuotedPost{
			ID: "2234567890", Author: PublicAuthor{AvatarURL: "https://pbs.twimg.com/q.png"},
			Images: []PublicImage{{URL: "https://pbs.twimg.com/qi.png"}}, Video: &PublicVideo{PosterURL: "https://pbs.twimg.com/qv.png"},
		},
	}}
	got := publicEntry(entry, "note1")
	if got.Post == nil || got.Post.Quote == nil {
		t.Fatalf("public entry = %#v", got)
	}
	quote := got.Post.Quote
	for _, value := range []string{quote.Author.AvatarURL, quote.Images[0].URL, quote.Video.PosterURL} {
		if !strings.HasPrefix(value, "/api/plugin/fxtwitter-preview/media/1234567890/quote-") || !strings.Contains(value, "noteId=note1") {
			t.Fatalf("quoted media URL was not rewritten: %q", value)
		}
	}
}

func TestPublicEntryPreservesEmptyMediaCollectionsAndRewritesVideoPoster(t *testing.T) {
	entry := &CacheEntry{State: "ready", Post: &PublicPost{
		ID: "1234567890", Author: PublicAuthor{AvatarURL: "https://pbs.twimg.com/a.png"},
		Images: []PublicImage{}, HasVideo: true, Video: &PublicVideo{PosterURL: "https://pbs.twimg.com/v.jpg"},
		Quote: &PublicQuotedPost{
			ID: "2234567890", Author: PublicAuthor{AvatarURL: "https://pbs.twimg.com/q.png"},
			Images: []PublicImage{},
		},
	}}
	got := publicEntry(entry, "note1")
	body := mustJSON(t, got)
	if strings.Count(body, `"images":[]`) != 2 {
		t.Fatalf("empty image collections changed shape: %s", body)
	}
	if got.Post == nil || got.Post.Video == nil || !strings.Contains(got.Post.Video.PosterURL, "/poster/0?") {
		t.Fatalf("video thumbnail was not rewritten to the authenticated poster route: %#v", got.Post)
	}
}

func TestCapabilitiesPubliclyAdvertisesOnlySafeClientPolicy(t *testing.T) {
	h := plugintest.New(t).WithName("fxtwitter-preview").WithConfig(map[string]any{
		"externalFetchEnabled": true,
		"videoPlaybackMode":    "officialEmbed",
	})
	routes := h.Routes(Plugin)
	result, err := routes.Call(t, "POST /capabilities", plugintest.Request{})
	if err != nil {
		t.Fatal(err)
	}
	body := mustJSON(t, result)
	for _, expected := range []string{`"externalFetchEnabled":true`, `"videoPlaybackMode":"officialEmbed"`, `"mediaMode":"sameOriginProxy"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("capabilities = %s, missing %s", body, expected)
		}
	}
	for _, forbidden := range []string{"retention", "snapshot", "requestTimeout", "rateLimit"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("public capabilities contains %q: %s", forbidden, body)
		}
	}
}

func TestCapabilitiesExposesCheckedPreviewPermissionOnlyToAuthenticatedViewer(t *testing.T) {
	h := plugintest.New(t).WithName("fxtwitter-preview").WithConfig(map[string]any{"externalFetchEnabled": false})
	routes := h.Routes(Plugin)
	anonymous, err := routes.Call(t, "POST /capabilities", plugintest.Request{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(mustJSON(t, anonymous), `"canUseFxTwitterPreview":true`) {
		t.Fatalf("anonymous capabilities exposed preview permission: %#v", anonymous)
	}
	allowed, err := routes.Call(t, "POST /capabilities", plugintest.Request{
		UserID: "u1", CheckedRolePolicies: map[string]any{
			"canUseFxTwitterPreview": true, "fxTwitterColdFetchesPerHour": 100, "fxTwitterColdFetchesPerDay": 600,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(t, allowed), `"canUseFxTwitterPreview":true`) {
		t.Fatalf("authenticated checked permission missing: %#v", allowed)
	}
	failed, err := routes.Call(t, "POST /capabilities", plugintest.Request{UserID: "u1", CheckedRolePolicyError: context.DeadlineExceeded})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(mustJSON(t, failed), `"canUseFxTwitterPreview":true`) {
		t.Fatalf("resolver failure did not fail closed: %#v", failed)
	}
}

func TestPreviewDeniesWarmCacheAfterCheckedPermissionRevocation(t *testing.T) {
	db := migratedDB(t)
	now := time.Now().UTC()
	s := newStore(db, testConfig())
	l, err := s.AcquireFetch(context.Background(), "1234567890", "u1", now)
	if err != nil {
		t.Fatal(err)
	}
	post := PublicPost{ID: l.PostID, Text: "cached", Author: PublicAuthor{Name: "A", ScreenName: "a"}}
	if _, err := s.CompleteReady(context.Background(), *l, post, now); err != nil {
		t.Fatal(err)
	}
	a := &stubAPI{body: `{"id":"note1","text":"https://x.com/a/status/1234567890"}`}
	h := plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).WithAPI(a).WithConfig(map[string]any{"externalFetchEnabled": true})
	routes := h.Routes(Plugin)
	_, err = routes.Call(t, "POST /preview", plugintest.Request{
		UserID: "u1", Body: `{"i":"native-token","noteId":"note1","postId":"1234567890"}`,
		CheckedRolePolicies: map[string]any{"canUseFxTwitterPreview": false, "fxTwitterColdFetchesPerHour": 100, "fxTwitterColdFetchesPerDay": 600},
	})
	if err == nil || a.endpoint != "" {
		t.Fatalf("revoked permission reached warm cache or note API: err=%v endpoint=%q", err, a.endpoint)
	}
}

func TestAdminAuditRoutesEnforceModeratorAndConfirmation(t *testing.T) {
	db := migratedDB(t)
	_, err := db.Exec(`INSERT INTO audit_body_snapshot (post_id, body, content_hash, observed_at)
		VALUES ('1234567890', 'moderator only', 'hash', $1)`, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	h := plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).WithConfig(map[string]any{})
	routes := h.Routes(Plugin)
	if _, err := routes.Call(t, "POST /admin/audit/list", plugintest.Request{UserID: "u1", Body: `{"postId":"1234567890"}`}); err == nil {
		t.Fatal("non-moderator audit list accepted")
	}
	result, err := routes.Call(t, "POST /admin/audit/list", plugintest.Request{UserID: "mod", Moderator: true, Body: `{"i":"native-token","postId":"1234567890"}`})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(t, result), "moderator only") {
		t.Fatalf("audit list = %#v", result)
	}
	deleteBody := `{"i":"native-token","target":"audit","postId":"1234567890","reason":"request","confirm":false}`
	if _, err := routes.Call(t, "POST /admin/audit/delete", plugintest.Request{UserID: "mod", Moderator: true, Body: deleteBody}); err == nil {
		t.Fatal("unconfirmed delete accepted")
	}
	deleteBody = `{"i":"native-token","target":"audit","postId":"1234567890","reason":"request","confirm":true}`
	if _, err := routes.Call(t, "POST /admin/audit/delete", plugintest.Request{UserID: "mod", Moderator: true, Body: deleteBody}); err != nil {
		t.Fatal(err)
	}
}

func TestPublicPostJSONCannotContainAuditFields(t *testing.T) {
	raw := mustJSON(t, map[string]any{
		"post":  PublicPost{ID: "1234567890", Text: "public", Author: PublicAuthor{Name: "A", ScreenName: "a"}},
		"cache": CacheEntry{PostID: "1234567890", State: "unavailable", ErrorClass: "network"},
	})
	for _, forbidden := range []string{"contentHash", "observedAt", "snapshot", "errorClass"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("public JSON contains %q: %s", forbidden, raw)
		}
	}
}

func TestPublicEntryMapsUnavailableErrorsAndRetryTimeWithoutLeakingClasses(t *testing.T) {
	nextFetchAt := time.Date(2026, 11, 12, 12, 30, 0, 0, time.UTC)
	for _, tc := range []struct {
		class string
		want  string
	}{
		{"network", "temporary"},
		{"tombstone_private", "private"},
		{"tombstone_deleted", "not-found"},
		{"fx_private", "private"},
		{"fx_not_found", "not-found"},
		{"upstream_not_found", "unavailable"},
		{"upstream_private", "unavailable"}, // Legacy rows may reflect a bare 401/403.
		{"upstream_status", "unavailable"},
	} {
		t.Run(tc.class, func(t *testing.T) {
			if (tc.class == "fx_private" || tc.class == "fx_not_found") && isTransientFailureClass(tc.class) {
				t.Fatalf("permanent classification %q retained stale last-success data", tc.class)
			}
			entry := &CacheEntry{PostID: "1234567890", State: "unavailable", ErrorClass: tc.class, NextFetchAt: nextFetchAt}
			got, err := json.Marshal(publicEntry(entry, "note1"))
			if err != nil {
				t.Fatal(err)
			}
			var public map[string]any
			if err := json.Unmarshal(got, &public); err != nil {
				t.Fatal(err)
			}
			if public["error"] != tc.want {
				t.Fatalf("public error = %#v, want %q; JSON=%s", public["error"], tc.want, got)
			}
			if public["retryAt"] != nextFetchAt.Format(time.RFC3339Nano) {
				t.Fatalf("retryAt = %#v, want %q; JSON=%s", public["retryAt"], nextFetchAt.Format(time.RFC3339Nano), got)
			}
			if strings.Contains(string(got), tc.class) || strings.Contains(string(got), "errorClass") {
				t.Fatalf("internal failure class leaked: %s", got)
			}
		})
	}
}

func TestPublicEntryMapsStaleToTemporaryRetryAndLeavesReadyClean(t *testing.T) {
	nextFetchAt := time.Date(2026, 11, 12, 12, 30, 0, 0, time.UTC)
	stale := &CacheEntry{PostID: "1234567890", State: "stale", Stale: true, ErrorClass: "upstream_5xx", NextFetchAt: nextFetchAt, Post: &PublicPost{ID: "1234567890", Author: PublicAuthor{Name: "A", ScreenName: "a"}}}
	got, err := json.Marshal(publicEntry(stale, "note1"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"error":"temporary"`) || !strings.Contains(string(got), `"retryAt":"`+nextFetchAt.Format(time.RFC3339Nano)+`"`) {
		t.Fatalf("stale response is missing retry details: %s", got)
	}
	ready := *stale
	ready.State, ready.Stale = "ready", false
	got, err = json.Marshal(publicEntry(&ready, "note1"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), `"error"`) || strings.Contains(string(got), `"retryAt"`) {
		t.Fatalf("ready response contains unavailable-only fields: %s", got)
	}
}

func TestPreviewRetryUsesNormalRouteAfterRetryAt(t *testing.T) {
	db := migratedDB(t)
	now := time.Now().UTC()
	cfg := testConfig()
	s := newStore(db, cfg)
	lease, err := s.AcquireFetch(context.Background(), "1234567890", "u1", now)
	if err != nil || lease == nil {
		t.Fatalf("initial lease = %#v, err=%v", lease, err)
	}
	if _, err := s.CompleteUnavailable(context.Background(), *lease, "network", now); err != nil {
		t.Fatal(err)
	}
	a := &stubAPI{body: `{"id":"note1","text":"https://x.com/a/status/1234567890"}`}
	h := plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).WithAPI(a).WithConfig(map[string]any{"externalFetchEnabled": true})
	routes := h.Routes(Plugin)
	request := plugintest.Request{
		UserID: "u1", Body: `{"i":"native-token","noteId":"note1","postId":"1234567890"}`,
		CheckedRolePolicies: map[string]any{"canUseFxTwitterPreview": true, "fxTwitterColdFetchesPerHour": 100, "fxTwitterColdFetchesPerDay": 600},
	}
	before, err := routes.Call(t, "POST /preview", request)
	if err != nil {
		t.Fatal(err)
	}
	beforeJSON := mustJSON(t, before)
	if !strings.Contains(beforeJSON, `"state":"unavailable"`) || !strings.Contains(beforeJSON, `"error":"temporary"`) || !strings.Contains(beforeJSON, `"retryAt":"`) || len(h.Enqueues()) != 0 {
		t.Fatalf("retry before retryAt should wait without enqueueing: result=%s enqueues=%v", beforeJSON, h.Enqueues())
	}
	if _, err := db.Exec(`UPDATE public_post_cache SET next_fetch_at = now() - interval '1 second' WHERE post_id = '1234567890'`); err != nil {
		t.Fatal(err)
	}
	after, err := routes.Call(t, "POST /preview", request)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(t, after), `"state":"pending"`) || len(h.Enqueues()) != 1 {
		t.Fatalf("normal preview request did not enqueue after retryAt: result=%#v enqueues=%v", after, h.Enqueues())
	}
}

func TestPublicEntryPreservesSparsePosterIndexes(t *testing.T) {
	entry := &CacheEntry{PostID: "1234567890", State: "ready", Post: &PublicPost{
		ID: "1234567890", Videos: []PublicVideoMetadata{{Provider: "x"}, {Provider: "x", PosterURL: "https://pbs.twimg.com/second.jpg"}},
		Quote: &PublicQuotedPost{ID: "2234567890", Videos: []PublicVideoMetadata{{Provider: "x"}, {Provider: "x", PosterURL: "https://pbs.twimg.com/quote-second.jpg"}}},
	}}
	got := publicEntry(entry, "note1")
	if len(got.Post.Videos) != 2 || got.Post.Videos[0].PosterURL != "" || !strings.Contains(got.Post.Videos[1].PosterURL, "/poster/1?") {
		t.Fatalf("main poster indexes were compacted or rewritten incorrectly: %#v", got.Post.Videos)
	}
	if got.Post.Quote == nil || len(got.Post.Quote.Videos) != 2 || got.Post.Quote.Videos[0].PosterURL != "" || !strings.Contains(got.Post.Quote.Videos[1].PosterURL, "/quote-poster/1?") {
		t.Fatalf("quote poster indexes were compacted or rewritten incorrectly: %#v", got.Post.Quote)
	}
}

func TestPreviewDefaultOffDoesNotCallNativeOrExternalServices(t *testing.T) {
	a := &stubAPI{body: `{"id":"note1","text":"https://x.com/a/status/1234567890"}`}
	h := plugintest.New(t).WithName("fxtwitter-preview").WithAPI(a).WithConfig(map[string]any{})
	routes := h.Routes(Plugin)
	result, err := routes.Call(t, "POST /preview", plugintest.Request{UserID: "u1", Body: `{"i":"native-token","noteId":"note1","postId":"1234567890"}`})
	if err != nil {
		t.Fatal(err)
	}
	state := result.(map[string]any)["state"]
	if state != "disabled" || a.actor != "" || len(h.Enqueues()) != 0 {
		t.Fatalf("result=%#v actor=%q enqueues=%v", result, a.actor, h.Enqueues())
	}
	if _, err := routes.Call(t, "POST /preview", plugintest.Request{UserID: "u1", Body: `{"i":"native-token","noteId":"note1","postId":"1234567890","unexpected":true}`}); err == nil {
		t.Fatal("preview accepted an unknown field other than the standard auth envelope")
	}
}

func TestEmbedShellRechecksNoteACLAndCacheCapability(t *testing.T) {
	db := migratedDB(t)
	now := time.Now().UTC()
	s := newStore(db, testConfig())
	lease, err := s.AcquireFetch(context.Background(), "1234567890", "u1", now)
	if err != nil {
		t.Fatal(err)
	}
	post := PublicPost{
		ID: "1234567890", Text: "video", Author: PublicAuthor{Name: "A", ScreenName: "a"}, HasVideo: true,
		Video: &PublicVideo{Provider: "x", PosterURL: "https://pbs.twimg.com/poster.jpg"},
	}
	if _, err := s.CompleteReady(context.Background(), *lease, post, now); err != nil {
		t.Fatal(err)
	}
	entry, err := s.ReadPublic(context.Background(), post.ID)
	if err != nil || entry == nil || entry.Post == nil || entry.Post.Video == nil {
		t.Fatalf("cache entry = %#v, err = %v", entry, err)
	}

	a := &stubAPI{body: `{"id":"note1","text":"https://x.com/a/status/1234567890"}`}
	h := plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).WithAPI(a).WithConfig(map[string]any{
		"externalFetchEnabled": true,
		"videoPlaybackMode":    "officialEmbed",
	})
	routes := h.Routes(Plugin)
	body := `{"i":"native-token","noteId":"note1","postId":"1234567890","token":"` + entry.Post.Video.EmbedToken + `","theme":"dark","lang":"ja","nonce":"0123456789abcdef0123456789abcdef"}`
	result, err := routes.Call(t, "POST /embed/shell", plugintest.Request{UserID: "u1", Body: body, CheckedRolePolicies: allowPreviewPolicies()})
	if err != nil {
		t.Fatal(err)
	}
	if a.actor != "u1" || a.endpoint != "notes/show" || !strings.Contains(mustJSON(t, result), "https://platform.twitter.com/embed/Tweet.html") {
		t.Fatalf("result=%#v actor=%q endpoint=%q", result, a.actor, a.endpoint)
	}

	a.body = `{"id":"note1","text":"https://x.com/a/status/9999999999"}`
	if _, err := routes.Call(t, "POST /embed/shell", plugintest.Request{UserID: "u1", Body: body, CheckedRolePolicies: allowPreviewPolicies()}); err == nil {
		t.Fatal("embed shell accepted a note/post mismatch")
	}
}

func TestEmbedShellBuildsYouTubeURLFromCachedQuotedVideo(t *testing.T) {
	db := migratedDB(t)
	now := time.Now().UTC()
	s := newStore(db, testConfig())
	lease, err := s.AcquireFetch(context.Background(), "1234567890", "u1", now)
	if err != nil {
		t.Fatal(err)
	}
	post := PublicPost{
		ID: "1234567890", Text: "quoted YouTube", Author: PublicAuthor{Name: "A", ScreenName: "a"},
		Quote: &PublicQuotedPost{
			ID: "2234567890", Text: "video", Author: PublicAuthor{Name: "Q", ScreenName: "q"}, HasVideo: true,
			Video: &PublicVideo{Provider: "youtube", ProviderVideoID: "dQw4w9WgXcQ", PosterURL: "https://pbs.twimg.com/quote.jpg"},
		},
	}
	if _, err := s.CompleteReady(context.Background(), *lease, post, now); err != nil {
		t.Fatal(err)
	}
	entry, err := s.ReadPublic(context.Background(), post.ID)
	if err != nil || entry == nil || entry.Post == nil || entry.Post.Quote == nil || entry.Post.Quote.Video == nil {
		t.Fatalf("cache entry = %#v, err = %v", entry, err)
	}
	token := entry.Post.Quote.Video.EmbedToken
	if token == "" {
		t.Fatal("quoted YouTube capability missing")
	}

	a := &stubAPI{body: `{"id":"note1","text":"https://x.com/a/status/1234567890"}`}
	h := plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).WithAPI(a).WithConfig(map[string]any{
		"externalFetchEnabled": true,
		"videoPlaybackMode":    "officialEmbed",
	})
	routes := h.Routes(Plugin)
	body := `{"noteId":"note1","postId":"1234567890","target":"quote","token":"` + token + `","theme":"dark","lang":"ja","nonce":"0123456789abcdef0123456789abcdef"}`
	result, err := routes.Call(t, "POST /embed/shell", plugintest.Request{UserID: "u1", Body: body, CheckedRolePolicies: allowPreviewPolicies()})
	if err != nil {
		t.Fatal(err)
	}
	encoded := mustJSON(t, result)
	if !strings.Contains(encoded, `https://www.youtube.com/embed/dQw4w9WgXcQ?autoplay=0\u0026playsinline=1`) || strings.Contains(encoded, "platform.twitter.com") {
		t.Fatalf("quoted YouTube embed = %s", encoded)
	}
}

func TestEmbedShellLinkOnlyModeStopsBeforeNoteLookup(t *testing.T) {
	a := &stubAPI{body: `{"id":"note1","text":"https://x.com/a/status/1234567890"}`}
	h := plugintest.New(t).WithName("fxtwitter-preview").WithAPI(a).WithConfig(map[string]any{
		"videoPlaybackMode": "linkOnly",
	})
	routes := h.Routes(Plugin)
	body := `{"noteId":"note1","postId":"1234567890","token":"0123456789abcdef0123456789abcdef","theme":"dark","lang":"ja","nonce":"0123456789abcdef0123456789abcdef"}`
	if _, err := routes.Call(t, "POST /embed/shell", plugintest.Request{UserID: "u1", Body: body}); err == nil {
		t.Fatal("linkOnly mode served an embed shell")
	}
	if a.endpoint != "" {
		t.Fatalf("linkOnly mode called native API %q", a.endpoint)
	}
}

func TestMediaRouteRechecksNoteACLBeforeOutboundFetch(t *testing.T) {
	db := migratedDB(t)
	now := time.Now().UTC()
	s := newStore(db, testConfig())
	mediaCache(t, s, now, "https://pbs.twimg.com/media/test.png")
	oldCache := processMediaResponseCache
	processMediaResponseCache = newMediaResponseCache(mediaResponseCacheBytes, mediaResponseCacheEntries, mediaResponseCacheTTL)
	t.Cleanup(func() { processMediaResponseCache = oldCache })
	rawURL, fetchedAt, err := s.ResolveMediaDescriptor(context.Background(), "1234567890", "image", "0", now.Add(time.Second))
	if err != nil || fetchedAt == nil {
		t.Fatalf("resolve media for cache seed: url=%q fetchedAt=%v err=%v", rawURL, fetchedAt, err)
	}
	processMediaResponseCache.Set(mediaCacheKey{PostID: "1234567890", FetchedAt: *fetchedAt, SourceURL: rawURL, MaxBytes: 10 << 20}, plugin.Blob{ContentType: "image/png", Body: tinyImage(t, "png")}, now)
	a := &stubAPI{body: `{"id":"note1","text":"https://x.com/a/status/9999999999"}`}
	mediaCalled := false
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		mediaCalled = true
		return response(200, "image/png", string(tinyImage(t, "png"))), nil
	})}
	h := plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).WithAPI(a).WithHTTPClient(client).WithConfig(map[string]any{
		"externalFetchEnabled": true,
	})
	routes := h.Routes(Plugin)
	_, err = routes.Call(t, "GET /media/:postId/:kind/:index", plugintest.Request{
		UserID:              "u1",
		Params:              map[string]string{"postId": "1234567890", "kind": "image", "index": "0"},
		Query:               map[string]string{"noteId": "note1"},
		CheckedRolePolicies: allowPreviewPolicies(),
	})
	if err == nil {
		t.Fatal("media route accepted a note/post mismatch")
	}
	if a.actor != "u1" || a.endpoint != "notes/show" {
		t.Fatalf("media ACL call = actor:%q endpoint:%q", a.actor, a.endpoint)
	}
	if mediaCalled {
		t.Fatal("media route fetched outbound bytes before note ACL passed")
	}
}

func TestMediaRouteRechecksDescriptorAfterQueueBeforeCacheOrFetch(t *testing.T) {
	db := migratedDB(t)
	now := time.Now().UTC()
	s := newStore(db, testConfig())
	mediaCache(t, s, now, "https://pbs.twimg.com/media/test.png")
	oldGate, oldLimiter, oldAdmission, oldCache := processMediaRequestGate, processMediaRequestLimiter, processMediaAdmission, processMediaResponseCache
	processMediaRequestGate = newMediaRequestGate(mediaRequestCapacity, mediaGlobalWaitLimit, mediaUserWaitLimit, 5*(1024+1))
	processMediaRequestLimiter = newMediaRequestLimiter(mediaUserRequestsPerMinute, mediaGlobalRequestsPerMinute)
	processMediaAdmission = newMediaAdmission(mediaProcessingConcurrency, mediaRequestCapacity, mediaBufferCapacity)
	processMediaResponseCache = newMediaResponseCache(mediaResponseCacheBytes, mediaResponseCacheEntries, mediaResponseCacheTTL)
	t.Cleanup(func() {
		processMediaRequestGate, processMediaRequestLimiter, processMediaAdmission, processMediaResponseCache = oldGate, oldLimiter, oldAdmission, oldCache
	})
	var held []func()
	for range 4 {
		release, err := processMediaRequestGate.acquire(context.Background(), "u1", 1024)
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, release)
	}
	a := &stubAPI{body: `{"id":"note1","text":"https://x.com/a/status/1234567890"}`}
	mediaCalled := false
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		mediaCalled = true
		return response(200, "image/png", string(tinyImage(t, "png"))), nil
	})}
	h := plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).WithAPI(a).WithHTTPClient(client).WithConfig(map[string]any{
		"externalFetchEnabled": true, "staticImageMaxBytes": 1024,
	})
	routes := h.Routes(Plugin)
	result := make(chan error, 1)
	go func() {
		_, err := routes.Call(t, "GET /media/:postId/:kind/:index", plugintest.Request{
			UserID: "u1", Params: map[string]string{"postId": "1234567890", "kind": "image", "index": "0"},
			Query: map[string]string{"noteId": "note1"}, CheckedRolePolicies: allowPreviewPolicies(),
		})
		result <- err
	}()
	waitForMediaGate(t, processMediaRequestGate, 4, 1)
	if _, err := db.Exec(`DELETE FROM public_post_cache WHERE post_id = $1`, "1234567890"); err != nil {
		t.Fatal(err)
	}
	for _, release := range held {
		release()
	}
	if err := <-result; err == nil {
		t.Fatal("queued media request succeeded after its descriptor was removed")
	}
	if mediaCalled {
		t.Fatal("queued request fetched outbound bytes after descriptor removal")
	}
}

func TestMediaRouteServesAuthorizedWarmCacheUnderSharedAdmission(t *testing.T) {
	db := migratedDB(t)
	now := time.Now().UTC()
	s := newStore(db, testConfig())
	mediaCache(t, s, now, "https://pbs.twimg.com/media/test.png")
	rawURL, fetchedAt, err := s.ResolveMediaDescriptor(context.Background(), "1234567890", "image", "0", now.Add(time.Second))
	if err != nil || fetchedAt == nil {
		t.Fatalf("resolve media for cache seed: url=%q fetchedAt=%v err=%v", rawURL, fetchedAt, err)
	}
	oldGate, oldLimiter, oldAdmission, oldCache := processMediaRequestGate, processMediaRequestLimiter, processMediaAdmission, processMediaResponseCache
	processMediaRequestGate = newMediaRequestGate(mediaRequestCapacity, mediaGlobalWaitLimit, mediaUserWaitLimit, mediaBufferCapacity)
	processMediaRequestLimiter = newMediaRequestLimiter(mediaUserRequestsPerMinute, mediaGlobalRequestsPerMinute)
	processMediaAdmission = newMediaAdmission(mediaProcessingConcurrency, mediaRequestCapacity, mediaBufferCapacity)
	processMediaResponseCache = newMediaResponseCache(mediaResponseCacheBytes, mediaResponseCacheEntries, mediaResponseCacheTTL)
	t.Cleanup(func() {
		processMediaRequestGate, processMediaRequestLimiter, processMediaAdmission, processMediaResponseCache = oldGate, oldLimiter, oldAdmission, oldCache
	})
	wantBody := tinyImage(t, "png")
	processMediaResponseCache.Set(mediaCacheKey{PostID: "1234567890", FetchedAt: *fetchedAt, SourceURL: rawURL, MaxBytes: 1024}, plugin.Blob{ContentType: "image/png", Body: wantBody}, now)
	mediaCalled := false
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		mediaCalled = true
		return nil, errors.New("warm cache should avoid outbound fetch")
	})}
	a := &stubAPI{body: `{"id":"note1","text":"https://x.com/a/status/1234567890"}`}
	h := plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).WithAPI(a).WithHTTPClient(client).WithConfig(map[string]any{
		"externalFetchEnabled": true, "staticImageMaxBytes": 1024,
	})
	routes := h.Routes(Plugin)
	result, err := routes.Call(t, "GET /media/:postId/:kind/:index", plugintest.Request{
		UserID: "u1", Params: map[string]string{"postId": "1234567890", "kind": "image", "index": "0"},
		Query: map[string]string{"noteId": "note1"}, CheckedRolePolicies: allowPreviewPolicies(),
	})
	if err != nil {
		t.Fatal(err)
	}
	blob, ok := result.(plugin.Blob)
	if !ok || string(blob.Body) != string(wantBody) || cap(blob.Body) != len(wantBody) {
		t.Fatalf("warm-cache blob = %#v, want exact copied image bytes", result)
	}
	if blob.CacheControl != "private, no-store" || blob.WriteTimeout != mediaResponseWriteTimeout || mediaCalled {
		t.Fatalf("cache response policy = cache:%q timeout:%s outbound:%v", blob.CacheControl, blob.WriteTimeout, mediaCalled)
	}
}

func TestImagePrepareReusesPublishedDescriptorAfterFreshAuthorization(t *testing.T) {
	db := migratedDB(t)
	now := time.Now().UTC()
	s := newStore(db, testConfig())
	mediaCache(t, s, now, "https://pbs.twimg.com/media/test.png")
	rawURL, fetchedAt, err := s.ResolveMediaDescriptor(context.Background(), "1234567890", "image", "0", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	sourceKey, _, err := imageMediaSourceKey("1234567890", "image", "0", rawURL, fetchedAt, 1024)
	if err != nil {
		t.Fatal(err)
	}
	oldRegistry, oldLimiter := processPublishedMediaRegistry, processMediaRequestLimiter
	processPublishedMediaRegistry = newPublishedMediaRegistry(1<<20, 8, publishedMediaTTL, 50<<20, 4)
	processMediaRequestLimiter = newMediaRequestLimiter(mediaUserRequestsPerMinute, mediaGlobalRequestsPerMinute)
	t.Cleanup(func() {
		processPublishedMediaRegistry, processMediaRequestLimiter = oldRegistry, oldLimiter
	})
	registered, err := processPublishedMediaRegistry.PublishForSource("image/png", tinyImage(t, "png"), sourceKey, now)
	if err != nil {
		t.Fatal(err)
	}
	mediaRequests := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		mediaRequests++
		return nil, errors.New("warm published registration should avoid upstream")
	})}
	api := &stubAPI{body: `{"id":"note1","text":"https://x.com/a/status/1234567890"}`}
	h := plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).WithAPI(api).WithHTTPClient(client).WithConfig(map[string]any{
		"externalFetchEnabled": true, "staticImageMaxBytes": 1024,
	})
	routes := h.Routes(Plugin)
	body := `{"noteId":"note1","postId":"1234567890","kind":"image","index":0}`
	result, err := routes.Call(t, "POST /media/prepare", plugintest.Request{UserID: "u1", Body: body, CheckedRolePolicies: allowPreviewPolicies()})
	if err != nil {
		t.Fatal(err)
	}
	payload, ok := result.(map[string]any)
	if !ok || payload["url"] != "/api/plugin/fxtwitter-preview/public-media/"+registered.Token+".png" || payload["expiresAt"] != registered.ExpiresAt.Format(time.RFC3339Nano) {
		t.Fatalf("prepare payload=%#v, expected opaque existing URL and original expiry", result)
	}
	if strings.Contains(payload["url"].(string), "noteId") || strings.Contains(payload["url"].(string), "1234567890") || mediaRequests != 0 {
		t.Fatalf("public URL leaked descriptor or hit upstream: url=%q requests=%d", payload["url"], mediaRequests)
	}
	if _, err := routes.Call(t, "POST /media/prepare", plugintest.Request{UserID: "u1", Body: body, CheckedRolePolicies: map[string]any{}}); err == nil {
		t.Fatal("warm public registration bypassed the current role policy")
	}
	if mediaRequests != 0 {
		t.Fatalf("denied warm prepare made %d upstream requests", mediaRequests)
	}
}

func TestImagePreparePublishesColdFetchThenServesPublicAndReusesIt(t *testing.T) {
	db := migratedDB(t)
	mediaCache(t, newStore(db, testConfig()), time.Now().UTC(), "https://pbs.twimg.com/media/test.png")
	oldRegistry, oldLimiter, oldCache := processPublishedMediaRegistry, processMediaRequestLimiter, processMediaResponseCache
	processPublishedMediaRegistry = newPublishedMediaRegistry(1<<20, 8, publishedMediaTTL, 50<<20, 4)
	processMediaRequestLimiter = newMediaRequestLimiter(mediaUserRequestsPerMinute, mediaGlobalRequestsPerMinute)
	processMediaResponseCache = newMediaResponseCache(mediaResponseCacheBytes, mediaResponseCacheEntries, mediaResponseCacheTTL)
	t.Cleanup(func() {
		processPublishedMediaRegistry, processMediaRequestLimiter, processMediaResponseCache = oldRegistry, oldLimiter, oldCache
	})
	image := tinyImage(t, "png")
	mediaRequests := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		mediaRequests++
		if r.URL.String() != "https://pbs.twimg.com/media/test.png" {
			t.Fatalf("unexpected image URL: %s", r.URL)
		}
		return response(http.StatusOK, "image/png", string(image)), nil
	})}
	api := &stubAPI{body: `{"id":"note1","text":"https://x.com/a/status/1234567890"}`}
	routes := plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).WithAPI(api).WithHTTPClient(client).WithConfig(map[string]any{
		"externalFetchEnabled": true, "staticImageMaxBytes": 1024,
	}).Routes(Plugin)
	body := `{"noteId":"note1","postId":"1234567890","kind":"image","index":0}`
	prepare := func() (map[string]any, error) {
		result, err := routes.Call(t, "POST /media/prepare", plugintest.Request{UserID: "u1", Body: body, CheckedRolePolicies: allowPreviewPolicies()})
		if err != nil {
			return nil, err
		}
		payload, ok := result.(map[string]any)
		if !ok {
			t.Fatalf("prepare response=%#v", result)
		}
		return payload, nil
	}
	first, err := prepare()
	if err != nil {
		t.Fatal(err)
	}
	if mediaRequests != 1 || first["contentType"] != "image/png" || first["size"] != int64(len(image)) {
		t.Fatalf("cold prepare=%#v upstream requests=%d", first, mediaRequests)
	}
	filename := strings.TrimPrefix(first["url"].(string), "/api/plugin/fxtwitter-preview/public-media/")
	publicResult, err := routes.Call(t, "GET /public-media/:filename", plugintest.Request{Params: map[string]string{"filename": filename}})
	if err != nil {
		t.Fatal(err)
	}
	publicBlob, ok := publicResult.(plugin.Blob)
	if !ok || publicBlob.ContentType != "image/png" || string(publicBlob.Body) != string(image) {
		t.Fatalf("public image did not return acquired bytes: %#v", publicResult)
	}
	second, err := prepare()
	if err != nil {
		t.Fatal(err)
	}
	if second["url"] != first["url"] || second["expiresAt"] != first["expiresAt"] || mediaRequests != 1 {
		t.Fatalf("warm prepare changed expiry/token or refetched: first=%#v second=%#v upstream=%d", first, second, mediaRequests)
	}
}

func TestYouTubePosterPrepareIsPrivateAndDoesNotCreatePublicEntry(t *testing.T) {
	db := migratedDB(t)
	now := time.Now().UTC()
	s := newStore(db, testConfig())
	lease, err := s.AcquireFetch(context.Background(), "1234567890", "u1", now)
	if err != nil || lease == nil {
		t.Fatalf("seed lease=%#v err=%v", lease, err)
	}
	posterURL := "https://img.youtube.com/vi/abcdefghijk/0.jpg"
	posterExpiry := now.Add(time.Hour)
	post := PublicPost{
		ID: lease.PostID, Text: "youtube poster", Author: PublicAuthor{Name: "A", ScreenName: "a"}, HasVideo: true,
		Video:  &PublicVideo{Provider: "youtube", ProviderVideoID: "abcdefghijk", PosterURL: posterURL, PosterExpiresAt: &posterExpiry},
		Videos: []PublicVideoMetadata{{Provider: "youtube", ProviderVideoID: "abcdefghijk", PosterURL: posterURL, PosterExpiresAt: &posterExpiry}},
	}
	if _, err := s.CompleteReady(context.Background(), *lease, post, now); err != nil {
		t.Fatal(err)
	}
	oldRegistry, oldLimiter := processPublishedMediaRegistry, processMediaRequestLimiter
	processPublishedMediaRegistry = newPublishedMediaRegistry(1<<20, 8, publishedMediaTTL, 50<<20, 4)
	processMediaRequestLimiter = newMediaRequestLimiter(mediaUserRequestsPerMinute, mediaGlobalRequestsPerMinute)
	t.Cleanup(func() { processPublishedMediaRegistry, processMediaRequestLimiter = oldRegistry, oldLimiter })
	image := tinyImage(t, "jpeg")
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.URL.String() != posterURL {
			t.Fatalf("unexpected poster URL: %s", r.URL)
		}
		return response(http.StatusOK, "image/jpeg", string(image)), nil
	})}
	api := &stubAPI{body: `{"id":"note1","text":"https://x.com/a/status/1234567890"}`}
	routes := plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).WithAPI(api).WithHTTPClient(client).WithConfig(map[string]any{
		"externalFetchEnabled": true, "staticImageMaxBytes": 1024,
	}).Routes(Plugin)
	prepared, err := routes.Call(t, "POST /media/prepare", plugintest.Request{
		UserID: "u1", Body: `{"noteId":"note1","postId":"1234567890","kind":"poster","index":0}`,
		CheckedRolePolicies: allowPreviewPolicies(),
	})
	status, code := plugin.ExtractStatusError(err)
	if status == nil || status.Status != http.StatusConflict || code != "PRIVATE_MEDIA_ONLY" || requests != 0 || processPublishedMediaRegistry.Stats().Entries != 0 {
		t.Fatalf("YouTube prepare=%#v status=%#v code=%q fetches=%d registry=%+v", prepared, status, code, requests, processPublishedMediaRegistry.Stats())
	}
	privateResult, err := routes.Call(t, "GET /media/:postId/:kind/:index", plugintest.Request{
		UserID: "u1", Params: map[string]string{"postId": "1234567890", "kind": "poster", "index": "0"},
		Query: map[string]string{"noteId": "note1"}, CheckedRolePolicies: allowPreviewPolicies(),
	})
	if err != nil {
		t.Fatal(err)
	}
	blob, ok := privateResult.(plugin.Blob)
	if !ok || blob.CacheControl != "private, no-store" || blob.ContentType != "image/jpeg" || requests != 1 || processPublishedMediaRegistry.Stats().Entries != 0 {
		t.Fatalf("legacy private poster=%#v fetches=%d registry=%+v", privateResult, requests, processPublishedMediaRegistry.Stats())
	}
}

func TestAnonymousPublicMediaRouteServesOnlyRegisteredBytes(t *testing.T) {
	now := time.Now().UTC()
	oldRegistry := processPublishedMediaRegistry
	processPublishedMediaRegistry = newPublishedMediaRegistry(1<<20, 8, publishedMediaTTL, 50<<20, 1)
	t.Cleanup(func() { processPublishedMediaRegistry = oldRegistry })
	media, err := processPublishedMediaRegistry.Publish("image/png", tinyImage(t, "png"), now)
	if err != nil {
		t.Fatal(err)
	}
	upstreamRequests := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		upstreamRequests++
		return nil, errors.New("public media route must not fetch upstream")
	})}
	db := migratedDB(t)
	h := plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).WithHTTPClient(client).WithConfig(map[string]any{"externalFetchEnabled": true})
	routes := h.Routes(Plugin)
	result, err := routes.Call(t, "GET /public-media/:filename", plugintest.Request{Params: map[string]string{"filename": media.Token + ".png"}})
	if err != nil {
		t.Fatal(err)
	}
	blob, ok := result.(plugin.Blob)
	if !ok || blob.ContentType != "image/png" || string(blob.Body) != string(media.Body) || !strings.HasPrefix(blob.CacheControl, "public, max-age=") || !strings.Contains(blob.CacheControl, "s-maxage=") || !strings.Contains(blob.CacheControl, "must-revalidate") || !strings.Contains(blob.CacheControl, "no-transform") {
		t.Fatalf("public media response=%#v", result)
	}
	for _, filename := range []string{strings.Repeat("0", 32) + ".png", media.Token + ".jpg", media.Token + ".svg", "../../" + media.Token + ".png"} {
		_, err := routes.Call(t, "GET /public-media/:filename", plugintest.Request{Params: map[string]string{"filename": filename}})
		if err == nil {
			t.Errorf("invalid public media filename %q was served", filename)
		}
	}
	_, err = routes.Call(t, "GET /public-media/:filename", plugintest.Request{Params: map[string]string{"filename": strings.Repeat("f", 32) + ".png"}})
	status, _ := plugin.ExtractStatusError(err)
	if status == nil || status.Status != http.StatusNotFound {
		t.Fatalf("unregistered public URL status=%#v err=%v", status, err)
	}
	expired, err := processPublishedMediaRegistry.Publish("image/png", []byte("expired bytes"), time.Now().UTC().Add(-publishedMediaTTL-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	_, err = routes.Call(t, "GET /public-media/:filename", plugintest.Request{Params: map[string]string{"filename": expired.Token + ".png"}})
	status, _ = plugin.ExtractStatusError(err)
	if status == nil || status.Status != http.StatusNotFound {
		t.Fatalf("expired public URL status=%#v err=%v", status, err)
	}
	if upstreamRequests != 0 {
		t.Fatalf("public media route made %d upstream requests", upstreamRequests)
	}
}

func TestExternalFetchKillSwitchStopsEmbedBeforeNativeLookup(t *testing.T) {
	a := &stubAPI{body: `{"id":"note1","text":"https://x.com/a/status/1234567890"}`}
	h := plugintest.New(t).WithName("fxtwitter-preview").WithDB(migratedDB(t)).WithAPI(a).WithConfig(map[string]any{})
	routes := h.Routes(Plugin)
	body := `{"noteId":"note1","postId":"1234567890","token":"0123456789abcdef0123456789abcdef","theme":"dark","lang":"ja","nonce":"0123456789abcdef0123456789abcdef"}`
	if _, err := routes.Call(t, "POST /embed/shell", plugintest.Request{UserID: "u1", Body: body}); err == nil {
		t.Fatal("external-fetch kill switch served an embed shell")
	}
	if a.endpoint != "" {
		t.Fatalf("external-fetch kill switch called native API %q", a.endpoint)
	}
}
