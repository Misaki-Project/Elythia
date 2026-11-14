package main

import (
	"encoding/json"
	"flag"
	"os"
	"os/exec"
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

func TestCompareRejectsFailClosedInventoryDrift(t *testing.T) {
	tests := []struct {
		name   string
		want   string
		mutate func(*inventory, *inventory, *migrationAdditions)
	}{
		{"source revision", "INVENTORY_REVISION_MISMATCH", func(actual, _ *inventory, _ *migrationAdditions) { actual.SourceRevision = strings.Repeat("b", 40) }},
		{"baseline format", "CORE_MIGRATIONS_DIFFER", func(_, baseline *inventory, _ *migrationAdditions) { baseline.Format = "unknown" }},
		{"empty source core history", "CORE_MIGRATIONS_DIFFER", func(actual, _ *inventory, _ *migrationAdditions) { actual.CoreMigrations = nil }},
		{"unapproved core migration", "CORE_MIGRATIONS_DIFFER", func(actual, _ *inventory, additions *migrationAdditions) {
			item := coreMigration{Version: 142, Path: "migration/000142_reviewed_change.up.sql", SHA256: strings.Repeat("a", 64)}
			actual.CoreMigrations = append(actual.CoreMigrations, item)
			additions.CoreMigrations = []coreMigration{{Version: 143, Path: item.Path, SHA256: item.SHA256}}
		}},
		{"invalid core path", "CORE_MIGRATIONS_DIFFER", func(actual, _ *inventory, additions *migrationAdditions) {
			item := coreMigration{Version: 142, Path: "migration/local/000142_wrong_track.up.sql", SHA256: strings.Repeat("a", 64)}
			actual.CoreMigrations = append(actual.CoreMigrations, item)
			additions.CoreMigrations = []coreMigration{item}
		}},
		{"invalid core hash", "CORE_MIGRATIONS_DIFFER", func(actual, _ *inventory, additions *migrationAdditions) {
			item := coreMigration{Version: 142, Path: "migration/000142_reviewed_change.up.sql", SHA256: "bad"}
			actual.CoreMigrations = append(actual.CoreMigrations, item)
			additions.CoreMigrations = []coreMigration{item}
		}},
		{"unapproved local migration", "LOCAL_MIGRATIONS_DIFFER", func(actual, _ *inventory, _ *migrationAdditions) {
			actual.LocalMigrations = append(actual.LocalMigrations, coreMigration{Version: 2, Path: "migration/local/000002_reviewed_change.up.sql", SHA256: strings.Repeat("a", 64)})
		}},
		{"plugin set", "PLUGIN_SET_DIFFER", func(actual, _ *inventory, _ *migrationAdditions) {
			actual.Plugins["unexpected"] = pluginEntry{SourceRef: "external@pin", Version: "1.0.0"}
		}},
		{"missing approved plugin version", "PLUGIN_MIGRATIONS_DIFFER", func(_, _ *inventory, additions *migrationAdditions) {
			entry := additions.Plugins["hsr"]
			entry.Version = ""
			additions.Plugins["hsr"] = entry
		}},
		{"invalid plugin migration sequence", "APPROVED_PLUGIN_MIGRATION_SEQUENCE_INVALID", func(_, _ *inventory, additions *migrationAdditions) {
			entry := additions.Plugins["tracking-parameter-cleaner"]
			entry.Migrations = []migration{{Version: 2, SQLHash: strings.Repeat("a", 64)}}
			additions.Plugins["tracking-parameter-cleaner"] = entry
		}},
		{"invalid plugin migration hash", "APPROVED_PLUGIN_MIGRATION_SEQUENCE_INVALID", func(_, _ *inventory, additions *migrationAdditions) {
			entry := additions.Plugins["tracking-parameter-cleaner"]
			entry.Migrations = []migration{{Version: 1, SQLHash: "bad"}}
			additions.Plugins["tracking-parameter-cleaner"] = entry
		}},
		{"actual plugin version", "PLUGIN_MIGRATIONS_DIFFER", func(actual, _ *inventory, _ *migrationAdditions) {
			entry := actual.Plugins["hsr"]
			entry.Version += "-changed"
			actual.Plugins["hsr"] = entry
		}},
		{"local plugin source revision", "LOCAL_PLUGIN_REVISION_MISMATCH", func(actual, _ *inventory, _ *migrationAdditions) {
			entry := actual.Plugins["fxtwitter-preview"]
			entry.SourceRef = "local@wrong"
			actual.Plugins["fxtwitter-preview"] = entry
		}},
		{"external plugin pin", "EXTERNAL_PLUGIN_PIN_MISMATCH", func(actual, _ *inventory, _ *migrationAdditions) {
			entry := actual.Plugins["hsr"]
			entry.SourceRef += "-changed"
			actual.Plugins["hsr"] = entry
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseline := loadBaseline(t)
			additions := loadAdditions(t)
			actual := withCandidateRevision(baseline)
			tt.mutate(&actual, &baseline, &additions)
			err := compare(actual, baseline, additions, testRevision)
			if err == nil || err.Error() != tt.want {
				t.Fatalf("compare error = %v, want %s", err, tt.want)
			}
		})
	}
}

func TestDecodeAdditionsRejectsInvalidJSON(t *testing.T) {
	valid := []byte("{\"format\":\"elythia-production-migration-additions/v2\",\"core_migrations\":[],\"local_migrations\":[],\"plugins\":{}}")
	tests := []struct {
		name string
		data []byte
	}{
		{"malformed", []byte("{\"format\":")},
		{"unknown field", []byte("{\"format\":\"elythia-production-migration-additions/v2\",\"unexpected\":true}")},
		{"trailing value", append(append([]byte(nil), valid...), []byte(" {}")...)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := decodeAdditions(tt.data); err == nil {
				t.Fatal("invalid additions JSON passed decoder")
			}
		})
	}
}

func TestComparePluginRegistryRejectsInvalidBundles(t *testing.T) {
	valid := strings.Join([]string{
		"pluginbuild: dir=plugins/rolelevel name=role-level (role.example/plugin, frontend=false)",
		"pluginbuild: dir=plugins/genshin name=genshin (gen.example/plugin, frontend=true)",
		"pluginbuild: dir=plugins/fedwatch name=fedwatch (fed.example/plugin, frontend=true)",
		"pluginbuild: dir=plugins/hsr name=hsr (hsr.example/plugin, frontend=true)",
		"pluginbuild: dir=plugins/fxtwitter-preview name=fxtwitter-preview (fx.example/plugin, frontend=true)",
		"pluginbuild: dir=plugins/tracking-parameter-cleaner name=tracking-parameter-cleaner (cleaner.example/plugin, frontend=true)",
	}, "\n")
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{"duplicate directory", valid + "\npluginbuild: dir=plugins/rolelevel name=role-level (role.example/plugin, frontend=false)", "PLUGIN_REGISTRY_DIFFER"},
		{"unknown directory", valid + "\npluginbuild: dir=plugins/extra name=extra (extra.example/plugin, frontend=false)", "PLUGIN_REGISTRY_DIFFER"},
		{"malformed registration", valid + "\npluginbuild: dir=plugins/rolelevel", "PLUGIN_REGISTRY_DIFFER"},
		{"incomplete bundle", strings.Replace(valid, "\npluginbuild: dir=plugins/hsr name=hsr (hsr.example/plugin, frontend=true)", "", 1), "PLUGIN_REGISTRY_INCOMPLETE"},
		{"Fx frontend omitted", strings.Replace(valid, "dir=plugins/fxtwitter-preview name=fxtwitter-preview (fx.example/plugin, frontend=true)", "dir=plugins/fxtwitter-preview name=fxtwitter-preview (fx.example/plugin, frontend=false)", 1), "PLUGIN_FRONTEND_REGISTRATION_MISSING"},
		{"cleaner frontend omitted", strings.Replace(valid, "dir=plugins/tracking-parameter-cleaner name=tracking-parameter-cleaner (cleaner.example/plugin, frontend=true)", "dir=plugins/tracking-parameter-cleaner name=tracking-parameter-cleaner (cleaner.example/plugin, frontend=false)", 1), "PLUGIN_FRONTEND_REGISTRATION_MISSING"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := comparePluginRegistry(tt.output)
			if err == nil || err.Error() != tt.want {
				t.Fatalf("registry error = %v, want %s", err, tt.want)
			}
		})
	}
}

func TestProductionInventoryMainHelperProcess(t *testing.T) {
	if os.Getenv("CHECKPRODINVENTORY_HELPER") != "1" {
		return
	}
	var args []string
	if err := json.Unmarshal([]byte(os.Getenv("CHECKPRODINVENTORY_ARGS")), &args); err != nil {
		t.Fatal(err)
	}
	flag.CommandLine = flag.NewFlagSet("checkproductioninventory", flag.ExitOnError)
	os.Args = append([]string{"checkproductioninventory"}, args...)
	main()
}

func TestProductionInventoryCLI(t *testing.T) {
	type files struct {
		inventory []byte
		baseline  []byte
		additions []byte
		registry  []byte
	}
	baselineBytes, err := os.ReadFile(filepath.Join("..", "..", ".github", "release", "production-migration-baseline.json"))
	if err != nil {
		t.Fatal(err)
	}
	additionsBytes, err := os.ReadFile(filepath.Join("..", "..", ".github", "release", "approved-production-migration-additions.json"))
	if err != nil {
		t.Fatal(err)
	}
	actualBytes, err := json.Marshal(withCandidateRevision(loadBaseline(t)))
	if err != nil {
		t.Fatal(err)
	}
	registry := []byte(strings.Join([]string{
		"pluginbuild: dir=plugins/rolelevel name=role-level (role.example/plugin, frontend=false)",
		"pluginbuild: dir=plugins/genshin name=genshin (gen.example/plugin, frontend=true)",
		"pluginbuild: dir=plugins/fedwatch name=fedwatch (fed.example/plugin, frontend=true)",
		"pluginbuild: dir=plugins/hsr name=hsr (hsr.example/plugin, frontend=true)",
		"pluginbuild: dir=plugins/fxtwitter-preview name=fxtwitter-preview (fx.example/plugin, frontend=true)",
		"pluginbuild: dir=plugins/tracking-parameter-cleaner name=tracking-parameter-cleaner (cleaner.example/plugin, frontend=true)",
	}, "\n"))
	cases := []struct {
		name        string
		mutate      func(*files)
		revision    string
		wantCode    string
		wantSuccess bool
	}{
		{"success", func(*files) {}, testRevision, "", true},
		{"invalid revision", func(*files) {}, "not-a-sha", "ARGUMENTS_INVALID", false},
		{"baseline hash mismatch", func(f *files) { f.baseline = append(append([]byte(nil), f.baseline...), ' ') }, testRevision, "BASELINE_HASH_MISMATCH", false},
		{"additions hash mismatch", func(f *files) { f.additions = append(append([]byte(nil), f.additions...), ' ') }, testRevision, "APPROVED_ADDITIONS_HASH_MISMATCH", false},
		{"inventory unreadable", func(*files) {}, testRevision, "INVENTORY_UNREADABLE", false},
		{"plugin registry mismatch", func(f *files) {
			f.registry = []byte("pluginbuild: dir=plugins/rolelevel name=role-level (role.example/plugin, frontend=false)")
		}, testRevision, "PLUGIN_REGISTRY_INCOMPLETE", false},
		{"pluginbuild output unreadable", func(*files) {}, testRevision, "PLUGINBUILD_OUTPUT_UNREADABLE", false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			payload := files{
				inventory: append([]byte(nil), actualBytes...),
				baseline:  append([]byte(nil), baselineBytes...),
				additions: append([]byte(nil), additionsBytes...),
				registry:  append([]byte(nil), registry...),
			}
			tt.mutate(&payload)
			dir := t.TempDir()
			inventoryPath := filepath.Join(dir, "inventory.json")
			baselinePath := filepath.Join(dir, "baseline.json")
			additionsPath := filepath.Join(dir, "additions.json")
			registryPath := filepath.Join(dir, "pluginbuild.log")
			for path, data := range map[string][]byte{inventoryPath: payload.inventory, baselinePath: payload.baseline, additionsPath: payload.additions, registryPath: payload.registry} {
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{
				"--inventory=" + inventoryPath, "--baseline=" + baselinePath,
				"--approved-additions=" + additionsPath, "--pluginbuild-output=" + registryPath,
				"--revision=" + tt.revision,
			}
			if tt.name == "inventory unreadable" {
				args[0] = "--inventory=" + filepath.Join(dir, "missing-inventory.json")
			}
			if tt.name == "pluginbuild output unreadable" {
				args[3] = "--pluginbuild-output=" + filepath.Join(dir, "missing-pluginbuild.log")
			}
			argsJSON, err := json.Marshal(args)
			if err != nil {
				t.Fatal(err)
			}
			if tt.wantSuccess {
				savedArgs, savedFlags, savedStdout := os.Args, flag.CommandLine, os.Stdout
				outputFile, err := os.CreateTemp(t.TempDir(), "cli-output-*.txt")
				if err != nil {
					t.Fatal(err)
				}
				restoreGlobals := func() {
					os.Args, flag.CommandLine, os.Stdout = savedArgs, savedFlags, savedStdout
				}
				defer restoreGlobals()
				flag.CommandLine = flag.NewFlagSet("checkproductioninventory", flag.ExitOnError)
				os.Args = append([]string{"checkproductioninventory"}, args...)
				os.Stdout = outputFile
				main()
				restoreGlobals()
				if err := outputFile.Close(); err != nil {
					t.Fatal(err)
				}
				output, err := os.ReadFile(outputFile.Name())
				if err != nil || !strings.Contains(string(output), "PRODUCTION_INVENTORY_OK core=") {
					t.Fatalf("CLI success output = %q, error = %v", output, err)
				}
				return
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestProductionInventoryMainHelperProcess$")
			cmd.Env = append(os.Environ(), "CHECKPRODINVENTORY_HELPER=1", "CHECKPRODINVENTORY_ARGS="+string(argsJSON))
			output, runErr := cmd.CombinedOutput()
			text := string(output)
			if runErr == nil || !strings.Contains(text, tt.wantCode) {
				t.Fatalf("CLI output = %q, error = %v, want code %s", text, runErr, tt.wantCode)
			}
		})
	}
}
