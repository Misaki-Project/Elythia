// SPDX-FileCopyrightText: syuilo and misskey-project
// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import (
	"context"
	"errors"
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
		WithCheckedUserRolePolicies(map[string]any{"canUseFxTwitterPreview": true}, nil).
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

func TestFetchJobRechecksIssuerPermissionBeforeOutboundRequest(t *testing.T) {
	db := migratedDB(t)
	var requests atomic.Int32
	h := plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).
		WithConfig(map[string]any{"externalFetchEnabled": true}).
		WithCheckedUserRolePolicies(map[string]any{"canUseFxTwitterPreview": false}, nil).
		WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			requests.Add(1)
			return response(200, "application/json", `{}`), nil
		})})
	cfg, err := loadConfig(h.Context())
	if err != nil {
		t.Fatal(err)
	}
	l, err := newStore(db, cfg).AcquireFetch(context.Background(), "1234567890", "issuer-1", time.Now().UTC())
	if err != nil || l == nil || l.ActorID != "issuer-1" {
		t.Fatalf("lease = %#v, %v", l, err)
	}
	if err := h.Jobs(Plugin).Run(t, "fetch-post", mustJSON(t, l)); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 0 {
		t.Fatalf("revoked queued fetch made %d upstream requests", requests.Load())
	}
	entry, err := newStore(db, cfg).ReadPublic(context.Background(), l.PostID)
	if err != nil || entry == nil || entry.State == "pending" || entry.Post != nil {
		t.Fatalf("revoked lease was not released: entry=%#v err=%v", entry, err)
	}
	allowed, err := newStore(db, cfg).AcquireFetchWithBudget(context.Background(), l.PostID, "issuer-2", coldFetchBudget{PerHour: 10, PerDay: 10}, time.Now().UTC())
	if err != nil || allowed == nil {
		t.Fatalf("allowed actor could not retry immediately after revoke: lease=%#v err=%v", allowed, err)
	}
}

func TestRetentionJobDefaultNoopAndConfiguredDelete(t *testing.T) {
	db := migratedDB(t)
	_, err := db.Exec(`INSERT INTO audit_body_snapshot (post_id, body, content_hash, observed_at)
		SELECT '1234567890', 'old-' || n, 'hash-old-' || n, now() - interval '2 days'
		FROM generate_series(1, 2001) AS n`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO audit_body_snapshot (post_id, body, content_hash, observed_at)
		VALUES ('1234567890', 'fresh', 'hash-fresh', now())`)
	if err != nil {
		t.Fatal(err)
	}
	defaultHarness := plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).WithConfig(map[string]any{})
	if err := defaultHarness.Jobs(Plugin).Run(t, "audit-retention", "null"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM audit_body_snapshot`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2002 {
		t.Fatalf("default retention changed row count to %d; want 2002", count)
	}

	configured := plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).WithConfig(map[string]any{"auditRetentionDays": 1})
	if err := configured.Jobs(Plugin).Run(t, "audit-retention", "null"); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM audit_body_snapshot`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("configured retention left %d rows; want only the fresh row", count)
	}
	var freshCount int
	if err := db.QueryRow(`SELECT count(*) FROM audit_body_snapshot WHERE content_hash = 'hash-fresh'`).Scan(&freshCount); err != nil || freshCount != 1 {
		t.Fatalf("fresh audit row count=%d, err=%v; want it retained", freshCount, err)
	}
	var batchCount int
	var totalDeleted int64
	var maxBatch int64
	if err := db.QueryRow(`SELECT count(*), sum(deleted_count), max(deleted_count) FROM audit_delete_log WHERE target = 'retention'`).Scan(&batchCount, &totalDeleted, &maxBatch); err != nil {
		t.Fatal(err)
	}
	if batchCount < 2 || totalDeleted != 2001 || maxBatch > auditRetentionBatchSize {
		t.Fatalf("retention batches=%d deleted=%d max batch=%d; want multiple bounded batches totaling 2001", batchCount, totalDeleted, maxBatch)
	}
	var reason string
	if err := db.QueryRow(`SELECT reason FROM audit_delete_log WHERE target = 'retention'`).Scan(&reason); err != nil || reason == "" {
		t.Fatalf("retention log = %q, %v", reason, err)
	}
}

func TestRetentionJobHonorsCancellationAndPropagatesDatabaseErrors(t *testing.T) {
	db := migratedDB(t)
	_, err := db.Exec(`INSERT INTO audit_body_snapshot (post_id, body, content_hash, observed_at)
		VALUES ('1234567890', 'old', 'hash-old', now() - interval '2 days')`)
	if err != nil {
		t.Fatal(err)
	}
	h := plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).WithConfig(map[string]any{"auditRetentionDays": 1})
	handler := h.Jobs(Plugin).Handlers["audit-retention"]
	if handler == nil {
		t.Fatal("audit-retention handler was not registered")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := handler(canceled, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled retention job error=%v; want context.Canceled", err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM audit_body_snapshot`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("canceled job left %d audit rows, err=%v; want 1", count, err)
	}
	if _, err := db.Exec(`DROP TABLE audit_body_snapshot`); err != nil {
		t.Fatal(err)
	}
	if err := handler(context.Background(), nil); err == nil {
		t.Fatal("retention job swallowed the database error")
	}
}
