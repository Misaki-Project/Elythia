// SPDX-FileCopyrightText: syuilo and misskey-project
// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elythia-network/elythia/plugin/plugintest"
	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
)

var testSchemaSeq atomic.Uint64

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	var random [4]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("plugin_fxtwitter_test_%d_%s_%d", os.Getpid(), hex.EncodeToString(random[:]), testSchemaSeq.Add(1))
	base := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		envOr("TEST_DB_HOST", "localhost"), envOr("TEST_DB_PORT", "5432"),
		envOr("TEST_DB_USER", "mk"), envOr("TEST_DB_PASS", "mk"), envOr("TEST_DB_NAME", "misskey_test"))
	admin, err := sql.Open("pgx", base)
	if err != nil {
		dbUnavailable(t, err)
	}
	defer admin.Close() //nolint:errcheck
	if err := admin.Ping(); err != nil {
		dbUnavailable(t, err)
	}
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(`CREATE SCHEMA ` + quoted); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", base+" search_path="+schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		if cleanup, err := sql.Open("pgx", base); err == nil {
			_, _ = cleanup.Exec(`DROP SCHEMA IF EXISTS ` + quoted + ` CASCADE`)
			_ = cleanup.Close()
		}
	})
	return db
}

func dbUnavailable(t *testing.T, err error) {
	t.Helper()
	if os.Getenv("MK_PLUGIN_TESTS_REQUIRE_DB") != "" {
		t.Fatalf("PostgreSQL is required: %v", err)
	}
	t.Skipf("PostgreSQL unavailable: %v", err)
}

func migratedDB(t *testing.T) *sql.DB {
	t.Helper()
	db := testDB(t)
	plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).WithConfig(map[string]any{}).Routes(Plugin)
	return db
}

func TestHostMigrationPathLeavesExistingHigherLedgerEntryUntouched(t *testing.T) {
	db := testDB(t)
	stamp := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version bigint PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (6, $1)`, stamp); err != nil {
		t.Fatal(err)
	}
	plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).WithConfig(map[string]any{}).Routes(Plugin)
	var count int
	var appliedAt time.Time
	if err := db.QueryRowContext(context.Background(), `SELECT count(*), max(applied_at) FILTER (WHERE version = 6) FROM schema_migrations`).Scan(&count, &appliedAt); err != nil {
		t.Fatal(err)
	}
	if count != 6 || !appliedAt.Equal(stamp) {
		t.Fatalf("migration ledger count=%d version6 appliedAt=%s; want 6 and unchanged %s", count, appliedAt, stamp)
	}
	var missing int
	if err := db.QueryRow(`SELECT count(*) FROM generate_series(1, 6) AS v WHERE NOT EXISTS (SELECT 1 FROM schema_migrations WHERE version = v)`).Scan(&missing); err != nil || missing != 0 {
		t.Fatalf("missing migration ledger entries=%d, err=%v", missing, err)
	}
}
