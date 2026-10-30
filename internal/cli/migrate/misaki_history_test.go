package migrate

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func misakiMigrationPaths() (string, string) {
	root := filepath.Join("..", "..", "..")
	return filepath.ToSlash(filepath.Join(root, "migration")), filepath.Join(root, "docs", "sql", "misaki-117-to-tracks.sql")
}

// 同番号の別ファイルは git merge が成功しても migrate の source が拒否する。
// 実際の source に両方向を読ませ、欠番・重複・対応する down の欠落を検出する。
func TestMisakiHistory_MigrationTracksAreUnique(t *testing.T) {
	core, _ := misakiMigrationPaths()
	for _, tt := range []struct {
		name, dir string
		latest    uint
	}{
		{"core", core, 138},
		{"local", filepath.Join(core, "local"), 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			src, err := iofs.New(os.DirFS(tt.dir), ".")
			require.NoError(t, err)
			t.Cleanup(func() { _ = src.Close() })
			version, err := src.First()
			require.NoError(t, err)
			for want := uint(1); want <= tt.latest; want++ {
				require.Equal(t, want, version)
				for _, read := range []func(uint) (io.ReadCloser, string, error){src.ReadUp, src.ReadDown} {
					body, _, err := read(version)
					require.NoError(t, err)
					data, err := io.ReadAll(body)
					require.NoError(t, body.Close())
					require.NoError(t, err)
					require.NotEmpty(t, data)
				}
				version, err = src.Next(version)
				if want < tt.latest {
					require.NoError(t, err)
				} else {
					require.ErrorIs(t, err, os.ErrNotExist)
				}
			}
		})
	}
}

func newMisakiHistoryFixture(t *testing.T, suffix string) *dbFixture {
	t.Helper()
	f := newDBFixture(t, suffix)
	f.coreDir, _ = misakiMigrationPaths()
	// migrator が file:// を前置するので、filepath.Join 後の local も slash に揃える。
	f.localDir = filepath.ToSlash(filepath.Join(f.coreDir, "local"))
	return f
}

func misakiReconciliationSQL(t *testing.T) string {
	t.Helper()
	_, path := misakiMigrationPaths()
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(body)
}

func prepareMisakiLegacy117(t *testing.T, f *dbFixture) {
	t.Helper()
	code, out := f.migrate(t, "-direction", "up", "-track", "core", "-steps", "116")
	require.Equal(t, 0, code, out)
	// 旧版の 106 は core98、旧107〜117 は core106〜116 と同じ SQL。
	// 独自98だけを適用し、旧版の最終ファイル名が示す117を台帳に再現する。
	cleanup, err := os.ReadFile(filepath.Join(f.localDir, "000001_misaki_drop_remote_avatar_decorations.up.sql"))
	require.NoError(t, err)
	require.NoError(t, f.db.Exec(string(cleanup)).Error)
	require.NoError(t, f.db.Exec(`UPDATE "schema_migrations" SET version = 117, dirty = false`).Error)
}

// 列・index・制約・enumを比較する。schema名と台帳だけはDBごとに異なる。
func misakiSchemaCatalog(t *testing.T, f *dbFixture) []string {
	t.Helper()
	var rows []struct{ Kind, Owner, Name, Definition string }
	require.NoError(t, f.db.Raw(`
		SELECT 'column' AS kind, table_name AS owner, column_name AS name,
		       ordinal_position::text || ':' || udt_name || ':' || is_nullable || ':' || coalesce(column_default, '') AS definition
		FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name NOT IN ('schema_migrations', 'schema_migrations_local')
		UNION ALL
		SELECT 'index', tablename, indexname, indexdef FROM pg_indexes
		WHERE schemaname = current_schema() AND tablename NOT IN ('schema_migrations', 'schema_migrations_local')
		UNION ALL
		SELECT 'constraint', c.relname, p.conname, pg_get_constraintdef(p.oid)
		FROM pg_constraint p JOIN pg_class c ON c.oid = p.conrelid
		WHERE p.connamespace = to_regnamespace(current_schema())
		  AND c.relname NOT IN ('schema_migrations', 'schema_migrations_local')
		UNION ALL
		SELECT 'enum', typname, e.enumlabel, e.enumsortorder::text
		FROM pg_type p JOIN pg_enum e ON e.enumtypid = p.oid
		WHERE p.typnamespace = to_regnamespace(current_schema())
		ORDER BY 1, 2, 3`).Scan(&rows).Error)
	require.NotEmpty(t, rows)
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, fmt.Sprintf("%s:%s:%s:%s", row.Kind, row.Owner, row.Name,
			strings.ReplaceAll(row.Definition, f.schema, "test_schema")))
	}
	return out
}

func TestMisakiHistory_Legacy117ReachesSameSchemaAsFresh(t *testing.T) {
	legacy := newMisakiHistoryFixture(t, "misaki_legacy")
	prepareMisakiLegacy117(t, legacy)
	require.NoError(t, legacy.db.Exec(misakiReconciliationSQL(t)).Error)
	assert.Equal(t, []int64{116}, legacy.versions(t, "schema_migrations"))
	assert.Equal(t, []int64{1}, legacy.versions(t, "schema_migrations_local"))
	require.ErrorContains(t, legacy.db.Exec(misakiReconciliationSQL(t)).Error, "clean legacy migration version 117")
	assert.Equal(t, []int64{116}, legacy.versions(t, "schema_migrations"))
	assert.Equal(t, []int64{1}, legacy.versions(t, "schema_migrations_local"))
	code, out := legacy.migrate(t, "-direction", "up")
	require.Equal(t, 0, code, out)
	assert.Equal(t, []int64{138}, legacy.versions(t, "schema_migrations"))
	assert.Equal(t, []int64{1}, legacy.versions(t, "schema_migrations_local"))
	assert.True(t, legacy.columnExists(t, "user", "accountCreatedAt"), "core117 must not be skipped")

	fresh := newMisakiHistoryFixture(t, "misaki_fresh")
	code, out = fresh.migrate(t, "-direction", "up")
	require.Equal(t, 0, code, out)
	assert.Equal(t, misakiSchemaCatalog(t, fresh), misakiSchemaCatalog(t, legacy))
}

func TestMisakiHistory_RejectsAmbiguousLegacyStates(t *testing.T) {
	f := newMisakiHistoryFixture(t, "misaki_guard")
	prepareMisakiLegacy117(t, f)
	for _, tt := range []struct {
		name, mutation, message string
	}{
		{"wrong version", `UPDATE "schema_migrations" SET version = 116`, "clean legacy migration version 117"},
		{"dirty", `UPDATE "schema_migrations" SET dirty = true`, "clean legacy migration version 117"},
		{"local ledger", `CREATE TABLE "schema_migrations_local" (version bigint, dirty boolean)`, "local migration ledger already exists"},
		{"new core117", `ALTER TABLE "user" ADD COLUMN IF NOT EXISTS "accountCreatedAt" timestamp with time zone`, "core migration 117 is already present"},
		{"missing index", `DROP INDEX "IDX_instance_faviconUrl"`, "missing or invalid legacy index"},
		{"remote decorations", `ALTER TABLE "avatar_decoration" ADD COLUMN IF NOT EXISTS "host" text;
			INSERT INTO "avatar_decoration" ("id", "url", "name", "description", "host")
			VALUES ('remote', 'https://remote.test/d.png', 'remote', '', 'remote.test')`, "remote avatar decorations remain"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := f.db.Transaction(func(tx *gorm.DB) error {
				if err := tx.Exec(tt.mutation).Error; err != nil {
					return fmt.Errorf("fixture: %w", err)
				}
				return tx.Exec(misakiReconciliationSQL(t)).Error
			})
			require.ErrorContains(t, err, tt.message)
			assert.Equal(t, []int64{117}, f.versions(t, "schema_migrations"))
			assert.False(t, f.tableExists(t, "schema_migrations_local"))
		})
	}
}

func TestMisakiHistory_ReconciliationRollsBackBothLedgers(t *testing.T) {
	f := newMisakiHistoryFixture(t, "misaki_atomic")
	prepareMisakiLegacy117(t, f)
	// local台帳を作った後の core台帳更新だけを失敗させ、DO文全体のrollbackを見る。
	require.NoError(t, f.db.Exec(`CREATE FUNCTION fail_core_update() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'injected core ledger failure'; END $$;
		CREATE TRIGGER fail_core_update BEFORE UPDATE ON "schema_migrations"
		FOR EACH ROW EXECUTE FUNCTION fail_core_update()`).Error)
	err := f.db.Exec(misakiReconciliationSQL(t)).Error
	require.ErrorContains(t, err, "injected core ledger failure")
	assert.Equal(t, []int64{117}, f.versions(t, "schema_migrations"))
	assert.False(t, f.tableExists(t, "schema_migrations_local"), "local must not remain after the core update fails")
}
