package server

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shiroha-a/mk/internal/config"
	"github.com/shiroha-a/mk/internal/misc/permissions"
	"github.com/shiroha-a/mk/internal/model"
	"github.com/shiroha-a/mk/internal/server/middleware"
	"github.com/shiroha-a/mk/internal/testutil"
	"github.com/shiroha-a/mk/plugin"
	"github.com/stretchr/testify/require"
)

func TestRoleLevelXPAppTokenPolicy(t *testing.T) {
	for _, tt := range []struct {
		name, method, path, body string
		scopes                   []string
		moderator, native        bool
		status                   int
	}{
		{"XP scope and moderator", "POST", "/role-level/admin/change-exp", `{"note":"event reward"}`, []string{permissions.WriteRoleLevelExperience}, true, false, 200},
		{"scope does not grant moderator", "POST", "/role-level/admin/change-exp", `{}`, []string{permissions.WriteRoleLevelExperience}, false, false, 403},
		{"missing scope", "POST", "/role-level/admin/change-exp", `{}`, nil, true, false, 403},
		{"read account is insufficient", "POST", "/role-level/admin/change-exp", `{}`, []string{"read:account"}, true, false, 403},
		{"native role write is insufficient", "POST", "/role-level/admin/change-exp", `{}`, []string{"write:admin:roles"}, true, false, 403},
		{"wildcard is insufficient", "POST", "/role-level/admin/change-exp", `{}`, []string{"write:admin:*"}, true, false, 403},
		{"different method", "GET", "/role-level/admin/change-exp", `{}`, []string{permissions.WriteRoleLevelExperience}, true, false, 403},
		{"other role-level route", "POST", "/role-level/admin/roles/update", `{}`, []string{permissions.WriteRoleLevelExperience}, true, false, 403},
		{"public plugin route still rejects app", "POST", "/role-level/users/show", `{}`, []string{permissions.WriteRoleLevelExperience}, true, false, 403},
		{"other plugin", "POST", "/genshin/admin/change-exp", `{}`, []string{permissions.WriteRoleLevelExperience}, true, false, 403},
		{"strict body still rejects i", "POST", "/role-level/admin/change-exp", `{"i":"not-a-credential"}`, []string{permissions.WriteRoleLevelExperience}, true, false, 400},
		{"native moderator unchanged", "POST", "/role-level/admin/change-exp", `{}`, nil, true, true, 200},
		{"native normal account denied", "POST", "/role-level/admin/change-exp", `{}`, nil, false, true, 403},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, api := newPluginTestServer(config.RoleServer)
			s.pluginRoles = &stubRoles{mod: tt.moderator}
			users := testutil.NewMockUserRepository()
			tokens := testutil.NewMockAccessTokenRepository()
			user := &model.User{ID: "caller", Username: "caller"}
			users.Users[user.ID] = user
			const rawToken = "rolelevel-scope-test-only"
			if tt.native {
				users.Tokens[rawToken] = user
			} else {
				sum := sha256.Sum256([]byte(rawToken))
				hash := hex.EncodeToString(sum[:])
				tokens.Tokens[hash] = &model.AccessToken{ID: "test-key", Hash: hash, UserID: user.ID, User: user, Permission: model.StringArray(tt.scopes)}
			}
			auth := middleware.NewAuthMiddleware(users, tokens)
			s.echo.Use(auth.Authenticate())
			reached := false
			handler := func(req plugin.Request) (any, error) {
				if !req.IsModerator() {
					return nil, plugin.Errorf(http.StatusForbidden, "moderator required")
				}
				var body struct {
					Note string `json:"note"`
				}
				if err := req.BindStrict(&body); err != nil {
					return nil, plugin.Errorf(http.StatusBadRequest, "invalid body")
				}
				reached = true
				return map[string]string{"actorId": req.UserID(), "note": body.Note}, nil
			}
			def := pluginDef("role-level", func(_ plugin.Context, r plugin.Router) error {
				r.POST("/admin/change-exp", handler)
				r.GET("/admin/change-exp", handler)
				r.POST("/admin/roles/update", handler)
				r.POST("/users/show", handler)
				return nil
			}, nil)
			other := pluginDef("genshin", func(_ plugin.Context, r plugin.Router) error {
				r.POST("/admin/change-exp", handler)
				return nil
			}, nil)
			require.NoError(t, s.setupPlugins(api, []plugin.Definition{def, other}, noopStorage))
			req := httptest.NewRequest(tt.method, "/api/plugin"+tt.path, strings.NewReader(tt.body))
			req.Header.Set("Authorization", "Bearer "+rawToken)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			s.echo.ServeHTTP(rec, req)
			require.Equal(t, tt.status, rec.Code, rec.Body.String())
			require.Equal(t, tt.status == http.StatusOK, reached)
			if tt.status == http.StatusOK {
				require.Contains(t, rec.Body.String(), `"actorId":"caller"`)
			}
		})
	}
}
