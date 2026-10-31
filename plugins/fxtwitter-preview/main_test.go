// SPDX-FileCopyrightText: syuilo and misskey-project
// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"sync/atomic"
	"testing"

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
	t.Fatalf("PostgreSQL is required: %v", err)
}

func migratedDB(t *testing.T) *sql.DB {
	t.Helper()
	db := testDB(t)
	plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).WithConfig(map[string]any{}).Routes(Plugin)
	return db
}
