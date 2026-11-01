// SPDX-FileCopyrightText: syuilo and misskey-project
// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

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
	result, err := routes.Call(t, "POST /embed/shell", plugintest.Request{UserID: "u1", Body: body})
	if err != nil {
		t.Fatal(err)
	}
	if a.actor != "u1" || a.endpoint != "notes/show" || !strings.Contains(mustJSON(t, result), "https://platform.twitter.com/embed/index.html") {
		t.Fatalf("result=%#v actor=%q endpoint=%q", result, a.actor, a.endpoint)
	}

	a.body = `{"id":"note1","text":"https://x.com/a/status/9999999999"}`
	if _, err := routes.Call(t, "POST /embed/shell", plugintest.Request{UserID: "u1", Body: body}); err == nil {
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
	result, err := routes.Call(t, "POST /embed/shell", plugintest.Request{UserID: "u1", Body: body})
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
		UserID: "u1",
		Params: map[string]string{"postId": "1234567890", "kind": "image", "index": "0"},
		Query:  map[string]string{"noteId": "note1"},
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
