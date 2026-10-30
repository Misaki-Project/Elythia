package backup

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"

	gomigrate "github.com/golang-migrate/migrate/v4"

	"github.com/elythia-network/elythia/internal/cli/migrate"
)

// applyBackupFixtureMigrations applies the bundled tracks and their ledgers.
// coreSteps limits only the core track; zero applies all core migrations.
func applyBackupFixtureMigrations(databaseURL string, coreSteps int) error {
	u, err := url.Parse(databaseURL)
	if err != nil {
		return err
	}
	u.Scheme = "pgx5"
	for _, track := range []struct {
		name, dir, table string
	}{
		{"core", rsCoreMigrations, ""},
		{"local", filepath.Join(rsCoreMigrations, "local"), migrate.LocalTable},
	} {
		if track.name == "local" {
			_, count, err := migrate.LatestVersion(track.dir)
			if err != nil {
				return fmt.Errorf("read local fixture migrations: %w", err)
			}
			if count == 0 {
				continue
			}
		}
		// 本番と同じくcoreの後にlocalを流し、台帳も別に作る。
		// SQLだけを当てたりcore台帳だけを手書きすると、復元後の自己検査を通らない。
		q := u.Query()
		q.Del("x-migrations-table")
		if track.table != "" {
			q.Set("x-migrations-table", track.table)
		}
		u.RawQuery = q.Encode()
		m, err := gomigrate.New("file://"+filepath.ToSlash(track.dir), u.String())
		if err != nil {
			return fmt.Errorf("open %s fixture migrations: %w", track.name, err)
		}
		if track.name == "core" && coreSteps != 0 {
			err = m.Steps(coreSteps)
		} else {
			err = m.Up()
		}
		sourceErr, databaseErr := m.Close()
		if errors.Is(err, gomigrate.ErrNoChange) {
			err = nil
		}
		if err = errors.Join(err, sourceErr, databaseErr); err != nil {
			return fmt.Errorf("apply %s fixture migrations: %w", track.name, err)
		}
	}
	return nil
}
