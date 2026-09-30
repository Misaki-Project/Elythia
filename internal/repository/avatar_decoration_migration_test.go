package repository

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/shiroha-a/mk/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

var (
	avatarDecorationMigDB      *gorm.DB
	avatarDecorationMigDBOnce  sync.Once
	avatarDecorationMigDBErr   error
	avatarDecorationMigSQL     string
	avatarDecorationMigSQLOnce sync.Once
	avatarDecorationMigSQLErr  error
)

func avatarDecorationMigrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	avatarDecorationMigDBOnce.Do(func() {
		avatarDecorationMigDB, avatarDecorationMigDBErr = testutil.OpenTestDBSchema("avatardecmig")
	})
	require.NoError(t, avatarDecorationMigDBErr)
	require.NotNil(t, avatarDecorationMigDB)
	return avatarDecorationMigDB
}

func avatarDecorationMigrationSQL(t *testing.T) string {
	t.Helper()
	avatarDecorationMigSQLOnce.Do(func() {
		body, err := os.ReadFile(filepath.Join("..", "..", "migration", "000098_drop_remote_avatar_decorations.up.sql"))
		avatarDecorationMigSQL, avatarDecorationMigSQLErr = string(body), err
	})
	require.NoError(t, avatarDecorationMigSQLErr)
	require.Contains(t, avatarDecorationMigSQL, `"host" IS NOT NULL`)
	return avatarDecorationMigSQL
}

func resetAvatarDecorationTable(t *testing.T, db *gorm.DB, withCherryPickColumns bool) {
	t.Helper()
	require.NoError(t, db.Exec(`DROP TABLE IF EXISTS "avatar_decoration"`).Error)
	columns := ""
	if withCherryPickColumns {
		columns = `,
		"remoteId" varchar(32),
		"host" varchar(128),
		"rawUrl" text`
	}
	require.NoError(t, db.Exec(`CREATE TABLE "avatar_decoration" (
		"id" varchar(32) PRIMARY KEY,
		"url" varchar(1024) NOT NULL,
		"name" varchar(256) NOT NULL,
		"description" varchar(2048) NOT NULL DEFAULT ''`+columns+`
	)`).Error)
}

func TestAvatarDecorationMigration_DropsEveryNonNullHost(t *testing.T) {
	db := avatarDecorationMigrationDB(t)
	resetAvatarDecorationTable(t, db, true)
	require.NoError(t, db.Exec(`INSERT INTO "avatar_decoration" (id, url, name, host) VALUES
		('local', 'https://local.test/local.png', 'local', NULL),
		('remote', 'https://remote.test/remote.png', 'remote', 'remote.test'),
		('empty-host', 'https://remote.test/empty.png', 'empty', '')`).Error)

	require.NoError(t, db.Exec(avatarDecorationMigrationSQL(t)).Error)
	// 手動再実行でも結果が変わらないこと。
	require.NoError(t, db.Exec(avatarDecorationMigrationSQL(t)).Error)

	var ids []string
	require.NoError(t, db.Raw(`SELECT id FROM "avatar_decoration" ORDER BY id`).Scan(&ids).Error)
	assert.Equal(t, []string{"local"}, ids)
}

func TestAvatarDecorationMigration_AcceptsUpstreamShapeWithoutHost(t *testing.T) {
	db := avatarDecorationMigrationDB(t)
	resetAvatarDecorationTable(t, db, false)
	require.NoError(t, db.Exec(`INSERT INTO "avatar_decoration" (id, url, name) VALUES
		('local', 'https://local.test/local.png', 'local')`).Error)

	require.NoError(t, db.Exec(avatarDecorationMigrationSQL(t)).Error)

	var count int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM "avatar_decoration"`).Scan(&count).Error)
	assert.EqualValues(t, 1, count)
}
