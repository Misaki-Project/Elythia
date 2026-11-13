package server

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/elythia-network/elythia/internal/model"
	"github.com/elythia-network/elythia/internal/server/middleware"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
)

type checkedPolicyResolverStub struct {
	policies map[string]any
	err      error
	userID   string
	keys     []string
}

func (s *checkedPolicyResolverStub) GetUserPoliciesCheckedForKeys(userID string, keys ...string) (map[string]any, error) {
	s.userID = userID
	s.keys = append([]string(nil), keys...)
	return s.policies, s.err
}

func TestPluginPolicyAdaptersReturnOnlyRequestedKeys(t *testing.T) {
	all := map[string]any{
		"canUseFxTwitterPreview":      true,
		"fxTwitterColdFetchesPerHour": 100,
		"unrequestedNativePolicy":     "private",
	}
	resolver := &checkedPolicyResolverStub{policies: all}
	requestContext := echo.New().NewContext(httptest.NewRequest("POST", "/", nil), httptest.NewRecorder())
	requestContext.Set(string(middleware.UserContextKey), &model.User{ID: "viewer"})

	gotRequest, err := (&pluginRequest{c: requestContext, policyResolver: resolver}).CheckedRolePolicies(
		"canUseFxTwitterPreview", "missingPolicy")
	require.NoError(t, err)
	require.Equal(t, map[string]any{"canUseFxTwitterPreview": true}, gotRequest)
	require.Equal(t, "viewer", resolver.userID)
	require.Equal(t, []string{"canUseFxTwitterPreview", "missingPolicy"}, resolver.keys)

	resolver.userID, resolver.keys = "", nil
	gotContext, err := (&pluginContext{policyResolver: resolver}).CheckedUserRolePolicies(
		"queued-actor", "fxTwitterColdFetchesPerHour")
	require.NoError(t, err)
	require.Equal(t, map[string]any{"fxTwitterColdFetchesPerHour": 100}, gotContext)
	require.Equal(t, "queued-actor", resolver.userID)
	require.Equal(t, []string{"fxTwitterColdFetchesPerHour"}, resolver.keys)

	require.Equal(t, "private", all["unrequestedNativePolicy"], "projection must not mutate resolver output")
}

func TestPluginPolicyAdaptersPropagateResolverErrors(t *testing.T) {
	lookupErr := errors.New("role policy lookup failed")
	resolver := &checkedPolicyResolverStub{err: lookupErr}
	requestContext := echo.New().NewContext(httptest.NewRequest("POST", "/", nil), httptest.NewRecorder())
	requestContext.Set(string(middleware.UserContextKey), &model.User{ID: "viewer"})

	_, err := (&pluginRequest{c: requestContext, policyResolver: resolver}).CheckedRolePolicies("canUseFxTwitterPreview")
	require.ErrorIs(t, err, lookupErr)
	_, err = (&pluginContext{policyResolver: resolver}).CheckedUserRolePolicies("queued-actor", "canUseFxTwitterPreview")
	require.ErrorIs(t, err, lookupErr)
}
