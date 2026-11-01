package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elythia-network/elythia/internal/config"
	corerole "github.com/elythia-network/elythia/internal/core/role"
	"github.com/elythia-network/elythia/internal/misc/id"
	"github.com/elythia-network/elythia/internal/model"
	"github.com/elythia-network/elythia/internal/repository"
	"github.com/elythia-network/elythia/internal/server/middleware"
	"github.com/elythia-network/elythia/plugin"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func TestPluginRequestActiveRolesFailsClosed(t *testing.T) {
	for _, authenticated := range []bool{false, true} {
		c := echo.New().NewContext(httptest.NewRequest("GET", "/?role=forged", nil), httptest.NewRecorder())
		if authenticated {
			c.Set(string(middleware.UserContextKey), &model.User{ID: "viewer"})
		}
		r := &pluginRequest{c: c}
		roles, ok := any(r).(interface{ ActiveRoleIDs() ([]string, error) })
		require.True(t, ok, "HTTP requests must expose the optional active-role capability")
		ids, err := roles.ActiveRoleIDs()
		if authenticated {
			require.Error(t, err)
			require.Nil(t, ids)
		} else {
			require.NoError(t, err)
			require.Equal(t, []string{}, ids)
		}
	}
}

func TestPluginActiveRolesUseAuthenticatedServerSnapshot(t *testing.T) {
	require.NotNil(t, serverIntegrationDB, "isolated PostgreSQL is required")
	db := serverIntegrationDB
	const viewer = "plugin-active-role-viewer"
	const token = "active-role-test"
	const other = "plugin-active-role-other"
	const otherToken = "other-role-token"
	const appToken = "plugin-role-app-token"
	tokenValue := token
	otherTokenValue := otherToken
	roles := []string{"plugin-active-z", "plugin-active-expired", "plugin-active-a"}
	cleanup := func() {
		require.NoError(t, db.Where(`"userId" IN ?`, []string{viewer, other}).Delete(&model.AccessToken{}).Error)
		require.NoError(t, db.Where(`"userId" IN ?`, []string{viewer, other}).Delete(&model.RoleAssignment{}).Error)
		require.NoError(t, db.Where("id IN ?", roles).Delete(&model.Role{}).Error)
		require.NoError(t, db.Where("id IN ?", []string{viewer, other}).Delete(&model.User{}).Error)
	}
	cleanup()
	t.Cleanup(cleanup)
	users := repository.NewUserRepository(db)
	require.NoError(t, users.Create(&model.User{ID: viewer, Username: viewer, UsernameLower: viewer, Token: &tokenValue, IsBot: true}))
	require.NoError(t, users.Create(&model.User{ID: other, Username: other, UsernameLower: other, Token: &otherTokenValue}))
	require.NoError(t, repository.NewAccessTokenRepository(db).Create(&model.AccessToken{ID: "plugin-role-app", UserID: viewer, Token: appToken, Hash: appToken, Permission: model.StringArray{"read:account"}}))
	for _, roleID := range roles {
		role := &model.Role{ID: roleID, Name: roleID, Target: model.RoleTargetManual}
		if roleID == roles[2] {
			role.Target = model.RoleTargetConditional
			role.CondFormula = datatypes.JSON(`{"type":"isBot"}`)
		}
		require.NoError(t, repository.NewRoleRepository(db).Create(role))
	}
	past := time.Now().Add(-time.Hour)
	require.NoError(t, repository.NewRoleAssignmentRepository(db).Create(&model.RoleAssignment{ID: "plugin-active-assignment", UserID: viewer, RoleID: roles[0]}))
	require.NoError(t, repository.NewRoleAssignmentRepository(db).Create(&model.RoleAssignment{ID: "plugin-expired-assignment", UserID: viewer, RoleID: roles[1], ExpiresAt: &past}))
	require.NoError(t, repository.NewRoleAssignmentRepository(db).Create(&model.RoleAssignment{ID: "plugin-other-assignment", UserID: other, RoleID: roles[1]}))
	// conditionalへ変更した後に残った割当行との重複も、requestでは1件にする。
	require.NoError(t, repository.NewRoleAssignmentRepository(db).Create(&model.RoleAssignment{ID: "plugin-conditional-assignment", UserID: viewer, RoleID: roles[2]}))
	newService := func(roleDB, assignmentDB, userDB *gorm.DB) *corerole.Service {
		generator, err := id.NewGenerator("aidx")
		require.NoError(t, err)
		service := corerole.NewService(repository.NewRoleRepository(roleDB), repository.NewRoleAssignmentRepository(assignmentDB), repository.NewMetaRepository(db), generator)
		service.SetUserRepo(repository.NewUserRepository(userDB))
		return service
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	failedDB := db.WithContext(ctx)
	for _, scenario := range []struct {
		name                         string
		roleDB, assignmentDB, userDB *gorm.DB
		failedLookup, warmPartial    bool
	}{
		{"healthy", db, db, db, false, false},
		{"assignments fail", db, failedDB, db, true, false},
		{"conditional list fails", failedDB, db, db, true, false},
		{"conditional user fails", db, db, failedDB, true, false},
		{"lenient cached conditional list failure", failedDB, db, db, true, true},
		{"lenient cached conditional user failure", db, db, failedDB, true, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			s, api := newPluginTestServer(config.RoleServer)
			s.roleService = newService(scenario.roleDB, scenario.assignmentDB, scenario.userDB)
			if scenario.warmPartial {
				partial, err := s.roleService.GetUserRoles(viewer)
				require.NoError(t, err, "legacy lookup must keep its lenient behavior")
				require.Len(t, partial, 2)
				require.ElementsMatch(t, []string{roles[0], roles[2]}, []string{partial[0].ID, partial[1].ID})
			}
			c := echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/", nil), httptest.NewRecorder())
			c.Set(string(middleware.UserContextKey), &model.User{ID: viewer})
			ids, lookupErr := (&pluginRequest{c: c, roleLister: s.roleService}).ActiveRoleIDs()
			if scenario.failedLookup {
				require.Error(t, lookupErr, "conditional lookup errors must not become partial success")
				require.Nil(t, ids)
			} else {
				require.NoError(t, lookupErr)
				require.Equal(t, []string{roles[2], roles[0]}, ids)
			}
			s.echo.Use(middleware.NewAuthMiddleware(users, repository.NewAccessTokenRepository(db)).Authenticate())
			reached := false
			def := pluginDef("active-role-test", func(_ plugin.Context, routes plugin.Router) error {
				handler := func(request plugin.Request) (any, error) {
					reached = true
					optional, ok := request.(plugin.ActiveRoleRequest)
					require.True(t, ok)
					return optional.ActiveRoleIDs()
				}
				routes.GET("/roles", handler)
				routes.POST("/roles", handler)
				return nil
			}, nil)
			require.NoError(t, s.setupPlugins(api, []plugin.Definition{def}, noopStorage))
			for _, method := range []string{http.MethodGet, http.MethodPost} {
				request := httptest.NewRequest(method, "/api/plugin/active-role-test/roles?role=forged&userId="+other, strings.NewReader(`{"roleIds":["forged"],"userId":"`+other+`","i":"`+otherToken+`"}`))
				request.Header.Set("Authorization", "Bearer "+token)
				request.Header.Set("Content-Type", "application/json")
				recorder := httptest.NewRecorder()
				s.echo.ServeHTTP(recorder, request)
				if scenario.failedLookup {
					require.Equal(t, http.StatusInternalServerError, recorder.Code, recorder.Body.String())
					require.NotContains(t, recorder.Body.String(), roles[0])
				} else {
					require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
					var ids []string
					require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &ids))
					require.Equal(t, []string{roles[2], roles[0]}, ids)
				}
				for _, credential := range []string{"", appToken} {
					reached = false
					request := httptest.NewRequest(method, "/api/plugin/active-role-test/roles?userId="+other, strings.NewReader(`{"userId":"`+other+`","roleIds":["forged"]}`))
					request.Header.Set("Content-Type", "application/json")
					if credential != "" {
						request.Header.Set("Authorization", "Bearer "+credential)
					}
					recorder := httptest.NewRecorder()
					s.echo.ServeHTTP(recorder, request)
					if credential == "" {
						require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
						require.JSONEq(t, `[]`, recorder.Body.String())
						require.True(t, reached)
					} else {
						require.Equal(t, http.StatusForbidden, recorder.Code, recorder.Body.String())
						require.Contains(t, recorder.Body.String(), "PERMISSION_DENIED")
						require.False(t, reached, "app token must not reach the plugin handler")
					}
				}
			}
		})
	}
}
