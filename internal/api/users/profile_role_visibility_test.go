package users

import (
	"context"
	"errors"
	"testing"

	"github.com/elythia-network/elythia/internal/core/userpack"
	"github.com/elythia-network/elythia/internal/entity"
	"github.com/elythia-network/elythia/internal/model"
	"github.com/stretchr/testify/require"
)

type profileRoleVisibilityStub struct {
	hidden map[string]struct{}
	err    error
}

func (s profileRoleVisibilityStub) HiddenProfileRoleIDs(context.Context, string) (map[string]struct{}, error) {
	return s.hidden, s.err
}

func TestApplyProfileRoleVisibilityFiltersRolesAndBadges(t *testing.T) {
	badges := []any{
		map[string]any{"name": "hidden-role"},
		map[string]any{"name": "visible-role"},
	}
	detailed := entity.UserDetailed{
		UserLite: entity.UserLite{BadgeRoles: &badges},
		Roles: []any{
			map[string]any{"id": "hidden-id", "name": "hidden-role"},
			map[string]any{"id": "visible-id", "name": "visible-role"},
		},
	}
	h := &Handler{profileRoleVisibility: profileRoleVisibilityStub{hidden: map[string]struct{}{"hidden-id": {}}}}

	h.applyProfileRoleVisibility(context.Background(), "user-id", &detailed)

	if len(detailed.Roles) != 1 || detailed.Roles[0].(map[string]any)["id"] != "visible-id" {
		t.Fatalf("roles were not filtered: %#v", detailed.Roles)
	}
	if detailed.BadgeRoles == nil || len(*detailed.BadgeRoles) != 1 || (*detailed.BadgeRoles)[0].(map[string]any)["name"] != "visible-role" {
		t.Fatalf("badge roles were not filtered: %#v", detailed.BadgeRoles)
	}
}

func TestApplyProfileRoleVisibilityFailsClosed(t *testing.T) {
	badges := []any{map[string]any{"name": "private"}}
	detailed := entity.UserDetailed{
		UserLite: entity.UserLite{BadgeRoles: &badges},
		Roles:    []any{map[string]any{"id": "private-id", "name": "private"}},
	}
	h := &Handler{profileRoleVisibility: profileRoleVisibilityStub{err: errors.New("storage unavailable")}}

	h.applyProfileRoleVisibility(context.Background(), "user-id", &detailed)

	if len(detailed.Roles) != 0 {
		t.Fatalf("roles leaked after lookup error: %#v", detailed.Roles)
	}
	if detailed.BadgeRoles == nil || len(*detailed.BadgeRoles) != 0 {
		t.Fatalf("badges leaked after lookup error: %#v", detailed.BadgeRoles)
	}
}

func TestFillDetailedExtrasManyAppliesProfileRoleVisibility(t *testing.T) {
	h, _ := newTestHandler(t)
	h.profileRoleVisibility = profileRoleVisibilityStub{hidden: map[string]struct{}{"hidden-id": {}}}
	badges := []any{map[string]any{"name": "Hidden"}, map[string]any{"name": "Visible"}}
	detailed := &entity.UserDetailed{
		UserLite: entity.UserLite{ID: "user-id", BadgeRoles: &badges},
		Roles: []any{
			map[string]any{"id": "hidden-id", "name": "Hidden"},
			map[string]any{"id": "visible-id", "name": "Visible"},
		},
	}

	h.FillDetailedExtrasMany(context.Background(), nil, []userpack.DetailTarget{{User: &model.User{ID: "user-id"}, Detailed: detailed}})

	require.Len(t, detailed.Roles, 1)
	require.Equal(t, "visible-id", detailed.Roles[0].(map[string]any)["id"])
	require.NotNil(t, detailed.BadgeRoles)
	require.Len(t, *detailed.BadgeRoles, 1)
	require.Equal(t, "Visible", (*detailed.BadgeRoles)[0].(map[string]any)["name"])
}
