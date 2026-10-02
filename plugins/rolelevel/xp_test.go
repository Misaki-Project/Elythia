package rolelevel

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/shiroha-a/mk/plugin"
	"github.com/shiroha-a/mk/plugin/plugintest"
)

// **計算は float64 で行い、結果を floor してから 0..9007199254740991 に収める。**
// multiplier の operand は **raw factor** で、1.5 は ×1.5 (百分率ではない)。
func TestApplyMode(t *testing.T) {
	for _, tt := range []struct {
		name    string
		mode    Mode
		current int64
		operand float64
		want    int64
	}{
		{"set は operand", ModeSet, 250, 40, 40},
		{"set は小数 operand を floor", ModeSet, 250, 40.9, 40},
		{"set は現在値無視", ModeSet, 250, -10, 0},
		{"add は加算", ModeAdd, 250, 40, 290},
		{"add は小数 operand を floor", ModeAdd, 250, 40.5, 290},
		{"add は負で引く", ModeAdd, 250, -40, 210},
		{"add は 0 から始める", ModeAdd, 0, 50, 50},
		{"multiplier は2倍", ModeMultiplier, 250, 2, 500},
		{"multiplier は1.5倍", ModeMultiplier, 250, 1.5, 375},
		{"multiplier は raw factor (0.5 は半分)", ModeMultiplier, 250, 0.5, 125},
		{"multiplier は0で消す", ModeMultiplier, 250, 0, 0},
		{"multiplier は0から0", ModeMultiplier, 0, 5, 0},
		{"負の値は 0 に丸める", ModeAdd, 0, -5, 0},
		{"大きすぎは上限", ModeAdd, MaxExperience, 100, MaxExperience},
		{"set は operand を丸める", ModeSet, 0, float64(MaxExperience) + 5000, MaxExperience},
		{"add の overflow は上限", ModeAdd, MaxExperience, math.MaxFloat64, MaxExperience},
		{"multiplier の正 overflow は上限", ModeMultiplier, MaxExperience, math.MaxFloat64, MaxExperience},
		{"multiplier の負 overflow は 0", ModeMultiplier, MaxExperience, -math.MaxFloat64, 0},
		{"multiplier の端数は切り捨て", ModeMultiplier, 101, 1.5, 151},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := applyMode(tt.mode, tt.current, tt.operand)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("= %d, want %d", got, tt.want)
			}
		})
	}
}

// **未定義の mode は保存時に落とす。** 黙って 0 にすると「XP を消した」ように見える。
func TestApplyModeRejectsUnknownMode(t *testing.T) {
	if _, err := applyMode("random", 0, 1); err == nil {
		t.Fatal("未知の mode を受理しています")
	}
}

// **非有限値の operand は保存時に落とす。** arithmetic overflow の ±Infinity は
// clamp するが、caller が直接渡した非有限 operand は計算前に拒否する。
func TestApplyModeRejectsNonFiniteOperand(t *testing.T) {
	if _, err := applyMode(ModeSet, 0, math.Inf(1)); err == nil {
		t.Fatal("Infinity を受けています")
	}
	if _, err := clampExperience(math.NaN()); err == nil {
		t.Fatal("NaN を整数に丸めています")
	}
}

// **operand は有限の小数を許す。** 整数に限らない (multiplier の 1.5 が要る)。
func TestValidateMode(t *testing.T) {
	for _, tt := range []struct {
		name    string
		mode    Mode
		operand float64
		code    string
	}{
		{"未知の mode", "random", 1, CodeUnknownMode},
		{"set は負の operand を許す", ModeSet, -100, ""},
		{"set は小数を許す", ModeSet, 1.5, ""},
		{"set は大きい有限値を許す", ModeSet, 1e300, ""},
		{"set は NaN を弾く", ModeSet, math.NaN(), CodeOperandOutOfRange},
		{"set は Infinity を弾く", ModeSet, math.Inf(-1), CodeOperandOutOfRange},
		{"add は小数を許す", ModeAdd, 0.25, ""},
		{"add は大きい有限値を許す", ModeAdd, 1e300, ""},
		{"multiplier は小数を許す", ModeMultiplier, 0.5, ""},
		{"multiplier は 1.5 を許す", ModeMultiplier, 1.5, ""},
		{"multiplier は 1000 を許す", ModeMultiplier, 1000, ""},
		// 負の factor は「XP を消す」効果だが、**拒否はしない** — 仕様は「有限の小数を
		// 受け付ける」で、例外を設けない。結果の clamp が 0 に落とす。
		{"multiplier は負を許す (結果は clamp で 0)", ModeMultiplier, -1, ""},
		{"multiplier は NaN を弾く", ModeMultiplier, math.NaN(), CodeOperandOutOfRange},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMode(tt.mode, tt.operand)
			if tt.code == "" {
				if err != nil {
					t.Fatalf("拒否しました: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("受理しています")
			}
			if _, code := extractCode(err); code != tt.code {
				t.Fatalf("code = %q, want %s (%v)", code, tt.code, err)
			}
		})
	}
}

// **idempotency key は必須。** 無いとクライアントが二重送信したときに 2 回適用され、
// reconciliation が「どの操作を再開するか」を決められない。
func TestValidateIdempotencyKey(t *testing.T) {
	if err := validateIdempotencyKey(""); err == nil {
		t.Fatal("空文字を受理しています")
	} else if _, code := extractCode(err); code != CodeIdempotencyKeyRequired {
		t.Fatalf("code = %q, want %s", code, CodeIdempotencyKeyRequired)
	}
	for _, bad := range []string{"a b", "with/slash", string(make([]byte, 65))} {
		if err := validateIdempotencyKey(bad); err == nil {
			t.Fatalf("%q を受理しています", bad)
		}
	}
	for _, ok := range []string{"a", "A-1_2", "0123456789abcdef"} {
		if err := validateIdempotencyKey(ok); err != nil {
			t.Fatalf("%q を拒否しました: %v", ok, err)
		}
	}
}

// **clamp は両端で効かせる。** 0 未満と MaxExperience 超を同じ「範囲外」に落とす。
func TestClampExperience(t *testing.T) {
	if got, err := clampExperience(-1); err != nil || got != 0 {
		t.Fatalf("= %d %v, want 0", got, err)
	}
	if got, err := clampExperience(math.MaxFloat64); err != nil || got != MaxExperience {
		t.Fatalf("= %d %v, want %d", got, err, MaxExperience)
	}
	if got, err := clampExperience(math.Inf(1)); err != nil || got != MaxExperience {
		t.Fatalf("+Infinity = %d %v, want %d", got, err, MaxExperience)
	}
	if got, err := clampExperience(math.Inf(-1)); err != nil || got != 0 {
		t.Fatalf("-Infinity = %d %v, want 0", got, err)
	}
	if got, err := clampExperience(42.9); err != nil || got != 42 {
		t.Fatalf("= %d %v, want 42", got, err)
	}
}

// recordingInvalidator records what the plugin asked the host to drop.
type recordingInvalidator struct {
	mu        sync.Mutex
	users     []string
	roles     []string
	roleError error
}

func (r *recordingInvalidator) InvalidateUser(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.users = append(r.users, id)
	return nil
}

func (r *recordingInvalidator) InvalidateRole(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.roles = append(r.roles, id)
	return r.roleError
}

func newXPService(t *testing.T, api plugin.API) (*service, *recordingInvalidator) {
	t.Helper()
	db := testDBRequired(t)
	h := plugintest.New(t).WithName("role-level").WithDB(db).
		WithConfig(map[string]any{"actorId": "admin1", "assignmentScanPages": 50}).WithAPI(api)
	// **Plugin の migration を先に適用する。** newService は migration を実行しないので、
	// Routes(Plugin) を経由しないと role_level_operation / role_level_experience /
	// role_level_audit が無く、状態機械の test が全部 "relation does not exist" になる。
	h.Routes(Plugin)
	svc, err := newService(h.Context())
	if err != nil {
		t.Fatal(err)
	}
	inv := &recordingInvalidator{}
	invalidatorHandle.set(inv)
	t.Cleanup(func() { invalidatorHandle.set(nil) })
	return svc, inv
}

func xpRequest(key, userID, roleID string, mode Mode, operand float64) ChangeExpRequest {
	return xpRequestAs("admin1", key, userID, roleID, mode, operand)
}

func xpRequestAs(actorID, key, userID, roleID string, mode Mode, operand float64) ChangeExpRequest {
	return ChangeExpRequest{
		IdempotencyKey: key, ActorID: actorID, UserID: userID, RoleID: roleID,
		Mode: mode, Operand: operand, Note: "test",
	}
}

// seedXP writes an experience row directly so the state machine starts from a known
// state without going through the API.
func seedXP(t *testing.T, s *service, assignmentID, roleID, userID string, xp int64) {
	t.Helper()
	err := s.store.SetExperience(context.Background(), s.store.db,
		experienceRow{AssignmentID: assignmentID, RoleID: roleID, UserID: userID, Experience: xp})
	if err != nil {
		t.Fatal(err)
	}
}

// **既存 assignment への変更は XP・audit・operation を1つの transaction で決める。**
// cache invalidation は **commit の後** だけ。
func TestChangeExpOnExistingAssignment(t *testing.T) {
	api := &stubAPI{
		roles:       map[string]roleInfo{"r1": {ID: "r1", Target: "manual"}},
		assignments: map[string][]assignment{"r1": {mkAssignment("asg1", "u1")}},
	}
	svc, inv := newXPService(t, api)
	seedXP(t, svc, "asg1", "r1", "u1", 250)

	res, err := svc.ChangeExp(context.Background(), xpRequest("k1", "u1", "r1", ModeAdd, 50))
	if err != nil {
		t.Fatal(err)
	}
	if res.AssignmentID != "asg1" || res.Experience != 300 || res.Status != StatusCompleted {
		t.Fatalf("= %+v", res)
	}
	if api.assignCallCount() != 0 {
		t.Fatal("既存の assignment があるのに admin/roles/assign を呼びました")
	}
	if res.Resumed {
		t.Fatal("新しい operation で Resumed = true")
	}
	if len(inv.users) != 1 || inv.users[0] != "u1" {
		t.Fatalf("user invalidation = %+v", inv.users)
	}
	if len(inv.roles) != 1 || inv.roles[0] != "r1" {
		t.Fatalf("role invalidation = %+v", inv.roles)
	}

	// audit に before / after が入る。
	entries, err := svc.store.RecentAudit(context.Background(), "r1", "u1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("audit = %d 件, want 1", len(entries))
	}
	if entries[0].Note != "test" || entries[0].ActorID != "admin1" || entries[0].Operation != "change-exp" {
		t.Fatalf("audit のnote/実行者/操作が一致しません: %+v", entries[0])
	}
	if entries[0].Before["experience"].(float64) != 250 || entries[0].After["experience"].(float64) != 300 {
		t.Fatalf("audit = %+v", entries[0])
	}
}

// **同じ idempotency key の再送は何もしない。** 2 回目は保存済みの結果を返すだけで
// XP は増えない。
func TestChangeExpIsIdempotent(t *testing.T) {
	api := &stubAPI{
		roles:       map[string]roleInfo{"r1": {ID: "r1", Target: "manual"}},
		assignments: map[string][]assignment{"r1": {mkAssignment("asg1", "u1")}},
	}
	svc, _ := newXPService(t, api)
	seedXP(t, svc, "asg1", "r1", "u1", 100)

	first, err := svc.ChangeExp(context.Background(), xpRequest("k1", "u1", "r1", ModeAdd, 50))
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.ChangeExp(context.Background(), xpRequest("k1", "u1", "r1", ModeAdd, 50))
	if err != nil {
		t.Fatal(err)
	}
	if first.Experience != 150 || second.Experience != 150 {
		t.Fatalf("1回目 %d / 2回目 %d, want 150 / 150", first.Experience, second.Experience)
	}
	if first.Resumed {
		t.Fatal("新しい operation で Resumed = true")
	}
	if second.Resumed == false {
		t.Fatal("既存 operation の再送で Resumed = false")
	}
	xp, err := svc.experienceFor(context.Background(), "r1", "u1", "asg1")
	if err != nil {
		t.Fatal(err)
	}
	if xp != 150 {
		t.Fatalf("XP = %d, want 150", xp)
	}
	entries, err := svc.store.RecentAudit(context.Background(), "r1", "u1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("audit = %d 件, want 1 (再送で audit が増えない)", len(entries))
	}
}

type barrierAPI struct {
	plugin.API
	endpoint string
	started  chan<- struct{}
	release  <-chan struct{}
}

func (a *barrierAPI) Anonymous() plugin.Caller {
	return &barrierCaller{Caller: a.API.Anonymous(), endpoint: a.endpoint, started: a.started, release: a.release}
}

func (a *barrierAPI) AsUser(userID string) plugin.Caller {
	return &barrierCaller{Caller: a.API.AsUser(userID), endpoint: a.endpoint, started: a.started, release: a.release}
}

type barrierCaller struct {
	plugin.Caller
	endpoint string
	started  chan<- struct{}
	release  <-chan struct{}
}

func (c *barrierCaller) Call(ctx context.Context, endpoint string, params any) (json.RawMessage, error) {
	if endpoint == c.endpoint {
		select {
		case c.started <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		select {
		case <-c.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return c.Caller.Call(ctx, endpoint, params)
}

func testConcurrentChanges(t *testing.T, keys []string, wantStarted int, wantXP int64, wantAudits int) {
	t.Helper()
	testConcurrentChangesWithPool(t, 0, keys, wantStarted, wantXP, wantAudits)
}

// testConcurrentChangesWithPool is testConcurrentChanges with a bounded pool.
//
// **maxOpenConns > 0 のときだけ pool を絞る。** 「lock 用の専用接続が pool を全部
// 埋めて、plugin 自身の書き込みが接続を待つ」状況を再現するため。
func testConcurrentChangesWithPool(t *testing.T, maxOpenConns int, keys []string,
	wantStarted int, wantXP int64, wantAudits int,
) {
	t.Helper()
	base := &stubAPI{
		roles:       map[string]roleInfo{"r1": {ID: "r1", Target: "manual"}},
		assignments: map[string][]assignment{"r1": {mkAssignment("asg1", "u1")}},
	}
	started := make(chan struct{}, len(keys))
	release := make(chan struct{})
	svc, _ := newXPService(t, &barrierAPI{API: base, endpoint: "admin/roles/users", started: started, release: release})
	if maxOpenConns > 0 {
		svc.store.db.SetMaxOpenConns(maxOpenConns)
	}
	seedXP(t, svc, "asg1", "r1", "u1", 100)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	released := false
	var wg sync.WaitGroup
	defer func() {
		if !released {
			close(release)
		}
		cancel()
		wg.Wait()
	}()

	type outcome struct {
		result ChangeExpResult
		err    error
	}
	outcomes := make(chan outcome, len(keys))
	for _, key := range keys {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			result, err := svc.ChangeExp(ctx, xpRequest(key, "u1", "r1", ModeAdd, 50))
			outcomes <- outcome{result: result, err: err}
		}(key)
	}
	for range make([]struct{}, wantStarted) {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatalf("want %d native calls at barrier, got fewer: %v", wantStarted, ctx.Err())
		}
	}
	close(release)
	released = true
	got := make([]outcome, 0, len(keys))
	for range keys {
		select {
		case result := <-outcomes:
			got = append(got, result)
		case <-ctx.Done():
			t.Fatalf("concurrent calls did not finish: %v", ctx.Err())
		}
	}
	for _, result := range got {
		if result.err != nil {
			t.Fatalf("concurrent result = %+v, err = %v", result.result, result.err)
		}
	}
	if len(keys) == 2 && keys[0] == keys[1] {
		resumed := 0
		for _, result := range got {
			if result.result.Resumed {
				resumed++
			}
		}
		// `resumed == 1` は「`Resumed=false` がちょうど 1 回」を意味する。
		// lock を取った順番ではなく `Resumed: !created` だけで決まるので、
		// creator が lock を 2 番目に取ってもこの値は変わらない。
		if resumed != 1 {
			t.Fatalf("same-key Resumed count = %d, want 1", resumed)
		}
	}
	xp, err := svc.experienceFor(context.Background(), "r1", "u1", "asg1")
	if err != nil || xp != wantXP {
		t.Fatalf("XP = %d, err = %v, want %d", xp, err, wantXP)
	}
	entries, err := svc.store.RecentAudit(context.Background(), "r1", "u1", 10)
	if err != nil || len(entries) != wantAudits {
		t.Fatalf("audit = %d, err = %v, want %d", len(entries), err, wantAudits)
	}
}

// **同じ key の route retry と reconciliation が同時でも1回だけ適用する。**
// session advisory lock が second path を serialize するため、native call に到達するのは
// first path だけ。second path は lock 後に completed と保存済み結果を読み、audit を増やさない。
// `Resumed=false` がちょうど 1 回返るのは、**lock を取った順ではなく `Resumed: !created`** による。
// `created` は `InsertOperation` の戻り値として lock 取得前に確定するので、Go が second
// goroutine に先に lock を持たせても creator は `Resumed=false` を返す。
func TestChangeExpConcurrentSameKeyAppliesOnce(t *testing.T) {
	testConcurrentChanges(t, []string{"same-key", "same-key"}, 1, 150, 1)
}

// **key が違っても同じ assignment の更新は直列化する。** 100 + 50 + 50 = 200。
func TestChangeExpConcurrentDifferentKeysDoNotLoseUpdate(t *testing.T) {
	testConcurrentChanges(t, []string{"key-1", "key-2"}, 2, 200, 2)
}

// **lock 用の専用接続が pool を埋めたら、plugin 自身の書き込みを待たせない。**
//
// 異なる key 4 本を `MaxOpenConns=4` で走らせ、4 本すべてが `admin/roles/users` の
// barrier まで到達することを要求する。barrier に到達した時点で **4 本が同時に
// advisory lock 用接続を掴んでいる**ので、pool には 1 本も残っていない。もし
// reload / status write / 最終 transaction が pool (`*sql.DB`) に出る実装のままだと、
// 4 本目は「接続が返るまで待つ → 返す接続は 4 本すべてが握っている」で **永久に
// 進まず**、barrier 到達を待つ側だけが ctx timeout で落ちる。
//
// 契約は 1 つ: **lock を掴んだ接続以外へ plugin の SQL を出さない。**
// 検証は 2 段: 4 本全部が barrier に到達すること (deadlock しないこと) と、
// barrier 解放後に 100 + 50 × 4 = 300 / audit 4 件まで進むこと。
func TestChangeExpConcurrentKeysDoNotStarveWhenLockPoolIsFull(t *testing.T) {
	testConcurrentChangesWithPool(t, 4,
		[]string{"starve-1", "starve-2", "starve-3", "starve-4"}, 4, 300, 4)
}

// **terminal failure 後に同じ key の retry は native side effect を起こさない。**
//
// **first / second は barrier で決定的に固定する。** first path が
// `admin/roles/assign` の barrier に到達したことを確認してから second path を起動するので、
// その時点で first path が operation の session advisory lock を保持していることは確定している。
// second path はその lock を取れず barrier にも届かない。sched や goroutine の起動順に依存しない。
//
// first path は native の拒否をそのまま返すので `CodeNativeAPIFailed` (502)。
// second path は lock 解放後の reloaded が `failed` なので 409 + `CodeOperationFailed`。
func TestChangeExpSerializesSameKeyAfterTerminalFailure(t *testing.T) {
	base := &stubAPI{
		roles:              map[string]roleInfo{"r1": {ID: "r1", Target: "manual"}},
		assignments:        map[string][]assignment{},
		assignDeniedActors: map[string]int{"moderator": http.StatusForbidden},
	}
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	api := &barrierAPI{API: base, endpoint: "admin/roles/assign", started: started, release: release}
	svc, _ := newXPService(t, api)
	if _, err := svc.store.InsertOperation(context.Background(), operation{
		IdempotencyKey: "race-key", ActorID: "moderator", UserID: "u1", RoleID: "r1",
		Mode: string(ModeAdd), Operand: 10, Status: string(StatusAssigning),
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	type outcome struct {
		path string
		err  error
	}
	results := make(chan outcome, 2)
	released := false
	defer func() {
		if !released {
			close(release)
		}
		wg.Wait()
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := svc.ChangeExp(ctx, xpRequestAs("moderator", "race-key", "u1", "r1", ModeAdd, 10))
		results <- outcome{path: "first", err: err}
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("first path did not reach assign barrier")
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := svc.ChangeExp(ctx, xpRequestAs("moderator", "race-key", "u1", "r1", ModeAdd, 10))
		results <- outcome{path: "second", err: err}
	}()
	close(release)
	released = true
	outcomes := make(map[string]error, 2)
	for range []int{0, 1} {
		got := <-results
		if got.err == nil {
			t.Fatalf("%s path must fail, err = nil", got.path)
		}
		outcomes[got.path] = got.err
	}
	// first path: native が拒否されるので 502 + CodeNativeAPIFailed をそのまま返す。
	se, code := extractCode(outcomes["first"])
	if se == nil || code != CodeNativeAPIFailed {
		t.Fatalf("first path: err = %v, code = %q, want %s", outcomes["first"], code, CodeNativeAPIFailed)
	}
	if se.Status != http.StatusBadGateway {
		t.Fatalf("first path: status = %d, want %d", se.Status, http.StatusBadGateway)
	}
	// second path: lock を待っている間に terminal になったので 409 + CodeOperationFailed。
	se, code = extractCode(outcomes["second"])
	if se == nil || code != CodeOperationFailed {
		t.Fatalf("second path: err = %v, code = %q, want %s", outcomes["second"], code, CodeOperationFailed)
	}
	if se.Status != http.StatusConflict {
		t.Fatalf("second path: status = %d, want %d", se.Status, http.StatusConflict)
	}
	// **terminal failure 後は native side effect を起こさない。** second path は assign に
	// 到達しないので、記録される actor も 1 件だけ。
	if got := base.assignCallCount(); got != 1 {
		t.Fatalf("assign calls = %d, want 1", got)
	}
	actors := base.assignCallActors()
	if len(actors) != 1 || actors[0] != "moderator" {
		t.Fatalf("assign actors = %v, want [moderator]", actors)
	}
	op, found, err := svc.store.LoadOperation(context.Background(), "race-key")
	if err != nil || !found || op.Status != string(StatusFailed) || op.LastError == "" {
		t.Fatalf("operation = %+v, found=%t, err=%v", op, found, err)
	}
	entries, err := svc.store.RecentAudit(context.Background(), "r1", "u1", 10)
	if err != nil || len(entries) != 0 {
		t.Fatalf("audit = %d, err = %v", len(entries), err)
	}
	rows, err := svc.store.ExperienceRowsForRoleUser(context.Background(), "r1", "u1")
	if err != nil || len(rows) != 0 {
		t.Fatalf("experience rows = %d, err = %v", len(rows), err)
	}
}

// **未assignment user は自動付与してから XP が入る。** 順序は spec の 6 ステップ。
func TestChangeExpAutoAssigns(t *testing.T) {
	api := &stubAPI{
		roles:       map[string]roleInfo{"r1": {ID: "r1", Target: "manual"}},
		assignments: map[string][]assignment{},
	}
	svc, _ := newXPService(t, api)

	res, err := svc.ChangeExp(context.Background(), xpRequest("k1", "u1", "r1", ModeAdd, 50))
	if err != nil {
		t.Fatal(err)
	}
	if res.AssignmentID == "" || res.Experience != 50 {
		t.Fatalf("= %+v, want assignmentID / experience 50", res)
	}
	if api.assignCallCount() != 1 {
		t.Fatalf("admin/roles/assign = %d 回", api.assignCallCount())
	}
	rows, err := svc.store.ExperienceRowsForRoleUser(context.Background(), "r1", "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].AssignmentID != res.AssignmentID || rows[0].Experience != 50 {
		t.Fatalf("XP 行 = %+v", rows)
	}
}

// **operation の actor を native assign にそのまま渡す。** 設定済み actor ではなく、
// 永続化する要求者の権限で assign するので、moderator の操作は moderator として記録・判定される。
func TestChangeExpAssignsAsPersistedModerator(t *testing.T) {
	api := &stubAPI{
		roles:              map[string]roleInfo{"r1": {ID: "r1", Target: "manual"}},
		assignments:        map[string][]assignment{},
		assignDeniedActors: map[string]int{},
	}
	svc, _ := newXPService(t, api)

	if _, err := svc.ChangeExp(context.Background(),
		xpRequestAs("moderator", "moderator-key", "u1", "r1", ModeAdd, 10)); err != nil {
		t.Fatal(err)
	}
	actors := api.assignCallActors()
	if len(actors) != 1 || actors[0] != "moderator" {
		t.Fatalf("assign actors = %v, want [moderator]", actors)
	}
}

// native 側の拒否は operation を成功扱いにせず、failed として保存する。
func TestChangeExpNativeDenialFailsOperation(t *testing.T) {
	api := &stubAPI{
		roles:              map[string]roleInfo{"r1": {ID: "r1", Target: "manual"}},
		assignments:        map[string][]assignment{},
		assignDeniedActors: map[string]int{"moderator": http.StatusForbidden},
	}
	svc, _ := newXPService(t, api)

	if _, err := svc.ChangeExp(context.Background(),
		xpRequestAs("moderator", "denied-key", "u1", "r1", ModeAdd, 10)); err == nil {
		t.Fatal("native の拒否を成功として扱いました")
	}
	op, found, err := svc.store.LoadOperation(context.Background(), "denied-key")
	if err != nil || !found {
		t.Fatalf("found = %t %v", found, err)
	}
	if op.Status != string(StatusFailed) {
		t.Fatalf("status = %q, want failed", op.Status)
	}
}

// actor が空なら AsUser("") を呼ばず、operation を失敗として記録する。
func TestChangeExpRejectsEmptyPersistedActor(t *testing.T) {
	api := &stubAPI{
		roles:       map[string]roleInfo{"r1": {ID: "r1", Target: "manual"}},
		assignments: map[string][]assignment{},
	}
	svc, _ := newXPService(t, api)

	if _, err := svc.ChangeExp(context.Background(),
		xpRequestAs("", "empty-actor-key", "u1", "r1", ModeAdd, 10)); err == nil {
		t.Fatal("空の actor を受け入れました")
	}
	if got := api.assignCallCount(); got != 0 {
		t.Fatalf("empty actor で assign を呼びました: %d", got)
	}
}

// administrator actor は native の assign を通過できる。
func TestChangeExpAssignsAsAdministrator(t *testing.T) {
	api := &stubAPI{
		roles:              map[string]roleInfo{"r1": {ID: "r1", Target: "manual"}},
		assignments:        map[string][]assignment{},
		assignDeniedActors: map[string]int{"moderator": http.StatusForbidden},
	}
	svc, _ := newXPService(t, api)

	if _, err := svc.ChangeExp(context.Background(),
		xpRequestAs("administrator", "admin-key", "u1", "r1", ModeAdd, 10)); err != nil {
		t.Fatal(err)
	}
	actors := api.assignCallActors()
	if len(actors) != 1 || actors[0] != "administrator" {
		t.Fatalf("assign actors = %v, want [administrator]", actors)
	}
}

// **未assignment user で multiplier なら初期 XP は 0。** operand が raw factor でも
// 0 × anything は 0 なので同じ。
func TestChangeExpMultiplierInitialIsZero(t *testing.T) {
	api := &stubAPI{
		roles:       map[string]roleInfo{"r1": {ID: "r1", Target: "manual"}},
		assignments: map[string][]assignment{},
	}
	svc, _ := newXPService(t, api)
	res, err := svc.ChangeExp(context.Background(), xpRequest("k1", "u1", "r1", ModeMultiplier, 1.5))
	if err != nil {
		t.Fatal(err)
	}
	if res.Experience != 0 {
		t.Fatalf("= %d, want 0", res.Experience)
	}
}

// **multiplier は raw factor で、小数を許す。** 250 × 1.5 = 375。
func TestChangeExpMultiplierUsesDecimalFactor(t *testing.T) {
	api := &stubAPI{
		roles:       map[string]roleInfo{"r1": {ID: "r1", Target: "manual"}},
		assignments: map[string][]assignment{"r1": {mkAssignment("asg1", "u1")}},
	}
	svc, _ := newXPService(t, api)
	seedXP(t, svc, "asg1", "r1", "u1", 250)

	res, err := svc.ChangeExp(context.Background(), xpRequest("k1", "u1", "r1", ModeMultiplier, 1.5))
	if err != nil {
		t.Fatal(err)
	}
	if res.Experience != 375 {
		t.Fatalf("= %d, want 375 (250 × 1.5)", res.Experience)
	}
}

// **unassign/reassign で古い XP が復活しない。** plugin table を信用せず、毎回
// native から live な assignment を引き直す。
func TestChangeExpDoesNotResurrectUnassignedExperience(t *testing.T) {
	api := &stubAPI{
		roles: map[string]roleInfo{"r1": {ID: "r1", Target: "manual"}},
		assignments: map[string][]assignment{
			"r1": {mkAssignment("asg-new", "u1")},
		},
	}
	svc, _ := newXPService(t, api)
	seedXP(t, svc, "asg-old", "r1", "u1", 9999)

	res, err := svc.ChangeExp(context.Background(), xpRequest("k1", "u1", "r1", ModeAdd, 10))
	if err != nil {
		t.Fatal(err)
	}
	if res.AssignmentID != "asg-new" {
		t.Fatalf("assignmentID = %q, want asg-new", res.AssignmentID)
	}
	if res.Experience != 10 {
		t.Fatalf("experience = %d, want 10 (古い 9999 を引き継いではいけない)", res.Experience)
	}
	// 古い行は残る (監査用) が、live な行の XP は 0 から始まる。
	rows, err := svc.store.ExperienceRowsForRoleUser(context.Background(), "r1", "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("XP 行 = %d 件, want 2 (古い行は監査用に残る)", len(rows))
	}
}

// **途中で停止した operation は同じ key で再開できる。** ここでは `assigning` で
// 止まった行を直接書いて、reconciliation job と同じ resume 経路で完了させる。
func TestChangeExpResumesStuckOperation(t *testing.T) {
	api := &stubAPI{
		roles:       map[string]roleInfo{"r1": {ID: "r1", Target: "manual"}},
		assignments: map[string][]assignment{"r1": {mkAssignment("asg1", "u1")}},
	}
	svc, _ := newXPService(t, api)

	if _, err := svc.store.InsertOperation(context.Background(), operation{
		IdempotencyKey: "k1", ActorID: "moderator", UserID: "u1", RoleID: "r1",
		Mode: string(ModeAdd), Operand: 40, Status: string(StatusAssigning),
	}); err != nil {
		t.Fatal(err)
	}

	res, err := svc.ChangeExp(context.Background(),
		xpRequestAs("administrator", "k1", "u1", "r1", ModeAdd, 40))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Resumed {
		t.Fatal("Resumed = false, want true")
	}
	if res.Experience != 40 || res.Status != StatusCompleted {
		t.Fatalf("= %+v", res)
	}
	actors := api.assignCallActors()
	if len(actors) != 0 {
		t.Fatalf("native に assignment があるのに assign を呼びました: %d", len(actors))
	}
	op, found, err := svc.store.LoadOperation(context.Background(), "k1")
	if err != nil || !found || op.DesiredExp == nil || *op.DesiredExp != 40 || op.Status != string(StatusCompleted) {
		t.Fatalf("persisted operation = %+v, found=%t, err=%v", op, found, err)
	}
}

// retry/reconciliation は再送時の request actor や設定 actor ではなく、operation.ActorID を使う。
func TestChangeExpRetryUsesPersistedOperationActor(t *testing.T) {
	api := &stubAPI{
		roles:       map[string]roleInfo{"r1": {ID: "r1", Target: "manual"}},
		assignments: map[string][]assignment{},
	}
	svc, _ := newXPService(t, api)
	if _, err := svc.store.InsertOperation(context.Background(), operation{
		IdempotencyKey: "persisted-actor-key", ActorID: "moderator", UserID: "u1", RoleID: "r1",
		Mode: string(ModeAdd), Operand: 40, Status: string(StatusAssigning),
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.ChangeExp(context.Background(),
		xpRequestAs("administrator", "persisted-actor-key", "u1", "r1", ModeAdd, 40)); err != nil {
		t.Fatal(err)
	}
	actors := api.assignCallActors()
	if len(actors) != 1 || actors[0] != "moderator" {
		t.Fatalf("retry assign actors = %v, want [moderator]", actors)
	}
}

// **native assign が失敗したら operation は failed になって止まる。** 再試行のたびに
// 勝手にやり直さない。`failed` は終端で reconciliation job の再開対象にならず、
// 原因を調べてから新しい idempotency key で再試行する。
func TestChangeExpFailsOnNativeError(t *testing.T) {
	api := &stubAPI{
		roles:        map[string]roleInfo{"r1": {ID: "r1", Target: "manual"}},
		assignments:  map[string][]assignment{},
		assignStatus: http.StatusInternalServerError,
	}
	svc, _ := newXPService(t, api)

	if _, err := svc.ChangeExp(context.Background(), xpRequest("k1", "u1", "r1", ModeAdd, 10)); err == nil {
		t.Fatal("native 失敗を隠しています")
	}
	op, found, err := svc.store.LoadOperation(context.Background(), "k1")
	if err != nil || !found {
		t.Fatalf("found = %t %v", found, err)
	}
	if op.Status != string(StatusFailed) {
		t.Fatalf("status = %q, want failed", op.Status)
	}
	if op.LastError == "" {
		t.Fatal("last_error が空です (原因が残らない)")
	}
}

// **XP 更新後に stale cache が復活しない。** invalidation は commit の後なので、
// transaction が失敗したら invalidate されない。
func TestChangeExpDoesNotInvalidateOnStorageFailure(t *testing.T) {
	api := &stubAPI{
		roles:       map[string]roleInfo{"r1": {ID: "r1", Target: "manual"}},
		assignments: map[string][]assignment{"r1": {mkAssignment("asg1", "u1")}},
	}
	svc, inv := newXPService(t, api)
	seedXP(t, svc, "asg1", "r1", "u1", 10)
	// 1行だけへの更新を失敗させる: role_id を別の値に書き換える (SetExperience の
	// ON CONFLICT ... WHERE が RowsAffected 0 になる状態)。
	_, err := svc.store.db.Exec(`UPDATE role_level_experience SET role_id = 'other' WHERE assignment_id = 'asg1'`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ChangeExp(context.Background(), xpRequest("k1", "u1", "r1", ModeAdd, 10)); err == nil {
		t.Fatal("storage failure で成功しています")
	}
	var experience int64
	if err := svc.store.db.QueryRowContext(context.Background(),
		"SELECT experience FROM role_level_experience WHERE assignment_id = 'asg1'").Scan(&experience); err != nil {
		t.Fatal(err)
	}
	if experience != 10 {
		t.Fatalf("XP = %d, want unchanged 10", experience)
	}
	entries, err := svc.store.RecentAudit(context.Background(), "r1", "u1", 10)
	if err != nil || len(entries) != 0 {
		t.Fatalf("audit = %d, err = %v, want 0", len(entries), err)
	}
	op, found, err := svc.store.LoadOperation(context.Background(), "k1")
	if err != nil || !found || op.Status != string(StatusFailed) || op.LastError == "" {
		t.Fatalf("failed operation = %+v, found=%t, err=%v", op, found, err)
	}
	// transaction は rollback され、operation の failure write は transaction 外なので残る。
	if len(inv.users) != 0 || len(inv.roles) != 0 {
		t.Fatalf("commit 前に invalidate しました: users=%+v roles=%+v", inv.users, inv.roles)
	}
}

// retry の payload mismatch は、stored terminal result より先に conflict を返す。
//
// **比較は payload check の直後、lock 取得より前に走る。** つまりこの 409 は lock を待つ
// 前に決まる。**payload identity に入るのは 4 フィールドだけ**（UserID / RoleID / Mode /
// Operand）で、この表がその 4 つを 1 つずつ実際に食い違わせて検証する。
func TestChangeExpRejectsMismatchedIdempotencyPayload(t *testing.T) {
	// **値コピーだと変更が消える。** `func(ChangeExpRequest)` は値を受け取るので
	// `r.UserID = "other"` は捨てられ、4 case とも未変更の要求を投げてしまい、
	// payload mismatch を一度も検証しないまま green になる。
	// `func(*ChangeExpRequest)` + `mutate(&req)` にして、UserID / RoleID / Mode / Operand が
	// 本当に食い違うようにする。
	fields := []struct {
		name   string
		mutate func(*ChangeExpRequest)
	}{
		{"UserID", func(r *ChangeExpRequest) { r.UserID = "other" }},
		{"RoleID", func(r *ChangeExpRequest) { r.RoleID = "other" }},
		{"Mode", func(r *ChangeExpRequest) { r.Mode = ModeSet }},
		{"Operand", func(r *ChangeExpRequest) { r.Operand = 11 }},
	}
	for _, field := range fields {
		t.Run(field.name, func(t *testing.T) {
			api := &stubAPI{roles: map[string]roleInfo{"r1": {ID: "r1", Target: "manual"}},
				assignments: map[string][]assignment{"r1": {mkAssignment("asg1", "u1")}}}
			svc, _ := newXPService(t, api)
			if _, err := svc.ChangeExp(context.Background(), xpRequest("mismatch", "u1", "r1", ModeAdd, 10)); err != nil {
				t.Fatal(err)
			}
			req := xpRequest("mismatch", "u1", "r1", ModeAdd, 10)
			field.mutate(&req)
			_, err := svc.ChangeExp(context.Background(), req)
			se, code := extractCode(err)
			if se == nil || code != CodeIdempotencyConflict {
				t.Fatalf("code = %q, err=%v", code, err)
			}
			if se.Status != http.StatusConflict {
				t.Fatalf("status = %d, want %d", se.Status, http.StatusConflict)
			}
		})
	}
	// ActorID と Note は payload identity の対象外なので、異なる再送者と note は受理する。
	api := &stubAPI{roles: map[string]roleInfo{"r1": {ID: "r1", Target: "manual"}},
		assignments: map[string][]assignment{"r1": {mkAssignment("asg1", "u1")}}}
	svc, _ := newXPService(t, api)
	if _, err := svc.ChangeExp(context.Background(), xpRequest("control", "u1", "r1", ModeAdd, 10)); err != nil {
		t.Fatal(err)
	}
	retry := xpRequestAs("different-actor", "control", "u1", "r1", ModeAdd, 10)
	retry.Note = "different-note"
	if _, err := svc.ChangeExp(context.Background(), retry); err != nil {
		t.Fatalf("actor/note mismatch rejected: %v", err)
	}
}

// **失敗済み operation の再送は 409 + CodeOperationFailed。** 保存済みの last_error が
// そのまま利用者に返るので、原因が追える。
func TestChangeExpRetryOfFailedOperationReturnsOperationFailed(t *testing.T) {
	api := &stubAPI{roles: map[string]roleInfo{"r1": {ID: "r1", Target: "manual"}}}
	svc, _ := newXPService(t, api)
	// **terminal 行は SetOperationStatus で作る。** pending から failed へ移すのが状態機械の
	// 実際の遷移で、`status NOT IN ('completed','failed')` のガードもその形でのみ効く。
	// `Status: failed` を直接 INSERT した fixture は guard を通らない。
	if _, err := svc.store.InsertOperation(context.Background(), operation{
		IdempotencyKey: "failed-key", ActorID: "moderator", UserID: "u1", RoleID: "r1",
		Mode: string(ModeAdd), Operand: 10, Status: string(StatusPending),
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.store.SetOperationStatus(context.Background(), svc.store.db,
		"failed-key", string(StatusFailed), "", nil, "native 500"); err != nil {
		t.Fatal(err)
	}
	_, err := svc.ChangeExp(context.Background(),
		xpRequestAs("administrator", "failed-key", "u1", "r1", ModeAdd, 10))
	se, code := extractCode(err)
	if se == nil || code != CodeOperationFailed {
		t.Fatalf("err = %v, code = %q", err, code)
	}
	if se.Status != http.StatusConflict {
		t.Fatalf("status = %d, want %d", se.Status, http.StatusConflict)
	}
	if !strings.Contains(err.Error(), "native 500") {
		t.Fatalf("err = %v, last_error が保存されていない", err)
	}
}

// **保存済み status が壊れていても再開させない。** `role_level_operation.status` は
// `text NOT NULL` で CHECK が無いので、`pending` / `assigning` / `applying` の 3 つ以外の
// 値が入りうる。「`completed` / `failed` 以外なら再開可能」と判定すると、
// **誰がどこまで進めたか判断できない操作**に対して native assign を呼び、XP を書く。
//
// ここでは 5 定数のどれでもない値 (`corrupt`) の行を直接 INSERT し、次を検証する。
// 未知 status は resumable ではなく、**native を呼ぶ前に** 409 + `CodeOperationFailed` で
// 拒否されること。operation が `failed` と、原因を追える `last_error` 付きで記録されること。
// XP 行も監査行も 1 件も増えないこと。
func TestChangeExpRejectsUnknownStoredOperationStatus(t *testing.T) {
	// role は定義するが assignment は無い。**続けてしまえば `admin/roles/assign` に
	// 届く**状況にしておけば、「拒否が native より前か」を副作用で観測できる。
	api := &stubAPI{
		roles:       map[string]roleInfo{"r1": {ID: "r1", Target: "manual"}},
		assignments: map[string][]assignment{},
	}
	svc, _ := newXPService(t, api)
	// **壊れた行は直接 INSERT する。** `SetOperationStatus` の
	// `status NOT IN ('completed','failed')` ガードは state machine の遷移だけを許すので、
	// 実際にそんな値が入りうるのは直接書き込み (手動 SQL / 壊れた migration) の場合だけ。
	// immutable payload (userId / roleId / mode / operand) は再送と **一致**させ、
	// `ensureSameOperation` を通って `resume` の判定まで到達させる。
	if _, err := svc.store.InsertOperation(context.Background(), operation{
		IdempotencyKey: "corrupt-status-key", ActorID: "moderator", UserID: "u1", RoleID: "r1",
		Mode: string(ModeAdd), Operand: 10, Status: "corrupt",
	}); err != nil {
		t.Fatal(err)
	}

	// **再送の ActorID は永続化分と違える。** ActorID は payload identity に含まれないので、
	// この conflict が actor 差ではなく未知 status の拒否であることが分かる。
	_, changeErr := svc.ChangeExp(context.Background(),
		xpRequestAs("administrator", "corrupt-status-key", "u1", "r1", ModeAdd, 10))
	se, code := extractCode(changeErr)
	if se == nil || code != CodeOperationFailed {
		t.Fatalf("err = %v, code = %q, want %s", changeErr, code, CodeOperationFailed)
	}
	if se.Status != http.StatusConflict {
		t.Fatalf("status = %d, want %d", se.Status, http.StatusConflict)
	}
	// **副作用は無い。** assignment が無いので、ここを通過してしまえば
	// `admin/roles/assign` を呼び、XP 行と監査行を書く。
	if got := api.assignCallCount(); got != 0 {
		t.Fatalf("admin/roles/assign = %d 回, want 0", got)
	}
	if actors := api.assignCallActors(); len(actors) != 0 {
		t.Fatalf("assign actors = %v, want 空", actors)
	}
	// **診断できる形で `failed` に落とす。** 壊れた status の値そのものを last_error に残す。
	// `fail` は `context.WithoutCancel` を使うので、request が破棄済みでも記録は残る。
	op, found, loadErr := svc.store.LoadOperation(context.Background(), "corrupt-status-key")
	if loadErr != nil || !found {
		t.Fatalf("found = %t, err = %v", found, loadErr)
	}
	if op.Status != string(StatusFailed) {
		t.Fatalf("status = %q, want %q", op.Status, StatusFailed)
	}
	if !strings.Contains(op.LastError, "corrupt") {
		t.Fatalf("last_error = %q, want 壊れた status を含む", op.LastError)
	}
	if !strings.Contains(changeErr.Error(), "corrupt") {
		t.Fatalf("err = %v, 壊れた status が利用者に伝わらない", changeErr)
	}
	// **XP も監査も書かれていない。** これが「native を呼ぶ前に拒否した」ことの
	// 最も強い証拠になる (行が無いので、万が一 assign を介しても適用されない)。
	rows, rowsErr := svc.store.ExperienceRowsForRoleUser(context.Background(), "r1", "u1")
	if rowsErr != nil {
		t.Fatal(rowsErr)
	}
	if len(rows) != 0 {
		t.Fatalf("XP 行 = %d 件, want 0 (%+v)", len(rows), rows)
	}
	entries, auditErr := svc.store.RecentAudit(context.Background(), "r1", "u1", 10)
	if auditErr != nil {
		t.Fatal(auditErr)
	}
	if len(entries) != 0 {
		t.Fatalf("audit = %d 件, want 0", len(entries))
	}
}

// seedCorruptCompletedOperation writes a terminal `completed` row and then applies
// the given raw SQL so the corruption under test is the only difference.
//
// **直接 SQL で壊す。** `SetOperationStatus` は `status NOT IN ('completed','failed')`
// のガードがあるので、状態機械の遷移では `completed` 行に `assignment_id` / `desired_exp`
// を後から生かせない。実際にそんな行が入りうるのは直接書き込み (手動 SQL / 壊れた
// migration / 復元) の場合だけなので、fixture もそうする。
func seedCorruptCompletedOperation(t *testing.T, svc *service, key, complete string) {
	t.Helper()
	ctx := context.Background()
	if _, err := svc.store.InsertOperation(ctx, operation{
		IdempotencyKey: key, ActorID: "moderator", UserID: "u1", RoleID: "r1",
		Mode: string(ModeAdd), Operand: 10, Status: string(StatusCompleted),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.store.db.ExecContext(ctx, complete, key); err != nil {
		t.Fatal(err)
	}
}

// **壊れた `completed` 行を「完了した操作」として返さない。**
//
// `status='completed'` でも `assignment_id` と `desired_exp` は NULL のままだ。**欠けた
// 値を既定で埋めて返すと、その再送は「XP が 0 になった完了操作」と嘘をつく**。実際に
// そうなのは 2 通り:
//   - `assignment_id` が無い … 完了として返し `AssignmentID` が空の応答を出す。**応答を
//     受け取った利用者は「XP が入ったのはどの assignment か」を追えない。**
//   - `desired_exp` が無い … `derefInt64(nil)` が 0 を返すので、**保存済みの XP が 0 と
//     読まれ、応答に 0 が返る**。
//
// どちらの行も **native side effect より前**に 409 + `CodeOperationFailed` で拒否し、
// 欠けている列名を呼出し側に伝える。`failed` には落とさない: `SetOperationStatus` の
// guard が terminal 行を動かせず、黙って `failed` にすると「運営者が直せる」ように
// 見えるが、残ったのは同じ壊れ行だから。**行は `completed` のまま固定され、再送する
// たびに同じ 409 が返る**ことが、この表が確かめる 2 番目の性質。
func TestChangeExpRejectsCorruptCompletedOperation(t *testing.T) {
	// **再送の payload は永続化分と一致させる。** そうしないと `ensureSameOperation` の
	// 409 に当たり、拒否が壊れ行の検証ではなく payload 不一致の検証になってしまう。
	// ActorID だけ別にしてあるのは、payload identity に含まれないため (下の test と同じ)。
	for _, tt := range []struct {
		name string
		key  string
		// complete produces the corrupt completed row.
		complete string
		// assignments is the native state. 空なら「続けば assign に届く」状況。
		assignments map[string][]assignment
		// seedXP is the stored experience of asg1, 0 で XP 行を作らない。
		seedXP     int64
		wantColumn string
	}{
		{
			name:        "missing assignment",
			key:         "corrupt-completed-no-assignment",
			complete:    `UPDATE role_level_operation SET desired_exp = 123 WHERE idempotency_key = $1`,
			assignments: map[string][]assignment{},
			wantColumn:  "assignment_id",
		},
		{
			name:        "missing desired XP",
			key:         "corrupt-completed-no-desired-exp",
			complete:    `UPDATE role_level_operation SET assignment_id = 'asg1' WHERE idempotency_key = $1`,
			assignments: map[string][]assignment{"r1": {mkAssignment("asg1", "u1")}},
			seedXP:      100,
			wantColumn:  "desired_exp",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			api := &stubAPI{
				roles:       map[string]roleInfo{"r1": {ID: "r1", Target: "manual"}},
				assignments: tt.assignments,
			}
			svc, _ := newXPService(t, api)
			if tt.seedXP > 0 {
				seedXP(t, svc, "asg1", "r1", "u1", tt.seedXP)
			}
			seedCorruptCompletedOperation(t, svc, tt.key, tt.complete)

			_, err := svc.ChangeExp(context.Background(),
				xpRequestAs("administrator", tt.key, "u1", "r1", ModeAdd, 10))
			se, code := extractCode(err)
			if se == nil || code != CodeOperationFailed {
				t.Fatalf("err = %v, code = %q, want %s", err, code, CodeOperationFailed)
			}
			if se.Status != http.StatusConflict {
				t.Fatalf("status = %d, want %d", se.Status, http.StatusConflict)
			}
			// **欠けている列が呼出し側に伝わる。** 運営者が row を直せるように。
			if !strings.Contains(err.Error(), tt.wantColumn) {
				t.Fatalf("err = %v, %s が伝わらない", err, tt.wantColumn)
			}
			// **再送しても同じ結果。** 壊れ行は `completed` のままなので、2 回目も
			// 同じ 409 で止まり、native にも XP にも触れない。
			_, retryErr := svc.ChangeExp(context.Background(),
				xpRequestAs("administrator", tt.key, "u1", "r1", ModeAdd, 10))
			if _, retryCode := extractCode(retryErr); retryCode != CodeOperationFailed {
				t.Fatalf("2 回目: err = %v, code = %q, want %s", retryErr, retryCode, CodeOperationFailed)
			}
			// **副作用が無い。** もし完了扱いを再開していれば、case 1 は
			// `admin/roles/assign` を呼び、case 2 は XP に +10 して audit を残す。
			if got := api.assignCallCount(); got != 0 {
				t.Fatalf("admin/roles/assign = %d 回, want 0", got)
			}
			xp, xpErr := svc.experienceFor(context.Background(), "r1", "u1", "asg1")
			if xpErr != nil {
				t.Fatal(xpErr)
			}
			if xp != tt.seedXP {
				t.Fatalf("XP = %d, want %d (壊れ行から XP を書き換えてはいけない)", xp, tt.seedXP)
			}
			entries, auditErr := svc.store.RecentAudit(context.Background(), "r1", "u1", 10)
			if auditErr != nil {
				t.Fatal(auditErr)
			}
			if len(entries) != 0 {
				t.Fatalf("audit = %d 件, want 0", len(entries))
			}
			// **行は `completed` のまま。** terminal guard が動かせないので `failed` に
			// 落とせないし、落とさない。壊れている事実は error のメッセージで伝える。
			op, found, loadErr := svc.store.LoadOperation(context.Background(), tt.key)
			if loadErr != nil || !found {
				t.Fatalf("found = %t, err = %v", found, loadErr)
			}
			if op.Status != string(StatusCompleted) {
				t.Fatalf("status = %q, want %q", op.Status, StatusCompleted)
			}
		})
	}
}

// invalidateRoleConfig is best-effort: a cache failure must not turn a successful config save into an API failure.
func TestInvalidateRoleConfigWarnsOnlyOnFailure(t *testing.T) {
	svc, inv := newXPService(t, &stubAPI{})
	rec := &logRecorder{}
	svc.log = slog.New(rec)

	svc.invalidateRoleConfig(context.Background(), "r1")
	if len(inv.roles) != 1 || inv.roles[0] != "r1" {
		t.Fatalf("roles = %v", inv.roles)
	}
	if got := rec.count("WARN"); got != 0 {
		t.Fatalf("成功した保存で %d 件の警告が出ました", got)
	}

	inv.roleError = errors.New("cache unavailable")
	svc.invalidateRoleConfig(context.Background(), "r1")
	// **保存は成功として扱い、警告だけで済ませる。** error を返すと「保存できたのに
	// API が失敗」になるので、warning の記録自体を検証する。
	if got := rec.count("WARN"); got != 1 {
		t.Fatalf("WARN = %d 件, want 1 (cache 失敗は警告のみ)", got)
	}
}

func TestTruncateKeepsValidUTF8(t *testing.T) {
	for _, tt := range []struct {
		name string
		got  string
		want string
	}{
		{"boundary", truncate("日本語", 9), "日本語"},
		{"mid-rune", truncate("日本語", 8), "日本..."},
		{"short", truncate("short", 20), "short"},
	} {
		if !utf8.ValidString(tt.got) {
			t.Fatalf("%s: invalid UTF-8: %q", tt.name, tt.got)
		}
		if tt.got != tt.want {
			t.Fatalf("%s: = %q, want %q", tt.name, tt.got, tt.want)
		}
	}
}

// **解放に失敗した接続は pool へ返さない。** 判断は SQL に依存しない純関数なので、
// 3 通りを fake なしで検証できる。
func TestOperationUnlockDecision(t *testing.T) {
	for _, tt := range []struct {
		name     string
		queryErr error
		unlocked bool
		discard  bool
	}{
		{"unlock query failed", errors.New("connection reset"), false, true},
		{"returned false", nil, false, true},
		{"released", nil, true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			discard, _ := operationUnlockDecision(tt.queryErr, tt.unlocked)
			if discard != tt.discard {
				t.Fatalf("discard = %t, want %t", discard, tt.discard)
			}
		})
	}
}

// cancelOnAssignAPI cancels the request context when admin/roles/assign is reached and
// then fails the call, so the terminal-failure write has to survive a canceled context.
type cancelOnAssignAPI struct {
	plugin.API
	cancel context.CancelFunc
}

func (a *cancelOnAssignAPI) AsUser(userID string) plugin.Caller {
	return &cancelOnAssignCaller{Caller: a.API.AsUser(userID), cancel: a.cancel}
}

type cancelOnAssignCaller struct {
	plugin.Caller
	cancel context.CancelFunc
}

func (c *cancelOnAssignCaller) Call(ctx context.Context, endpoint string, params any) (json.RawMessage, error) {
	if endpoint == "admin/roles/assign" {
		c.cancel()
		return nil, apiError(endpoint, http.StatusInternalServerError, "STUB_ASSIGN_FAILED")
	}
	return c.Caller.Call(ctx, endpoint, params)
}

// **context が破棄された状態でも terminal failure は記録される。** `fail` は原因となった
// context をそのまま使うと、`SetOperationStatus` が cancellation で失敗して operation が
// resumable のまま残る。`ResumePendingOperations` は `pending`/`assigning`/`applying` を
// 再開するので、そのままだと**運営者が止めるべき failure が自動で再開される**。
func TestChangeExpPersistsTerminalFailureAfterContextCancellation(t *testing.T) {
	base := &stubAPI{
		roles:       map[string]roleInfo{"r1": {ID: "r1", Target: "manual"}},
		assignments: map[string][]assignment{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	api := &cancelOnAssignAPI{API: base, cancel: cancel}
	svc, _ := newXPService(t, api)

	if _, err := svc.ChangeExp(ctx, xpRequest("cancel-key", "u1", "r1", ModeAdd, 10)); err == nil {
		t.Fatal("native の失敗を隠しています")
	}
	// 読み出しは破棄済み context ではなく新しい context で行う。
	op, found, err := svc.store.LoadOperation(context.Background(), "cancel-key")
	if err != nil || !found {
		t.Fatalf("found = %t, err = %v", found, err)
	}
	if op.Status != string(StatusFailed) {
		t.Fatalf("status = %q, want %q (context 破棄で failure の記録が失われる)", op.Status, StatusFailed)
	}
	if op.LastError == "" {
		t.Fatal("last_error が空です (原因が残らない)")
	}
	entries, err := svc.store.RecentAudit(context.Background(), "r1", "u1", 10)
	if err != nil || len(entries) != 0 {
		t.Fatalf("audit = %d 件, err = %v, want 0", len(entries), err)
	}
}

// **この test が保証するのは 2 点だけ。** 破棄済み (canceled) の request context では
// operation lock の専用接続の確保自体が失敗し、`acquireOperationLock` が error を返す。
// そのあとも pool は使えており、別の key の `ChangeExp` は最後まで完了する。
//
// **この test は専用接続の確保・返却を観測しない。** canceled context では
// `s.store.db.Conn(ctx)` 自体が失敗するため、`Conn` の取得に成功したあとの
// `ExecContext` 失敗分岐には到達せず、`driver.ErrBadConn` による物理接続の破棄も
// **カバーしていない**。現行の具体的な `*sql.DB` 設計には transport fault の決定的な
// 注入点が無く、観測できない。これは test できる範囲の既知の限界であり、production の破棄処理を弱める理由にはならない。
func TestChangeExpReturnsErrorAfterCanceledLockAcquisitionAndLaterOperationSucceeds(t *testing.T) {
	api := &stubAPI{
		roles:       map[string]roleInfo{"r1": {ID: "r1", Target: "manual"}},
		assignments: map[string][]assignment{"r1": {mkAssignment("asg1", "u1")}},
	}
	svc, _ := newXPService(t, api)
	seedXP(t, svc, "asg1", "r1", "u1", 10)

	dead, cancelDead := context.WithCancel(context.Background())
	cancelDead()
	if _, err := svc.acquireOperationLock(dead, "poison-key"); err == nil {
		t.Fatal("破棄済み context で lock を取得できました")
	}

	res, err := svc.ChangeExp(context.Background(),
		xpRequest("after-failed-acquire", "u1", "r1", ModeAdd, 5))
	if err != nil {
		t.Fatal(err)
	}
	if res.Experience != 15 || res.Status != StatusCompleted {
		t.Fatalf("= %+v", res)
	}
}

// logRecorder captures slog records so a test can assert what the plugin warned about.
type logRecorder struct {
	mu    sync.Mutex
	lines []string
}

func (l *logRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (l *logRecorder) Handle(_ context.Context, r slog.Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, r.Level.String()+" "+r.Message)
	return nil
}

func (l *logRecorder) WithAttrs([]slog.Attr) slog.Handler { return l }
func (l *logRecorder) WithGroup(string) slog.Handler      { return l }

func (l *logRecorder) count(level string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, line := range l.lines {
		if strings.HasPrefix(line, level+" ") {
			n++
		}
	}
	return n
}
