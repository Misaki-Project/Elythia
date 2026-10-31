package fxtwitterpreview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/elythia-network/elythia/plugin/plugintest"
)

// Catches retaining public fallback after a permanent rejection, even on a later transport failure.
func TestPortPermanentFailureNeverResurrectsPublicOrCapabilities(t *testing.T) {
	for _, class := range []string{"upstream_not_found", "upstream_private", "schema", "tombstone_deleted"} {
		t.Run(class, func(t *testing.T) {
			s, now := testStore(t)
			ctx := context.Background()
			id := "1234567890"
			post := PublicPost{ID: id, Text: "retained audit", Author: PublicAuthor{ID: "11", Name: "A", ScreenName: "a", AvatarURL: "https://pbs.twimg.com/a.jpg"}, Video: &PublicVideo{Provider: "x", PosterURL: "https://pbs.twimg.com/p.jpg"}, HasVideo: true}
			l, err := s.AcquireFetch(ctx, id, "u1", now)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.CompleteReady(ctx, *l, post, now); err != nil {
				t.Fatal(err)
			}
			ready, _ := s.ReadPublic(ctx, id)
			token := ready.Post.Video.EmbedToken
			for i, failure := range []string{class, "network", "upstream_5xx"} {
				at := now.Add(time.Duration(i+1) * time.Hour)
				l, err = s.AcquireFetch(ctx, id, "u1", at)
				if err != nil || l == nil {
					t.Fatalf("lease: %v %v", l, err)
				}
				if _, err = s.CompleteUnavailable(ctx, *l, failure, at); err != nil {
					t.Fatal(err)
				}
				var retired bool
				if err = s.db.QueryRow(`SELECT last_success_payload IS NULL AND last_success_at IS NULL FROM public_post_cache WHERE post_id=$1`, id).Scan(&retired); err != nil || !retired {
					t.Fatalf("untrusted fallback metadata retained: retired=%v err=%v", retired, err)
				}
				got, err := s.ReadPublic(ctx, id)
				if err != nil || got.State != "unavailable" || got.Post != nil || got.Stale {
					t.Fatalf("public resurrected after %s: %#v %v", failure, got, err)
				}
				if _, err = s.ResolveMedia(ctx, id, "avatar", "0", at); err == nil {
					t.Fatal("retired media capability accepted")
				}
				if err = s.ResolveEmbed(ctx, id, token, at); err == nil {
					t.Fatal("retired embed capability accepted")
				}
			}
			audit, err := s.ListAudit(ctx, id)
			if err != nil || len(audit) != 1 || audit[0].Body != "retained audit" {
				t.Fatalf("audit changed: %#v %v", audit, err)
			}
			at := now.Add(4 * time.Hour)
			l, err = s.AcquireFetch(ctx, id, "u1", at)
			if err != nil || l == nil {
				t.Fatal(err)
			}
			if _, err = s.CompleteReady(ctx, *l, post, at); err != nil {
				t.Fatal(err)
			}
			got, err := s.ReadPublic(ctx, id)
			if err != nil || got.State != "ready" || got.Post == nil {
				t.Fatalf("new success rejected: %#v %v", got, err)
			}
		})
	}
}

// Catches upgrade leaving legacy pending/unavailable fallback able to reappear after a network failure.
func TestPortMigrationRetiresOnlyNonReadyLegacyFallback(t *testing.T) {
	db := testDB(t)
	old := Plugin
	old.Migrations = old.Migrations[:5]
	plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).Routes(old)
	ctx := context.Background()
	now := time.Now().UTC()
	s := newStore(db, testConfig())
	for i, state := range []string{"ready", "unavailable", "pending"} {
		id := fmt.Sprintf("123456789%d", i)
		post := PublicPost{ID: id, Text: "legacy audit", Author: PublicAuthor{ID: "11", Name: "A", ScreenName: "a"}, Images: []PublicImage{}}
		l, err := s.AcquireFetch(ctx, id, "u1", now)
		if err != nil || l == nil {
			t.Fatal(err)
		}
		if _, err = s.CompleteReady(ctx, *l, post, now); err != nil {
			t.Fatal(err)
		}
		if state != "ready" {
			if _, err = db.Exec(`UPDATE public_post_cache SET state=$1,payload=NULL,last_error_class='upstream_not_found',next_fetch_at=$2 WHERE post_id=$3`, state, now, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).Routes(Plugin)
	for i, state := range []string{"ready", "unavailable", "pending"} {
		id := fmt.Sprintf("123456789%d", i)
		var fallback bool
		if err := db.QueryRow(`SELECT last_success_payload IS NOT NULL FROM public_post_cache WHERE post_id=$1`, id).Scan(&fallback); err != nil {
			t.Fatal(err)
		}
		if fallback != (state == "ready") {
			t.Errorf("legacy %s fallback retained=%v", state, fallback)
		}
		if state != "ready" {
			l, err := s.AcquireFetch(ctx, id, "u1", now.Add(time.Hour))
			if err != nil || l == nil {
				t.Fatal(err)
			}
			if _, err = s.CompleteUnavailable(ctx, *l, "network", now.Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			got, err := s.ReadPublic(ctx, id)
			if err != nil || got.Post != nil {
				t.Fatalf("legacy resurrected: %#v %v", got, err)
			}
		}
	}
	for table, want := range map[string]int{"audit_body_snapshot": 3, "x_post": 3, "x_user": 1} {
		var count int
		if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != want {
			t.Errorf("%s count=%d want=%d err=%v", table, count, want, err)
		}
	}
}

// Exercises the real HTTP parser, job completion and authorized route DTO, with hand-written upstream bodies.
func TestPortTombstoneReasonSurvivesClientStoreAndRoute(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		status           int
	}{
		{"deleted", `{"code":200,"status":{"type":"tombstone","provider":"twitter","reason":"deleted","id":"1234567890","message":"gone"}}`, "deleted", 200},
		{"suspended", `{"code":403,"status":{"type":"tombstone","provider":"twitter","reason":"suspended","message":"gone"}}`, "suspended", 403},
		{"private", `{"code":404,"status":{"type":"tombstone","provider":"twitter","reason":"private","message":"gone"}}`, "private", 404},
		{"blocked", `{"code":200,"status":{"type":"tombstone","provider":"twitter","reason":"blocked","message":"gone"}}`, "blocked", 200},
		{"unavailable", `{"code":200,"status":{"type":"tombstone","provider":"twitter","reason":"unavailable","message":"gone"}}`, "unavailable", 200},
		{"unknown_reason", `{"code":200,"status":{"type":"tombstone","provider":"twitter","reason":"terms_withheld"}}`, "unknown", 200},
		{"missing_reason", `{"code":200,"status":{"type":"tombstone","provider":"twitter"}}`, "unknown", 200},
		{"other_provider", `{"code":200,"status":{"type":"tombstone","provider":"bluesky","reason":"deleted"}}`, "unknown", 200},
		{"wrong_identity", `{"code":200,"status":{"type":"tombstone","provider":"twitter","id":"9999999999","reason":"deleted"}}`, "unknown", 200},
		{"bare404", `{}`, "unknown", 404}, {"bare410", `{}`, "unknown", 410}, {"bare401", `{}`, "unknown", 401}, {"bare403", `{}`, "unknown", 403},
		{"bad_json", `{"code":200`, "unknown", 200}, {"network", "", "fetch_failed", 0}, {"upstream500", `{"code":500,"status":{"type":"tombstone","provider":"twitter","reason":"deleted"}}`, "fetch_failed", 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := newFxClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.String() != "https://api.fxtwitter.com/2/status/1234567890" {
					t.Fatalf("wrong URL: %s", r.URL)
				}
				if tc.status == 0 {
					return nil, errors.New("fixture network failure")
				}
				return response(tc.status, "application/json", tc.body), nil
			})}, testConfig())
			_, fetchErr := client.Fetch(context.Background(), "1234567890")
			if fetchErr == nil {
				t.Fatal("tombstone accepted as ready")
			}
			db := migratedDB(t)
			s := newStore(db, testConfig())
			now := time.Now().UTC()
			l, err := s.AcquireFetch(context.Background(), "1234567890", "u1", now)
			if err != nil || l == nil {
				t.Fatal(err)
			}
			if _, err = s.CompleteUnavailable(context.Background(), *l, errorClass(fetchErr), now); err != nil {
				t.Fatal(err)
			}
			a := &stubAPI{body: `{"id":"note1","text":"https://x.com/a/status/1234567890"}`}
			routes := plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).WithAPI(a).WithConfig(map[string]any{"externalFetchEnabled": true}).Routes(Plugin)
			got, err := routes.Call(t, "POST /preview", plugintest.Request{UserID: "u1", Body: `{"noteId":"note1","postId":"1234567890"}`})
			if err != nil {
				t.Fatal(err)
			}
			var dto map[string]any
			if err = json.Unmarshal([]byte(mustJSON(t, got)), &dto); err != nil {
				t.Fatal(err)
			}
			if dto["unavailableReason"] != tc.want || dto["state"] != "unavailable" || dto["post"] != nil {
				t.Fatalf("public DTO = %s, want reason %s", mustJSON(t, got), tc.want)
			}
			for _, secret := range []string{"fixture network failure", "message", "errorClass", "bodyFormat", "snapshot"} {
				if strings.Contains(mustJSON(t, got), secret) {
					t.Fatalf("public DTO leaked %s", secret)
				}
			}
		})
	}
}

func TestPortQuoteTombstonePreservesOnlyKnownReason(t *testing.T) {
	for _, reason := range []string{"deleted", "suspended", "private", "blocked", "unavailable", "terms_withheld", ""} {
		body := fmt.Sprintf(`{"code":200,"status":{"id":"1234567890","text":"main","author":{"name":"A","screen_name":"a"},"quote":{"type":"tombstone","provider":"twitter","reason":%q,"message":"internal upstream wording"}}}`, reason)
		post, err := parseFxResponse([]byte(body), "1234567890")
		if err != nil {
			t.Fatal(err)
		}
		dto := map[string]any{}
		if err = json.Unmarshal([]byte(mustJSON(t, publicEntry(&CacheEntry{Post: &post}, "note1"))), &dto); err != nil {
			t.Fatal(err)
		}
		want := reason
		if reason == "terms_withheld" || reason == "" {
			want = "unknown"
		}
		p := dto["post"].(map[string]any)
		if p["quoteUnavailableReason"] != want || p["quote"] != nil || strings.Contains(mustJSON(t, dto), "internal upstream wording") {
			t.Fatalf("quote tombstone: %s", mustJSON(t, dto))
		}
	}
}

func TestPortMalformedQuoteTombstonesRemainExplicitUnknown(t *testing.T) {
	for _, tc := range []struct {
		name, quote string
	}{
		{"object_reason", `{"type":"tombstone","provider":"twitter","reason":{},"message":"private upstream message","text":"private quote body"}`},
		{"numeric_provider", `{"type":"tombstone","provider":17,"reason":"deleted","message":"private upstream message","text":"private quote body"}`},
		{"object_id", `{"type":"tombstone","provider":"twitter","reason":"deleted","id":{},"message":"private upstream message","text":"private quote body"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"code":200,"status":{"id":"1234567890","text":"main","author":{"name":"A","screen_name":"a"},"quote":` + tc.quote + `}}`)
			post, err := parseFxResponse(body, "1234567890")
			if err != nil {
				t.Fatal(err)
			}
			if post.ID != "1234567890" || post.Text != "main" || post.Quote != nil {
				t.Fatalf("outer post or unavailable quote changed: %#v", post)
			}
			publicJSON := mustJSON(t, publicEntry(&CacheEntry{State: "ready", Post: &post}, "note1"))
			var dto struct {
				Post map[string]any `json:"post"`
			}
			if err := json.Unmarshal([]byte(publicJSON), &dto); err != nil {
				t.Fatal(err)
			}
			if dto.Post["quoteUnavailableReason"] != "unknown" || dto.Post["quote"] != nil {
				t.Fatalf("malformed tombstone lost explicit unknown: %s", publicJSON)
			}
			for _, forbidden := range []string{"private upstream message", "private quote body", `"message"`} {
				if strings.Contains(publicJSON, forbidden) {
					t.Fatalf("unavailable quote leaked %q: %s", forbidden, publicJSON)
				}
			}
		})
	}
}

func TestPortAuditRouteLabelsHistoricalNormalizedText(t *testing.T) {
	db := migratedDB(t)
	post, err := parseFxResponse([]byte(`{"code":200,"status":{"id":"1234567890","author":{"name":"A","screen_name":"a"},"raw_text":{"text":"&amp; https://t.co/x media","facets":[{"type":"url","indices":[6,20],"original":"https://t.co/x","replacement":"https://example.com/page"},{"type":"media","indices":[21,26],"original":"media"}]}}}`), "1234567890")
	if err != nil {
		t.Fatal(err)
	}
	if post.Text != "& https://example.com/page" {
		t.Fatalf("normalized text=%q", post.Text)
	}
	s := newStore(db, testConfig())
	now := time.Now().UTC()
	l, err := s.AcquireFetch(context.Background(), post.ID, "u1", now)
	if err != nil || l == nil {
		t.Fatal(err)
	}
	if _, err = s.CompleteReady(context.Background(), *l, post, now); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO audit_body_snapshot(post_id,body,content_hash,observed_at) VALUES('1234567890','historical normalized text','legacy-hash',$1)`, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	routes := plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).Routes(Plugin)
	result, err := routes.Call(t, "POST /admin/audit/list", plugintest.Request{UserID: "mod", Moderator: true, Body: `{"postId":"1234567890"}`})
	if err != nil {
		t.Fatal(err)
	}
	body := mustJSON(t, result)
	if strings.Count(body, `"bodyFormat":"normalized_text_v1"`) != 2 || !strings.Contains(body, `https://example.com/page`) || !strings.Contains(body, "historical normalized text") {
		t.Fatalf("audit label: %s", body)
	}
	if strings.Contains(mustJSON(t, publicEntry(&CacheEntry{Post: &post}, "note1")), "bodyFormat") {
		t.Fatal("audit format exposed publicly")
	}
}

func TestPortCompletePostEmbedAndLegacyYouTubeDenied(t *testing.T) {
	raw, err := buildEmbedURL("1234567890", "dark", "ja", "0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "https" || u.Host != "platform.twitter.com" || u.Path != "/embed/index.html" || u.Query().Get("id") != "1234567890" {
		t.Fatalf("not complete-post embed: %s", raw)
	}
	for _, key := range []string{"frame", "maxWidth", "width"} {
		if u.Query().Has(key) {
			t.Fatalf("video presentation parameter retained: %s", key)
		}
	}
	if playableVideo(&PublicVideo{Provider: "youtube", ProviderVideoID: "dQw4w9WgXcQ"}) {
		t.Fatal("legacy YouTube descriptor remains playable")
	}
}

func TestPortAuthenticatedImageCannotRemainInBrowserCache(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(200, "image/png", string(tinyImage(t, "png"))), nil
	})}
	blob, err := fetchMedia(context.Background(), client, "https://pbs.twimg.com/a.png", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if blob.CacheControl != "private, no-store" {
		t.Fatalf("authorized bytes remain reusable: %q", blob.CacheControl)
	}
}
