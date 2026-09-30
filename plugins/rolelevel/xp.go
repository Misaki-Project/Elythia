package rolelevel

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Mode is one experience mutation mode.
type Mode string

const (
	// ModeSet replaces the experience with the operand.
	ModeSet Mode = "set"
	// ModeAdd adds the operand to the current experience.
	ModeAdd Mode = "add"
	// ModeMultiplier multiplies the current experience by the operand. **The operand
	// is a raw factor**: 1.5 means times 1.5, never "150 percent".
	ModeMultiplier Mode = "multiplier"
)

// Status is one operation state.
//
//	pending   -> recorded, native assignment not resolved yet
//	assigning -> native assign is in flight
//	applying  -> the plugin transaction is about to commit
//	completed -> the XP write committed
//	failed    -> stopped on an error the operator has to look at
//
// **resumable なのは pending / assigning / applying だけ。** completed は目的の状態、
// failed は原因を追って運営者が直すもので、自動再開すると判断を飛ばす。
type Status string

const (
	StatusPending   Status = "pending"
	StatusAssigning Status = "assigning"
	StatusApplying  Status = "applying"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
)

// maxAssignLookupAttempts bounds how many times the plugin re-reads the assignment
// list right after admin/roles/assign.
//
// native の assign は transaction 内で完結するので1回でも見えるはずだが、read replica
// を経由している構成だと 1 回では現れないことがある。数回で足りなければ「不明」として
// failed に落とす (冪等に回せるので、再実行しても二重適用にならない)。
const maxAssignLookupAttempts = 3

// ChangeExpRequest is one administrator experience mutation.
type ChangeExpRequest struct {
	IdempotencyKey string
	ActorID        string
	UserID         string
	RoleID         string
	Mode           Mode
	// Operand is a finite decimal. multiplier は raw factor (1.5 = ×1.5)。
	Operand float64
	Note    string
}

// ChangeExpResult is what the route and the reconciliation job both report.
type ChangeExpResult struct {
	AssignmentID string
	Experience   int64
	Status       Status
	// Resumed reports that the operation was already on record and this call
	// continued it rather than starting a new one.
	Resumed bool
}

// applyMode computes the desired experience for one mutation.
//
// current is the assignment's stored experience, 0 when no row exists yet — that is
// what makes set yield the operand, add yield the operand and multiplier yield 0 for
// a fresh assignment.
//
// **3モードとも float64 で計算してから floor する。** operand が小数を許すので
// 整数演算にはできない。floor の後に 0..MaxExperience へ収めるので、XP は JSON number
// / Go safe integer のまま保たれる。
func applyMode(mode Mode, current int64, operand float64) (int64, error) {
	if err := validateMode(mode, operand); err != nil {
		return 0, err
	}
	var raw float64
	switch mode {
	case ModeSet:
		raw = operand
	case ModeAdd:
		raw = float64(current) + operand
	case ModeMultiplier:
		// **raw factor。** 1.5 は ×1.5。100 で割って百分率にしない。
		raw = float64(current) * operand
	default:
		return 0, codedErrorf(http.StatusBadRequest, CodeUnknownMode,
			"mode %q は %s|%s|%s のいずれかです", mode, ModeSet, ModeAdd, ModeMultiplier)
	}
	return clampExperience(raw)
}

// clampExperience folds a computed value into 0..MaxExperience after flooring it.
//
// **NaN は error、overflow の ±Infinity は両端へ clamp。** operand 自体の非有限値は
// applyMode の validateMode で拒否する。ここへ来る Infinity は有限 operand の加算・乗算
// が float64 を overflow した結果なので、仕様どおり XP 範囲へ飽和させる。
func clampExperience(v float64) (int64, error) {
	if math.IsNaN(v) {
		return 0, codedErrorf(http.StatusBadRequest, CodeOperandOutOfRange,
			"計算結果が NaN になりました")
	}
	if math.IsInf(v, 1) {
		return MaxExperience, nil
	}
	if math.IsInf(v, -1) {
		return 0, nil
	}
	f := math.Floor(v)
	switch {
	case f < 0:
		return 0, nil
	case f > MaxExperienceFloat:
		return MaxExperience, nil
	default:
		return int64(f), nil
	}
}

// validateMode rejects an unknown mode and an operand that is not a usable finite
// number.
func validateMode(mode Mode, operand float64) error {
	// **NaN と ±Infinity は先に落とす。** 範囲比較は NaN に対して常に false になるので、
	// そのまま比較すると「範囲外か」の判定が静かに壊れる。
	if math.IsNaN(operand) || math.IsInf(operand, 0) {
		return codedErrorf(http.StatusBadRequest, CodeOperandOutOfRange,
			"operand は有限の数にしてください (%v)", operand)
	}
	switch mode {
	case ModeSet, ModeAdd, ModeMultiplier:
		// **mode ごとに追加の制約は作らない。** 仕様は「有限の小数を受け付ける」だけで、
		// 負の factor を拒むと承認範囲外の制約になる。負の multiplier はXP が 0 になるが、
		// その判断は `clampExperience` に一本化する。
		return nil
	default:
		return codedErrorf(http.StatusBadRequest, CodeUnknownMode,
			"mode %q は %s|%s|%s のいずれかです", mode, ModeSet, ModeAdd, ModeMultiplier)
	}
}

// idempotencyKeyRe bounds an operator-supplied key.
//
// **主キーに入る値** なので、短くして引用文字を含まない形にする。ログ行やエラーメッセージ
// に出るので、そこで整形が要らないことが条件。
var idempotencyKeyRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func validateIdempotencyKey(key string) error {
	if key == "" {
		return codedErrorf(http.StatusBadRequest, CodeIdempotencyKeyRequired,
			"idempotencyKey が必要です (再送には同じ値を渡してください)")
	}
	if !idempotencyKeyRe.MatchString(key) {
		return codedErrorf(http.StatusBadRequest, CodeIdempotencyKeyInvalid,
			"idempotencyKey は英数字とハイフン・アンダースコアのみ、64文字までにしてください")
	}
	return nil
}

// lockAssignment serializes XP mutations even when different idempotency keys target
// the same assignment. An advisory lock works before the first XP row exists and is
// released automatically with the transaction.
func lockAssignment(ctx context.Context, tx *sql.Tx, assignmentID string) error {
	_, err := tx.ExecContext(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended('role-level:' || $1, 0))`, assignmentID)
	return err
}

// ChangeExp runs one experience mutation to completion, or records where it stopped
// so the reconciliation job can resume it with the same idempotency key.
//
// The native assignment creation cannot share a transaction with the plugin writes
// (plugin.Storage says so explicitly), so the operation row is the hand-off point:
// it is written before the native call and updated after the plugin transaction
// commits.
func (s *service) ChangeExp(ctx context.Context, req ChangeExpRequest) (ChangeExpResult, error) {
	if err := validateIdempotencyKey(req.IdempotencyKey); err != nil {
		return ChangeExpResult{}, err
	}
	if err := validateMode(req.Mode, req.Operand); err != nil {
		return ChangeExpResult{}, err
	}

	created, err := s.store.InsertOperation(ctx, operation{
		IdempotencyKey: req.IdempotencyKey,
		ActorID:        req.ActorID,
		UserID:         req.UserID,
		RoleID:         req.RoleID,
		Mode:           string(req.Mode),
		Operand:        req.Operand,
		Status:         string(StatusPending),
	})
	if err != nil {
		return ChangeExpResult{}, s.storageError(ctx, "XP 操作の記録", err)
	}

	op, found, err := s.store.LoadOperation(ctx, req.IdempotencyKey)
	if err != nil {
		return ChangeExpResult{}, s.storageError(ctx, "XP 操作の読み込み", err)
	}
	if !found {
		return ChangeExpResult{}, s.storageError(ctx, "XP 操作の読み込み",
			fmt.Errorf("記録直後に %s が見つかりません", req.IdempotencyKey))
	}
	// 同じ key の再送。**payload comparison だけを先に行う。**
	//
	// **ここに terminal の fast-return を置かない。** caller が渡した `op` は lock 取得前の copy
	// なので、別経路 (reconciliation job や別の retry) が先に lock を取って terminal に進めた場合、
	// この copy は古い。lock を取らずに返すと、その stale copy の `DesiredExp` / `LastError` を
	// そのまま返す。comparison のあとは必ず `resume` へ渡し、**session advisory lock の中で
	// re-read して** 判定する。lock を取った側が決めるので、creator でなくても同じ結果になる。
	if !created {
		if err := ensureSameOperation(op, req); err != nil {
			return ChangeExpResult{}, err
		}
	}
	return s.resume(ctx, op, req.Note, created)
}

// ensureSameOperation rejects a retry that reuses an idempotency key for a different
// request payload.
//
// **ActorID と Note は比較しない。** retry と reconciliation は永続化された
// operation.ActorID で native を呼ぶので、要求者だけが違う再送は正常系。Note は
// operation テーブルに列が無く監査専用で、再開の判断にも要らない
// (再開する呼び出し側が毎回渡す)。
func ensureSameOperation(op operation, req ChangeExpRequest) error {
	if op.UserID != req.UserID || op.RoleID != req.RoleID || op.Mode != string(req.Mode) ||
		op.Operand != req.Operand {
		return codedErrorf(http.StatusConflict, CodeIdempotencyConflict,
			"この idempotencyKey は別の要求で使用済みです (userId=%s, roleId=%s, mode=%s, operand=%v)",
			req.UserID, req.RoleID, req.Mode, req.Operand)
	}
	return nil
}

// operationLockNamespace is the stable prefix of the session advisory lock key.
//
// **stable な文字列に保つ。** lock key は `namespace || idempotencyKey` の hash なので、値を
// 変えると、同じ idempotency key でも process を跨いで別 lock を待つことになる。旧版と混在する
// 移行中でも相互排除が成立しなくなる。
//
// **rationale**: 取得・解放の 2 query はこの定数を **bind parameter** として渡す。namespace を
// SQL へ literal で直書きしないので、SQL と定数がずれることが起こらない。namespace はこの
// 定数の 1 箇所にだけ存在する。
const operationLockNamespace = "role-level-operation:"

// acquireOperationLock serializes every resume of one idempotency key on a dedicated
// connection.
//
// **transaction ではなく session advisory lock を使う。** native API は plugin の
// transaction に載らないため、transaction を跨ぐ専用の *sql.Conn を使う。
func (s *service) acquireOperationLock(ctx context.Context, key string) (*sql.Conn, error) {
	conn, err := s.store.db.Conn(ctx)
	if err != nil {
		return nil, s.storageError(ctx, "XP 操作の lock 用接続の確保", err)
	}
	if _, err := conn.ExecContext(ctx,
		`SELECT pg_advisory_lock(hashtextextended($1 || $2, 0))`,
		operationLockNamespace, key); err != nil {
		// **取得に失敗した接続も pool へ返さない。** cancellation や transport error は
		// 「server 側が lock を取り終えた直後、client が成功を受け取る前」の可能性が
		// ある。握ったまま pool へ戻すと `releaseOperationLock` と同じ理由で次の借用者の
		// lock を取り損ねるので、先に物理接続を切る。
		s.log.Error("role-level: XP 操作の lock を取得できなかったので接続を破棄します",
			"idempotencyKey", key, "err", err)
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		_ = conn.Close()
		return nil, s.storageError(ctx, "XP 操作の lock の取得", err)
	}
	return conn, nil
}

// releaseOperationLock unlocks the session advisory lock, then returns the connection to
// the pool.
//
// **解放できていない接続を pool へ返さない。** `pg_advisory_unlock` は bool を返し、`false` は
// 「この session はその key を握っていない」を意味する。握ったまま pool へ返すと、次の借用者が
// 別 key の lock を取り損ね、その key の再取得も出来なくなる。`driver.ErrBadConn` を返して
// 物理接続を切り離してから `Close` する。
//
// unlock は request の cancellation に巻き込まれないよう、切断済み context + timeout で実行する。
func (s *service) releaseOperationLock(ctx context.Context, conn *sql.Conn, key string) {
	unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	var unlocked bool
	queryErr := conn.QueryRowContext(unlockCtx,
		`SELECT pg_advisory_unlock(hashtextextended($1 || $2, 0))`,
		operationLockNamespace, key).Scan(&unlocked)
	if discard, reason := operationUnlockDecision(queryErr, unlocked); discard {
		s.log.Error("role-level: XP 操作の lock を解放できていないので接続を破棄します",
			"idempotencyKey", key, "reason", reason, "err", queryErr)
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	}
	_ = conn.Close()
}

// operationUnlockDecision says whether the physical connection may go back to the pool.
//
// **false なら pool へ返さない。** 解放の失敗は 1 回の矛盾ではなく、その key の session
// advisory lock を pool の中に持ち込む。判断だけを純関数にしておくので、SQL を待たずに
// 3 通りを検証できる。
func operationUnlockDecision(queryErr error, unlocked bool) (discard bool, reason string) {
	switch {
	case queryErr != nil:
		return true, "unlock query failed"
	case !unlocked:
		return true, "pg_advisory_unlock returned false"
	default:
		return false, ""
	}
}

// resume continues an operation from whatever state the row records.
//
// It is the single implementation behind the first attempt, a client retry and the
// reconciliation job, so the three can never drift apart.
//
// note is the operator's note for the audit row. **operation テーブルに note 列が無い**
// のは、note が監査専用で操作の再開には要らないため。job からの再開は "" を渡す。
func (s *service) resume(ctx context.Context, op operation, note string, created bool) (ChangeExpResult, error) {
	conn, err := s.acquireOperationLock(ctx, op.IdempotencyKey)
	if err != nil {
		return ChangeExpResult{}, err
	}
	defer s.releaseOperationLock(ctx, conn, op.IdempotencyKey)
	// caller の copy は信頼しない。lock 待ちの間に別経路が terminal に進めた可能性がある。
	//
	// **read も lock 用接続でやる。** この conn は session advisory lock を握った
	// ままだし、以降のこの関数の SQL はすべて同じ conn に載せる。pool (`*sql.DB`) を
	// 触る書き手が 1 つでも残ると、lock 中の接続数 == MaxOpenConns の瞬間に pool が
	// 空きゼロになり、**pool を待つ者も pool を返す者も先に進めない**。
	reloaded, found, err := s.store.loadOperation(ctx, conn, op.IdempotencyKey)
	if err != nil || !found {
		if err == nil {
			err = fmt.Errorf("%s が見つかりません", op.IdempotencyKey)
		}
		return ChangeExpResult{}, s.storageError(ctx, "XP 操作の読み込み", err)
	}
	// `Resumed: !created` — `created` は「**この呼び出しが** operation 行を INSERT したか」で、
	// lock を取った順番ではない。`InsertOperation` の戻り値として lock 取得前に確定している。
	// **creator が lock を 2 番目に取っても `Resumed=false` を返す**ので、同一 key の並行呼び出しで
	// `Resumed=false` はちょうど 1 回だけになる (creator のみ)。terminal 判定のために transaction や
	// native call を追加で持つ必要もない。
	// **resumable なのは 3 つを明示列挙する。** `status` 列は CHECK の無い `text NOT NULL`
	// なので、5 定数以外の値が入りうる。「`completed` / `failed` 以外なら再開可」と
	// 書くと、壊れた行を resumable と誤解して **判断できない XP 操作に native を呼ぶ**
	// ことになる。unknown は再開不可として `failed` に落とす。
	switch Status(reloaded.Status) {
	case StatusCompleted:
		// **完了行の必須列を検証してから返す。** 欠けた値を既定で埋めて返すと、その
		// 再送は「XP が 0 の完了操作」「assignment が空の完了操作」という**嘘**を返す。
		// 検証は native side effect の前 (この switch より前) で行うので、壊れ行に
		// 対して `admin/roles/assign` を呼ぶことがない。
		if defect := completedOperationDefect(reloaded); defect != "" {
			return ChangeExpResult{}, s.corruptCompletedError(reloaded, defect)
		}
		return ChangeExpResult{AssignmentID: reloaded.AssignmentID,
			Experience: derefInt64(reloaded.DesiredExp), Status: StatusCompleted, Resumed: !created}, nil
	case StatusPending, StatusAssigning, StatusApplying:
		// 途中まで進んだ行はそのまま再開する。
	// **rationale**: `failed` は **終端** である。`ResumePendingOperations` は `failed` を
	// 候補に一切出さないので **自動で再開しない**。運営者は記録された `last_error` を調査
	// してから **新しい** idempotency key で retry する。**同じ** key を再送すると
	// HTTP 409 + `CodeOperationFailed` が返るので、意図しない二重適用は起こらない。
	// `default` (unknown status) も同じ 409 + `CodeOperationFailed` で終端にする。
	case StatusFailed:
		return ChangeExpResult{}, codedErrorf(http.StatusConflict, CodeOperationFailed,
			"この XP 操作は失敗済みです: %s", reloaded.LastError)
	default:
		return s.fail(ctx, conn, reloaded, codedErrorf(http.StatusConflict, CodeOperationFailed,
			"この XP 操作は不明な状態 %q で保存されているため再開できません", reloaded.Status))
	}

	op = reloaded
	// This operation may create a native assignment. Bind every native call in this
	// operation to the persisted original requester, not the configured read actor
	// and not a possibly different actor supplied by a retry.
	native, err := s.nativeFor(op.ActorID)
	if err != nil {
		return s.fail(ctx, conn, op, err)
	}

	assignmentID := op.AssignmentID
	if assignmentID == "" {
		assignmentID, _, err = s.assignmentIDFor(ctx, native, op.RoleID, op.UserID)
		if err != nil {
			return s.fail(ctx, conn, op, err)
		}
	}
	if assignmentID == "" {
		if op.AssignmentCreated {
			return s.fail(ctx, conn, op, codedErrorf(http.StatusBadGateway, CodeAssignmentUnresolved,
				"作成済み assignment を取得できませんでした (roleId=%s, userId=%s)",
				op.RoleID, op.UserID))
		}
		// 未付与。**先に status を書いてから native を呼ぶ。** クラッシュしても
		// 「どこまで進んだか」が残らず、reconciliation が再開できない。
		if err := s.store.SetOperationStatus(ctx, conn, op.IdempotencyKey,
			string(StatusAssigning), "", nil, ""); err != nil {
			return ChangeExpResult{}, s.storageError(ctx, "XP 操作の記録", err)
		}
		if err := native.Assign(ctx, op.UserID, op.RoleID); err != nil {
			return s.fail(ctx, conn, op, err)
		}
		if err := s.store.MarkAssignmentCreated(ctx, conn, op.IdempotencyKey); err != nil {
			return s.fail(ctx, conn, op, s.storageError(ctx, "XP 操作の assignment 作成記録", err))
		}
		assignmentID, _, err = s.assignmentIDFor(ctx, native, op.RoleID, op.UserID)
		if err != nil {
			return s.fail(ctx, conn, op, err)
		}
		if assignmentID == "" {
			return s.fail(ctx, conn, op, codedErrorf(http.StatusBadGateway, CodeAssignmentUnresolved,
				"付与直後に assignment を取得できませんでした (roleId=%s, userId=%s)",
				op.RoleID, op.UserID))
		}
	}

	if err := s.store.SetOperationStatus(ctx, conn, op.IdempotencyKey,
		string(StatusApplying), assignmentID, nil, ""); err != nil {
		return ChangeExpResult{}, s.storageError(ctx, "XP 操作の記録", err)
	}

	// **XP・audit・operation completion を1つの transaction で決める。**
	// これ以外の組み合わせだと「XP は増えたのに operation が pending のまま」という窓が
	// できて、reconciliation が add を二重適用する。operation row の FOR UPDATE は
	// client retry と reconciliation が同じ key を同時に実行する場合も直列化する。
	//
	// **transaction は lock 用接続のほかに開かない。** これより手前 (この BeginTx が
	// 終わるまで) は接続 1 本だけで足りる。**native call を挟んでいる間は transaction
	// を持たない**: native 呼び出しは plugin の transaction に載らないので、待たせる
	// のは plugin の SQL だけ。BEGIN を開いたまま native を待つと、lock を掴んだ
	// backend 1 台を丸ごと占有し、他の同時実行まで巻き添えになる。
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return ChangeExpResult{}, s.storageError(ctx, "transaction の開始", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	locked, found, err := s.store.LoadOperationForUpdate(ctx, tx, op.IdempotencyKey)
	if err != nil {
		return s.failTx(ctx, conn, tx, op, "XP 操作の lock", err)
	}
	if !found {
		return s.failTx(ctx, conn, tx, op, "XP 操作の lock",
			fmt.Errorf("%s が見つかりません", op.IdempotencyKey))
	}
	// terminal state は session lock 後の reload で処理済みなので、ここでは再判定しない。
	// FOR UPDATE は advisory lock を通らない writer に対して operation row を守る。
	op = locked
	if op.AssignmentID != "" {
		assignmentID = op.AssignmentID
	}
	if err := lockAssignment(ctx, tx, assignmentID); err != nil {
		return s.failTx(ctx, conn, tx, op, "assignment の lock", err)
	}
	var current int64
	err = tx.QueryRowContext(ctx, `
		SELECT experience FROM role_level_experience
		WHERE assignment_id = $1 AND role_id = $2 AND user_id = $3
	`, assignmentID, op.RoleID, op.UserID).Scan(&current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return s.failTx(ctx, conn, tx, op, "経験値の読み込み", err)
	}
	desired, err := applyMode(Mode(op.Mode), current, op.Operand)
	if err != nil {
		return s.failTx(ctx, conn, tx, op, "経験値の計算", err)
	}

	if err := s.store.SetExperience(ctx, tx, experienceRow{
		AssignmentID: assignmentID, RoleID: op.RoleID, UserID: op.UserID, Experience: desired,
	}); err != nil {
		return s.failTx(ctx, conn, tx, op, "経験値の保存", err)
	}
	if err := s.store.InsertAudit(ctx, tx, auditEntry{
		ActorID: op.ActorID, Operation: "change-exp", RoleID: op.RoleID, UserID: op.UserID,
		AssignmentID: assignmentID, Note: truncate(note, 500),
		Before: map[string]any{"experience": current},
		After:  map[string]any{"experience": desired},
	}); err != nil {
		return s.failTx(ctx, conn, tx, op, "監査の記録", err)
	}
	if err := s.store.SetOperationStatus(ctx, tx, op.IdempotencyKey,
		string(StatusCompleted), assignmentID, &desired, ""); err != nil {
		return s.failTx(ctx, conn, tx, op, "XP 操作の記録", err)
	}
	if err := tx.Commit(); err != nil {
		return s.fail(ctx, conn, op, s.storageError(ctx, "transaction の commit", err))
	}
	committed = true

	// **commit の後にだけ cache を落とす。** 先に落とすと、commit に失敗した変更を
	// 他ワーカーに配ってしまう。 invalidate に失敗しても XP 自体は保存済みなので、
	// warn だけで済ませる (次回の invalidation で回復する)。
	s.invalidateAfterXPChange(ctx, op.UserID, op.RoleID)

	return ChangeExpResult{
		AssignmentID: assignmentID, Experience: desired, Status: StatusCompleted, Resumed: !created,
	}, nil
}

// assignmentIDFor resolves the native assignment id of one (role, user) pair.
//
// **plugin table を代わりの根拠にしない。** unassign 後に同じ role を再付与すると古い行が
// 残っており、それを読むと古い assignment ID を返して XP が復活してしまう。だから毎回
// native へ聞き直す。
func (s *service) assignmentIDFor(ctx context.Context, native *nativeRole, roleID, userID string) (string, bool, error) {
	for attempt := 0; attempt < maxAssignLookupAttempts; attempt++ {
		id, found, err := native.FindAssignment(ctx, roleID, userID)
		if err != nil {
			return "", false, err
		}
		if found {
			return id, true, nil
		}
	}
	return "", false, nil
}

// experienceFor returns the stored experience of one assignment, 0 when there is no
// row yet.
//
// **role_id と user_id も照合する。** assignment_id だけ見ると、壊れた行や別の plugin
// の記録が同じ id を指したときに、静かに別の experience を上書きする。
func (s *service) experienceFor(ctx context.Context, roleID, userID, assignmentID string) (int64, error) {
	rows, err := s.store.ExperienceRowsForRoleUser(ctx, roleID, userID)
	if err != nil {
		return 0, s.storageError(ctx, "経験値の読み込み", err)
	}
	for _, r := range rows {
		if r.AssignmentID == assignmentID {
			return r.Experience, nil
		}
	}
	return 0, nil
}

// fail records a terminal failure and returns the error unchanged.
//
// **terminal failure は pool ではなく lock 用接続に書く。** connection を 1 本も
// 持たない呼び出し (lock 取得前) からは `fail` を使わない: `*sql.DB` を借りると、
// lock を掴んだ数 == MaxOpenConns のときに「pool を待つ者 / pool を返す者が
// 共に進めない」状態になるため。
func (s *service) fail(ctx context.Context, conn *sql.Conn, op operation, cause error) (ChangeExpResult, error) {
	// **terminal failure の記録は cancellation に巻き込ませない。** 原因となった context は
	// 既に破棄されていることがあるので、そのまま使うと `SetOperationStatus` が
	// cancellation で失敗し、operation が resumable のまま残る
	// (`ResumePendingOperations` は `pending`/`assigning`/`applying` を再開するので、
	// 運営者が原因を追って止めるべき failure を自動で再開してしまう)。
	// 切断済み context + timeout なら **pool の空きを待たずに** 書ける。
	writeCtx, cancelWrite := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancelWrite()
	if err := s.store.SetOperationStatus(writeCtx, conn, op.IdempotencyKey,
		string(StatusFailed), op.AssignmentID, op.DesiredExp, truncate(cause.Error(), 500)); err != nil {
		s.log.Error("role-level: XP 操作の失敗を記録できませんでした",
			"idempotencyKey", op.IdempotencyKey, "err", err)
	}
	return ChangeExpResult{}, cause
}

// failTx rolls back and records the failure outside the (already doomed) transaction.
//
// **rollback してから、同じ接続で terminal を書き込む。** transaction の backend は
// 死んでいるので、その transaction へ繋がない。書き先は lock 用接続なので、pool が
// 空いてなくても記録は残る。
func (s *service) failTx(ctx context.Context, conn *sql.Conn, tx *sql.Tx, op operation, what string, cause error) (ChangeExpResult, error) {
	_ = tx.Rollback()
	wrapped := s.storageError(ctx, what, cause)
	return s.fail(ctx, conn, op, wrapped)
}

// completedOperationDefect names the columns a terminal `completed` row must carry,
// or returns "" when the row is intact.
//
// **`completed` 行の `assignment_id` と `desired_exp` は NOT NULL ではない。**
// `status` を書く UPDATE は両方とも `COALESCE` なので、未設定のまま `completed` に
// なりうる。壊れた行は人手 (手動 SQL / 壊れた migration / 復元) でしか作れない。
// **`status` が 5 定数のままなので state machine から見れば「完了済み」**で、欠落は
// 「何が完了なのかを返せない」問題になる。判定は純関数にしてあるので、必須列の
// 判定だけ SQL を待たずに検証できる。
func completedOperationDefect(op operation) string {
	var missing []string
	if op.AssignmentID == "" {
		missing = append(missing, "assignment_id")
	}
	if op.DesiredExp == nil {
		missing = append(missing, "desired_exp")
	}
	if len(missing) == 0 {
		return ""
	}
	return strings.Join(missing, " / ")
}

// corruptCompletedError reports a completed row that cannot describe its own result.
//
// **返り値を推測で埋めない。** `derefInt64(nil)` は 0 を返すので、欠けた `desired_exp`
// をそのまま返すと「XP 0 の完了操作」になる。`assignment_id` が空のまま返すと、応答を
// 見た利用者は「どの assignment に何が入ったのか」を追えない。
//
// **`failed` へ落とさない。** `SetOperationStatus` は `status NOT IN
// ('completed','failed')` のガードで terminal 行を動かせない。動かせない行を
// 「運営者が直せる failed」に見せる方が危ないので、**行は `completed` のまま**にして
// 欠落した列名だけを伝える。呼び出し側は同じ 409 を受け続ける。
func (s *service) corruptCompletedError(op operation, defect string) error {
	s.log.Error("role-level: 完了扱いの XP 操作に必須列が欠けています",
		"idempotencyKey", op.IdempotencyKey, "missing", defect,
		"assignmentId", op.AssignmentID, "desiredExp", derefInt64(op.DesiredExp))
	return codedErrorf(http.StatusConflict, CodeOperationFailed,
		"この XP 操作は %s が保存されていないため結果を返せません (idempotencyKey=%s)",
		defect, op.IdempotencyKey)
}

// invalidateAfterXPChange drops the cached policy inputs this XP change affects.
//
// **失敗しても XP は保存済み。** ここで error を返すと「変更は成功したのに API が失敗」
// になるので、warn だけ出して次回の invalidation に任せる。
func (s *service) invalidateAfterXPChange(ctx context.Context, userID, roleID string) {
	inv := invalidator()
	if err := inv.InvalidateUser(ctx, userID); err != nil {
		s.log.Warn("role-level: user の policy cache を破棄できませんでした", "userId", userID, "err", err)
	}
	if err := inv.InvalidateRole(ctx, roleID); err != nil {
		s.log.Warn("role-level: role の policy cache を破棄できませんでした", "roleId", roleID, "err", err)
	}
}

// invalidateRoleConfig drops the cached policy inputs of one role after a level
// configuration write.
//
// **失敗しても保存は成功。** Task 10 の config 保存経路がここを呼んでおり、ここで error を返すと
// 「保存できたのに API が失敗」になる。warn だけ出して次回の invalidation に任せる
// (invalidateAfterXPChange と同じ方針)。
func (s *service) invalidateRoleConfig(ctx context.Context, roleID string) {
	if err := invalidator().InvalidateRole(ctx, roleID); err != nil {
		s.log.Warn("role-level: role の policy cache を破棄できませんでした", "roleId", roleID, "err", err)
	}
}

// derefInt64 reads a nullable bigint column.
func derefInt64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// truncate bounds a stored message without splitting a multi-byte rune.
//
// **バイトで切らない。** s[:n] は日本語・絵文字の途中を切って、DB に壊れた UTF-8 を書き込む。
// 壊れた行は JSON 化も読取も壊れるので、最後の完全な rune まで巻き戻す。
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	for len(cut) > 0 {
		r, size := utf8.DecodeLastRuneInString(cut)
		if r != utf8.RuneError || size > 1 {
			break
		}
		cut = cut[:len(cut)-1]
	}
	return strings.TrimSpace(cut) + "..."
}
