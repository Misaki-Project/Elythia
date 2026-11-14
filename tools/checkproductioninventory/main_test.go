package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testRevision = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func loadBaseline(t *testing.T) inventory {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "release", "production-migration-baseline.json"))
	if err != nil {
		t.Fatal(err)
	}
	value, err := decodeInventory(data)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func loadAdditions(t *testing.T) migrationAdditions {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "release", "approved-production-migration-additions.json"))
	if err != nil {
		t.Fatal(err)
	}
	value, err := decodeAdditions(data)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func withCandidateRevision(base inventory) inventory {
	value := base
	value.SourceRevision = testRevision
	value.CoreMigrations = append([]coreMigration(nil), base.CoreMigrations...)
	value.LocalMigrations = append([]coreMigration(nil), base.LocalMigrations...)
	value.Plugins = make(map[string]pluginEntry, len(base.Plugins))
	for name, entry := range base.Plugins {
		entry.Migrations = append([]migration(nil), entry.Migrations...)
		if name == "role-level" || name == "fxtwitter-preview" || name == "tracking-parameter-cleaner" {
			entry.SourceRef = "local@" + testRevision
		}
		value.Plugins[name] = entry
	}
	return value
}

func TestCompareAcceptsOnlySourceRevisionChange(t *testing.T) {
	baseline := loadBaseline(t)
	if err := compare(withCandidateRevision(baseline), baseline, loadAdditions(t), testRevision); err != nil {
		t.Fatalf("expected unchanged production migration inventory to pass: %v", err)
	}
	cleaner := baseline.Plugins["tracking-parameter-cleaner"]
	if cleaner.Version != "1.0.0" || len(cleaner.Migrations) != 0 {
		t.Fatalf("tracking cleaner must remain a metadata-only, migration-free registration: %+v", cleaner)
	}
	actual := withCandidateRevision(baseline)
	cleaner = actual.Plugins["tracking-parameter-cleaner"]
	cleaner.Migrations = []migration{{Version: 1, SQLHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}
	actual.Plugins["tracking-parameter-cleaner"] = cleaner
	if err := compare(actual, baseline, loadAdditions(t), testRevision); err == nil {
		t.Fatal("a migration was unexpectedly added to the metadata-only cleaner plugin")
	}
}

func TestCompareRejectsCoreMigrationHashChange(t *testing.T) {
	baseline := loadBaseline(t)
	actual := withCandidateRevision(baseline)
	actual.CoreMigrations[0].SHA256 = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	if err := compare(actual, baseline, loadAdditions(t), testRevision); err == nil {
		t.Fatal("changed core migration hash passed release gate")
	}
}

func TestCompareRejectsPluginMigrationOrPinChange(t *testing.T) {
	baseline := loadBaseline(t)
	additions := loadAdditions(t)
	t.Run("plugin migration", func(t *testing.T) {
		actual := withCandidateRevision(baseline)
		entry := actual.Plugins["fxtwitter-preview"]
		entry.Migrations[0].SQLHash = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
		actual.Plugins["fxtwitter-preview"] = entry
		if err := compare(actual, baseline, additions, testRevision); err == nil {
			t.Fatal("changed plugin migration passed release gate")
		}
	})
	t.Run("external source pin", func(t *testing.T) {
		actual := withCandidateRevision(baseline)
		entry := actual.Plugins["hsr"]
		entry.SourceRef += "-changed"
		actual.Plugins["hsr"] = entry
		if err := compare(actual, baseline, additions, testRevision); err == nil {
			t.Fatal("changed external plugin pin passed release gate")
		}
	})
	t.Run("plugin set", func(t *testing.T) {
		actual := withCandidateRevision(baseline)
		delete(actual.Plugins, "fxtwitter-preview")
		if err := compare(actual, baseline, additions, testRevision); err == nil {
			t.Fatal("missing production plugin passed release gate")
		}
	})
}

func TestCompareAllowsOnlyExplicitOrderedMigrationAdditions(t *testing.T) {
	baseline := loadBaseline(t)
	additions := loadAdditions(t)
	actual := withCandidateRevision(baseline)
	extra := coreMigration{Version: 142, Path: "migration/000142_reviewed_change.up.sql", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	actual.CoreMigrations = append(actual.CoreMigrations, extra)
	if err := compare(actual, baseline, additions, testRevision); err == nil {
		t.Fatal("unapproved appended migration passed release gate")
	}
	additions.CoreMigrations = []coreMigration{extra}
	if err := compare(actual, baseline, additions, testRevision); err != nil {
		t.Fatalf("explicitly approved appended migration should pass: %v", err)
	}
	wrongOrder := extra
	wrongOrder.Version = 144
	additions.CoreMigrations = []coreMigration{wrongOrder}
	if err := compare(actual, baseline, additions, testRevision); err == nil {
		t.Fatal("out-of-order migration approval passed release gate")
	}
}

func TestCompareTracksLocalMigrationHistoryIndependently(t *testing.T) {
	baseline := loadBaseline(t)
	additions := loadAdditions(t)
	actual := withCandidateRevision(baseline)
	if len(actual.LocalMigrations) != 1 || actual.LocalMigrations[0].Version != 1 {
		t.Fatalf("expected pinned local migration history, got %+v", actual.LocalMigrations)
	}
	changed := withCandidateRevision(baseline)
	changed.LocalMigrations[0].SHA256 = strings.Repeat("f", 64)
	if err := compare(changed, baseline, additions, testRevision); err == nil {
		t.Fatal("changed local migration hash passed release gate")
	}

	extra := coreMigration{Version: 2, Path: "migration/local/000002_reviewed_local_change.up.sql", SHA256: strings.Repeat("a", 64)}
	changed = withCandidateRevision(baseline)
	changed.LocalMigrations = append(changed.LocalMigrations, extra)
	if err := compare(changed, baseline, additions, testRevision); err == nil {
		t.Fatal("unapproved local migration passed release gate")
	}
	additions.LocalMigrations = []coreMigration{extra}
	if err := compare(changed, baseline, additions, testRevision); err != nil {
		t.Fatalf("explicitly approved ordered local migration should pass: %v", err)
	}
	wrongOrder := extra
	wrongOrder.Version = 4
	additions.LocalMigrations = []coreMigration{wrongOrder}
	if err := compare(changed, baseline, additions, testRevision); err == nil {
		t.Fatal("out-of-order local migration approval passed")
	}
}

func TestComparePluginRegistryRequiresExactBundleAndFrontendPlugins(t *testing.T) {
	output := strings.Join([]string{
		"pluginbuild: dir=plugins/rolelevel name=role-level (role.example/plugin, frontend=false)",
		"pluginbuild: dir=plugins/genshin name=genshin (gen.example/plugin, frontend=true)",
		"pluginbuild: dir=plugins/fedwatch name=fedwatch (fed.example/plugin, frontend=true)",
		"pluginbuild: dir=plugins/hsr name=hsr (hsr.example/plugin, frontend=true)",
		"pluginbuild: dir=plugins/fxtwitter-preview name=fxtwitter-preview (fx.example/plugin, frontend=true, disabled but explicitly included)",
		"pluginbuild: dir=plugins/tracking-parameter-cleaner name=tracking-parameter-cleaner (cleaner.example/plugin, frontend=true)",
	}, "\n")
	if err := comparePluginRegistry(output); err != nil {
		t.Fatalf("expected exact production bundle to pass: %v", err)
	}
	if err := comparePluginRegistry(output + "\npluginbuild: dir=plugins/unexpected name=extra (extra.example/plugin, frontend=false)"); err == nil {
		t.Fatal("unexpected plugin passed registry gate")
	}
	if err := comparePluginRegistry(strings.Replace(output, "dir=plugins/tracking-parameter-cleaner name=tracking-parameter-cleaner (cleaner.example/plugin, frontend=true)", "dir=plugins/tracking-parameter-cleaner name=tracking-parameter-cleaner (cleaner.example/plugin, frontend=false)", 1)); err == nil {
		t.Fatal("frontend-only cleaner output passed without frontend registration")
	}
}

func TestDecodeInventoryRejectsTrailingJSON(t *testing.T) {
	data, err := json.Marshal(loadBaseline(t))
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte(" {}")...)
	if _, err := decodeInventory(data); err == nil {
		t.Fatal("trailing JSON object passed inventory decoder")
	}
}
