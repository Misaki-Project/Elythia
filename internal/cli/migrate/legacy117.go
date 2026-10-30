package migrate

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	gomigrate "github.com/golang-migrate/migrate/v4"
	"github.com/jackc/pgx/v5"
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
	if version != 117 || dirty {
		return nil
	}

	// 旧forkの117は上流116の処理だった。同じ番号だけを信じると、必要な列を
	// 作る新core117が飛ばされる。判断できないDBは手順を案内して止め、採用しない。
	// cfgからURLを作り直さず、migratorと同じ注入済みDSN/search_pathを使う。
	u, err := url.Parse(databaseURL)
	if err != nil {
		// url.Errorは資格情報を含むURL全体を保持するので、そのまま返さない。
		return errors.New("invalid database URL for legacy migration check")
	}
	u.Scheme = "postgres"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, u.String())
	if err != nil {
		return fmt.Errorf("connect for legacy migration check: %w", err)
	}
	defer conn.Close(context.Background())
	var present bool
	err = conn.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'user' AND column_name = 'accountCreatedAt'
	)`).Scan(&present)
	if err != nil {
		return fmt.Errorf("read core migration 117 column: %w", err)
	}
	if !present {
		return errors.New("clean core version 117 lacks user.accountCreatedAt; refusing to migrate an unreconciled legacy Misaki database; " +
			"follow docs/misaki-migration-reconciliation.md before running up")
	}
	return nil
}
