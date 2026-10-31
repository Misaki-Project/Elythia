package migrationhistory_test

import (
	"context"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elythia-network/elythia/internal/migrationhistory"
	"github.com/elythia-network/elythia/internal/testutil"
)

func canonicalSource(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "000117_user_account_created_at.up.sql"), []byte("SELECT 1;"), 0o600))
	return dir
}

func TestCheckLegacy117_IgnoresOtherSourcesAndStates(t *testing.T) {
	ctx := context.Background()
	source := canonicalSource(t)
	for _, tt := range []struct {
		name    string
		version uint
		dirty   bool
	}{
		{"fresh", 0, false},
		{"before canonical117", 116, false},
		{"after canonical117", 118, false},
		{"dirty117", 117, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assert.NoError(t, migrationhistory.CheckLegacy117(ctx, source, "invalid database URL", tt.version, tt.dirty))
		})
	}
	t.Run("custom117", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "000117_custom.up.sql"), []byte("SELECT 1;"), 0o600))
		assert.NoError(t, migrationhistory.CheckLegacy117(ctx, dir, "invalid database URL", 117, false))
	})
	t.Run("directory is not a migration", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(dir, "000117_user_account_created_at.up.sql"), 0o700))
		assert.NoError(t, migrationhistory.CheckLegacy117(ctx, dir, "invalid database URL", 117, false))
	})
}

func TestCheckLegacy117_FailsClosed(t *testing.T) {
	source := canonicalSource(t)
	t.Run("invalid source path", func(t *testing.T) {
		err := migrationhistory.CheckLegacy117(context.Background(), filepath.Join(t.TempDir(), "\x00"), "invalid database URL", 117, false)
		assert.ErrorContains(t, err, "inspect core migration 117")
	})
	for _, tt := range []struct{ name, databaseURL string }{
		{"malformed URL", "pgx5://user:legacy-check-secret@[::1"},
		{"invalid driver parameter", "pgx5://user:legacy-check-secret@127.0.0.1/db?connect_timeout=not-a-number"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := migrationhistory.CheckLegacy117(context.Background(), source, tt.databaseURL, 117, false)
			require.ErrorContains(t, err, "invalid database URL")
			assert.NotContains(t, err.Error(), "legacy-check-secret")
		})
	}
	t.Run("connection refused", func(t *testing.T) {
		u := url.URL{Scheme: "pgx5", Host: testutil.DeadAddr(t), Path: "/none"}
		err := migrationhistory.CheckLegacy117(context.Background(), source, u.String(), 117, false)
		assert.ErrorContains(t, err, "connect for legacy migration check")
	})
	t.Run("cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := migrationhistory.CheckLegacy117(ctx, source, "pgx5://user@127.0.0.1:1/none", 117, false)
		assert.ErrorIs(t, err, context.Canceled)
	})
}

func schemaURL(t *testing.T, suffix string, withColumn bool) string {
	t.Helper()
	db, err := testutil.OpenTestDBSchema(suffix)
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	var schema string
	require.NoError(t, db.Raw(`SELECT current_schema()`).Scan(&schema).Error)
	require.Equal(t, "internal_migrationhistory_"+suffix, schema)
	require.NoError(t, db.Exec(`DROP TABLE IF EXISTS "user"`).Error)
	columns := `"id" text PRIMARY KEY`
	if withColumn {
		columns += `, "accountCreatedAt" timestamp with time zone`
	}
	require.NoError(t, db.Exec(`CREATE TABLE "user" (`+columns+`)`).Error)
	q := url.Values{}
	q.Set("sslmode", testutil.EnvOrDefault("TEST_DB_SSLMODE", "disable"))
	q.Set("search_path", schema)
	u := url.URL{
		Scheme: "pgx5",
		User: url.UserPassword(
			testutil.EnvOrDefault("TEST_DB_USER", "mk"),
			testutil.EnvOrDefault("TEST_DB_PASS", "mk")),
		Host: net.JoinHostPort(testutil.EnvOrDefault("TEST_DB_HOST", "localhost"), testutil.EnvOrDefault("TEST_DB_PORT", "5432")),
		Path: "/" + testutil.EnvOrDefault("TEST_DB_NAME", "misskey_test"), RawQuery: q.Encode(),
	}
	return u.String()
}

func TestCheckLegacy117_UsesTargetSchema(t *testing.T) {
	source := canonicalSource(t)
	present := schemaURL(t, "present", true)
	missing := schemaURL(t, "missing", false)
	err := migrationhistory.CheckLegacy117(context.Background(), source, missing, 117, false)
	require.ErrorContains(t, err, "docs/misaki-migration-reconciliation.md")
	assert.NoError(t, migrationhistory.CheckLegacy117(context.Background(), source, present, 117, false))
}
