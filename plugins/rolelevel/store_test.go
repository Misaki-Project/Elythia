package rolelevel

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elythia-network/elythia/plugin"
)

func newTestStore(t *testing.T) (*sql.DB, *store) {
	t.Helper()
	db := testDB(t)
	// DBを使う全テストで、store呼び出しより先にPlugin migrationを適用する。
	newHarness(t, db, nil).Routes(Plugin)
	return db, &store{db: db}
}

func TestStoreConfigRoundTrip(t *testing.T) {
	_, s := newTestStore(t)
	ctx := context.Background()

	cfg := DefaultConfig()
	cfg.RoleID = "role1"
	cfg.UpdatedBy = "admin1"
	saved, err := s.UpsertConfig(ctx, cfg, 0)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Revision != 1 {
		t.Fatalf("初回 revision = %d, want 1", saved.Revision)
	}
	if saved.CreatedAt.IsZero() || saved.UpdatedAt.IsZero() {
		t.Fatalf("時刻が入っていません: %+v", saved)
	}

	got, found, err := s.LoadConfig(ctx, "role1")
	if err != nil || !found {
		t.Fatalf("found = %t %v", found, err)
	}
	if got.BaseLevel != 1 || len(got.ExperienceCurve) != 1 || got.ExperienceCurve[0].Base != 100 {
		t.Fatalf("往復で curve が壊れています: %+v", got)
	}
	if len(got.PolicyRanges) != 1 || got.PolicyRanges[0].Type != RangeBase {
		t.Fatalf("往復で ranges が壊れています: %+v", got.PolicyRanges)
	}
	if got.UpdatedBy != "admin1" {
		t.Fatalf("updatedBy = %q", got.UpdatedBy)
	}
	if _, found, err := s.LoadConfig(ctx, "nope"); err != nil || found {
		t.Fatalf("無い role が found になっています: %t %v", found, err)
	}
}

// **revision は楽観並行制御。** 運営者が古い画面を開いたまま保存すると
// 409 になる (spec「API errorにはstable codeを付け…conflictを区別する」)。
func TestStoreConfigRevisionConflict(t *testing.T) {
	_, s := newTestStore(t)
	ctx := context.Background()

	cfg := DefaultConfig()
	cfg.RoleID = "role1"
	first, err := s.UpsertConfig(ctx, cfg, 0)
	if err != nil {
		t.Fatal(err)
	}
	cfg.BaseLevel = 5
	second, err := s.UpsertConfig(ctx, cfg, first.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if second.Revision != 2 {
		t.Fatalf("revision = %d, want 2", second.Revision)
	}
	// 古い revision での保存は衝突。
	cfg.BaseLevel = 9
	if _, err := s.UpsertConfig(ctx, cfg, first.Revision); err == nil {
		t.Fatal("古い revision で上書きできています")
	}
	// 既存行に対して revision 0 (新規扱い) でも衝突。
	if _, err := s.UpsertConfig(ctx, cfg, 0); err == nil {
		t.Fatal("既存行を新規扱いして上書きできています")
	}
	// 存在しない role に revision > 0 を渡しても衝突扱い。
	missing := DefaultConfig()
	missing.RoleID = "gone"
	if _, err := s.UpsertConfig(ctx, missing, 7); err == nil {
		t.Fatal("存在しない role を新規扱いして作れています")
	}
	if _, found, err := s.LoadConfig(ctx, "gone"); err != nil || found {
		t.Fatalf("衝突した保存で row が作られました: %t %v", found, err)
	}
}

func TestStoreDuplicateCreateInTransactionLeavesTransactionUsable(t *testing.T) {
	db := testDBRequired(t)
	newHarness(t, db, nil).Routes(Plugin)
	s := &store{db: db}
	ctx := context.Background()
	cfg := DefaultConfig()
	cfg.RoleID = "role1"
	if _, err := s.UpsertConfig(ctx, cfg, 0); err != nil {
		t.Fatal(err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback() //nolint:errcheck // the test intentionally does not commit
	if _, err := s.UpsertConfigTx(ctx, tx, cfg, 0); err == nil {
		t.Fatal("duplicate create succeeded")
	} else if _, code := extractCode(err); code != CodeConfigConflict {
		t.Fatalf("duplicate create code = %v, want %s: %v", code, CodeConfigConflict, err)
	}
	var one int
	if err := tx.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil {
		t.Fatalf("transaction was aborted after duplicate create: %v", err)
	}
}

// **返り値はその文自身が書いた行でなければならない。** 書き込みのあとに読み直すと、
// 並行に進んだ別 transaction の行を自分の保存結果として返してしまう。
func TestStoreUpsertConfigReturnsWrittenRow(t *testing.T) {
	_, s := newTestStore(t)
	ctx := context.Background()

	cfg := DefaultConfig()
	cfg.RoleID = "role1"
	cfg.UpdatedBy = "admin1"
	created, err := s.UpsertConfig(ctx, cfg, 0)
	if err != nil {
		t.Fatal(err)
	}
	if created.Revision != 1 {
		t.Fatalf("created revision = %d, want 1", created.Revision)
	}

	cfg.BaseLevel = 7
	cfg.UpdatedBy = "admin2"
	updated, err := s.UpsertConfig(ctx, cfg, created.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 {
		t.Fatalf("updated revision = %d, want 2", updated.Revision)
	}
	if updated.RoleID != "role1" || updated.BaseLevel != 7 || updated.UpdatedBy != "admin2" {
		t.Fatalf("自分の書き込みが返っていません: %+v", updated)
	}
	if len(updated.ExperienceCurve) != 1 || updated.ExperienceCurve[0].Base != 100 {
		t.Fatalf("自分の curve が返っていません: %+v", updated.ExperienceCurve)
	}
	if len(updated.PolicyRanges) != 1 || updated.PolicyRanges[0].Type != RangeBase {
		t.Fatalf("自分の policy ranges が返っていません: %+v", updated.PolicyRanges)
	}
	// 作成時刻は更新で変わらない。別行が返っているならここがずれる。
	if !updated.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("created_at が変わっています: %s -> %s", created.CreatedAt, updated.CreatedAt)
	}
	if updated.UpdatedAt.Before(updated.CreatedAt) {
		t.Fatalf("updated_at < created_at: %s", updated.UpdatedAt)
	}

	third := cfg
	third.BaseLevel = 9
	again, err := s.UpsertConfig(ctx, third, updated.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if again.Revision != 3 || again.BaseLevel != 9 {
		t.Fatalf("3 回目の保存結果 = %+v", again)
	}
	if !again.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("3 回目で created_at が変わっています: %s", again.CreatedAt)
	}
}

// **同じ revision への同時保存は 1 件だけ通る。** もう 1 件は 409 で止まり、
// revision が 2 段進んで残らない。
func TestStoreUpsertConfigConcurrentRevision(t *testing.T) {
	_, s := newTestStore(t)
	ctx := context.Background()

	cfg := DefaultConfig()
	cfg.RoleID = "role1"
	created, err := s.UpsertConfig(ctx, cfg, 0)
	if err != nil {
		t.Fatal(err)
	}

	levels := []int64{3, 4}
	errs := make([]error, len(levels))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range levels {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			attempt := cfg
			attempt.BaseLevel = levels[i]
			<-start
			_, errs[i] = s.UpsertConfig(ctx, attempt, created.Revision)
		}(i)
	}
	close(start)
	wg.Wait()

	wins, conflicts := 0, 0
	for i, err := range errs {
		if err == nil {
			wins++
			continue
		}
		if _, code := extractCode(err); code != CodeConfigConflict {
			t.Fatalf("同時保存 %d が 409 以外で失敗しました: %v", i, err)
		}
		conflicts++
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("成功 %d / 衝突 %d, want 1 / 1", wins, conflicts)
	}
	// 負けた方の書き込みは残らない。revision は 1 段だけ進む。
	got, found, err := s.LoadConfig(ctx, "role1")
	if err != nil || !found {
		t.Fatalf("found = %t %v", found, err)
	}
	if got.Revision != created.Revision+1 {
		t.Fatalf("revision = %d, want %d", got.Revision, created.Revision+1)
	}
	if got.BaseLevel != 3 && got.BaseLevel != 4 {
		t.Fatalf("baseLevel = %d, want 3 か 4", got.BaseLevel)
	}
}

func TestStoreDeleteConfig(t *testing.T) {
	_, s := newTestStore(t)
	ctx := context.Background()

	cfg := DefaultConfig()
	cfg.RoleID = "role1"
	saved, err := s.UpsertConfig(ctx, cfg, 0)
	if err != nil {
		t.Fatal(err)
	}
	if deleted, err := s.DeleteConfig(ctx, "role1", saved.Revision+1); err != nil || deleted {
		t.Fatalf("古い revision で消えています: %t %v", deleted, err)
	}
	deleted, err := s.DeleteConfig(ctx, "role1", saved.Revision)
	if err != nil || !deleted {
		t.Fatalf("削除できません: %t %v", deleted, err)
	}
	if deleted, err := s.DeleteConfig(ctx, "role1", saved.Revision); err != nil || deleted {
		t.Fatalf("2回目はdeleted = false になるべき: %t %v", deleted, err)
	}
}

func TestStoreRolesWithExperienceIncludesRolesWithoutConfig(t *testing.T) {
	_, s := newTestStore(t)
	ctx := context.Background()
	cfg := DefaultConfig()
	cfg.RoleID = "configured"
	saved, err := s.UpsertConfig(ctx, cfg, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetExperience(ctx, s.db, experienceRow{AssignmentID: "a", RoleID: "configured", UserID: "u", Experience: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetExperience(ctx, s.db, experienceRow{AssignmentID: "b", RoleID: "deleted", UserID: "u", Experience: 2}); err != nil {
		t.Fatal(err)
	}
	if deleted, err := s.DeleteConfig(ctx, "configured", saved.Revision); err != nil || !deleted {
		t.Fatalf("delete config: %v %v", deleted, err)
	}
	roles, err := s.RolesWithExperience(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(roles); got != "[configured deleted]" {
		t.Fatalf("roles = %s, want [configured deleted]", got)
	}
}

// **XP は assignment_id が primary key。** unassign/reassign で古い XP が復活しないのは
// ここが根拠。
func TestStoreExperienceIsKeyedByAssignment(t *testing.T) {
	_, s := newTestStore(t)
	ctx := context.Background()

	row := experienceRow{AssignmentID: "asg1", RoleID: "role1", UserID: "u1", Experience: 250}
	if err := s.SetExperience(ctx, s.db, row); err != nil {
		t.Fatal(err)
	}
	row.Experience = 300
	if err := s.SetExperience(ctx, s.db, row); err != nil {
		t.Fatal(err)
	}
	got, err := s.ExperienceForAssignments(ctx, []string{"asg1"})
	if err != nil {
		t.Fatal(err)
	}
	if got["asg1"] != 300 {
		t.Fatalf("上書きされてません: %+v", got)
	}

	// 同じ user / role でも別の assignment は別の行。
	next := experienceRow{AssignmentID: "asg2", RoleID: "role1", UserID: "u1", Experience: 0}
	if err := s.SetExperience(ctx, s.db, next); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ExperienceRowsForRoleUser(ctx, "role1", "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("行 = %d, want 2 (古い asg1 と新しい asg2)", len(rows))
	}
	got, err = s.ExperienceForAssignments(ctx, []string{"asg2"})
	if err != nil {
		t.Fatal(err)
	}
	if got["asg2"] != 0 {
		t.Fatalf("新しい assignment の XP が 0 ではありません: %+v", got)
	}
}

func TestStoreExperienceRejectsAssignmentIdentityMismatch(t *testing.T) {
	_, s := newTestStore(t)
	ctx := context.Background()
	original := experienceRow{AssignmentID: "asg1", RoleID: "role1", UserID: "u1", Experience: 10}
	if err := s.SetExperience(ctx, s.db, original); err != nil {
		t.Fatal(err)
	}
	mismatch := experienceRow{AssignmentID: "asg1", RoleID: "other", UserID: "u1", Experience: 20}
	if err := s.SetExperience(ctx, s.db, mismatch); err == nil {
		t.Fatal("assignment identity mismatch was silently accepted")
	}
	got, err := s.ExperienceForAssignments(ctx, []string{"asg1"})
	if err != nil {
		t.Fatal(err)
	}
	if got["asg1"] != 10 {
		t.Fatalf("mismatched update changed experience to %d", got["asg1"])
	}
}

// **空のリストは「行が無い」= XP 0。** `IN ()` に落としてはいけない。
func TestStoreExperienceForEmptyList(t *testing.T) {
	_, s := newTestStore(t)
	got, err := s.ExperienceForAssignments(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("空リストで %d 件返しました", len(got))
	}
}

// **同値は assignment id 昇順で決定的に並ぶ。** offset をまたいでもその並びが
// 崩れないことが、page を切る側の前提になる。
func TestStoreExperienceRowsForRolePaging(t *testing.T) {
	_, s := newTestStore(t)
	ctx := context.Background()
	xp := map[string]int64{"a": 40, "b": 20, "c": 40, "d": 40, "e": 40}
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		row := experienceRow{
			AssignmentID: id, RoleID: "role1",
			UserID: "u-" + id, Experience: xp[id],
		}
		if err := s.SetExperience(ctx, s.db, row); err != nil {
			t.Fatal(err)
		}
	}
	// XP 降順、同値は assignment id 昇順。全体は a c d e b。
	page := func(limit, offset int, want ...string) {
		t.Helper()
		rows, err := s.ExperienceRowsForRole(ctx, "role1", limit, offset)
		if err != nil {
			t.Fatal(err)
		}
		assertExperienceIDs(t, rows, want...)
	}
	page(2, 0, "a", "c")
	page(2, 2, "d", "e")
	page(2, 4, "b")
	keyset, err := s.ExperienceRowsForRoleAfter(ctx, "role1", 2, 40, "c")
	if err != nil {
		t.Fatal(err)
	}
	assertExperienceIDs(t, keyset, "d", "e")
	// offset を飛ばした全件でも同じ順序。ページ同士が重ならないことも読み取れる。
	page(5, 0, "a", "c", "d", "e", "b")
	// 範囲外の offset は空で、エラーにしない。
	page(2, 5)
	// 別の role の行は混ざらない。
	other := experienceRow{AssignmentID: "asg-other", RoleID: "role2", UserID: "u-other", Experience: 9000}
	if err := s.SetExperience(ctx, s.db, other); err != nil {
		t.Fatal(err)
	}
	page(5, 0, "a", "c", "d", "e", "b")
}

// **LoadConfigsForRoles は PostgreSQL の ANY 配列でまとめて受ける。** role を 1 件ずつ
// 引くと、policy 解決の round trip が role 数だけ増える。
func TestStoreLoadConfigsForRoles(t *testing.T) {
	_, s := newTestStore(t)
	ctx := context.Background()
	for i, roleID := range []string{"role1", "role2", "role3"} {
		cfg := DefaultConfig()
		cfg.RoleID = roleID
		cfg.BaseLevel = int64(i + 1)
		if _, err := s.UpsertConfig(ctx, cfg, 0); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.LoadConfigsForRoles(ctx, []string{"role3", "role1", "absent"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("%d 件返しました, want 2", len(got))
	}
	// 設定が無い role は入らない。
	if _, present := got["absent"]; present {
		t.Fatalf("設定が無い role が入っています: %+v", got)
	}
	// map は role id を key にする。並び順ではなく id で引く契約。
	if got["role1"].RoleID != "role1" || got["role1"].BaseLevel != 1 {
		t.Fatalf("role1 = %+v", got["role1"])
	}
	if got["role3"].RoleID != "role3" || got["role3"].BaseLevel != 3 {
		t.Fatalf("role3 = %+v", got["role3"])
	}
	// 並び順を入れ替えた配列と 1 要素だけの配列でも、同じ行を返す。
	swapped, err := s.LoadConfigsForRoles(ctx, []string{"role1", "role3"})
	if err != nil {
		t.Fatal(err)
	}
	if len(swapped) != 2 || swapped["role1"].BaseLevel != 1 || swapped["role3"].BaseLevel != 3 {
		t.Fatalf("入れ替え = %+v", swapped)
	}
	one, err := s.LoadConfigsForRoles(ctx, []string{"role2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 1 || one["role2"].BaseLevel != 2 {
		t.Fatalf("role2 単独 = %+v", one)
	}
	// 空の入力は空 map。クエリを投げないので文法エラーにならない。
	empty, err := s.LoadConfigsForRoles(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("空入力で %d 件返しました", len(empty))
	}
	blank, err := s.LoadConfigsForRoles(ctx, []string{})
	if err != nil || len(blank) != 0 {
		t.Fatalf("空スライス = %+v %v", blank, err)
	}
}

// **ListConfigs は role id 昇順。** 管理画面が offset でページを切る前提の並び。
func TestStoreListConfigsIsOrderedByRoleID(t *testing.T) {
	_, s := newTestStore(t)
	ctx := context.Background()
	// 辞書順と数値順が食い違う id を、挿入順とも違う順に入れる。
	for i, roleID := range []string{"role10", "role2", "role1"} {
		cfg := DefaultConfig()
		cfg.RoleID = roleID
		cfg.BaseLevel = int64(i + 1)
		if _, err := s.UpsertConfig(ctx, cfg, 0); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.ListConfigs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ids := configIDs(all); ids != "[role1 role10 role2]" {
		t.Fatalf("order = %s, want [role1 role10 role2]", ids)
	}
	// 並びと行が両方そろっていること。baseLevel は 3, 1, 2 の順。
	if all[0].BaseLevel != 3 || all[1].BaseLevel != 1 || all[2].BaseLevel != 2 {
		t.Fatalf("行が並んでいません: %s", configIDs(all))
	}
	// offset でページを切るので、2 回呼んでも同じ順序であること。
	again, err := s.ListConfigs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ids := configIDs(again); ids != "[role1 role10 role2]" {
		t.Fatalf("2 回目の order = %s", ids)
	}
	// 1 つも設定が無い環境では nil ではなく空スライスを返す。
	for _, roleID := range []string{"role1", "role10", "role2"} {
		if _, err := s.DeleteConfig(ctx, roleID, 1); err != nil {
			t.Fatal(err)
		}
	}
	none, err := s.ListConfigs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if none == nil || len(none) != 0 {
		t.Fatalf("空のとき = %+v, want 空の非 nil スライス", none)
	}
}

// **FOR UPDATE は 2 つの transaction を直列化する。** 1 つ目が保持している間、2 つ目は
// 同じ行を読めない。Task 8 の完了経路がここを土台に最終 XP 書き込みを 1 本にする。
func TestStoreLoadOperationForUpdateSerializesTransactions(t *testing.T) {
	_, s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.InsertOperation(ctx, operation{
		IdempotencyKey: "key1", ActorID: "admin1", UserID: "u1", RoleID: "role1",
		Mode: "add", Operand: 1, Status: "pending",
	}); err != nil {
		t.Fatal(err)
	}

	first, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Rollback() //nolint:errcheck // commit 済みなら ErrTxDone
	if _, found, err := s.LoadOperationForUpdate(ctx, first, "key1"); err != nil || !found {
		t.Fatalf("load: %t %v", found, err)
	}

	second, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Rollback() //nolint:errcheck // 失敗時は rollback
	done := make(chan error, 1)
	go func() {
		_, found, err := s.LoadOperationForUpdate(ctx, second, "key1")
		switch {
		case err != nil:
			done <- err
		case !found:
			done <- fmt.Errorf("key1 が見つかりません")
		default:
			done <- nil
		}
	}()

	// ロックを保持している間は 2 つ目が読めない。待ち時間を長く取りすぎない。
	select {
	case err := <-done:
		t.Fatalf("1 つ目がロックを保持したまま 2 つ目が読めました: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if err := first.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("commit 後も 2 つ目がロックを取得できません: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("commit しても 2 つ目がロックを取得できません")
	}
	// 見つからない key は not found になる。存在しない行は lock を持たない。
	third, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer third.Rollback() //nolint:errcheck // 読み取りのみ
	if _, found, err := s.LoadOperationForUpdate(ctx, third, "absent"); err != nil || found {
		t.Fatalf("存在しない key = %t %v", found, err)
	}
}

// **終端状態は巻き戻さない。** 古い状態を見た再試行が completed / failed を上書き
// しても、assignment も desired_exp も終端のものが残る。
func TestStoreSetOperationStatusKeepsTerminalRows(t *testing.T) {
	_, s := newTestStore(t)
	ctx := context.Background()
	for _, tt := range []struct {
		key   string
		final string
	}{
		{"done", "completed"},
		{"broken", "failed"},
	} {
		if _, err := s.InsertOperation(ctx, operation{
			IdempotencyKey: tt.key, ActorID: "admin1", UserID: "u1", RoleID: "role1",
			Mode: "add", Operand: 1, Status: "pending",
		}); err != nil {
			t.Fatal(err)
		}
		desired := int64(50)
		if err := s.SetOperationStatus(ctx, s.db, tt.key, tt.final, "asg1", &desired, "boom"); err != nil {
			t.Fatal(err)
		}
		overwrite := int64(99)
		if err := s.SetOperationStatus(ctx, s.db, tt.key, "applying", "asg2", &overwrite, "retry"); err != nil {
			t.Fatal(err)
		}
		got, found, err := s.LoadOperation(ctx, tt.key)
		if err != nil || !found {
			t.Fatalf("%s load: %t %v", tt.key, found, err)
		}
		if got.Status != tt.final {
			t.Fatalf("%s: status = %q, want %q", tt.key, got.Status, tt.final)
		}
		if got.AssignmentID != "asg1" {
			t.Fatalf("%s: assignment_id = %q, want asg1", tt.key, got.AssignmentID)
		}
		if got.DesiredExp == nil || *got.DesiredExp != 50 {
			t.Fatalf("%s: desired_exp = %v, want 50", tt.key, got.DesiredExp)
		}
		if got.LastError != "boom" {
			t.Fatalf("%s: last_error = %q, want boom", tt.key, got.LastError)
		}
	}
}

// **行が無い key は黙って無視される。** UPDATE 0 行なので error にも row にもならない。
func TestStoreSetOperationStatusIgnoresMissingRow(t *testing.T) {
	_, s := newTestStore(t)
	ctx := context.Background()
	desired := int64(50)
	if err := s.SetOperationStatus(ctx, s.db, "absent", "completed", "asg1", &desired, ""); err != nil {
		t.Fatalf("存在しない key で error: %v", err)
	}
	if _, found, err := s.LoadOperation(ctx, "absent"); err != nil || found {
		t.Fatalf("存在しない key で row が作られました: %t %v", found, err)
	}
	// 既存行ならガードを通って更新できる。
	if _, err := s.InsertOperation(ctx, operation{
		IdempotencyKey: "key1", ActorID: "admin1", UserID: "u1", RoleID: "role1",
		Mode: "add", Operand: 1, Status: "pending",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOperationStatus(ctx, s.db, "key1", "completed", "asg1", &desired, ""); err != nil {
		t.Fatal(err)
	}
	got, found, err := s.LoadOperation(ctx, "key1")
	if err != nil || !found || got.Status != "completed" {
		t.Fatalf("key1 = %+v %t %v", got, found, err)
	}
}

func TestStoreOperationLifecycle(t *testing.T) {
	_, s := newTestStore(t)
	ctx := context.Background()

	op := operation{
		IdempotencyKey: "key1", ActorID: "admin1", UserID: "u1", RoleID: "role1",
		Mode: "add", Operand: 50.5, Status: "pending",
	}
	created, err := s.InsertOperation(ctx, op)
	if err != nil || !created {
		t.Fatalf("insert: created = %t %v", created, err)
	}
	created, err = s.InsertOperation(ctx, op)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("同じ idempotency key が2回挿入されました")
	}

	if err := s.SetOperationStatus(ctx, s.db, "key1", "assigning", "", nil, ""); err != nil {
		t.Fatal(err)
	}
	loaded, found, err := s.LoadOperation(ctx, "key1")
	if err != nil || !found {
		t.Fatalf("load: %t %v", found, err)
	}
	if loaded.Status != "assigning" {
		t.Fatalf("status = %q", loaded.Status)
	}

	resumable, err := s.ResumableOperations(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(resumable) != 1 {
		t.Fatalf("resumable = %d 件, want 1", len(resumable))
	}

	desired := int64(50)
	if err := s.SetOperationStatus(ctx, s.db, "key1", "completed", "asg1", &desired, ""); err != nil {
		t.Fatal(err)
	}
	loaded, _, err = s.LoadOperation(ctx, "key1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != "completed" || loaded.AssignmentID != "asg1" ||
		loaded.DesiredExp == nil || *loaded.DesiredExp != 50 {
		t.Fatalf("completed = %+v", loaded)
	}
	// **operand は有限の小数を往復する。** double precision なので 50.5 が 50 にならない。
	if loaded.Operand != 50.5 {
		t.Fatalf("operand = %v, want 50.5", loaded.Operand)
	}
	// completed は resumable に入らない。
	resumable, err = s.ResumableOperations(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(resumable) != 0 {
		t.Fatalf("completed が resumable に入りました: %d 件", len(resumable))
	}
}

func TestStoreAudit(t *testing.T) {
	_, s := newTestStore(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		entry := auditEntry{
			ActorID: "admin1", Operation: "change-exp", RoleID: "role1", UserID: "u1",
			AssignmentID: "asg1", Note: "note",
			Before: map[string]any{"experience": i * 10},
			After:  map[string]any{"experience": i*10 + 50},
		}
		if err := s.InsertAudit(ctx, s.db, entry); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.RecentAudit(ctx, "role1", "u1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("limit 2 で %d 件", len(got))
	}
	// 新しい順。
	if got[0].Before["experience"].(float64) != 20 {
		t.Fatalf("新しい順になっていません: %+v", got)
	}
	// role-only は userID を空にし、roleID だけで絞り込む。
	roleOnly, err := s.RecentAudit(ctx, "role1", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(roleOnly) != 3 {
		t.Fatalf("role-only の件数 = %d, want 3", len(roleOnly))
	}
	for _, entry := range roleOnly {
		if entry.RoleID != "role1" {
			t.Fatalf("role-only に別 role が混入: %+v", entry)
		}
	}
	// roleID と userID がともに空の監査検索は、全履歴を返すのではなく拒否する。
	if _, err := s.RecentAudit(ctx, "", "", 10); err == nil {
		t.Fatal("unscoped audit query was accepted")
	}
}

func TestStoreAuditNilStatesPersistAsSQLNull(t *testing.T) {
	db := testDBRequired(t)
	newHarness(t, db, nil).Routes(Plugin)
	s := &store{db: db}
	ctx := context.Background()
	if err := s.InsertAudit(ctx, db, auditEntry{ActorID: "admin1", Operation: "config-create"}); err != nil {
		t.Fatal(err)
	}
	var beforeNull, afterNull bool
	if err := db.QueryRowContext(ctx, `
		SELECT before_state IS NULL, after_state IS NULL
		FROM role_level_audit WHERE operation = 'config-create'
	`).Scan(&beforeNull, &afterNull); err != nil {
		t.Fatal(err)
	}
	if !beforeNull || !afterNull {
		t.Fatalf("nil audit states were not stored as SQL NULL: before=%t after=%t", beforeNull, afterNull)
	}
}

// **壊れた jsonb は黙って捨てない。** object でない値を入れると map は nil のまま
// になり、監査が「無い」ように見えるだけなので、どの列の壊れかを分かる形で返す。
func TestStoreRecentAuditRejectsNonObjectJSON(t *testing.T) {
	db, s := newTestStore(t)
	ctx := context.Background()
	for _, tt := range []struct {
		userID string
		column string
		value  string
		want   string
	}{
		{"u-array-before", "before_state", `[1, 2]`, "before"},
		{"u-string-after", "after_state", `"nope"`, "after"},
		{"u-number-before", "before_state", `7`, "before"},
	} {
		if _, err := db.Exec(`INSERT INTO role_level_audit
			(actor_id, operation, role_id, user_id, before_state, after_state)
			VALUES ('admin1', 'change-exp', 'role1', $1, '{}'::jsonb, '{}'::jsonb)`, tt.userID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE role_level_audit SET `+tt.column+` = $2::jsonb
			WHERE user_id = $1`, tt.userID, tt.value); err != nil {
			t.Fatal(err)
		}
		_, err := s.RecentAudit(ctx, "role1", tt.userID, 10)
		if err == nil {
			t.Fatalf("%s = %s でエラーになりません", tt.column, tt.value)
		}
		// どの行の、どの列の壊れかがメッセージから分かること。
		if !strings.Contains(err.Error(), tt.want) {
			t.Fatalf("error = %v, want %s を含むもの", err, tt.want)
		}
		if !strings.Contains(err.Error(), "change-exp") {
			t.Fatalf("error = %v, want operation を含むもの", err)
		}
	}
}

// **orphan 行は保持期間後まで消さない。** 監査のために残すのが目的。
//
// **保持期間の起点は orphaned_at。** `updated_at` を古くしただけでは消えない —
// XP が最後に書かれた時刻であって、orphan と判定された時刻ではない。判定されて
// いない行は、XP が古くても呼び出し側が削除候補に入れても残る。
func TestStoreDeleteOrphanExperienceRespectsRetention(t *testing.T) {
	db, s := newTestStore(t)
	ctx := context.Background()

	old := experienceRow{AssignmentID: "asg-old", RoleID: "role1", UserID: "u1", Experience: 10}
	fresh := experienceRow{AssignmentID: "asg-new", RoleID: "role1", UserID: "u2", Experience: 20}
	// never は orphan 判定を受けていない行。updated_at だけ 90 日前にしてある。
	never := experienceRow{AssignmentID: "asg-never", RoleID: "role1", UserID: "u3", Experience: 30}
	for _, row := range []experienceRow{old, fresh, never} {
		if err := s.SetExperience(ctx, s.db, row); err != nil {
			t.Fatal(err)
		}
	}
	// 実際に orphan 判定された行だけに印を付ける。判定は native 走査の保存で
	// 付くので、テストは MarkOrphaned を通して同じ形にする。
	if _, err := s.MarkOrphaned(ctx, s.db, "role1", []string{"asg-old", "asg-new"}); err != nil {
		t.Fatal(err)
	}
	for _, ts := range []struct {
		id   string
		days int
	}{{id: "asg-old", days: 90}, {id: "asg-new", days: 1}} {
		if _, err := db.Exec(`UPDATE role_level_experience
			SET orphaned_at = now() - make_interval(days => $2) WHERE assignment_id = $1`, ts.id, ts.days); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`UPDATE role_level_experience
		SET updated_at = now() - interval '90 days' WHERE assignment_id = 'asg-never'`); err != nil {
		t.Fatal(err)
	}

	// never も削除候補に入れて、orphaned_at 基準であることが検証できる形にする。
	n, err := s.DeleteOrphanExperience(ctx, "role1",
		[]string{"asg-old", "asg-new", "asg-never"}, time.Now().Add(-30*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("削除 = %d, want 1 (保持期間内の asg-new は残す)", n)
	}
	got, err := s.ExperienceForAssignments(ctx, []string{"asg-old", "asg-new", "asg-never"})
	if err != nil {
		t.Fatal(err)
	}
	if _, alive := got["asg-new"]; !alive {
		t.Fatal("保持期間内の行が消えています")
	}
	if _, alive := got["asg-old"]; alive {
		t.Fatal("保持期間を超えた行が残っています")
	}
	if _, alive := got["asg-never"]; !alive {
		t.Fatal("orphan 判定を受けていない行が updated_at を根拠に消えています")
	}
}

// **plugin table のエラーは stable code を連れて外に出る。** 素の error を返すと
// host が 500 に丸めるので、frontend が storage 起因と分からない。
func TestStoreWrapsErrors(t *testing.T) {
	db, s := newTestStore(t)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	svc := &service{log: discardLogger()}
	_, _, err := svc.loadConfigOrStorageError(context.Background(), s, "role1")
	if err == nil {
		t.Fatal("closed DB でエラーになりません")
	}
	se, code := extractCode(err)
	if code != CodeStorageFailed {
		t.Fatalf("code = %q, want %s (%v)", code, CodeStorageFailed, err)
	}
	if se == nil {
		t.Fatalf("status error がありません: %v", err)
	}
}

// assertExperienceIDs checks the exact contents and order of one page.
func assertExperienceIDs(t *testing.T, rows []experienceRow, want ...string) {
	t.Helper()
	got := make([]string, len(rows))
	for i, row := range rows {
		got[i] = row.AssignmentID
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("assignment ids = %v, want %v", got, want)
	}
}

// configIDs renders the role ids of a ListConfigs result in order.
func configIDs(cfgs []Config) string {
	ids := make([]string, len(cfgs))
	for i, cfg := range cfgs {
		ids[i] = cfg.RoleID
	}
	return fmt.Sprint(ids)
}

// extractCode pulls the coded status error out of an error chain the way the host
// does, so the test asserts what the client actually sees.
func extractCode(err error) (*plugin.StatusError, string) {
	return plugin.ExtractStatusError(err)
}

// discardLogger returns a logger that throws everything away, for the tests that
// only need a non-nil *slog.Logger.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
