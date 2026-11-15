package rolelevel

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/elythia-network/elythia/plugin"
)

// maxResumeBatch bounds one reconciliation pass.
//
// **1 周の所要時間に上限を掛ける。** ジョブには1時間の上限 (#2658) があるので、
// 大量に残っていても1周で終わらせ、残りは次回に回す。
//
// **飢餓を起こさないために ctx を見る。** `ResumableOperations` は `updated_at ASC` で
// 先頭 N 件を返す。1周で 200 件が終われば後ろに進むが、**終わらない設定だと同じ 200 件
// を毎回やり直す**ので、1 周の途中で打ち切って次の周に進める必要がある。
const maxResumeBatch = 200

const (
	// orphanPageSize is one page of role_level_experience.
	orphanPageSize = 1000
)

// reconcileSource names who started a reconciliation pass.
const (
	reconcileSourceRoute = "route"
	reconcileSourceCron  = "cron"
)

// reconcileTrigger says who or what started a reconciliation pass.
//
// **cron には要求者が居ない。** だから `Source` を必ず残す。監査行を `actorId` だけで
// 表すと、cron が消したのか運営者が消したのか区別がつかなくなる。
type reconcileTrigger struct {
	// Source is `route` or `cron`.
	Source string
	// ActorID is the operator on the route path, empty on the cron path.
	ActorID string
}

// orphanRoleReport is one role's orphan summary.
type orphanRoleReport struct {
	// Tracked is how many XP rows the role has.
	Tracked int `json:"tracked"`
	// Orphans is how many of them have no live native assignment.
	Orphans int `json:"orphans"`
	// Prunable is how many of those are also past the retention. **Orphans ではない。**
	// `DeleteOrphanExperience` は `orphaned_at < cutoff` の行しか消さないので、Orphans
	// を「消える行」に見せる嘘になる。
	Prunable int `json:"prunable"`
	// OrphanAssignmentIDs lets the administrator screen see exactly which rows are
	// waiting for the retention period to pass.
	OrphanAssignmentIDs []string `json:"orphanAssignmentIds"`
}

// retentionCutoff is the boundary `DeleteOrphanExperience` deletes at.
//
// **起点を DB の時計に合わせる。** `orphaned_at` は PostgreSQL の `now()` で押されて
// いるので、比較も `now()` に対して行う。host の時計とのずれが残ると、保持期間内の行を
// 消すか、期間を経た行を残すかが host 依存で揺れる。
func (s *service) retentionCutoff(ctx context.Context) (time.Time, error) {
	now, err := s.store.DBNow(ctx)
	if err != nil {
		return time.Time{}, s.storageError(ctx, "保持期間の起点の読み込み", err)
	}
	return now.Add(-time.Duration(s.cfg.OrphanRetentionDays) * 24 * time.Hour), nil
}

// jobs registers the background work.
//
// **3つに分ける。** 停止した操作の再開 (10分ごと)、orphan の判定 (1日1回)、orphan の
// 削除 (1日1回) は周期も失敗時の意味が違うので、1つの job に押し込まない。
func jobs(pctx plugin.Context, j plugin.Jobs) error {
	svc, err := newService(pctx)
	if err != nil {
		return err
	}
	cronTrigger := reconcileTrigger{Source: reconcileSourceCron, ActorID: svc.cfg.ActorID}

	j.Handle("resume-operations", func(ctx context.Context, _ json.RawMessage) error {
		return svc.ResumePendingOperations(ctx)
	})
	j.Schedule(svc.cfg.ReconcileCron, "resume-operations", nil)

	j.Handle("reconcile-orphans", func(ctx context.Context, _ json.RawMessage) error {
		report, err := svc.OrphanReport(ctx)
		if err != nil {
			return err
		}
		svc.log.Info("role-level: orphan を確認しました", "report", report)
		return nil
	})
	j.Schedule(svc.cfg.OrphanCron, "reconcile-orphans", nil)

	j.Handle("prune-orphans", func(ctx context.Context, _ json.RawMessage) error {
		n, err := svc.PruneOrphans(ctx, cronTrigger)
		if err != nil {
			return err
		}
		if n > 0 {
			svc.log.Info("role-level: 保持期間を超えた orphan の XP を削除しました", "count", n)
		}
		return nil
	})
	j.Schedule(svc.cfg.PruneCron, "prune-orphans", nil)

	return nil
}

// ResumePendingOperations replays operations that stopped mid-flight with the same
// idempotency key.
//
// **何も削除しない。** native API の失敗で operator のリクエストを失うのは、
// 「止まった」と「失敗した」の区別が分からなくなる。
func (s *service) ResumePendingOperations(ctx context.Context) error {
	ops, err := s.store.ResumableOperations(ctx, maxResumeBatch)
	if err != nil {
		return s.storageError(ctx, "XP 操作の読み込み", err)
	}
	var firstErr error
	resumed := 0
	for _, op := range ops {
		// **ctx を尊重する。** cron job には1時間の上限があり、host は越えると
		// 待つのをやめる (`plugin/jobs.go`)。見ずに回すと 1 周が終わらず、次の周が
		// また同じ先頭から始める。
		if err := ctx.Err(); err != nil {
			s.log.Warn("role-level: 保留中の XP 操作の再開を時間切れで打ち切りました",
				"resumed", resumed, "total", len(ops))
			if firstErr != nil {
				return firstErr
			}
			return ctx.Err()
		}
		if _, err := s.resume(ctx, op, false); err != nil {
			s.log.Warn("role-level: 保留中の XP 操作を再開できませんでした",
				"idempotencyKey", op.IdempotencyKey, "status", op.Status, "err", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		resumed++
	}
	s.log.Info("role-level: 保留中の XP 操作を再開しました",
		"resumed", resumed, "total", len(ops))
	return firstErr
}

// liveAssignmentIDs collects every native assignment id the plugin can see.
//
// **truncated を捨てない。** `ListAssignments` は `assignmentScanPages` ページで打ち
// 切るので、2 番目の戻り値が true だと「まだ member がいる」可能性が残る。**その
// 状態で行を消すと、生きている member の XP を orphan として消す。** なので全体を
// 失敗にする (`memberCount` と同じ fail closed 方針、`CodeAssignmentScanExhausted` を
// 同じ code で返す)。
func (s *service) liveAssignmentIDs(ctx context.Context) (map[string]bool, error) {
	native, err := s.native()
	if err != nil {
		return nil, err
	}
	roleIDs, err := s.reconcileRoles(ctx)
	if err != nil {
		return nil, err
	}
	live := map[string]bool{}
	for _, roleID := range roleIDs {
		all, truncated, err := native.ListAssignments(ctx, roleID)
		if err != nil {
			// A deleted native role has no live assignments. This is the only
			// native error safe to treat as an empty result; all other failures
			// must fail closed to avoid orphaning live XP.
			_, code := plugin.ExtractStatusError(err)
			if code == CodeNativeRoleNotFound {
				continue
			}
			// **1つでも失敗したら全体を失敗にする。** 一部だけ返すと、見なかった
			// role の XP 行を全部 orphan と誤判定して消してしまう。
			return nil, err
		}
		if truncated {
			return nil, codedErrorf(http.StatusConflict, CodeAssignmentScanExhausted,
				"%s の member が多すぎて (上限 %d ページ) assignment を全部確認できません。"+
					"orphan を判定する前に assignmentScanPages を上げるか、XP を個別に変更してください",
				roleID, s.cfg.AssignmentScanPages)
		}
		for _, a := range all {
			live[a.ID] = true
		}
	}
	return live, nil
}

// reconcileRoles is every role a pass has to judge: level-enabled roles, plus roles
// that still own XP rows.
//
// **`ListConfigs` だけでは足りない。** `DeleteConfig` は XP を消さずに「保持期間を越えたら
// Task 11 の prune job が削除する」契約で残す。設定だけを見るとその契約の受け手が居ない
// role ができて、監査用の行が永久に残る。
func (s *service) reconcileRoles(ctx context.Context) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	add := func(id string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	configs, err := s.store.ListConfigs(ctx)
	if err != nil {
		return nil, s.storageError(ctx, "level 設定の読み込み", err)
	}
	for _, cfg := range configs {
		add(cfg.RoleID)
	}
	roleIDs, err := s.store.RolesWithExperience(ctx)
	if err != nil {
		return nil, s.storageError(ctx, "経験値を持つ role の読み込み", err)
	}
	for _, id := range roleIDs {
		add(id)
	}
	// 順序を固定する。job のログと report の出力を安定させる。
	sort.Strings(out)
	return out, nil
}

// allExperienceRows walks every XP row of one role using a deterministic keyset.
//
// **どのページが失敗しても storage の stable code で返す。** 素の error のままだと
// host が 500 に丸め、frontend が「readiness ではなく XP 読み込みの障害」と判断でき
// ない。1周の途中 page で落ちても印は1行も付けないので、同じ code を返してよい。
func (s *service) allExperienceRows(ctx context.Context, roleID string) ([]experienceRow, error) {
	out := []experienceRow{}
	rows, err := s.store.ExperienceRowsForRole(ctx, roleID, orphanPageSize, 0)
	if err != nil {
		return nil, s.storageError(ctx, "経験値行の読み込み", err)
	}
	out = append(out, rows...)
	for len(rows) == orphanPageSize {
		last := rows[len(rows)-1]
		rows, err = s.store.ExperienceRowsForRoleAfter(ctx, roleID, orphanPageSize, last.Experience, last.AssignmentID)
		if err != nil {
			return nil, s.storageError(ctx, "経験値行の続きの読み込み", err)
		}
		out = append(out, rows...)
	}
	return out, nil
}

// reconcileOrphans verifies both sides of the comparison before changing any
// marker. A truncated or failed native/XP scan therefore cannot mark live rows
// as orphaned. MarkOrphaned preserves an existing timestamp and live rows are
// cleared in the same pass, so one role's markers are decided together.
func (s *service) reconcileOrphans(ctx context.Context) (map[string]bool, []string, map[string][]experienceRow, error) {
	live, err := s.liveAssignmentIDs(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	roleIDs, err := s.reconcileRoles(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	rowsByRole := make(map[string][]experienceRow, len(roleIDs))
	// Complete every XP scan before applying any marker update.
	for _, roleID := range roleIDs {
		rows, err := s.allExperienceRows(ctx, roleID)
		if err != nil {
			return nil, nil, nil, err
		}
		rowsByRole[roleID] = rows
	}
	for _, roleID := range roleIDs {
		missing := []string{}
		present := []string{}
		for i := range rowsByRole[roleID] {
			r := &rowsByRole[roleID][i]
			if live[r.AssignmentID] {
				present = append(present, r.AssignmentID)
				r.OrphanedAt = nil
			} else {
				missing = append(missing, r.AssignmentID)
			}
		}
		marked, err := s.store.ApplyOrphanMarkers(ctx, roleID, missing, present)
		if err != nil {
			return nil, nil, nil, s.storageError(ctx, "orphan の記録", err)
		}
		for i := range rowsByRole[roleID] {
			r := &rowsByRole[roleID][i]
			if markedAt, ok := marked[r.AssignmentID]; ok {
				r.OrphanedAt = &markedAt
			}
		}
	}
	return live, roleIDs, rowsByRole, nil
}

// OrphanReport counts the XP rows whose native assignment is gone, per role.
//
// **削除はしない。** orphan 判定と削除を分けるのは、判定が1回失敗したときに
// まとめて消えるのを避けるため。ただし、検証済みの判定結果は retention の起点として保存する。
func (s *service) OrphanReport(ctx context.Context) (map[string]any, error) {
	_, roleIDs, rowsByRole, err := s.reconcileOrphans(ctx)
	if err != nil {
		return nil, err
	}
	cutoff, err := s.retentionCutoff(ctx)
	if err != nil {
		return nil, err
	}
	report := make(map[string]orphanRoleReport, len(roleIDs))
	for _, roleID := range roleIDs {
		rows := rowsByRole[roleID]
		entry := orphanRoleReport{Tracked: len(rows), OrphanAssignmentIDs: []string{}}
		for _, r := range rows {
			if r.OrphanedAt != nil {
				entry.Orphans++
				if r.OrphanedAt.Before(cutoff) {
					entry.Prunable++
				}
				entry.OrphanAssignmentIDs = append(entry.OrphanAssignmentIDs, r.AssignmentID)
			}
		}
		sort.Strings(entry.OrphanAssignmentIDs)
		report[roleID] = entry
	}
	return map[string]any{"roles": report}, nil
}

// PruneOrphans deletes XP rows that have been orphans longer than the retention.
//
// **保持期間内の行は残す。** 監査のために残すのが目的で、native API の障害で一度に
// 消えると operator の記録が失われる。
func (s *service) PruneOrphans(ctx context.Context, trigger reconcileTrigger) (int64, error) {
	_, roleIDs, rowsByRole, err := s.reconcileOrphans(ctx)
	if err != nil {
		return 0, err
	}
	cutoff, err := s.retentionCutoff(ctx)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, roleID := range roleIDs {
		rows := rowsByRole[roleID]
		orphans := make([]string, 0, len(rows))
		for _, r := range rows {
			if r.OrphanedAt != nil {
				orphans = append(orphans, r.AssignmentID)
			}
		}
		n, err := s.pruneRoleOrphans(ctx, roleID, orphans, cutoff, trigger)
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

// pruneRoleOrphans deletes one role's expired orphan XP rows and writes the audit row
// on the same transaction, returning how many rows went away.
//
// **削除と監査を 1 つの transaction に決める。** 別々に書くと、監査の書き込みだけが
// 失敗したときに「XP は消えたのに記録が無い」状態が残る。XP の削除は本 plugin で唯一
// 不可逆の操作なので、記録の欠落をそのまま受け入れる理由がない。`RETURNING` で消えた行
// そのものを受け取るので、監査には件数だけでなく experience まで残せる。
//
// **1 行も消えなかったら何も commit しない。** 消える行が無い role で transaction を commit
// すると「何もしなかった」ことが成功した記録として残ってしまう。
func (s *service) pruneRoleOrphans(ctx context.Context, roleID string, orphanIDs []string,
	cutoff time.Time, trigger reconcileTrigger,
) (int64, error) {
	if len(orphanIDs) == 0 {
		return 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, s.storageError(ctx, "orphan の削除 transaction の開始", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	removed, err := s.store.DeleteOrphanExperienceRemoved(ctx, tx, roleID, orphanIDs, cutoff)
	if err != nil {
		return 0, s.storageError(ctx, "orphan の削除", err)
	}
	if len(removed) == 0 {
		// 保持期間内の行しか残っていなかった、または既に別の job が消していた。
		// 監査に書く価値が無いので何も commit しない。
		return 0, nil
	}
	rows := make([]map[string]any, 0, len(removed))
	for _, d := range removed {
		rows = append(rows, map[string]any{
			"assignmentId": d.AssignmentID,
			"userId":       d.UserID,
			"experience":   d.Experience,
		})
	}
	// **消した role ごとに必ず監査行を残す。** XP 行の削除は本 plugin で唯一
	// 不可逆の操作で、`role_level_audit` に残さなければ「誰が消したか」を答えられない。
	// assignment を 1 行ずつ書くと監査表が行数だけ増えるので、1 role 1 行に畳む。
	if err := s.store.InsertAudit(ctx, tx, auditEntry{
		ActorID:   trigger.ActorID,
		Operation: "prune-orphans",
		RoleID:    roleID,
		Note: truncate("保持期間 ("+strconv.Itoa(s.cfg.OrphanRetentionDays)+
			"日) を超えた orphan の XP 行を削除 (source="+trigger.Source+")", 500),
		Before: map[string]any{"deleted": len(removed), "rows": rows},
		After:  map[string]any{"deleted": len(removed)},
	}); err != nil {
		return 0, s.storageError(ctx, "削除の監査記録", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, s.storageError(ctx, "orphan の削除 transaction の commit", err)
	}
	committed = true
	return int64(len(removed)), nil
}

// The modes `POST /admin/reconcile` accepts. **cron の job 名と揃える** — 別名を
// 増やすと「手動でどの名前を入れればよいか」が分からなくなる。
const (
	reconcileModeResume  = "resume-operations"
	reconcileModeOrphans = "reconcile-orphans"
	reconcileModePrune   = "prune-orphans"
)

// RunReconcile runs one reconciliation pass on demand, for `POST /admin/reconcile`.
//
// **job と同じ関数を呼ぶだけで、専用の実装は書かない。** route と cron で経路が
// 分けると、「手動では直るが自動では直らない」が静かに残る。
//
// **trigger を落とさない。** route は `requireAdmin` を通った要求者を持っている。これを
// 捨てると監査行の `actorId` が空になり、cron と手動の区別がつかなくなる。
func (s *service) RunReconcile(ctx context.Context, mode string, trigger reconcileTrigger) (map[string]any, error) {
	run := map[string]func(context.Context) (any, error){
		reconcileModeResume: func(ctx context.Context) (any, error) {
			return nil, s.ResumePendingOperations(ctx)
		},
		reconcileModeOrphans: func(ctx context.Context) (any, error) {
			return s.OrphanReport(ctx)
		},
		reconcileModePrune: func(ctx context.Context) (any, error) {
			n, err := s.PruneOrphans(ctx, trigger)
			if err != nil {
				return nil, err
			}
			return map[string]any{"pruned": n}, nil
		},
	}

	var order []string
	var steps []map[string]any
	if mode == reconcileAllMode {
		// 全部をこの順に。**判定 → 削除** の順が崩れると、削除した行を次の判定で
		// 見直すことになる。
		order = []string{reconcileModeResume, reconcileModeOrphans, reconcileModePrune}
	} else {
		if _, ok := run[mode]; !ok {
			// **未知の mode は黙って無視しない。** 無視すると運営者は「直した」と
			// 思って画面を閉じる。code は Task 10 が定義済みの `CodeUnknownMode` を
			// 使う (汎用の validation とは別件として扱う)。
			return nil, codedErrorf(http.StatusBadRequest, CodeUnknownMode,
				"mode %q は %s|%s|%s|%s のいずれかです", mode, reconcileModeResume,
				reconcileModeOrphans, reconcileModePrune, reconcileAllMode)
		}
		order = []string{mode}
	}

	for _, name := range order {
		result, err := run[name](ctx)
		if err != nil {
			return nil, err
		}
		steps = append(steps, map[string]any{"mode": name, "result": result})
	}
	return map[string]any{"steps": steps}, nil
}
