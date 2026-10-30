package migrate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	gomigrate "github.com/golang-migrate/migrate/v4"

	"github.com/elythia-network/elythia/internal/migrationhistory"
)

// refuseLegacy117 checks the one ambiguous version in the canonical core track.
// It never changes either migration ledger.
func refuseLegacy117(dir, databaseURL string, m migrator) error {
	info, err := os.Stat(filepath.Join(dir, "000117_user_account_created_at.up.sql"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect core migration 117: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	version, dirty, err := m.Version()
	if errors.Is(err, gomigrate.ErrNilVersion) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read core migration version: %w", err)
	}
	// cfgからURLを作り直さず、migratorと同じ注入済みDSN/search_pathを使う。
	return migrationhistory.CheckLegacy117(context.Background(), dir, databaseURL, version, dirty)
}
