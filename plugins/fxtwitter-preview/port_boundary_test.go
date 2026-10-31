package fxtwitterpreview

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/elythia-network/elythia/plugin"
	"github.com/elythia-network/elythia/plugin/plugintest"
)

func TestPortClientReasonFixtureBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		httpStatus       int
	}{
		{"root_deleted", `{"code":200,"status":{"type":"tombstone","provider":"twitter","reason":"deleted","message":"gone"}}`, "deleted", 200},
		{"root_suspended", `{"code":403,"status":{"type":"tombstone","provider":"twitter","reason":"suspended","message":"gone"}}`, "suspended", 403},
		{"root_private", `{"code":401,"status":{"type":"tombstone","provider":"twitter","reason":"private","message":"gone"}}`, "private", 401},
		{"root_blocked", `{"code":200,"status":{"type":"tombstone","provider":"twitter","reason":"blocked","message":"gone"}}`, "blocked", 200},
		{"root_unavailable", `{"code":200,"status":{"type":"tombstone","provider":"twitter","reason":"unavailable","message":"gone"}}`, "unavailable", 200},
		{"bare404", `{}`, "unknown", 404}, {"bare410", `{}`, "unknown", 410}, {"bare401", `{}`, "unknown", 401}, {"bare403", `{}`, "unknown", 403},
		{"undocumented", `{"code":200,"status":{"type":"tombstone","provider":"twitter","reason":"terms_withheld"}}`, "unknown", 200},
		{"missing_reason", `{"code":200,"status":{"type":"tombstone","provider":"twitter"}}`, "unknown", 200},
		{"invalid_provider", `{"code":200,"status":{"type":"tombstone","provider":"bluesky","reason":"deleted"}}`, "unknown", 200},
		{"mismatching_identity", `{"code":200,"status":{"type":"tombstone","provider":"twitter","id":"9999999999","reason":"deleted"}}`, "unknown", 200},
		{"invalid_schema_reason", `{"code":200,"status":{"type":"tombstone","provider":"twitter","reason":{}}}`, "unknown", 200},
		{"invalid_envelope", `{"code":500,"status":{"type":"tombstone","provider":"twitter","reason":"deleted"}}`, "unknown", 200},
		{"oversized", `{"code":200,"status":{"type":"tombstone","provider":"twitter","reason":"deleted","message":"` + strings.Repeat("x", 1024) + `"}}`, "unknown", 200},
		{"network", "", "fetch_failed", 0}, {"rate_limit", `{}`, "fetch_failed", 429},
		{"upstream_error", `{"code":500,"status":{"type":"tombstone","provider":"twitter","reason":"deleted"}}`, "fetch_failed", 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newFxClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				if tc.httpStatus == 0 {
					return nil, fmt.Errorf("fixture network error")
				}
				return response(tc.httpStatus, "application/json", tc.body), nil
			})}, testConfig())
			_, err := c.Fetch(context.Background(), "1234567890")
			if err == nil {
				t.Fatal("unavailable source accepted")
			}
			if got := unavailableReason(errorClass(err)); got != tc.want {
				t.Fatalf("reason=%s want=%s class=%s", got, tc.want, errorClass(err))
			}
		})
	}
}

// Catches dropping the lease transition guard independently of the upgrade migration.
func TestPortLegacyPermanentStateCannotAcquireStaleFallback(t *testing.T) {
	s, now := testStore(t)
	ctx := context.Background()
	id := "1234567890"
	l, err := s.AcquireFetch(ctx, id, "u1", now)
	if err != nil || l == nil {
		t.Fatal(err)
	}
	if _, err = s.CompleteReady(ctx, *l, PublicPost{ID: id, Text: "legacy", Author: PublicAuthor{Name: "A", ScreenName: "a"}}, now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE public_post_cache SET state='unavailable',payload=NULL,last_error_class='schema' WHERE post_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	l, err = s.AcquireFetch(ctx, id, "u1", now.Add(time.Hour))
	if err != nil || l == nil {
		t.Fatal(err)
	}
	if _, err = s.CompleteUnavailable(ctx, *l, "network", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadPublic(ctx, id)
	if err != nil || got.Post != nil || got.State != "unavailable" {
		t.Fatalf("legacy permanent state resurrected: %#v %v", got, err)
	}
}

// Existing kill switch behavior must protect legacy descriptors and queued work too.
func TestPortExternalOffBlocksCachedMediaPreviewEmbedAndQueuedJob(t *testing.T) {
	db := migratedDB(t)
	s := newStore(db, testConfig())
	now := time.Now().UTC()
	mediaCache(t, s, now, "https://pbs.twimg.com/a.png")
	a := &stubAPI{body: `{"id":"note1","text":"https://x.com/a/status/1234567890"}`}
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("must not dial") })}
	h := plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).WithAPI(a).WithHTTPClient(client).WithConfig(map[string]any{"externalFetchEnabled": false})
	routes := h.Routes(Plugin)
	got, err := routes.Call(t, "POST /preview", plugintest.Request{UserID: "u1", Body: `{"noteId":"note1","postId":"1234567890"}`})
	if err != nil || !strings.Contains(mustJSON(t, got), `"state":"disabled"`) {
		t.Fatalf("preview: %v %v", got, err)
	}
	_, err = routes.Call(t, "GET /media/:postId/:kind/:index", plugintest.Request{UserID: "u1", Params: map[string]string{"postId": "1234567890", "kind": "image", "index": "0"}, Query: map[string]string{"noteId": "note1"}})
	if err == nil {
		t.Fatal("disabled media accepted cached descriptor")
	}
	_, err = routes.Call(t, "POST /embed/shell", plugintest.Request{UserID: "u1", Body: `{"noteId":"note1","postId":"1234567890","token":"0123456789abcdef0123456789abcdef","theme":"dark","lang":"ja","nonce":"0123456789abcdef0123456789abcdef"}`})
	if err == nil {
		t.Fatal("disabled embed accepted")
	}
	l, err := s.AcquireFetch(context.Background(), "2234567890", "u1", now)
	if err != nil || l == nil {
		t.Fatal(err)
	}
	if err = h.Jobs(Plugin).Run(t, "fetch-post", mustJSON(t, l)); err != nil {
		t.Fatal(err)
	}
	if calls != 0 || a.endpoint != "" || len(h.Enqueues()) != 0 {
		t.Fatalf("OFF escaped: outbound=%d native=%q queued=%d", calls, a.endpoint, len(h.Enqueues()))
	}
}

func TestPortMediaRechecksACLAfterSuccessfulViewerRequest(t *testing.T) {
	db := migratedDB(t)
	s := newStore(db, testConfig())
	mediaCache(t, s, time.Now().UTC(), "https://pbs.twimg.com/a.png")
	a := &stubAPI{body: `{"id":"note1","text":"https://x.com/a/status/1234567890"}`}
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return response(200, "image/png", string(tinyImage(t, "png"))), nil
	})}
	routes := plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).WithAPI(a).WithHTTPClient(client).WithConfig(map[string]any{"externalFetchEnabled": true}).Routes(Plugin)
	req := plugintest.Request{UserID: "u1", Params: map[string]string{"postId": "1234567890", "kind": "image", "index": "0"}, Query: map[string]string{"noteId": "note1"}}
	result, err := routes.Call(t, "GET /media/:postId/:kind/:index", req)
	if err != nil {
		t.Fatal(err)
	}
	if blob, ok := result.(plugin.Blob); !ok || blob.CacheControl != "private, no-store" {
		t.Fatalf("cache policy: %#v", result)
	}
	a.body = `{"id":"note1","text":"content no longer includes source"}`
	if _, err = routes.Call(t, "GET /media/:postId/:kind/:index", req); err == nil || calls != 1 {
		t.Fatalf("revoked access reused media: calls=%d err=%v", calls, err)
	}
}
