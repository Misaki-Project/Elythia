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
	Name:       "role-level",
	Version:    "1.0.0",
	APIVersion: plugin.APIVersion,
	Migrations: migrations,
	Routes:     routes,
	Jobs:       jobs,
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
}

// config mirrors the `plugins.role-level` section of the instance config.
type config struct {
	// ActorID is the local administrator every native API call is made as.
	//
	// **AsSystem に相当するものは無い。** 管理操作は必ず誰かの権限で行われ、
	// モデレーションログにもこのIDで残る。空でも起動は止めない — read系の経路は
	// そのまま動き、native API を要する操作だけが ROLE_LEVEL_ACTOR_NOT_CONFIGURED を返す。
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
		// のは同梱pluginとしては大きすぎる。read系は動き、nativeを要する操作だけが
		// stable code を返す。
		ctx.Logger().Warn("actorId が未設定なので XP 変更と自動付与は使えません (level 設定の保存はできます)")
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

// native returns the role adapter bound to the configured actor.
func (s *service) native() (*nativeRole, error) {
	if s.cfg.ActorID == "" {
		return nil, codedErrorf(http.StatusForbidden, CodeActorNotConfigured,
			"actorId が未設定なのでこの操作はできません (.config の plugins.role-level.actorId を設定してください)")
	}
	return &nativeRole{caller: s.api.AsUser(s.cfg.ActorID), pages: s.cfg.AssignmentScanPages}, nil
}

// store is the plugin's own PostgreSQL access. Every method is scoped to the
// plugin schema; native ids are opaque text and there is no foreign key into
// mk-go's tables.
//
// **Task 1 では宣言だけ。** メソッドは Task 5 が同じ形のまま足す。
type store struct{ db *sql.DB }

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
// は常に routes/jobs より前。reader は1つ (routes / jobs) なので競合しない。
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

// routes registers the plugin's HTTP endpoints. The route table is added in
// Task 10; this task only proves the module starts, the schema is created and the
// configuration is validated.
func routes(pctx plugin.Context, router plugin.Router) error {
	_, err := newService(pctx)
	return err
}

// jobs registers the background work. The handlers are added in Task 11.
func jobs(pctx plugin.Context, j plugin.Jobs) error {
	_, err := newService(pctx)
	return err
}

// roleInfo is the slice of admin/roles/show the plugin needs. The full Role shape
// is deliberately not decoded: upstream adds fields over time and binding them all
// would make the plugin's fate depend on that.
type roleInfo struct {
	ID                        string `json:"id"`
	Target                    string `json:"target"`
	IsAdministrator           bool   `json:"isAdministrator"`
	CanEditMembersByModerator bool   `json:"canEditMembersByModerator"`
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
