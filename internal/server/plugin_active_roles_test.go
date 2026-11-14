package server

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/elythia-network/elythia/internal/model"
	"github.com/elythia-network/elythia/internal/server/middleware"
	"github.com/elythia-network/elythia/plugin"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
)

type activeRolesStub struct {
	roles  []*model.Role
	err    error
	userID string
}

func (s *activeRolesStub) GetUserRolesStrict(userID string) ([]*model.Role, error) {
	s.userID = userID
	return s.roles, s.err
}

func TestPluginRequestActiveRoleIDsRequiresIdentityAndRoleLookup(t *testing.T) {
	e := echo.New()
	request := e.NewContext(httptest.NewRequest("POST", "/", nil), httptest.NewRecorder())
	ids, err := (&pluginRequest{c: request}).ActiveRoleIDs()
	require.NoError(t, err)
	require.Empty(t, ids)

	request.Set(string(middleware.UserContextKey), &model.User{ID: "viewer"})
	_, err = (&pluginRequest{c: request}).ActiveRoleIDs()
	require.Error(t, err)
}

func TestPluginRequestActiveRoleIDsAreStrictSortedAndUnique(t *testing.T) {
	e := echo.New()
	request := e.NewContext(httptest.NewRequest("POST", "/", nil), httptest.NewRecorder())
	request.Set(string(middleware.UserContextKey), &model.User{ID: "viewer"})
	lookup := &activeRolesStub{roles: []*model.Role{
		{ID: "role-b"}, nil, {ID: ""}, {ID: "role-a"}, {ID: "role-b"},
	}}

	ids, err := (&pluginRequest{c: request, roleLister: lookup}).ActiveRoleIDs()
	require.NoError(t, err)
	require.Equal(t, "viewer", lookup.userID)
	require.Equal(t, []string{"role-a", "role-b"}, ids)
}

func TestPluginRequestActiveRoleIDsPropagatesLookupFailure(t *testing.T) {
	e := echo.New()
	request := e.NewContext(httptest.NewRequest("POST", "/", nil), httptest.NewRecorder())
	request.Set(string(middleware.UserContextKey), &model.User{ID: "viewer"})
	want := errors.New("role store unavailable")
	_, err := (&pluginRequest{c: request, roleLister: &activeRolesStub{err: want}}).ActiveRoleIDs()
	require.ErrorIs(t, err, want)
}

func TestWrapPluginHandlerCarriesStrictRoleLookup(t *testing.T) {
	lookup := &activeRolesStub{roles: []*model.Role{{ID: "role-1"}}}
	e := echo.New()
	request := e.NewContext(httptest.NewRequest("POST", "/roles", nil), httptest.NewRecorder())
	request.Set(string(middleware.UserContextKey), &model.User{ID: "viewer"})
	handler := wrapPluginHandlerWithRoles(func(req plugin.Request) (any, error) {
		return req.(plugin.ActiveRoleRequest).ActiveRoleIDs()
	}, nil, lookup, nil)

	require.NoError(t, handler(request))
	require.Equal(t, "viewer", lookup.userID)
	require.JSONEq(t, `["role-1"]`, request.Response().Writer.(*httptest.ResponseRecorder).Body.String())
}
