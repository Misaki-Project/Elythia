// SPDX-FileCopyrightText: syuilo and misskey-project
// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var errSettingsConflict = errors.New("rate limit settings were changed by another administrator")

type rateLimitSettings struct {
	Revision             int64
	AuthenticatedPerHour int
	AuthenticatedPerDay  int
	GlobalPerHour        int
	RoleLimits           map[string]coldFetchBudget
}

type rateLimitSettingsResponse struct {
	Revision      int64     `json:"revision"`
	GlobalPerHour int       `json:"globalPerHour"`
	UpdatedAt     time.Time `json:"updatedAt"`
	UpdatedBy     string    `json:"updatedBy,omitempty"`
}

func validateRateLimitSettings(value rateLimitSettings) error {
	if value.GlobalPerHour < 1 || value.GlobalPerHour > 100000 {
		return errors.New("globalPerHour must be between 1 and 100000")
	}
	if len(value.RoleLimits) != 0 {
		return errors.New("role policies are managed through admin/roles")
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
	return rateLimitSettingsResponse{
		Revision: value.Revision, GlobalPerHour: value.GlobalPerHour,
		UpdatedAt: updatedAt, UpdatedBy: updatedBy,
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
			global_per_hour = $1, revision = revision + 1,
			updated_at = now(), updated_by = $2
		WHERE singleton = true AND revision = $3
		RETURNING revision, updated_at
	`, value.GlobalPerHour, actorID, value.Revision).Scan(&revision, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return rateLimitSettingsResponse{}, errSettingsConflict
	}
	if err != nil {
		return rateLimitSettingsResponse{}, err
	}
	value.Revision = revision
	if err := tx.Commit(); err != nil {
		return rateLimitSettingsResponse{}, err
	}
	return rateLimitResponse(value, updatedAt, actorID), nil
}

func effectiveBudgetFromSettings(value rateLimitSettings, roleIDs []string) coldFetchBudget {
	// Role policies are resolved by the host's checked admin/roles policy path.
	// Stored role_rate_limit rows remain readable for migration compatibility but
	// never override the host-resolved policy on an execution path.
	_ = roleIDs
	return coldFetchBudget{PerHour: value.AuthenticatedPerHour, PerDay: value.AuthenticatedPerDay}
}
