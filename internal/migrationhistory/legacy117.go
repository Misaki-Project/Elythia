// Package migrationhistory checks ambiguous migration histories before applying SQL.
package migrationhistory

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5"
)

// CheckLegacy117 refuses the old Misaki numbering when canonical core 117 was
// skipped. It reads the supplied target database and never changes either ledger.
// Other versions, dirty states and custom migration sources are left to the caller.
func CheckLegacy117(ctx context.Context, coreDir, databaseURL string, version uint, dirty bool) error {
	if version != 117 || dirty {
		return nil
	}
	info, err := os.Stat(filepath.Join(coreDir, "000117_user_account_created_at.up.sql"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect core migration 117: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil
	}

	// 旧forkの117は上流116の処理だった。同じ番号だけを信じると、必要な列を
	// 作る新core117が飛ばされる。判断できないDBは手順を案内して止め、採用しない。
	u, err := url.Parse(databaseURL)
	if err != nil {
		// URLを保持するparse errorを返すと資格情報も外へ出る。
		return errors.New("invalid database URL for legacy migration check")
	}
	u.Scheme = "postgres"
	connectionConfig, err := pgx.ParseConfig(u.String())
	if err != nil {
		return errors.New("invalid database URL for legacy migration check")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := pgx.ConnectConfig(ctx, connectionConfig)
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
