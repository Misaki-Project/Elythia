//go:build productioninventory

// Command migrationinventory emits the migration definitions that the bundled
// production plugins actually register. It does not connect to a database.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	fxtwitterpreview "github.com/Misaki-Project/Elythia/plugins/fxtwitter-preview"
	trackingcleaner "github.com/Misaki-Project/Elythia/plugins/tracking-parameter-cleaner"
	fedwatch "github.com/elythia-network/elythia-plugin-fedwatch"
	genshin "github.com/elythia-network/elythia-plugin-genshin"
	hsr "github.com/elythia-network/elythia-plugin-hsr"
	"github.com/elythia-network/elythia/plugin"
	rolelevel "github.com/shiroha-a/mk-plugin-rolelevel"
)

const format = "elythia-source-migration-inventory/v2"

type migration struct {
	Version int    `json:"version"`
	SQLHash string `json:"sql_sha256"`
}

type pluginEntry struct {
	SourceRef  string      `json:"source_ref"`
	Version    string      `json:"version"`
	Migrations []migration `json:"migrations"`
}

type coreMigration struct {
	Version int    `json:"version"`
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
}

type inventory struct {
	Format          string                 `json:"format"`
	SourceRevision  string                 `json:"source_revision"`
	CoreMigrations  []coreMigration        `json:"core_migrations"`
	LocalMigrations []coreMigration        `json:"local_migrations"`
	Plugins         map[string]pluginEntry `json:"plugins"`
}

func main() {
	revision := flag.String("revision", "", "full checked-out source commit SHA")
	flag.Parse()
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(*revision) {
		fail("SOURCE_REVISION_INVALID")
	}

	refs := map[string]string{
		"role-level":                 "local@" + *revision,
		"tracking-parameter-cleaner": "local@" + *revision,
		"genshin":                    "https://github.com/Misaki-Project/mk-plugin-genshin.git@9a50079ec9d198ab44e4b9ff24d0a408109c09da",
		"fedwatch":                   "https://github.com/Elythia-Network/elythia-plugin-fedwatch.git@af105a020656aaba121998a0bd5a51bee232dec8",
		"hsr":                        "https://github.com/Misaki-Project/elythia-plugin-hsr.git@820068d720654650d5dc05d4136ad9f32f677fef",
		"fxtwitter-preview":          "local@" + *revision,
	}
	definitions := []plugin.Definition{
		rolelevel.Plugin, genshin.Plugin, fedwatch.Plugin, hsr.Plugin, fxtwitterpreview.Plugin,
		trackingcleaner.Plugin,
	}
	plugins := make(map[string]pluginEntry, len(definitions))
	for _, definition := range definitions {
		ref, ok := refs[definition.Name]
		if !ok || definition.Version == "" {
			fail("PLUGIN_DEFINITION_UNREVIEWED")
		}
		migrations := make([]migration, 0, len(definition.Migrations))
		for _, item := range definition.Migrations {
			if item.Version <= 0 || item.SQL == "" {
				fail("PLUGIN_MIGRATION_INVALID")
			}
			sum := sha256.Sum256([]byte(item.SQL))
			migrations = append(migrations, migration{Version: item.Version, SQLHash: hex.EncodeToString(sum[:])})
		}
		sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
		for i := 1; i < len(migrations); i++ {
			if migrations[i-1].Version >= migrations[i].Version {
				fail("PLUGIN_MIGRATION_DUPLICATE")
			}
		}
		if _, exists := plugins[definition.Name]; exists {
			fail("PLUGIN_DEFINITION_DUPLICATE")
		}
		plugins[definition.Name] = pluginEntry{SourceRef: ref, Version: definition.Version, Migrations: migrations}
	}
	if len(plugins) != len(refs) {
		fail("PLUGIN_SET_INCOMPLETE")
	}

	core, err := inventoryMigrationFiles(filepath.Join("migration", "*.up.sql"))
	if err != nil || len(core) == 0 {
		fail("CORE_MIGRATIONS_UNAVAILABLE")
	}
	local, err := inventoryMigrationFiles(filepath.Join("migration", "local", "*.up.sql"))
	if err != nil {
		fail("LOCAL_MIGRATIONS_UNREADABLE")
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(inventory{Format: format, SourceRevision: *revision, CoreMigrations: core, LocalMigrations: local, Plugins: plugins}); err != nil {
		fail("INVENTORY_WRITE_FAILED")
	}
}

func inventoryMigrationFiles(pattern string) ([]coreMigration, error) {
	paths, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	out := make([]coreMigration, 0, len(paths))
	for _, path := range paths {
		base := filepath.Base(path)
		parts := strings.SplitN(base, "_", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("migration name invalid")
		}
		version, parseErr := strconv.Atoi(parts[0])
		if parseErr != nil || version <= 0 {
			return nil, fmt.Errorf("migration version invalid")
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, readErr
		}
		sum := sha256.Sum256(data)
		out = append(out, coreMigration{Version: version, Path: filepath.ToSlash(path), SHA256: hex.EncodeToString(sum[:])})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	for i := 1; i < len(out); i++ {
		if out[i-1].Version >= out[i].Version {
			return nil, fmt.Errorf("migration version duplicated")
		}
	}
	return out, nil
}

func fail(code string) {
	fmt.Fprintln(os.Stderr, code)
	os.Exit(1)
}
