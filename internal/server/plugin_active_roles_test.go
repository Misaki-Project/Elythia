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
			require.Empty(t, ids)
		}
	}
}

func TestPluginActiveRolesUseAuthenticatedServerSnapshot(t *testing.T) {
	require.NotNil(t, serverIntegrationDB, "isolated PostgreSQL is required")
	db := serverIntegrationDB
	const viewer = "plugin-active-role-viewer"
	const token = "active-role-test"
	tokenValue := token
	roles := []string{"plugin-active-a", "plugin-active-expired", "plugin-active-z"}
	cleanup := func() {
		require.NoError(t, db.Where(`"userId" = ?`, viewer).Delete(&model.RoleAssignment{}).Error)
		require.NoError(t, db.Where("id IN ?", roles).Delete(&model.Role{}).Error)
		require.NoError(t, db.Where("id = ?", viewer).Delete(&model.User{}).Error)
	}
	cleanup()
	t.Cleanup(cleanup)
	users := repository.NewUserRepository(db)
	require.NoError(t, users.Create(&model.User{ID: viewer, Username: viewer, UsernameLower: viewer, Token: &tokenValue, IsBot: true}))
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
				require.Len(t, partial, 1)
				require.Equal(t, roles[0], partial[0].ID)
			}
			c := echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/", nil), httptest.NewRecorder())
			c.Set(string(middleware.UserContextKey), &model.User{ID: viewer})
			ids, lookupErr := (&pluginRequest{c: c, roleLister: s.roleService}).ActiveRoleIDs()
			if scenario.failedLookup {
				require.Error(t, lookupErr, "conditional lookup errors must not become partial success")
				require.Nil(t, ids)
			} else {
				require.NoError(t, lookupErr)
				require.Equal(t, []string{roles[0], roles[2]}, ids)
			}
			s.echo.Use(middleware.NewAuthMiddleware(users, repository.NewAccessTokenRepository(db)).Authenticate())
			def := pluginDef("active-role-test", func(_ plugin.Context, routes plugin.Router) error {
				handler := func(request plugin.Request) (any, error) {
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
				request := httptest.NewRequest(method, "/api/plugin/active-role-test/roles?role=forged&userId=forged", strings.NewReader(`{"roleIds":["forged"],"userId":"forged"}`))
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
					require.Contains(t, ids, roles[0])
					require.Contains(t, ids, roles[2])
					require.NotContains(t, ids, roles[1])
					require.NotContains(t, ids, "forged")
				}
			}
		})
	}
}
