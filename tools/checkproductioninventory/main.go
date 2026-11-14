// Command checkproductioninventory blocks release builds whose registered core
// or production plugin migrations differ from the reviewed production baseline.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
)

const baselineSHA256 = "a2182e0134bff1d660e2309bcaaa09d23eaa48249d21ded319deb9010675ce9b"
const additionsSHA256 = "5c43a714fc02ef9445e166af56bdd8d9ec8dc67c7b00fc1d429c5e883a919e15"

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

type pluginApproval struct {
	Version    string      `json:"version"`
	Migrations []migration `json:"migrations"`
}

type migrationAdditions struct {
	Format          string                    `json:"format"`
	CoreMigrations  []coreMigration           `json:"core_migrations"`
	LocalMigrations []coreMigration           `json:"local_migrations"`
	Plugins         map[string]pluginApproval `json:"plugins"`
}

func main() {
	inventoryPath := flag.String("inventory", "", "generated migration inventory JSON")
	baselinePath := flag.String("baseline", "", "reviewed production baseline JSON")
	additionsPath := flag.String("approved-additions", "", "explicitly approved appended migrations JSON")
	pluginbuildPath := flag.String("pluginbuild-output", "", "captured output from pluginbuild")
	revision := flag.String("revision", "", "checked-out source commit SHA")
	flag.Parse()
	if *inventoryPath == "" || *baselinePath == "" || *additionsPath == "" || *pluginbuildPath == "" || !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(*revision) {
		fail("ARGUMENTS_INVALID")
	}
	baselineBytes, err := os.ReadFile(*baselinePath)
	if err != nil {
		fail("BASELINE_UNREADABLE")
	}
	sum := sha256.Sum256(baselineBytes)
	if hex.EncodeToString(sum[:]) != baselineSHA256 {
		fail("BASELINE_HASH_MISMATCH")
	}
	additionsBytes, err := os.ReadFile(*additionsPath)
	if err != nil {
		fail("APPROVED_ADDITIONS_UNREADABLE")
	}
	additionsSum := sha256.Sum256(additionsBytes)
	if hex.EncodeToString(additionsSum[:]) != additionsSHA256 {
		fail("APPROVED_ADDITIONS_HASH_MISMATCH")
	}
	actualBytes, err := os.ReadFile(*inventoryPath)
	if err != nil {
		fail("INVENTORY_UNREADABLE")
	}
	actual, err := decodeInventory(actualBytes)
	if err != nil {
		fail("INVENTORY_INVALID")
	}
	baseline, err := decodeInventory(baselineBytes)
	if err != nil {
		fail("BASELINE_INVALID")
	}
	additions, err := decodeAdditions(additionsBytes)
	if err != nil {
		fail("APPROVED_ADDITIONS_INVALID")
	}
	if err := compare(actual, baseline, additions, *revision); err != nil {
		fail(err.Error())
	}
	pluginbuildOutput, err := os.ReadFile(*pluginbuildPath)
	if err != nil {
		fail("PLUGINBUILD_OUTPUT_UNREADABLE")
	}
	if err := comparePluginRegistry(string(pluginbuildOutput)); err != nil {
		fail(err.Error())
	}
	actualSum := sha256.Sum256(actualBytes)
	fmt.Printf("PRODUCTION_INVENTORY_OK core=%d plugins=%s inventory_sha256=%s\n",
		actual.CoreMigrations[len(actual.CoreMigrations)-1].Version,
		joinPluginNames(actual.Plugins), hex.EncodeToString(actualSum[:]))
}

func comparePluginRegistry(output string) error {
	want := map[string]bool{
		"plugins/rolelevel": true, "plugins/genshin": true, "plugins/fedwatch": true,
		"plugins/hsr": true, "plugins/fxtwitter-preview": true,
		"plugins/tracking-parameter-cleaner": true,
	}
	registered := make(map[string]bool, len(want))
	for _, line := range strings.Split(output, "\n") {
		const prefix = "pluginbuild: dir="
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		rest := strings.TrimPrefix(line, prefix)
		dir, tail, ok := strings.Cut(rest, " name=")
		if !ok || !want[dir] || registered[dir] {
			return fmt.Errorf("PLUGIN_REGISTRY_DIFFER")
		}
		registered[dir] = true
		if dir == "plugins/fxtwitter-preview" || dir == "plugins/tracking-parameter-cleaner" {
			_, frontend, found := strings.Cut(tail, ", frontend=")
			if !found || !(strings.HasPrefix(frontend, "true)") || strings.HasPrefix(frontend, "true,")) {
				return fmt.Errorf("PLUGIN_FRONTEND_REGISTRATION_MISSING")
			}
		}
	}
	if len(registered) != len(want) {
		return fmt.Errorf("PLUGIN_REGISTRY_INCOMPLETE")
	}
	return nil
}

func decodeInventory(data []byte) (inventory, error) {
	var value inventory
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return value, fmt.Errorf("trailing JSON")
	}
	return value, nil
}

func decodeAdditions(data []byte) (migrationAdditions, error) {
	var value migrationAdditions
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return value, fmt.Errorf("trailing JSON")
	}
	return value, nil
}

func compare(actual, baseline inventory, additions migrationAdditions, revision string) error {
	if actual.Format != "elythia-source-migration-inventory/v2" || actual.SourceRevision != revision {
		return fmt.Errorf("INVENTORY_REVISION_MISMATCH")
	}
	if baseline.Format != actual.Format || additions.Format != "elythia-production-migration-additions/v2" || len(actual.CoreMigrations) == 0 {
		return fmt.Errorf("CORE_MIGRATIONS_DIFFER")
	}
	approvedCore, ok := appendCoreMigrations(baseline.CoreMigrations, additions.CoreMigrations, `^migration/[0-9]{6}_[a-z0-9_]+\.up\.sql$`)
	if !ok || !reflect.DeepEqual(actual.CoreMigrations, approvedCore) {
		return fmt.Errorf("CORE_MIGRATIONS_DIFFER")
	}
	approvedLocal, ok := appendCoreMigrations(baseline.LocalMigrations, additions.LocalMigrations, `^migration/local/[0-9]{6}_[a-z0-9_]+\.up\.sql$`)
	if !ok || !reflect.DeepEqual(actual.LocalMigrations, approvedLocal) {
		return fmt.Errorf("LOCAL_MIGRATIONS_DIFFER")
	}
	// tracking-parameter-cleaner is intentionally registered as a metadata-only
	// Go plugin. It has no migrations; the baseline pins that empty inventory.
	want := map[string]bool{
		"role-level": true, "genshin": true, "fedwatch": true, "hsr": true,
		"fxtwitter-preview": true, "tracking-parameter-cleaner": true,
	}
	if len(actual.Plugins) != len(want) || len(baseline.Plugins) != len(want) || len(additions.Plugins) != len(want) {
		return fmt.Errorf("PLUGIN_SET_DIFFER")
	}
	for name := range actual.Plugins {
		if !want[name] {
			return fmt.Errorf("PLUGIN_SET_DIFFER")
		}
	}
	for name := range want {
		got, ok := actual.Plugins[name]
		base, baseOK := baseline.Plugins[name]
		approved, approvedOK := additions.Plugins[name]
		if !ok || !baseOK || !approvedOK || approved.Version == "" {
			return fmt.Errorf("PLUGIN_MIGRATIONS_DIFFER")
		}
		wantMigrations := append([]migration(nil), base.Migrations...)
		lastVersion := 0
		if len(wantMigrations) > 0 {
			lastVersion = wantMigrations[len(wantMigrations)-1].Version
		}
		for _, item := range approved.Migrations {
			if item.Version != lastVersion+1 || !validSHA256(item.SQLHash) {
				return fmt.Errorf("APPROVED_PLUGIN_MIGRATION_SEQUENCE_INVALID")
			}
			wantMigrations = append(wantMigrations, item)
			lastVersion = item.Version
		}
		if got.Version != approved.Version || !slices.Equal(got.Migrations, wantMigrations) {
			return fmt.Errorf("PLUGIN_MIGRATIONS_DIFFER")
		}
		if name == "role-level" || name == "fxtwitter-preview" || name == "tracking-parameter-cleaner" {
			if got.SourceRef != "local@"+revision {
				return fmt.Errorf("LOCAL_PLUGIN_REVISION_MISMATCH")
			}
		} else if got.SourceRef != base.SourceRef {
			return fmt.Errorf("EXTERNAL_PLUGIN_PIN_MISMATCH")
		}
	}
	return nil
}

func appendCoreMigrations(baseline, additions []coreMigration, pathPattern string) ([]coreMigration, bool) {
	approved := append([]coreMigration{}, baseline...)
	lastVersion := 0
	if len(approved) > 0 {
		lastVersion = approved[len(approved)-1].Version
	}
	pathRE := regexp.MustCompile(pathPattern)
	for _, item := range additions {
		if item.Version != lastVersion+1 || !validSHA256(item.SHA256) || !pathRE.MatchString(item.Path) {
			return nil, false
		}
		approved = append(approved, item)
		lastVersion = item.Version
	}
	return approved, true
}

func validSHA256(value string) bool {
	return regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(value)
}

func joinPluginNames(plugins map[string]pluginEntry) string {
	names := make([]string, 0, len(plugins))
	for name := range plugins {
		names = append(names, name)
	}
	sort.Strings(names)
	return fmt.Sprint(names)
}

func fail(code string) {
	fmt.Fprintln(os.Stderr, code)
	os.Exit(1)
}
