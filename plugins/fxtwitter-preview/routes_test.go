// SPDX-FileCopyrightText: syuilo and misskey-project
// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import (
	"bytes"
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

func TestPublicEntryExposesNativePlaybackOnlyInNativeProxyMode(t *testing.T) {
	entry := &CacheEntry{
		State: "ready", PlaybackToken: "stored-capability",
		Post: &PublicPost{
			ID:     "1234567890",
			Video:  &PublicVideo{Provider: "x", EmbedToken: "legacy-embed-token"},
			Videos: []PublicVideoMetadata{{Provider: "x", PlaybackAvailable: true}},
		},
	}
	for _, mode := range []string{"officialEmbed", "linkOnly"} {
		got := publicEntryForMode(entry, "note1", mode)
		if got.Post.Videos[0].PlaybackURL != "" {
			t.Fatalf("mode %q exposed native playback URL: %#v", mode, got.Post.Videos[0])
		}
		if mode == "officialEmbed" && got.Post.Video.EmbedToken != "legacy-embed-token" {
			t.Fatalf("officialEmbed mode lost the legacy embed token: %#v", got.Post.Video)
		}
		if mode == "linkOnly" && (got.Post.Video.EmbedToken != "" || got.Post.Video.EmbedExpiresAt != nil) {
			t.Fatalf("linkOnly exposed legacy embed capability: %#v", got.Post.Video)
		}
	}
	native := publicEntryForMode(entry, "note1", "nativeProxy")
	if native.Post.Video.EmbedToken != "" || native.Post.Videos[0].PlaybackURL == "" {
		t.Fatalf("nativeProxy did not expose only native playback: %#v", native.Post)
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

func TestCapabilitiesReflectsAdminRolePolicyWithoutPluginRoleSettings(t *testing.T) {
	h := plugintest.New(t).WithName("fxtwitter-preview").WithConfig(map[string]any{"externalFetchEnabled": true})
	routes := h.Routes(Plugin)
	for _, tc := range []struct {
		name string
		use  bool
	}{
		{name: "admin roles grants", use: true},
		{name: "admin roles denies", use: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := routes.Call(t, "POST /capabilities", plugintest.Request{
				UserID: "u1", CheckedRolePolicies: map[string]any{
					"canUseFxTwitterPreview":      tc.use,
					"fxTwitterColdFetchesPerHour": 100,
					"fxTwitterColdFetchesPerDay":  600,
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if got := result.(map[string]any)["canUseFxTwitterPreview"]; got != tc.use {
				t.Fatalf("capability = %v, want host admin/roles policy %v", got, tc.use)
			}
		})
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
	if a.actor != "u1" || a.endpoint != "notes/show" || !strings.Contains(mustJSON(t, result), "https://platform.twitter.com/embed/index.html") {
		t.Fatalf("result=%#v actor=%q endpoint=%q", result, a.actor, a.endpoint)
	}

	a.body = `{"id":"note1","text":"https://x.com/a/status/9999999999"}`
	if _, err := routes.Call(t, "POST /embed/shell", plugintest.Request{UserID: "u1", Body: body, CheckedRolePolicies: allowPreviewPolicies()}); err == nil {
		t.Fatal("embed shell accepted a note/post mismatch")
	}
}

func TestEmbedShellBuildsCompletePostURLFromCachedQuotedVideo(t *testing.T) {
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
			Video: &PublicVideo{Provider: "x", PosterURL: "https://pbs.twimg.com/quote.jpg"},
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
	if !strings.Contains(encoded, `https://platform.twitter.com/embed/index.html?`) || !strings.Contains(encoded, `id=2234567890`) {
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
	if _, err := routes.Call(t, "POST /embed/shell", plugintest.Request{UserID: "u1", Body: body, CheckedRolePolicies: allowPreviewPolicies()}); err == nil {
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
	_, err := routes.Call(t, "GET /media/:postId/:kind/:index", plugintest.Request{
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

func TestMediaRouteAllowsAuthorizedYouTubePosterFallbackForMainAndQuote(t *testing.T) {
	db := migratedDB(t)
	cfg := testConfig()
	s := newStore(db, cfg)
	now := time.Now().UTC()
	lease, err := s.AcquireFetch(context.Background(), "1234567890", "u1", now)
	if err != nil || lease == nil {
		t.Fatalf("lease = %#v, %v", lease, err)
	}
	poster := "https://img.youtube.com/vi/dQw4w9WgXcQ/hqdefault.jpg"
	post := PublicPost{
		ID: "1234567890", Text: "main", Author: PublicAuthor{Name: "A", ScreenName: "a"}, HasVideo: true,
		Video:  &PublicVideo{Provider: "external", PosterURL: poster},
		Videos: []PublicVideoMetadata{{Provider: "external", PosterURL: poster}},
		Quote: &PublicQuotedPost{
			ID: "2234567890", Text: "quote", Author: PublicAuthor{Name: "Q", ScreenName: "q"}, HasVideo: true,
			Video:  &PublicVideo{Provider: "external", PosterURL: poster},
			Videos: []PublicVideoMetadata{{Provider: "external", PosterURL: poster}},
		},
	}
	if _, err := s.CompleteReady(context.Background(), *lease, post, now); err != nil {
		t.Fatal(err)
	}
	a := &stubAPI{body: `{"id":"note1","text":"https://x.com/a/status/1234567890"}`}
	var outbound int
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		outbound++
		if r.URL.String() != poster {
			return nil, errors.New("unexpected poster URL")
		}
		return response(http.StatusOK, "image/jpeg", string(tinyImage(t, "jpeg"))), nil
	})}
	routes := plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).WithAPI(a).WithHTTPClient(client).WithConfig(map[string]any{"externalFetchEnabled": true}).Routes(Plugin)
	for _, kind := range []string{"poster", "quote-poster"} {
		requestCtx, cancel := context.WithCancel(context.Background())
		result, err := routes.Call(t, "GET /media/:postId/:kind/:index", plugintest.Request{
			Ctx: requestCtx, UserID: "u1", Params: map[string]string{"postId": "1234567890", "kind": kind, "index": "0"},
			Query: map[string]string{"noteId": "note1"}, CheckedRolePolicies: allowPreviewPolicies(),
		})
		cancel()
		if err != nil {
			t.Fatalf("%s poster: %v", kind, err)
		}
		blob, ok := result.(plugin.Blob)
		if !ok || blob.CacheControl != "private, no-store" || blob.ContentType != "image/jpeg" {
			t.Fatalf("%s poster response = %#v", kind, result)
		}
	}
	if outbound != 2 {
		t.Fatalf("authorized main/quote poster fetches = %d, want 2", outbound)
	}
}

func TestPublishedMediaPrepareRequiresAuthorizationAndGETUsesRegistryOnly(t *testing.T) {
	db := migratedDB(t)
	cfg := testConfig()
	s := newStore(db, cfg)
	mediaCache(t, s, time.Now().UTC(), "https://pbs.twimg.com/media/public-prepare.png")
	a := &stubAPI{body: `{"id":"note1","text":"https://x.com/a/status/1234567890"}`}
	body := `{"noteId":"note1","postId":"1234567890","kind":"image","index":0}`
	var outbound int
	imageBytes := tinyImage(t, "png")
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		outbound++
		if r.URL.String() != "https://pbs.twimg.com/media/public-prepare.png" {
			return nil, errors.New("unexpected source URL")
		}
		return response(http.StatusOK, "image/png", string(imageBytes)), nil
	})}
	routes := plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).WithAPI(a).WithHTTPClient(client).WithConfig(map[string]any{"externalFetchEnabled": true}).Routes(Plugin)
	prepareCtx, cancelPrepare := context.WithCancel(context.Background())
	prepared, err := routes.Call(t, "POST /media/prepare", plugintest.Request{
		Ctx: prepareCtx, UserID: "u1", Body: body, CheckedRolePolicies: allowPreviewPolicies(),
	})
	cancelPrepare()
	if err != nil {
		t.Fatal(err)
	}
	preparedJSON, err := json.Marshal(prepared)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		URL       string    `json:"url"`
		Size      int64     `json:"size"`
		ExpiresAt time.Time `json:"expiresAt"`
	}
	if err := json.Unmarshal(preparedJSON, &result); err != nil || result.URL == "" || result.Size != int64(len(imageBytes)) || !result.ExpiresAt.After(time.Now().UTC()) {
		t.Fatalf("prepare result = %s, err=%v", preparedJSON, err)
	}
	parts := strings.Split(strings.Trim(result.URL, "/"), "/")
	if len(parts) != 5 || parts[0] != "api" || parts[1] != "plugin" || parts[2] != "fxtwitter-preview" || parts[3] != "public-media" {
		t.Fatalf("unexpected public media path %q", result.URL)
	}
	filename := parts[4]
	if _, _, ok := parsePublishedMediaFilename(filename); !ok || !strings.HasSuffix(filename, ".png") {
		t.Fatalf("public media filename lacks strict token.extension form: %q", filename)
	}
	publicCtx, cancelPublic := context.WithCancel(context.Background())
	public, err := routes.Call(t, "GET /public-media/:filename", plugintest.Request{
		Ctx: publicCtx, Params: map[string]string{"filename": filename},
	})
	cancelPublic()
	if err != nil {
		t.Fatal(err)
	}
	blob, ok := public.(plugin.Blob)
	if !ok || blob.ContentType != "image/png" || !bytes.Equal(blob.Body, imageBytes) || blob.WriteTimeout != mediaResponseWriteTimeout || !strings.HasPrefix(blob.CacheControl, "public, max-age=") {
		t.Fatalf("public media response = %#v", public)
	}
	if outbound != 1 {
		t.Fatalf("prepare/GET made %d upstream requests, want one prepare fetch and registry-only GET", outbound)
	}
}

func TestPublishedMediaPrepareUsesPrivateRouteForValidatedYouTubePoster(t *testing.T) {
	db := migratedDB(t)
	cfg := testConfig()
	s := newStore(db, cfg)
	now := time.Now().UTC()
	lease, err := s.AcquireFetch(context.Background(), "1234567890", "u1", now)
	if err != nil || lease == nil {
		t.Fatalf("lease = %#v, %v", lease, err)
	}
	poster := "https://img.youtube.com/vi/dQw4w9WgXcQ/hqdefault.jpg"
	post := PublicPost{
		ID: "1234567890", Author: PublicAuthor{Name: "A", ScreenName: "a"}, HasVideo: true,
		Video:  &PublicVideo{Provider: "external", PosterURL: poster, PosterExpiresAt: ptrTime(now.Add(time.Hour))},
		Videos: []PublicVideoMetadata{{Provider: "external", PosterURL: poster, PosterExpiresAt: ptrTime(now.Add(time.Hour))}},
	}
	if _, err := s.CompleteReady(context.Background(), *lease, post, now); err != nil {
		t.Fatal(err)
	}
	a := &stubAPI{body: `{"id":"note1","text":"https://x.com/a/status/1234567890"}`}
	var outbound int
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		outbound++
		return response(http.StatusOK, "image/jpeg", string(tinyImage(t, "jpeg"))), nil
	})}
	routes := plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).WithAPI(a).WithHTTPClient(client).WithConfig(map[string]any{"externalFetchEnabled": true}).Routes(Plugin)
	requestCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err = routes.Call(t, "POST /media/prepare", plugintest.Request{
		Ctx: requestCtx, UserID: "u1", CheckedRolePolicies: allowPreviewPolicies(),
		Body: `{"noteId":"note1","postId":"1234567890","kind":"poster","index":0}`,
	})
	_, code := plugin.ExtractStatusError(err)
	if code != "PRIVATE_MEDIA_ONLY" {
		t.Fatalf("validated YouTube poster prepare error = %v, want PRIVATE_MEDIA_ONLY", err)
	}
	if outbound != 0 {
		t.Fatalf("private YouTube fallback made %d public preparation fetches", outbound)
	}
}

func TestParsePublishedVideoPlaybackURLRequiresExactRelativeCapability(t *testing.T) {
	valid := videoPlaybackPath("1234567890", "quote", 2, "note1", "0123456789abcdef0123456789abcdef")
	if token, ok := parsePublishedVideoPlaybackURL(valid, "note1", "1234567890", "quote", 2); !ok || token != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("valid playback URL rejected: token=%q ok=%v", token, ok)
	}
	for _, invalid := range []string{
		"https://example.com" + valid,
		"//example.com" + valid,
		valid + "#fragment",
		valid + "&extra=1",
		strings.Replace(valid, "noteId=note1", "noteId=other", 1),
		strings.Replace(valid, "noteId=note1", "noteId=note1&noteId=note1", 1),
		strings.Replace(valid, "/quote/2?", "/quote/%32?", 1),
	} {
		if _, ok := parsePublishedVideoPlaybackURL(invalid, "note1", "1234567890", "quote", 2); ok {
			t.Errorf("invalid playback URL accepted: %q", invalid)
		}
	}
}

func ptrTime(value time.Time) *time.Time { return &value }

func TestPublishedMediaPublicGetMissDoesNotFetchUpstream(t *testing.T) {
	var outbound int
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		outbound++
		return nil, errors.New("public cache miss must not fetch upstream")
	})}
	routes := plugintest.New(t).WithName("fxtwitter-preview").WithDB(migratedDB(t)).WithHTTPClient(client).WithConfig(map[string]any{"externalFetchEnabled": true}).Routes(Plugin)
	requestCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := routes.Call(t, "GET /public-media/:filename", plugintest.Request{
		Ctx: requestCtx, Params: map[string]string{"filename": strings.Repeat("0", 32) + ".png"},
	}); err == nil {
		t.Fatal("unknown public media token was served")
	}
	if outbound != 0 {
		t.Fatalf("public cache miss made %d upstream requests", outbound)
	}
}

func TestPublishedMediaPrepareFailsClosedBeforeFetch(t *testing.T) {
	db := migratedDB(t)
	s := newStore(db, testConfig())
	mediaCache(t, s, time.Now().UTC(), "https://pbs.twimg.com/media/private-prepare.png")
	a := &stubAPI{body: `{"id":"note1","text":"https://x.com/another/status/2234567890"}`}
	var outbound int
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		outbound++
		return response(http.StatusOK, "image/png", string(tinyImage(t, "png"))), nil
	})}
	routes := plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).WithAPI(a).WithHTTPClient(client).WithConfig(map[string]any{"externalFetchEnabled": true}).Routes(Plugin)
	body := `{"noteId":"note1","postId":"1234567890","kind":"image","index":0}`
	tests := []struct {
		name     string
		userID   string
		policies map[string]any
	}{
		{name: "anonymous"},
		{name: "role denied", userID: "u1", policies: map[string]any{"canUseFxTwitterPreview": false, "fxTwitterColdFetchesPerHour": 100, "fxTwitterColdFetchesPerDay": 600}},
		{name: "note does not contain post", userID: "u1", policies: allowPreviewPolicies()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			requestCtx, cancel := context.WithCancel(context.Background())
			defer cancel()
			_, err := routes.Call(t, "POST /media/prepare", plugintest.Request{
				Ctx: requestCtx, UserID: tc.userID, Body: body, CheckedRolePolicies: tc.policies,
			})
			if err == nil {
				t.Fatal("unauthorized media preparation succeeded")
			}
		})
	}
	if outbound != 0 {
		t.Fatalf("unauthorized preparation made %d upstream requests", outbound)
	}
}

func TestPublishedMediaGetEnforcesActiveQuotaAndExpiry(t *testing.T) {
	db := migratedDB(t)
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("public GET must not fetch upstream")
		return nil, errors.New("unreachable")
	})}
	routes := plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).WithHTTPClient(client).WithConfig(map[string]any{"externalFetchEnabled": true}).Routes(Plugin)
	now := time.Now().UTC()
	media, err := processPublishedMediaRegistry.Publish("image/png", tinyImage(t, "png"), now)
	if err != nil {
		t.Fatal(err)
	}
	releases := make([]func(), 0, publishedMediaMaxActiveGets)
	defer func() {
		for _, release := range releases {
			release()
		}
	}()
	for range publishedMediaMaxActiveGets {
		_, release, state := processPublishedMediaRegistry.Acquire(media.Token, media.Extension, now)
		if state != publishedMediaFound {
			t.Fatalf("registry setup state = %d", state)
		}
		releases = append(releases, release)
	}
	requestCtx, cancel := context.WithCancel(context.Background())
	_, err = routes.Call(t, "GET /public-media/:filename", plugintest.Request{
		Ctx: requestCtx, Params: map[string]string{"filename": media.Token + "." + media.Extension},
	})
	cancel()
	if err == nil {
		t.Fatal("public GET bypassed active reader limit")
	}
	for _, release := range releases {
		release()
	}
	releases = nil
	requestCtx, cancel = context.WithCancel(context.Background())
	_, err = routes.Call(t, "GET /public-media/:filename", plugintest.Request{
		Ctx: requestCtx, Params: map[string]string{"filename": media.Token + "." + media.Extension},
	})
	cancel()
	if err != nil {
		t.Fatalf("public GET failed after quota release: %v", err)
	}
	expired, err := processPublishedMediaRegistry.Publish("image/png", []byte("unique expired registry fixture"), now.Add(-publishedMediaTTL-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	requestCtx, cancel = context.WithCancel(context.Background())
	_, err = routes.Call(t, "GET /public-media/:filename", plugintest.Request{
		Ctx: requestCtx, Params: map[string]string{"filename": expired.Token + "." + expired.Extension},
	})
	cancel()
	if err == nil {
		t.Fatal("expired public media token was served")
	}
}

func TestExternalFetchKillSwitchStopsEmbedBeforeNativeLookup(t *testing.T) {
	a := &stubAPI{body: `{"id":"note1","text":"https://x.com/a/status/1234567890"}`}
	h := plugintest.New(t).WithName("fxtwitter-preview").WithDB(migratedDB(t)).WithAPI(a).WithConfig(map[string]any{})
	routes := h.Routes(Plugin)
	body := `{"noteId":"note1","postId":"1234567890","token":"0123456789abcdef0123456789abcdef","theme":"dark","lang":"ja","nonce":"0123456789abcdef0123456789abcdef"}`
	if _, err := routes.Call(t, "POST /embed/shell", plugintest.Request{UserID: "u1", Body: body, CheckedRolePolicies: allowPreviewPolicies()}); err == nil {
		t.Fatal("external-fetch kill switch served an embed shell")
	}
	if a.endpoint != "" {
		t.Fatalf("external-fetch kill switch called native API %q", a.endpoint)
	}
}
