package deliverytargets

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elythia-network/elythia/plugin"
	"github.com/elythia-network/elythia/plugin/plugintest"
)

const testSchema = "plugin_delivery_targets_test"

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	base := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		envOr("TEST_DB_HOST", "localhost"), envOr("TEST_DB_PORT", "5432"),
		envOr("TEST_DB_USER", "mk"), envOr("TEST_DB_PASS", "mk"),
		envOr("TEST_DB_NAME", "misskey_test"))
	admin, err := sql.Open("pgx", base)
	require.NoError(t, err)
	defer admin.Close() //nolint:errcheck // 使い捨て
	if err := admin.Ping(); err != nil {
		if os.Getenv("MK_PLUGIN_TESTS_REQUIRE_DB") != "" {
			t.Fatalf("PostgreSQL に繋がらない: %v", err)
		}
		t.Skipf("PostgreSQL に繋がらない: %v", err)
	}
	for _, q := range []string{`DROP SCHEMA IF EXISTS ` + testSchema + ` CASCADE`, `CREATE SCHEMA ` + testSchema} {
		_, err := admin.Exec(q)
		require.NoError(t, err)
	}
	db, err := sql.Open("pgx", base+" search_path="+testSchema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = db.Close()
		if a, err := sql.Open("pgx", base); err == nil {
			_, _ = a.Exec(`DROP SCHEMA IF EXISTS ` + testSchema + ` CASCADE`)
			_ = a.Close()
		}
	})
	return db
}

func registration(t *testing.T) (plugin.NoteDeliveryRegistration, *sql.DB) {
	t.Helper()
	db := testDB(t)
	return plugintest.New(t).WithName(Name).WithDB(db).NoteDelivery(Plugin), db
}

func prepare(t *testing.T, reg plugin.NoteDeliveryRegistration, noteID, option string) error {
	t.Helper()
	return reg.Prepare(context.Background(), plugin.NoteDeliveryPrepareRequest{NoteID: noteID, UserID: "u1", Option: json.RawMessage(option)})
}

func TestDefinitionIsValid(t *testing.T) {
	require.NoError(t, Plugin.Validate())
}

func TestPrepareAndTargets(t *testing.T) {
	reg, _ := registration(t)
	require.NoError(t, prepare(t, reg, "n1", `{"mode":"include","hosts":["A.Example","https://b.example/@bob","a.example"]}`))
	require.NoError(t, prepare(t, reg, "n2", `{"mode":"exclude","hosts":["c.example:8443"]}`))

	got, err := reg.Targets(context.Background(), []string{"n1", "n2", "n3"})
	require.NoError(t, err)
	assert.Equal(t, map[string]plugin.DeliveryTargets{
		"n1": {Mode: plugin.DeliveryTargetsInclude, Hosts: []string{"a.example", "b.example"}},
		"n2": {Mode: plugin.DeliveryTargetsExclude, Hosts: []string{"c.example:8443"}},
	}, got)

	got, err = reg.Targets(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, got)
}

// A list without hosts narrows nothing in either mode, like no choice at all,
// so no row is written.
func TestPrepare_EmptyLists(t *testing.T) {
	reg, db := registration(t)
	require.NoError(t, prepare(t, reg, "inc", `{"mode":"include","hosts":[]}`))
	require.NoError(t, prepare(t, reg, "exc", `{"mode":"exclude"}`))
	got, err := reg.Targets(context.Background(), []string{"inc", "exc"})
	require.NoError(t, err)
	assert.Empty(t, got)
	var n int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM note_targets`).Scan(&n))
	assert.Equal(t, 0, n)
}

func TestPrepare_ReplacesSameNote(t *testing.T) {
	reg, _ := registration(t)
	require.NoError(t, prepare(t, reg, "n1", `{"mode":"include","hosts":["a.example"]}`))
	require.NoError(t, prepare(t, reg, "n1", `{"mode":"exclude","hosts":["b.example"]}`))
	got, err := reg.Targets(context.Background(), []string{"n1"})
	require.NoError(t, err)
	assert.Equal(t, plugin.DeliveryTargets{Mode: plugin.DeliveryTargetsExclude, Hosts: []string{"b.example"}}, got["n1"])
}

func TestPrepare_RejectsBadOptions(t *testing.T) {
	reg, _ := registration(t)
	many := `{"mode":"include","hosts":[`
	for i := 0; i <= MaxHosts; i++ {
		if i > 0 {
			many += ","
		}
		many += fmt.Sprintf(`"h%d.example"`, i)
	}
	many += `]}`
	for name, option := range map[string]string{
		"not json":       `{`,
		"not object":     `["a.example"]`,
		"unknown mode":   `{"mode":"all","hosts":[]}`,
		"missing mode":   `{"hosts":["a.example"]}`,
		"unknown field":  `{"mode":"include","hosts":[],"extra":1}`,
		"bad host":       `{"mode":"include","hosts":["a b"]}`,
		"empty host":     `{"mode":"include","hosts":[""]}`,
		"too many hosts": many,
	} {
		t.Run(name, func(t *testing.T) {
			err := prepare(t, reg, "n1", option)
			require.Error(t, err)
			assert.True(t, errors.Is(err, plugin.ErrBadNoteOption))
		})
	}
	got, err := reg.Targets(context.Background(), []string{"n1"})
	require.NoError(t, err)
	assert.Empty(t, got, "a rejected option must not be stored")
}

func TestTargets_StorageFailure(t *testing.T) {
	reg, db := registration(t)
	require.NoError(t, prepare(t, reg, "n1", `{"mode":"include","hosts":["a.example"]}`))
	require.NoError(t, db.Close())
	_, err := reg.Targets(context.Background(), []string{"n1"})
	require.Error(t, err, "the host turns this into federating nowhere")
	require.Error(t, prepare(t, reg, "n2", `{"mode":"include","hosts":["a.example"]}`))
}

func TestTargets_CorruptRow(t *testing.T) {
	reg, db := registration(t)
	_, err := db.Exec(`INSERT INTO note_targets (note_id, user_id, mode, hosts) VALUES ('n1', 'u1', 'include', '{"x":1}')`)
	require.NoError(t, err)
	_, err = reg.Targets(context.Background(), []string{"n1"})
	require.Error(t, err)
}

func TestNormalizeHost(t *testing.T) {
	ok := map[string]string{
		"remote.example":                "remote.example",
		"  Remote.Example.  ":           "remote.example",
		"@remote.example":               "remote.example",
		"https://remote.example/@u":     "remote.example",
		"https://remote.example:8443/x": "remote.example:8443",
		"remote.example:443":            "remote.example:443",
		"例え.テスト":                        "xn--r8jz45g.xn--zckzah",
		"https://例え.テスト/@user?x=1#frag": "xn--r8jz45g.xn--zckzah",
	}
	for in, want := range ok {
		got, err := NormalizeHost(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	for _, in := range []string{"", "   ", "https://", "a b", "remote.example:0", "remote.example:99999", "remote.example:x", "a/b", "user@remote.example", "[::1]", "%zz://x"} {
		_, err := NormalizeHost(in)
		assert.Error(t, err, in)
	}
	long := ""
	for len(long) <= maxHostLength {
		long += "a"
	}
	_, err := NormalizeHost(long)
	assert.Error(t, err)
}
