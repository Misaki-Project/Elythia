// SPDX-FileCopyrightText: syuilo and misskey-project
// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"time"
)

var (
	errSettingsConflict = errors.New("rate limit settings were changed by another administrator")
	roleIDPattern       = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

type rateLimitSettings struct {
	Revision             int64
	AuthenticatedPerHour int
	AuthenticatedPerDay  int
	GlobalPerHour        int
	RoleLimits           map[string]coldFetchBudget
}

type roleRateLimitResponse struct {
	RoleID  string `json:"roleId"`
	PerHour int    `json:"perHour"`
	PerDay  int    `json:"perDay"`
}

type rateLimitSettingsResponse struct {
	Revision             int64                   `json:"revision"`
	AuthenticatedPerHour int                     `json:"authenticatedPerHour"`
	AuthenticatedPerDay  int                     `json:"authenticatedPerDay"`
	GlobalPerHour        int                     `json:"globalPerHour"`
	RoleLimits           []roleRateLimitResponse `json:"roleLimits"`
	UpdatedAt            time.Time               `json:"updatedAt"`
	UpdatedBy            string                  `json:"updatedBy,omitempty"`
}

func validateRateLimitSettings(value rateLimitSettings) error {
	if value.AuthenticatedPerHour < 1 || value.AuthenticatedPerHour > 10000 {
		return errors.New("authenticatedPerHour must be between 1 and 10000")
	}
	if value.AuthenticatedPerDay < 1 || value.AuthenticatedPerDay > 100000 {
		return errors.New("authenticatedPerDay must be between 1 and 100000")
	}
	if value.GlobalPerHour < 1 || value.GlobalPerHour > 100000 {
		return errors.New("globalPerHour must be between 1 and 100000")
	}
	for roleID, limits := range value.RoleLimits {
		if !roleIDPattern.MatchString(roleID) {
			return fmt.Errorf("invalid role ID %q", roleID)
		}
		if limits.PerHour < 1 || limits.PerHour > 10000 || limits.PerDay < 1 || limits.PerDay > 100000 {
			return fmt.Errorf("role %q limits are outside the allowed range", roleID)
		}
	}
	return nil
}

func (s *store) ensureRateLimitSettingsTx(ctx context.Context, tx *sql.Tx) error {
	result, err := tx.ExecContext(ctx, `
		INSERT INTO rate_limit_settings
			(singleton, authenticated_per_hour, authenticated_per_day, global_per_hour)
		VALUES (true, $1, $2, $3)
		ON CONFLICT (singleton) DO NOTHING
	`, s.cfg.AuthenticatedBudget, s.cfg.AuthenticatedDailyBudget, s.cfg.GlobalBudget)
	if err != nil {
		return err
	}
	inserted, err := result.RowsAffected()
	if err != nil || inserted == 0 {
		return err
	}
	for roleID, limits := range s.cfg.RoleBudgets {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO role_rate_limit (role_id, per_hour, per_day)
			VALUES ($1, $2, $3)
		`, roleID, limits.PerHour, limits.PerDay); err != nil {
			return err
		}
	}
	return nil
}

func readRateLimitSettingsTx(ctx context.Context, tx *sql.Tx) (rateLimitSettings, time.Time, string, error) {
	value := rateLimitSettings{RoleLimits: map[string]coldFetchBudget{}}
	var updatedAt time.Time
	var updatedBy sql.NullString
	err := tx.QueryRowContext(ctx, `
		SELECT revision, authenticated_per_hour, authenticated_per_day,
		       global_per_hour, updated_at, updated_by
		FROM rate_limit_settings WHERE singleton = true
	`).Scan(&value.Revision, &value.AuthenticatedPerHour, &value.AuthenticatedPerDay,
		&value.GlobalPerHour, &updatedAt, &updatedBy)
	if err != nil {
		return rateLimitSettings{}, time.Time{}, "", err
	}
	rows, err := tx.QueryContext(ctx, `SELECT role_id, per_hour, per_day FROM role_rate_limit ORDER BY role_id`)
	if err != nil {
		return rateLimitSettings{}, time.Time{}, "", err
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var roleID string
		var limits coldFetchBudget
		if err := rows.Scan(&roleID, &limits.PerHour, &limits.PerDay); err != nil {
			return rateLimitSettings{}, time.Time{}, "", err
		}
		value.RoleLimits[roleID] = limits
	}
	return value, updatedAt, updatedBy.String, rows.Err()
}

func (s *store) ReadRateLimitSettings(ctx context.Context) (rateLimitSettings, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return rateLimitSettings{}, err
	}
	defer tx.Rollback() //nolint:errcheck
	if err := s.ensureRateLimitSettingsTx(ctx, tx); err != nil {
		return rateLimitSettings{}, err
	}
	value, _, _, err := readRateLimitSettingsTx(ctx, tx)
	if err != nil {
		return rateLimitSettings{}, err
	}
	if err := tx.Commit(); err != nil {
		return rateLimitSettings{}, err
	}
	return value, nil
}

func (s *store) ReadRateLimitSettingsResponse(ctx context.Context) (rateLimitSettingsResponse, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return rateLimitSettingsResponse{}, err
	}
	defer tx.Rollback() //nolint:errcheck
	if err := s.ensureRateLimitSettingsTx(ctx, tx); err != nil {
		return rateLimitSettingsResponse{}, err
	}
	value, updatedAt, updatedBy, err := readRateLimitSettingsTx(ctx, tx)
	if err != nil {
		return rateLimitSettingsResponse{}, err
	}
	if err := tx.Commit(); err != nil {
		return rateLimitSettingsResponse{}, err
	}
	return rateLimitResponse(value, updatedAt, updatedBy), nil
}

func rateLimitResponse(value rateLimitSettings, updatedAt time.Time, updatedBy string) rateLimitSettingsResponse {
	roleIDs := make([]string, 0, len(value.RoleLimits))
	for roleID := range value.RoleLimits {
		roleIDs = append(roleIDs, roleID)
	}
	sort.Strings(roleIDs)
	roles := make([]roleRateLimitResponse, 0, len(roleIDs))
	for _, roleID := range roleIDs {
		limits := value.RoleLimits[roleID]
		roles = append(roles, roleRateLimitResponse{RoleID: roleID, PerHour: limits.PerHour, PerDay: limits.PerDay})
	}
	return rateLimitSettingsResponse{
		Revision: value.Revision, AuthenticatedPerHour: value.AuthenticatedPerHour,
		AuthenticatedPerDay: value.AuthenticatedPerDay, GlobalPerHour: value.GlobalPerHour,
		RoleLimits: roles, UpdatedAt: updatedAt, UpdatedBy: updatedBy,
	}
}

func (s *store) UpdateRateLimitSettings(ctx context.Context, actorID string, value rateLimitSettings) (rateLimitSettingsResponse, error) {
	if actorID == "" {
		return rateLimitSettingsResponse{}, errors.New("administrator ID is required")
	}
	if err := validateRateLimitSettings(value); err != nil {
		return rateLimitSettingsResponse{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return rateLimitSettingsResponse{}, err
	}
	defer tx.Rollback() //nolint:errcheck
	if err := s.ensureRateLimitSettingsTx(ctx, tx); err != nil {
		return rateLimitSettingsResponse{}, err
	}
	var revision int64
	var updatedAt time.Time
	err = tx.QueryRowContext(ctx, `
		UPDATE rate_limit_settings SET
			authenticated_per_hour = $1, authenticated_per_day = $2,
			global_per_hour = $3, revision = revision + 1,
			updated_at = now(), updated_by = $4
		WHERE singleton = true AND revision = $5
		RETURNING revision, updated_at
	`, value.AuthenticatedPerHour, value.AuthenticatedPerDay, value.GlobalPerHour, actorID, value.Revision).Scan(&revision, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return rateLimitSettingsResponse{}, errSettingsConflict
	}
	if err != nil {
		return rateLimitSettingsResponse{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM role_rate_limit`); err != nil {
		return rateLimitSettingsResponse{}, err
	}
	for roleID, limits := range value.RoleLimits {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO role_rate_limit (role_id, per_hour, per_day, updated_at, updated_by)
			VALUES ($1, $2, $3, $4, $5)
		`, roleID, limits.PerHour, limits.PerDay, updatedAt, actorID); err != nil {
			return rateLimitSettingsResponse{}, err
		}
	}
	value.Revision = revision
	if err := tx.Commit(); err != nil {
		return rateLimitSettingsResponse{}, err
	}
	return rateLimitResponse(value, updatedAt, actorID), nil
}

func effectiveBudgetFromSettings(value rateLimitSettings, roleIDs []string) coldFetchBudget {
	result := coldFetchBudget{PerHour: value.AuthenticatedPerHour, PerDay: value.AuthenticatedPerDay}
	for _, roleID := range roleIDs {
		limits, ok := value.RoleLimits[roleID]
		if !ok {
			continue
		}
		if limits.PerHour > result.PerHour {
			result.PerHour = limits.PerHour
		}
		if limits.PerDay > result.PerDay {
			result.PerDay = limits.PerDay
		}
	}
	return result
}
