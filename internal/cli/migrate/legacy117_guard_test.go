package migrate

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elythia-network/elythia/internal/config"
)

func TestRun_RefusesLegacy117BeforeMigration(t *testing.T) {
	m := &fakeMigrator{version: 117}
	e, out, opened := testEnv(t, m, nil)
	e.coreDir = filepath.Join(t.TempDir(), "migration")
	writeMigration(t, e.coreDir, "000117_user_account_created_at", `ALTER TABLE "user" ADD COLUMN "accountCreatedAt" timestamptz;`, "SELECT 1;")
	e.databaseURL = func(_ *config.Config) string { return "pgx5://user@127.0.0.1:1/none" }

	code := run(e, &bytes.Buffer{}, []string{"-config", writeConfig(t), "-track", "core"})
	assert.Equal(t, 1, code)
	assert.Contains(t, out.String(), "migration history check failed")
	assert.NotContains(t, out.String(), "secret")
	assert.Empty(t, m.calls, "the core migration must not start")
	require.Len(t, *opened, 2)
}

func TestRun_ForceRefusesCore117BeforeConnecting(t *testing.T) {
	m := &fakeMigrator{}
	e, out, opened := forceEnv(t, m)
	code := run(e, &bytes.Buffer{}, []string{"-config", writeConfig(t), "-force", "117", "-track", "core"})
	assert.Equal(t, 1, code)
	assert.Contains(t, out.String(), "refusing to force core version 117")
	assert.Empty(t, *opened, "the ambiguous version must be rejected before opening the database")
	assert.Empty(t, m.calls)
}