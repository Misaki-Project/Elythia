package backup

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	gomigrate "github.com/golang-migrate/migrate/v4"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
)

func rsP1StartPG(t *testing.T) *rsPG {
	t.Helper()
	return rsStartPG(t, "postgres:18-alpine",
		testcontainers.WithTmpfs(map[string]string{"/var/lib/postgresql": "rw,size=512m"}),
		testcontainers.WithEnv(map[string]string{"POSTGRES_INITDB_ARGS": "--no-sync"}))
}

func rsCreateMigrationDatabase(t *testing.T, pg *rsPG, name, user, pass string, version uint) {
	t.Helper()
	pg.exec(t, "postgres", "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" OWNER "+pgx.Identifier{user}.Sanitize())
	if version == 0 {
		return
	}
	m, err := gomigrate.New("file://"+rsCoreMigrations, pg.url("pgx5", user, pass, name))
	require.NoError(t, err)
	require.NoError(t, m.Migrate(version))
	sourceErr, databaseErr := m.Close()
	require.NoError(t, sourceErr)
	require.NoError(t, databaseErr)
}

// 旧Misaki117は正規core116までと同じschemaで、独自98のcleanupは
// host列の無いfresh schemaではno-opだった。適用履歴の番号だけが117になる。
func rsCreateLegacy117(t *testing.T, pg *rsPG, name, user, pass string) {
	t.Helper()
	rsCreateMigrationDatabase(t, pg, name, user, pass, 116)
	pg.exec(t, name, `UPDATE schema_migrations SET version = 117, dirty = false`)
}

func rsLegacyRestorer(pg *rsPG, storage Storage, user, pass string) *Restorer {
	r := pg.restorer(storage, user, pass)
	r.LocalMigrationsDir = filepath.Join(rsCoreMigrations, "local")
	return r
}

func rsAssertLegacy117Unchanged(t *testing.T, pg *rsPG, name string) {
	t.Helper()
	conn := pg.connect(t, rsSuperUser, rsSuperPass, name)
	defer conn.Close(context.Background())
	var version int64
	var dirty bool
	require.NoError(t, conn.QueryRow(context.Background(), `SELECT version, dirty FROM schema_migrations`).Scan(&version, &dirty))
	assert.EqualValues(t, 117, version, "the legacy core ledger must not advance")
	assert.False(t, dirty)
	assert.Zero(t, rsCount(t, conn, `SELECT count(*) FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'user' AND column_name = 'accountCreatedAt'`))
	assert.Zero(t, rsCount(t, conn, `SELECT count(*) FROM information_schema.tables
		WHERE table_schema = current_schema() AND table_name = 'schema_migrations_local'`), "refusal must not create the local ledger")
}

func TestRestorerMigrate_RefusesUnreconciledLegacy117(t *testing.T) {
	pg := rsP1StartPG(t)
	const target = "legacy117"
	rsCreateLegacy117(t, pg, target, rsSuperUser, rsSuperPass)
	rsAssertLegacy117Unchanged(t, pg, target)
	r := rsLegacyRestorer(pg, newRSStorage(), rsSuperUser, rsSuperPass)
	states, err := r.Migrate(target)
	assert.ErrorContains(t, err, "docs/misaki-migration-reconciliation.md")
	assert.Empty(t, states)
	rsAssertLegacy117Unchanged(t, pg, target)
}

func TestRestorerMigrate_AcceptsFreshAndCanonical117(t *testing.T) {
	pg := rsP1StartPG(t)
	r := rsLegacyRestorer(pg, newRSStorage(), rsSuperUser, rsSuperPass)
	latest, _, err := latestMigration(rsCoreMigrations)
	require.NoError(t, err)
	local, _, err := latestMigration(r.LocalMigrationsDir)
	require.NoError(t, err)
	for _, tt := range []struct {
		name    string
		version uint
	}{
		{"fresh", 0},
		{"canonical117", 117},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rsCreateMigrationDatabase(t, pg, tt.name, rsSuperUser, rsSuperPass, tt.version)
			states, err := r.Migrate(tt.name)
			require.NoError(t, err)
			assert.ElementsMatch(t, []MigrationState{
				{Table: CoreMigrationsTable, Version: int64(latest)},
				{Table: LocalMigrationsTable, Version: int64(local)},
			}, states)
			conn := pg.connect(t, rsSuperUser, rsSuperPass, tt.name)
			defer conn.Close(context.Background())
			assert.EqualValues(t, 1, rsCount(t, conn, `SELECT count(*) FROM information_schema.columns
				WHERE table_schema = current_schema() AND table_name = 'user' AND column_name = 'accountCreatedAt'`))
		})
	}
}

func TestCheckDatabaseMigrations_RefusesUnreconciledLegacy117(t *testing.T) {
	pg := rsP1StartPG(t)
	const target = "elythia_before_restore_20261001000000"
	rsCreateLegacy117(t, pg, target, rsSuperUser, rsSuperPass)
	r := rsLegacyRestorer(pg, newRSStorage(), rsSuperUser, rsSuperPass)
	_, err := r.CheckDatabaseMigrations(context.Background(), target)
	assert.ErrorContains(t, err, "docs/misaki-migration-reconciliation.md")
	rsAssertLegacy117Unchanged(t, pg, target)
}

func TestRestoreSwap_RefusesLegacy117BeforeSwitch(t *testing.T) {
	pg := rsP1StartPG(t)
	rsSetupInstance(t, pg, true)
	const source = "legacy117_source"
	rsCreateLegacy117(t, pg, source, rsAppUser, rsAppPass)
	pg.exec(t, source,
		`INSERT INTO "user" ("id", "username", "usernameLower") VALUES ('legacyroot', 'root', 'root')`,
		`INSERT INTO meta ("id", "rootUserId") VALUES ('legacy', 'legacyroot')`,
		`INSERT INTO note ("id", "userId", "visibility") VALUES ('legacy_note', 'legacyroot', 'public')`)
	storage := newRSStorage()
	rsTakeBackup(t, pg, storage, source, rsGenID, rsBackupOptions{})
	ctx := context.Background()
	meta, err := storage.Get(ctx, Key(rsGenID, MetaFile))
	require.NoError(t, err)
	body, err := io.ReadAll(meta)
	require.NoError(t, err)
	require.NoError(t, meta.Close())
	require.NoError(t, storage.Put(ctx, Key(rsGenID, MetaSigFile), strings.NewReader(SignMeta(testIntegrityKey, rsGenID, body)+"\n")))
	r := rsLegacyRestorer(pg, storage, rsAppUser, rsAppPass)
	r.IntegrityKey = testIntegrityKey
	_, err = r.Load(ctx, rsGenID)
	require.NoError(t, err, "the signed legacy generation must reach migration validation")
	admin := pg.connect(t, rsSuperUser, rsSuperPass, "postgres")
	defer admin.Close(ctx)
	var beforeOID int64
	require.NoError(t, admin.QueryRow(ctx, `SELECT oid::bigint FROM pg_database WHERE datname = $1`, rsAppDB).Scan(&beforeOID))
	res, err := r.Restore(ctx, RestoreOptions{ID: rsGenID, Mode: RestoreSwap, Database: rsAppDB, Confirm: rsAppDB})
	assert.ErrorContains(t, err, "docs/misaki-migration-reconciliation.md")
	assert.Nil(t, res)
	var afterOID int64
	require.NoError(t, admin.QueryRow(ctx, `SELECT oid::bigint FROM pg_database WHERE datname = $1`, rsAppDB).Scan(&afterOID))
	assert.Equal(t, beforeOID, afterOID, "the original application database must not be switched out")
	restored, before, err := RestoreNames(rsAppDB, r.now())
	require.NoError(t, err)
	has, err := r.ExistingDatabases(ctx, "postgres", []string{rsAppDB, source, restored, before})
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{rsAppDB: true, source: true}, has, "the rejected restore target is removed without keeping or renaming the current database")
	rsAssertLegacy117Unchanged(t, pg, source)
}
