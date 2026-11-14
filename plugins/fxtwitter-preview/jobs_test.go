// SPDX-FileCopyrightText: syuilo and misskey-project
// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elythia-network/elythia/plugin/plugintest"
)

func TestFetchJobUsesMockFxAndCompletesReady(t *testing.T) {
	db := migratedDB(t)
	var requests atomic.Int32
	h := plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).
		WithConfig(map[string]any{"externalFetchEnabled": true}).
		WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			requests.Add(1)
			if r.URL.String() != fxEndpointPrefix+"1234567890" {
				t.Fatalf("unexpected URL %s", r.URL)
			}
			return response(200, "application/json", `{"code":200,"status":{"id":"1234567890","text":"from mock","author":{"name":"Alice","screen_name":"alice"}}}`), nil
		})})
	cfg, err := loadConfig(h.Context())
	if err != nil {
		t.Fatal(err)
	}
	l, err := newStore(db, cfg).AcquireFetch(context.Background(), "1234567890", "u1", time.Now().UTC())
	if err != nil || l == nil {
		t.Fatalf("lease = %#v, %v", l, err)
	}
	jobs := h.Jobs(Plugin)
	if err := jobs.Run(t, "fetch-post", mustJSON(t, l)); err != nil {
		t.Fatal(err)
	}
	if err := jobs.Run(t, "fetch-post", mustJSON(t, l)); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatalf("duplicate job made %d Fx requests", requests.Load())
	}
	entry, err := newStore(db, cfg).ReadPublic(context.Background(), l.PostID)
	if err != nil || entry == nil || entry.State != "ready" || entry.Post.Text != "from mock" {
		t.Fatalf("entry = %#v, %v", entry, err)
	}
}

func TestRetentionJobDefaultNoopAndConfiguredDelete(t *testing.T) {
	db := migratedDB(t)
	_, err := db.Exec(`INSERT INTO audit_body_snapshot (post_id, body, content_hash, observed_at)
		VALUES ('1234567890', 'old', 'hash-old', now() - interval '2 days')`)
	if err != nil {
		t.Fatal(err)
	}
	defaultHarness := plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).WithConfig(map[string]any{})
	if err := defaultHarness.Jobs(Plugin).Run(t, "audit-retention", "null"); err != nil {
		t.Fatal(err)
	}
	var count int
	_ = db.QueryRow(`SELECT count(*) FROM audit_body_snapshot`).Scan(&count)
	if count != 1 {
		t.Fatalf("default retention deleted %d rows", 1-count)
	}

	configured := plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).WithConfig(map[string]any{"auditRetentionDays": 1})
	if err := configured.Jobs(Plugin).Run(t, "audit-retention", "null"); err != nil {
		t.Fatal(err)
	}
	_ = db.QueryRow(`SELECT count(*) FROM audit_body_snapshot`).Scan(&count)
	if count != 0 {
		t.Fatalf("configured retention left %d old rows", count)
	}
	var reason string
	if err := db.QueryRow(`SELECT reason FROM audit_delete_log WHERE target = 'retention'`).Scan(&reason); err != nil || reason == "" {
		t.Fatalf("retention log = %q, %v", reason, err)
	}
}
