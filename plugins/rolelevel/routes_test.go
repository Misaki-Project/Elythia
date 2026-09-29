package rolelevel

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/shiroha-a/mk/plugin"
	"github.com/shiroha-a/mk/plugin/plugintest"
)

// decode re-marshals a handler result so the assertions run against the JSON the
// client actually sees, not against the plugin's Go types.
func decode(t *testing.T, res any, out any) {
	t.Helper()
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("応答を読めません: %v (%s)", err, raw)
	}
}

func levelRoleAPI() *stubAPI {
	return &stubAPI{
		roles: map[string]roleInfo{"r1": {ID: "r1", Target: "manual", IsPublic: true, IsExplorable: true}},
		assignments: map[string][]assignment{
			"r1": {mkAssignment("asg1", "u1"), mkAssignment("asg2", "u2")},
		},
	}
}

func routeHarness(t *testing.T, api plugin.API) plugintest.Handlers {
	t.Helper()
	return newHarness(t, testDBRequired(t), api).WithConfig(map[string]any{"actorId": "admin1"}).Routes(Plugin)
}

// actorlessRouteHarness verifies that public native reads do not silently
// downgrade to the anonymous caller when the privileged plugin actor is absent.
func actorlessRouteHarness(t *testing.T, api plugin.API) plugintest.Handlers {
	t.Helper()
	return newHarness(t, testDBRequired(t), api).WithConfig(map[string]any{}).Routes(Plugin)
}

func TestPublicRoutesRequireConfiguredActor(t *testing.T) {
	h := actorlessRouteHarness(t, levelRoleAPI())
	for _, tc := range []struct {
		name, route, body string
	}{
		{"profile", "POST /users/show", `{"userId":"u1"}`},
		{"members", "POST /roles/users", `{"roleId":"r1"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := h.Call(t, tc.route, plugintest.Request{Body: tc.body})
			if err == nil {
				t.Fatal("public route used an anonymous native caller")
			}
			se, code := extractCode(err)
			if code != CodeActorNotConfigured || se == nil || se.Status != http.StatusForbidden {
				t.Fatalf("status/code = %v/%q, want 403/%s (%v)", statusOf(se), code, CodeActorNotConfigured, err)
			}
		})
	}
}

// **全 route が POST で登録されている。** path parameter も query string なしで、
// 全部 body で受ける (Global Constraints の「設計上の判断」)。GET が1本でも残って
// いたら、frontend が POST で叩いて 404 になる。
func TestAllRoutesArePost(t *testing.T) {
	h := routeHarness(t, levelRoleAPI())
	want := []string{
		"POST /admin/roles/list",
		"POST /admin/roles/show",
		"POST /admin/roles/update",
		"POST /admin/roles/delete",
		"POST /admin/users/show",
		"POST /admin/change-exp",
		"POST /roles/users",
		"POST /users/show",
		"POST /admin/audit",
		"POST /admin/orphans",
		"POST /admin/reconcile",
	}
	if len(h) != len(want) {
		t.Fatalf("登録された route = %d 本 (%v), want %d 本", len(h), keysOf(h), len(want))
	}
	for _, key := range want {
		if _, registered := h[key]; !registered {
			t.Fatalf("expected route %s is not registered", key)
		}
		if _, err := h.Call(t, key, plugintest.Request{Body: `{}`}); err == nil {
			t.Fatalf("%s: body 空でも通っています (path / 権限の検証が要先)", key)
		}
	}
}

func keysOf(h plugintest.Handlers) []string {
	out := make([]string, 0, len(h))
	for k := range h {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestAllBodyRoutesRejectNonStrictJSON(t *testing.T) {
	tests := []struct {
		name, route, body string
		req               plugintest.Request
	}{
		{"roles-list unknown field", "POST /admin/roles/list", `{"unknown":true}`, plugintest.Request{UserID: "m1", Moderator: true}},
		{"roles-list trailing value", "POST /admin/roles/list", `{} {}`, plugintest.Request{UserID: "m1", Moderator: true}},
		{"roles-show unknown field", "POST /admin/roles/show", `{"roleId":"r1","unknown":true}`, plugintest.Request{UserID: "m1", Moderator: true}},
		{"roles-show trailing value", "POST /admin/roles/show", `{"roleId":"r1"} {}`, plugintest.Request{UserID: "m1", Moderator: true}},
		{"roles-update unknown field", "POST /admin/roles/update", `{"roleId":"r1","unknown":true}`, plugintest.Request{UserID: "a1", Administrator: true}},
		{"roles-update trailing value", "POST /admin/roles/update", `{"roleId":"r1"} {}`, plugintest.Request{UserID: "a1", Administrator: true}},
		{"roles-delete unknown field", "POST /admin/roles/delete", `{"roleId":"r1","unknown":true}`, plugintest.Request{UserID: "a1", Administrator: true}},
		{"roles-delete trailing value", "POST /admin/roles/delete", `{"roleId":"r1"} {}`, plugintest.Request{UserID: "a1", Administrator: true}},
		{"admin-users-show unknown field", "POST /admin/users/show", `{"userId":"u1","unknown":true}`, plugintest.Request{UserID: "m1", Moderator: true}},
		{"admin-users-show trailing value", "POST /admin/users/show", `{"userId":"u1"} {}`, plugintest.Request{UserID: "m1", Moderator: true}},
		{"change-exp unknown field", "POST /admin/change-exp", `{"idempotencyKey":"strict-unknown","userId":"u1","roleId":"r1","mode":"add","operand":1,"unknown":true}`, plugintest.Request{UserID: "a1", Administrator: true}},
		{"change-exp trailing value", "POST /admin/change-exp", `{"idempotencyKey":"strict-trailing","userId":"u1","roleId":"r1","mode":"add","operand":1} {}`, plugintest.Request{UserID: "a1", Administrator: true}},
		{"audit unknown field", "POST /admin/audit", `{"userId":"u1","unknown":true}`, plugintest.Request{UserID: "m1", Moderator: true}},
		{"audit trailing value", "POST /admin/audit", `{"userId":"u1"} {}`, plugintest.Request{UserID: "m1", Moderator: true}},
		{"orphans unknown field", "POST /admin/orphans", `{"unknown":true}`, plugintest.Request{UserID: "m1", Moderator: true}},
		{"orphans trailing value", "POST /admin/orphans", `{} {}`, plugintest.Request{UserID: "m1", Moderator: true}},
		{"reconcile unknown field", "POST /admin/reconcile", `{"mode":"all","unknown":true}`, plugintest.Request{UserID: "a1", Administrator: true}},
		{"reconcile trailing value", "POST /admin/reconcile", `{"mode":"all"} {}`, plugintest.Request{UserID: "a1", Administrator: true}},
		{"roles-users unknown field", "POST /roles/users", `{"roleId":"r1","unknown":true}`, plugintest.Request{}},
		{"roles-users trailing value", "POST /roles/users", `{"roleId":"r1"} {}`, plugintest.Request{}},
		{"users-show unknown field", "POST /users/show", `{"userId":"u1","unknown":true}`, plugintest.Request{}},
		{"users-show trailing value", "POST /users/show", `{"userId":"u1"} {}`, plugintest.Request{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := routeHarness(t, levelRoleAPI())
			tt.req.Body = tt.body
			_, err := h.Call(t, tt.route, tt.req)
			if err == nil {
				t.Fatal("non-strict JSON was accepted")
			}
			se, code := extractCode(err)
			if code != CodeValidationFailed || se == nil || se.Status != 400 {
				t.Fatalf("status/code = %v/%q, want 400/%s (%v)", statusOf(se), code, CodeValidationFailed, err)
			}
		})
	}
}

func statusOf(err *plugin.StatusError) any {
	if err == nil {
		return nil
	}
	return err.Status
}

// **level 設定の作成・更新・削除は administrator だけ。** frontend を隠しても
// 守らないので、ハンドラ側で確認する。
func TestAdminConfigRoutesRequireAdministrator(t *testing.T) {
	h := routeHarness(t, levelRoleAPI())
	// **/admin/roles/show は level 設定が無い role に 404 を返す。** このテストは
	// 認可だけを確かめるので、先に r1 の level 設定を作ってから試す。
	if _, err := h.Call(t, "POST /admin/roles/update", plugintest.Request{
		UserID: "a1", Administrator: true,
		Body: `{"roleId":"r1","baseLevel":1,"experienceCurve":[{"type":"const","levelUps":9,"base":10}],"policyRanges":[{"type":"base","start":1,"end":10}],"revision":0}`,
	}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"POST /admin/roles/list", "POST /admin/roles/show"} {
		body := `{"roleId":"r1"}`
		if key == "POST /admin/roles/list" {
			body = `{}`
		}
		if _, err := h.Call(t, key, plugintest.Request{UserID: "u1", Body: body}); err == nil {
			t.Fatalf("%s: 未認証の一般 user を通しています", key)
		}
		if _, err := h.Call(t, key, plugintest.Request{UserID: "m1", Moderator: true, Body: body}); err != nil {
			t.Fatalf("%s: moderator を拒否しました: %v", key, err)
		}
	}
	if _, err := h.Call(t, "POST /admin/roles/update", plugintest.Request{
		UserID: "m1", Moderator: true,
		Body: `{"roleId":"r1","baseLevel":1,"experienceCurve":[{"type":"const","levelUps":9,"base":10}],"policyRanges":[{"type":"base","start":1,"end":10}],"revision":0}`,
	}); err == nil {
		t.Fatal("moderator に level 設定を保存させています")
	}
	if _, err := h.Call(t, "POST /admin/roles/delete", plugintest.Request{
		UserID: "m1", Moderator: true, Body: `{"roleId":"r1","revision":1}`,
	}); err == nil {
		t.Fatal("moderator に level 設定を消させています")
	}
	if _, err := h.Call(t, "POST /admin/reconcile", plugintest.Request{
		UserID: "m1", Moderator: true, Body: `{"mode":"resume-operations"}`,
	}); err == nil {
		t.Fatal("moderator に reconciliation を実行させています")
	}
}

// **保存の round trip。**
func TestAdminConfigRoundTrip(t *testing.T) {
	h := routeHarness(t, levelRoleAPI())
	res, err := h.Call(t, "POST /admin/roles/update", plugintest.Request{
		UserID: "a1", Administrator: true,
		Body: `{"roleId":"r1","baseLevel":1,"experienceCurve":[{"type":"const","levelUps":9,"base":10}],"policyRanges":[{"type":"base","start":1,"end":10}],"revision":0}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Role struct {
			Revision int64 `json:"revision"`
		} `json:"role"`
	}
	decode(t, res, &saved)
	if saved.Role.Revision != 1 {
		t.Fatalf("revision = %d, want 1", saved.Role.Revision)
	}

	res, err = h.Call(t, "POST /admin/roles/show", plugintest.Request{
		UserID: "a1", Administrator: true, Body: `{"roleId":"r1"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	var one struct {
		Role struct {
			BaseLevel int64 `json:"baseLevel"`
		} `json:"role"`
		MemberCount      int  `json:"memberCount"`
		MembersTruncated bool `json:"membersTruncated"`
	}
	decode(t, res, &one)
	if one.Role.BaseLevel != 1 || one.MemberCount != 2 || one.MembersTruncated {
		t.Fatalf("= %+v", one)
	}
}

// Config mutations and their audit rows must be one database transaction.
func TestAdminConfigRequiredDBAuditAndRollback(t *testing.T) {
	db := testDBRequired(t)
	h := newHarness(t, db, levelRoleAPI()).WithConfig(map[string]any{"actorId": "admin1"}).Routes(Plugin)
	count := func() int {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM role_level_audit`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	request := func(body string, admin bool) error {
		_, err := h.Call(t, "POST /admin/roles/update", plugintest.Request{UserID: "a1", Administrator: admin, Body: body})
		return err
	}
	delete := func(body string) error {
		_, err := h.Call(t, "POST /admin/roles/delete", plugintest.Request{UserID: "a1", Administrator: true, Body: body})
		return err
	}
	config := func(base, rev int, note string) string {
		return fmt.Sprintf(`{"roleId":"r1","baseLevel":%d,"experienceCurve":[{"type":"const","levelUps":9,"base":10}],"policyRanges":[{"type":"base","start":1,"end":10}],"revision":%d,"note":%q}`, base, rev, note)
	}
	if request(`{}`, true) == nil || request(config(1, 0, "bad"), false) == nil || count() != 0 {
		t.Fatal("validation/authentication wrote an audit row")
	}
	fail := func() {
		if _, err := db.Exec(`CREATE FUNCTION fail_role_level_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'audit failure'; END $$; CREATE TRIGGER fail_role_level_audit BEFORE INSERT ON role_level_audit FOR EACH ROW EXECUTE FUNCTION fail_role_level_audit()`); err != nil {
			t.Fatal(err)
		}
	}
	dropFail := func() {
		if _, err := db.Exec(`DROP TRIGGER fail_role_level_audit ON role_level_audit; DROP FUNCTION fail_role_level_audit()`); err != nil {
			t.Fatal(err)
		}
	}
	fail()
	if request(config(1, 0, "create"), true) == nil {
		t.Fatal("audit failure accepted create")
	}
	dropFail()
	if count() != 0 {
		t.Fatal("failed create wrote audit")
	}
	if err := request(config(1, 0, "create"), true); err != nil {
		t.Fatal(err)
	}
	if request(config(9, 0, "duplicate"), true) == nil || count() != 1 {
		t.Fatal("duplicate create changed audit count")
	}
	if request(config(2, 1, "update"), true) != nil {
		t.Fatal("update failed")
	}
	if request(config(3, 1, "stale"), true) == nil || count() != 2 {
		t.Fatal("revision conflict changed audit count")
	}
	fail()
	if request(config(3, 2, "failed update"), true) == nil {
		t.Fatal("audit failure accepted update")
	}
	dropFail()
	var revision, base int64
	if err := db.QueryRow(`SELECT revision, base_level FROM role_level_config WHERE role_id='r1'`).Scan(&revision, &base); err != nil {
		t.Fatal(err)
	}
	if revision != 2 || base != 2 {
		t.Fatalf("failed update committed: revision=%d base=%d", revision, base)
	}
	fail()
	if delete(`{"roleId":"r1","revision":2}`) == nil {
		t.Fatal("audit failure accepted delete")
	}
	dropFail()
	if err := db.QueryRow(`SELECT revision FROM role_level_config WHERE role_id='r1'`).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if revision != 2 {
		t.Fatal("failed delete committed")
	}
	if delete(`{"roleId":"r1","revision":2}`) != nil {
		t.Fatal("delete failed")
	}
	if delete(`{"roleId":"r1","revision":3}`) == nil || count() != 3 {
		t.Fatal("delete no-op changed audit count")
	}

	rows, err := db.Query(`SELECT id, actor_id, operation, role_id, user_id, assignment_id, note, before_state, after_state, created_at FROM role_level_audit WHERE role_id='r1' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	wantOps := []string{"config-create", "config-update", "config-delete"}
	wantNotes := []string{"create", "update", ""}
	for i := range wantOps {
		var id int64
		var actor, op, role string
		var user, assignment, note sql.NullString
		var before, after []byte
		var createdAt time.Time
		if !rows.Next() {
			t.Fatalf("audit rows ended at %d", i)
		}
		if err := rows.Scan(&id, &actor, &op, &role, &user, &assignment, &note, &before, &after, &createdAt); err != nil {
			t.Fatal(err)
		}
		if id <= 0 || actor != "a1" || op != wantOps[i] || role != "r1" || user.Valid || assignment.Valid || !createdAt.After(time.Time{}) || note.Valid != (i < 2) || note.String != wantNotes[i] {
			t.Fatalf("audit[%d] columns: id=%d actor=%q op=%q role=%q user=%v assignment=%v note=%q", i, id, actor, op, role, user, assignment, note.String)
		}
		if (len(before) == 0) != (i == 0) || (len(after) == 0) != (i == 2) {
			t.Fatalf("audit[%d] null states: before=%s after=%s", i, before, after)
		}
		state := func(base, revision int64) map[string]any {
			return configAuditState(Config{RoleID: "r1", BaseLevel: base, ExperienceCurve: []Curve{{Type: "const", LevelUps: 9, Base: 10}}, PolicyRanges: []PolicyRange{{Type: RangeBase, Start: 1, End: 10}, {Type: RangeBase, Start: 10, End: 11}}, Revision: revision, UpdatedBy: "a1"})
		}
		if i > 0 {
			var got any
			if err := json.Unmarshal(before, &got); err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(state(int64(i), int64(i)))
			var want any
			_ = json.Unmarshal(raw, &want)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("audit[%d] before=%s want=%s", i, before, raw)
			}
		}
		if i < 2 {
			var got any
			if err := json.Unmarshal(after, &got); err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(state(int64(i+1), int64(i+1)))
			var want any
			_ = json.Unmarshal(raw, &want)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("audit[%d] after=%s want=%s", i, after, raw)
			}
		}
	}
	if rows.Next() {
		t.Fatal("unexpected audit row")
	}
}

// **list は全 level 設定と member 件数を返す。**
func TestAdminRolesList(t *testing.T) {
	h := routeHarness(t, levelRoleAPI())
	if _, err := h.Call(t, "POST /admin/roles/update", plugintest.Request{
		UserID: "a1", Administrator: true,
		Body: `{"roleId":"r1","baseLevel":1,"experienceCurve":[{"type":"const","levelUps":9,"base":10}],"policyRanges":[{"type":"base","start":1,"end":10}],"revision":0}`,
	}); err != nil {
		t.Fatal(err)
	}
	res, err := h.Call(t, "POST /admin/roles/list", plugintest.Request{
		UserID: "m1", Moderator: true, Body: `{}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Roles []struct {
			RoleID string `json:"roleId"`
		} `json:"roles"`
		MemberCounts          map[string]int  `json:"memberCounts"`
		MemberCountsTruncated map[string]bool `json:"memberCountsTruncated"`
	}
	decode(t, res, &got)
	if len(got.Roles) != 1 || got.Roles[0].RoleID != "r1" {
		t.Fatalf("roles = %+v", got.Roles)
	}
	if got.MemberCounts["r1"] != 2 || got.MemberCountsTruncated["r1"] {
		t.Fatalf("memberCounts = %+v truncated = %+v", got.MemberCounts, got.MemberCountsTruncated)
	}
}

// **未知の policy key は stable code 付きで 400。**
func TestAdminConfigRejectsUnknownPolicyKey(t *testing.T) {
	h := routeHarness(t, levelRoleAPI())
	_, err := h.Call(t, "POST /admin/roles/update", plugintest.Request{
		UserID: "a1", Administrator: true,
		Body: `{"roleId":"r1","baseLevel":1,"experienceCurve":[{"type":"const","levelUps":9,"base":10}],"policyRanges":[{"type":"const","start":1,"end":10,"key":"noSuchKey","value":true}],"revision":0}`,
	})
	if err == nil {
		t.Fatal("未知の key を受理しています")
	}
	if _, code := extractCode(err); code != CodeUnknownPolicyKey {
		t.Fatalf("code = %q, want %s (%v)", code, CodeUnknownPolicyKey, err)
	}
}

// **curve の overflow は保存時に弾かれる。**
func TestAdminConfigRejectsUnsafeCurve(t *testing.T) {
	h := routeHarness(t, levelRoleAPI())
	_, err := h.Call(t, "POST /admin/roles/update", plugintest.Request{
		UserID: "a1", Administrator: true,
		Body: `{"roleId":"r1","baseLevel":0,"experienceCurve":[{"type":"const","levelUps":1000000,"base":9007199254740991}],"policyRanges":[{"type":"base","start":1,"end":1000001}],"revision":0}`,
	})
	if err == nil {
		t.Fatal("overflow する curve を受理しています")
	}
	if _, code := extractCode(err); code != CodeInvalidCurve {
		t.Fatalf("code = %q, want %s (%v)", code, CodeInvalidCurve, err)
	}
}

// **revision 不一致は conflict。** 古い画面からの保存で黙って上書きしない。
func TestAdminConfigRevisionConflict(t *testing.T) {
	h := routeHarness(t, levelRoleAPI())
	body := `{"roleId":"r1","baseLevel":1,"experienceCurve":[{"type":"const","levelUps":9,"base":10}],"policyRanges":[{"type":"base","start":1,"end":10}],"revision":%d}`
	if _, err := h.Call(t, "POST /admin/roles/update", plugintest.Request{
		UserID: "a1", Administrator: true, Body: fmt.Sprintf(body, 0),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Call(t, "POST /admin/roles/update", plugintest.Request{
		UserID: "a1", Administrator: true, Body: fmt.Sprintf(body, 1),
	}); err != nil {
		t.Fatal(err)
	}
	_, err := h.Call(t, "POST /admin/roles/update", plugintest.Request{
		UserID: "a1", Administrator: true, Body: fmt.Sprintf(body, 1),
	})
	if err == nil {
		t.Fatal("古い revision で上書きできています")
	}
	if _, code := extractCode(err); code != CodeConfigConflict {
		t.Fatalf("code = %q, want %s (%v)", code, CodeConfigConflict, err)
	}
}

// **conditional role は level 対象ではない。**
func TestAdminConfigRejectsConditionalRole(t *testing.T) {
	api := levelRoleAPI()
	api.roles["r1"] = roleInfo{ID: "r1", Target: "conditional"}
	h := routeHarness(t, api)
	_, err := h.Call(t, "POST /admin/roles/update", plugintest.Request{
		UserID: "a1", Administrator: true,
		Body: `{"roleId":"r1","baseLevel":1,"experienceCurve":[{"type":"const","levelUps":9,"base":10}],"policyRanges":[{"type":"base","start":1,"end":10}],"revision":0}`,
	})
	if err == nil {
		t.Fatal("conditional role に level 設定を保存しています")
	}
	if _, code := extractCode(err); code != CodeRoleNotManual {
		t.Fatalf("code = %q, want %s (%v)", code, CodeRoleNotManual, err)
	}
}

// **XP 変更は idempotency key 必須。** 無いと二重送信で 2 回適用される。
func TestChangeExpRouteRequiresIdempotencyKey(t *testing.T) {
	h := routeHarness(t, levelRoleAPI())
	_, err := h.Call(t, "POST /admin/change-exp", plugintest.Request{
		UserID: "a1", Administrator: true,
		Body: `{"userId":"u1","roleId":"r1","mode":"add","operand":50}`,
	})
	if err == nil {
		t.Fatal("idempotencyKey 無しで受け付けています")
	}
	if _, code := extractCode(err); code != CodeIdempotencyKeyRequired {
		t.Fatalf("code = %q, want %s (%v)", code, CodeIdempotencyKeyRequired, err)
	}
}

// **operand は有限の小数を JSON から受ける。** 1.5 は ×1.5。
func TestChangeExpRouteAcceptsDecimalOperand(t *testing.T) {
	h := routeHarness(t, levelRoleAPI())
	if _, err := h.Call(t, "POST /admin/change-exp", plugintest.Request{
		UserID: "a1", Administrator: true,
		Body: `{"idempotencyKey":"k0","userId":"u1","roleId":"r1","mode":"set","operand":250}`,
	}); err != nil {
		t.Fatal(err)
	}
	res, err := h.Call(t, "POST /admin/change-exp", plugintest.Request{
		UserID: "a1", Administrator: true,
		Body: `{"idempotencyKey":"k1","userId":"u1","roleId":"r1","mode":"multiplier","operand":1.5}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Experience int64 `json:"experience"`
	}
	decode(t, res, &got)
	if got.Experience != 375 {
		t.Fatalf("experience = %d, want 375", got.Experience)
	}
}

func TestChangeExpRoute(t *testing.T) {
	h := routeHarness(t, levelRoleAPI())
	res, err := h.Call(t, "POST /admin/change-exp", plugintest.Request{
		UserID: "a1", Administrator: true,
		Body: `{"idempotencyKey":"k1","userId":"u1","roleId":"r1","mode":"add","operand":50,"note":"test"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		AssignmentID string `json:"assignmentId"`
		Experience   int64  `json:"experience"`
		Status       string `json:"status"`
	}
	decode(t, res, &got)
	if got.AssignmentID != "asg1" || got.Experience != 50 || got.Status != "completed" {
		t.Fatalf("= %+v", got)
	}
}

// **moderator は canEditMembersByModerator の role だけ。** 権限判定は route でも
// service でも同じ判定を1箇所にまとめる (Task 7)。
func TestChangeExpRouteModeratorGate(t *testing.T) {
	api := levelRoleAPI()
	api.roles["r1"] = roleInfo{ID: "r1", Target: "manual", CanEditMembersByModerator: true}
	h := routeHarness(t, api)
	if _, err := h.Call(t, "POST /admin/change-exp", plugintest.Request{
		UserID: "m1", Moderator: true,
		Body: `{"idempotencyKey":"k1","userId":"u1","roleId":"r1","mode":"add","operand":50}`,
	}); err != nil {
		t.Fatalf("開いている role を moderator に拒否しました: %v", err)
	}

	api.roles["r1"] = roleInfo{ID: "r1", Target: "manual"}
	h2 := routeHarness(t, api)
	if _, err := h2.Call(t, "POST /admin/change-exp", plugintest.Request{
		UserID: "m1", Moderator: true,
		Body: `{"idempotencyKey":"k2","userId":"u1","roleId":"r1","mode":"add","operand":50}`,
	}); err == nil {
		t.Fatal("閉じた role を moderator に通しています")
	}
}

// **public response は未付与の role を出さない。** native の role visibility を超える
// 情報を返さないため。
func TestPublicProfileOnlyReturnsAssignedRoles(t *testing.T) {
	api := levelRoleAPI()
	h := routeHarness(t, api)
	if _, err := h.Call(t, "POST /admin/roles/update", plugintest.Request{
		UserID: "a1", Administrator: true,
		Body: `{"roleId":"r1","baseLevel":1,"experienceCurve":[{"type":"const","levelUps":9,"base":10}],"policyRanges":[{"type":"base","start":1,"end":10}],"revision":0}`,
	}); err != nil {
		t.Fatal(err)
	}
	// u1 は r1 に付与済み、u9 は未付与。
	res, err := h.Call(t, "POST /users/show", plugintest.Request{Body: `{"userId":"u1"}`})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Roles []struct {
			RoleID string `json:"roleId"`
			Level  struct {
				CurrentLevel int64 `json:"currentLevel"`
			} `json:"level"`
		} `json:"roles"`
	}
	decode(t, res, &got)
	if len(got.Roles) != 1 || got.Roles[0].RoleID != "r1" || got.Roles[0].Level.CurrentLevel != 1 {
		t.Fatalf("= %+v", got)
	}

	res, err = h.Call(t, "POST /users/show", plugintest.Request{Body: `{"userId":"u9"}`})
	if err != nil {
		t.Fatal(err)
	}
	decode(t, res, &got)
	if len(got.Roles) != 0 {
		t.Fatalf("未付与の role を返しました: %+v", got)
	}

	api.roles["r1"] = roleInfo{ID: "r1", Target: "manual", IsPublic: false, IsExplorable: true}
	res, err = h.Call(t, "POST /users/show", plugintest.Request{Body: `{"userId":"u1"}`})
	if err != nil {
		t.Fatal(err)
	}
	decode(t, res, &got)
	if len(got.Roles) != 0 {
		t.Fatalf("assigned private role leaked: %+v", got.Roles)
	}
}

// **XP 順の member 一覧。** XP の無い member は 0 で並ぶ。
func TestRoleMembersOrderedByExperience(t *testing.T) {
	h := routeHarness(t, levelRoleAPI())
	if _, err := h.Call(t, "POST /admin/roles/update", plugintest.Request{
		UserID: "a1", Administrator: true,
		Body: `{"roleId":"r1","baseLevel":1,"experienceCurve":[{"type":"const","levelUps":9,"base":10}],"policyRanges":[{"type":"base","start":1,"end":10}],"revision":0}`,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Call(t, "POST /admin/change-exp", plugintest.Request{
		UserID: "a1", Administrator: true,
		Body: `{"idempotencyKey":"k1","userId":"u1","roleId":"r1","mode":"set","operand":500}`,
	}); err != nil {
		t.Fatal(err)
	}
	res, err := h.Call(t, "POST /roles/users", plugintest.Request{
		Body: `{"roleId":"r1","limit":10}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Total     int  `json:"total"`
		Truncated bool `json:"truncated"`
		Members   []struct {
			UserID     string `json:"userId"`
			Experience int64  `json:"experience"`
		} `json:"members"`
	}
	decode(t, res, &got)
	if got.Total != 2 || got.Truncated {
		t.Fatalf("= total %d / truncated %t", got.Total, got.Truncated)
	}
	if len(got.Members) != 2 {
		t.Fatalf("member = %d 人", len(got.Members))
	}
	if got.Members[0].UserID != "u1" || got.Members[0].Experience != 500 {
		t.Fatalf("XP 順になっていません: %+v", got.Members)
	}
}

// **監査を単独で引ける。** admin/user 画面の「履歴」タブ相当。
func TestRoleMembersRequireNativeExplorableVisibility(t *testing.T) {
	api := levelRoleAPI()
	h := routeHarness(t, api)
	if _, err := h.Call(t, "POST /admin/roles/update", plugintest.Request{
		UserID: "a1", Administrator: true,
		Body: `{"roleId":"r1","baseLevel":1,"experienceCurve":[{"type":"const","levelUps":1,"base":10}],"policyRanges":[{"type":"base","start":1,"end":3}],"revision":0}`,
	}); err != nil {
		t.Fatal(err)
	}
	api.roles["r1"] = roleInfo{ID: "r1", Target: "manual", IsPublic: true, IsExplorable: false}
	_, err := h.Call(t, "POST /roles/users", plugintest.Request{Body: `{"roleId":"r1"}`})
	if err == nil {
		t.Fatal("non-explorable native role leaked members")
	}
	se, code := extractCode(err)
	if code != CodeNativeRoleNotFound || se == nil || se.Status != http.StatusNotFound {
		t.Fatalf("status/code = %v/%q, want 404/%s", statusOf(se), code, CodeNativeRoleNotFound)
	}
}

func TestAdminAuditRoute(t *testing.T) {
	h := routeHarness(t, levelRoleAPI())
	if _, err := h.Call(t, "POST /admin/change-exp", plugintest.Request{
		UserID: "a1", Administrator: true,
		Body: `{"idempotencyKey":"k1","userId":"u1","roleId":"r1","mode":"add","operand":50,"note":"first"}`,
	}); err != nil {
		t.Fatal(err)
	}
	res, err := h.Call(t, "POST /admin/audit", plugintest.Request{
		UserID: "m1", Moderator: true, Body: `{"userId":"u1","limit":10}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Entries []struct {
			Operation string `json:"operation"`
			UserID    string `json:"userId"`
		} `json:"entries"`
	}
	decode(t, res, &got)
	if len(got.Entries) != 1 || got.Entries[0].Operation != "change-exp" || got.Entries[0].UserID != "u1" {
		t.Fatalf("= %+v", got.Entries)
	}
}

func TestAuditResponseJSONShape(t *testing.T) {
	entries := auditResponses([]auditEntry{{ID: 9007199254740993, ActorID: "a1", Operation: "config"}})
	raw, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got[0]["id"] != "9007199254740993" {
		t.Fatalf("id = %#v, want decimal string", got[0]["id"])
	}
	if value, ok := got[0]["userId"]; !ok || value != nil {
		t.Fatalf("userId = %#v (present=%t), want explicit null", value, ok)
	}
}

// orphanRoleEntry is one role's entry in the `{"roles":{…}}` body that
// `/admin/orphans` and the reconcile route both return.
type orphanRoleEntry struct {
	Tracked             int      `json:"tracked"`
	Orphans             int      `json:"orphans"`
	Prunable            int      `json:"prunable"`
	OrphanAssignmentIDs []string `json:"orphanAssignmentIds"`
}

// orphanRouteHarness is routeHarness plus two XP rows for r1: one whose native
// assignment (asg1) is still live, and one whose assignment is already gone.
//
// **live と消えた行を 1 本ずつ置く。** orphan 判定は native の一覧との差集合なので、
// 両方揃えないと「全部 live」「全部 orphan」のどちらかにしかならず、数え方を確かめられない。
func orphanRouteHarness(t *testing.T) plugintest.Handlers {
	t.Helper()
	db := testDBRequired(t)
	h := newHarness(t, db, levelRoleAPI()).
		WithConfig(map[string]any{"actorId": "admin1"}).Routes(Plugin)
	for _, row := range []struct {
		assignmentID, userID string
		experience           int64
	}{
		{"asg1", "u1", 10},
		{"gone", "u3", 20},
	} {
		if _, err := db.Exec(`INSERT INTO role_level_experience
			(assignment_id, role_id, user_id, experience) VALUES ($1, 'r1', $2, $3)`,
			row.assignmentID, row.userID, row.experience); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

// assertR1OrphanReport checks the report both reconciliation routes must return for
// the rows orphanRouteHarness writes.
//
// **prunable は 0。** 保持期間を超える前の行を「消える行」に見せない (jobs.go)。
func assertR1OrphanReport(t *testing.T, roles map[string]orphanRoleEntry) {
	t.Helper()
	if len(roles) != 1 {
		t.Fatalf("roles = %+v, want r1 だけ", roles)
	}
	entry, ok := roles["r1"]
	if !ok {
		t.Fatalf("r1 の report がありません: %+v", roles)
	}
	if entry.Tracked != 2 || entry.Orphans != 1 || entry.Prunable != 0 {
		t.Fatalf("r1 = %+v, want tracked 2 / orphans 1 / prunable 0", entry)
	}
	if fmt.Sprint(entry.OrphanAssignmentIDs) != "[gone]" {
		t.Fatalf("orphanAssignmentIds = %v, want [gone]", entry.OrphanAssignmentIDs)
	}
}

// **orphan route は Task 11 の判定結果をそのまま返す。** 削除はしないので、
// live な行も 1 番の tracked に残る。
func TestAdminOrphansRoute(t *testing.T) {
	h := orphanRouteHarness(t)

	res, err := h.Call(t, "POST /admin/orphans",
		plugintest.Request{UserID: "m1", Moderator: true, Body: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Roles map[string]orphanRoleEntry `json:"roles"`
	}
	decode(t, res, &got)
	assertR1OrphanReport(t, got.Roles)
}

// **route から reconciliation を 1 回呼べる。** 要求した mode がそのまま response の
// `mode` に出て、実行した段だけが `steps` に並ぶ。
func TestAdminReconcileRoute(t *testing.T) {
	h := orphanRouteHarness(t)

	res, err := h.Call(t, "POST /admin/reconcile", plugintest.Request{
		UserID: "a1", Administrator: true, Body: `{"mode":"reconcile-orphans"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Mode   string `json:"mode"`
		Result struct {
			Steps []struct {
				Mode   string `json:"mode"`
				Result struct {
					Roles map[string]orphanRoleEntry `json:"roles"`
				} `json:"result"`
			} `json:"steps"`
		} `json:"result"`
	}
	decode(t, res, &got)
	if got.Mode != reconcileModeOrphans {
		t.Fatalf("mode = %q, want %q", got.Mode, reconcileModeOrphans)
	}
	if len(got.Result.Steps) != 1 || got.Result.Steps[0].Mode != reconcileModeOrphans {
		t.Fatalf("steps = %+v, want %q 1 段だけ", got.Result.Steps, reconcileModeOrphans)
	}
	assertR1OrphanReport(t, got.Result.Steps[0].Result.Roles)
}

// **level 設定が無い role は 404。** 既定の level 設定を捏造して 200 で返さない。
// 同じ「level 設定が無い」状態に対して `/roles/users` が 404
// (CodeConfigNotFound) を返すので、両 route の答えを揃える。
func TestAdminRolesShowMissingConfigIsNotFound(t *testing.T) {
	h := routeHarness(t, levelRoleAPI())
	// r1 は native には存在するが plugin 側の level 設定が無い。
	// no-such-role は native にも存在しない。
	for _, roleID := range []string{"r1", "no-such-role"} {
		t.Run(roleID, func(t *testing.T) {
			_, err := h.Call(t, "POST /admin/roles/show", plugintest.Request{
				UserID: "m1", Moderator: true,
				Body: fmt.Sprintf(`{"roleId":%q}`, roleID),
			})
			if err == nil {
				t.Fatalf("%s: level 設定が無いのに既定設定を返した", roleID)
			}
			se, code := extractCode(err)
			if code != CodeConfigNotFound || se == nil || se.Status != 404 {
				t.Fatalf("status/code = %v/%q, want 404/%s (%v)", statusOf(se), code, CodeConfigNotFound, err)
			}
		})
	}
}

// fakeOrphanReporter stands in for the Task 11 orphan handler.
type fakeOrphanReporter struct {
	report map[string]any
	err    error
	calls  int
}

func (f *fakeOrphanReporter) OrphanReport(context.Context) (map[string]any, error) {
	f.calls++
	return f.report, f.err
}

// **orphan route は Task 11 の handler の結果をそのまま返す。** 結果を捨てると
// Task 11 の実装後も `{"roles":{…}}` が返らない。
func TestAdminOrphansHandlerReturnsReport(t *testing.T) {
	rep := &fakeOrphanReporter{report: map[string]any{
		"roles": map[string]any{"r1": map[string]any{"tracked": 1, "orphans": 2}},
	}}
	h := plugintest.Handlers{"POST /admin/orphans": orphanHandler(rep)}

	res, err := h.Call(t, "POST /admin/orphans",
		plugintest.Request{UserID: "m1", Moderator: true, Body: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	if rep.calls != 1 {
		t.Fatalf("OrphanReport calls = %d, want 1", rep.calls)
	}
	var got struct {
		Roles map[string]struct {
			Tracked int `json:"tracked"`
			Orphans int `json:"orphans"`
		} `json:"roles"`
	}
	decode(t, res, &got)
	if got.Roles["r1"].Tracked != 1 || got.Roles["r1"].Orphans != 2 {
		t.Fatalf("roles = %+v", got.Roles)
	}
}

// **orphan route の認可とエラー伝搬。** 未承認は通さない。handler のエラーは
// そのままクライアントに届く。
func TestAdminOrphansHandlerAuthorizationAndError(t *testing.T) {
	rep := &fakeOrphanReporter{report: map[string]any{}}
	h := plugintest.Handlers{"POST /admin/orphans": orphanHandler(rep)}

	if _, err := h.Call(t, "POST /admin/orphans", plugintest.Request{Body: `{}`}); err == nil {
		t.Fatal("未承認の user が通ってしまった")
	}
	if rep.calls != 0 {
		t.Fatalf("未承認で OrphanReport が %d 回呼ばれた", rep.calls)
	}

	rep.err = codedErrorf(http.StatusNotFound, CodeNativeRoleNotFound, "no such role")
	_, err := h.Call(t, "POST /admin/orphans",
		plugintest.Request{UserID: "m1", Moderator: true, Body: `{}`})
	if err == nil {
		t.Fatal("handler のエラーが飲み込まれた")
	}
	se, code := extractCode(err)
	if code != CodeNativeRoleNotFound || se == nil || se.Status != 404 {
		t.Fatalf("status/code = %v/%q, want 404/%s", statusOf(se), code, CodeNativeRoleNotFound)
	}
}
