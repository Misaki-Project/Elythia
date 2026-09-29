package rolelevel

import (
	"context"
	"net/http"
	"testing"

	"github.com/shiroha-a/mk/plugin"
	"github.com/shiroha-a/mk/plugin/plugintest"
)

// **管理用のルートは自分で守る。** Router は認証の有無しか見ないので、管理画面を
// 出しただけでは API は誰でも叩ける (plugin/http.go の doc)。
func TestRequireAdmin(t *testing.T) {
	for _, tt := range []struct {
		name string
		req  plugintest.Request
		code string
	}{
		{"未認証", plugintest.Request{}, CodeUnauthenticated},
		{"authenticated only", plugintest.Request{UserID: "u1"}, CodeForbidden},
		{"moderator", plugintest.Request{UserID: "m1", Moderator: true}, CodeForbidden},
		{"administrator", plugintest.Request{UserID: "a1", Administrator: true}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := requireAdmin(httptestRequest(t, tt.req))
			if tt.code == "" {
				if err != nil {
					t.Fatalf("拒否しました: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("受け入れています")
			}
			se, code := extractCode(err)
			if code != tt.code {
				t.Fatalf("code = %q, want %s (%v)", code, tt.code, err)
			}
			wantStatus := http.StatusForbidden
			if tt.code == CodeUnauthenticated {
				wantStatus = http.StatusUnauthorized
			}
			if tt.code == CodeNativeRoleNotFound {
				wantStatus = http.StatusNotFound
			}
			if tt.code == CodeNativeAPIFailed {
				wantStatus = http.StatusBadGateway
			}
			if se == nil || se.Status != wantStatus {
				t.Fatalf("status = %v, want %d (%v)", se, wantStatus, err)
			}
		})
	}
}

func TestRequireModerator(t *testing.T) {
	for _, tt := range []struct {
		name string
		req  plugintest.Request
		code string
	}{
		{"未認証", plugintest.Request{}, CodeUnauthenticated},
		{"authenticated only", plugintest.Request{UserID: "u1"}, CodeForbidden},
		{"moderator", plugintest.Request{UserID: "m1", Moderator: true}, ""},
		{"administrator", plugintest.Request{UserID: "a1", Administrator: true}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := requireModerator(httptestRequest(t, tt.req))
			if tt.code == "" {
				if err != nil {
					t.Fatalf("拒否しました: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("受け入れています")
			}
			if _, code := extractCode(err); code != tt.code {
				t.Fatalf("code = %q, want %s (%v)", code, tt.code, err)
			}
		})
	}
}

// **moderator は canEditMembersByModerator が true の role だけ。** false なら
// administrator 限定。これは `IsAdministrator` と `canEditMembersByModerator` の
// 直接 preflight だけを行う。native の assign / unassign 全体との parity は主張しない。
func TestAuthorizeXPChange(t *testing.T) {
	api := &stubAPI{roles: map[string]roleInfo{
		"open":  {ID: "open", Target: "manual", CanEditMembersByModerator: true},
		"shut":  {ID: "shut", Target: "manual"},
		"admin": {ID: "admin", Target: "manual", IsAdministrator: true, CanEditMembersByModerator: true},
	}}
	svc := newAuthzService(t, api)

	for _, tt := range []struct {
		name      string
		req       plugintest.Request
		role      string
		code      string
		actorless bool
		show      int
	}{
		{"未認証", plugintest.Request{}, "open", CodeUnauthenticated, false, 0},
		{"素の user", plugintest.Request{UserID: "u1"}, "open", CodeForbidden, false, 0},
		{"moderator + open", plugintest.Request{UserID: "m1", Moderator: true}, "open", "", false, 0},
		{"moderator + shut", plugintest.Request{UserID: "m1", Moderator: true}, "shut", CodeRoleNotAssignable, false, 0},
		{"moderator + admin role", plugintest.Request{UserID: "m1", Moderator: true}, "admin", CodeRoleNotAssignable, false, 0},
		{"administrator + shut", plugintest.Request{UserID: "a1", Administrator: true}, "shut", "", false, 0},
		{"administrator + admin role", plugintest.Request{UserID: "a1", Administrator: true}, "admin", "", false, 0},
		{"actorless moderator", plugintest.Request{UserID: "m1", Moderator: true}, "open", CodeActorNotConfigured, true, 0},
		{"actorless administrator", plugintest.Request{UserID: "a1", Administrator: true}, "shut", CodeActorNotConfigured, true, 0},
		{"moderator missing role", plugintest.Request{UserID: "m1", Moderator: true}, "missing", CodeNativeRoleNotFound, false, 0},
		{"moderator native 500", plugintest.Request{UserID: "m1", Moderator: true}, "open", CodeNativeAPIFailed, false, http.StatusInternalServerError},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if tt.actorless {
				svc.cfg.ActorID = ""
			} else {
				svc.cfg.ActorID = "admin1"
			}
			api.showErr = tt.show
			err := svc.AuthorizeXPChange(httptestRequest(t, tt.req), tt.role)
			api.showErr = 0
			if tt.code == "" {
				if err != nil {
					t.Fatalf("拒否しました: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("受け入れています")
			}
			se, code := extractCode(err)
			if code != tt.code {
				t.Fatalf("code = %q, want %s (%v)", code, tt.code, err)
			}
			wantStatus := http.StatusForbidden
			if tt.code == CodeUnauthenticated {
				wantStatus = http.StatusUnauthorized
			}
			if tt.code == CodeNativeRoleNotFound {
				wantStatus = http.StatusNotFound
			}
			if tt.code == CodeNativeAPIFailed {
				wantStatus = http.StatusBadGateway
			}
			if se == nil || se.Status != wantStatus {
				t.Fatalf("status = %v, want %d (%v)", se, wantStatus, err)
			}
		})
	}
}

// **level 設定の変更は administrator だけ。** moderator が curve を変えて権限を
// 緩められると、canEditMembersByModerator の意味が壊れる。
func TestRequireConfigAdminValidatesConfiguredActorRole(t *testing.T) {
	api := &stubAPI{roles: map[string]roleInfo{
		"manual":      {ID: "manual", Target: "manual"},
		"conditional": {ID: "conditional", Target: "conditional"},
	}}
	svc := newAuthzService(t, api)
	for _, tt := range []struct {
		name string
		role string
		show int
		code string
	}{
		{"manual", "manual", 0, ""},
		{"conditional", "conditional", 0, CodeRoleNotManual},
		{"missing", "missing", 0, CodeNativeRoleNotFound},
		{"show error", "manual", http.StatusInternalServerError, CodeNativeAPIFailed},
	} {
		t.Run(tt.name, func(t *testing.T) {
			api.showErr = tt.show
			err := svc.RequireConfigAdmin(context.Background(), tt.role)
			api.showErr = 0
			if tt.code == "" {
				if err != nil {
					t.Fatalf("manual role を拒否しました: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("受け入れています")
			}
			if _, code := extractCode(err); code != tt.code {
				t.Fatalf("code = %q, want %s (%v)", code, tt.code, err)
			}
		})
	}
	svc.cfg.ActorID = ""
	if err := svc.RequireConfigAdmin(context.Background(), "manual"); err == nil {
		t.Fatal("actorId 空文字を受け入れています")
	} else if _, code := extractCode(err); code != CodeActorNotConfigured {
		t.Fatalf("code = %q, want %s (%v)", code, CodeActorNotConfigured, err)
	}
}

// helpers

// newAuthzService builds a service bound to a stub API without a database.
// Authorization only uses config, native API and in-memory service fields; it must
// not run migrations or issue SQL queries.
func newAuthzService(t *testing.T, api *stubAPI) *service {
	t.Helper()
	h := plugintest.New(t).WithName("role-level").
		WithConfig(map[string]any{"actorId": "admin1"}).WithAPI(api)
	svc, err := newService(h.Context())
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

// httptestRequest builds the plugin.Request a route handler would receive.
//
// **authz は plugin.Request の 3 つ (UserID / IsModerator / IsAdministrator) だけを見る。**
// plugintest の Harness は handler を公開しないので、同じ入力を作る最小実装をここに
// 持つ。route のテスト (Task 10) では harness 経由で確認する。
func httptestRequest(t *testing.T, r plugintest.Request) plugin.Request {
	t.Helper()
	return &fakeReq{
		userID:    r.UserID,
		moderator: r.Moderator || r.Administrator,
		admin:     r.Administrator,
	}
}

// fakeReq is a minimal plugin.Request for the authorization tests.
type fakeReq struct {
	ctx       context.Context
	userID    string
	moderator bool
	admin     bool
}

func (r *fakeReq) Context() context.Context {
	if r.ctx == nil {
		return context.Background()
	}
	return r.ctx
}

func (r *fakeReq) Bind(any) error       { return nil }
func (r *fakeReq) BindStrict(any) error { return nil }

func (r *fakeReq) Param(string) string { return "" }

func (r *fakeReq) Query(string) string { return "" }

func (r *fakeReq) UserID() string { return r.userID }

func (r *fakeReq) IsModerator() bool { return r.moderator }

func (r *fakeReq) IsAdministrator() bool { return r.admin }
