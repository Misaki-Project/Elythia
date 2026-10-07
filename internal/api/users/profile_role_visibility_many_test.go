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

func TestFillDetailedExtrasManyKeepsProfileRoleVisibility(t *testing.T) {
	for _, storageFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "hidden roles", true: "storage failure"}[storageFailure], func(t *testing.T) {
			h, _ := newTestHandler(t)
			provider := stubProfileRoleVisibility{hidden: map[string]struct{}{"hidden": {}}}
			if storageFailure {
				provider.err = errors.New("storage unavailable")
			}
			h.profileRoleVisibility = provider
			var targets []userpack.DetailTarget
			for _, id := range []string{"u1", "u2"} {
				badges := []any{map[string]any{"name": "Hidden"}, map[string]any{"name": "Visible"}}
				detailed := &entity.UserDetailed{
					UserLite: entity.UserLite{ID: id, BadgeRoles: &badges},
					Roles: []any{
						map[string]any{"id": "hidden", "name": "Hidden"},
						map[string]any{"id": "visible", "name": "Visible"},
					},
				}
				targets = append(targets, userpack.DetailTarget{User: &model.User{ID: id}, Detailed: detailed})
			}
			h.FillDetailedExtrasMany(context.Background(), nil, targets)
			for _, target := range targets {
				if storageFailure {
					require.Empty(t, target.Detailed.Roles)
					require.Empty(t, *target.Detailed.BadgeRoles)
				} else {
					require.Len(t, target.Detailed.Roles, 1)
					require.Equal(t, "visible", target.Detailed.Roles[0].(map[string]any)["id"])
					require.Len(t, *target.Detailed.BadgeRoles, 1)
					require.Equal(t, "Visible", (*target.Detailed.BadgeRoles)[0].(map[string]any)["name"])
				}
			}
		})
	}
}
