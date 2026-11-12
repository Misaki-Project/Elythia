// SPDX-FileCopyrightText: syuilo and misskey-project
// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import (
	"context"
	"errors"
	"strconv"
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
	one := newStore(db, testConfig())
	two := newStore(db, testConfig())
	current, err := one.ReadRateLimitSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	next := rateLimitSettings{
		Revision:             current.Revision,
		AuthenticatedPerHour: 1,
		AuthenticatedPerDay:  10,
		GlobalPerHour:        20,
		RoleLimits: map[string]coldFetchBudget{
			"role-a": {PerHour: 2, PerDay: 12},
		},
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

	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	if lease, err := two.AcquireFetchForRoles(context.Background(), "1234567890", "u1", []string{"role-a"}, now); err != nil || lease == nil {
		t.Fatalf("first role lease = %#v, %v", lease, err)
	}
	if lease, err := two.AcquireFetchForRoles(context.Background(), "2234567890", "u1", []string{"role-a"}, now); err != nil || lease == nil {
		t.Fatalf("second role lease = %#v, %v", lease, err)
	}
	if lease, err := two.AcquireFetchForRoles(context.Background(), "3234567890", "u1", []string{"role-a"}, now); !errors.Is(err, errRateLimited) || lease != nil {
		t.Fatalf("updated role limit was not enforced: %#v, %v", lease, err)
	}
}

func TestGlobalRateLimitUpdatePreservesLegacyDefaultsAndRoleExport(t *testing.T) {
	db := migratedDB(t)
	s := newStore(db, testConfig())
	current, err := s.ReadRateLimitSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	legacy := current
	legacy.AuthenticatedPerHour = 237
	legacy.AuthenticatedPerDay = 1700
	legacy.GlobalPerHour = 300
	legacy.RoleLimits = map[string]coldFetchBudget{"role-a": {PerHour: 800, PerDay: 5000}}
	if _, err := s.UpdateRateLimitSettings(context.Background(), "admin", legacy); err != nil {
		t.Fatal(err)
	}
	global, err := s.ReadGlobalRateLimit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	updated, err := s.UpdateGlobalRateLimit(context.Background(), "admin", global.Revision, 480)
	if err != nil {
		t.Fatal(err)
	}
	if updated.GlobalPerHour != 480 || updated.Revision != global.Revision+1 {
		t.Fatalf("global result = %#v", updated)
	}
	if _, err := s.UpdateGlobalRateLimit(context.Background(), "admin", global.Revision, 600); !errors.Is(err, errSettingsConflict) {
		t.Fatalf("stale global update error = %v", err)
	}
	legacyAfter, err := s.ReadRateLimitSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if legacyAfter.AuthenticatedPerHour != 237 || legacyAfter.AuthenticatedPerDay != 1700 || legacyAfter.GlobalPerHour != 480 || legacyAfter.RoleLimits["role-a"] != (coldFetchBudget{PerHour: 800, PerDay: 5000}) {
		t.Fatalf("global write changed migration values: %#v", legacyAfter)
	}
}

func TestAdminRateLimitSettingsRequireAdministrator(t *testing.T) {
	db := migratedDB(t)
	h := plugintest.New(t).WithName("fxtwitter-preview").WithDB(db).WithConfig(map[string]any{})
	routes := h.Routes(Plugin)
	if _, err := routes.Call(t, "POST /admin/rate-limit-settings", plugintest.Request{UserID: "mod", Moderator: true, Body: `{}`}); err == nil {
		t.Fatal("moderator read administrator-only rate limit settings")
	}
	result, err := routes.Call(t, "POST /admin/rate-limit-settings", plugintest.Request{UserID: "admin", Administrator: true, Body: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	current := result.(rateLimitSettingsResponse)
	if current.AuthenticatedPerHour < 1 || current.AuthenticatedPerDay < 1 || current.GlobalPerHour < 1 {
		t.Fatalf("legacy settings read omitted migration values: %#v", current)
	}
	global, err := routes.Call(t, "POST /admin/global-rate-limit", plugintest.Request{UserID: "admin", Administrator: true, Body: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	globalValue := global.(globalRateLimitResponse)
	globalBody := `{"revision":` + strconv.FormatInt(globalValue.Revision, 10) + `,"globalPerHour":40}`
	if _, err := routes.Call(t, "POST /admin/global-rate-limit/update", plugintest.Request{UserID: "mod", Moderator: true, Body: globalBody}); err == nil {
		t.Fatal("moderator changed administrator-only global limit")
	}
	if _, err := routes.Call(t, "POST /admin/global-rate-limit/update", plugintest.Request{UserID: "admin", Administrator: true, Body: globalBody}); err != nil {
		t.Fatal(err)
	}
}
