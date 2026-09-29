// Package rolelevel gives mk-go's own manual roles a level, an experience curve
// and per-level policies without touching mk-go's core schema.
//
// # mk-go の schema に何も足さない
//
// level設定・curve・policy range・XP・監査はすべてこのプラグインの schema
// (`plugin_role_level`) にある。mk-go 側に column も enum も足さない。だから TS へ
// 戻しても消えるのは level 機能だけで、native role と assignment は残る。
//
// # native id は opaque として扱う
//
// `role_id` / `assignment_id` / `user_id` は text で保存し、foreign key は張らない。
// 張ると core schema をプラグインの寿命に紐付けにするうえ、「orphan を監査のために
// 残す」運用と両立しない。
//
// # 置換が要る理由
//
// 権限の最終形は EffectivePolicyContribution.ReplaceRoleID で置換する。追加だけで
// 済むなら追加するが、boolean の OR と数値の max では「緩い policy を level で上書き」
// できないので置換経路が要る。
package rolelevel

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/shiroha-a/mk/plugin"
)

// Plugin is the entry point the build-time generator references.
//
// **既定で有効。** `mk-plugin.yml` に `disabled: true` を書かないのは Misaki の
// bundled image で level 機能がそのまま入るのが目的だから。無効化は runtime の
// `plugins.role-level.enabled: false` で行う (再ビルド不要)。
var Plugin = plugin.Definition{
	Name:              "role-level",
	Version:           "1.0.0",
	APIVersion:        plugin.APIVersion,
	Migrations:        migrations,
	EffectivePolicies: effectivePolicies,
	Routes:            routes,
	Jobs:              jobs,
}

// migrations は Definition で宣言する。
//
// **DROP は書かない。** plugin を無効化・削除しても schema を自動消さない (消した行は
// 復元できないので、一時的に外しただけの運用でデータが飛ぶ方が損害が大きいという
// host 全体の判断に合わせる)。不要なら運営者が手で DROP する。
var migrations = []plugin.Migration{
	{Version: 1, SQL: `
		CREATE TABLE role_level_config (
			role_id          text PRIMARY KEY,
			base_level       bigint NOT NULL,
			experience_curve jsonb NOT NULL,
			policy_ranges    jsonb NOT NULL,
			revision         bigint NOT NULL,
			created_at       timestamptz NOT NULL DEFAULT now(),
			updated_at       timestamptz NOT NULL DEFAULT now(),
			updated_by       text NOT NULL
		);

		-- XP は assignment_id に紐づけ、user_id + role_id ではない。unassign 後に同じ
		-- role を再 assign しても古い XP が復活しないように。
		CREATE TABLE role_level_experience (
			assignment_id text PRIMARY KEY,
			role_id       text NOT NULL,
			user_id       text NOT NULL,
			-- **XP は JSON number / Go safe integer。** 0..9007199254740991 に収める。
			-- 上限は API 契約 (JavaScript の Number.MAX_SAFE_INTEGER) と一致させてあり、
			-- DB 側でも守るので、API まで出てから丸める形のミスが起きない。
			experience    bigint NOT NULL
			              CHECK (experience >= 0 AND experience <= 9007199254740991),
			created_at    timestamptz NOT NULL DEFAULT now(),
			updated_at    timestamptz NOT NULL DEFAULT now()
		);
		-- 「この role のこの user は今 XP を持つか」を引く index。orphan 判定と
		-- 管理画面の XP 順一覧がこれを使う。
		CREATE INDEX role_level_experience_role_user_idx ON role_level_experience (role_id, user_id);
		CREATE INDEX role_level_experience_user_idx ON role_level_experience (user_id);

		-- native assignment 作成と plugin の XP 保存の間で再開可能にするための手渡し。
		-- idempotency key が primary key なので、同じ鍵の再送は1回だけ通る。
		CREATE TABLE role_level_operation (
			idempotency_key text PRIMARY KEY,
			actor_id        text NOT NULL,
			user_id         text NOT NULL,
			role_id         text NOT NULL,
			mode            text NOT NULL,
			-- **operand は有限の小数を許す。** multiplier の raw factor (1.5 = ×1.5) を
			-- 保持する必要があるため bigint ではなく double precision。NaN と ±Infinity は
			-- 下の CHECK で落ちる (PostgreSQL では NaN との比較が常に false になるため、
			-- CHECK を通らない)。
			operand         double precision NOT NULL
			              CHECK (operand > -9007199254740991 AND operand < 9007199254740991),
			desired_exp     bigint
			              CHECK (desired_exp IS NULL OR
			                     (desired_exp >= 0 AND desired_exp <= 9007199254740991)),
			assignment_id   text,
			status          text NOT NULL,
			last_error      text NOT NULL DEFAULT '',
			created_at      timestamptz NOT NULL DEFAULT now(),
			updated_at      timestamptz NOT NULL DEFAULT now()
		);
		CREATE INDEX role_level_operation_status_idx ON role_level_operation (status, updated_at);

		CREATE TABLE role_level_audit (
			id            bigserial PRIMARY KEY,
			actor_id      text NOT NULL,
			operation     text NOT NULL,
			role_id       text,
			user_id       text,
			assignment_id text,
			before_state  jsonb,
			after_state   jsonb,
			note          text,
			created_at    timestamptz NOT NULL DEFAULT now()
		);
		CREATE INDEX role_level_audit_role_created_idx ON role_level_audit (role_id, created_at DESC);
		CREATE INDEX role_level_audit_user_created_idx ON role_level_audit (user_id, created_at DESC);
	`},
	{Version: 2, SQL: `
		ALTER TABLE role_level_experience ADD COLUMN orphaned_at timestamptz;
		CREATE INDEX role_level_experience_orphaned_idx ON role_level_experience (orphaned_at) WHERE orphaned_at IS NOT NULL;
		CREATE INDEX role_level_experience_role_keyset_idx ON role_level_experience (role_id, assignment_id);
	`},
	{Version: 3, SQL: `
		ALTER TABLE role_level_operation
			ADD COLUMN assignment_created boolean NOT NULL DEFAULT false;
		-- Rows written before this flag existed cannot safely be replayed: an
		-- assignment id proves existence, not who created it. Terminal rows are
		-- retained; active ambiguous rows are made explicitly non-resumable.
		UPDATE role_level_operation
		SET status = 'failed', last_error = 'assignment creation state unavailable', updated_at = now()
		WHERE status IN ('pending', 'assigning', 'applying');
	`},
	{Version: 4, SQL: `
		-- The original v1 bound excluded otherwise valid finite float64 values.
		-- Drop by its generated name so this also works when v1 was updated before
		-- being applied, then retain the constraint name with the finite bounds.
		ALTER TABLE role_level_operation
			DROP CONSTRAINT IF EXISTS role_level_operation_operand_check;
		ALTER TABLE role_level_operation
			ADD CONSTRAINT role_level_operation_operand_check CHECK (
				operand > '-Infinity'::double precision
				AND operand < 'Infinity'::double precision
			);
		CREATE INDEX IF NOT EXISTS role_level_experience_role_rank_idx
			ON role_level_experience (role_id, experience DESC, assignment_id);
	`},
	{Version: 5, SQL: `
		-- プロフィール上のlevel role表示は利用者ごとの設定。native role / assignment
		-- schemaへ列を足さず、plugin所有schema内だけで保持する。
		CREATE TABLE role_level_profile_visibility (
			role_id    text NOT NULL,
			user_id    text NOT NULL,
			hidden     boolean NOT NULL DEFAULT true,
			updated_at timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (role_id, user_id)
		);
		CREATE INDEX role_level_profile_visibility_user_idx
			ON role_level_profile_visibility (user_id);
	`},
}

// config mirrors the `plugins.role-level` section of the instance config.
type config struct {
	// ActorID is the configured actor used for native read/scan calls and for
	// operations whose persisted actor is not otherwise available.
	//
	// **AsSystem に相当するものは無い。** 管理操作は必ず誰かの権限で行われ、
	// モデレーションログにもこのIDで残る。空でも起動は止めない — native API を必要としない
	// 参照経路はそのまま動き、native API を必要とする操作は ROLE_LEVEL_ACTOR_NOT_CONFIGURED を返す。
	ActorID string `json:"actorId"`
	// AssignmentScanPages bounds how many pages of admin/roles/users the plugin
	// walks to resolve a (role, user) pair to a native assignment id.
	// admin/roles/users の limit 上限は 100 なので 1 page = 100 assignment。
	AssignmentScanPages int `json:"assignmentScanPages"`
	// OrphanRetentionDays is how long an XP row whose native assignment is gone is
	// kept before the prune job deletes it (監査のために残す期間)。
	OrphanRetentionDays int `json:"orphanRetentionDays"`
	// ReconcileCron resumes stopped XP operations (5-field, UTC).
	ReconcileCron string `json:"reconcileCron"`
	// OrphanCron recomputes which XP rows are orphans (5-field, UTC).
	OrphanCron string `json:"orphanCron"`
	// PruneCron deletes XP rows that have been orphans past the retention (5-field, UTC).
	PruneCron string `json:"pruneCron"`
}

func loadConfig(ctx plugin.Context) (config, error) {
	c := config{
		AssignmentScanPages: 50,
		OrphanRetentionDays: 30,
		ReconcileCron:       "*/10 * * * *",
		OrphanCron:          "17 3 * * *",
		PruneCron:           "43 4 * * *",
	}
	if err := ctx.Config().Unmarshal(&c); err != nil {
		return c, err
	}
	if c.AssignmentScanPages < 1 || c.AssignmentScanPages > 200 {
		return c, fmt.Errorf("assignmentScanPages は 1〜200 で指定してください (%d)", c.AssignmentScanPages)
	}
	if c.OrphanRetentionDays < 1 || c.OrphanRetentionDays > 3650 {
		return c, fmt.Errorf("orphanRetentionDays は 1〜3650 で指定してください (%d)", c.OrphanRetentionDays)
	}
	for _, f := range []struct{ name, expr string }{
		{"reconcileCron", c.ReconcileCron},
		{"orphanCron", c.OrphanCron},
		{"pruneCron", c.PruneCron},
	} {
		if err := validateCron(f.expr); err != nil {
			return c, fmt.Errorf("%s が不正です: %w", f.name, err)
		}
	}
	if c.ActorID != "" {
		if err := validateID("actorId", c.ActorID); err != nil {
			return c, err
		}
	}
	return c, nil
}

// cronFieldRe is a deliberately loose 5-field shape check.
//
// **構文を完全に検証する気はない。** 本当の検証は host 側の scheduler が行う
// (RegisterPluginJob が失敗すると Jobs の登録自体がエラーになる)。ここではよくある
// 書き間違い — 4フィールド、6フィールド、引用符 — を設定読み込みの時点で黙って
// 通り過ぎないためだけのもの。
var cronFieldRe = regexp.MustCompile(`^[0-9*/,\-?A-Za-z]+$`)

func validateCron(expr string) error {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return fmt.Errorf("5 フィールド (UTC, 5-field) を指定してください (渡されたのは %d フィールド: %q)",
			len(fields), expr)
	}
	for i, f := range fields {
		if !cronFieldRe.MatchString(f) {
			return fmt.Errorf("%d 番目のフィールド %q に使用できない文字があります", i+1, f)
		}
	}
	return nil
}

// service is the plugin's runtime. Routes / Jobs / the effective-policy resolver
// each build their own from the plugin.Context they are handed, so a test can
// point two services at two schemas.
type service struct {
	ctx   plugin.Context
	cfg   config
	db    *sql.DB
	log   *slog.Logger
	api   plugin.API
	store *store
}

func newService(ctx plugin.Context) (*service, error) {
	cfg, err := loadConfig(ctx)
	if err != nil {
		return nil, err
	}
	if cfg.ActorID == "" {
		// **起動は止めない。** 設定していない運営者のインスタンスが起動不能になる
		// のは同梱pluginとしては大きすぎる。native API を必要としない参照系の経路は
		// そのまま動き、XP 変更・自動付与・level 設定の保存だけが stable code を返す。
		ctx.Logger().Warn("actorId が未設定のため、XP 変更・自動付与・level 設定の保存は利用できません（native API を必要としない参照操作は利用できます）")
	}
	db := ctx.Storage().DB()
	return &service{
		ctx:   ctx,
		cfg:   cfg,
		db:    db,
		log:   ctx.Logger(),
		api:   ctx.API(),
		store: &store{db: db},
	}, nil
}

// native returns the role adapter bound to the configured svc.
func (s *service) native() (*nativeRole, error) {
	return s.nativeFor(s.cfg.ActorID)
}

// nativeFor returns the role adapter for one specific svc.
//
// **actor ごとに 1 枚作る。** Task 8 の assignment write では、永続化された
// operation.ActorID のユーザーとして nativeFor(operation.ActorID) を使って呼び出す。
// native() は read/scan と、永続化された actor を利用できない操作で設定済みの actor を使う。
func (s *service) nativeFor(actorID string) (*nativeRole, error) {
	if actorID == "" {
		return nil, codedErrorf(http.StatusForbidden, CodeActorNotConfigured,
			"actorId が未設定なのでこの操作はできません (.config の plugins.role-level.actorId を設定してください)")
	}
	return &nativeRole{caller: s.api.AsUser(actorID), pages: s.cfg.AssignmentScanPages}, nil
}

// nativeRole reads and writes mk-go's own role state through its REST API.
//
// **core table を直接読まない。** プラグインは自分の schema しか触れないので、role /
// assignment の正本は native API だけ。Task 1 では宣言だけ — メソッドは Task 6 が
// 同じ形のまま足す。
type nativeRole struct {
	caller plugin.Caller
	// pages bounds how many pages of admin/roles/users a scan walks.
	pages int
}

// invHolder carries the invalidator from EffectivePolicies to the routes and jobs,
// which are not handed one.
//
// host は EffectivePolicies を Routes / Jobs より前に呼ぶので、値が register されるの
// は常に routes/jobs より前。同じ process の routes と jobs の両方から読まれるため、
// test の差し替えも含めて mutex で同期する。
type invHolder struct {
	mu sync.Mutex
	v  plugin.EffectivePolicyInvalidator
}

var invalidatorHandle invHolder

func (h *invHolder) set(v plugin.EffectivePolicyInvalidator) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.v = v
}

// get returns the stored invalidator, or a no-op when EffectivePolicies has not run.
// **nil は返さない** — 呼び出し側の nil チェック漏れがそのまま panic になる。
func (h *invHolder) get() plugin.EffectivePolicyInvalidator {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.v == nil {
		return noopInvalidator{}
	}
	return h.v
}

// invalidator returns the host invalidator for this process.
func invalidator() plugin.EffectivePolicyInvalidator { return invalidatorHandle.get() }

type noopInvalidator struct{}

func (noopInvalidator) InvalidateUser(context.Context, string) error { return nil }
func (noopInvalidator) InvalidateRole(context.Context, string) error { return nil }

// orphanReporter is what the orphan route needs from the service.
//
// **Task 11 の handler だけ差し替えられる形にしている。** Task 10 では
// `service.OrphanReport` が 501 stub なので、route 自身の検証は差し替え先から受け取る
// fake で行う。
type orphanReporter interface {
	OrphanReport(context.Context) (map[string]any, error)
}

// orphanHandler serves POST /admin/orphans.
//
// **Task 11 の結果をそのまま返す。** 結果を捨て (`_, err := ...; return nil, err`)
// ると、実装が入っても `{"roles":{…}}` が返らない。
func orphanHandler(rep orphanReporter) plugin.Handler {
	return func(req plugin.Request) (any, error) {
		if err := requireModerator(req); err != nil {
			return nil, err
		}
		if err := bindJSON(req, &struct{}{}); err != nil {
			return nil, err
		}
		return rep.OrphanReport(req.Context())
	}
}

// routes registers the plugin's HTTP endpoints.
//
// **13 routes, all POST.** path parameter も query string も見ず、すべて body で受ける
// (Global Constraints「全部 POST で，全部 body で受ける」)。native の
// `admin/roles/assign` / `admin/roles/unassign` はここでは作らない。割り当ての操作は
// native API が行う (Task 6 / Task 8 が使う)。
//
// These endpoints are implemented here; they no longer return the Task 10 stub.
// 登録済みなので、frontend は 404 ではなく stable code 付きの「未実装」を受け取る。
func routes(pctx plugin.Context, router plugin.Router) error {
	svc, err := newService(pctx)
	if err != nil {
		return err
	}

	router.POST("/admin/roles/list", func(req plugin.Request) (any, error) {
		if err := requireModerator(req); err != nil {
			return nil, err
		}
		if err := bindJSON(req, &struct{}{}); err != nil {
			return nil, err
		}
		// native を要する集計は native の権限で行う。actorId が未設定なら実行者に
		// fallback し、それも無ければ 0 件 + 不明 を出す。
		configs, err := svc.store.ListConfigs(req.Context())
		if err != nil {
			return nil, svc.storageError(req.Context(), "level 設定の読み込み", err)
		}
		// **正確な member 数が要るのは 1 個の role のときだけ。** list では「全部
		// 走査できた role」だけ member 数を埋める。取れなかった role は 0 のまま
		// にして、正確な数は `/admin/roles/show` に集約する。
		counts := make(map[string]int, len(configs))
		truncatedCounts := make(map[string]bool, len(configs))
		for _, cfg := range configs {
			counts[cfg.RoleID] = 0
			truncatedCounts[cfg.RoleID] = true
		}
		for _, cfg := range configs {
			count, trunc, err := svc.memberCount(req.Context(), cfg.RoleID)
			if err != nil {
				continue
			}
			// A truncated native scan yields a lower bound; retain it and flag it.
			counts[cfg.RoleID] = count
			truncatedCounts[cfg.RoleID] = trunc
		}
		return map[string]any{
			"roles":                 configs,
			"memberCounts":          counts,
			"memberCountsTruncated": truncatedCounts,
		}, nil
	})

	router.POST("/admin/roles/show", func(req plugin.Request) (any, error) {
		if err := requireModerator(req); err != nil {
			return nil, err
		}
		var body struct {
			RoleID string `json:"roleId"`
		}
		if err := bindJSON(req, &body); err != nil {
			return nil, err
		}
		if err := validateID("roleId", body.RoleID); err != nil {
			return nil, err
		}
		cfg, found, err := svc.loadConfigOrStorageError(req.Context(), svc.store, body.RoleID)
		if err != nil {
			return nil, err
		}
		if !found {
			// **level 設定が無い role は 404。** 既定の level 設定を捏造して 200 で
			// 返すと、「設定した記憶が無いのに level がある」行列が返る。公開の
			// member 一覧 (`/roles/users`) も同じ状態で 404 (CodeConfigNotFound) を
			// 返すので、両 route の答えを揃える (routes.go)。新規作成は
			// `POST /admin/roles/update` の revision 0 がその役を持つ。
			return nil, codedErrorf(http.StatusNotFound, CodeConfigNotFound,
				"その role には level 設定がありません")
		}
		count, truncated, err := svc.memberCount(req.Context(), body.RoleID)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"role":             cfg,
			"memberCount":      count,
			"membersTruncated": truncated,
		}, nil
	})

	router.POST("/admin/roles/update", func(req plugin.Request) (any, error) {
		if err := requireAdmin(req); err != nil {
			return nil, err
		}
		var body struct {
			RoleID          string        `json:"roleId"`
			BaseLevel       *int64        `json:"baseLevel"`
			ExperienceCurve []Curve       `json:"experienceCurve"`
			PolicyRanges    []PolicyRange `json:"policyRanges"`
			Revision        *int64        `json:"revision"`
			Note            string        `json:"note"`
		}
		if err := bindJSON(req, &body); err != nil {
			return nil, err
		}
		if body.BaseLevel == nil || body.Revision == nil {
			return nil, codedErrorf(http.StatusBadRequest, CodeValidationFailed, "baseLevel と revision が必要です")
		}
		if err := validateID("roleId", body.RoleID); err != nil {
			return nil, err
		}
		// **manual role だけ許可する。** conditional には assignment が無いので、
		// XP を紐づけられる対象が存在せず、あとから policy 置換の先が壊れる。
		if err := svc.RequireConfigAdmin(req.Context(), body.RoleID); err != nil {
			return nil, err
		}
		cfg := Config{
			RoleID:          body.RoleID,
			BaseLevel:       *body.BaseLevel,
			ExperienceCurve: body.ExperienceCurve,
			PolicyRanges:    body.PolicyRanges,
			UpdatedBy:       req.UserID(),
		}
		// **末尾の未指定 stage は保存前に埋める。** 共有 validator は ranges が
		// [1, levelUps+2) をちょうど覆うことを要求するが、request は「指定したい
		// stage だけ」しか持ってこない (frontend の既定 range にも end が無い)。
		// 埋めずに検証すると、既定の 1 段を落としただけの保存が 400 になり、未知の
		// policy key の検証にも到達しない。**RequireConfigAdmin のあと・Validate の
		// まえ**に置くことで、conditional 弾きと curve / range の stable code の
		// 優先順位はそのまま保たれる。
		cfg.PolicyRanges = fillPolicyRangeTail(cfg.PolicyRanges, cfg.TotalLevelUps())
		if err := cfg.Validate(defaultCatalog); err != nil {
			return nil, statusError(err)
		}
		// Read and write the configuration and its audit row in one transaction.  The
		// read is deliberately inside the transaction as well: the audit must describe
		// the row that this revision-checked write actually replaced.
		tx, err := svc.db.BeginTx(req.Context(), nil)
		if err != nil {
			return nil, svc.storageStatusError(req.Context(), "level 設定の保存", err)
		}
		committed := false
		defer func() {
			if !committed {
				_ = tx.Rollback()
			}
		}()
		before, found, err := loadConfigRow(req.Context(), tx, body.RoleID)
		if err != nil {
			return nil, svc.storageStatusError(req.Context(), "level 設定の読み込み", err)
		}
		saved, err := svc.store.UpsertConfigTx(req.Context(), tx, cfg, *body.Revision)
		if err != nil {
			// **revision 衝突は storage error に潰さない。** UpsertConfig が 409 +
			// CodeConfigConflict を返しているのに 500 にすると、frontend が
			// 読み直して再保存するかどうかを決められない。
			return nil, svc.storageStatusError(req.Context(), "level 設定の保存", err)
		}
		var auditBefore map[string]any
		if found {
			auditBefore = configAuditState(before)
		}
		op := "config-create"
		if found {
			op = "config-update"
		}
		if err := svc.store.InsertAudit(req.Context(), tx, auditEntry{
			ActorID: req.UserID(), Operation: op, RoleID: body.RoleID, Note: truncate(body.Note, 500),
			Before: auditBefore, After: configAuditState(saved),
		}); err != nil {
			return nil, svc.storageStatusError(req.Context(), "監査の記録", err)
		}
		if err := tx.Commit(); err != nil {
			return nil, svc.storageStatusError(req.Context(), "level 設定の保存", err)
		}
		committed = true
		// **commit のあと cache を捨てる。** curve を変えると保存済みの level と
		// policy が変わる。dynamic policy は次の解決時に読み直される。
		svc.invalidateRoleConfig(req.Context(), body.RoleID)
		return map[string]any{"role": saved}, nil
	})

	router.POST("/admin/roles/delete", func(req plugin.Request) (any, error) {
		if err := requireAdmin(req); err != nil {
			return nil, err
		}
		var body struct {
			RoleID   string `json:"roleId"`
			Revision *int64 `json:"revision"`
		}
		if err := bindJSON(req, &body); err != nil {
			return nil, err
		}
		if body.Revision == nil {
			return nil, codedErrorf(http.StatusBadRequest, CodeValidationFailed, "revision が必要です")
		}
		if err := validateID("roleId", body.RoleID); err != nil {
			return nil, err
		}
		tx, err := svc.db.BeginTx(req.Context(), nil)
		if err != nil {
			return nil, svc.storageStatusError(req.Context(), "level 設定の保存", err)
		}
		committed := false
		defer func() {
			if !committed {
				_ = tx.Rollback()
			}
		}()
		before, found, err := loadConfigRow(req.Context(), tx, body.RoleID)
		if err != nil {
			return nil, svc.storageStatusError(req.Context(), "level 設定の読み込み", err)
		}
		deleted, err := svc.store.DeleteConfigTx(req.Context(), tx, body.RoleID, *body.Revision)
		if err != nil {
			return nil, svc.storageStatusError(req.Context(), "level 設定の保存", err)
		}
		if !deleted {
			return nil, codedErrorf(http.StatusConflict, CodeConfigConflict,
				"level 設定は他の操作で更新されています。読み直して削除してください")
		}
		if !found {
			return nil, codedErrorf(http.StatusConflict, CodeConfigConflict,
				"level 設定は他の操作で更新されています。読み直して削除してください")
		}
		if err := svc.store.InsertAudit(req.Context(), tx, auditEntry{
			ActorID: req.UserID(), Operation: "config-delete", RoleID: body.RoleID,
			Before: configAuditState(before), After: nil,
		}); err != nil {
			return nil, svc.storageStatusError(req.Context(), "監査の記録", err)
		}
		if err := tx.Commit(); err != nil {
			return nil, svc.storageStatusError(req.Context(), "level 設定の保存", err)
		}
		committed = true
		svc.invalidateRoleConfig(req.Context(), body.RoleID)
		// XP is retained as an orphan; Task 11 reconciliation prunes it after retention.
		return map[string]any{"roleId": body.RoleID, "deleted": true}, nil
	})

	router.POST("/admin/users/show", func(req plugin.Request) (any, error) {
		if err := requireModerator(req); err != nil {
			return nil, err
		}
		var body struct {
			UserID string `json:"userId"`
		}
		if err := bindJSON(req, &body); err != nil {
			return nil, err
		}
		if err := validateID("userId", body.UserID); err != nil {
			return nil, err
		}
		return svc.adminUser(req.Context(), body.UserID)
	})

	router.POST("/admin/change-exp", func(req plugin.Request) (any, error) {
		var body struct {
			IdempotencyKey string   `json:"idempotencyKey"`
			UserID         string   `json:"userId"`
			RoleID         string   `json:"roleId"`
			Mode           string   `json:"mode"`
			Operand        *float64 `json:"operand"`
			Note           string   `json:"note"`
		}
		if err := bindJSON(req, &body); err != nil {
			return nil, err
		}
		if body.Operand == nil {
			return nil, codedErrorf(http.StatusBadRequest, CodeValidationFailed, "operand が必要です")
		}
		// **入力検証を先に置く。** 不正なリクエストを認可のあとで弾くと、同じ
		// 400 が「権限がありません」に化ける。
		if err := validateIdempotencyKey(body.IdempotencyKey); err != nil {
			return nil, err
		}
		if err := validateID("userId", body.UserID); err != nil {
			return nil, err
		}
		if err := validateID("roleId", body.RoleID); err != nil {
			return nil, err
		}
		// 権限は XP を書く前に済ませる。**不正な操作は監査表に残さない。**
		if err := svc.AuthorizeXPChange(req, body.RoleID); err != nil {
			return nil, err
		}
		native, err := svc.native()
		if err != nil {
			return nil, err
		}
		if _, err := native.RequireManual(req.Context(), body.RoleID); err != nil {
			return nil, err
		}
		res, err := svc.ChangeExp(req.Context(), ChangeExpRequest{
			IdempotencyKey: body.IdempotencyKey,
			ActorID:        req.UserID(),
			UserID:         body.UserID,
			RoleID:         body.RoleID,
			Mode:           Mode(body.Mode),
			Operand:        *body.Operand,
			Note:           body.Note,
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"assignmentId": res.AssignmentID,
			"experience":   res.Experience,
			"status":       string(res.Status),
			"resumed":      res.Resumed,
		}, nil
	})

	router.POST("/admin/audit", func(req plugin.Request) (any, error) {
		if err := requireModerator(req); err != nil {
			return nil, err
		}
		var body struct {
			RoleID string `json:"roleId"`
			UserID string `json:"userId"`
			Limit  int    `json:"limit"`
		}
		if err := bindJSON(req, &body); err != nil {
			return nil, err
		}
		if body.RoleID != "" {
			if err := validateID("roleId", body.RoleID); err != nil {
				return nil, err
			}
		}
		if body.UserID != "" {
			if err := validateID("userId", body.UserID); err != nil {
				return nil, err
			}
		}
		// **スコープは必須。** role も user も無いと、権限のない人が自分自身にも
		// ない全件の履歴が読める。
		if body.RoleID == "" && body.UserID == "" {
			return nil, codedErrorf(http.StatusBadRequest, CodeValidationFailed,
				"roleId か userId のどちらかを指定してください (監査履歴はスコープ付きでなければ読めません)")
		}
		if err := validatePageLimit("limit", body.Limit); err != nil {
			return nil, err
		}
		limit, _ := pageBounds(pageRequest{Limit: body.Limit})
		return svc.auditTrail(req.Context(), body.RoleID, body.UserID, limit)
	})

	router.POST("/admin/orphans", orphanHandler(svc))

	router.POST("/admin/reconcile", func(req plugin.Request) (any, error) {
		if err := requireAdmin(req); err != nil {
			return nil, err
		}
		var body struct {
			Mode string `json:"mode"`
		}
		if err := bindJSON(req, &body); err != nil {
			return nil, err
		}
		mode := body.Mode
		if mode == "" {
			// **mode 省略は all。** RunReconcile は mode を照合するので、
			// そのまま渡すと未知の mode として弾かれる。all に翻訳する。
			mode = reconcileAllMode
		}
		// **要求者を trigger として渡す。** 監査行の `actorId` に運営者の id が
		// 残らないと、手動の削除と cron の削除が区別できなくなる。
		result, err := svc.RunReconcile(req.Context(), mode, reconcileTrigger{
			Source:  reconcileSourceRoute,
			ActorID: req.UserID(),
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{"mode": mode, "result": result}, nil
	})

	router.POST("/roles/users", func(req plugin.Request) (any, error) {
		var body struct {
			RoleID string `json:"roleId"`
			Limit  int    `json:"limit"`
			Offset int    `json:"offset"`
		}
		if err := bindJSON(req, &body); err != nil {
			return nil, err
		}
		if err := validateID("roleId", body.RoleID); err != nil {
			return nil, err
		}
		if err := validatePageLimit("limit", body.Limit); err != nil {
			return nil, err
		}
		if err := validatePageOffset("offset", body.Offset); err != nil {
			return nil, err
		}
		limit, offset := pageBounds(pageRequest{Limit: body.Limit, Offset: body.Offset})
		return svc.roleMembers(req.Context(), body.RoleID, limit, offset)
	})

	router.POST("/users/show", func(req plugin.Request) (any, error) {
		var body struct {
			UserID string `json:"userId"`
		}
		if err := bindJSON(req, &body); err != nil {
			return nil, err
		}
		if err := validateID("userId", body.UserID); err != nil {
			return nil, err
		}
		return svc.publicProfile(req.Context(), body.UserID)
	})

	router.POST("/users/profile-settings", func(req plugin.Request) (any, error) {
		if req.UserID() == "" {
			return nil, codedErrorf(http.StatusUnauthorized, CodeUnauthenticated, "ログインが必要です")
		}
		if err := bindJSON(req, &struct{}{}); err != nil {
			return nil, err
		}
		return svc.profileSettings(req.Context(), req.UserID())
	})

	router.POST("/users/profile-hide", func(req plugin.Request) (any, error) {
		if req.UserID() == "" {
			return nil, codedErrorf(http.StatusUnauthorized, CodeUnauthenticated, "ログインが必要です")
		}
		var body struct {
			RoleID string `json:"roleId"`
			Hidden bool   `json:"hidden"`
		}
		if err := bindJSON(req, &body); err != nil {
			return nil, err
		}
		if err := validateID("roleId", body.RoleID); err != nil {
			return nil, err
		}
		if _, found, err := svc.loadConfigOrStorageError(req.Context(), svc.store, body.RoleID); err != nil {
			return nil, err
		} else if !found {
			return nil, codedErrorf(http.StatusNotFound, CodeConfigNotFound, "その role には level 設定がありません")
		}
		native, err := svc.readNative()
		if err != nil {
			return nil, err
		}
		assigned, err := native.Assigned(req.Context(), body.RoleID, req.UserID())
		if err != nil {
			return nil, err
		}
		if !assigned {
			return nil, codedErrorf(http.StatusForbidden, CodeForbidden, "割り当てられていないroleは変更できません")
		}
		if err := svc.store.SetProfileRoleHidden(req.Context(), body.RoleID, req.UserID(), body.Hidden); err != nil {
			return nil, svc.storageError(req.Context(), "プロフィール表示設定の保存", err)
		}
		return map[string]any{"roleId": body.RoleID, "hidden": body.Hidden}, nil
	})

	return nil
}

func configAuditState(cfg Config) map[string]any {
	return map[string]any{
		"roleId": cfg.RoleID, "baseLevel": cfg.BaseLevel,
		"experienceCurve": cfg.ExperienceCurve, "policyRanges": cfg.PolicyRanges,
		"revision": cfg.Revision, "updatedBy": cfg.UpdatedBy,
	}
}

// roleInfo is the slice of admin/roles/show the plugin needs. The full Role shape
// is deliberately not decoded: upstream adds fields over time and binding them all
// would make the plugin's fate depend on that.
type roleInfo struct {
	ID                        string `json:"id"`
	Target                    string `json:"target"`
	IsAdministrator           bool   `json:"isAdministrator"`
	CanEditMembersByModerator bool   `json:"canEditMembersByModerator"`
	IsPublic                  bool   `json:"isPublic"`
	IsExplorable              bool   `json:"isExplorable"`
}

// assignment is one row of admin/roles/users. `id` is the native
// `role_assignment.id` — the key the plugin stores experience under — and
// `user.id` is the assignee.
//
// **レスポンスに role id は含まれない。** admin/roles/users は role ごとに取るので
// 呼び出し側が role を知っている。experience 側の行と突き合わせるときも plugin
// table の role_id ではなく、この endpoint から取った role を使う。
type assignment struct {
	ID   string `json:"id"`
	User struct {
		ID string `json:"id"`
	} `json:"user"`
}

// UserID returns the assignee id decoded from the packed user.
func (a assignment) UserID() string { return a.User.ID }
