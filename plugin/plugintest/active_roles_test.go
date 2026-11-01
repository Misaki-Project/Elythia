package plugintest

import (
	"errors"
	"testing"

	"github.com/elythia-network/elythia/plugin"
	"github.com/stretchr/testify/require"
)

func TestRequestProvidesOptionalActiveRoles(t *testing.T) {
	r := Request{UserID: "viewer"}.build()
	roles, ok := r.(interface{ ActiveRoleIDs() ([]string, error) })
	require.True(t, ok, "host requests must implement the optional active-role extension")
	ids, err := roles.ActiveRoleIDs()
	require.NoError(t, err)
	require.Empty(t, ids)
}

func TestRequestActiveRolesSnapshotAndFailure(t *testing.T) {
	input := []string{"z", "a", "z", ""}
	r := Request{UserID: "viewer", ActiveRoleIDs: input}.build().(plugin.ActiveRoleRequest)
	input[0] = "forged"
	first, err := r.ActiveRoleIDs()
	require.NoError(t, err)
	require.Equal(t, []string{"a", "z"}, first)
	first[0] = "changed"
	second, err := r.ActiveRoleIDs()
	require.NoError(t, err)
	require.Equal(t, []string{"a", "z"}, second)
	lookupErr := errors.New("role lookup failed")
	failing := Request{UserID: "viewer", ActiveRoleIDs: input, ActiveRoleError: lookupErr}.build().(plugin.ActiveRoleRequest)
	ids, err := failing.ActiveRoleIDs()
	require.ErrorIs(t, err, lookupErr)
	require.Nil(t, ids)
	anonymous := Request{ActiveRoleIDs: input, ActiveRoleError: lookupErr}.build().(plugin.ActiveRoleRequest)
	ids, err = anonymous.ActiveRoleIDs()
	require.NoError(t, err)
	require.Empty(t, ids)
}
