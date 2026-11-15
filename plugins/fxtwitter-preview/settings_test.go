// SPDX-FileCopyrightText: syuilo and misskey-project
// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/elythia-network/elythia/plugin/plugintest"
)

func TestRateLimitSettingsSeedOnceAndRemainDatabaseCanonical(t *testing.T) {
	db := migratedDB(t)
	first := testConfig()
	first.AuthenticatedBudget = 7
	first.AuthenticatedDailyBudget = 70
	first.GlobalBudget = 17
	first.RoleBudgets = map[string]coldFetchBudget{"role-a": {PerHour: 9, PerDay: 90}}

	s := newStore(db, first)
	got, err := s.ReadRateLimitSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.AuthenticatedPerHour != 7 || got.AuthenticatedPerDay != 70 || got.GlobalPerHour != 17 || got.RoleLimits["role-a"].PerHour != 9 {
		t.Fatalf("seeded settings = %#v", got)
	}

	changedYAML := first
	changedYAML.AuthenticatedBudget = 999
	changedYAML.GlobalBudget = 999
	changedYAML.RoleBudgets = map[string]coldFetchBudget{"role-b": {PerHour: 999, PerDay: 999}}
	got, err = newStore(db, changedYAML).ReadRateLimitSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.AuthenticatedPerHour != 7 || got.GlobalPerHour != 17 || len(got.RoleLimits) != 1 || got.RoleLimits["role-a"].PerHour != 9 {
		t.Fatalf("YAML replaced canonical DB settings: %#v", got)
	}
}

func TestRateLimitSettingsUpdateUsesRevisionAndAffectsAllStores(t *testing.T) {
	db := migratedDB(t)
	cfg := testConfig()
	cfg.RoleBudgets = map[string]coldFetchBudget{"role-a": {PerHour: 9, PerDay: 90}}
	one := newStore(db, cfg)
	two := newStore(db, cfg)
	current, err := one.ReadRateLimitSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	next := rateLimitSettings{
		Revision:             current.Revision,
		AuthenticatedPerHour: 1,
		AuthenticatedPerDay:  10,
		GlobalPerHour:        20,
	}
	updated, err := one.UpdateRateLimitSettings(context.Background(), "admin", next)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != current.Revision+1 {
		t.Fatalf("revision = %d", updated.Revision)
	}
	if _, err := one.UpdateRateLimitSettings(context.Background(), "admin", next); !errors.Is(err, errSettingsConflict) {
		t.Fatalf("stale update error = %v", err)
	}

	stored, err := two.ReadRateLimitSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if limits, ok := stored.RoleLimits["role-a"]; !ok || limits.PerHour != 9 || limits.PerDay != 90 {
		t.Fatalf("global settings update changed legacy role rows: %#v", stored.RoleLimits)
	}
	if stored.GlobalPerHour != 20 {
		t.Fatalf("global safety cap = %d, want 20", stored.GlobalPerHour)
	}
	defaults := testConfig()
	if stored.AuthenticatedPerHour != defaults.AuthenticatedBudget || stored.AuthenticatedPerDay != defaults.AuthenticatedDailyBudget {
		t.Fatalf("global settings update changed legacy per-user values: %#v", stored)
	}
}

func TestCheckedAdminRoleBudgetOverridesLegacyPluginRoleRows(t *testing.T) {
	db := migratedDB(t)
	if _, err := db.Exec(`INSERT INTO role_rate_limit (role_id, per_hour, per_day) VALUES ('legacy-role', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	s := newStore(db, testConfig())
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	limits := coldFetchBudget{PerHour: 2, PerDay: 2}
	for _, postID := range []string{"1234567890", "2234567890"} {
		if lease, err := s.AcquireFetchWithBudget(context.Background(), postID, "u1", limits, now); err != nil || lease == nil {
			t.Fatalf("checked admin/roles budget did not override legacy row for %s: lease=%#v err=%v", postID, lease, err)
		}
	}
	if lease, err := s.AcquireFetchWithBudget(context.Background(), "3234567890", "u1", limits, now); !errors.Is(err, errRateLimited) || lease != nil {
		t.Fatalf("checked admin/roles budget was not enforced: lease=%#v err=%v", lease, err)
	}
}

func TestAdminRateLimitSettingsRequireAdministrator(t *testing.T) {
	db := migratedDB(t)
	if _, err := db.Exec(`INSERT INTO role_rate_limit (role_id, per_hour, per_day) VALUES ('legacy-role', 7, 70)`); err != nil {
		t.Fatal(err)
	}
	h := plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).WithConfig(map[string]any{})
	routes := h.Routes(Plugin)
	if _, err := routes.Call(t, "POST /admin/rate-limit-settings", plugintest.Request{UserID: "mod", Moderator: true, Body: `{}`}); err == nil {
		t.Fatal("moderator read administrator-only rate limit settings")
	}
	result, err := routes.Call(t, "POST /admin/rate-limit-settings", plugintest.Request{UserID: "admin", Administrator: true, Body: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(mustJSON(t, result), "roleLimits") {
		t.Fatal("rate-limit settings response still exposes plugin-owned role policy editing data")
	}
	current := result.(rateLimitSettingsResponse)
	initialSettings, err := newStore(db, testConfig()).ReadRateLimitSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	body := `{"revision":` + strconv.FormatInt(current.Revision, 10) + `,"globalPerHour":40}`
	if _, err := routes.Call(t, "POST /admin/rate-limit-settings/update", plugintest.Request{UserID: "mod", Moderator: true, Body: body}); err == nil {
		t.Fatal("moderator changed administrator-only rate limit settings")
	}
	if _, err := routes.Call(t, "POST /admin/rate-limit-settings/update", plugintest.Request{UserID: "admin", Administrator: true, Body: body}); err != nil {
		t.Fatal(err)
	}
	legacyRoleBody := `{"revision":` + strconv.FormatInt(current.Revision+1, 10) + `,"authenticatedPerHour":3,"authenticatedPerDay":30,"globalPerHour":40,"roleLimits":[{"roleId":"role-a","perHour":5,"perDay":50}]}`
	if _, err := routes.Call(t, "POST /admin/rate-limit-settings/update", plugintest.Request{UserID: "admin", Administrator: true, Body: legacyRoleBody}); err == nil {
		t.Fatal("legacy plugin endpoint still accepts per-user or role policy changes")
	}
	var perHour, perDay int
	if err := db.QueryRow(`SELECT per_hour, per_day FROM role_rate_limit WHERE role_id = 'legacy-role'`).Scan(&perHour, &perDay); err != nil {
		t.Fatal(err)
	}
	if perHour != 7 || perDay != 70 {
		t.Fatalf("rejected plugin role update changed preserved legacy row: %d/%d", perHour, perDay)
	}
	stored, err := newStore(db, testConfig()).ReadRateLimitSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := stored.RoleLimits["legacy-role"]; got.PerHour != 7 || got.PerDay != 70 {
		t.Fatalf("legacy role data was not preserved: %#v", stored.RoleLimits)
	}
	if stored.AuthenticatedPerHour != initialSettings.AuthenticatedPerHour || stored.AuthenticatedPerDay != initialSettings.AuthenticatedPerDay {
		t.Fatalf("rejected legacy per-user update changed stored values: %#v", stored)
	}
}
