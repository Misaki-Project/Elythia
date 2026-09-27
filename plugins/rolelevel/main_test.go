package rolelevel

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/shiroha-a/mk/plugin"
	"github.com/shiroha-a/mk/plugin/plugintest"
)

// testSchema is the plugin-owned schema the tests exercise.
const testSchema = "plugin_role_level_test"

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// testDB opens a real PostgreSQL connection scoped to testSchema.
//
// **fake SQL は使わない。** 模した挙動は本物とずれ、通ったのに本番で落ちる
// 形のテストになる (plugins/status と同じ方針)。
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	base := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		envOr("TEST_DB_HOST", "localhost"), envOr("TEST_DB_PORT", "5432"),
		envOr("TEST_DB_USER", "mk"), envOr("TEST_DB_PASS", "mk"),
		envOr("TEST_DB_NAME", "misskey_test"))

	admin, err := sql.Open("pgx", base)
	if err != nil {
		dbUnavailable(t, err)
	}
	defer admin.Close() //nolint:errcheck // 使い捨て
	if err := admin.Ping(); err != nil {
		dbUnavailable(t, err)
	}
	for _, q := range []string{
		`DROP SCHEMA IF EXISTS ` + testSchema + ` CASCADE`,
		`CREATE SCHEMA ` + testSchema,
	} {
		if _, err := admin.Exec(q); err != nil {
			t.Fatal(err)
		}
	}

	db, err := sql.Open("pgx", base+" search_path="+testSchema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		if a, err := sql.Open("pgx", base); err == nil {
			_, _ = a.Exec(`DROP SCHEMA IF EXISTS ` + testSchema + ` CASCADE`)
			_ = a.Close()
		}
	})
	return db
}

// newHarness wires a harness with the plugin's real name, schema and API stub.
func newHarness(t *testing.T, db *sql.DB, api plugin.API) *plugintest.Harness {
	t.Helper()
	if api == nil {
		api = &stubAPI{}
	}
	return plugintest.New(t).WithName("role-level").WithDB(db).WithAPI(api)
}

// plugintestContext builds a plugin.Context carrying only configuration, for the
// tests that exercise loadConfig without touching the database.
func plugintestContext(t *testing.T, cfg map[string]any) plugin.Context {
	t.Helper()
	return plugintest.New(t).WithName("role-level").WithConfig(cfg).Context()
}

// dbUnavailable decides what to do when the test database cannot be reached.
//
// **CI では skip を許さない。** skip は成功として扱われるので、接続に失敗したことに
// 気づかないまま緑になる (#2588)。
func dbUnavailable(t *testing.T, err error) {
	t.Helper()
	if os.Getenv("MK_PLUGIN_TESTS_REQUIRE_DB") != "" {
		t.Fatalf("PostgreSQL に接続できません (MK_PLUGIN_TESTS_REQUIRE_DB が設定されているので skip しません): %v", err)
	}
	t.Skipf("PostgreSQL に接続できません: %v", err)
}

// stubAPI answers the native endpoints without a running mk-go.
//
// **production と同じ形を返す。** admin/roles/show は object、admin/roles/users は
// [{id, user:{id}}]、users/show に userIds を渡すと配列。形をずらすと「本番では通ら
// ない経路」を「通った」ことにしてしまう (plugins/status の stub と同じ理由)。
type stubAPI struct {
	mu sync.Mutex
	// roles is admin/roles/show by role id.
	roles map[string]roleInfo
	// assignments is admin/roles/users by role id, newest first.
	assignments map[string][]assignment
	// assignStatus, when non-zero, is the status admin/roles/assign fails with.
	assignStatus int
	assignCalls  int
	// showErr, when non-zero, is the status admin/roles/show fails with.
	showErr int
}

func (a *stubAPI) Anonymous() plugin.Caller    { return &stubCaller{api: a} }
func (a *stubAPI) AsUser(string) plugin.Caller { return &stubCaller{api: a} }

type stubCaller struct{ api *stubAPI }

func (c *stubCaller) Call(_ context.Context, endpoint string, params any) (json.RawMessage, error) {
	c.api.mu.Lock()
	defer c.api.mu.Unlock()
	m, _ := params.(map[string]any)
	switch endpoint {
	case "admin/roles/show":
		if c.api.showErr != 0 {
			return nil, apiError(endpoint, c.api.showErr, "SHOW_FAILED")
		}
		roleID, _ := m["roleId"].(string)
		info, ok := c.api.roles[roleID]
		if !ok {
			return nil, apiError(endpoint, http.StatusBadRequest, "NO_SUCH_ROLE")
		}
		return json.Marshal(info)
	case "admin/roles/assignment-show":
		roleID, _ := m["roleId"].(string)
		userID, _ := m["userId"].(string)
		assigned := false
		for _, a := range c.api.assignments[roleID] {
			if a.UserID() == userID {
				assigned = true
			}
		}
		return json.Marshal(map[string]any{
			"assigned": assigned, "expiresAt": nil,
			"role": map[string]any{"id": roleID, "target": "manual", "isPublic": true,
				"canEditMembersByModerator": c.api.roleInfoFor(roleID).CanEditMembersByModerator},
		})
	case "admin/roles/users":
		roleID, _ := m["roleId"].(string)
		if c.api.assignments[roleID] == nil {
			return json.Marshal([]assignment{})
		}
		return json.Marshal(c.api.assignments[roleID])
	case "admin/roles/assign":
		c.api.assignCalls++
		if c.api.assignStatus != 0 {
			return nil, apiError(endpoint, c.api.assignStatus, "STUB_ASSIGN_FAILED")
		}
		roleID, _ := m["roleId"].(string)
		userID, _ := m["userId"].(string)
		// production は 204 を返すだけで、assignment は同じ transaction でできて
		// いるので直後の admin/roles/users にはもう出ている。
		next := assignment{ID: fmt.Sprintf("asg-%s-%d", roleID, len(c.api.assignments[roleID])+1)}
		next.User.ID = userID
		c.api.assignments[roleID] = append([]assignment{next}, c.api.assignments[roleID]...)
		return nil, nil
	case "users/show":
		ids, _ := m["userIds"].([]string)
		out := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			out = append(out, map[string]any{"id": id, "username": "user-" + id})
		}
		return json.Marshal(out)
	}
	return nil, apiError(endpoint, http.StatusNotImplemented, "STUB_UNKNOWN_ENDPOINT")
}

func (a *stubAPI) roleInfoFor(roleID string) roleInfo {
	if info, ok := a.roles[roleID]; ok {
		return info
	}
	return roleInfo{ID: roleID, Target: "manual"}
}

func (a *stubAPI) assignCallCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.assignCalls
}

func apiError(endpoint string, status int, code string) error {
	return &plugin.APIError{Endpoint: endpoint, Status: status,
		Body: json.RawMessage(`{"error":{"code":"` + code + `"}}`)}
}
