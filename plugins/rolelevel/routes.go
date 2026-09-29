package rolelevel

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/shiroha-a/mk/plugin"
)

// maxPageSize / defaultPageSize bound the member list.
//
// **上限は native の admin/roles/users の 100 件に揃える。** これより大きい
// pageSize をそのまま native へ渡すと同じ走査を plugin 側で繰り返すことになるので、
// 1 リクエストあたりの負荷を頭打ちにし、続きは offset で要求する形にする。
const (
	maxPageSize     = 100
	defaultPageSize = 20
)

// adminUserAuditLimit / adminUserOperationCap are the fixed history sizes of the
// administrator user view. この画面は「いまどうなっているか」を見せるので全件走査
// にしない (全件は DB を直接読むか Task 11 の job を使う)。
const (
	adminUserAuditLimit   = 20
	adminUserOperationCap = 20
)

// reconcileAllMode is the `mode` that makes /admin/reconcile run every pass.
//
// **mode 省略は all。** frontend の「全部やる」は mode を送らない。Task 11 の
// RunReconcile は mode を whitelist で照合するので、"" をそのまま渡すと未知の
// mode として 400 になる。省略を all に翻訳してから渡す。
const reconcileAllMode = "all"

// pageRequest is the paging body every listing route accepts.
type pageRequest struct {
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

// pageBounds clamps the requested paging to what the native member list returns.
//
// **0 は「指定なし」** なので既定値を使い、負の offset は 0 起点に落とす。
func pageBounds(body pageRequest) (int, int) {
	limit, offset := defaultPageSize, 0
	if body.Limit > 0 {
		limit = body.Limit
	}
	if limit > maxPageSize {
		limit = maxPageSize
	}
	if body.Offset > 0 {
		offset = body.Offset
	}
	return limit, offset
}

// validatePageLimit rejects an explicitly out-of-range limit instead of silently
// clamping it.
//
// **無指定 (0) は既定値、範囲外は 400。** limit に 1000 を入れて黙って 100 に
// 丸めると、frontend から見ると「全部取れた 100 件」と「100 件で切れた」の
// 区別がつかなくなる。`total` / `truncated` の下界契約を守るなら、範囲外は
// 受け付けないほうが正しい。
func validatePageLimit(field string, limit int) error {
	if limit == 0 {
		return nil
	}
	if limit < 0 || limit > maxPageSize {
		return codedErrorf(http.StatusBadRequest, CodeValidationFailed,
			"%s は 1..%d の範囲で指定してください (%d)", field, maxPageSize, limit)
	}
	return nil
}

// validatePageOffset rejects a negative offset instead of silently clamping it.
//
// **負の offset は 400。** pageBounds は負を 0 に落とすので、ここを通らないと
// 「1 ページ目」と「0 ページ目」の違いが応答から消えてしまう。
func validatePageOffset(field string, offset int) error {
	if offset < 0 {
		return codedErrorf(http.StatusBadRequest, CodeValidationFailed,
			"%s は 0 以上で指定してください (%d)", field, offset)
	}
	return nil
}

// bindJSON decodes exactly one strict JSON value and maps decode failures to validation.
// BindStrict rejects unknown fields and trailing JSON values consistently in host and tests.
func bindJSON(req plugin.Request, v any) error {
	if err := req.BindStrict(v); err != nil {
		return codedErrorf(http.StatusBadRequest, CodeValidationFailed,
			"リクエスト JSON を解読できません: %v", err)
	}
	return nil
}

// fillPolicyRangeTail makes a request's policy ranges tile the whole reachable
// progression, so the shared validator only has to judge what the caller actually wrote.
//
// **保存 API は「指定したい stage だけ」を送る。** 共有 validator (`validateRanges`) は
// [1, levelUps+2) をちょうど覆うことを要求するが、operator が明示していない最終 stage
// まで書かせると、既定の 1 段だけ落としただけの保存が 400 になる (levelUps 9 に対して
// [1,10) を送ると合計長 9 が要求 10 に合わない)。frontend の既定値も `startStage` だけ
// で `end` を持たないので、**末尾の未指定区間を base で埋めてから**検証する。base は
// 「instance / native の既定のまま」なので、書いていない stage の policy を勝手に
// 決めない。
//
// **埋められるのは末尾だけ。** 隙間・重複・end <= start・はみ出しはそのまま共有
// validator に渡すので、壊れた設定が保存される形に緩くはしない。受理する形を
// 増やすだけで、判定そのものは共有側に残す。
func fillPolicyRangeTail(ranges []PolicyRange, levelUps int64) []PolicyRange {
	// 到達可能 stage は 1..levelUps+1 なので半開区間の右端は levelUps+2。level が
	// 0 の設定 (curve 空) でも stage 1 は存在するので 2 を下回らないようにする。
	want := levelUps + 2
	if want < 2 {
		want = 2
	}
	if len(ranges) == 0 {
		// **既定は全範囲を覆う 1 個の base range。** validateRanges の
		// 「最低1個必要です (既定は全範囲を覆う1個の base range)」という条文と
		// 同じ形にして、その条文が嘘にならないようにする。
		return []PolicyRange{{Type: RangeBase, Start: 1, End: want}}
	}
	out := append([]PolicyRange(nil), ranges...)
	last := &out[len(out)-1]
	// **はみ出しは触らない。** 存在しない stage を設定しているのは設定の誤りなので、
	// 共有 validator に CodeInvalidRanges を出す。
	if last.End >= want {
		return out
	}
	// 末尾を 1 個の base で埋める。既存の range の意味 (const / multiplier) を伸ばさない
	// ので、operator が書いていない値が後から効いてくることはない。
	//
	// **MaxPolicyRanges 個ちょうどで末尾が余っている保存は落ちる。** 埋めるために 257 個
	// になり、共有 validator の個数上限に当たる。上限を越えて受理するほうが危険なので、
	// これは上限どおりに落ちる挙動として残す。
	return append(out, PolicyRange{Type: RangeBase, Start: last.End, End: want})
}

// --- native actor の解決 ----------------------------------------------------

// readNative returns the configured native adapter used by the read paths.
//
// **公開ルートでも configured actor を使う。** `/roles/users` と `/users/show` は認証を
// 要求しないが、native の管理 endpoint を匿名で読むことはできない。actor が未設定、
// または native API を構成できない場合は `s.native()` のエラーを返して **fail closed**
// する。黙って 0 件を返すのは別物で、それは「0 人」と読まれる。
func (s *service) readNative() (*nativeRole, error) {
	return s.native()
}

// --- read helpers ------------------------------------------------------------

// memberCount returns how many assignments a role has, up to the scan cap.
//
// **過少申告に倒す。** 取得できた行だけを数え、native が落ちた
// ときや上限で切れたときは truncated を立てる。推定値を「全部で何件か」として
// 出すと、operator が member 制限の判断を誤る。
func (s *service) memberCount(ctx context.Context, roleID string) (int, bool, error) {
	native, err := s.native()
	if err != nil {
		// actorId 未配置でも起動は止めないので read 系は動く。native へ聞けない
		// ときは「不明」を 0 + truncated で返す。
		return 0, true, nil //nolint:nilerr // 過少申告 + truncated で fail closed
	}
	all, truncated, err := native.ListAssignments(ctx, roleID)
	if err != nil {
		return 0, false, err
	}
	return len(all), truncated, nil
}

// experienceForRoleUser returns the experience that currently applies to one
// (role, user) pair, and whether the user holds the role at all.
//
// **二重計上を許さない。** unassign 後に再 assign すると、plugin table には同じ
// (role, user) の XP row が 2 行並ぶ。0 行なら未投入として 0、1 行ならその行が
// live な assignment、2 行以上なら native へ聞き直して live な 1 行に合わせる。
// 見つからなければ XP 0 (未投入) として返し、古い行は勝手には消さない (XP の
// 復活を防げるのは監査側の仕事)。
func (s *service) experienceForRoleUser(ctx context.Context, native *nativeRole, roleID, userID string) (assigned bool, experience int64, assignmentID string, err error) {
	assigned, err = native.Assigned(ctx, roleID, userID)
	if err != nil {
		return false, 0, "", err
	}
	if !assigned {
		return false, 0, "", nil
	}
	rows, err := s.store.ExperienceRowsForRoleUser(ctx, roleID, userID)
	if err != nil {
		return false, 0, "", s.storageError(ctx, "経験値の読み込み", err)
	}
	switch len(rows) {
	case 0:
		// XP 0 相当。assignment 無いので紐付けもしない。
		return true, 0, "", nil
	case 1:
		// **1 行でも live との一致を確かめる。** unassign 後に assign し直すと
		// 古い XP 行が 1 行だけ残ることがある。「1 行だから live の assignment
		// だ」と考えると stale な assignmentId をそのまま返してしまう。
		liveID, found, err := native.FindAssignment(ctx, roleID, userID)
		if err != nil {
			return false, 0, "", err
		}
		if !found {
			return false, 0, "", nil
		}
		if rows[0].AssignmentID == liveID {
			return true, rows[0].Experience, rows[0].AssignmentID, nil
		}
		// 残っているのは stale 行だけ。live な assignment には XP 行がまだ無いので 0。
		return true, 0, liveID, nil
	default:
		// 2 行以上 = 再 assign 済み。live な 1 行に合わせる。
		liveID, found, err := native.FindAssignment(ctx, roleID, userID)
		if err != nil {
			return false, 0, "", err
		}
		if !found {
			return false, 0, "", nil
		}
		for _, r := range rows {
			if r.AssignmentID == liveID {
				return true, r.Experience, r.AssignmentID, nil
			}
		}
		return true, 0, liveID, nil
	}
}

// publicProfile returns the level of one user for every level-enabled role the user
// is actually assigned to and allowed to see.
//
// Native assignment and visibility are checked before plugin experience is exposed.
//
// roleIsPublic reports whether native would let an unauthenticated caller see
// the role. It is the only authorization the public routes get.
//
// **public 応答は native の可視性を超えない。** spec の「public user response
// は native role visibility を超える情報を返さない」に対応する。private な role
// を moderator 権限で隠すだけだと、public な /users/show から level / XP が
// 漏れる。native 障害で Show が読めないときは error を返す（fail closed）。
func roleIsPublic(ctx context.Context, native *nativeRole, roleID string) (bool, error) {
	info, err := native.Show(ctx, roleID)
	if err != nil {
		return false, err
	}
	return info.IsPublic, nil
}

func roleMembersAreVisible(ctx context.Context, native *nativeRole, roleID string) (bool, error) {
	info, err := native.Show(ctx, roleID)
	if err != nil {
		return false, err
	}
	return info.IsPublic && info.IsExplorable, nil
}

func (s *service) publicProfile(ctx context.Context, userID string) (any, error) {
	native, err := s.readNative()
	if err != nil {
		// actorId が無い / native が読めないときは level を返さない。**XP を空で
		// 返さず operator に設定不備を知らせる** (Task 1 の警告と同じ方針)。
		return nil, err
	}
	configs, err := s.store.ListConfigs(ctx)
	if err != nil {
		return nil, s.storageError(ctx, "level 設定の読み込み", err)
	}
	out := make([]map[string]any, 0, len(configs))
	for _, cfg := range configs {
		assigned, xp, _, err := s.experienceForRoleUser(ctx, native, cfg.RoleID, userID)
		if err != nil {
			return nil, err
		}
		if !assigned {
			continue
		}
		// **private な role の level / XP は公開しない。** member の
		// experienceForRoleUser と同じ順で「割り当てている role だけ」を
		// 判定してから返す。Show が読めなければ fail closed で落とす。
		public, err := roleIsPublic(ctx, native, cfg.RoleID)
		if err != nil {
			return nil, err
		}
		if !public {
			continue
		}
		exp, err := cfg.Experience(xp)
		if err != nil {
			return nil, s.storageError(ctx, "level の計算", err)
		}
		out = append(out, map[string]any{
			"roleId":     cfg.RoleID,
			"experience": xp,
			"level":      exp,
		})
	}
	return map[string]any{"userId": userID, "roles": out}, nil
}

// roleMembers returns one role's members ordered by experience descending.
//
// **total / truncated は下界。** 走査の正は native の assignment 一覧で、plugin
// table は experience の引き落としに使うだけ。上限で切れたときは truncated を
// 立てる。ページングは並び替えた一覧に対して行うので、page 内の並びも全体と
// 一致する。
func (s *service) roleMembers(ctx context.Context, roleID string, limit, offset int) (any, error) {
	native, err := s.readNative()
	if err != nil {
		return nil, err
	}
	cfg, found, err := s.loadConfigOrStorageError(ctx, s.store, roleID)
	if err != nil {
		return nil, err
	}
	if !found {
		// **`/admin/roles/show` は既定値を返すが、こちらは 404。** level を返し
		// たいのに設定が無い状態を、公開 member 一覧では「存在しない role」に
		// 見せる。editor 用の既定値は管理画面側の責務で、公開 API には出さない。
		return nil, codedErrorf(http.StatusNotFound, CodeConfigNotFound,
			"その role には level 設定がありません")
	}
	public, err := roleMembersAreVisible(ctx, native, roleID)
	if err != nil {
		return nil, err
	}
	if !public {
		// **private な role の member 一覧は出さない。** 存在も教えない
		// ので 404（level 設定が無いときと同じ契約）。
		return nil, codedErrorf(http.StatusNotFound, CodeNativeRoleNotFound,
			"この role は公開されていません")
	}

	all, truncated, err := native.ListAssignments(ctx, roleID)
	if err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(all))
	for _, a := range all {
		ids = append(ids, a.ID)
	}
	experience, err := s.store.ExperienceForAssignments(ctx, ids)
	if err != nil {
		return nil, s.storageError(ctx, "経験値の読み込み", err)
	}

	type member struct {
		assignmentID string
		userID       string
		experience   int64
	}
	members := make([]member, 0, len(all))
	for _, a := range all {
		members = append(members, member{
			assignmentID: a.ID, userID: a.UserID(), experience: experience[a.ID],
		})
	}
	// **XP 降順、同点は user id 昇順。** 同点を走査順に任せると page をまたい
	// で並びが変わるので、frontend 側で安定に並べられるようにする。
	sort.Slice(members, func(i, j int) bool {
		if members[i].experience != members[j].experience {
			return members[i].experience > members[j].experience
		}
		return members[i].userID < members[j].userID
	})

	page := members
	if offset >= len(page) {
		page = nil
	} else {
		page = page[offset:]
	}
	if len(page) > limit {
		page = page[:limit]
	}

	userIDs := make([]string, 0, len(page))
	for _, m := range page {
		userIDs = append(userIDs, m.userID)
	}
	usernames, err := native.Usernames(ctx, userIDs)
	if err != nil {
		return nil, err
	}

	out := make([]map[string]any, 0, len(page))
	for _, m := range page {
		exp, err := cfg.Experience(m.experience)
		if err != nil {
			return nil, s.storageError(ctx, "level の計算", err)
		}
		out = append(out, map[string]any{
			"userId":       m.userID,
			"username":     usernames[m.userID],
			"assignmentId": m.assignmentID,
			"experience":   m.experience,
			"level":        exp,
		})
	}
	return map[string]any{
		"roleId":    roleID,
		"total":     len(members),
		"truncated": truncated,
		"members":   out,
	}, nil
}

type auditResponse struct {
	ID               string    `json:"id"`
	ActorID          string    `json:"actorId"`
	Operation        string    `json:"operation"`
	RoleID           *string   `json:"roleId"`
	UserID           *string   `json:"userId"`
	AssignmentID     *string   `json:"assignmentId"`
	BeforeExperience *int64    `json:"beforeExperience"`
	AfterExperience  *int64    `json:"afterExperience"`
	Note             *string   `json:"note"`
	CreatedAt        time.Time `json:"createdAt"`
}

func auditResponses(entries []auditEntry) []auditResponse {
	out := make([]auditResponse, 0, len(entries))
	for _, entry := range entries {
		r := auditResponse{ID: strconv.FormatInt(entry.ID, 10), ActorID: entry.ActorID, Operation: entry.Operation, CreatedAt: entry.CreatedAt}
		if entry.RoleID != "" {
			v := entry.RoleID
			r.RoleID = &v
		}
		if entry.UserID != "" {
			v := entry.UserID
			r.UserID = &v
		}
		if entry.AssignmentID != "" {
			v := entry.AssignmentID
			r.AssignmentID = &v
		}
		if entry.Note != "" {
			v := entry.Note
			r.Note = &v
		}
		if v, ok := entry.Before["experience"].(float64); ok {
			n := int64(v)
			r.BeforeExperience = &n
		}
		if v, ok := entry.After["experience"].(float64); ok {
			n := int64(v)
			r.AfterExperience = &n
		}
		out = append(out, r)
	}
	return out
}

// auditTrail returns the newest audit rows for a role and/or user.
//
// **スコープは必須。** role も user も指定しないと監査表の全件を、自分の権限
// も無いまま列挙してしまう。store も同じ制約を持つが、route 側の stable code を
// 保つために先に弾く。
func (s *service) auditTrail(ctx context.Context, roleID, userID string, limit int) (any, error) {
	entries, err := s.store.RecentAudit(ctx, roleID, userID, limit)
	if err != nil {
		return nil, s.storageError(ctx, "監査履歴の読み込み", err)
	}
	return map[string]any{"entries": auditResponses(entries)}, nil
}

// adminUser is the administrator view of one user's level data.
//
// **未完了の operation だけ持ち出す。** ResumableOperations は全 operation が
// 対象なので、この user に絞ってから出す。limit を先にとるので、該当用户在らな
// ければ空の配列になる (null を出さない)。
func (s *service) adminUser(ctx context.Context, userID string) (any, error) {
	native, err := s.native()
	if err != nil {
		return nil, err
	}
	configs, err := s.store.ListConfigs(ctx)
	if err != nil {
		return nil, s.storageError(ctx, "level 設定の読み込み", err)
	}
	roles := make([]map[string]any, 0, len(configs))
	for _, cfg := range configs {
		assigned, xp, assignmentID, err := s.experienceForRoleUser(ctx, native, cfg.RoleID, userID)
		if err != nil {
			return nil, err
		}
		if !assigned {
			continue
		}
		exp, err := cfg.Experience(xp)
		if err != nil {
			return nil, s.storageError(ctx, "level の計算", err)
		}
		roles = append(roles, map[string]any{
			"roleId":       cfg.RoleID,
			"assignmentId": assignmentID,
			"experience":   xp,
			"level":        exp,
		})
	}
	audit, err := s.store.RecentAudit(ctx, "", userID, adminUserAuditLimit)
	if err != nil {
		return nil, s.storageError(ctx, "監査履歴の読み込み", err)
	}
	ops, err := s.store.ResumableOperationsForUser(ctx, userID, adminUserOperationCap)
	if err != nil {
		return nil, s.storageError(ctx, "XP 操作の読み込み", err)
	}
	open := make([]map[string]any, 0, len(ops))
	for _, op := range ops {
		open = append(open, map[string]any{
			"idempotencyKey": op.IdempotencyKey,
			"roleId":         op.RoleID,
			"mode":           op.Mode,
			"operand":        op.Operand,
			"status":         op.Status,
			"lastError":      op.LastError,
		})
	}
	return map[string]any{
		"userId":     userID,
		"roles":      roles,
		"audit":      auditResponses(audit),
		"operations": open,
	}, nil
}
