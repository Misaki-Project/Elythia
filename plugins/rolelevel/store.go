package rolelevel

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/shiroha-a/mk/plugin"
)

// queryer is satisfied by both *sql.DB and *sql.Tx, so a write that must be atomic
// with the audit row can run inside a transaction while the read paths stay simple.
type queryer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// store is the plugin's own PostgreSQL access. Every method is scoped to the
// plugin schema; native ids are opaque text and there is no foreign key into
// mk-go's tables.
type store struct{ db *sql.DB }

// experienceRow is one row of role_level_experience.
type experienceRow struct {
	AssignmentID string
	RoleID       string
	UserID       string
	Experience   int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
	OrphanedAt   *time.Time
}

// operation is one row of role_level_operation: the hand-off between native
// assignment creation and the plugin's own XP write.
type operation struct {
	IdempotencyKey string
	ActorID        string
	UserID         string
	RoleID         string
	Mode           string
	// Operand is a finite decimal. multiplier の raw factor (1.5 = ×1.5) を保持する
	// 必要があるので整数にはしない (DB 列も double precision)。
	Operand float64
	// DesiredExp is the integer XP the operation settled on, filled on completion.
	DesiredExp        *int64
	AssignmentID      string
	AssignmentCreated bool
	Status            string
	LastError         string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// auditEntry is one row of role_level_audit. Before / After are stored as jsonb so
// a future field can be added without a migration.
type auditEntry struct {
	ID           int64
	ActorID      string
	Operation    string
	RoleID       string
	UserID       string
	AssignmentID string
	Note         string
	Before       map[string]any
	After        map[string]any
	CreatedAt    time.Time
}

// orphanDeletion is one XP row a prune actually removed, read back from the
// `DELETE ... RETURNING` that deleted it.
//
// **行そのものを返す。** 件数だけだと「どの XP 行が消えたか」を答えられない。監査行の
// before に消えた experience を残すことで、後から「消えた XP は何だったか」を答えられる
// ようになる。`role_id` は削除条件になっているし 1 つの呼び出しで 1 role だけなので
// 持たない。
type orphanDeletion struct {
	// AssignmentID is the native assignment the row belonged to.
	AssignmentID string
	// UserID is the member who held the assignment.
	UserID string
	// Experience is the XP the row held right before the delete.
	Experience int64
}

// storageError wraps a storage failure with a stable code and logs it.
//
// **内部の SQL エラー文字列を呼出し側に渡さない。** host はこのメッセージをそのまま
// クライアントへ返すので、schema 名やカラム名が外に出る。
func (s *service) storageError(ctx context.Context, what string, err error) error {
	s.log.Error("role-level: "+what, "err", err)
	return codedErrorf(500, CodeStorageFailed, "%s に失敗しました", what)
}

// loadConfigOrStorageError is the read helper the routes use.
func (s *service) loadConfigOrStorageError(ctx context.Context, st *store, roleID string) (Config, bool, error) {
	cfg, found, err := st.LoadConfig(ctx, roleID)
	if err != nil {
		return Config{}, false, s.storageError(ctx, "level 設定の読み込み", err)
	}
	return cfg, found, nil
}

// UpsertConfig creates or updates one role's level configuration.
//
// expectRevision is the revision the operator last saw. 0 means "must not exist";
// a mismatch is a conflict. This is the only protection against two administrators
// silently overwriting each other's curve.
//
// `INSERT` は作成を、revision 条件付き `UPDATE` は更新を担当する。既存行に対する
// revision 0、revision 不一致、行の不存在は衝突として扱う。
func (s *store) UpsertConfig(ctx context.Context, cfg Config, expectRevision int64) (Config, error) {
	return s.upsertConfig(ctx, s.db, cfg, expectRevision)
}

// UpsertConfigTx is the transactional form used by the configuration routes.
func (s *store) UpsertConfigTx(ctx context.Context, q queryer, cfg Config, expectRevision int64) (Config, error) {
	return s.upsertConfig(ctx, q, cfg, expectRevision)
}

func (s *store) upsertConfig(ctx context.Context, q queryer, cfg Config, expectRevision int64) (Config, error) {
	curvePayload, err := json.Marshal(cfg.ExperienceCurve)
	if err != nil {
		return Config{}, fmt.Errorf("rolelevel: experience_curve を JSON 化できません: %w", err)
	}
	rangesPayload, err := json.Marshal(cfg.PolicyRanges)
	if err != nil {
		return Config{}, fmt.Errorf("rolelevel: policy_ranges を JSON 化できません: %w", err)
	}
	now := time.Now().UTC()

	if expectRevision == 0 {
		saved, err := scanConfig(q.QueryRowContext(ctx, `
			INSERT INTO role_level_config
				(role_id, base_level, experience_curve, policy_ranges, revision, created_at, updated_at, updated_by)
			VALUES ($1, $2, $3, $4, 1, $5, $5, $6)
			ON CONFLICT (role_id) DO NOTHING
			RETURNING role_id, base_level, experience_curve, policy_ranges, revision, created_at, updated_at, updated_by
		`, cfg.RoleID, cfg.BaseLevel, curvePayload, rangesPayload, now, cfg.UpdatedBy))
		if err != nil {
			// ON CONFLICT DO NOTHING leaves the transaction usable, so the existing
			// row can be read here without turning a duplicate create into a 500.
			if errors.Is(err, sql.ErrNoRows) {
				existing, found, lerr := loadConfigRow(ctx, q, cfg.RoleID)
				if lerr != nil {
					return Config{}, lerr
				}
				if found {
					return Config{}, fmt.Errorf("%w (現在の revision は %d です)",
						codedErrorf(409, CodeConfigConflict, "この role には既に level 設定があります"), existing.Revision)
				}
			}
			return Config{}, err
		}
		return saved, nil
	} else {
		saved, err := scanConfig(q.QueryRowContext(ctx, `
			UPDATE role_level_config SET
				base_level       = $2,
				experience_curve = $3,
				policy_ranges    = $4,
				revision         = revision + 1,
				updated_at       = $5,
				updated_by       = $6
			WHERE role_id = $1 AND revision = $7
			RETURNING role_id, base_level, experience_curve, policy_ranges, revision, created_at, updated_at, updated_by
		`, cfg.RoleID, cfg.BaseLevel, curvePayload, rangesPayload, now, cfg.UpdatedBy, expectRevision))
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return Config{}, codedErrorf(409, CodeConfigConflict,
					"level 設定は他の操作で更新されています。読み直して保存してください")
			}
			return Config{}, err
		}
		return saved, nil
	}
}

// LoadConfig returns one role's configuration. found is false when there is none.
//
// **experience_curve と policy_ranges は別カラムなので別々に Scan する。** 一つの
// jsonb に縦に積むと、どちらかが壊れたときにどちらの壊れか分からない。
func (s *store) LoadConfig(ctx context.Context, roleID string) (Config, bool, error) {
	return loadConfigRow(ctx, s.db, roleID)
}

func loadConfigRow(ctx context.Context, q queryer, roleID string) (Config, bool, error) {
	var (
		cfg    Config
		curve  []byte
		ranges []byte
	)
	err := q.QueryRowContext(ctx, `
		SELECT role_id, base_level, experience_curve, policy_ranges, revision, created_at, updated_at, updated_by
		FROM role_level_config WHERE role_id = $1
	`, roleID).Scan(&cfg.RoleID, &cfg.BaseLevel, &curve, &ranges, &cfg.Revision,
		&cfg.CreatedAt, &cfg.UpdatedAt, &cfg.UpdatedBy)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Config{}, false, nil
		}
		return Config{}, false, err
	}
	if err := json.Unmarshal(curve, &cfg.ExperienceCurve); err != nil {
		return Config{}, false, fmt.Errorf("rolelevel: %s の experience_curve を読み込めません: %w", roleID, err)
	}
	if err := json.Unmarshal(ranges, &cfg.PolicyRanges); err != nil {
		return Config{}, false, fmt.Errorf("rolelevel: %s の policy_ranges を読み込めません: %w", roleID, err)
	}
	return cfg, true, nil
}

// LoadConfigsForRoles returns the configurations of the given roles in one query.
//
// policy解決は1リクエストで複数の role に触ることがあるので、1件ずつ引くと
// round trip が role 数だけ増える。
func (s *store) LoadConfigsForRoles(ctx context.Context, roleIDs []string) (map[string]Config, error) {
	out := map[string]Config{}
	if len(roleIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT role_id, base_level, experience_curve, policy_ranges, revision, created_at, updated_at, updated_by
		FROM role_level_config WHERE role_id = ANY($1)
	`, roleIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // 読み取りのみ
	for rows.Next() {
		cfg, err := scanConfig(rows)
		if err != nil {
			return nil, err
		}
		out[cfg.RoleID] = cfg
	}
	return out, rows.Err()
}

// ListConfigs returns every level-enabled role, ordered by role id.
func (s *store) ListConfigs(ctx context.Context) ([]Config, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT role_id, base_level, experience_curve, policy_ranges, revision, created_at, updated_at, updated_by
		FROM role_level_config ORDER BY role_id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // 読み取りのみ
	out := []Config{}
	for rows.Next() {
		cfg, err := scanConfig(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, cfg)
	}
	return out, rows.Err()
}

// rowScanner is satisfied by *sql.Row and *sql.Rows.
type rowScanner interface{ Scan(dest ...any) error }

func scanConfig(sc rowScanner) (Config, error) {
	var (
		cfg    Config
		curve  []byte
		ranges []byte
	)
	if err := sc.Scan(&cfg.RoleID, &cfg.BaseLevel, &curve, &ranges, &cfg.Revision,
		&cfg.CreatedAt, &cfg.UpdatedAt, &cfg.UpdatedBy); err != nil {
		return Config{}, err
	}
	if err := json.Unmarshal(curve, &cfg.ExperienceCurve); err != nil {
		return Config{}, fmt.Errorf("rolelevel: %s の experience_curve を読み込めません: %w", cfg.RoleID, err)
	}
	if err := json.Unmarshal(ranges, &cfg.PolicyRanges); err != nil {
		return Config{}, fmt.Errorf("rolelevel: %s の policy_ranges を読み込めません: %w", cfg.RoleID, err)
	}
	return cfg, nil
}

// DeleteConfig removes one role's level configuration.
//
// **XP の行は消さない。** level 設定を消したあとで XP まで消すと、監査に必要な
// 記録が operator の1操作で消える。行は orphan として保持期間まで残り、prune job が
// 削除する。
func (s *store) DeleteConfig(ctx context.Context, roleID string, expectRevision int64) (bool, error) {
	return s.deleteConfig(ctx, s.db, roleID, expectRevision)
}

// DeleteConfigTx is the transactional form used by the configuration routes.
func (s *store) DeleteConfigTx(ctx context.Context, q queryer, roleID string, expectRevision int64) (bool, error) {
	return s.deleteConfig(ctx, q, roleID, expectRevision)
}

func (s *store) deleteConfig(ctx context.Context, q queryer, roleID string, expectRevision int64) (bool, error) {
	res, err := q.ExecContext(ctx,
		`DELETE FROM role_level_config WHERE role_id = $1 AND revision = $2`, roleID, expectRevision)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// ExperienceForAssignments returns the stored experience of each assignment.
//
// **引数が空なら空 map を返す。** `IN ()` は PostgreSQL の構文エラーになるので、呼び出し
// 側を信用せず自分で守る。
func (s *store) ExperienceForAssignments(ctx context.Context, assignmentIDs []string) (map[string]int64, error) {
	out := map[string]int64{}
	if len(assignmentIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT assignment_id, experience FROM role_level_experience
		WHERE assignment_id IN (`+placeholders(len(assignmentIDs))+`)
	`, toAny(assignmentIDs)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // 読み取りのみ
	for rows.Next() {
		var id string
		var xp int64
		if err := rows.Scan(&id, &xp); err != nil {
			return nil, err
		}
		out[id] = xp
	}
	return out, rows.Err()
}

// ExperienceRowsForRoleUser returns every XP row of one (role, user) pair.
//
// 2行以上あるのは「unassign 後に再 assign した」状態なので、live な assignment を
// native へ聞き直して newest 以外を無効にする必要がある (XP が復活してはいけない)。
func (s *store) ExperienceRowsForRoleUser(ctx context.Context, roleID, userID string) ([]experienceRow, error) {
	return s.queryExperience(ctx, `
		SELECT assignment_id, role_id, user_id, experience, created_at, updated_at, orphaned_at
		FROM role_level_experience
		WHERE role_id = $1 AND user_id = $2
		ORDER BY created_at DESC, assignment_id DESC
	`, roleID, userID)
}

// ExperienceRowsForRole returns one page of a role's XP rows, ordered by experience
// descending.
func (s *store) ExperienceRowsForRole(ctx context.Context, roleID string, limit, offset int) ([]experienceRow, error) {
	return s.queryExperience(ctx, `
		SELECT assignment_id, role_id, user_id, experience, created_at, updated_at, orphaned_at
		FROM role_level_experience
		WHERE role_id = $1
		ORDER BY experience DESC, assignment_id ASC
		LIMIT $2 OFFSET $3
	`, roleID, limit, offset)
}

// ExperienceRowsForRoleAfter returns the next keyset page after the supplied
// (experience, assignment_id) cursor. The ordering matches ExperienceRowsForRole
// and is stable even when rows have equal experience values.
func (s *store) ExperienceRowsForRoleAfter(ctx context.Context, roleID string, limit int, afterExperience int64, afterAssignmentID string) ([]experienceRow, error) {
	return s.queryExperience(ctx, `
		SELECT assignment_id, role_id, user_id, experience, created_at, updated_at, orphaned_at
		FROM role_level_experience
		WHERE role_id = $1
		  AND (experience < $2 OR (experience = $2 AND assignment_id > $3))
		ORDER BY experience DESC, assignment_id ASC
		LIMIT $4
	`, roleID, afterExperience, afterAssignmentID, limit)
}

// RolesWithExperience returns every role id that still owns XP rows, ascending.
//
// **level 設定の無い role も返す。** `DeleteConfig` は XP の行を消さず「保持期間を
// 越えたら Task 11 の prune job が削除する」契約で残す。`ListConfigs` から組み立てると
// その契約の受け手が居ない role ができて、行が永久に残る。
func (s *store) RolesWithExperience(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT role_id FROM role_level_experience ORDER BY role_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // 読み取りのみ
	out := []string{}
	for rows.Next() {
		var roleID string
		if err := rows.Scan(&roleID); err != nil {
			return nil, err
		}
		out = append(out, roleID)
	}
	return out, rows.Err()
}

func (s *store) queryExperience(ctx context.Context, query string, args ...any) ([]experienceRow, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // 読み取りのみ
	out := []experienceRow{}
	for rows.Next() {
		var r experienceRow
		if err := rows.Scan(&r.AssignmentID, &r.RoleID, &r.UserID, &r.Experience,
			&r.CreatedAt, &r.UpdatedAt, &r.OrphanedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetExperience writes one assignment's experience. q may be a transaction, so the
// XP write, the audit row and the operation status can commit together.
func (s *store) SetExperience(ctx context.Context, q queryer, row experienceRow) error {
	res, err := q.ExecContext(ctx, `
		INSERT INTO role_level_experience (assignment_id, role_id, user_id, experience, created_at, updated_at, orphaned_at)
		VALUES ($1, $2, $3, $4, now(), now(), NULL)
		ON CONFLICT (assignment_id) DO UPDATE SET
			experience = EXCLUDED.experience,
			orphaned_at = NULL,
			updated_at = now()
		WHERE role_level_experience.role_id = EXCLUDED.role_id
			AND role_level_experience.user_id = EXCLUDED.user_id

	`, row.AssignmentID, row.RoleID, row.UserID, row.Experience)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("rolelevel: assignment %s の experience を保存できませんでした (role/user mismatch)", row.AssignmentID)
	}
	return nil
}

// orphanMarkerBatch bounds one `MarkOrphaned` / `ClearOrphaned` statement.
//
// **IN 節を無制限に伸ばさない。** bind parameter は PostgreSQL で65535個までしか
// 渡せないので、orphan 候補が 6万5千行を超える role があると 1 文では書けず
// reconciliation 全体が止まる。100件ずつなら候補が何行でも文の長さは同じ。
const orphanMarkerBatch = 100

// MarkOrphaned stamps rows that are absent from the verified native assignment
// scan. Existing timestamps are deliberately preserved so every reconciliation
// pass does not restart the retention period.
//
// **100件ずつに刻んで更新する。** `DeleteOrphanExperienceRemoved` と同じ方針で、
// 1 role の XP が 6万5千行を超えても1文に収まる。返るのは DB が押した
// `orphaned_at` なので、呼び出し側は保持期間の起点としてそのまま使える。
//
// **分割しても最初の 1 押しだけを残す。** `COALESCE(orphaned_at, now())` が
// 保持期間の起点を守る。返す map も、後から来た値で上書きしない。
//
// **q で受ける。** 分割した全文を 1 つの transaction に収めるのは呼び出し側の責務で
// あり (`ApplyOrphanMarkers`)、文が抜けた瞬間に「印は全部の行に載った」とは言えなく
// なるから (`ApplyOrphanMarkers` を参照)。
func (s *store) MarkOrphaned(ctx context.Context, q queryer, roleID string, assignmentIDs []string) (map[string]time.Time, error) {
	marked := make(map[string]time.Time, len(assignmentIDs))
	if len(assignmentIDs) == 0 {
		return marked, nil
	}
	for start := 0; start < len(assignmentIDs); start += orphanMarkerBatch {
		end := start + orphanMarkerBatch
		if end > len(assignmentIDs) {
			end = len(assignmentIDs)
		}
		rows, err := q.QueryContext(ctx, `
			UPDATE role_level_experience
			SET orphaned_at = COALESCE(orphaned_at, now())
			WHERE role_id = $1 AND assignment_id IN (`+placeholdersFrom(2, end-start)+`)
			RETURNING assignment_id, orphaned_at
		`, append([]any{roleID}, toAny(assignmentIDs[start:end])...)...)
		if err != nil {
			return nil, err
		}
		if err := collectOrphanMarkers(rows, marked); err != nil {
			return nil, err
		}
	}
	return marked, nil
}

// collectOrphanMarkers reads one `UPDATE ... RETURNING` result into marked, so the
// caller can keep the markers stamped by earlier batches.
func collectOrphanMarkers(rows *sql.Rows, marked map[string]time.Time) error {
	defer rows.Close() //nolint:errcheck // 読み取りのみ
	for rows.Next() {
		var assignmentID string
		var orphanedAt time.Time
		if err := rows.Scan(&assignmentID, &orphanedAt); err != nil {
			return err
		}
		// 同じ id が複数の分割に入っても、最初に返された時刻を保つ。
		if _, seen := marked[assignmentID]; !seen {
			marked[assignmentID] = orphanedAt
		}
	}
	return rows.Err()
}

// ClearOrphaned clears orphan markers for assignments present in the verified
// native scan. roleID keeps the update scoped to the role being reconciled.
//
// **mark と同じ個数で刻む。** 解除は `MarkOrphaned` が押した印を消すだけなので
// 分割しても結果は同じで、文の長さを role の行数から切り離せる。
//
// **q で受ける。** 印を押し直す `MarkOrphaned` と、同じ transaction でしか意味を
// 成さない (`ApplyOrphanMarkers` を参照)。
func (s *store) ClearOrphaned(ctx context.Context, q queryer, roleID string, assignmentIDs []string) error {
	for start := 0; start < len(assignmentIDs); start += orphanMarkerBatch {
		end := start + orphanMarkerBatch
		if end > len(assignmentIDs) {
			end = len(assignmentIDs)
		}
		if _, err := q.ExecContext(ctx, `
			UPDATE role_level_experience
			SET orphaned_at = NULL
			WHERE role_id = $1 AND orphaned_at IS NOT NULL
			  AND assignment_id IN (`+placeholdersFrom(2, end-start)+`)
		`, append([]any{roleID}, toAny(assignmentIDs[start:end])...)...); err != nil {
			return err
		}
	}
	return nil
}

// ApplyOrphanMarkers writes one role's orphan marks and clears as a single unit.
//
// **1 role の印は全部か無しで決める。** mark と clear を別々の transaction で書くと、
// 3 つ目の分割で失敗したときに「前の分割の印だけ残った」が次の周まで居る。
// `orphaned_at` は保持期間の起点なので、partial な印は「保持期間を経えた」と
// 読まれ、prune が監査に残っていない XP を消す。**片側だけが commit されないよう、
// rollback で partial な印を残さないことが本メソッドの要件。**
//
// **両方空なら transaction を開かない。** XP 行の無い role で「何もしない commit」を
// 残さない (prune と同じ方針)。
func (s *store) ApplyOrphanMarkers(ctx context.Context, roleID string, orphanIDs, liveIDs []string) (map[string]time.Time, error) {
	if len(orphanIDs) == 0 && len(liveIDs) == 0 {
		return map[string]time.Time{}, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	marked, err := s.applyOrphanMarkers(ctx, tx, roleID, orphanIDs, liveIDs)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	return marked, nil
}

// applyOrphanMarkers is ApplyOrphanMarkers' statement sequence on any queryer, so
// the caller decides where the transaction starts.
//
// **どちらの文で失敗したかが wrapped error に残る。** `ApplyOrphanMarkers` を外から
// 見たときは 1 個の storage 失敗だが、ログに残すのは「押したのか外したのか」までで
// 後から判定できる形にする。
func (s *store) applyOrphanMarkers(ctx context.Context, q queryer, roleID string,
	orphanIDs, liveIDs []string,
) (map[string]time.Time, error) {
	marked, err := s.MarkOrphaned(ctx, q, roleID, orphanIDs)
	if err != nil {
		return nil, fmt.Errorf("rolelevel: %s の orphan の記録に失敗しました: %w", roleID, err)
	}
	if err := s.ClearOrphaned(ctx, q, roleID, liveIDs); err != nil {
		return nil, fmt.Errorf("rolelevel: %s の live assignment の orphan 解除に失敗しました: %w", roleID, err)
	}
	return marked, nil
}

// InsertAudit appends one audit row. q may be a transaction.
func (s *store) InsertAudit(ctx context.Context, q queryer, entry auditEntry) error {
	before, err := json.Marshal(entry.Before)
	if err != nil {
		return fmt.Errorf("rolelevel: 監査の before を JSON 化できません: %w", err)
	}
	after, err := json.Marshal(entry.After)
	if err != nil {
		return fmt.Errorf("rolelevel: 監査の after を JSON 化できません: %w", err)
	}
	_, err = q.ExecContext(ctx, `
		INSERT INTO role_level_audit
			(actor_id, operation, role_id, user_id, assignment_id, before_state, after_state, note)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, entry.ActorID, entry.Operation, nullableString(entry.RoleID), nullableString(entry.UserID),
		nullableString(entry.AssignmentID), nullableJSON(entry.Before, before), nullableJSON(entry.After, after), nullableString(entry.Note))
	return err
}

func nullableJSON(value map[string]any, payload []byte) any {
	if value == nil {
		return nil
	}
	return payload
}

// RecentAudit returns the newest audit rows for a role and/or user.
//
// roleID と userID がともに非空なら AND 条件、片方だけ非空ならその一方だけで
// 絞り込む。両方空の unscoped query は全履歴を返さずエラーにする。
func (s *store) RecentAudit(ctx context.Context, roleID, userID string, limit int) ([]auditEntry, error) {
	if roleID == "" && userID == "" {
		return nil, fmt.Errorf("rolelevel: audit query must be scoped by roleID or userID")
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, actor_id, operation, role_id, user_id, assignment_id, before_state, after_state, note, created_at
		FROM role_level_audit
		WHERE ($1 = '' OR role_id = $1) AND ($2 = '' OR user_id = $2)
		ORDER BY created_at DESC, id DESC
		LIMIT $3
	`, roleID, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // 読み取りのみ
	out := []auditEntry{}
	for rows.Next() {
		var (
			entry              auditEntry
			role, user, assign *string
			before, after      []byte
			note               sql.NullString
			createdAt          time.Time
		)
		if err := rows.Scan(&entry.ID, &entry.ActorID, &entry.Operation, &role, &user, &assign,
			&before, &after, &note, &createdAt); err != nil {
			return nil, err
		}
		entry.CreatedAt = createdAt
		entry.RoleID, entry.UserID, entry.AssignmentID = deref(role), deref(user), deref(assign)
		if note.Valid {
			entry.Note = note.String
		}
		if len(before) > 0 {
			if err := json.Unmarshal(before, &entry.Before); err != nil {
				return nil, fmt.Errorf("rolelevel: audit operation %q の before_state を読み込めません: %w", entry.Operation, err)
			}
		}
		if len(after) > 0 {
			if err := json.Unmarshal(after, &entry.After); err != nil {
				return nil, fmt.Errorf("rolelevel: audit operation %q の after_state を読み込めません: %w", entry.Operation, err)
			}
		}
		out = append(out, entry)
	}
	return out, rows.Err()
}

// InsertOperation records a new XP operation.
//
// created is false when the key already exists, which is the idempotent path: the
// caller resumes the stored operation instead of starting a second one.
func (s *store) InsertOperation(ctx context.Context, op operation) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO role_level_operation
			(idempotency_key, actor_id, user_id, role_id, mode, operand, status, assignment_created)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (idempotency_key) DO NOTHING
	`, op.IdempotencyKey, op.ActorID, op.UserID, op.RoleID, op.Mode, op.Operand, op.Status, op.AssignmentCreated)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// LoadOperation returns one operation by idempotency key. A NULL assignment_id
// is represented by the empty AssignmentID string in the in-memory contract.
func (s *store) LoadOperation(ctx context.Context, key string) (operation, bool, error) {
	return s.loadOperation(ctx, s.db, key)
}

// loadOperation is LoadOperation on any queryer, so a caller that already owns the
// advisory-lock connection can re-read the row on that same connection.
//
// **pool に出さないため queryer を受ける。** `role_level_operation` の reread が
// `*sql.DB` 経由だと、lock 用の専用接続が pool を使い切ったとき (同時実行数が
// MaxOpenConns に達したとき) に「返す接続を待つ → 返す接続は全部 lock 中」で
// 永久に止まる。SQL は 1 箇所に書いて、read 先を呼び出し側が選ぶ。
func (s *store) loadOperation(ctx context.Context, q queryer, key string) (operation, bool, error) {
	op, err := scanOperation(q.QueryRowContext(ctx, `
		SELECT idempotency_key, actor_id, user_id, role_id, mode, operand, desired_exp,
		       assignment_id, assignment_created, status, last_error, created_at, updated_at
		FROM role_level_operation WHERE idempotency_key = $1
	`, key))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return operation{}, false, nil
		}
		return operation{}, false, err
	}
	return op, true, nil
}

// LoadOperationForUpdate serializes the final XP mutation for one idempotency key.
// The lock is held until tx commits or rolls back.
func (s *store) LoadOperationForUpdate(ctx context.Context, tx *sql.Tx, key string) (operation, bool, error) {
	op, err := scanOperation(tx.QueryRowContext(ctx, `
		SELECT idempotency_key, actor_id, user_id, role_id, mode, operand, desired_exp,
		       assignment_id, assignment_created, status, last_error, created_at, updated_at
		FROM role_level_operation WHERE idempotency_key = $1 FOR UPDATE
	`, key))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return operation{}, false, nil
		}
		return operation{}, false, err
	}
	return op, true, nil
}

// scanOperation keeps nullable database columns out of the public in-memory
// contract: NULL assignment_id becomes "", while desired_exp remains *int64.
func scanOperation(sc rowScanner) (operation, error) {
	var (
		op           operation
		assignmentID sql.NullString
	)
	if err := sc.Scan(&op.IdempotencyKey, &op.ActorID, &op.UserID, &op.RoleID, &op.Mode,
		&op.Operand, &op.DesiredExp, &assignmentID, &op.AssignmentCreated, &op.Status, &op.LastError,
		&op.CreatedAt, &op.UpdatedAt); err != nil {
		return operation{}, err
	}
	if assignmentID.Valid {
		op.AssignmentID = assignmentID.String
	}
	return op, nil
}

// SetOperationStatus records progress. q may be a transaction, so the completion of
// an XP write can commit together with the XP write itself. Terminal rows are never
// moved backwards by a concurrent retry that observed an older state.
func (s *store) SetOperationStatus(ctx context.Context, q queryer, key, status, assignmentID string,
	desiredExp *int64, lastErr string,
) error {
	_, err := q.ExecContext(ctx, `
		UPDATE role_level_operation SET
			status        = $2,
			assignment_id = COALESCE(NULLIF($3, ''), assignment_id),
			desired_exp   = COALESCE($4, desired_exp),
			last_error    = $5,
			updated_at    = now()
		WHERE idempotency_key = $1 AND status NOT IN ('completed', 'failed')
	`, key, status, assignmentID, desiredExp, lastErr)
	return err
}

// MarkAssignmentCreated records the only point at which this operation is known
// to have created a native assignment. It is intentionally not inferred from an
// assignment id (an id can predate the operation).
func (s *store) MarkAssignmentCreated(ctx context.Context, q queryer, key string) error {
	_, err := q.ExecContext(ctx, `
		UPDATE role_level_operation
		SET assignment_created = TRUE, updated_at = now()
		WHERE idempotency_key = $1 AND status NOT IN ('completed', 'failed')
	`, key)
	return err
}

// ResumableOperations returns operations that stopped mid-flight.
//
// **completed と failed は入らない。** completed は目的の状態、failed は原因を追って
// 運営者が直すもので、どちらも自動再開すると operator の判断を飛ばす。
func (s *store) ResumableOperations(ctx context.Context, limit int) ([]operation, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT idempotency_key, actor_id, user_id, role_id, mode, operand, desired_exp,
		       assignment_id, assignment_created, status, last_error, created_at, updated_at
		FROM role_level_operation
		WHERE status IN ('pending', 'assigning', 'applying')
		ORDER BY updated_at ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // 読み取りのみ
	out := []operation{}
	for rows.Next() {
		op, err := scanOperation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	return out, rows.Err()
}

// ResumableOperationsForUser returns the oldest resumable operations of one user.
//
// **絞り込みは SQL 側で行う。** limit の後に Go で userId を絞ると、他 user の
// 未完了 operation が limit を先に使い切って、対象 user の operation が黙って
// 消える。表示する user の行が必ず limit 件返るようにする。
func (s *store) ResumableOperationsForUser(ctx context.Context, userID string, limit int) ([]operation, error) {
	if limit <= 0 || limit > 1000 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT idempotency_key, actor_id, user_id, role_id, mode, operand, desired_exp,
		       assignment_id, assignment_created, status, last_error, created_at, updated_at
		FROM role_level_operation
		WHERE status IN ('pending', 'assigning', 'applying') AND user_id = $1
		ORDER BY updated_at ASC
		LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // 読み取りのみ
	out := []operation{}
	for rows.Next() {
		op, err := scanOperation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	return out, rows.Err()
}

// DeleteOrphanExperience removes XP rows whose native assignment is gone and which
// have been orphans since before.
//
// **保持期間の起点は orphaned_at で見る。** `updated_at` は「XP が最後に書かれた時刻」で、
// orphan になった時刻ではない。まだ orphan と判定していない行は、XP が古くても消しては
// いけない。消せるのは `MarkOrphaned` が orphaned_at を押した行だけ。
//
// **保持期間以内の行は消さない。** 監査のために残すのが目的で、native API の障害で
// 消えると operator の記録が失われる。
func (s *store) DeleteOrphanExperience(ctx context.Context, roleID string, orphanIDs []string, before time.Time) (int64, error) {
	removed, err := s.DeleteOrphanExperienceRemoved(ctx, s.db, roleID, orphanIDs, before)
	return int64(len(removed)), err
}

// DeleteOrphanExperienceRemoved is DeleteOrphanExperience on any queryer, returning
// the exact rows it removed instead of only a count.
//
// **消した行を返す。** `RowsAffected` の数だけでは「どの XP 行が消えたか」を答えられ
// ず、監査行の before に exact な内容を残せない。`RETURNING` で同じ文の中から読むので、
// 数と対象がずれる隙間が無く、呼び出し側が transaction の中で削除と監査を一緒に決められる。
func (s *store) DeleteOrphanExperienceRemoved(ctx context.Context, q queryer, roleID string,
	orphanIDs []string, before time.Time,
) ([]orphanDeletion, error) {
	if len(orphanIDs) == 0 {
		return nil, nil
	}
	// 100件ずつ刻む。bind parameter は PostgreSQL で65535個までだが、IN 節が
	// 長くなりすぎると plan が読みにくくなるだけなので。
	const batch = 100
	out := []orphanDeletion{}
	for start := 0; start < len(orphanIDs); start += batch {
		end := start + batch
		if end > len(orphanIDs) {
			end = len(orphanIDs)
		}
		rows, err := q.QueryContext(ctx, `
			DELETE FROM role_level_experience
			WHERE role_id = $1 AND orphaned_at IS NOT NULL AND orphaned_at < $2
			  AND assignment_id IN (`+placeholdersFrom(3, end-start)+`)
			RETURNING assignment_id, user_id, experience
		`, append([]any{roleID, before}, toAny(orphanIDs[start:end])...)...)
		if err != nil {
			return out, err
		}
		out, err = collectOrphanDeletions(rows, out)
		if err != nil {
			return out, err
		}
	}
	// 順序を固定する。監査行の JSON とログを安定させる。
	sort.Slice(out, func(i, j int) bool { return out[i].AssignmentID < out[j].AssignmentID })
	return out, nil
}

// collectOrphanDeletions reads one `DELETE ... RETURNING` result into out, so the
// caller can keep the rows of earlier batches.
func collectOrphanDeletions(rows *sql.Rows, out []orphanDeletion) ([]orphanDeletion, error) {
	defer rows.Close() //nolint:errcheck // 読み取りのみ
	for rows.Next() {
		var d orphanDeletion
		if err := rows.Scan(&d.AssignmentID, &d.UserID, &d.Experience); err != nil {
			return out, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DBNow returns the database's current transaction timestamp.
//
// **保持期間の境界は DB の時計で決める。** `time.Now()` は host の時計なので、host と
// PostgreSQL の時計がずれていると「保持期間内の行」を消したり、消えるべき行を残したり
// する。`orphaned_at` は DB の `now()` で書かれているので、同じnow() を基準に比べる
// ことが唯一素直な比較になる。
func (s *store) DBNow(ctx context.Context) (time.Time, error) {
	var now time.Time
	if err := s.db.QueryRowContext(ctx, `SELECT now()`).Scan(&now); err != nil {
		return time.Time{}, err
	}
	return now, nil
}

// placeholders builds "$1, $2, ..." for an IN clause. n must be positive.
func placeholders(n int) string {
	return placeholdersFrom(1, n)
}

// placeholdersFrom builds a PostgreSQL placeholder list beginning at start.
// DeleteOrphanExperience reserves $1 and $2 for roleID and before.
func placeholdersFrom(start, n int) string {
	var b strings.Builder
	for i := start; i < start+n; i++ {
		if i > start {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "$%d", i)
	}
	return b.String()
}

// toAny widens a string slice for variadic database/sql arguments.
func toAny(values []string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}

// nullableString maps "" to a SQL NULL so the audit columns stay NULL instead of
// holding an empty string that reads like a real id.
func nullableString(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// storageStatusError preserves coded domain conflicts while mapping raw storage failures to the stable storage code used by route clients.
func (s *service) storageStatusError(ctx context.Context, what string, err error) error {
	if err == nil {
		return nil
	}
	var ve *ValidationError
	if errors.As(err, &ve) {
		return statusError(err)
	}
	// Preserve coded/status errors such as revision conflicts; only raw storage
	// failures are translated to the stable storage code.
	if status, _ := plugin.ExtractStatusError(err); status != nil {
		return err
	}
	return s.storageError(ctx, what, err)
}
