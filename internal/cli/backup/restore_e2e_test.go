package backup

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/wait"

	bkp "github.com/elythia-network/elythia/internal/backup"
	"github.com/elythia-network/elythia/internal/config"
)

// TestRestoreCommandEndToEnd takes a generation with "elythia backup take"
// (#3458) into a marked directory, then runs "elythia backup restore" with
// the storage the config selects and the real database, Redis and check
// steps: a swap, then a rollback. pg_dump runs inside the database
// container and pg_restore in a postgres:18-alpine container on the host
// network, standing in for the backup image (the host's tools are older
// than the server).
func TestRestoreCommandEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI is not available")
	}
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:18-alpine",
		tcpostgres.WithDatabase("postgres"), tcpostgres.WithUsername("test"), tcpostgres.WithPassword("test"),
		testcontainers.WithTmpfs(map[string]string{"/var/lib/postgresql": "rw,size=512m"}),
		testcontainers.WithEnv(map[string]string{"POSTGRES_INITDB_ARGS": "--no-sync"}),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").
			WithOccurrence(2).WithStartupTimeout(60*time.Second)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = pg.Terminate(context.Background()) })
	rc, err := tcredis.Run(ctx, "redis:7-alpine",
		testcontainers.WithWaitStrategy(wait.ForListeningPort("6379/tcp").WithStartupTimeout(30*time.Second)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = rc.Terminate(context.Background()) })
	pgPort, err := pg.MappedPort(ctx, "5432/tcp")
	require.NoError(t, err)
	redisPort, err := rc.MappedPort(ctx, "6379/tcp")
	require.NoError(t, err)

	url := func(user, pass, db string) string {
		return fmt.Sprintf("postgres://%s:%s@127.0.0.1:%d/%s?sslmode=disable", user, pass, pgPort.Num(), db)
	}
	exec1 := func(db string, sqls ...string) {
		conn, err := pgx.Connect(ctx, url("test", "test", db))
		require.NoError(t, err)
		defer conn.Close(ctx)
		for _, s := range sqls {
			_, err := conn.Exec(ctx, s)
			require.NoError(t, err, s)
		}
	}
	exec1("postgres", "CREATE ROLE app LOGIN PASSWORD 'app-pass' CREATEDB",
		"CREATE DATABASE src OWNER app", "CREATE DATABASE elythia OWNER app")
	for _, db := range []string{"src", "elythia"} {
		require.NoError(t, applyBackupFixtureMigrations(url("app", "app-pass", db), 0))
		exec1(db, `INSERT INTO "user" ("id", "username", "usernameLower") VALUES ('u1', 'root', 'root')`,
			`INSERT INTO meta ("id", "rootUserId") SELECT 'x', 'u1' WHERE NOT EXISTS (SELECT 1 FROM meta)`,
			`UPDATE meta SET "rootUserId" = 'u1'`,
			`INSERT INTO note ("id", "userId", "visibility") VALUES ('in_`+db+`', 'u1', 'public')`)
	}

	// src のバックアップを、(1) の take で目印付きのディレクトリへ取る。
	dir := t.TempDir()
	require.NoError(t, bkp.CreateDirMarker(dir))
	cfgFor := func(db, extra string) string {
		path := filepath.Join(t.TempDir(), "default.yml")
		require.NoError(t, os.WriteFile(path, []byte(fmt.Sprintf(`url: https://elythia.example/
port: 3000
db:
  host: 127.0.0.1
  port: %d
  db: %s
  user: app
  pass: app-pass
redis:
  host: 127.0.0.1
  port: %d
backup:
  storage:
    type: dir
    dir:
      path: %s
%s`, pgPort.Num(), db, redisPort.Num(), dir, extra)), 0o600))
		return path
	}
	te, takeOut, takeErr := testEnv(nil)
	te.runner = dockerExecRunner{container: pg.GetContainerID()}
	te.dumpConn = func(*config.Config) (bkp.DumpConn, error) {
		return bkp.DumpConn{URI: "postgresql://app@localhost:5432/src?sslmode=disable", Password: "app-pass"}, nil
	}
	require.Equal(t, 0, take(ctx, te, []string{"-config", cfgFor("src", "")}), takeErr.String())
	id := regexp.MustCompile(`generation (\d{8}T\d{6}Z)`).FindStringSubmatch(takeOut.String())
	require.Len(t, id, 2, takeOut.String())

	wrapper := filepath.Join(t.TempDir(), "pg_restore")
	require.NoError(t, os.WriteFile(wrapper, []byte("#!/bin/sh\nexec docker run --rm -i --network host -e PGPASSWORD postgres:18-alpine pg_restore \"$@\"\n"), 0o700))
	cfgPath := cfgFor("elythia", "  tools:\n    pgRestore: "+wrapper+"\n")

	e := defaultRestoreEnv()
	var out, errOut bytes.Buffer
	e.stdout, e.stderr = &out, &errOut
	code := restore(e, []string{"-config", cfgPath, "-id", id[1], "-mode", "swap", "-confirm", "elythia", "-migrations", rsCoreMigrations})
	require.Equal(t, 0, code, out.String()+errOut.String())
	before := regexp.MustCompile(`kept as (elythia_before_restore_\d{14})`).FindStringSubmatch(out.String())
	require.Len(t, before, 2, out.String())

	noteIn := func(db string) string {
		conn, err := pgx.Connect(ctx, url("app", "app-pass", db))
		require.NoError(t, err)
		defer conn.Close(ctx)
		var id string
		require.NoError(t, conn.QueryRow(ctx, `SELECT id FROM note`).Scan(&id))
		return id
	}
	assert.Equal(t, "in_src", noteIn("elythia"))
	assert.Equal(t, "in_elythia", noteIn(before[1]))

	out.Reset()
	errOut.Reset()
	code = restore(e, []string{"-config", cfgPath, "-rollback", before[1], "-confirm", "elythia", "-migrations", rsCoreMigrations})
	require.Equal(t, 0, code, out.String()+errOut.String())
	assert.Equal(t, "in_elythia", noteIn("elythia"))

	t.Run("rollback refuses unreconciled legacy117 before switching", func(t *testing.T) {
		const kept = "elythia_before_restore_legacy"
		const aside = "elythia_rolled_back_20261011000000"
		exec1("postgres", "CREATE DATABASE "+pgx.Identifier{kept}.Sanitize()+" OWNER app")
		require.NoError(t, applyBackupFixtureMigrations(url("app", "app-pass", kept), 116))
		exec1(kept,
			`DROP TABLE schema_migrations_local`,
			`UPDATE schema_migrations SET version = 117, dirty = false`,
			`INSERT INTO "user" ("id", "username", "usernameLower") VALUES ('legacyroot', 'root', 'root')`,
			`INSERT INTO meta ("id", "rootUserId") SELECT 'legacy', 'legacyroot' WHERE NOT EXISTS (SELECT 1 FROM meta)`,
			`UPDATE meta SET "rootUserId" = 'legacyroot'`,
			`INSERT INTO note ("id", "userId", "visibility") VALUES ('legacy_note', 'legacyroot', 'public')`)

		// 接続を保持すると ALTER DATABASE が別の理由で失敗し、切替前検査が
		// 無くても通ってしまう。各観測の接続は restore を呼ぶ前に閉じる。
		databaseOIDs := func() map[string]int64 {
			conn, err := pgx.Connect(ctx, url("test", "test", "postgres"))
			require.NoError(t, err)
			defer conn.Close(ctx)
			rows, err := conn.Query(ctx, `SELECT datname, oid::bigint FROM pg_database WHERE datname IN ($1, $2, $3)`, "elythia", kept, aside)
			require.NoError(t, err)
			defer rows.Close()
			oids := map[string]int64{}
			for rows.Next() {
				var name string
				var oid int64
				require.NoError(t, rows.Scan(&name, &oid))
				oids[name] = oid
			}
			require.NoError(t, rows.Err())
			return oids
		}
		assertLegacyUnchanged := func() {
			conn, err := pgx.Connect(ctx, url("app", "app-pass", kept))
			require.NoError(t, err)
			defer conn.Close(ctx)
			var version int64
			var dirty, localLedger, accountCreatedAt bool
			require.NoError(t, conn.QueryRow(ctx, `SELECT version, dirty FROM schema_migrations`).Scan(&version, &dirty))
			assert.EqualValues(t, 117, version)
			assert.False(t, dirty)
			require.NoError(t, conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.tables
				WHERE table_schema = current_schema() AND table_name = 'schema_migrations_local')`).Scan(&localLedger))
			assert.False(t, localLedger, "refusal must not create the local migration ledger")
			require.NoError(t, conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
				WHERE table_schema = current_schema() AND table_name = 'user' AND column_name = 'accountCreatedAt')`).Scan(&accountCreatedAt))
			assert.False(t, accountCreatedAt, "refusal must not migrate the legacy database")
		}
		beforeOIDs := databaseOIDs()
		require.Len(t, beforeOIDs, 2)
		require.Contains(t, beforeOIDs, "elythia")
		require.Contains(t, beforeOIDs, kept)
		require.Equal(t, "in_elythia", noteIn("elythia"))
		require.Equal(t, "legacy_note", noteIn(kept))
		assertLegacyUnchanged()

		e.now = func() time.Time { return time.Date(2026, time.October, 11, 0, 0, 0, 0, time.UTC) }
		out.Reset()
		errOut.Reset()
		code := restore(e, []string{"-config", cfgPath, "-rollback", kept, "-confirm", "elythia", "-migrations", rsCoreMigrations})
		assert.Equal(t, 1, code, out.String()+errOut.String())
		assert.Contains(t, out.String()+errOut.String(), "docs/misaki-migration-reconciliation.md")

		// 切替後の自己検査でも終了コード 1 になる。実際の名前・OID・投稿で、
		// 本番名を動かす前に拒否したことを確かめる。
		afterOIDs := databaseOIDs()
		assert.Equal(t, beforeOIDs, afterOIDs, "refusal must preserve both database names and identities")
		assert.NotContains(t, afterOIDs, aside, "the current database must not be renamed aside")
		assert.Equal(t, "in_elythia", noteIn("elythia"))
		require.Contains(t, afterOIDs, kept, "the legacy database must retain its kept name")
		assert.Equal(t, "legacy_note", noteIn(kept))
		assertLegacyUnchanged()
	})
}
