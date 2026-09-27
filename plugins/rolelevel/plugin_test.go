package rolelevel

import (
	"testing"

	"github.com/shiroha-a/mk/plugin"
)

// manifest の `name` と `Definition.Name` がずれると、ルートが
// /api/plugin/<manifest名> に生えたのに admin/server-plugins の名前が別になる。
func TestPluginNameMatchesManifest(t *testing.T) {
	if Plugin.Name != "role-level" {
		t.Fatalf("Definition.Name = %q, want %q", Plugin.Name, "role-level")
	}
	if Plugin.APIVersion != plugin.APIVersion {
		t.Fatalf("APIVersion = %d, want %d", Plugin.APIVersion, plugin.APIVersion)
	}
	if err := Plugin.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(Plugin.Migrations) == 0 {
		t.Fatal("Migrations が空です")
	}
	if Plugin.Peered || Plugin.Peer != nil {
		t.Fatal("rolelevel は peer を使わないので Peered / Peer は立てない")
	}
}

// migration は4つのtableをversionedかつtransactionalに作る。
func TestMigrationsCreatePluginTables(t *testing.T) {
	db := testDB(t)
	newHarness(t, db, nil).Routes(Plugin)

	for _, table := range []string{
		"role_level_config", "role_level_experience",
		"role_level_operation", "role_level_audit",
	} {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM information_schema.tables
			WHERE table_schema = current_schema() AND table_name = $1`, table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("%s が作られていません", table)
		}
	}
}

// migration は冪等。production は起動のたびに呼ぶので、2回目が already exists で
// 落ちたら起動不能になる。
func TestMigrationsAreIdempotent(t *testing.T) {
	db := testDB(t)
	h := newHarness(t, db, nil)
	h.Routes(Plugin)
	h.Routes(Plugin)
}

// config の範囲違反は **起動を止める。** 黙って既定値で動かせない。
func TestLoadConfigRejectsOutOfRange(t *testing.T) {
	for _, tt := range []struct {
		name string
		cfg  map[string]any
	}{
		{"assignmentScanPages 0", map[string]any{"assignmentScanPages": 0}},
		{"assignmentScanPages 201", map[string]any{"assignmentScanPages": 201}},
		{"orphanRetentionDays 0", map[string]any{"orphanRetentionDays": 0}},
		{"orphanRetentionDays 3651", map[string]any{"orphanRetentionDays": 3651}},
		{"reconcileCron 4 fields", map[string]any{"reconcileCron": "*/10 * * *"}},
		{"orphanCron 6 fields", map[string]any{"orphanCron": "17 3 * * * *"}},
		{"pruneCron に不正文字", map[string]any{"pruneCron": "43 4 * * *; rm"}},
		{"actorId の形式が不正", map[string]any{"actorId": "bad id"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := loadConfig(plugintestContext(t, tt.cfg)); err == nil {
				t.Fatal("範囲外の設定を受け入れています")
			}
		})
	}
}

// actorId 未設定でも **起動は止めない。** read系の経路は動き、native を要する操作
// だけが stable code を返す。
func TestLoadConfigAllowsMissingActor(t *testing.T) {
	cfg, err := loadConfig(plugintestContext(t, nil))
	if err != nil {
		t.Fatalf("actorId 無しで起動を止めています: %v", err)
	}
	if cfg.ActorID != "" {
		t.Fatalf("actorId = %q, want empty", cfg.ActorID)
	}
	if cfg.AssignmentScanPages != 50 || cfg.OrphanRetentionDays != 30 {
		t.Fatalf("既定値が効きません: %+v", cfg)
	}
	if cfg.ReconcileCron != "*/10 * * * *" || cfg.OrphanCron != "17 3 * * *" || cfg.PruneCron != "43 4 * * *" {
		t.Fatalf("cron の既定値が効きません: %+v", cfg)
	}
}
