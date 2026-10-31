// SPDX-FileCopyrightText: syuilo and misskey-project
// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import (
	"strings"
	"testing"
	"time"

	"github.com/elythia-network/elythia/plugin"
	"github.com/elythia-network/elythia/plugin/plugintest"
)

func TestPluginDefinition(t *testing.T) {
	if Plugin.Name != "fxtwitter-preview" {
		t.Fatalf("name = %q", Plugin.Name)
	}
	if Plugin.APIVersion != plugin.APIVersion {
		t.Fatalf("api version = %d", Plugin.APIVersion)
	}
	if err := Plugin.Validate(); err != nil {
		t.Fatal(err)
	}
	if Plugin.Routes == nil || Plugin.Jobs == nil || len(Plugin.Migrations) == 0 {
		t.Fatal("routes, jobs, and migrations are required")
	}
}

func TestMigrationsSeparatePublicAndAuditData(t *testing.T) {
	var sql strings.Builder
	for _, migration := range Plugin.Migrations {
		sql.WriteString(migration.SQL)
	}
	got := strings.ToLower(sql.String())
	for _, table := range []string{"public_post_cache", "audit_body_snapshot", "audit_delete_log", "cold_fetch_budget", "x_user", "x_post", "rate_limit_settings", "role_rate_limit"} {
		if !strings.Contains(got, "create table "+table) {
			t.Fatalf("migration does not create %s", table)
		}
	}
	if len(Plugin.Migrations) < 5 || Plugin.Migrations[len(Plugin.Migrations)-1].Version < 5 {
		t.Fatalf("runtime settings migration missing: %#v", Plugin.Migrations)
	}
	for _, fragment := range []string{"foreign key (author_id) references x_user", "quote_post_id", "author_snapshot"} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("normalized X migration missing %q", fragment)
		}
	}
	publicStart := strings.Index(got, "create table public_post_cache")
	publicEnd := strings.Index(got[publicStart:], ");") + publicStart
	if publicStart < 0 || publicEnd < publicStart {
		t.Fatal("public cache DDL missing")
	}
	publicDDL := got[publicStart:publicEnd]
	for _, forbidden := range []string{"body text", "audit", "snapshot"} {
		if strings.Contains(publicDDL, forbidden) {
			t.Fatalf("public cache contains audit field %q", forbidden)
		}
	}
}

func TestLoadConfigDefaultsAndValidation(t *testing.T) {
	ctx := plugintest.New(t).WithConfig(nil).Context()
	cfg, err := loadConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ExternalFetchEnabled {
		t.Fatal("external fetch must default off")
	}
	if cfg.RefetchInterval != time.Hour || cfg.AuditRetention != 0 {
		t.Fatalf("unexpected durations: refetch=%s retention=%s", cfg.RefetchInterval, cfg.AuditRetention)
	}
	if cfg.VideoPlaybackMode != "officialEmbed" {
		t.Fatalf("video mode = %q", cfg.VideoPlaybackMode)
	}
	if cfg.StaticImageMaxBytes != 10<<20 || cfg.AnonymousBudget != 60 {
		t.Fatalf("reserved media/anonymous defaults = %d, %d", cfg.StaticImageMaxBytes, cfg.AnonymousBudget)
	}
	if cfg.AuthenticatedBudget != 100 || cfg.AuthenticatedDailyBudget != 600 || cfg.GlobalBudget != 300 {
		t.Fatalf("cold fetch defaults = hourly:%d daily:%d global:%d", cfg.AuthenticatedBudget, cfg.AuthenticatedDailyBudget, cfg.GlobalBudget)
	}

	bad := plugintest.New(t).WithConfig(map[string]any{"refetchIntervalMinutes": 0}).Context()
	if _, err := loadConfig(bad); err == nil {
		t.Fatal("zero refetch interval accepted")
	}
}

func TestRoleColdFetchLimitsUseMaximumEntitlementPerDimension(t *testing.T) {
	ctx := plugintest.New(t).WithConfig(map[string]any{
		"roleColdFetchLimits": map[string]any{
			"role-a": map[string]any{"perHour": 250, "perDay": 700},
			"role-b": map[string]any{"perHour": 150, "perDay": 1200},
		},
	}).Context()
	cfg, err := loadConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.effectiveBudget([]string{"role-a", "role-b", "unconfigured"})
	if got.PerHour != 250 || got.PerDay != 1200 {
		t.Fatalf("effective budget = %#v", got)
	}
}
