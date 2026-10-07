package rolelevel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/elythia-network/elythia/plugin"
)

// scriptedCaller answers each endpoint with a pre-baked raw body.
//
// stubAPI は production と同じ形を返すことが目的なので、ここに「壊れた応答」を混ぜると production との差異が分かりにくくなる。
type scriptedCaller struct {
	mu     sync.Mutex
	bodies map[string]string
	errs   map[string]error
	calls  int
}

func (c *scriptedCaller) Call(_ context.Context, endpoint string, _ any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if err, ok := c.errs[endpoint]; ok {
		return nil, err
	}
	body, ok := c.bodies[endpoint]
	if !ok {
		return nil, apiError(endpoint, http.StatusNotImplemented, "SCRIPTED_MISS")
	}
	return json.RawMessage(body), nil
}

func (c *scriptedCaller) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func newScriptedNative(bodies map[string]string, pages int) *nativeRole {
	return &nativeRole{caller: &scriptedCaller{bodies: bodies}, pages: pages}
}

func newTestNative(t *testing.T, api *stubAPI, pages int) *nativeRole {
	t.Helper()
	return &nativeRole{caller: api.AsUser("admin1"), pages: pages}
}

func TestNativeShow(t *testing.T) {
	api := &stubAPI{roles: map[string]roleInfo{
		"r1": {ID: "r1", Target: "manual", CanEditMembersByModerator: true},
	}}
	n := newTestNative(t, api, 2)
	got, err := n.Show(context.Background(), "r1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Target != "manual" || !got.CanEditMembersByModerator {
		t.Fatalf("= %+v", got)
	}
	// 無い role は stable code 付きで 404相当。
	_, err = n.Show(context.Background(), "nope")
	if err == nil {
		t.Fatal("無い role を受け入れています")
	}
	_, code := extractCode(err)
	if code != CodeNativeRoleNotFound {
		t.Fatalf("code = %q, want %s (%v)", code, CodeNativeRoleNotFound, err)
	}
}

// **conditional role は扱わない。** level は manual assignment に紐づくので、
// conditional に対して XP を持たせられると policy の置換先が壊れる。
func TestNativeRequireManual(t *testing.T) {
	api := &stubAPI{roles: map[string]roleInfo{
		"cond": {ID: "cond", Target: "conditional"},
	}}
	n := newTestNative(t, api, 2)
	_, err := n.RequireManual(context.Background(), "cond")
	if err == nil {
		t.Fatal("conditional role を受け入れています")
	}
	_, code := extractCode(err)
	if code != CodeRoleNotManual {
		t.Fatalf("code = %q, want %s (%v)", code, CodeRoleNotManual, err)
	}
}

func TestNativeFindAssignment(t *testing.T) {
	api := &stubAPI{assignments: map[string][]assignment{
		"r1": {mkAssignment("asg1", "u1")},
	}}
	n := newTestNative(t, api, 2)
	got, found, err := n.FindAssignment(context.Background(), "r1", "u1")
	if err != nil || !found {
		t.Fatalf("found = %t %v", found, err)
	}
	if got != "asg1" {
		t.Fatalf("assignmentID = %q, want asg1", got)
	}
	if _, found, err := n.FindAssignment(context.Background(), "r1", "u2"); err != nil || found {
		t.Fatalf("居ない利用者を found にしています: %t %v", found, err)
	}
}

// **走査上限を超えたら「不明」を返す。** 黙って「居ない」と言うと、未付与の
// user に XP を割り当ててしまう。
func TestNativeFindAssignmentScanExhausted(t *testing.T) {
	var many []assignment
	for i := 0; i < 250; i++ {
		many = append(many, mkAssignment(
			fmt.Sprintf("asg-%03d", i), fmt.Sprintf("u%03d", i)))
	}
	api := &stubAPI{assignments: map[string][]assignment{"r1": many}}
	n := newTestNative(t, api, 1) // 100件しか見ない

	_, found, err := n.FindAssignment(context.Background(), "r1", "u000")
	if err == nil {
		t.Fatalf("上限を超えたのにエラーになりません (found = %t)", found)
	}
	_, code := extractCode(err)
	if code != CodeAssignmentScanExhausted {
		t.Fatalf("code = %q, want %s (%v)", code, CodeAssignmentScanExhausted, err)
	}
	if found {
		t.Fatal("上限超過で found になってはいけません")
	}
}

// **走査上限に達しても、取得済み page に居る user は見つける。** truncated は
// 「未走査部分に居ないとは断言できない」であって、取得済み結果を捨てる理由ではない。
func TestNativeFindAssignmentFindsUserBeforeScanLimit(t *testing.T) {
	var many []assignment
	for i := 0; i < 250; i++ {
		many = append(many, mkAssignment(
			fmt.Sprintf("asg-%03d", i), fmt.Sprintf("u%03d", i)))
	}
	api := &stubAPI{assignments: map[string][]assignment{"r1": many}}
	n := newTestNative(t, api, 1)

	got, found, err := n.FindAssignment(context.Background(), "r1", "u249")
	if err != nil || !found || got != "asg-249" {
		t.Fatalf("assignmentID = %q, found = %t, err = %v", got, found, err)
	}
}

// **admin/roles/assign の 409 は目的の状態。** 冪等に回すので、既に付いていること
// を異常系にすると reconcile のたびに失敗が積み上がる (plugins/trustlevel と同じ)。
func TestNativeAssignTreatsConflictAsSuccess(t *testing.T) {
	api := &stubAPI{assignments: map[string][]assignment{}}
	n := newTestNative(t, api, 2)
	if err := n.Assign(context.Background(), "u1", "r1"); err != nil {
		t.Fatal(err)
	}
	// stub は既存 assignment を検出して production-shaped 409 を返す。
	if err := n.Assign(context.Background(), "u1", "r1"); err != nil {
		t.Fatalf("重複 assign の 409 を失敗として扱いました: %v", err)
	}
	if len(api.assignments["r1"]) != 1 {
		t.Fatalf("重複 assignment を作成しました: %+v", api.assignments["r1"])
	}
	api.assignStatus = http.StatusInternalServerError
	err := n.Assign(context.Background(), "u1", "r1")
	if err == nil {
		t.Fatal("500 を成功として扱いました")
	}
	_, code := extractCode(err)
	if code != CodeNativeAPIFailed {
		t.Fatalf("code = %q, want %s (%v)", code, CodeNativeAPIFailed, err)
	}
}

func TestNativeAssigned(t *testing.T) {
	api := &stubAPI{assignments: map[string][]assignment{"r1": {mkAssignment("asg1", "u1")}}}
	n := newTestNative(t, api, 2)
	got, err := n.Assigned(context.Background(), "r1", "u1")
	if err != nil || !got {
		t.Fatalf("= %t %v", got, err)
	}
	got, err = n.Assigned(context.Background(), "r1", "u2")
	if err != nil || got {
		t.Fatalf("= %t %v", got, err)
	}
}

// **usernames は production の上限100件ごとにまとめて引く。** 1件ずつ users/show
// を呼ぶと member 一覧の人数だけ round trip が増える一方、101件以上を1回で渡すと
// production の users/show が先頭100件に切り捨てる。
func TestNativeUsernames(t *testing.T) {
	api := &stubAPI{}
	n := newTestNative(t, api, 2)
	ids := make([]string, 101)
	for i := range ids {
		ids[i] = fmt.Sprintf("u%03d", i)
	}
	got, err := n.Usernames(context.Background(), ids)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if got[id] != "user-"+id {
			t.Fatalf("username[%s] = %q", id, got[id])
		}
	}
	if calls := api.usersShowCallCount(); calls != 2 {
		t.Fatalf("users/show calls = %d, want 2", calls)
	}
	if m, err := n.Usernames(context.Background(), nil); err != nil || m == nil || len(m) != 0 {
		t.Fatalf("空リスト = %+v %v", m, err)
	}
}

func TestNativeListAssignmentsPageCapBoundary(t *testing.T) {
	for _, tt := range []struct {
		name      string
		count     int
		truncated bool
	}{
		{"99 rows", 99, false},
		{"100 rows", 100, true},
		{"101 rows", 101, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rows := make([]assignment, tt.count)
			for i := range rows {
				rows[i] = mkAssignment(fmt.Sprintf("asg-%03d", i), fmt.Sprintf("u%03d", i))
			}
			api := &stubAPI{assignments: map[string][]assignment{"r1": rows}}
			got, truncated, err := newTestNative(t, api, 1).ListAssignments(context.Background(), "r1")
			if err != nil {
				t.Fatal(err)
			}
			wantRows := tt.count
			if wantRows > nativePageSize {
				wantRows = nativePageSize
			}
			if len(got) != wantRows || truncated != tt.truncated {
				t.Fatalf("rows = %d, truncated = %t; want rows = %d, truncated = %t",
					len(got), truncated, wantRows, tt.truncated)
			}
		})
	}
}

func TestNativeAPIFailureMapping(t *testing.T) {
	for _, tt := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"missing role", apiError("admin/roles/show", http.StatusBadRequest, "NO_SUCH_ROLE"), http.StatusNotFound, CodeNativeRoleNotFound},
		{"unrelated 400", apiError("admin/roles/show", http.StatusBadRequest, "INVALID_PARAM"), http.StatusBadGateway, CodeNativeAPIFailed},
		{"500", apiError("admin/roles/show", http.StatusInternalServerError, "FAILED"), http.StatusBadGateway, CodeNativeAPIFailed},
		{"malformed response", &plugin.APIError{Endpoint: "admin/roles/show", Status: http.StatusBadRequest, Body: []byte(`not-json`)}, http.StatusBadGateway, CodeNativeAPIFailed},
		{"non-API error", errors.New("transport failed"), http.StatusBadGateway, CodeNativeAPIFailed},
	} {
		t.Run(tt.name, func(t *testing.T) {
			se, code := extractCode(newTestNative(t, &stubAPI{}, 1).apiFailure(context.Background(), "show", tt.err))
			if se == nil || se.Status != tt.status || code != tt.code {
				t.Fatalf("status = %v, code = %q; want status = %d, code = %s", se, code, tt.status, tt.code)
			}
		})
	}
}

// 別の role が返ってきたときに「その role の policy を編集した」ことになるため、id の完全一致を検証しない経路は壊れた role をそのまま下流へ渡してしまう。
func TestNativeShowRejectsMismatchedID(t *testing.T) {
	n := newScriptedNative(map[string]string{
		"admin/roles/show": `{"id":"other","target":"manual","canEditMembersByModerator":true}`,
	}, 1)
	_, err := n.Show(context.Background(), "r1")
	if err == nil {
		t.Fatal("別 id の role を受け入れています")
	}
	se, code := extractCode(err)
	if se == nil || se.Status != http.StatusBadGateway || code != CodeNativeAPIFailed {
		t.Fatalf("status = %v, code = %q; want 502, %s (%v)", se, code, CodeNativeAPIFailed, err)
	}
}

// **assigned の「false」と「無し」を混ぜない。** `assigned` が無い応答を false として
// 読むと、未付与の user に XP を割り当ててしまう。missing と null は両方 502。
func TestNativeAssignedRequiresPresentFlag(t *testing.T) {
	for _, tt := range []struct {
		name    string
		body    string
		want    bool
		wantErr bool
	}{
		{"true", `{"assigned":true,"expiresAt":null}`, true, false},
		{"false", `{"assigned":false,"expiresAt":null}`, false, false},
		{"missing", `{"expiresAt":null}`, false, true},
		{"null", `{"assigned":null}`, false, true},
		{"malformed", `not-json`, false, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			n := newScriptedNative(map[string]string{
				"admin/roles/assignment-show": tt.body,
			}, 1)
			got, err := n.Assigned(context.Background(), "r1", "u1")
			if tt.wantErr {
				if err == nil {
					t.Fatalf("%q を %t として受け入れた", tt.body, got)
				}
				se, code := extractCode(err)
				if se == nil || se.Status != http.StatusBadGateway || code != CodeNativeAPIFailed {
					t.Fatalf("status = %v, code = %q; want 502, %s (%v)", se, code, CodeNativeAPIFailed, err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("= %t %v, want %t", got, err, tt.want)
			}
		})
	}
}

// 壊れた行のカーソルでページングを続けない。空の assignment id を untilId に使うと1ページ目を繰り返し取得し、同じ行が重複して蓄積される。
func TestNativeListAssignmentsRejectsUnusableRows(t *testing.T) {
	oversized := make([]string, 0, nativePageSize+1)
	for i := 0; i <= nativePageSize; i++ {
		oversized = append(oversized, fmt.Sprintf(
			`{"id":"asg-%03d","user":{"id":"u%03d"}}`, i, i))
	}
	for _, tt := range []struct {
		name string
		rows string
	}{
		// 最後の行だけ id が空 = カーソルが空になり得る形。
		{"empty cursor id", `[{"id":"asg-001","user":{"id":"u001"}},` +
			`{"id":"asg-002","user":{"id":"u002"}},` +
			`{"id":"","user":{"id":"u003"}}]`},
		{"empty user id", `[{"id":"asg-001","user":{"id":""}}]`},
		{"empty user id object", `[{"id":"asg-001","user":{}}]`},
		{"oversized page", "[" + joinJSON(oversized) + "]"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			caller := &scriptedCaller{bodies: map[string]string{"admin/roles/users": tt.rows}}
			n := &nativeRole{caller: caller, pages: 3}
			_, _, err := n.ListAssignments(context.Background(), "r1")
			if err == nil {
				t.Fatalf("壊れた行をエラーにせず受け入れました")
			}
			se, code := extractCode(err)
			if se == nil || se.Status != http.StatusBadGateway || code != CodeNativeAPIFailed {
				t.Fatalf("status = %v, code = %q; want 502, %s (%v)", se, code, CodeNativeAPIFailed, err)
			}
			// 1ページ目で落とす。再取得していなければ重複ページも起きない。
			if got := caller.callCount(); got != 1 {
				t.Fatalf("admin/roles/users calls = %d, want 1 (再取得で pages を回していない)", got)
			}
		})
	}
}

func joinJSON(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ","
		}
		out += p
	}
	return out
}
