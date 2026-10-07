package server

import (
	"context"

	"github.com/elythia-network/elythia/plugin"
	"gorm.io/gorm"
)

// roleLevelProfileVisibilityReader is intentionally wired only when the
// bundled role-level plugin is enabled. The plugin owns the table; the host
// reads this one privacy decision so native users/show cannot expose a badge
// before the separate plugin profile request completes.
type roleLevelProfileVisibilityReader struct {
	db *gorm.DB
}

func (r roleLevelProfileVisibilityReader) HiddenProfileRoleIDs(ctx context.Context, userID string) (map[string]struct{}, error) {
	var roleIDs []string
	err := r.db.WithContext(ctx).
		Table("plugin_role_level.role_level_profile_visibility").
		Where("user_id = ? AND hidden IS TRUE", userID).
		Pluck("role_id", &roleIDs).Error
	if err != nil {
		return nil, err
	}
	out := make(map[string]struct{}, len(roleIDs))
	for _, roleID := range roleIDs {
		out[roleID] = struct{}{}
	}
	return out, nil
}

func roleLevelPluginEnabled(defs []plugin.Definition, settings map[string]map[string]any) bool {
	for _, def := range defs {
		if def.Name == "role-level" {
			return pluginEnabled(settings[def.Name])
		}
	}
	return false
}
