package rolelevel

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/elythia-network/elythia/plugin"
	"github.com/elythia-network/elythia/plugin/plugintest"
	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// testSchemaPrefix is the plugin-owned schema namespace the tests exercise.
//
// **固定名にしない。** 固定名の schema は同時 `go test` 同士が setup の
// `DROP SCHEMA ... CASCADE` で互いの schema を消して壊し合う。接頭辞だけ残し、
// process と test ごとに一意な名前を足す。
const testSchemaPrefix = "plugin_role_level_test"

// testSchemaMaxLen is PostgreSQL's identifier limit (NAMEDATALEN - 1).
//
// **超過した識別子は黙って切られる。** 切られた結果が別 test と被ると、名前を
// 一意にした意図が失われるので 63 byte で止める。
const testSchemaMaxLen = 63

// testSchemaRunID identifies one test process.
//
// **PID だけだと足りない。** コンテナ再起動で PID が使い回されると別 process が
// 同じ名前を見る。乱数を混ぜて process ごとに必ず違わせる。
var testSchemaRunID = sync.OnceValue(func() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// 乱数が取れなくても PID + 連番で process 内の一意性は保つ。
		return "00000000"
	}
	return hex.EncodeToString(b[:])
})

// testSchemaSeq numbers the schemas created inside one process.
var testSchemaSeq atomic.Uint64

// newTestSchema returns a schema name owned by this call only, so a test and the
// rest of its process never share one.
func newTestSchema(t *testing.T) string {
	t.Helper()
	name := fmt.Sprintf("%s_%d_%s_%d", testSchemaPrefix, os.Getpid(),
		testSchemaRunID(), testSchemaSeq.Add(1))
	if len(name) > testSchemaMaxLen {
		// 可変部分は先頭側に置いているので、削られるのは末尾の連番だけ。
		name = name[:testSchemaMaxLen]
	}
	return name
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// testDB opens a real PostgreSQL connection scoped to a schema owned by this test.
//
// **fake SQL は使わない。** 模した挙動は本物とずれ、通ったのに本番で落ちる
// 形のテストになる (plugins/status と同じ方針)。
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	schema := newTestSchema(t)
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
	// **引用符は pgx に任せる。** DDL は識別子として引用し、search_path は DSN の
	// 値として渡すので引用できない。名前は newTestSchema が [a-z0-9_] だけで作る。
	quoted := pgx.Identifier{schema}.Sanitize()
	for _, q := range []string{
		`DROP SCHEMA IF EXISTS ` + quoted + ` CASCADE`,
		`CREATE SCHEMA ` + quoted,
	} {
		if _, err := admin.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	db, err := sql.Open("pgx", base+" search_path="+schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		if a, err := sql.Open("pgx", base); err == nil {
			_, _ = a.Exec(`DROP SCHEMA IF EXISTS ` + quoted + ` CASCADE`)
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

// testDBRequired is testDB for the tests that assert the XP state machine's real
// behaviour.
//
// **skip を許さない。** state machine は PostgreSQL の session advisory lock と
// ON CONFLICT に依存するので、DB なしで skip すると「緑だが何も検証していない」まま
// CI を通ってしまう (#2588 と同じ理由)。dbUnavailable は MK_PLUGIN_TESTS_REQUIRE_DB が
// 設定済みなら skip ではなく Fatal になるので、既存の接続コードはそのまま再利用できる。
func testDBRequired(t *testing.T) *sql.DB {
	t.Helper()
	t.Setenv("MK_PLUGIN_TESTS_REQUIRE_DB", "1")
	return testDB(t)
}

// stubAPI answers the native endpoints without a running mk-go.
//
// **production と同じ形を返す。** admin/roles/show は object、admin/roles/users は
// [{id, user:{id}}]、users/show に userIds を渡すと配列。形をずらすと「本番では通ら
// ない経路」を「通った」ことにしてしまう (plugins/status の stub と同じ理由)。
type stubAPI struct {
	mu sync.Mutex
	// assignDeniedActors injects an actor-specific native denial for assign.
	assignDeniedActors map[string]int
	// assignActors records, in call order, the actor of every admin/roles/assign call.
	assignActors []string
	// roles is admin/roles/show by role id.
	roles map[string]roleInfo
	// assignments is admin/roles/users by role id, newest first.
	assignments map[string][]assignment
	// assignStatus, when non-zero, is the status admin/roles/assign fails with.
	assignStatus int
	assignCalls  int
	// usersShowErr injects a non-API failure for users/show tests.
	usersShowErr   error
	usersShowCalls int
	// showErr, when non-zero, is the status admin/roles/show fails with.
	showErr    int
	showActors []string
	// usersErr, when non-zero, is the status admin/roles/users fails with.
	usersErr    int
	usersActors []string
}

func (a *stubAPI) Anonymous() plugin.Caller            { return &stubCaller{api: a} }
func (a *stubAPI) AsUser(actorID string) plugin.Caller { return &stubCaller{api: a, actorID: actorID} }

// **actor ごとに 1 枚の caller を持つ。** 現在の `AsUser` は actor ID を捨てている。
// operation は「永続化された actor の権限」で native を呼ぶので、テストもその actor を
// 観測できないと persisted-actor の契約を検証できない。
type stubCaller struct {
	api     *stubAPI
	actorID string
}

func (c *stubCaller) Call(_ context.Context, endpoint string, params any) (json.RawMessage, error) {
	c.api.mu.Lock()
	defer c.api.mu.Unlock()
	m, _ := params.(map[string]any)
	switch endpoint {
	case "admin/roles/show":
		c.api.showActors = append(c.api.showActors, c.actorID)
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
		assignmentID := ""
		for _, a := range c.api.assignments[roleID] {
			if a.UserID() == userID {
				assigned = true
				assignmentID = a.ID
			}
		}
		return json.Marshal(map[string]any{
			"assigned": assigned, "assignmentId": assignmentID, "expiresAt": nil,
			"role": map[string]any{"id": roleID, "target": "manual", "isPublic": true,
				"canEditMembersByModerator": c.api.roleInfoFor(roleID).CanEditMembersByModerator},
		})
	case "roles/assignment-show":
		roleID, _ := m["roleId"].(string)
		assigned := false
		assignmentID := ""
		for _, a := range c.api.assignments[roleID] {
			if a.UserID() == c.actorID {
				assigned = true
				assignmentID = a.ID
			}
		}
		info := c.api.roleInfoFor(roleID)
		return json.Marshal(map[string]any{
			"assigned": assigned, "assignmentId": assignmentID, "expiresAt": nil,
			"role": map[string]any{"id": roleID, "target": "manual", "isPublic": info.IsPublic,
				"canEditMembersByModerator": info.CanEditMembersByModerator},
		})
	case "admin/roles/users":
		c.api.usersActors = append(c.api.usersActors, c.actorID)
		if c.api.usersErr != 0 {
			return nil, apiError(endpoint, c.api.usersErr, "NO_SUCH_ROLE")
		}
		roleID, _ := m["roleId"].(string)
		limit := 10
		if requested, ok := m["limit"].(int); ok {
			if requested < 1 || requested > 100 {
				return nil, apiError(endpoint, http.StatusBadRequest, "INVALID_PARAM")
			}
			limit = requested
		}
		rows := append([]assignment(nil), c.api.assignments[roleID]...)
		sort.Slice(rows, func(i, j int) bool { return rows[i].ID > rows[j].ID })
		sinceID, _ := m["sinceId"].(string)
		untilID, _ := m["untilId"].(string)
		filtered := rows[:0]
		for _, row := range rows {
			if untilID != "" && strings.Compare(row.ID, untilID) >= 0 {
				continue
			}
			if sinceID != "" && strings.Compare(row.ID, sinceID) <= 0 {
				continue
			}
			filtered = append(filtered, row)
		}
		if sinceID != "" && untilID == "" {
			sort.Slice(filtered, func(i, j int) bool { return filtered[i].ID < filtered[j].ID })
		}
		if len(filtered) > limit {
			filtered = filtered[:limit]
		}
		return json.Marshal(filtered)
	case "admin/roles/assign":
		c.api.assignCalls++
		// **記録は判定より前。** 拒否された呼び出しも native に届いた実績なので、
		// terminal failure 後に 2 回目を呼ばないこと (actor 数 == 1) を検証できる。
		c.api.assignActors = append(c.api.assignActors, c.actorID)
		if c.api.assignStatus != 0 {
			return nil, apiError(endpoint, c.api.assignStatus, "STUB_ASSIGN_FAILED")
		}
		// **actor ごとの拒否。** persisted actor の権限で native を呼ぶので、
		// この actor だけが 403 になる経路を状態機械が扱えるようにする。
		if status := c.api.assignDeniedActors[c.actorID]; status != 0 {
			return nil, apiError("admin/roles/assign", status, "ROLE_LEVEL_FORBIDDEN")
		}
		roleID, _ := m["roleId"].(string)
		userID, _ := m["userId"].(string)
		for _, existing := range c.api.assignments[roleID] {
			if existing.UserID() == userID {
				return nil, apiError(endpoint, http.StatusConflict, "ALREADY_ASSIGNED")
			}
		}
		// production は 204 を返すだけで、assignment は同じ transaction でできて
		// いるので直後の admin/roles/users にはもう出ている。
		next := assignment{ID: fmt.Sprintf("asg-%s-%d", roleID, len(c.api.assignments[roleID])+1)}
		next.User.ID = userID
		c.api.assignments[roleID] = append([]assignment{next}, c.api.assignments[roleID]...)
		return nil, nil
	case "users/show":
		c.api.usersShowCalls++
		if c.api.usersShowErr != nil {
			return nil, c.api.usersShowErr
		}
		ids, _ := m["userIds"].([]string)
		if len(ids) > 100 {
			ids = ids[:100]
		}
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

// assignCallActors returns the actors that reached admin/roles/assign, in call order.
//
// **コピーを返す。** 呼び出し側が記録中の slice を触ってはいけず、mutex を外に出さない。
func (a *stubAPI) assignCallActors() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.assignActors...)
}

func (a *stubAPI) usersShowCallCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.usersShowCalls
}

func apiError(endpoint string, status int, code string) error {
	return &plugin.APIError{Endpoint: endpoint, Status: status,
		Body: json.RawMessage(`{"error":{"code":"` + code + `"}}`)}
}

// newTestSchema は呼び出しごとに違う名前を返す。固定名に戻すと同時 `go test` が
// 互いの schema を DROP し合うので、ここは一意性と識別子の上限を固定する。
func TestNewTestSchemaIsUniquePerCall(t *testing.T) {
	seen := map[string]bool{}
	for range 2 {
		name := newTestSchema(t)
		if seen[name] {
			t.Fatalf("schema %q が重複しました", name)
		}
		seen[name] = true
		if !strings.HasPrefix(name, testSchemaPrefix+"_") {
			t.Fatalf("schema %q が接頭辞 %q を持ちません", name, testSchemaPrefix)
		}
		if len(name) > testSchemaMaxLen {
			t.Fatalf("schema %q が識別子上限 %d を超えています", name, testSchemaMaxLen)
		}
	}
}

func TestStubRolesUsersPaginatesLikeProduction(t *testing.T) {
	api := &stubAPI{assignments: map[string][]assignment{
		"role": {mkAssignment("asg-1", "u1"), mkAssignment("asg-4", "u4"),
			mkAssignment("asg-2", "u2"), mkAssignment("asg-3", "u3")},
	}}
	caller := api.AsUser("admin")

	first := callAssignments(t, caller, map[string]any{"roleId": "role", "limit": 2})
	assertAssignmentIDs(t, first, "asg-4", "asg-3")
	second := callAssignments(t, caller, map[string]any{"roleId": "role", "limit": 2, "untilId": "asg-3"})
	assertAssignmentIDs(t, second, "asg-2", "asg-1")
	since := callAssignments(t, caller, map[string]any{"roleId": "role", "limit": 2, "sinceId": "asg-2"})
	assertAssignmentIDs(t, since, "asg-3", "asg-4")
}

func mkAssignment(id, userID string) assignment {
	var a assignment
	a.ID = id
	a.User.ID = userID
	return a
}

func callAssignments(t *testing.T, caller plugin.Caller, params map[string]any) []assignment {
	t.Helper()
	raw, err := caller.Call(t.Context(), "admin/roles/users", params)
	if err != nil {
		t.Fatal(err)
	}
	var rows []assignment
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

func assertAssignmentIDs(t *testing.T, rows []assignment, want ...string) {
	t.Helper()
	got := make([]string, len(rows))
	for i, row := range rows {
		got[i] = row.ID
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("assignment ids = %v, want %v", got, want)
	}
}
