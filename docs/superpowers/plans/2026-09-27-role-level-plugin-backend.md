# Misaki roleLevel Plugin (backend) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Misaki固有のroleLevel機能 (level設定 / XP curve / level別policy / assignment別XP / 自動assignment / 監査・reconciliation) を、mk-go core schemaを一切変えずに bundled plugin `plugins/rolelevel` として実装する。

**Architecture:** role本体・assignment・期限・moderator/administrator属性はmk-go標準のmanual roleをそのまま使う。プラグインはplugin-owned schema (config / experience / operation / audit の4 table) だけを持ち、純関数としてのlevelドメイン、native API経由のassignment解決、そして `EffectivePolicyContribution.ReplaceRoleID` によるpolicy置換を担当する。plugin無効時もnative roleとassignmentは有効なまま残り、level別policyのcontributionだけが消えてnative policyへfallbackする。

**Tech Stack:** Go 1.27、`github.com/shiroha-a/mk/plugin` 公開API、`plugin/plugintest`、PostgreSQL 18 (pgx/v5)、標準 `testing` のみ (既存同梱プラグインと同じくtestify非使用)、`tools/pluginbuild`、`make plugin-test`

---

## Global Constraints

- **依存する汎用host interface (別issue/PRで先にlanded済である必要あり)。** 全て `plugin/` への非破壊的な追加で、本planでは**実装しない**:
  - `plugin.ActiveRoleAssignment` — `struct { RoleID string; AssignmentID string }`
  - `plugin.EffectivePolicyRequest.ActiveAssignments []plugin.ActiveRoleAssignment` — activeな**manual** assignmentのみ。expired/削除済みを含めない。conditional roleはassignmentを持たないので含めない
  - `plugin.EffectivePolicyContribution.ReplaceRoleID string` — 空文字なら追加 (既存挙動)、非空なら `ReplaceRoleID`+`Key` のnative contributionを1対1で置換し、**そのnative contributionのpriorityが使われる** (`Priority`フィールドは無視される)
  - `plugin.APIVersion` は `1` のまま。`apiVersion` を上げる必要はない
- **Task 1 の Step 0 が RED になったら停止する。** 汎用host interface PR が未mergeならrolelevel pluginは**コンパイルできない**。先にhost側をlandする (spec「Delivery Boundaries」1〜4)。
- **mk-go core schemaを一切変更しない。** `migration/` にファイルを追加しない。`role` / `role_assignment` にMisaki固有のcolumn・table・enumを追加しない。core role target enumに `manualLevel` を追加しない。
- **rolelevel pluginは既定で有効** (`mk-plugin.yml` に `disabled: true` を**書かない**)。runtimeの `enabled` 既定がtrueなので、ビルドに含めた時点で有効になる。
- **`plugins/rolelevel` は `internal/` をimportしない。** importしてよいのは `github.com/shiroha-a/mk/plugin` だけ。core側のrole / assignment / 経験値には**native API (`plugin.API`) 経由**でしか触らない。
- **XPは `user_id + role_id` ではなく `assignment_id` に紐づける。** unassign後に同じroleを再assignしても古いXPが復活してはいけない。
- **level model。** `minimumLevel = baseLevel`、`maximumLevel = baseLevel + sum(level-up counts)`、`effectiveLevel = baseLevel + completed level-ups`、`progressionStage = effectiveLevel - baseLevel + 1`。`progressionStage` は常に1始まりで、level別policy rangeの判定に使う。
- **既定値は全作成経路で統一する。** `baseLevel = 1` / `experience curve = const 100 XP × 99 level-ups` / `effective level = 1..100` / `policy range = 全範囲を覆う1個の base range`。curveを明示的に空にした場合は `baseLevel` 固定。
- **XP curve。** 型は `const | linear | exponential` のみ。level-up コストは `base` / `base + additional*n` / `base + additional*exponential**n` (n は rule 内 0 始まり)。**コストは実数のまま保持し、1 level ごとに切り上げない。** rule が変わると n は 0 に戻る。
- **XP は JSON number / Go safe integer。** 0..9007199254740991。string にも full uint64 にもしない。DB の `experience` は `bigint` で `CHECK (experience >= 0 AND experience <= 9007199254740991)` を付ける。
- **rule をまたぐ offset は float64 で持ち越す。** 前の rule の累積が 2.5 なら、次の rule は 2.5 から積算する (2.5 + 3.25 = 5.75)。rule ごとに 0 へ戻さない。
- **評価は「rule を順に走査し、各 rule の中で binary search」。** rule 数 S、1 rule の最大 level 数 L として O(S log L) 時間・O(1) メモリ。累積和は閉形式で求める:

  | type | k level ぶんの累積 |
  |---|---|
  | `const` | `k*base` |
  | `linear` | `k*base + (k*(k-1)/2)*additional` |
  | `exponential` (`e != 1`) | `k*base + additional * (e**k - 1)/(e - 1)` |
  | `exponential` (`e == 1`) | `k*(base + additional)` |

  巨大な level-up 数を逐次 loop しない。
- **指数関数は 1 に近い値で安定させる。** `e == 1` は専用処理し、それ以外は `math.Pow(e, k)` ではなく `math.Expm1(float64(k) * math.Log1p(e - 1)) / (e - 1)` を使う。`Pow` は 1 付近で桁が落ちて-accumulativeが狂う。
- **1 level あたりのコストは有限かつ 0 より大きいこと、累積和は有限かつ 9007199254740991 以下であることを保存時に検証する。** コストは n の1次式か単調な指数関数なので、最小と最大は両端 (n = 0 と n = LevelUps-1) にある。両端だけを見れば全 level が収まることを示せる。
- **整数 XP は小数しきい値に `ceil` で到達する。** しきい値が 2.5 なら整数XPが 3 のときに到達。`currentLevelExp` は `floor(整数XP - 小数しきい値)`、次の level までの表示値は `ceil(次のしきい値) - 整数XP`。
- **最大level到達後の余剰XPは `currentLevelExp` に保持する。** 最大level または curve なしの場合 `nextLevelExp` は `null`。JSONで表現できないNaNはAPIへ返さない。
- **policy rangeは実効level値ではなく `progressionStage` に対して設定する。** 半開区間 `[start, end)` で評価し、重複も欠落も許さない。range長の合計は `level-up count合計 + 1` と一致させる。range typeは `base | const | multiplier` のみ。
- **policy rule。** `base` = instance/native defaultを使う。**この場合も置換 contribution を出す** (`UseDefault: true` を立てて `ReplaceRoleID` を付ける) — 出さないと native の静的 role contribution がそのまま生き残り、level 設定が効かない。`const` = 指定値。`multiplier` は **numeric な native policy だけ**に許され、値は `base + additional * range内0始まりoffset`。**multiplier offsetは `baseLevel` の値に影響されない。** boolean と string/enum policy には native 型と一致する `const` だけ許可。未知の native policy key は拒否。計算結果が native policy 型・許容値を満たさない設定は保存時に拒否する。
- **旧実装のinclusive boundaryによるrange重複と、`effectiveLevel - startLevel` によるmultiplier offsetずれは再現しない。**
- **XP mutation modeは `set | add | multiplier`。** 計算は float64 で行い、結果を `floor` してから 0..9007199254740991 に収める。mode 別の初期XP (新しい行の値) は `set: operand` / `add: operand` / `multiplier: 0`。
- **`multiplier` の operand は raw factor。** `1.5` は ×1.5 であって百分率ではない。整数に限らない。`role_level_operation.operand` は有限の小数を保持する必要があるため `double precision` を使う。
- **既存assignmentへのXP変更は、plugin transaction内で XP・audit・operation status をまとめて更新する。** commit成功**後**にuserとroleのeffective-policy cacheをinvalidateする。commit前には更新しない。
- **未assignment userへのXP操作の順序は固定する。** ① idempotency key付きpending operationをplugin DBへ保存 → ② native `admin/roles/assign` → ③ native APIから新assignment IDを再取得 → ④ assignment IDにXPを保存 → ⑤ auditを記録しoperationをcompletedに → ⑥ commit後にcacheをinvalidate。
- **途中で停止したpending operationはreconciliation jobが同じidempotency keyで再開する。** XP writeとoperation completionは**1つのtransaction**なので、`add` を二重適用する窓が存在しない。
- **authorization。** level configの作成・更新・削除はadministratorだけ。XP変更対象roleのnative `canEditMembersByModerator` がtrueならmoderatorも可 (ただし `isAdministrator` なroleはadministrator限定 — nativeのassign/unassignと同じ方針)。それ以外のXP変更はadministratorだけ。frontendの表示可否をauthorization boundaryにしない。
- **public user responseはnative role visibilityを超える情報を返さない。** 未付与のroleは返さない。role ID・user ID・assignment IDの対応は毎回検証し、plugin storageの値だけを信用しない。
- **API errorにはstable codeを付ける。** validation / authorization / native API failure / storage failure / conflictを区別する。
- **plugin migrationはversionedかつtransactional。** plugin無効化・削除時にschemaを自動DROPしない (orphan rowは監査のために保持期間後まで残す)。
- **policy resolution pathではnetwork callを行わず、indexed plugin DB queryだけを使う。** host側のprovider timeoutは1秒で、解決頻度は高いため。
- **config/XP更新後のcross-worker invalidationを必須とする** (`plugin.EffectivePolicyInvalidator.InvalidateUser` / `InvalidateRole`)。
- **reconciliation jobはidempotentにする。** native API failure時にデータを削除しない。
- **本planにfrontendコードを含めない。** `plugins/rolelevel/frontend/` は作らない。`admin:role-editor` / `admin:user` / `profile:info` slotとfrontend pluginは `Misaki-Project/misskey-ts` 側で別に実装する。spec「Frontend」節は本planの対象外。
- **本planに旧CherryPickからのデータ移行SQLを含めない。** spec「Data Migration Boundary」の成果物は全機能実装後にローカルSQL scriptだけで作る (GitHubへpushしない)。
- **`internal/` の新規packageを作らない。** 追加するhost側は `internal/entitycompat/` のgate test 1ファイルだけ。
- **既存bundled plugin (`status` / `trustlevel`) と他のplan fileを変更しない。**
- **既存 `go test ./...` にはPostgreSQL未構成、plugin surface golden drift、Windows固有testの既存failureがある。** 対象packageとrepository gateで新規regressionを判定する。

### 設計上の判断 (spec の逸脱ではなく、実装の選択)

- **route は全部 POST で11本。** `plugin.Router` は `GET` / `POST` を提供し、Misskey 本体の API も POST が基本なので、spec の route 一覧をそのまま POST で実装する。verb に `PUT` / `DELETE` は無いので path parameter ではなく **body** で受ける。query string は使わない。
- **native `admin/roles/assign` と `admin/roles/unassign` は plugin route ではない。** plugin 内部から呼ぶ native API call として扱う (XP の自動付与で使うのは assign)。
- **XP 0 は「行が無い」状態として表現する。** 行は XP write のときだけ作る。XP 0 は `Experience(0)` から計算できるので、plugin table に空の行を書かなくてよい。public route が assignment ID を解決するたびに native paging が必要になるのを避ける。
- **host 側の drift gate 用のファイルを JSON にする。** plugin は `internal/` を import できないので native policy schema を自前のファイルとして持つ。`/plugins/` を直接読めない root module 側からも同じものを同じ方法で読めるようにするため。

---

## 依存関係マップ

```
Task 1 (scaffold / migrations / config / errors)
  ├─ Task 2 (native policy catalog + host drift gate)
  │    ├─ Task 3 (level domain + curve)
  │    └─ Task 4 (policy ranges)             ← 3 と並列可
  ├─ Task 5 (plugin storage)
  ├─ Task 6 (native role adapter)            ← 1 に依存
  │    └─ Task 7 (authorization)            ← 6 に依存
  │         └─ Task 8 (XP operation state machine) ← 5,6,7 に依存
  ├─ Task 9 (effective-policy resolver)      ← 2,3,4,5 に依存
  ├─ Task 10 (routes)                        ← 2〜8,11 に依存
  ├─ Task 11 (reconciliation jobs + audit)   ← 5,6,8 に依存
  ├─ Task 12 (docs / config / build-CI registration)
  └─ Task 13 (issue #12 + full verification)
```

---

### Task 1: plugin module・manifest・plugin-owned migration

**Files:**
- Create: `plugins/rolelevel/go.mod`
- Create: `plugins/rolelevel/go.sum`
- Create: `plugins/rolelevel/mk-plugin.yml`
- Create: `plugins/rolelevel/plugin.go`
- Create: `plugins/rolelevel/errors.go`
- Create: `plugins/rolelevel/main_test.go`
- Create: `plugins/rolelevel/plugin_test.go`
- Modify: `.gitignore:86-91`

**Interfaces:**
- Consumes: `plugin.Definition`, `plugin.Migration`, `plugin.Context`, `plugin.Router`, `plugin.Jobs`, `plugin.EffectivePolicyInvalidator`, `plugin.NewCodedStatusError`, `plugin/plugintest.Harness`
- Produces:
  - `var Plugin plugin.Definition` — `Name: "role-level"`, `Version: "1.0.0"`, `APIVersion: plugin.APIVersion`, `Migrations: migrations`, `Routes: routes`, `Jobs: jobs`。`EffectivePolicies` は Task 9 で追加
  - `var migrations []plugin.Migration` — version 1 に4つのtable
  - `type config struct { ActorID string; AssignmentScanPages int; OrphanRetentionDays int; ReconcileCron, OrphanCron, PruneCron string }`
  - `func loadConfig(ctx plugin.Context) (config, error)`
  - `type service struct { ctx plugin.Context; cfg config; db *sql.DB; log *slog.Logger; api plugin.API; store *store }`
  - `func newService(ctx plugin.Context) (*service, error)`
  - `func (s *service) native() (*nativeRole, error)`
  - `func invalidator() plugin.EffectivePolicyInvalidator`
  - `type ValidationError struct { Code, Field string; Err error }`, `func invalid(code, field, format string, args ...any) error`, `func statusError(err error) error`, `func codedErrorf(status int, code, format string, args ...any) error`, `func validateID(field, value string) error`
  - `const Code*` — 25個のstable code (Step 5 に全量)
  - test helper: `const testSchema`, `func testDB(t *testing.T) *sql.DB`, `func envOr`, `func dbUnavailable`, `func newHarness(t, db, api) *plugintest.Harness`, `func plugintestContext(t, cfg) plugin.Context`, `type stubAPI`, `type stubCaller`, `type roleInfo`, `type assignment`

- [ ] **Step 0: 汎用host interfaceの3点が既にあるか確認する (RED = 未land)**

Run:

```powershell
Select-String -Path "plugin/policy.go" -Pattern "ActiveRoleAssignment|ActiveAssignments|ReplaceRoleID"
```

Expected: 3件ヒットする。1件でも無いなら**ここで停止**し、汎用のhost interface PRを先にlandする。

- [ ] **Step 1: moduleとmanifestを作る**

`plugins/rolelevel/go.mod` (依存集合は `plugins/trustlevel/go.mod` と同一):

```
module github.com/shiroha-a/mk-plugin-rolelevel

go 1.27.1

require (
	github.com/jackc/pgx/v5 v5.9.2
	github.com/shiroha-a/mk v0.0.0
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/text v0.41.0 // indirect
)

replace github.com/shiroha-a/mk => ../..
```

`plugins/rolelevel/go.sum` は依存集合が `plugins/trustlevel/go.sum` と同一なのでコピーでつくる:

```powershell
New-Item -ItemType Directory -Force -Path "plugins/rolelevel" | Out-Null
Copy-Item "plugins/trustlevel/go.sum" "plugins/rolelevel/go.sum"
```

`plugins/rolelevel/mk-plugin.yml`:

```yaml
name: role-level
apiVersion: 1
# **既定で有効。** Misaki の bundled image では設定なしで level 機能が入る。
#
# 同梱の status / trustlevel が `disabled: true` なのは「サンプル」だからで、
# こちらは実運用に必要な機能なので外す。判定を緩めるのではなく、build job の
# `Check bundled plugins are disabled by default` と `make plugin-vet` に
# **意図的に既定有効のプラグインの allowlist** を入れてある (#12)。
```

- [ ] **Step 2: gitignore に同梱の例外を追加する**

`plugins/*` はgitignore済みなので、例外を1行足さないと**trackされない** (= CIの `git ls-files` 列挙に現れず、vetもtestもされない)。`.gitignore` の `!plugins/trustlevel/` の直後に追加:

```
# roleLevel も同梱する (#12)。他の2つと異なり、**既定で有効** — Misaki の
# bundled image で level 機能が設定なしで入るのはこれが意図だから。
!plugins/rolelevel/
```

- [ ] **Step 3: RED — moduleがtestを実行できること、manifestが登録できること、configの契約を固定する**

`plugins/rolelevel/main_test.go` (以降全taskが使うtest基盤):

```go
package rolelevel

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/shiroha-a/mk/plugin"
	"github.com/shiroha-a/mk/plugin/plugintest"
)

// testSchema is the plugin-owned schema the tests exercise.
const testSchema = "plugin_role_level_test"

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// testDB opens a real PostgreSQL connection scoped to testSchema.
//
// **fake SQL は使わない。** 模した挙動は本物とずれ、通ったのに本番で落ちる
// 形のテストになる (plugins/status と同じ方針)。
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	base := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		envOr("TEST_DB_HOST", "localhost"), envOr("TEST_DB_PORT", "5432"),
		envOr("TEST_DB_USER", "mk"), envOr("TEST_DB_PASS", "mk"),
		envOr("TEST_DB_NAME", "misskey_test"))

	admin, err := sql.Open("pgx", base)
	if err != nil {
		dbUnavailable(t, err)
	}
	defer admin.Close() //nolint:errcheck // 使い捨て
	if err := admin.Ping(); err != nil {
		dbUnavailable(t, err)
	}
	for _, q := range []string{
		`DROP SCHEMA IF EXISTS ` + testSchema + ` CASCADE`,
		`CREATE SCHEMA ` + testSchema,
	} {
		if _, err := admin.Exec(q); err != nil {
			t.Fatal(err)
		}
	}

	db, err := sql.Open("pgx", base+" search_path="+testSchema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		if a, err := sql.Open("pgx", base); err == nil {
			_, _ = a.Exec(`DROP SCHEMA IF EXISTS ` + testSchema + ` CASCADE`)
			_ = a.Close()
		}
	})
	return db
}

// newHarness wires a harness with the plugin's real name, schema and API stub.
func newHarness(t *testing.T, db *sql.DB, api plugin.API) *plugintest.Harness {
	t.Helper()
	if api == nil {
		api = &stubAPI{}
	}
	return plugintest.New(t).WithName("role-level").WithDB(db).WithAPI(api)
}

// plugintestContext builds a plugin.Context carrying only configuration, for the
// tests that exercise loadConfig without touching the database.
func plugintestContext(t *testing.T, cfg map[string]any) plugin.Context {
	t.Helper()
	return plugintest.New(t).WithName("role-level").WithConfig(cfg).Context()
}

// dbUnavailable decides what to do when the test database cannot be reached.
//
// **CI では skip を許さない。** skip は成功として扱われるので、接続に失敗したことに
// 気づかないまま緑になる (#2588)。
func dbUnavailable(t *testing.T, err error) {
	t.Helper()
	if os.Getenv("MK_PLUGIN_TESTS_REQUIRE_DB") != "" {
		t.Fatalf("PostgreSQL に接続できません (MK_PLUGIN_TESTS_REQUIRE_DB が設定されているので skip しません): %v", err)
	}
	t.Skipf("PostgreSQL に接続できません: %v", err)
}

// stubAPI answers the native endpoints without a running mk-go.
//
// **production と同じ形を返す。** admin/roles/show は object、admin/roles/users は
// [{id, user:{id}}]、users/show に userIds を渡すと配列。形をずらすと「本番では通ら
// ない経路」を「通った」ことにしてしまう (plugins/status の stub と同じ理由)。
type stubAPI struct {
	mu sync.Mutex
	// roles is admin/roles/show by role id.
	roles map[string]roleInfo
	// assignments is admin/roles/users by role id, newest first.
	assignments map[string][]assignment
	// assignStatus, when non-zero, is the status admin/roles/assign fails with.
	assignStatus int
	assignCalls  int
	// showErr, when non-zero, is the status admin/roles/show fails with.
	showErr int
}

func (a *stubAPI) Anonymous() plugin.Caller    { return &stubCaller{api: a} }
func (a *stubAPI) AsUser(string) plugin.Caller { return &stubCaller{api: a} }

type stubCaller struct{ api *stubAPI }

func (c *stubCaller) Call(_ context.Context, endpoint string, params any) (json.RawMessage, error) {
	c.api.mu.Lock()
	defer c.api.mu.Unlock()
	m, _ := params.(map[string]any)
	switch endpoint {
	case "admin/roles/show":
		if c.api.showErr != 0 {
			return nil, apiError(endpoint, c.api.showErr, "SHOW_FAILED")
		}
		roleID, _ := m["roleId"].(string)
		info, ok := c.api.roles[roleID]
		if !ok {
			return nil, apiError(endpoint, http.StatusBadRequest, "NO_SUCH_ROLE")
		}
		return json.Marshal(info)
	case "admin/roles/assignment-show":
		roleID, _ := m["roleId"].(string)
		userID, _ := m["userId"].(string)
		assigned := false
		for _, a := range c.api.assignments[roleID] {
			if a.UserID() == userID {
				assigned = true
			}
		}
		return json.Marshal(map[string]any{
			"assigned": assigned, "expiresAt": nil,
			"role": map[string]any{"id": roleID, "target": "manual", "isPublic": true,
				"canEditMembersByModerator": c.api.roleInfoFor(roleID).CanEditMembersByModerator},
		})
	case "admin/roles/users":
		roleID, _ := m["roleId"].(string)
		if c.api.assignments[roleID] == nil {
			return json.Marshal([]assignment{})
		}
		return json.Marshal(c.api.assignments[roleID])
	case "admin/roles/assign":
		c.api.assignCalls++
		if c.api.assignStatus != 0 {
			return nil, apiError(endpoint, c.api.assignStatus, "STUB_ASSIGN_FAILED")
		}
		roleID, _ := m["roleId"].(string)
		userID, _ := m["userId"].(string)
		// production は 204 を返すだけで、assignment は同じ transaction でできて
		// いるので直後の admin/roles/users にはもう出ている。
		next := assignment{ID: fmt.Sprintf("asg-%s-%d", roleID, len(c.api.assignments[roleID])+1)}
		next.User.ID = userID
		c.api.assignments[roleID] = append([]assignment{next}, c.api.assignments[roleID]...)
		return nil, nil
	case "users/show":
		ids, _ := m["userIds"].([]string)
		out := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			out = append(out, map[string]any{"id": id, "username": "user-" + id})
		}
		return json.Marshal(out)
	}
	return nil, apiError(endpoint, http.StatusNotImplemented, "STUB_UNKNOWN_ENDPOINT")
}

func (a *stubAPI) roleInfoFor(roleID string) roleInfo {
	if info, ok := a.roles[roleID]; ok {
		return info
	}
	return roleInfo{ID: roleID, Target: "manual"}
}

func (a *stubAPI) assignCallCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.assignCalls
}

func apiError(endpoint string, status int, code string) error {
	return &plugin.APIError{Endpoint: endpoint, Status: status,
		Body: json.RawMessage(`{"error":{"code":"` + code + `"}}`)}
}
```

`plugins/rolelevel/plugin_test.go`:

```go
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
```

- [ ] **Step 4: RED を確認する**

```powershell
$env:GOWORK = "off"
go -C plugins/rolelevel test ./... -count=1
```

Expected: FAIL — `undefined: Plugin`, `undefined: testDB`, `undefined: loadConfig`, `undefined: roleInfo` など。`go.sum` / `replace` の不整合が出たら先にそちらを直す。

- [ ] **Step 5: `errors.go` を実装する**

`plugins/rolelevel/errors.go`:

```go
package rolelevel

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"

	"github.com/shiroha-a/mk/plugin"
)

// Stable API error codes.
//
// **frontend がここで分岐する。** code をリネームすると Misaki 側の frontend plugin が
// 黙って壊れるので公開契約として扱う。prefix 以外の変更は破壊的。
const (
	CodeUnauthenticated = "ROLE_LEVEL_UNAUTHENTICATED"
	CodeForbidden       = "ROLE_LEVEL_FORBIDDEN"
	CodeValidationFailed = "ROLE_LEVEL_VALIDATION_FAILED"

	CodeConfigNotFound       = "ROLE_LEVEL_CONFIG_NOT_FOUND"
	CodeConfigConflict       = "ROLE_LEVEL_CONFIG_CONFLICT"
	CodeInvalidBaseLevel     = "ROLE_LEVEL_INVALID_BASE_LEVEL"
	CodeInvalidCurve         = "ROLE_LEVEL_INVALID_CURVE"
	CodeInvalidRanges        = "ROLE_LEVEL_INVALID_RANGES"
	CodeUnknownPolicyKey     = "ROLE_LEVEL_UNKNOWN_POLICY_KEY"
	CodeMultiplierNotNumeric = "ROLE_LEVEL_MULTIPLIER_NOT_NUMERIC"
	CodeInvalidRangeValue    = "ROLE_LEVEL_INVALID_RANGE_VALUE"

	CodeRoleNotManual      = "ROLE_LEVEL_ROLE_NOT_MANUAL"
	CodeRoleNotAssignable  = "ROLE_LEVEL_ROLE_NOT_ASSIGNABLE"
	CodeActorNotConfigured = "ROLE_LEVEL_ACTOR_NOT_CONFIGURED"

	CodeUnknownMode            = "ROLE_LEVEL_UNKNOWN_MODE"
	CodeOperandOutOfRange      = "ROLE_LEVEL_OPERAND_OUT_OF_RANGE"
	CodeIdempotencyKeyRequired = "ROLE_LEVEL_IDEMPOTENCY_KEY_REQUIRED"
	CodeIdempotencyKeyInvalid  = "ROLE_LEVEL_IDEMPOTENCY_KEY_INVALID"

	CodeAssignmentUnresolved    = "ROLE_LEVEL_ASSIGNMENT_UNRESOLVED"
	CodeAssignmentScanExhausted = "ROLE_LEVEL_ASSIGNMENT_SCAN_EXHAUSTED"
	CodeNativeRoleNotFound      = "ROLE_LEVEL_NATIVE_ROLE_NOT_FOUND"
	CodeNativeAPIFailed         = "ROLE_LEVEL_NATIVE_API_FAILED"
	CodeStorageFailed           = "ROLE_LEVEL_STORAGE_FAILED"
)

// ValidationError is a configuration rejection that already knows which stable
// code it should surface as.
//
// **domain層はHTTPを知らない。** 同じ検査を route / reconciliation job / unit test から
// 呼ぶので、domainはcode付きの型を返し、routeだけがstatusを付ける。
type ValidationError struct {
	Code  string
	Field string
	Err   error
}

// Error implements error.
func (e *ValidationError) Error() string {
	if e.Field == "" {
		return e.Err.Error()
	}
	return e.Field + ": " + e.Err.Error()
}

// Unwrap implements the error chain contract.
func (e *ValidationError) Unwrap() error { return e.Err }

func invalid(code, field, format string, args ...any) error {
	return &ValidationError{Code: code, Field: field, Err: fmt.Errorf(format, args...)}
}

// codedErrorf builds a coded status error directly, for failures that are not
// configuration rejections (authorization, native API, storage).
//
// 素のerrorを返すとhostは 500 + "Internal error." に丸め、frontendはvalidation事由と
// 障害を区別できなくなる。
func codedErrorf(status int, code, format string, args ...any) error {
	return plugin.NewCodedStatusError(status, fmt.Sprintf(format, args...), code)
}

// statusError converts a domain validation failure into a coded status error and
// leaves every other error alone (so the host logs it and answers 500).
func statusError(err error) error {
	if err == nil {
		return nil
	}
	var ve *ValidationError
	if errors.As(err, &ve) {
		return plugin.NewCodedStatusError(http.StatusBadRequest, ve.Error(), ve.Code)
	}
	return err
}

// idRe bounds an opaque native id the plugin accepts. **mk-go の id 形式は解釈しない**
// (aidx / ulid / uuid のどれが来てもそのまま扱う)。禁じるのはログ行やエラーメッセージで
// 引用が必要になる文字だけ。
var idRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func validateID(field, value string) error {
	if value == "" {
		return codedErrorf(http.StatusBadRequest, CodeValidationFailed, "%s が必要です", field)
	}
	if !idRe.MatchString(value) {
		return codedErrorf(http.StatusBadRequest, CodeValidationFailed,
			"%s の形式が不正です (英数字とハイフン・アンダースコアのみ、64文字まで)", field)
	}
	return nil
}
```

- [ ] **Step 6: `plugin.go` を実装する**

`plugins/rolelevel/plugin.go`:

```go
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
```

- [ ] **Step 7: GREEN を確認する**

```powershell
$env:GOWORK = "off"
go -C plugins/rolelevel test ./... -count=1 -run "TestMigrations|TestLoadConfig|TestPluginNameMatchesManifest"
```

Expected: PASS。

- [ ] **Step 8: vet・build・fmt を確認する**

```powershell
$env:GOWORK = "off"
go -C plugins/rolelevel vet ./...
go -C plugins/rolelevel build ./...
gofmt -l plugins/rolelevel
```

Expected: `gofmt -l` が出力しないこと。出力があれば `gofmt -w plugins/rolelevel` を実行する。

- [ ] **Step 9: commit する**

```powershell
git add .gitignore plugins/rolelevel/go.mod plugins/rolelevel/go.sum plugins/rolelevel/mk-plugin.yml plugins/rolelevel/plugin.go plugins/rolelevel/errors.go plugins/rolelevel/main_test.go plugins/rolelevel/plugin_test.go
git commit -m "Phase roleLevel: add role-level plugin module and owned tables"
```

Expected: 1 commit。`git diff --stat HEAD~1 -- migration/` が空であること (core schema に触っていない)。

---

### Task 2: native policy catalog と host側drift gate

**Files:**
- Create: `plugins/rolelevel/native_policy_catalog.json`
- Create: `plugins/rolelevel/catalog.go`
- Create: `plugins/rolelevel/catalog_test.go`
- Create: `internal/entitycompat/rolelevel_policy_catalog_test.go`

**Interfaces:**
- Consumes: `effectivepolicy.Defaults()` (host moduleのみ), Task 1 の `invalid` / `CodeUnknownPolicyKey` / `CodeInvalidRangeValue` / `ValidationError`
- Produces:
  - `type Kind string` with `KindBoolean/KindNumber/KindString/KindStringSet`, `func (k Kind) Numeric() bool`
  - `type Catalog struct`, `func (c *Catalog) Keys() []string`, `func (c *Catalog) Kind(key string) (Kind, bool)`, `func (c *Catalog) NumericKey(key string) bool`, `func (c *Catalog) NormalizeConst(key string, value any) (any, error)`, `func (c *Catalog) AcceptsValue(key string, value any) bool`
  - `var defaultCatalog *Catalog`
  - host側: `TestRoleLevelCatalogMatchesNativeDefaults`, `TestRoleLevelCatalogEnumsAcceptTheNativeDefault`

- [ ] **Step 1: RED — catalogの契約を固定する**

`plugins/rolelevel/catalog_test.go`:

```go
package rolelevel

import "testing"

// **未知の native policy key は拒否する** ので、catalog に無い key を range に
// 書けてはいけない。catalog 自体が host の既定とずれていたら「拒否すべき key を
// 受け入れる」ことになる。Task 2 Step 6 の drift gate がそれを host 側から止める。
func TestCatalogCoversOnlyKnownKinds(t *testing.T) {
	for _, key := range defaultCatalog.Keys() {
		kind, ok := defaultCatalog.Kind(key)
		if !ok {
			t.Fatalf("%q が Kind を持ちません", key)
		}
		switch kind {
		case KindBoolean, KindNumber, KindString, KindStringSet:
		default:
			t.Fatalf("%q の kind %q が不正です", key, kind)
		}
	}
	if _, ok := defaultCatalog.Kind("noSuchPolicyKey"); ok {
		t.Fatal("未知の key を admitted しています")
	}
}

func TestCatalogKeysAreSortedAndUnique(t *testing.T) {
	keys := defaultCatalog.Keys()
	for i := 1; i < len(keys); i++ {
		if keys[i-1] >= keys[i] {
			t.Fatalf("ソート順が崩れています (%q >= %q)", keys[i-1], keys[i])
		}
	}
}

// multiplier は **numeric な key だけ**に許される。boolean や enum に「1段ごとに
// 1.5倍」は意味が無いし、host 側の集約も乗算を受けない。
func TestCatalogNumericKeys(t *testing.T) {
	for _, tt := range []struct {
		key  string
		want bool
	}{
		{"canPublicNote", false},
		{"chatAvailability", false},
		{"uploadableFileTypes", false},
		{"noteEachClipsLimit", true},
		{"userListLimit", true},
		{"rateLimitFactor", true},
		{"noSuchPolicyKey", false},
	} {
		if got := defaultCatalog.NumericKey(tt.key); got != tt.want {
			t.Fatalf("NumericKey(%q) = %t, want %t", tt.key, got, tt.want)
		}
	}
}

// JSON 経由の const は float64 / bool / string / []any で届く。native の数値 policy は
// 整数なので、受理する前に int64 へ寄せる (1.5 を黙って 1 にしない)。
func TestCatalogNormalizeConst(t *testing.T) {
	got, err := defaultCatalog.NormalizeConst("noteEachClipsLimit", float64(200))
	if err != nil {
		t.Fatal(err)
	}
	if got != int64(200) {
		t.Fatalf("NormalizeConst = %#v (%T), want int64(200)", got, got)
	}
	if _, err := defaultCatalog.NormalizeConst("noteEachClipsLimit", 1.5); err == nil {
		t.Fatal("小数は受理しています")
	}
	if _, err := defaultCatalog.NormalizeConst("canPublicNote", "true"); err == nil {
		t.Fatal("boolean に string を受理しています")
	}
	if _, err := defaultCatalog.NormalizeConst("chatAvailability", "nope"); err == nil {
		t.Fatal("enum に未知の値を受理しています")
	}
	if v, err := defaultCatalog.NormalizeConst("chatAvailability", "readonly"); err != nil || v != "readonly" {
		t.Fatalf("enum の受理: %v %v", v, err)
	}
	if v, err := defaultCatalog.NormalizeConst("uploadableFileTypes", []string{"text/*"}); err != nil || v == nil {
		t.Fatalf("string set の受理: %v %v", v, err)
	}
}
```

- [ ] **Step 2: RED を確認する**

```powershell
$env:GOWORK = "off"
go -C plugins/rolelevel test ./... -count=1 -run "TestCatalog"
```

Expected: FAIL — `undefined: defaultCatalog`, `undefined: Kind`。

- [ ] **Step 3: catalog data を書く**

`plugins/rolelevel/native_policy_catalog.json` — `internal/effectivepolicy/validation.go` の `defaults` map と**同じキー集合・同じ型**で書く。`enum` を持てるのは `chatAvailability` だけ:

```json
{
  "keys": [
    { "key": "alwaysMarkNsfw", "kind": "boolean" },
    { "key": "antennaLimit", "kind": "number" },
    { "key": "avatarDecorationLimit", "kind": "number" },
    { "key": "canCreateChannel", "kind": "boolean" },
    { "key": "canDeleteAccount", "kind": "boolean" },
    { "key": "canHideAds", "kind": "boolean" },
    { "key": "canImportAntennas", "kind": "boolean" },
    { "key": "canImportBlocking", "kind": "boolean" },
    { "key": "canImportFollowing", "kind": "boolean" },
    { "key": "canImportMuting", "kind": "boolean" },
    { "key": "canImportUserLists", "kind": "boolean" },
    { "key": "canInvite", "kind": "boolean" },
    { "key": "canManageAvatarDecorations", "kind": "boolean" },
    { "key": "canManageCustomEmojis", "kind": "boolean" },
    { "key": "canPublicNote", "kind": "boolean" },
    { "key": "canRequestCustomEmojis", "kind": "boolean" },
    { "key": "canSearchIpHistory", "kind": "boolean" },
    { "key": "canSearchNotes", "kind": "boolean" },
    { "key": "canSearchUsers", "kind": "boolean" },
    { "key": "canUpdateBioMedia", "kind": "boolean" },
    { "key": "canUseChunkedUpload", "kind": "boolean" },
    { "key": "canUseEmojiAsAvatarDecoration", "kind": "boolean" },
    { "key": "canUseTranslator", "kind": "boolean" },
    { "key": "chatAvailability", "kind": "string", "enum": ["available", "readonly", "unavailable"] },
    { "key": "chunkedUploadMaxConcurrentSessions", "kind": "number" },
    { "key": "chunkedUploadMaxPendingMb", "kind": "number" },
    { "key": "clipLimit", "kind": "number" },
    { "key": "driveCapacityMb", "kind": "number" },
    { "key": "emojiApplicationMaxPending", "kind": "number" },
    { "key": "emojiApplicationMaxPerDay", "kind": "number" },
    { "key": "emojiApplicationMaxPerMonth", "kind": "number" },
    { "key": "emojiApplicationMaxPerWeek", "kind": "number" },
    { "key": "gtlAvailable", "kind": "boolean" },
    { "key": "inviteExpirationTime", "kind": "number" },
    { "key": "inviteLimit", "kind": "number" },
    { "key": "inviteLimitCycle", "kind": "number" },
    { "key": "ltlAvailable", "kind": "boolean" },
    { "key": "maxFileSizeMb", "kind": "number" },
    { "key": "mentionLimit", "kind": "number" },
    { "key": "noteDraftLimit", "kind": "number" },
    { "key": "noteEachClipsLimit", "kind": "number" },
    { "key": "optOutNotificationTypes", "kind": "stringSet" },
    { "key": "pinLimit", "kind": "number" },
    { "key": "rateLimitFactor", "kind": "number" },
    { "key": "scheduledNoteLimit", "kind": "number" },
    { "key": "uploadableFileTypes", "kind": "stringSet" },
    { "key": "userEachUserListsLimit", "kind": "number" },
    { "key": "userListLimit", "kind": "number" },
    { "key": "watermarkAvailable", "kind": "boolean" },
    { "key": "webhookLimit", "kind": "number" },
    { "key": "wordMuteLimit", "kind": "number" }
  ]
}
```

- [ ] **Step 4: `catalog.go` を実装する**

`plugins/rolelevel/catalog.go`:

```go
package rolelevel

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
)

// nativePolicyCatalogJSON is the plugin's own copy of the host's native policy schema.
//
// **embed するのは「実行時にファイルが手元に無い」ため。** pluginはバイナリに
// 組み込まれるのでJSONを外置するとビルド環境でしか読めない形になる。
//
// **二重管理になるので host側drift gate が必ず見る。**
// `internal/entitycompat/rolelevel_policy_catalog_test.go` がこのファイルを
// `effectivepolicy.Defaults()` と突き合わせる。`/plugins/` を直接importできないので
// JSONファイル越しに見るのは、そのgateが同じファイルを読めるようにするため。
//
//go:embed native_policy_catalog.json
var nativePolicyCatalogJSON []byte

// Kind is the native policy value type a key accepts.
type Kind string

const (
	KindBoolean   Kind = "boolean"
	KindNumber    Kind = "number"
	KindString    Kind = "string"
	KindStringSet Kind = "stringSet"
)

// Numeric reports whether multiplier ranges may target this kind. Only numbers
// have an arithmetic meaning; a boolean or an enum has no per-level scaling, and
// the host's own aggregation does not multiply them.
func (k Kind) Numeric() bool { return k == KindNumber }

type catalogEntry struct {
	Key  string   `json:"key"`
	Kind Kind     `json:"kind"`
	Enum []string `json:"enum,omitempty"`
}

// Catalog is the plugin's copy of the host's native policy schema.
type Catalog struct {
	entries []catalogEntry
	byKey   map[string]catalogEntry
}

// defaultCatalog is the decoded embedded catalog. A decode failure is a build
// defect, not an operational state, so it panics with a message naming the file
// (otherwise the catalog tests would never run).
var defaultCatalog = mustLoadCatalog()

func mustLoadCatalog() *Catalog {
	var doc struct {
		Keys []catalogEntry `json:"keys"`
	}
	if err := json.Unmarshal(nativePolicyCatalogJSON, &doc); err != nil {
		panic(fmt.Sprintf("rolelevel: native_policy_catalog.json を解釈できません: %v", err))
	}
	c := &Catalog{entries: doc.Keys, byKey: make(map[string]catalogEntry, len(doc.Keys))}
	for _, e := range doc.Keys {
		if _, dup := c.byKey[e.Key]; dup {
			panic(fmt.Sprintf("rolelevel: native_policy_catalog.json に %q が重複しています", e.Key))
		}
		c.byKey[e.Key] = e
	}
	return c
}

// Keys returns every native policy key the plugin may touch, sorted.
func (c *Catalog) Keys() []string {
	out := make([]string, 0, len(c.entries))
	for _, e := range c.entries {
		out = append(out, e.Key)
	}
	slices.Sort(out)
	return out
}

// Kind reports the native type of key.
func (c *Catalog) Kind(key string) (Kind, bool) {
	e, ok := c.byKey[key]
	if !ok {
		return "", false
	}
	return e.Kind, true
}

// NumericKey reports whether key accepts a multiplier range.
func (c *Catalog) NumericKey(key string) bool {
	kind, ok := c.Kind(key)
	return ok && kind.Numeric()
}

// NormalizeConst converts a JSON-decoded constant into the Go value the host
// expects for key, rejecting anything the host's own contribution validation
// would reject.
//
// **小数を受理しない。** native の数値 policy は整数で、host 側は `numberValid` で
// 「int に収まるか」しか見てないので 1.5 は通って無言で解釈が変わる。保存時に弾く。
func (c *Catalog) NormalizeConst(key string, value any) (any, error) {
	e, ok := c.byKey[key]
	if !ok {
		return nil, invalid(CodeUnknownPolicyKey, "policyRanges.key",
			"%q はネイティブの policy key ではありません", key)
	}
	switch e.Kind {
	case KindBoolean:
		b, isBool := value.(bool)
		if !isBool {
			return nil, invalid(CodeInvalidRangeValue, "policyRanges.value",
				"%q は boolean ですが %#v が渡されました", key, value)
		}
		return b, nil
	case KindNumber:
		n, err := asInteger(value)
		if err != nil {
			return nil, invalid(CodeInvalidRangeValue, "policyRanges.value",
				"%q は整数ですが %#v が渡されました (%s)", key, value, err)
		}
		if n < math.MinInt || n > math.MaxInt {
			return nil, invalid(CodeInvalidRangeValue, "policyRanges.value",
				"%q の値 %d が int の範囲外です", key, n)
		}
		return n, nil
	case KindString:
		s, isStr := value.(string)
		if !isStr {
			return nil, invalid(CodeInvalidRangeValue, "policyRanges.value",
				"%q は文字列ですが %#v が渡されました", key, value)
		}
		if len(e.Enum) > 0 && !slices.Contains(e.Enum, s) {
			return nil, invalid(CodeInvalidRangeValue, "policyRanges.value",
				"%q の値 %q は許列表にありません (%s)", key, s, strings.Join(e.Enum, ", "))
		}
		return s, nil
	case KindStringSet:
		items, err := asStringSlice(value)
		if err != nil {
			return nil, invalid(CodeInvalidRangeValue, "policyRanges.value",
				"%q は文字列の配列ですが %#v が渡されました (%s)", key, value, err)
		}
		return items, nil
	}
	return nil, invalid(CodeUnknownPolicyKey, "policyRanges.key", "%q の kind が不正です", key)
}

// AcceptsValue reports whether value is usable as a native policy value for key.
func (c *Catalog) AcceptsValue(key string, value any) bool {
	_, err := c.NormalizeConst(key, value)
	return err == nil
}

// nativeNumber renders a computed multiplier value as the integer the host stores.
//
// **floor する。** native の数値 policy は整数で、1.5 を返すと host 側の集約側で
// 解釈が変わるため。
func nativeNumber(v float64) (any, error) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil, invalid(CodeInvalidRangeValue, "policyRanges", "計算結果が有限値になりません")
	}
	return int64(math.Floor(v)), nil
}

// asInteger accepts the shapes a JSON number or a Go integer arrives in and
// rejects anything with a fractional part.
func asInteger(value any) (int64, error) {
	switch v := value.(type) {
	case int:
		return int64(v), nil
	case int64:
		return v, nil
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, fmt.Errorf("有限値ではありません")
		}
		if v != math.Trunc(v) {
			return 0, fmt.Errorf("小数は不正です")
		}
		if v < math.MinInt64 || v > math.MaxInt64 {
			return 0, fmt.Errorf("int64 の範囲外です")
		}
		return int64(v), nil
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			return 0, err
		}
		return n, nil
	}
	return 0, fmt.Errorf("数値ではありません")
}

// asStringSlice accepts []string and the []any that encoding/json produces.
func asStringSlice(value any) ([]string, error) {
	switch v := value.(type) {
	case []string:
		return v, nil
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("要素が文字列ではありません")
			}
			out = append(out, s)
		}
		return out, nil
	}
	return nil, fmt.Errorf("配列ではありません")
}
```

- [ ] **Step 5: GREEN を確認する**

```powershell
$env:GOWORK = "off"
go -C plugins/rolelevel test ./... -count=1 -run "TestCatalog"
```

Expected: PASS。

- [ ] **Step 6: host側drift gate を書く (RED を確認する)**

`internal/entitycompat/rolelevel_policy_catalog_test.go`:

```go
package entitycompat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/shiroha-a/mk/internal/effectivepolicy"
)

// roleLevel plugin は `internal/` を import できないので、native policy の schema を
// **自前の JSON ファイル**として持つ。**二重管理は放置できない。** ずれると plugin は
// 「拒否すべき key を受け入れる」「native が受け付けない型を contribution する」状態
// になる。host 側はそれを検証しない — `ValidateContributions` は宣言された key しか
// 見ないので、catalog が host とずれていても通ってしまう。
//
// そのため **host側の既定値と突き合わせる gate をここに置く**。
func TestRoleLevelCatalogMatchesNativeDefaults(t *testing.T) {
	type entry struct {
		Key  string `json:"key"`
		Kind string `json:"kind"`
	}
	var doc struct {
		Keys []entry `json:"keys"`
	}
	path := filepath.Join("..", "..", "plugins", "rolelevel", "native_policy_catalog.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if len(doc.Keys) == 0 {
		t.Fatalf("%s に key がありません (列挙が壊れています)", path)
	}

	want := map[string]string{}
	for key, native := range effectivepolicy.Defaults() {
		switch native.(type) {
		case bool:
			want[key] = "boolean"
		case int:
			want[key] = "number"
		case string:
			want[key] = "string"
		case []string:
			want[key] = "stringSet"
		default:
			// 新しい kind が host 側に入ったのに catalog が unaware なら、plugin は
			// その key を level で変えられない。黙って無視すると「その policy は
			// level では動かない」ことが運営者に説明できない。
			t.Fatalf("native default %q の型 %T は catalog の kind に対応していません", key, native)
		}
	}

	got := map[string]string{}
	for _, e := range doc.Keys {
		if _, dup := got[e.Key]; dup {
			t.Errorf("%s に %q が重複しています", path, e.Key)
		}
		got[e.Key] = e.Kind
	}

	for key, kind := range want {
		gotKind, ok := got[key]
		if !ok {
			t.Errorf("native default %q (%s) が %s にありません", key, kind, path)
			continue
		}
		if gotKind != kind {
			t.Errorf("%q の kind が %s ですが native 側では %s です", key, gotKind, kind)
		}
	}
	for key := range got {
		if _, ok := want[key]; !ok {
			t.Errorf("%s に %q がありますが native default にありません", path, key)
		}
	}
}

// 制約付き文字列の enum は、**native 側の既定値自身を受け入れられること**。
// 見ているのは「enum を取りこぼしていないか」で、enum の一覧そのものは
// `effectivepolicy.valueValid` の手写字なので二重に持たない。
func TestRoleLevelCatalogEnumsAcceptTheNativeDefault(t *testing.T) {
	type entry struct {
		Key  string   `json:"key"`
		Enum []string `json:"enum,omitempty"`
	}
	var doc struct {
		Keys []entry `json:"keys"`
	}
	path := filepath.Join("..", "..", "plugins", "rolelevel", "native_policy_catalog.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}

	defaults := effectivepolicy.Defaults()
	var constrained []string
	for _, e := range doc.Keys {
		if len(e.Enum) == 0 {
			continue
		}
		constrained = append(constrained, e.Key)
		def, ok := defaults[e.Key].(string)
		if !ok {
			t.Errorf("%q に enum がありますが native default が文字列ではありません", e.Key)
			continue
		}
		if !containsString(e.Enum, def) {
			t.Errorf("%q の enum に native default %q が含まれていません (%v)", e.Key, def, e.Enum)
		}
	}
	sort.Strings(constrained)
	// 制約付き enum を持つのは今のところ chatAvailability だけ。増えたときは値を
	// 丸めて通さないよう、この行を明示的に直す。
	if len(constrained) != 1 || constrained[0] != "chatAvailability" {
		t.Errorf("enum を持つ key が %v です。chatAvailability 以外が増えたら、この gate を明示的に直す必要があります", constrained)
	}
}

func containsString(haystack []string, needle string) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}
```

**この test が RED になることを確認する** — `native_policy_catalog.json` に存在しないキーを足した状態で一度走らせる:

```powershell
# 一時的に fake な1行を足して RED を見る (実行後すぐに戻す)
Add-Content -Path "plugins/rolelevel/native_policy_catalog.json" -Value '    { "key": "zzzNotANativeKey", "kind": "boolean" },'
go test ./internal/entitycompat/... -run "TestRoleLevelCatalog" -count=1
```

Expected: FAIL with `zzzNotANativeKey がありますが native default にありません`。

```powershell
# 追加した行を戻す
$catalog = Get-Content "plugins/rolelevel/native_policy_catalog.json" |
    Where-Object { $_ -notmatch "zzzNotANativeKey" }
Set-Content "plugins/rolelevel/native_policy_catalog.json" $catalog
go test ./internal/entitycompat/... -run "TestRoleLevelCatalog" -count=1
```

Expected: PASS。`Set-Content` は既定でUTF-8 (BOMなし) を書くので、元のJSONとバイト列が一致することを確認してよい (`git diff --stat plugins/rolelevel/native_policy_catalog.json` が空ならOK)。

- [ ] **Step 7: commit する**

```powershell
gofmt -w plugins/rolelevel/catalog.go plugins/rolelevel/catalog_test.go internal/entitycompat/rolelevel_policy_catalog_test.go
go test ./internal/entitycompat/... -run "TestRoleLevelCatalog" -count=1
$env:GOWORK = "off"
go -C plugins/rolelevel test ./... -count=1 -run "TestCatalog"
git add plugins/rolelevel/native_policy_catalog.json plugins/rolelevel/catalog.go plugins/rolelevel/catalog_test.go internal/entitycompat/rolelevel_policy_catalog_test.go
git commit -m "Phase roleLevel: pin the native policy catalog with a drift gate"
```

---

### Task 3: level domain と XP curve

**Files:**
- Create: `plugins/rolelevel/level.go`
- Create: `plugins/rolelevel/level_test.go`
- Create: `plugins/rolelevel/policyrange.go` (Task 4 が実装する型とスタブのみ)

**Interfaces:**
- Consumes: Task 1 の `invalid` / `CodeInvalidCurve` / `CodeInvalidBaseLevel` / `ValidationError`, Task 2 の `Catalog` / `defaultCatalog`
- Produces:
  - `const MaxExperience int64 = 1<<53 - 1` (JSON number / Go safe integer の上限)
  - `const MaxExperienceFloat = 9007199254740991.0`
  - `type CurveType string` with `CurveConst/CurveLinear/CurveExponential`
  - `type Curve struct { Type CurveType; LevelUps int64; Base int64; Additional int64; Exponential float64 }` (json: `type/levelUps/base/additional/exponential`)
  - `type Config struct { RoleID string; BaseLevel int64; ExperienceCurve []Curve; PolicyRanges []PolicyRange; Revision int64; UpdatedBy string; CreatedAt, UpdatedAt time.Time }`
  - `func DefaultConfig() Config`
  - `func (c Curve) LevelUpCost(n int64) (float64, error)`
  - `func (c Curve) SegmentCumExp(k int64) (float64, error)`
  - `func (c Curve) ValidateCosts() error`
  - `func (c Curve) TotalLevelUps() int64`
  - `func (c Config) TotalLevelUps() int64`, `func (c Config) MinLevel() int64`, `func (c Config) MaxLevel() int64`, `func (c Config) TotalExperienceThreshold() (float64, error)`, `func (c Config) Experience(totalExp int64) (Experience, error)`, `func (c Config) Validate(cat *Catalog) error`
  - `type Experience struct { CurrentLevel, CurrentLevelExp, TotalExp, MinLevel, MaxLevel, ProgressionStage int64; NextLevelExp *int64 }` (json: `currentLevel/currentLevelExp/nextLevelExp/totalExp/minLevel/maxLevel/progressionStage`)
  - `type RangeType string` with `RangeBase/RangeConst/RangeMultiplier`, `type PolicyRange struct { Type RangeType; Key string; Start, End int64; Value any; Base, Additional float64 }`
  - `func validateRanges(ranges []PolicyRange, levelUps int64, cat *Catalog) error` — Task 4 が本実装に置き換えるスタブ

- [ ] **Step 1: RED — spec の Level Model テストを書く**

`plugins/rolelevel/level_test.go`:

```go
package rolelevel

import (
	"math"
	"testing"
)

// **既定値は全作成経路で統一する。** baseLevel 1 / const 100 XP × 99 level-ups /
// effective level 1..100。
func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.BaseLevel != 1 {
		t.Fatalf("baseLevel = %d, want 1", cfg.BaseLevel)
	}
	if cfg.TotalLevelUps() != 99 {
		t.Fatalf("level-ups = %d, want 99", cfg.TotalLevelUps())
	}
	if cfg.MinLevel() != 1 || cfg.MaxLevel() != 100 {
		t.Fatalf("level range = %d..%d, want 1..100", cfg.MinLevel(), cfg.MaxLevel())
	}
	if err := cfg.Validate(defaultCatalog); err != nil {
		t.Fatalf("既定の設定が validation を通りません: %v", err)
	}

	exp, err := cfg.Experience(0)
	if err != nil {
		t.Fatal(err)
	}
	if exp.CurrentLevel != 1 || exp.ProgressionStage != 1 {
		t.Fatalf("XP 0 のとき = %+v, want currentLevel 1 / stage 1", exp)
	}
	// 99 × 100 = 9900 で最大 level に届く。
	exp, err = cfg.Experience(9900)
	if err != nil {
		t.Fatal(err)
	}
	if exp.CurrentLevel != 100 || exp.NextLevelExp != nil || exp.CurrentLevelExp != 0 {
		t.Fatalf("最大 level = %+v, want currentLevel 100 / nextLevelExp nil / exp 0", exp)
	}
	// 最大 level 超過の余剰は currentLevelExp に持ち越す。
	exp, err = cfg.Experience(9950)
	if err != nil {
		t.Fatal(err)
	}
	if exp.CurrentLevel != 100 || exp.CurrentLevelExp != 50 || exp.ProgressionStage != 100 {
		t.Fatalf("最大 level 超過 = %+v", exp)
	}
	if exp.NextLevelExp != nil {
		t.Fatalf("nextLevelExp = %d, want nil", *exp.NextLevelExp)
	}
}

// **baseLevel は負数・0・正数を許可する。** stage は常に 1 始まり。
func TestNegativeAndZeroBaseLevel(t *testing.T) {
	for _, tt := range []struct {
		name      string
		baseLevel int64
	}{
		{"負数", -10},
		{"0", 0},
		{"正数", 5},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.BaseLevel = tt.baseLevel
			if err := cfg.Validate(defaultCatalog); err != nil {
				t.Fatal(err)
			}
			exp, err := cfg.Experience(0)
			if err != nil {
				t.Fatal(err)
			}
			if exp.CurrentLevel != tt.baseLevel {
				t.Fatalf("XP 0 = %+v, want currentLevel %d", exp, tt.baseLevel)
			}
			if exp.MaxLevel != tt.baseLevel+99 {
				t.Fatalf("maxLevel = %d, want %d", exp.MaxLevel, tt.baseLevel+99)
			}
			if exp.ProgressionStage != 1 {
				t.Fatalf("stage = %d, want 1 (baseLevel に依らない)", exp.ProgressionStage)
			}
			exp, err = cfg.Experience(9900)
			if err != nil {
				t.Fatal(err)
			}
			if exp.CurrentLevel != tt.baseLevel+99 || exp.ProgressionStage != 100 {
				t.Fatalf("到達 = %+v, want currentLevel %d / stage 100", exp, tt.baseLevel+99)
			}
		})
	}
}

// **明示的に空の curve は baseLevel に固定する。** nextLevelExp は無い。
func TestEmptyCurvePinsTheLevel(t *testing.T) {
	cfg := Config{BaseLevel: 7, ExperienceCurve: []Curve{},
		PolicyRanges: []PolicyRange{{Type: RangeBase, Start: 1, End: 1}}}
	if err := cfg.Validate(defaultCatalog); err != nil {
		t.Fatal(err)
	}
	for _, total := range []int64{0, 1, 1000000} {
		exp, err := cfg.Experience(total)
		if err != nil {
			t.Fatal(err)
		}
		if exp.CurrentLevel != 7 || exp.NextLevelExp != nil || exp.ProgressionStage != 1 {
			t.Fatalf("total %d = %+v, want currentLevel 7 / nextLevelExp nil / stage 1", total, exp)
		}
		if exp.CurrentLevelExp != total {
			t.Fatalf("total %d の余剰 = %d, want %d", total, exp.CurrentLevelExp, total)
		}
	}
}

func TestCurveCostsAreFractional(t *testing.T) {
	t.Run("const", func(t *testing.T) {
		c := Curve{Type: CurveConst, LevelUps: 3, Base: 100}
		for n, want := range []float64{100, 100, 100} {
			got, err := c.LevelUpCost(int64(n))
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("cost(%d) = %v, want %v", n, got, want)
			}
		}
		if got, err := c.SegmentCumExp(3); err != nil || got != 300 {
			t.Fatalf("cum(3) = %v %v, want 300", got, err)
		}
	})
	t.Run("linear", func(t *testing.T) {
		c := Curve{Type: CurveLinear, LevelUps: 4, Base: 100, Additional: 50}
		for n, want := range []float64{100, 150, 200, 250} {
			got, err := c.LevelUpCost(int64(n))
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("cost(%d) = %v, want %v", n, got, want)
			}
		}
		if got, err := c.SegmentCumExp(4); err != nil || got != 700 {
			t.Fatalf("cum(4) = %v %v, want 700", got, err)
		}
	})
	// **1 level ごとに切り上げない。** コストは実数のまま積算する。
	t.Run("exponential keeps the fraction", func(t *testing.T) {
		c := Curve{Type: CurveExponential, LevelUps: 4, Base: 1, Additional: 1, Exponential: 1.5}
		for n, want := range []float64{2, 2.5, 3.25, 4.375} {
			got, err := c.LevelUpCost(int64(n))
			if err != nil {
				t.Fatal(err)
			}
			if math.Abs(got-want) > 1e-9 {
				t.Fatalf("cost(%d) = %v, want %v", n, got, want)
			}
		}
		// 2 + 2.5 + 3.25 + 4.375 = 12.125
		if got, err := c.SegmentCumExp(4); err != nil || math.Abs(got-12.125) > 1e-9 {
			t.Fatalf("cum(4) = %v %v, want 12.125", got, err)
		}
	})
	// **e == 1 は専用処理する。** (e^k - 1)/(e - 1) は 0/0 になる。
	t.Run("exponential with ratio 1", func(t *testing.T) {
		c := Curve{Type: CurveExponential, LevelUps: 3, Base: 10, Additional: 5, Exponential: 1}
		for n, want := range []float64{15, 15, 15} {
			got, err := c.LevelUpCost(int64(n))
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("cost(%d) = %v, want %v", n, got, want)
			}
		}
		if got, err := c.SegmentCumExp(3); err != nil || got != 45 {
			t.Fatalf("cum(3) = %v %v, want 45", got, err)
		}
	})
	// **閉形式は整数の等比数列で検算できる。** e = 2 の和は 2^k - 1。
	t.Run("exponential closed form", func(t *testing.T) {
		c := Curve{Type: CurveExponential, LevelUps: 10, Base: 0, Additional: 1, Exponential: 2}
		got, err := c.SegmentCumExp(10)
		if err != nil {
			t.Fatal(err)
		}
		if got != 1023 {
			t.Fatalf("cum(10) = %v, want 1023", got)
		}
	})
	// **1 に近い ratio でも桁が飛ばない。** 素の `(Pow(e,k)-1)/(e-1)` は
	// e = 1+1e-12, k = 100 で 4 桁以上落ちる。Log1p / Expm1 の等価形なら
	// 100.0000000049 がそのまま出る。
	t.Run("exponential near 1 stays stable", func(t *testing.T) {
		c := Curve{Type: CurveExponential, LevelUps: 100, Base: 0, Additional: 1,
			Exponential: 1.000000000001}
		if err := c.ValidateCosts(); err != nil {
			t.Fatalf("検証で弾かれました: %v", err)
		}
		got, err := c.SegmentCumExp(100)
		if err != nil {
			t.Fatal(err)
		}
		if math.Abs(got-100.0000000049) > 1e-6 {
			t.Fatalf("cum(100) = %v, want 100.0000000049 付近 (誤差 1e-6 以内)", got)
		}
	})
}

// **整数XP は小数しきい値に ceil で到達し、currentLevelExp は floor(整数XP - しきい値)。**
// 次の level までは ceil(次のしきい値) - 整数XP。
func TestFractionsRoundCorrectly(t *testing.T) {
	cfg := Config{
		BaseLevel:       0,
		ExperienceCurve: []Curve{{Type: CurveExponential, LevelUps: 2, Base: 1, Additional: 1, Exponential: 1.5}},
		PolicyRanges:    []PolicyRange{{Type: RangeBase, Start: 1, End: 3}},
	}
	if err := cfg.Validate(defaultCatalog); err != nil {
		t.Fatal(err)
	}
	// cost = 2, 2.5 → 累積 2, 4.5
	for _, tt := range []struct {
		total       int64
		wantLevel   int64
		wantCurrent int64
		wantNext    int64
	}{
		{0, 0, 0, 2},
		{1, 0, 1, 1},
		{2, 1, 0, 3},
		{3, 1, 1, 2},
		{4, 1, 2, 1},
		// 最大 level 到達。しきい値 4.5 に対して余剰は floor(5 - 4.5) = 0。
		{5, 2, 0, -1},
	} {
		exp, err := cfg.Experience(tt.total)
		if err != nil {
			t.Fatal(err)
		}
		if exp.CurrentLevel != tt.wantLevel {
			t.Fatalf("total %d: currentLevel = %d, want %d", tt.total, exp.CurrentLevel, tt.wantLevel)
		}
		if exp.CurrentLevelExp != tt.wantCurrent {
			t.Fatalf("total %d: currentLevelExp = %d, want %d", tt.total, exp.CurrentLevelExp, tt.wantCurrent)
		}
		if tt.wantNext < 0 {
			if exp.NextLevelExp != nil {
				t.Fatalf("total %d: nextLevelExp = %d, want nil", tt.total, *exp.NextLevelExp)
			}
			continue
		}
		if exp.NextLevelExp == nil {
			t.Fatalf("total %d: nextLevelExp がありません", tt.total)
		}
		if *exp.NextLevelExp != tt.wantNext {
			t.Fatalf("total %d: nextLevelExp = %d, want %d", tt.total, *exp.NextLevelExp, tt.wantNext)
		}
	}
}

// **rule をまたぐ offset は float64 で持ち越す。** 1つ目が 2.5 まで伸びたら、
// 2つ目は 0 ではなく 2.5 から積算する (2.5 + 3.25 = 5.75)。
func TestOffsetCarriesAcrossRules(t *testing.T) {
	cfg := Config{
		BaseLevel: 0,
		ExperienceCurve: []Curve{
			// cost = 1, 1.5 → 累積 2.5
			{Type: CurveExponential, LevelUps: 2, Base: 0, Additional: 1, Exponential: 1.5},
			// cost = 1, 2.25 → この rule の寄与 3.25
			{Type: CurveExponential, LevelUps: 2, Base: 0, Additional: 1, Exponential: 2.25},
		},
		PolicyRanges: []PolicyRange{{Type: RangeBase, Start: 1, End: 5}},
	}
	if err := cfg.Validate(defaultCatalog); err != nil {
		t.Fatal(err)
	}
	total, err := cfg.TotalExperienceThreshold()
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(total-5.75) > 1e-9 {
		t.Fatalf("累積しきい値 = %v, want 5.75 (2.5 + 3.25)", total)
	}
	// 累積は 1 / 2.5 / 3.5 / 5.75
	for _, tt := range []struct {
		total       int64
		wantLevel   int64
		wantCurrent int64
		wantNext    int64
	}{
		{0, 0, 0, 1},
		{1, 1, 0, 2},
		{2, 1, 1, 1},
		// 2 つ目の rule の先頭。しきい値 2.5 に対して余剰は floor(3 - 2.5) = 0。
		{3, 2, 0, 1},
		// 3 つ目の level の途中。しきい値 3.5 に対して floor(5 - 3.5) = 1。
		{5, 3, 1, 1},
		{6, 4, 0, -1},
	} {
		exp, err := cfg.Experience(tt.total)
		if err != nil {
			t.Fatal(err)
		}
		if exp.CurrentLevel != tt.wantLevel {
			t.Fatalf("total %d: currentLevel = %d, want %d", tt.total, exp.CurrentLevel, tt.wantLevel)
		}
		if exp.CurrentLevelExp != tt.wantCurrent {
			t.Fatalf("total %d: currentLevelExp = %d, want %d", tt.total, exp.CurrentLevelExp, tt.wantCurrent)
		}
		if tt.wantNext < 0 {
			if exp.NextLevelExp != nil {
				t.Fatalf("total %d: nextLevelExp = %d, want nil", tt.total, *exp.NextLevelExp)
			}
			continue
		}
		if exp.NextLevelExp == nil || *exp.NextLevelExp != tt.wantNext {
			t.Fatalf("total %d: nextLevelExp = %v, want %d", tt.total, exp.NextLevelExp, tt.wantNext)
		}
	}
}

// **rule ごとに walk し、その中で binary search する。** 大きい level 数でも
// O(S log L) で答えが出る。
func TestLargeLevelUpCountUsesClosedForm(t *testing.T) {
	small := Config{BaseLevel: 0,
		ExperienceCurve: []Curve{{Type: CurveConst, LevelUps: 1, Base: 10}},
		PolicyRanges:    []PolicyRange{{Type: RangeBase, Start: 1, End: 2}}}
	large := Config{BaseLevel: 0,
		ExperienceCurve: []Curve{{Type: CurveConst, LevelUps: 100_000_000, Base: 10}},
		PolicyRanges:    []PolicyRange{{Type: RangeBase, Start: 1, End: 100_000_001}}}

	gotSmall, err := small.Experience(9_999_999)
	if err != nil {
		t.Fatal(err)
	}
	if gotSmall.CurrentLevel != 1 {
		t.Fatalf("1 段の curve = %+v, want currentLevel 1 (10 XP で最大)", gotSmall)
	}
	gotLarge, err := large.Experience(9_999_999)
	if err != nil {
		t.Fatal(err)
	}
	if gotLarge.CurrentLevel != 999_999 || gotLarge.ProgressionStage != 1_000_000 {
		t.Fatalf("1 億段の curve = %+v, want currentLevel 999999 / stage 1000000", gotLarge)
	}
	// e == 1 の巨大な rule も誤差なく扱える (3 XP × 5000 万 = 1.5 億)。
	geo := Curve{Type: CurveExponential, LevelUps: 50_000_000, Base: 1, Additional: 2, Exponential: 1}
	if got, err := geo.SegmentCumExp(50_000_000); err != nil || got != 150_000_000 {
		t.Fatalf("exponential cum = %v %v, want 150000000", got, err)
	}
}

// **保存時に全部落とす。** NaN / Infinity / overflow / コスト 0 以下を
// 「policy 解決のとき初めて起きる」形に持たない。
func TestCurveValidationRejectsUnsafeCurves(t *testing.T) {
	for _, tt := range []struct {
		name string
		seg  Curve
		span int64
	}{
		{"未知の type", Curve{Type: "quadratic", LevelUps: 1, Base: 1}, 2},
		{"levelUps 0", Curve{Type: CurveConst, LevelUps: 0, Base: 1}, 1},
		{"levelUps 負", Curve{Type: CurveConst, LevelUps: -1, Base: 1}, 1},
		{"コストが 0", Curve{Type: CurveConst, LevelUps: 1, Base: 0}, 2},
		{"コストが負", Curve{Type: CurveConst, LevelUps: 1, Base: -5}, 2},
		{"コストが 0 を横切る", Curve{Type: CurveLinear, LevelUps: 3, Base: 1, Additional: -1}, 4},
		{"累積が上限超", Curve{Type: CurveConst, LevelUps: 1_000_000, Base: MaxExperience}, 1_000_001},
		{"exponential が 0", Curve{Type: CurveExponential, LevelUps: 2, Base: 1, Additional: 1, Exponential: 0}, 3},
		{"exponential が負", Curve{Type: CurveExponential, LevelUps: 2, Base: 1, Additional: 1, Exponential: -2}, 3},
		{"exponential が NaN", Curve{Type: CurveExponential, LevelUps: 2, Base: 1, Additional: 1, Exponential: math.NaN()}, 3},
		{"exponential が Inf", Curve{Type: CurveExponential, LevelUps: 2, Base: 1, Additional: 1, Exponential: math.Inf(1)}, 3},
		{"exponential が有限値を超える", Curve{Type: CurveExponential, LevelUps: 8, Base: 1, Additional: 1, Exponential: 1e300}, 9},
		{"exponential のコストが 0 になる", Curve{Type: CurveExponential, LevelUps: 4, Base: 10, Additional: -10, Exponential: 2}, 5},
		{"base が safe integer 超", Curve{Type: CurveConst, LevelUps: 2, Base: MaxExperience, Additional: 0}, 3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{BaseLevel: 0, ExperienceCurve: []Curve{tt.seg},
				PolicyRanges: []PolicyRange{{Type: RangeBase, Start: 1, End: tt.span}}}
			err := cfg.Validate(defaultCatalog)
			if err == nil {
				t.Fatal("拒否していません")
			}
			ve, ok := err.(*ValidationError)
			if !ok {
				t.Fatalf("ValidationError ではありません: %v", err)
			}
			if ve.Code != CodeInvalidCurve {
				t.Fatalf("code = %q, want %q (%v)", ve.Code, CodeInvalidCurve, err)
			}
		})
	}
}

// **負の additional は許す。** コストが 0 より大きければ通る。
func TestCurveValidationAllowsNegativeAdditional(t *testing.T) {
	cfg := Config{BaseLevel: 0,
		ExperienceCurve: []Curve{{Type: CurveLinear, LevelUps: 4, Base: 100, Additional: -20}},
		PolicyRanges:    []PolicyRange{{Type: RangeBase, Start: 1, End: 5}}}
	if err := cfg.Validate(defaultCatalog); err != nil {
		t.Fatalf("負の additional を拒否しました: %v", err)
	}
	exp, err := cfg.Experience(100 + 80 + 60 + 40)
	if err != nil {
		t.Fatal(err)
	}
	if exp.CurrentLevel != 4 || exp.CurrentLevelExp != 0 {
		t.Fatalf("= %+v, want currentLevel 4", exp)
	}
}

// **rule をまたいでも累積が上限内に収まること。** 各 rule ごとではなく合計で見る。
func TestCurveValidationRejectsGrandTotalOverflow(t *testing.T) {
	cfg := Config{BaseLevel: 0,
		ExperienceCurve: []Curve{
			{Type: CurveConst, LevelUps: 5, Base: 1_000_000_000_000_000},
			{Type: CurveConst, LevelUps: 5, Base: 1_000_000_000_000_000},
		},
		PolicyRanges: []PolicyRange{{Type: RangeBase, Start: 1, End: 11}}}
	err := cfg.Validate(defaultCatalog)
	if err == nil {
		t.Fatal("rule をまたいで上限を超える累積を受理しています")
	}
	ve, ok := err.(*ValidationError)
	if !ok || ve.Code != CodeInvalidCurve {
		t.Fatalf("code = %+v, want %s (%v)", err, CodeInvalidCurve, err)
	}
}

func TestExperienceRejectsNegativeTotal(t *testing.T) {
	if _, err := DefaultConfig().Experience(-1); err == nil {
		t.Fatal("負の総経験値を受理しています")
	}
}
```

- [ ] **Step 2: RED を確認する**

Step 1 のテストを適用し、Step 3 の本体を**まだ入れていない**状態で走らせる。`PolicyRange` の型もまだ無いので、コンパイルエラーが出るのが正しい:

```powershell
$env:GOWORK = "off"
go -C plugins/rolelevel test ./... -count=1 -run "TestDefaultConfig|TestNegativeAndZeroBaseLevel|TestEmptyCurve|TestCurveCosts|TestLargeLevelUpCount|TestCurveValidation|TestExperienceRejects|TestFractionsRound|TestOffsetCarries"
```

Expected: FAIL — `undefined: RangeBase`, `undefined: Config`, `undefined: Curve` など。

- [ ] **Step 3: `level.go` を実装する**

`plugins/rolelevel/level.go`:

```go
package rolelevel

import (
	"errors"
	"fmt"
	"math"
	"time"
)

// MaxExperience is the largest experience value the plugin stores or returns.
//
// **JSON number / Go safe integer の上限**。frontend へ返すと number になり、2^53 を
// 超えると 1 しか変わらない精度になるため。string や full uint64 にはしない。
const MaxExperience int64 = 1<<53 - 1

// MaxExperienceFloat is MaxExperience as a float64, for threshold comparisons.
const MaxExperienceFloat = 9007199254740991.0

// CurveType names one XP curve segment shape.
type CurveType string

const (
	// CurveConst costs Base for every level-up.
	CurveConst CurveType = "const"
	// CurveLinear costs Base + Additional*n.
	CurveLinear CurveType = "linear"
	// CurveExponential costs Base + Additional*Exponential**n.
	CurveExponential CurveType = "exponential"
)

// Curve is one XP curve segment. LevelUps is how many level-ups it covers and the
// cost of its n-th level-up (0-based, n restarts at every rule) is:
//
//	const:       Base
//	linear:      Base + Additional*n
//	exponential: Base + Additional*Exponential**n
//
// **コストは実数のまま積算する。1 level ごとに切り上げない。** 1.5 刻みの curve を
// 設定すると level の切り替わりが 2.5 / 5.75 のように半端になる。保存される XP は整数で
// あり、しきい値には ceil で到達し (spec)、currentLevelExp は
// floor(整数XP - しきい値) で表示する (TestFractionsRoundCorrectly が固定する)。
//
// Base と Additional は整数で受ける。JSON の数として往復しても 2^53 を超える精度を
// 保持できないため、整数であることが検証の前提になる。
type Curve struct {
	Type        CurveType `json:"type"`
	LevelUps    int64     `json:"levelUps"`
	Base        int64     `json:"base"`
	Additional  int64     `json:"additional"`
	Exponential float64   `json:"exponential,omitempty"`
}

// Config is one role's level configuration, the plugin-owned counterpart of the
// native role row.
type Config struct {
	RoleID          string        `json:"roleId"`
	BaseLevel       int64         `json:"baseLevel"`
	ExperienceCurve []Curve       `json:"experienceCurve"`
	PolicyRanges    []PolicyRange `json:"policyRanges"`
	Revision        int64         `json:"revision"`
	UpdatedBy       string        `json:"updatedBy"`
	CreatedAt       time.Time     `json:"createdAt"`
	UpdatedAt       time.Time     `json:"updatedAt"`
}

// DefaultConfig returns the configuration every creation path starts from.
func DefaultConfig() Config {
	return Config{
		BaseLevel:       1,
		ExperienceCurve: []Curve{{Type: CurveConst, LevelUps: 99, Base: 100}},
		PolicyRanges:    []PolicyRange{{Type: RangeBase, Start: 1, End: 100}},
	}
}

// TotalLevelUps is how many level-ups the segment contains.
func (c Curve) TotalLevelUps() int64 { return c.LevelUps }

// TotalLevelUps is how many level-ups the curve contains. An empty curve is 0,
// which pins the level at baseLevel.
func (c Config) TotalLevelUps() int64 {
	var total int64
	for _, seg := range c.ExperienceCurve {
		total += seg.LevelUps
	}
	return total
}

// MinLevel is the level an assignment with 0 experience has.
func (c Config) MinLevel() int64 { return c.BaseLevel }

// MaxLevel is the level the last level-up reaches.
func (c Config) MaxLevel() int64 { return c.BaseLevel + c.TotalLevelUps() }

// LevelUpCost returns the experience the n-th level-up of the segment costs.
//
// **切り上げない。** 実数のまま返す。n は rule 内 0 始まり。
func (c Curve) LevelUpCost(n int64) (float64, error) {
	var cost float64
	switch c.Type {
	case CurveConst:
		cost = float64(c.Base)
	case CurveLinear:
		cost = float64(c.Base) + float64(c.Additional)*float64(n)
	case CurveExponential:
		cost = float64(c.Base) + float64(c.Additional)*math.Pow(c.Exponential, float64(n))
	default:
		return 0, fmt.Errorf("rolelevel: curve type %q が不正です", c.Type)
	}
	if math.IsNaN(cost) || math.IsInf(cost, 0) {
		return 0, fmt.Errorf("rolelevel: %s segment の cost(%d) が有限値になりません", c.Type, n)
	}
	return cost, nil
}

// SegmentCumExp returns the fractional experience threshold for completing k
// level-ups of the segment. k must be 0..LevelUps.
//
// **3型とも閉形式**なので、level 数に比例する loop をしない:
//
//	const:   k*Base
//	linear:  k*Base + (k*(k-1)/2)*Additional
//	exponential: k*Base + Additional * (e**k - 1)/(e - 1)
//
// 指数の和は geoSum に閉じ込めてあるので、e == 1 と 1 に近い e の扱いが 1 箇所に
// 集まる。
func (c Curve) SegmentCumExp(k int64) (float64, error) {
	if k < 0 || k > c.LevelUps {
		return 0, fmt.Errorf("rolelevel: k=%d は segment の範囲 (0..%d) 外です", k, c.LevelUps)
	}
	kf := float64(k)
	var total float64
	switch c.Type {
	case CurveConst:
		total = kf * float64(c.Base)
	case CurveLinear:
		pairs := kf * (kf - 1) / 2
		total = kf*float64(c.Base) + pairs*float64(c.Additional)
	case CurveExponential:
		total = kf*float64(c.Base) + float64(c.Additional)*geoSum(c.Exponential, kf)
	default:
		return 0, fmt.Errorf("rolelevel: curve type %q が不正です", c.Type)
	}
	if math.IsNaN(total) || math.IsInf(total, 0) {
		return 0, fmt.Errorf("rolelevel: %s segment の累積 (k=%d) が有限値になりません", c.Type, k)
	}
	return total, nil
}

// geoSum returns (e**k - 1) / (e - 1) for a positive finite ratio e.
//
// **e == 1 は k** (各項が e**n = 1 なので和は k)。それ以外は素の
// `(math.Pow(e, k) - 1) / (e - 1)` を使わず **Expm1 を外側にもつ形**にする。
// 素の式は e が 1 に近いと分子 (e**k - 1) が 1 と同桁になって打ち消しが起き、
// 桁が 10 桁以上落ちる。Log1p / Expm1 の等価形なら 1 に近い e でも誤差が桁数に
// 比例しない。
func geoSum(e, k float64) float64 {
	if e == 1 {
		return k
	}
	return math.Expm1(k*math.Log1p(e-1)) / (e - 1)
}

// completedLevelUps returns how many level-ups of the segment the given budget pays
// for. It binary searches the closed-form cumulative, so the cost does not depend
// on how deep the segment is.
//
// budget is the *remaining* budget after the previous rules, so a fractional offset
// carries across rule boundaries.
func (c Curve) completedLevelUps(budget float64) (int64, error) {
	if budget <= 0 {
		return 0, nil
	}
	lo, hi := int64(0), c.LevelUps
	for lo < hi {
		mid := lo + (hi-lo+1)/2
		sum, err := c.SegmentCumExp(mid)
		if err != nil {
			return 0, err
		}
		if sum <= budget {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo, nil
}

// Experience is the response shape for one role and one experience total.
type Experience struct {
	CurrentLevel    int64 `json:"currentLevel"`
	CurrentLevelExp int64 `json:"currentLevelExp"`
	// NextLevelExp is null at the maximum level or when there is no curve.
	NextLevelExp     *int64 `json:"nextLevelExp"`
	TotalExp         int64  `json:"totalExp"`
	MinLevel         int64  `json:"minLevel"`
	MaxLevel         int64  `json:"maxLevel"`
	ProgressionStage int64 `json:"progressionStage"`
}

// TotalExperienceThreshold returns the fractional experience needed to reach the
// maximum level. It is the sum of every rule's cumulative, so a fractional offset
// from one rule carries into the next (2.5 + 3.25 = 5.75).
func (c Config) TotalExperienceThreshold() (float64, error) {
	var offset float64
	for _, seg := range c.ExperienceCurve {
		sum, err := seg.SegmentCumExp(seg.LevelUps)
		if err != nil {
			return 0, err
		}
		offset += sum
	}
	return offset, nil
}

// Experience maps a stored (integer) experience total onto the level model.
//
// **rule を順に walk し、その中で binary search する。** rule 数 S、1 rule の最大 level
// 数 L として O(S log L) 時間・O(1) メモリ。累積はすべて閉形式で求める。
//
// **整数XP は小数しきい値に ceil で到達する。** 累積が 2 / 4.5 のとき XP 4 は level 1 で
// currentLevelExp 2、XP 5 で level 2。currentLevelExp は floor(整数XP - しきい値)。
//
// **JSON で表現できない NaN を返す経路が無い。** すべてのしきい値が有限であることを
// Validate で保証してからしかここに到達しない。
func (c Config) Experience(totalExp int64) (Experience, error) {
	if totalExp < 0 {
		return Experience{}, fmt.Errorf("rolelevel: 総経験値 %d は負です", totalExp)
	}
	levelUps := c.TotalLevelUps()
	maxLevel := c.MaxLevel()
	total := float64(totalExp)
	// **offset は rule をまたいで持ち越す。** ここを 0 に戻すと前の rule で 0.5 余った
	// 分が消える (2.5 + 3.25 が 3.25 になってしまう)。
	offset := 0.0
	completed := int64(0)

	for _, seg := range c.ExperienceCurve {
		segTotal, err := seg.SegmentCumExp(seg.LevelUps)
		if err != nil {
			return Experience{}, err
		}
		if total < offset+segTotal {
			budget := total - offset
			if budget < 0 {
				budget = 0
			}
			k, err := seg.completedLevelUps(budget)
			if err != nil {
				return Experience{}, err
			}
			done, err := seg.SegmentCumExp(k)
			if err != nil {
				return Experience{}, err
			}
			threshold := offset + done
			// **次のしきい値は「次の1 level の cost を足したもの」**。rule 全体の
			// 累積 (segTotal) ではない — それは rule を走り終えた先で、まだ到達して
			// いない。`seg.SegmentCumExp(k+1)` を使う。
			nextThreshold, err := seg.SegmentCumExp(k + 1)
			if err != nil {
				return Experience{}, err
			}
			next := int64(math.Ceil(offset+nextThreshold)) - totalExp
			return Experience{
				CurrentLevel:     c.BaseLevel + completed + k,
				CurrentLevelExp:  floorExperience(total - threshold),
				NextLevelExp:     &next,
				TotalExp:         totalExp,
				MinLevel:         c.MinLevel(),
				MaxLevel:         maxLevel,
				ProgressionStage: completed + k + 1,
			}, nil
		}
		offset += segTotal
		completed += seg.LevelUps
	}

	// 最大 level 到達後の余剰は currentLevelExp に持ち越す。nextLevelExp は無い。
	return Experience{
		CurrentLevel:     maxLevel,
		CurrentLevelExp:  floorExperience(total - offset),
		NextLevelExp:     nil,
		TotalExp:         totalExp,
		MinLevel:         c.MinLevel(),
		MaxLevel:         maxLevel,
		ProgressionStage: levelUps + 1,
	}, nil
}

// floorExperience computes floor(整数XP - 小数しきい値) with a floor at 0.
//
// **`totalExp - floor(threshold)` ではない。** 小数しきい値の端が次の level の
// 進捗として残るので、XP を先に floor すると余りを取り違える (しきい値 2.5 に
// 整数XP 4 なら floor(4 - 2.5) = 1)。
func floorExperience(remaining float64) int64 {
	f := math.Floor(remaining)
	if f < 0 {
		return 0
	}
	return int64(f)
}

// Validate rejects a configuration the plugin could not evaluate later.
//
// **保存時にだけ走る。** policy 解決のたびに防御しないのは、そのための設定が保存時点で
// 拒まれているから。
func (c Config) Validate(cat *Catalog) error {
	if c.BaseLevel < -MaxExperience || c.BaseLevel > MaxExperience {
		return invalid(CodeInvalidBaseLevel, "baseLevel",
			"%d..%d の範囲で指定してください (%d)", -MaxExperience, MaxExperience, c.BaseLevel)
	}
	var offset float64
	for i, seg := range c.ExperienceCurve {
		if err := validateCurveSegment(seg); err != nil {
			// field 名に index を足すだけで、code と条文は segment 検査が持つ。
			var ve *ValidationError
			if errors.As(err, &ve) {
				return &ValidationError{Code: ve.Code,
					Field: fmt.Sprintf("experienceCurve[%d].%s", i, ve.Field), Err: ve.Err}
			}
			return err
		}
		sum, err := seg.SegmentCumExp(seg.LevelUps)
		if err != nil {
			return invalid(CodeInvalidCurve, "experienceCurve", "%s", err)
		}
		offset += sum
		// **rule をまたいだ合計も見ておく。** 各 rule 之内だけなら通るが、合計が上限を
		// 超えると Experience の currentLevelExp が壊れる。
		if math.IsNaN(offset) || math.IsInf(offset, 0) || offset > MaxExperienceFloat {
			return invalid(CodeInvalidCurve, "experienceCurve",
				"curve 全体の累積 (%v) が有限かつ %v 以下である必要があります", offset, MaxExperienceFloat)
		}
	}
	return validateRanges(c.PolicyRanges, c.TotalLevelUps(), cat)
}

// validateCurveSegment rejects one segment whose evaluation would leave the
// safe-integer range or produce a non-positive, NaN or infinite level-up cost.
func validateCurveSegment(seg Curve) error {
	switch seg.Type {
	case CurveConst, CurveLinear, CurveExponential:
	default:
		return invalid(CodeInvalidCurve, "type",
			"type %q は %s|%s|%s のいずれかです", seg.Type, CurveConst, CurveLinear, CurveExponential)
	}
	if seg.LevelUps < 1 {
		return invalid(CodeInvalidCurve, "levelUps", "1 以上にしてください (%d)", seg.LevelUps)
	}
	if seg.Base < -MaxExperience || seg.Base > MaxExperience {
		return invalid(CodeInvalidCurve, "base", "%d..%d の範囲で指定してください (%d)",
			-MaxExperience, MaxExperience, seg.Base)
	}
	if seg.Additional < -MaxExperience || seg.Additional > MaxExperience {
		return invalid(CodeInvalidCurve, "additional", "%d..%d の範囲で指定してください (%d)",
			-MaxExperience, MaxExperience, seg.Additional)
	}
	if seg.Type == CurveExponential {
		if math.IsNaN(seg.Exponential) || math.IsInf(seg.Exponential, 0) || seg.Exponential <= 0 {
			return invalid(CodeInvalidCurve, "exponential",
				"0 より大きい有限数にしてください (%v)", seg.Exponential)
		}
	}
	return seg.ValidateCosts()
}

// ValidateCosts checks that every level-up cost of the segment is finite and greater
// than zero.
//
// **両端だけ見る。** コストは n の1次式 (const / linear) か単調な指数関数
// (exponential) なので、最小と最大は必ず n = 0 と n = LevelUps-1 にある。両端が条件を
// 満たしていれば中間も満たす。**全 level を走査しない**のは、level 数が 1 億でも O(1)
// で済ませるため。
func (c Curve) ValidateCosts() error {
	check := func(n int64) error {
		cost, err := c.LevelUpCost(n)
		if err != nil {
			return invalid(CodeInvalidCurve, "levelUps", "%s", err)
		}
		if cost <= 0 {
			return invalid(CodeInvalidCurve, "levelUps",
				"%d 番目の level-up の必要経験値 %v が 0 より大きい必要があります", n, cost)
		}
		return nil
	}
	if err := check(0); err != nil {
		return err
	}
	return check(c.LevelUps - 1)
}
```

- [ ] **Step 4: `policyrange.go` の型とスタブを置いて GREEN を確認する**

Task 4 が本実装に置き換えるが、Task 3 の test が `RangeBase` を参照するので型を先に置く。`plugins/rolelevel/policyrange.go` を新規作成:

```go
package rolelevel

// RangeType names one level-based policy rule shape.
type RangeType string

const (
	// RangeBase keeps the instance / native default. **置換 contribution も出す**ので、
	// native の静的 role contribution が生き残らない (Task 9)。
	RangeBase RangeType = "base"
	// RangeConst uses a fixed value.
	RangeConst RangeType = "const"
	// RangeMultiplier scales from Base by Additional per stage inside the range.
	RangeMultiplier RangeType = "multiplier"
)

// PolicyRange replaces the native policy of Key for the progression stages
// [Start, End). Stages are 1-based, Start is inclusive and End is exclusive, so
// the ranges of one config tile 1..levelUps+1 without overlapping.
type PolicyRange struct {
	Type  RangeType `json:"type"`
	Key   string    `json:"key,omitempty"`
	Start int64     `json:"start"`
	End   int64     `json:"end"`
	// Value is the const value; it is only meaningful for RangeConst.
	Value any `json:"value,omitempty"`
	// Base and Additional drive RangeMultiplier only. They are read as float64
	// because the multiplier is computed in floating point and floored.
	Base       float64 `json:"base,omitempty"`
	Additional float64 `json:"additional,omitempty"`
}

// validateRanges is implemented in Task 4.
func validateRanges(_ []PolicyRange, _ int64, _ *Catalog) error { return nil }
```

```powershell
$env:GOWORK = "off"
go -C plugins/rolelevel test ./... -count=1 -run "TestDefaultConfig|TestNegativeAndZeroBaseLevel|TestEmptyCurve|TestCurveCosts|TestLargeLevelUpCount|TestCurveValidation|TestExperienceRejects|TestFractionsRound|TestOffsetCarries"
```

Expected: PASS。

- [ ] **Step 5: commit する**

```powershell
gofmt -w plugins/rolelevel/level.go plugins/rolelevel/policyrange.go plugins/rolelevel/level_test.go
$env:GOWORK = "off"
go -C plugins/rolelevel vet ./...
go -C plugins/rolelevel test ./... -count=1
git add plugins/rolelevel/level.go plugins/rolelevel/policyrange.go plugins/rolelevel/level_test.go
git commit -m "Phase roleLevel: add the pure level model and fractional XP curves"
```

---
### Task 4: policy range と置換値の計算

**Files:**
- Modify: `plugins/rolelevel/policyrange.go` (`validateRanges` のスタブを本実装に置換し、`rangeForStage` / `rangeValue` / `validateRangeRules` を追加)
- Create: `plugins/rolelevel/policyrange_test.go`

**Interfaces:**
- Consumes: Task 2 の `Catalog` / `defaultCatalog` / `nativeNumber`, Task 3 の `RangeType` / `PolicyRange` / `Config`, Task 1 の `invalid` / `CodeInvalidRanges` / `CodeUnknownPolicyKey` / `CodeMultiplierNotNumeric` / `CodeInvalidRangeValue`
- Produces: `func validateRanges(ranges []PolicyRange, levelUps int64, cat *Catalog) error`, `func rangeForStage(ranges []PolicyRange, stage int64) (PolicyRange, bool)`, `func rangeValue(r PolicyRange, stage int64) (any, error)` (**`base` は nil を返す** — nil が「UseDefault: true の contribution」を意味するので bool は要らない), `const MaxPolicyRanges = 256`

- [ ] **Step 1: RED — rangeの契約を固定する**

`plugins/rolelevel/policyrange_test.go`:

```go
package rolelevel

import "testing"

func rangeCfg(ranges ...PolicyRange) Config {
	return Config{
		BaseLevel:       0,
		ExperienceCurve: []Curve{{Type: CurveConst, LevelUps: 4, Base: 100}},
		PolicyRanges:    ranges,
	}
}

// **range は半開区間で、1 から levelUps+1 まで重複も欠落も無く敷き詰める。**
// 旧実装の inclusive boundary は level 境界で必ず重複していた。
func TestValidateRangesRequiresAnExactTiling(t *testing.T) {
	for _, tt := range []struct {
		name   string
		ranges []PolicyRange
	}{
		{"空", nil},
		{"先頭が 2 から始まる", []PolicyRange{{Type: RangeBase, Start: 2, End: 6}}},
		{"1 を飛ばす", []PolicyRange{{Type: RangeBase, Start: 1, End: 2}, {Type: RangeBase, Start: 3, End: 6}}},
		{"重複", []PolicyRange{{Type: RangeBase, Start: 1, End: 3}, {Type: RangeBase, Start: 2, End: 6}}},
		{"途中が欠ける", []PolicyRange{{Type: RangeBase, Start: 1, End: 2}, {Type: RangeBase, Start: 4, End: 6}}},
		{"合計が足りない", []PolicyRange{{Type: RangeBase, Start: 1, End: 5}}},
		{"合計が多い", []PolicyRange{{Type: RangeBase, Start: 1, End: 7}}},
		{"空の range がある", []PolicyRange{{Type: RangeBase, Start: 1, End: 1}, {Type: RangeBase, Start: 1, End: 6}}},
		{"end < start", []PolicyRange{{Type: RangeBase, Start: 6, End: 1}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := rangeCfg(tt.ranges...).Validate(defaultCatalog); err == nil {
				t.Fatalf("受理しています: %+v", tt.ranges)
			}
		})
	}
}

func TestValidateRangesAcceptsOneCoveringBaseRange(t *testing.T) {
	if err := rangeCfg(PolicyRange{Type: RangeBase, Start: 1, End: 5}).Validate(defaultCatalog); err != nil {
		t.Fatal(err)
	}
}

// **未知の native policy key は拒否する。** 拒否しないと、host 側が見ない key に
// contribution して「設定したのに効かない」状態になる。
func TestValidateRangesRejectsUnknownKey(t *testing.T) {
	err := rangeCfg(PolicyRange{Type: RangeConst, Start: 1, End: 5, Key: "noSuchPolicyKey", Value: true}).
		Validate(defaultCatalog)
	if err == nil {
		t.Fatal("未知の key を受理しています")
	}
	ve, ok := err.(*ValidationError)
	if !ok || ve.Code != CodeUnknownPolicyKey {
		t.Fatalf("code = %+v, want %s (%v)", err, CodeUnknownPolicyKey, err)
	}
}

// **boolean と enum には const だけ。** 乗算の意味が無い上に、host 側の集約も乗算を
// 受けない。
func TestValidateRangesRejectsMultiplierOnNonNumericKey(t *testing.T) {
	for _, tt := range []struct{ key, name string }{
		{"canPublicNote", "boolean"},
		{"chatAvailability", "enum"},
		{"uploadableFileTypes", "string set"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := rangeCfg(PolicyRange{Type: RangeMultiplier, Start: 1, End: 5,
				Key: tt.key, Base: 1, Additional: 1}).Validate(defaultCatalog)
			if err == nil {
				t.Fatalf("%s に multiplier を受理しています", tt.key)
			}
			ve, ok := err.(*ValidationError)
			if !ok || ve.Code != CodeMultiplierNotNumeric {
				t.Fatalf("code = %+v, want %s (%v)", err, CodeMultiplierNotNumeric, err)
			}
		})
	}
}

func TestValidateRangesRejectsMismatchedConst(t *testing.T) {
	for _, tt := range []struct {
		name  string
		rule  PolicyRange
	}{
		{"boolean に文字列", PolicyRange{Type: RangeConst, Start: 1, End: 5, Key: "canPublicNote", Value: "true"}},
		{"数値に文字列", PolicyRange{Type: RangeConst, Start: 1, End: 5, Key: "noteEachClipsLimit", Value: "10"}},
		{"数値に小数", PolicyRange{Type: RangeConst, Start: 1, End: 5, Key: "noteEachClipsLimit", Value: 1.5}},
		{"enum に未知値", PolicyRange{Type: RangeConst, Start: 1, End: 5, Key: "chatAvailability", Value: "nope"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := rangeCfg(tt.rule).Validate(defaultCatalog)
			if err == nil {
				t.Fatalf("受理しています: %+v", tt.rule)
			}
			ve, ok := err.(*ValidationError)
			if !ok || ve.Code != CodeInvalidRangeValue {
				t.Fatalf("code = %+v, want %s (%v)", err, CodeInvalidRangeValue, err)
			}
		})
	}
}

// **multiplier は全 stage で native の数値範囲に収まらなければならない。** 端点2個だけ
// 見れば十分 (range 内で n の1次式なので単調で、中間にだけ範囲外になる値は無い)。
func TestValidateRangesRejectsOutOfRangeMultiplier(t *testing.T) {
	err := rangeCfg(PolicyRange{Type: RangeMultiplier, Start: 1, End: 5,
		Key: "noteEachClipsLimit", Base: 1, Additional: 1e19}).Validate(defaultCatalog)
	if err == nil {
		t.Fatal("int 範囲外の multiplier を受理しています")
	}
}

// multiplier の計算結果が非有限値になる設定は保存時に落とす。
func TestValidateRangesRejectsNonFiniteMultiplier(t *testing.T) {
	err := rangeCfg(PolicyRange{Type: RangeMultiplier, Start: 1, End: 5,
		Key: "noteEachClipsLimit", Base: 1e308, Additional: 1e308}).Validate(defaultCatalog)
	if err == nil {
		t.Fatal("受理しています")
	}
}

// **multiplier offset は range 内0始まりで、baseLevel の値に依存しない。**
func TestRangeValueMultiplierIgnoresBaseLevel(t *testing.T) {
	ranges := []PolicyRange{
		{Type: RangeBase, Start: 1, End: 2},
		{Type: RangeMultiplier, Start: 2, End: 6, Key: "noteEachClipsLimit", Base: 10, Additional: 5},
	}
	cfg := Config{BaseLevel: -10,
		ExperienceCurve: []Curve{{Type: CurveConst, LevelUps: 4, Base: 100}},
		PolicyRanges:    ranges}
	if err := cfg.Validate(defaultCatalog); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ stage, want int64 }{
		{2, 10}, {3, 15}, {4, 20}, {5, 25},
	} {
		got, err := rangeValue(ranges[1], tt.stage)
		if err != nil {
			t.Fatalf("stage %d: %v", tt.stage, err)
		}
		if got != tt.want {
			t.Fatalf("stage %d = %v (%T), want %d", tt.stage, got, got, tt.want)
		}
	}
	// baseLevel を変えても stage 1 は同じで、offset は baseLevel を見ていない。
	other := cfg
	other.BaseLevel = 1000
	exp, err := other.Experience(0)
	if err != nil {
		t.Fatal(err)
	}
	if exp.ProgressionStage != 1 {
		t.Fatalf("stage = %d, want 1", exp.ProgressionStage)
	}
	if got, _ := rangeValue(ranges[1], 3); got != int64(15) {
		t.Fatalf("baseLevel を変えても offset がずれた: %v", got)
	}
}

// **base range は nil を返す。** nil は「instance default を使う =
// UseDefault: true の contribution」を意味するので、resolver が native の静的
// contribution を落とせる。
func TestRangeValueBaseIsNil(t *testing.T) {
	got, err := rangeValue(PolicyRange{Type: RangeBase, Start: 1, End: 5}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("base range が値を返しました: %#v", got)
	}
}

func TestRangeForStageCoversEveryStageExactlyOnce(t *testing.T) {
	ranges := []PolicyRange{
		{Type: RangeConst, Start: 1, End: 3, Key: "canPublicNote", Value: false},
		{Type: RangeBase, Start: 3, End: 4},
		{Type: RangeConst, Start: 4, End: 6, Key: "canPublicNote", Value: true},
	}
	for stage := int64(1); stage <= 5; stage++ {
		if _, ok := rangeForStage(ranges, stage); !ok {
			t.Fatalf("stage %d を受ける range がありません", stage)
		}
	}
	if _, ok := rangeForStage(ranges, 6); ok {
		t.Fatal("stage 6 を受ける range がある")
	}
	if _, ok := rangeForStage(ranges, 0); ok {
		t.Fatal("stage 0 を受ける range がある")
	}
}
```

- [ ] **Step 2: RED を確認する**

Step 1 のテストを適用し、Step 3 の本体を**まだ入れていない**状態で走らせる。Task 3 が `validateRanges` をスタブ (`return nil`) で置いているため、range の検査は 1 件も機能していない:

```powershell
$env:GOWORK = "off"
go -C plugins/rolelevel test ./... -count=1 -run "TestValidateRanges|TestRangeValue|TestRangeForStage"
```

Expected: FAIL —

- `TestValidateRangesRequiresAnExactTiling/空` が「受理しています」で落ちる (スタブが常に nil を返す)
- `TestValidateRangesRequiresAnExactTiling/重複` が「受理しています」で落ちる
- `TestValidateRangesRejectsUnknownKey` が「未知の key を受理しています」で落ちる
- compile error: `undefined: rangeValue`
- compile error: `undefined: rangeForStage`

- [ ] **Step 3: `policyrange.go` に本体を実装する (スタブを置換)**

`plugins/rolelevel/policyrange.go` のスタブ `validateRanges` を削除し、次を追加:

```go
// MaxPolicyRanges bounds how many rules one config may carry. Each range is one
// policy key over a stage interval, so a sane configuration is a handful; the
// bound exists so a pathological payload cannot make validation itself the
// expensive part of a save.
const MaxPolicyRanges = 256

// validateRanges rejects a range list that does not tile the reachable progression
// exactly, then applies the per-key rules.
//
// **継ぎ目だけを見て合計長は最後の End から判定する。** 半開区間なので
// `ranges[i].Start == ranges[i-1].End` が重複も欠落も無いことの証明になる。
func validateRanges(ranges []PolicyRange, levelUps int64, cat *Catalog) error {
	if len(ranges) == 0 {
		return invalid(CodeInvalidRanges, "policyRanges",
			"最低1個必要です (既定は全範囲を覆う1個の base range)")
	}
	if len(ranges) > MaxPolicyRanges {
		return invalid(CodeInvalidRanges, "policyRanges",
			"%d 個以下で指定してください (%d)", MaxPolicyRanges, len(ranges))
	}
	next := int64(1)
	for i, r := range ranges {
		if r.Start != next {
			return invalid(CodeInvalidRanges, "policyRanges",
				"policyRanges[%d].start は %d であるべきですが %d です (重複または欠落があります)",
				i, next, r.Start)
		}
		if r.End <= r.Start {
			return invalid(CodeInvalidRanges, "policyRanges",
				"policyRanges[%d]: end (%d) は start (%d) より大きい必要があります", i, r.End, r.Start)
		}
		next = r.End
	}
	if want := levelUps + 1; next-1 != want {
		return invalid(CodeInvalidRanges, "policyRanges",
			"policyRanges の合計長 (%d) は到達可能 level 数 (%d) と一致する必要があります", next-1, want)
	}
	return validateRangeRules(ranges, cat)
}

// validateRangeRules rejects rules the host could not accept: unknown native keys,
// multipliers on non-numeric keys, and constants or multiplier results that do
// not match the native type.
func validateRangeRules(ranges []PolicyRange, cat *Catalog) error {
	for i, r := range ranges {
		field := fmt.Sprintf("policyRanges[%d]", i)
		kind, known := cat.Kind(r.Key)
		if !known {
			return invalid(CodeUnknownPolicyKey, field+".key",
				"%q はネイティブの policy key ではありません", r.Key)
		}
		switch r.Type {
		case RangeBase:
			// instance / native default を使うので key も値も要らない。
		case RangeConst:
			if _, err := cat.NormalizeConst(r.Key, r.Value); err != nil {
				return reField(err, field+".value")
			}
		case RangeMultiplier:
			if !kind.Numeric() {
				return invalid(CodeMultiplierNotNumeric, field,
					"%q は %s なので multiplier は使えません (const で指定してください)", r.Key, kind)
			}
			// 両端だけ見る。range 内の値は n の1次式なので単調で、端点が
			// 受理されるなら中間も受理される。**全 stage を走査しないのは
			// levelUps が 9e15 になりうるため** (走査すると保存が落ちる)。
			for _, stage := range []int64{r.Start, r.End - 1} {
				v, err := rangeValue(r, stage)
				if err != nil {
					return err
				}
				if !cat.AcceptsValue(r.Key, v) {
					return invalid(CodeInvalidRangeValue, field,
						"stage %d の値 %v がネイティブの %q の型・範囲に合いません", stage, v, r.Key)
				}
			}
		default:
			return invalid(CodeInvalidRanges, field+".type",
				"type %q は %s|%s|%s のいずれかです", r.Type, RangeBase, RangeConst, RangeMultiplier)
		}
	}
	return nil
}

// reField re-labels a validation error with a more specific field path.
func reField(err error, field string) error {
	var ve *ValidationError
	if errors.As(err, &ve) {
		return &ValidationError{Code: ve.Code, Field: field, Err: ve.Err}
	}
	return err
}

// rangeForStage returns the range covering stage. The list is validated to tile
// the progression without gaps, so at most one range matches.
func rangeForStage(ranges []PolicyRange, stage int64) (PolicyRange, bool) {
	for _, r := range ranges {
		if stage >= r.Start && stage < r.End {
			return r, true
		}
	}
	return PolicyRange{}, false
}

// rangeValue resolves one range into the value the native policy should take.
//
// **base は nil を返す。** 呼び出し側は nil を「instance default を使う =
// UseDefault: true の contribution」として扱う。base なのか「値が落ちた」のかを
// 区別できるので、base の contribution を黙って落とす形的ミスが起きない。
func rangeValue(r PolicyRange, stage int64) (any, error) {
	switch r.Type {
	case RangeBase:
		return nil, nil
	case RangeConst:
		return r.Value, nil
	case RangeMultiplier:
		// offset は range 内の0始まり位置。**baseLevel は見ない** (旧実装の
		// `effectiveLevel - startLevel` ずれは再現しない)。
		offset := float64(stage - r.Start)
		return nativeNumber(r.Base + r.Additional*offset)
	}
	return nil, invalid(CodeInvalidRanges, "policyRanges.type",
		"type %q は %s|%s|%s のいずれかです", r.Type, RangeBase, RangeConst, RangeMultiplier)
}
```

`policyrange.go` の import に `errors` を追加すること。`itoa` は Task 2 の `catalog.go` では**削除済み**なので、`policyrange.go` に `strconv` を import して使うか、`fmt.Sprintf` に置き換える。**`fmt.Sprintf` を使う** (新しい helper を増やさない):

```go
		field := fmt.Sprintf("policyRanges[%d]", i)
```

`policyrange.go` の import は次の3つ:

```go
import (
	"errors"
	"fmt"
)
```

- [ ] **Step 4: GREEN を確認する**

```powershell
$env:GOWORK = "off"
go -C plugins/rolelevel test ./... -count=1 -run "TestValidateRanges|TestRangeValue|TestRangeForStage"
```

Expected: PASS。Step 2 で出した 4 つの失敗 (range 受理・未知 key・未定義 symbol) がすべて消える。

- [ ] **Step 5: plugin 全体の test・vet・fmt を確認する**

```powershell
$env:GOWORK = "off"
go -C plugins/rolelevel test ./... -count=1
go -C plugins/rolelevel vet ./...
gofmt -l plugins/rolelevel
```

Expected: 全て PASS、`gofmt -l` が出力しないこと。

- [ ] **Step 6: commit する**

```powershell
git add plugins/rolelevel/policyrange.go plugins/rolelevel/policyrange_test.go
git commit -m "Phase roleLevel: validate level-based policy ranges"
```

---

### Task 5: plugin-owned storage

**Files:**
- Create: `plugins/rolelevel/store.go`
- Create: `plugins/rolelevel/store_test.go`

**Interfaces:**
- Consumes: Task 1 の `codedErrorf` / `CodeStorageFailed` / `service` / `context` / `log/slog`, Task 3 の `Config` / `Curve` / `PolicyRange` / `MaxExperience`
- Produces:
  - `type queryer interface { ExecContext(...); QueryContext(...); QueryRowContext(...) }` — `*sql.DB` と `*sql.Tx` の両方を満たす
  - `type store struct { db *sql.DB }`
  - `type experienceRow struct { AssignmentID, RoleID, UserID string; Experience int64; CreatedAt, UpdatedAt time.Time }`
  - `type operation struct { IdempotencyKey, ActorID, UserID, RoleID, Mode string; Operand float64; DesiredExp *int64; AssignmentID, Status, LastError string; CreatedAt, UpdatedAt time.Time }` (operand は `double precision` なので有限の小数を保持する)
  - `type auditEntry struct { ActorID, Operation, RoleID, UserID, AssignmentID, Note string; Before, After map[string]any; CreatedAt time.Time }`
  - `func (s *store) UpsertConfig(ctx, cfg Config, expectRevision int64) (Config, error)`
  - `func (s *store) LoadConfig(ctx, roleID string) (Config, bool, error)`
  - `func (s *store) LoadConfigsForRoles(ctx, roleIDs []string) (map[string]Config, error)`
  - `func (s *store) ListConfigs(ctx) ([]Config, error)`
  - `func (s *store) DeleteConfig(ctx, roleID string, expectRevision int64) (bool, error)`
  - `func (s *store) ExperienceForAssignments(ctx, assignmentIDs []string) (map[string]int64, error)`
  - `func (s *store) ExperienceRowsForRoleUser(ctx, roleID, userID string) ([]experienceRow, error)`
  - `func (s *store) ExperienceRowsForRole(ctx, roleID string, limit, offset int) ([]experienceRow, error)`
  - `func (s *store) SetExperience(ctx, q queryer, row experienceRow) error`
  - `func (s *store) InsertAudit(ctx, q queryer, entry auditEntry) error`
  - `func (s *store) RecentAudit(ctx, roleID, userID string, limit int) ([]auditEntry, error)`
  - `func (s *store) InsertOperation(ctx, op operation) (created bool, err error)`
  - `func (s *store) LoadOperation(ctx, key string) (operation, bool, error)`
  - `func (s *store) SetOperationStatus(ctx, q queryer, key string, status Status, assignmentID string, desiredExp *int64, lastErr string) error`
  - `func (s *store) ResumableOperations(ctx, limit int) ([]operation, error)`
  - `func (s *store) DeleteOrphanExperience(ctx, roleID string, olderThan time.Time) (int64, error)`
  - `func placeholders(n int) string`

- [ ] **Step 1: RED — storage の契約を固定する**

`plugins/rolelevel/store_test.go`:

```go
package rolelevel

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *store {
	t.Helper()
	return &store{db: testDB(t)}
}

func TestStoreConfigRoundTrip(t *testing.T) {
	db := testDB(t)
	s := &store{db: db}
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
		t.Fatalf("往復で_curve が壊れています: %+v", got)
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
	db := testDB(t)
	s := &store{db: db}
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

func TestStoreDeleteConfig(t *testing.T) {
	db := testDB(t)
	s := &store{db: db}
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
		t.Fatalf("2回目はdeleted = true になるべき: %t %v", deleted, err)
	}
}

// **XP は assignment_id が primary key。** unassign/reassign で古い XP が復活しないのは
// ここが根拠。
func TestStoreExperienceIsKeyedByAssignment(t *testing.T) {
	db := testDB(t)
	s := &store{db: db}
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

// **空のリストは「行が無い」= XP 0。** `IN ()` に落としてはいけない。
func TestStoreExperienceForEmptyList(t *testing.T) {
	db := testDB(t)
	s := &store{db: db}
	got, err := s.ExperienceForAssignments(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("空リストで %d 件返しました", len(got))
	}
}

func TestStoreExperienceRowsForRolePaging(t *testing.T) {
	db := testDB(t)
	s := &store{db: db}
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		row := experienceRow{
			AssignmentID: string(rune('a' + i)), RoleID: "role1",
			UserID: "u" + string(rune('0'+i)), Experience: int64(i * 10),
		}
		if err := s.SetExperience(ctx, s.db, row); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.ExperienceRowsForRole(ctx, "role1", 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("limit 2 で %d 行", len(rows))
	}
	// XP 降順。同じ値は assignment id 昇順で決定的に並べる。
	if rows[0].Experience < rows[1].Experience {
		t.Fatalf("XP 降順になっていません: %+v", rows)
	}
}

func TestStoreOperationLifecycle(t *testing.T) {
	db := testDB(t)
	s := &store{db: db}
	ctx := context.Background()

	op := operation{
		IdempotencyKey: "key1", ActorID: "admin1", UserID: "u1", RoleID: "role1",
		Mode: string(ModeAdd), Operand: 50.5, Status: string(StatusPending),
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

	if err := s.SetOperationStatus(ctx, s.db, "key1", StatusAssigning, "", nil, ""); err != nil {
		t.Fatal(err)
	}
	loaded, found, err := s.LoadOperation(ctx, "key1")
	if err != nil || !found {
		t.Fatalf("load: %t %v", found, err)
	}
	if loaded.Status != string(StatusAssigning) {
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
	if err := s.SetOperationStatus(ctx, s.db, "key1", StatusCompleted, "asg1", &desired, ""); err != nil {
		t.Fatal(err)
	}
	loaded, _, err = s.LoadOperation(ctx, "key1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != string(StatusCompleted) || loaded.AssignmentID != "asg1" ||
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
	db := testDB(t)
	s := &store{db: db}
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
	if got[0].Before["experience"].(float64) != 10 {
		t.Fatalf("新しい順になっていません: %+v", got)
	}
	// role 側だけの絞り込み。
	roleOnly, err := s.RecentAudit(ctx, "role1", "u2", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(roleOnly) != 0 {
		t.Fatalf("user で絞れていません: %+v", roleOnly)
	}
}

// **orphan 行は保持期間後まで消さない。** 監査のために残すのが目的。
func TestStoreDeleteOrphanExperienceRespectsRetention(t *testing.T) {
	db := testDB(t)
	s := &store{db: db}
	ctx := context.Background()

	old := experienceRow{AssignmentID: "asg-old", RoleID: "role1", UserID: "u1", Experience: 10}
	fresh := experienceRow{AssignmentID: "asg-new", RoleID: "role1", UserID: "u2", Experience: 20}
	for _, row := range []experienceRow{old, fresh} {
		if err := s.SetExperience(ctx, s.db, row); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`UPDATE role_level_experience
		SET updated_at = now() - interval '90 days' WHERE assignment_id = 'asg-old'`); err != nil {
		t.Fatal(err)
	}

	n, err := s.DeleteOrphanExperience(ctx, "role1", []string{"asg-old", "asg-new"}, time.Now().Add(-30*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("削除 = %d, want 1 (保持期間内の asg-new は残す)", n)
	}
	got, err := s.ExperienceForAssignments(ctx, []string{"asg-old", "asg-new"})
	if err != nil {
		t.Fatal(err)
	}
	if _, alive := got["asg-new"]; !alive {
		t.Fatal("保持期間内の行が消えています")
	}
	if _, alive := got["asg-old"]; alive {
		t.Fatal("保持期間を超えた行が残っています")
	}
}

// **plugin table のエラーは stable code を連れて外に出る。** 素の error を返すと
// host が 500 に丸めるので、frontend が storage 起因と分からない。
func TestStoreWrapsErrors(t *testing.T) {
	s := &store{db: nil}
	svc := &service{log: discardLogger()}
	_, err := svc.loadConfigOrStorageError(context.Background(), s, "role1")
	if err == nil {
		t.Fatal("nil DB でエラーになりません")
	}
	se, code := extractCode(err)
	if code != CodeStorageFailed {
		t.Fatalf("code = %q, want %s (%v)", code, CodeStorageFailed, err)
	}
	if se == nil {
		t.Fatalf("status error がありません: %v", err)
	}
}
```

`store_test.go` に残る2つの helper を同じファイルの末尾に追加する:

```go
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
```

`store_test.go` の import は次の4つにする (`database/sql` は test 側では使わないので外す):

```go
import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/shiroha-a/mk/plugin"
)
```

- [ ] **Step 2: RED を確認する**

```powershell
$env:GOWORK = "off"
go -C plugins/rolelevel test ./... -count=1 -run "TestStore"
```

Expected: FAIL — `undefined: store`, `undefined: experienceRow`, `undefined: operation`, `undefined: auditEntry`, `undefined: ModeAdd`, `undefined: StatusPending`。

- [ ] **Step 3: `store.go` を実装する**

`plugins/rolelevel/store.go`:

```go
package rolelevel

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// queryer is satisfied by both *sql.DB and *sql.Tx, so a write that must be atomic
// with the audit row can run inside a transaction while the read paths stay simple.
type queryer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// store is the plugin's own PostgreSQL access. Every method is scoped to the plugin
// schema; native ids are opaque text and there is no foreign key into mk-go's tables.
type store struct{ db *sql.DB }

// experienceRow is one row of role_level_experience.
type experienceRow struct {
	AssignmentID string
	RoleID       string
	UserID       string
	Experience   int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
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
	Operand        float64
	// DesiredExp is the integer XP the operation settled on, filled on completion.
	DesiredExp     *int64
	AssignmentID   string
	Status         string
	LastError      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// auditEntry is one row of role_level_audit. Before / After are stored as jsonb so
// a future field can be added without a migration.
type auditEntry struct {
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

// levelPayload is the jsonb shape of the two config columns.
//
// **curve と range は別 column にしない。** 設定は1単位として read-modify-write される
// ので、分けて持つと片方だけ古い状態で書ける。
type levelPayload struct {
	Curve  []Curve       `json:"experienceCurve"`
	Ranges []PolicyRange `json:"policyRanges"`
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
func (s *service) loadConfigOrStorageError(ctx context.Context, s *store, roleID string) (Config, bool, error) {
	cfg, found, err := s.LoadConfig(ctx, roleID)
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
// **2本の文に分ける。** 1本の `INSERT .. ON CONFLICT` に「新規なら revision 0、
// 以外は revision 一致で更新」を押し込むと、`RETURNING` が空のときに「衝突」と
// 「存在しない」を区別できない。両方が 409 に見えても運営者が原因を誤る。
func (s *store) UpsertConfig(ctx context.Context, cfg Config, expectRevision int64) (Config, error) {
	payload, err := json.Marshal(levelPayload{Curve: cfg.ExperienceCurve, Ranges: cfg.PolicyRanges})
	if err != nil {
		return Config{}, fmt.Errorf("rolelevel: level 設定を JSON 化できません: %w", err)
	}
	now := time.Now().UTC()

	if expectRevision == 0 {
		_, err := s.db.ExecContext(ctx, `
			INSERT INTO role_level_config
				(role_id, base_level, experience_curve, policy_ranges, revision, created_at, updated_at, updated_by)
			VALUES ($1, $2, $3, $4, 1, $5, $5, $6)
		`, cfg.RoleID, cfg.BaseLevel, payload, payload, now, cfg.UpdatedBy)
		if err != nil {
			// 主キー衝突 = 既に level 設定がある。409 にして既存 revision を伝える。
			if isUniqueViolation(err) {
				existing, found, lerr := s.LoadConfig(ctx, cfg.RoleID)
				if lerr == nil && found {
					return Config{}, fmt.Errorf("%w (現在の revision は %d です)",
						codedErrorf(409, CodeConfigConflict, "この role には既に level 設定があります"), existing.Revision)
				}
			}
			return Config{}, err
		}
	} else {
		res, err := s.db.ExecContext(ctx, `
			UPDATE role_level_config SET
				base_level       = $2,
				experience_curve = $3,
				policy_ranges    = $4,
				revision         = revision + 1,
				updated_at       = $5,
				updated_by       = $6
			WHERE role_id = $1 AND revision = $7
		`, cfg.RoleID, cfg.BaseLevel, payload, payload, now, cfg.UpdatedBy, expectRevision)
		if err != nil {
			return Config{}, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return Config{}, codedErrorf(409, CodeConfigConflict,
				"level 設定は他の操作で更新されています。読み直して保存してください")
		}
	}
	return s.loadConfigOrError(ctx, cfg.RoleID)
}
```

```go
func (s *store) loadConfigOrError(ctx context.Context, roleID string) (Config, error) {
	cfg, found, err := s.LoadConfig(ctx, roleID)
	if err != nil {
		return Config{}, err
	}
	if !found {
		return Config{}, fmt.Errorf("rolelevel: 保存直後の読み込みで %q が見つかりません", roleID)
	}
	return cfg, nil
}

// isUniqueViolation reports whether err is a PostgreSQL unique constraint failure.
func isUniqueViolation(err error) bool {
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		return pgErr.SQLState() == "23505"
	}
	return false
}
```

**store.go の残りの実装** (同じファイルに追加):

```go
// LoadConfig returns one role's configuration. found is false when there is none.
//
// **experience_curve と policy_ranges は別カラムなので別々に Scan する。** 一つの
// jsonb に縦に積むと、どちらかが壊れたときにどちらの壊れか分からない。
func (s *store) LoadConfig(ctx context.Context, roleID string) (Config, bool, error) {
	var (
		cfg    Config
		curve  []byte
		ranges []byte
	)
	err := s.db.QueryRowContext(ctx, `
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
// **XP の行は消さない。** level 設定を消したあとで XP まで消すと、監査 desirable な
// 記録が operator の1操作で消える。行は orphan として保持期間まで残り、Task 11 の
// prune job が削除する。
func (s *store) DeleteConfig(ctx context.Context, roleID string, expectRevision int64) (bool, error) {
	res, err := s.db.ExecContext(ctx,
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
// 2本以上あるのは「unassign 後に再 assign した」状態なので、live な assignment を
// native へ聞き直して newest 以外を無効にする必要がある (XP が復活してはいけない)。
func (s *store) ExperienceRowsForRoleUser(ctx context.Context, roleID, userID string) ([]experienceRow, error) {
	return s.queryExperience(ctx, `
		SELECT assignment_id, role_id, user_id, experience, created_at, updated_at
		FROM role_level_experience
		WHERE role_id = $1 AND user_id = $2
		ORDER BY created_at DESC, assignment_id DESC
	`, roleID, userID)
}

// ExperienceRowsForRole returns one page of a role's XP rows, ordered by experience
// descending.
func (s *store) ExperienceRowsForRole(ctx context.Context, roleID string, limit, offset int) ([]experienceRow, error) {
	return s.queryExperience(ctx, `
		SELECT assignment_id, role_id, user_id, experience, created_at, updated_at
		FROM role_level_experience
		WHERE role_id = $1
		ORDER BY experience DESC, assignment_id ASC
		LIMIT $2 OFFSET $3
	`, roleID, limit, offset)
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
			&r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetExperience writes one assignment's experience. q may be a transaction, so the
// XP write, the audit row and the operation status can commit together.
func (s *store) SetExperience(ctx context.Context, q queryer, row experienceRow) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO role_level_experience (assignment_id, role_id, user_id, experience, created_at, updated_at)
		VALUES ($1, $2, $3, $4, now(), now())
		ON CONFLICT (assignment_id) DO UPDATE SET
			experience = EXCLUDED.experience,
			updated_at = now()
		WHERE role_level_experience.role_id = EXCLUDED.role_id
		  AND role_level_experience.user_id = EXCLUDED.user_id
	`, row.AssignmentID, row.RoleID, row.UserID, row.Experience)
	return err
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
		nullableString(entry.AssignmentID), before, after, entry.Note)
	return err
}

// RecentAudit returns the newest audit rows for a role and/or user.
//
// roleID か userID のどちらかが空なら、その条件は無視される (片側だけの絞り込みに
// なる)。両方空ならその role の全履歴。
func (s *store) RecentAudit(ctx context.Context, roleID, userID string, limit int) ([]auditEntry, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT actor_id, operation, role_id, user_id, assignment_id, before_state, after_state, note, created_at
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
			createdAt          time.Time
		)
		if err := rows.Scan(&entry.ActorID, &entry.Operation, &role, &user, &assign,
			&before, &after, &entry.Note, &createdAt); err != nil {
			return nil, err
		}
		entry.CreatedAt = createdAt
		entry.RoleID, entry.UserID, entry.AssignmentID = deref(role), deref(user), deref(assign)
		if len(before) > 0 {
			_ = json.Unmarshal(before, &entry.Before)
		}
		if len(after) > 0 {
			_ = json.Unmarshal(after, &entry.After)
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
			(idempotency_key, actor_id, user_id, role_id, mode, operand, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (idempotency_key) DO NOTHING
	`, op.IdempotencyKey, op.ActorID, op.UserID, op.RoleID, op.Mode, op.Operand, op.Status)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// LoadOperation returns one operation by idempotency key.
func (s *store) LoadOperation(ctx context.Context, key string) (operation, bool, error) {
	var op operation
	err := s.db.QueryRowContext(ctx, `
		SELECT idempotency_key, actor_id, user_id, role_id, mode, operand, desired_exp,
		       assignment_id, status, last_error, created_at, updated_at
		FROM role_level_operation WHERE idempotency_key = $1
	`, key).Scan(&op.IdempotencyKey, &op.ActorID, &op.UserID, &op.RoleID, &op.Mode, &op.Operand,
		&op.DesiredExp, &op.AssignmentID, &op.Status, &op.LastError, &op.CreatedAt, &op.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return operation{}, false, nil
		}
		return operation{}, false, err
	}
	return op, true, nil
}

// SetOperationStatus records progress. q may be a transaction, so the completion of
// an XP write can commit together with the XP write itself.
func (s *store) SetOperationStatus(ctx context.Context, q queryer, key string, status Status,
	assignmentID string, desiredExp *int64, lastErr string,
) error {
	_, err := q.ExecContext(ctx, `
		UPDATE role_level_operation SET
			status        = $2,
			assignment_id = COALESCE(NULLIF($3, ''), assignment_id),
			desired_exp   = COALESCE($4, desired_exp),
			last_error    = $5,
			updated_at    = now()
		WHERE idempotency_key = $1
	`, key, string(status), assignmentID, desiredExp, lastErr)
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
		       assignment_id, status, last_error, created_at, updated_at
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
		var op operation
		if err := rows.Scan(&op.IdempotencyKey, &op.ActorID, &op.UserID, &op.RoleID, &op.Mode,
			&op.Operand, &op.DesiredExp, &op.AssignmentID, &op.Status, &op.LastError,
			&op.CreatedAt, &op.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	return out, rows.Err()
}

// DeleteOrphanExperience removes XP rows whose native assignment is gone and whose
// last update is older than before.
//
// **保持期間以内の行は消さない。** 監査のために残すのが目的で、native API の障害で
// 消えると operator の記録が失われる。
func (s *store) DeleteOrphanExperience(ctx context.Context, roleID string, orphanIDs []string, before time.Time) (int64, error) {
	if len(orphanIDs) == 0 {
		return 0, nil
	}
	// 100件ずつ刻む。bind parameter は PostgreSQL で65535個までだが、IN 節が
	// 長くなりすぎると plan が読みにくくなるだけなので。
	const batch = 100
	var total int64
	for start := 0; start < len(orphanIDs); start += batch {
		end := start + batch
		if end > len(orphanIDs) {
			end = len(orphanIDs)
		}
		res, err := s.db.ExecContext(ctx, `
			DELETE FROM role_level_experience
			WHERE role_id = $1 AND updated_at < $2
			  AND assignment_id IN (`+placeholders(end-start)+`)
		`, roleID, before, toAny(orphanIDs[start:end])...)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}

// placeholders builds "$1, $2, ..." for an IN clause. n must be positive.
func placeholders(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		if i > 1 {
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
```

```go
import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)
```

`log/slog` は `store.go` では使わない (`service` の `log` フィールドだけが使う)。`storageError` は `*service` のメソッドなので `s.log` を使える。

**`SetExperience` に付けた `ON CONFLICT ... WHERE role_id = EXCLUDED.role_id AND user_id = EXCLUDED.user_id` は残す。** 既存の行が別の role / user を指している (plugin table が壊れている / 別 plugin の数据和混ざった) 状態を黙って上書きしないため。**RowsAffected が 0 になることがある**ので、Task 8 の `xp.go` は `SetExperience` の戻りとして「書けた行数」を見て、0 なら `ROLE_LEVEL_STORAGE_FAILED` として **operation を failed に落下させる** (黙って成功扱いにしない)。Task 5 では `SetExperience` は `error` だけを返す実装でよい。

- [ ] **Step 4: GREEN を確認する**

```powershell
$env:GOWORK = "off"
go -C plugins/rolelevel test ./... -count=1 -run "TestStore"
go -C plugins/rolelevel vet ./...
gofmt -l plugins/rolelevel
```

Expected: PASS。`gofmt -l` が出力しないこと。

**`TestStoreWrapsErrors` が `svc.loadConfigOrStorageError` を要求している点に注意。** `service.log` は `newService` が設定するが、この test は `&service{log: discardLogger()}` を直接作る。それでよい (loadConfigOrStorageError は ctx と store しか使わない)。

- [ ] **Step 5: commit する**

```powershell
git add plugins/rolelevel/store.go plugins/rolelevel/store_test.go
git commit -m "Phase roleLevel: add plugin-owned storage"
```

---

### Task 6: native role adapter

**Files:**
- Create: `plugins/rolelevel/nativerole.go`
- Create: `plugins/rolelevel/nativerole_test.go`

**Interfaces:**
- Consumes: Task 1 の `codedErrorf` / `CodeNativeRoleNotFound` / `CodeNativeAPIFailed` / `CodeAssignmentScanExhausted` / `roleInfo` / `assignment` / `plugin.Caller`
- Produces:
  - `const nativePageSize = 100`
  - `type nativeRole struct { caller plugin.Caller; pages int }`
  - `func (n *nativeRole) Show(ctx context.Context, roleID string) (roleInfo, error)`
  - `func (n *nativeRole) RequireManual(ctx context.Context, roleID string) (roleInfo, error)`
  - `func (n *nativeRole) Assigned(ctx context.Context, roleID, userID string) (bool, error)`
  - `func (n *nativeRole) ListAssignments(ctx context.Context, roleID string) ([]assignment, bool, error)`
  - `func (n *nativeRole) FindAssignment(ctx context.Context, roleID, userID string) (string, bool, error)`
  - `func (n *nativeRole) Assign(ctx context.Context, userID, roleID string) error`
  - `func (n *nativeRole) Usernames(ctx context.Context, userIDs []string) (map[string]string, error)`
  - **native `admin/roles/assign` だけを使う。** `admin/roles/unassign` は呼ばない (XP 変更は native を unassign しない)

- [ ] **Step 1: RED — native adapter の契約を固定する**

`plugins/rolelevel/nativerole_test.go`:

```go
package rolelevel

import (
	"context"
	"net/http"
	"testing"
)

func newTestNative(t *testing.T, api *stubAPI, pages int) *nativeRole {
	t.Helper()
	return &nativeRole{caller: api.AsUser("admin1"), pages: pages}
}

func TestNativeShow(t *testing.T) {
	api := &stubAPI{roles: map[string]roleInfo{
		"r1": {ID: "r1", Target: "manual", CanEditMembersByModerator: true},
	}}
	n := newTestNative(t, api, 2)
	got, err := n.Show(context.Background(), "r1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Target != "manual" || !got.CanEditMembersByModerator {
		t.Fatalf("= %+v", got)
	}
	// 無い role は stable code 付きで 404相当。
	_, err = n.Show(context.Background(), "nope")
	if err == nil {
		t.Fatal("無い role を受け入れています")
	}
	_, code := extractCode(err)
	if code != CodeNativeRoleNotFound {
		t.Fatalf("code = %q, want %s (%v)", code, CodeNativeRoleNotFound, err)
	}
}

// **conditional role は扱わない。** level は manual assignment に紐づくので、
// conditional に対して XP を持たせられると policy の置換先が壊れる。
func TestNativeRequireManual(t *testing.T) {
	api := &stubAPI{roles: map[string]roleInfo{
		"cond": {ID: "cond", Target: "conditional"},
	}}
	n := newTestNative(t, api, 2)
	_, err := n.RequireManual(context.Background(), "cond")
	if err == nil {
		t.Fatal("conditional role を受け入れています")
	}
	_, code := extractCode(err)
	if code != CodeRoleNotManual {
		t.Fatalf("code = %q, want %s (%v)", code, CodeRoleNotManual, err)
	}
}

func TestNativeFindAssignment(t *testing.T) {
	api := &stubAPI{assignments: map[string][]assignment{
		"r1": {{ID: "asg1", User: struct {
			ID string `json:"id"`
		}{ID: "u1"}}},
	}}
	n := newTestNative(t, api, 2)
	got, found, err := n.FindAssignment(context.Background(), "r1", "u1")
	if err != nil || !found {
		t.Fatalf("found = %t %v", found, err)
	}
	if got != "asg1" {
		t.Fatalf("assignmentID = %q, want asg1", got)
	}
	if _, found, err := n.FindAssignment(context.Background(), "r1", "u2"); err != nil || found {
		t.Fatalf("居ない利用者を found にしています: %t %v", found, err)
	}
}

// **走査上限を超えたら「不明」を返す。** 黙って「居ない」と言うと、未付与の
// user に XP を割り当ててしまう。
func TestNativeFindAssignmentScanExhausted(t *testing.T) {
	var many []assignment
	for i := 0; i < 250; i++ {
		a := assignment{ID: string(rune('a'+i%26)) + string(rune('0'+i/26))}
		a.User.ID = "u" + string(rune('0'+i%10))
		many = append(many, a)
	}
	api := &stubAPI{assignments: map[string][]assignment{"r1": many}}
	n := newTestNative(t, api, 1) // 100件しか見ない

	_, found, err := n.FindAssignment(context.Background(), "r1", "u9")
	if err == nil {
		t.Fatalf("上限を超えたのにエラーになりません (found = %t)", found)
	}
	_, code := extractCode(err)
	if code != CodeAssignmentScanExhausted {
		t.Fatalf("code = %q, want %s (%v)", code, CodeAssignmentScanExhausted, err)
	}
	if found {
		t.Fatal("上限超過で found になってはいけません")
	}
}

// **admin/roles/assign の 409 は目的の状態。** 冪等に回すので、既に付いていること
// を異常系にすると reconcile のたびに失敗が積み上がる (plugins/trustlevel と同じ)。
func TestNativeAssignTreatsConflictAsSuccess(t *testing.T) {
	api := &stubAPI{assignments: map[string][]assignment{}}
	n := newTestNative(t, api, 2)
	if err := n.Assign(context.Background(), "u1", "r1"); err != nil {
		t.Fatal(err)
	}
	api.assignStatus = http.StatusConflict
	if err := n.Assign(context.Background(), "u1", "r1"); err != nil {
		t.Fatalf("409 を失敗として扱いました: %v", err)
	}
	api.assignStatus = http.StatusInternalServerError
	err := n.Assign(context.Background(), "u1", "r1")
	if err == nil {
		t.Fatal("500 を成功として扱いました")
	}
	_, code := extractCode(err)
	if code != CodeNativeAPIFailed {
		t.Fatalf("code = %q, want %s (%v)", code, CodeNativeAPIFailed, err)
	}
}

func TestNativeAssigned(t *testing.T) {
	var a assignment
	a.ID = "asg1"
	a.User.ID = "u1"
	api := &stubAPI{assignments: map[string][]assignment{"r1": {a}}}
	n := newTestNative(t, api, 2)
	got, err := n.Assigned(context.Background(), "r1", "u1")
	if err != nil || !got {
		t.Fatalf("= %t %v", got, err)
	}
	got, err = n.Assigned(context.Background(), "r1", "u2")
	if err != nil || got {
		t.Fatalf("= %t %v", got, err)
	}
}

// **usernames は1回でまとめて引く。** 1件ずつ users/show を呼ぶと member 一覧の
// 人数だけ round trip が増える (plugins/status の /recent と同じ理由)。
func TestNativeUsernames(t *testing.T) {
	api := &stubAPI{}
	n := newTestNative(t, api, 2)
	got, err := n.Usernames(context.Background(), []string{"u1", "u2"})
	if err != nil {
		t.Fatal(err)
	}
	if got["u1"] != "user-u1" || got["u2"] != "user-u2" {
		t.Fatalf("= %+v", got)
	}
	if m, err := n.Usernames(context.Background(), nil); err != nil || len(m) != 0 {
		t.Fatalf("空リスト = %+v %v", m, err)
	}
}
```

**`assignment` の合成リテラルが書きにくいので、`main_test.go` に helper を追加する** (`nativerole_test.go` の先頭に置く):

```go
// mkAssignment builds one native assignment row for the stub API.
func mkAssignment(id, userID string) assignment {
	var a assignment
	a.ID = id
	a.User.ID = userID
	return a
}
```

`TestNativeShow` / `TestNativeFindAssignment` / `TestNativeAssignTreatsConflictAsSuccess` / `TestNativeAssigned` 内の構造体リテラルは `mkAssignment("asg1", "u1")` に置き換える。`TestNativeFindAssignmentScanExhausted` の `many` は次のように書く:

```go
	var many []assignment
	for i := 0; i < 250; i++ {
		many = append(many, mkAssignment(
			fmt.Sprintf("asg-%03d", i), fmt.Sprintf("u%03d", i)))
	}
```

その場合 `nativerole_test.go` の import に `fmt` を追加すること。対象 user を走査上限の**外**に置くため `u9` ではなく `u249` を使う:

```go
	_, found, err := n.FindAssignment(context.Background(), "r1", "u249")
```

- [ ] **Step 2: RED を確認する**

```powershell
$env:GOWORK = "off"
go -C plugins/rolelevel test ./... -count=1 -run "TestNative"
```

Expected: FAIL — `undefined: nativeRole`, `undefined: nativePageSize`。

- [ ] **Step 3: `nativerole.go` を実装する**

`plugins/rolelevel/nativerole.go`:

```go
package rolelevel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/shiroha-a/mk/plugin"
)

// nativePageSize is the per-page limit of admin/roles/users.
//
// **上限は 100** (pagination.ResolveLimit)。これより大きい値を渡すと mk-go 側で丸められ、
// offset 送りと件数がずれて、使わない member を見失う。
const nativePageSize = 100

// nativeRole reads and writes mk-go's own role state through its REST API.
//
// **core table を直接読まない。** プラグインは自分の schema しか触れないので、role /
// assignment の正本は native API だけ。可視性判定と権限が native と同じ経路を通るので、
// そちらで実装し直す必要が無く、mk-go 側が変えても自動的に追従する。
type nativeRole struct {
	caller plugin.Caller
	// pages bounds how many pages of admin/roles/users a scan walks.
	pages int
}

// apiFailure converts a plugin.APIError into a coded status error.
//
// **素の error を返さない。** host は 500 に丸めて「内部エラー」しか返さないので、
// frontend が「native 側の問題」と「plugin の問題」を区別できない。
func (n *nativeRole) apiFailure(ctx context.Context, what string, err error) error {
	var apiErr *plugin.APIError
	if errors.As(err, &apiErr) {
		if apiErr.Status == http.StatusBadRequest &&
			strings.Contains(string(apiErr.Body), "NO_SUCH_ROLE") {
			return codedErrorf(http.StatusNotFound, CodeNativeRoleNotFound,
				"%s: その role は存在しません", what)
		}
		return codedErrorf(http.StatusBadGateway, CodeNativeAPIFailed,
			"%s に失敗しました (native status %d)", what, apiErr.Status)
	}
	return codedErrorf(http.StatusBadGateway, CodeNativeAPIFailed, "%s に失敗しました", what)
}

// Show reads one role.
func (n *nativeRole) Show(ctx context.Context, roleID string) (roleInfo, error) {
	raw, err := n.caller.Call(ctx, "admin/roles/show", map[string]any{"roleId": roleID})
	if err != nil {
		return roleInfo{}, n.apiFailure(ctx, "admin/roles/show", err)
	}
	var info roleInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return roleInfo{}, codedErrorf(http.StatusBadGateway, CodeNativeAPIFailed,
			"admin/roles/show の応答を読めません")
	}
	if info.ID == "" {
		// **id の無い応答は「読めない」で扱う。** 空の role を作ると、以降の
		// 権限判定が全部 false になって「誰も触れない」状態になる。
		return roleInfo{}, codedErrorf(http.StatusBadGateway, CodeNativeAPIFailed,
			"admin/roles/show が id を返しませんでした")
	}
	return info, nil
}

// RequireManual reads one role and rejects anything but a manual role.
//
// **conditional role は assignment を持たない。** level は assignment に紐づくので、
// conditional に XP を持たせられると「どの assignment の XP か」が決まらない。
func (n *nativeRole) RequireManual(ctx context.Context, roleID string) (roleInfo, error) {
	info, err := n.Show(ctx, roleID)
	if err != nil {
		return roleInfo{}, err
	}
	if info.Target != "manual" {
		return roleInfo{}, codedErrorf(http.StatusBadRequest, CodeRoleNotManual,
			"level は manual role だけに設定できます (この role の target は %s です)", info.Target)
	}
	return info, nil
}

// Assigned reports whether the user currently holds the role.
//
// **admin/roles/assignment-show を 1 回だけ呼ぶ。** member 一覧を走査するより遥かに
// 安いので、XP 0 の user の membership を確かめる経路ではこちらを使う。
func (n *nativeRole) Assigned(ctx context.Context, roleID, userID string) (bool, error) {
	raw, err := n.caller.Call(ctx, "admin/roles/assignment-show",
		map[string]any{"roleId": roleID, "userId": userID})
	if err != nil {
		return false, n.apiFailure(ctx, "admin/roles/assignment-show", err)
	}
	var res struct {
		Assigned bool `json:"assigned"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return false, codedErrorf(http.StatusBadGateway, CodeNativeAPIFailed,
			"admin/roles/assignment-show の応答を読めません")
	}
	return res.Assigned, nil
}

// ListAssignments returns one page-bounded scan of a role's assignments.
//
// truncated is true when the scan hit the page cap, so the caller can tell
// "this role has no more members" from "we stopped looking".
func (n *nativeRole) ListAssignments(ctx context.Context, roleID string) ([]assignment, bool, error) {
	out := []assignment{}
	var sinceID string
	for page := 0; page < n.pages; page++ {
		params := map[string]any{"roleId": roleID, "limit": nativePageSize}
		if sinceID != "" {
			params["sinceId"] = sinceID
		}
		raw, err := n.caller.Call(ctx, "admin/roles/users", params)
		if err != nil {
			return nil, false, n.apiFailure(ctx, "admin/roles/users", err)
		}
		var batch []assignment
		if err := json.Unmarshal(raw, &batch); err != nil {
			return nil, false, codedErrorf(http.StatusBadGateway, CodeNativeAPIFailed,
				"admin/roles/users の応答を読めません")
		}
		if len(batch) == 0 {
			return out, false, nil
		}
		out = append(out, batch...)
		// aidx の id は降順で並ぶので、最後 (最も古い) 1 件を次の sinceId にする。
		sinceID = batch[len(batch)-1].ID
		if len(batch) < nativePageSize {
			return out, false, nil
		}
	}
	return out, true, nil
}

// FindAssignment resolves the native assignment id of one (role, user) pair.
//
// found is false only when the scan finished without an upper limit. A truncated
// scan returns a RoleLevelAssignmentScanExhausted error instead, because
// mistaking "we stopped looking" for "not assigned" would create an XP row for a
// member that already exists.
func (n *nativeRole) FindAssignment(ctx context.Context, roleID, userID string) (string, bool, error) {
	all, truncated, err := n.ListAssignments(ctx, roleID)
	if err != nil {
		return "", false, err
	}
	if truncated {
		return "", false, codedErrorf(http.StatusConflict, CodeAssignmentScanExhausted,
			"この role の member が多すぎて (上限 %d ページ) assignment を確認できません。assignmentScanPages を上げるか、XP を個別に変更してください",
			n.pages)
	}
	for _, a := range all {
		if a.UserID() == userID {
			return a.ID, true, nil
		}
	}
	return "", false, nil
}

// Assign grants the role. A conflict (already assigned) counts as success so the
// caller can retry without special-casing it.
func (n *nativeRole) Assign(ctx context.Context, userID, roleID string) error {
	_, err := n.caller.Call(ctx, "admin/roles/assign",
		map[string]any{"userId": userID, "roleId": roleID})
	if err == nil {
		return nil
	}
	var apiErr *plugin.APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusConflict {
		return nil
	}
	return n.apiFailure(ctx, "admin/roles/assign", err)
}

// Usernames resolves display names in one call.
//
// A missing user is simply absent from the map: suspended and deleted users are
// exactly the ones mk-go stops returning, and a list that still shows them is worse
// than a short list.
func (n *nativeRole) Usernames(ctx context.Context, userIDs []string) (map[string]string, error) {
	out := map[string]string{}
	if len(userIDs) == 0 {
		return out, nil
	}
	raw, err := n.caller.Call(ctx, "users/show", map[string]any{"userIds": userIDs})
	if err != nil {
		return nil, n.apiFailure(ctx, "users/show", err)
	}
	var users []struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	}
	if err := json.Unmarshal(raw, &users); err != nil {
		return nil, codedErrorf(http.StatusBadGateway, CodeNativeAPIFailed,
			"users/show の応答を読めません")
	}
	for _, u := range users {
		out[u.ID] = u.Username
	}
	return out, nil
}
```

`nativerole.go` の import は次の6つ (`fmt` は使わないので入れない):

```go
import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/shiroha-a/mk/plugin"
)
```

`strings` は `apiFailure` の `NO_SUCH_ROLE` 判定に使う。**`Unassign` は無い。** native の
`admin/roles/unassign` は plugin route ではなく、XP 変更は native を unassign しないので
呼ばない (Global Constraints の「設計上の判断」)。

- [ ] **Step 4: GREEN を確認する**

```powershell
$env:GOWORK = "off"
go -C plugins/rolelevel test ./... -count=1 -run "TestNative"
go -C plugins/rolelevel vet ./...
gofmt -l plugins/rolelevel
```

Expected: PASS、`gofmt -l` が出力しないこと。

- [ ] **Step 5: commit する**

```powershell
git add plugins/rolelevel/nativerole.go plugins/rolelevel/nativerole_test.go
git commit -m "Phase roleLevel: add the native role adapter"
```

---

### Task 7: authorization

**Files:**
- Create: `plugins/rolelevel/authz.go`
- Create: `plugins/rolelevel/authz_test.go`

**Interfaces:**
- Consumes: Task 1 の `codedErrorf` / `CodeUnauthenticated` / `CodeForbidden` / `CodeRoleNotAssignable` / `plugin.Request`, Task 6 の `nativeRole.Show`
- Produces:
  - `func requireAdmin(req plugin.Request) error`
  - `func requireModerator(req plugin.Request) error`
  - `func (s *service) AuthorizeXPChange(req plugin.Request, roleID string) error`
  - `func (s *service) RequireConfigAdmin(ctx context.Context, roleID string) error`

- [ ] **Step 1: RED — authorization の契約を固定する**

`plugins/rolelevel/authz_test.go`:

```go
package rolelevel

import (
	"context"
	"testing"

	"github.com/shiroha-a/mk/plugin"
	"github.com/shiroha-a/mk/plugin/plugintest"
)

// **管理用のルートは自分で守る。** Router は認証の有無しか見ないので、管理画面を
// 出しただけでは API は誰でも叩ける (plugin/http.go の doc)。
func TestRequireAdmin(t *testing.T) {
	for _, tt := range []struct {
		name string
		req  plugintest.Request
		code string
	}{
		{"未認証", plugintest.Request{}, CodeUnauthenticated},
		{"authenticated only", plugintest.Request{UserID: "u1"}, CodeForbidden},
		{"moderator", plugintest.Request{UserID: "m1", Moderator: true}, CodeForbidden},
		{"administrator", plugintest.Request{UserID: "a1", Administrator: true}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := requireAdmin(httptestRequest(t, tt.req))
			if tt.code == "" {
				if err != nil {
					t.Fatalf("拒否しました: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("受け入れています")
			}
			if _, code := extractCode(err); code != tt.code {
				t.Fatalf("code = %q, want %s (%v)", code, tt.code, err)
			}
		})
	}
}

func TestRequireModerator(t *testing.T) {
	if err := requireModerator(httptestRequest(t, plugintest.Request{UserID: "u1"})); err == nil {
		t.Fatal("未認証を通しています")
	}
	if err := requireModerator(httptestRequest(t, plugintest.Request{UserID: "m1", Moderator: true})); err != nil {
		t.Fatalf("moderator を拒否しました: %v", err)
	}
}

// **moderator は canEditMembersByModerator が true の role だけ。** false なら
// administrator 限定。**administrator な role は moderator にも閉ざす** —
// native の assign / unassign と同じ方針 (#3037 と同じ穴)。
func TestAuthorizeXPChange(t *testing.T) {
	api := &stubAPI{roles: map[string]roleInfo{
		"open":  {ID: "open", Target: "manual", CanEditMembersByModerator: true},
		"shut":  {ID: "shut", Target: "manual"},
		"admin": {ID: "admin", Target: "manual", IsAdministrator: true, CanEditMembersByModerator: true},
	}}
	svc := newAuthzService(t, api)

	for _, tt := range []struct {
		name string
		req  plugintest.Request
		role string
		code string
	}{
		{"未認証", plugintest.Request{}, "open", CodeUnauthenticated},
		{"素の user", plugintest.Request{UserID: "u1"}, "open", CodeForbidden},
		{"moderator + open", plugintest.Request{UserID: "m1", Moderator: true}, "open", ""},
		{"moderator + shut", plugintest.Request{UserID: "m1", Moderator: true}, "shut", CodeRoleNotAssignable},
		{"moderator + admin role", plugintest.Request{UserID: "m1", Moderator: true}, "admin", CodeRoleNotAssignable},
		{"administrator + shut", plugintest.Request{UserID: "a1", Administrator: true}, "shut", ""},
		{"administrator + admin role", plugintest.Request{UserID: "a1", Administrator: true}, "admin", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := svc.AuthorizeXPChange(httptestRequest(t, tt.req), tt.role)
			if tt.code == "" {
				if err != nil {
					t.Fatalf("拒否しました: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("受け入れています")
			}
			if _, code := extractCode(err); code != tt.code {
				t.Fatalf("code = %q, want %s (%v)", code, tt.code, err)
			}
		})
	}
}

// **actorId 未設定なら XP 変更は 403。** 「動かない」を「設定不足」で返す。
func TestAuthorizeXPChangeNeedsActor(t *testing.T) {
	svc := newAuthzService(t, &stubAPI{})
	svc.cfg.ActorID = ""
	err := svc.AuthorizeXPChange(
		httptestRequest(t, plugintest.Request{UserID: "a1", Administrator: true}), "r1")
	if err == nil {
		t.Fatal("actorId 未設定で通しています")
	}
	if _, code := extractCode(err); code != CodeActorNotConfigured {
		t.Fatalf("code = %q, want %s (%v)", code, CodeActorNotConfigured, err)
	}
}

// **level 設定の変更は administrator だけ。** moderator が curve を変えて権限を
// 緩められると、canEditMembersByModerator の意味が壊れる。
func TestRequireConfigAdminRejectsModerator(t *testing.T) {
	api := &stubAPI{roles: map[string]roleInfo{
		"open": {ID: "open", Target: "manual", CanEditMembersByModerator: true},
	}}
	svc := newAuthzService(t, api)
	if err := svc.RequireConfigAdmin(context.Background(), "open"); err == nil {
		t.Fatal("service 側の moderator を通しています")
	}
}

// helpers

// newAuthzService builds a service bound to a stub API and a temp schema.
func newAuthzService(t *testing.T, api *stubAPI) *service {
	t.Helper()
	h := plugintest.New(t).WithName("role-level").WithDB(testDB(t)).
		WithConfig(map[string]any{"actorId": "admin1"}).WithAPI(api)
	svc, err := newService(h.Context())
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

// httptestRequest builds the plugin.Request a route handler would receive.
//
// **authz は plugin.Request の 3 つ (UserID / IsModerator / IsAdministrator) だけを見る。**
// plugintest の Harness は handler を公開しないので、同じ入力を作る最小実装をここに
// 持つ。route のテスト (Task 10) では harness 経由で確認する。
func httptestRequest(t *testing.T, r plugintest.Request) plugin.Request {
	t.Helper()
	return &fakeReq{
		userID:    r.UserID,
		moderator: r.Moderator || r.Administrator,
		admin:     r.Administrator,
	}
}

// fakeReq is a minimal plugin.Request for the authorization tests.
type fakeReq struct {
	ctx       context.Context
	userID    string
	moderator bool
	admin     bool
}

func (r *fakeReq) Context() context.Context {
	if r.ctx == nil {
		return context.Background()
	}
	return r.ctx
}

func (r *fakeReq) Bind(any) error { return nil }

func (r *fakeReq) Param(string) string { return "" }

func (r *fakeReq) Query(string) string { return "" }

func (r *fakeReq) UserID() string { return r.userID }

func (r *fakeReq) IsModerator() bool { return r.moderator }

func (r *fakeReq) IsAdministrator() bool { return r.admin }
```

- [ ] **Step 2: RED を確認する**

```powershell
$env:GOWORK = "off"
go -C plugins/rolelevel test ./... -count=1 -run "TestRequireAdmin|TestRequireModerator|TestAuthorizeXPChange|TestRequireConfigAdmin"
```

Expected: FAIL — `undefined: requireAdmin`, `undefined: requireModerator`, `undefined: AuthorizeXPChange`, `undefined: RequireConfigAdmin`。

- [ ] **Step 3: `authz.go` を実装する**

`plugins/rolelevel/authz.go`:

```go
package rolelevel

import (
	"context"
	"net/http"

	"github.com/shiroha-a/mk/plugin"
)

// requireAdmin gates every level-configuration route.
//
// A role's curve and per-level policies decide who may post and who may not, so only
// an administrator may change them. Hiding the editor in the frontend is not a
// boundary.
func requireAdmin(req plugin.Request) error {
	if req.UserID() == "" {
		return codedErrorf(http.StatusUnauthorized, CodeUnauthenticated, "ログインが必要です")
	}
	if !req.IsAdministrator() {
		return codedErrorf(http.StatusForbidden, CodeForbidden, "この操作は管理者だけが行えます")
	}
	return nil
}

// requireModerator gates the administrator read routes.
func requireModerator(req plugin.Request) error {
	if req.UserID() == "" {
		return codedErrorf(http.StatusUnauthorized, CodeUnauthenticated, "ログインが必要です")
	}
	if !req.IsModerator() {
		return codedErrorf(http.StatusForbidden, CodeForbidden, "この操作はモデレーター以上が必要です")
	}
	return nil
}

// AuthorizeXPChange applies the experience-mutation rule: an administrator may change
// any role's experience, and a moderator only the roles whose native
// canEditMembersByModerator is true.
//
// The native role is read through admin/roles/show **as the configured actor**, so a
// moderator cannot widen the rule by editing plugin storage.
//
// **管理者ロールは moderator にも閉ざす。** native の assign / unassign と同じ方針で
// (handler.go の requireCanEditRoleMembers)、`canEditMembersByModerator` の
// チェックボックス1つで管理者へ昇格できる穴を作らない (#3037)。
func (s *service) AuthorizeXPChange(req plugin.Request, roleID string) error {
	if req.UserID() == "" {
		return codedErrorf(http.StatusUnauthorized, CodeUnauthenticated, "ログインが必要です")
	}
	if req.IsAdministrator() {
		return nil
	}
	if !req.IsModerator() {
		return codedErrorf(http.StatusForbidden, CodeForbidden,
			"この操作はモデレーター以上が必要です")
	}
	native, err := s.native()
	if err != nil {
		return err
	}
	info, err := native.Show(req.Context(), roleID)
	if err != nil {
		return err
	}
	if info.IsAdministrator {
		return codedErrorf(http.StatusForbidden, CodeRoleNotAssignable,
			"管理者ロールの experience は管理者だけ変更できます")
	}
	if !info.CanEditMembersByModerator {
		return codedErrorf(http.StatusForbidden, CodeRoleNotAssignable,
			"このロールの experience は管理者だけ変更できます (canEditMembersByModerator が false です)")
	}
	return nil
}

// RequireConfigAdmin rejects anything but a manual role for level configuration.
//
// **level は manual role にしか付けない。** conditional role には assignment が無いので、
// XP を紐づけられる対象が存在しない。target を確認せずに保存すると、あとから
// policy 置換の先が壊れた状態で保存される。
func (s *service) RequireConfigAdmin(ctx context.Context, roleID string) error {
	native, err := s.native()
	if err != nil {
		return err
	}
	_, err = native.RequireManual(ctx, roleID)
	return err
}
```

- [ ] **Step 4: GREEN を確認する**

```powershell
$env:GOWORK = "off"
go -C plugins/rolelevel test ./... -count=1 -run "TestRequireAdmin|TestRequireModerator|TestAuthorizeXPChange|TestRequireConfigAdmin"
go -C plugins/rolelevel vet ./...
gofmt -l plugins/rolelevel
```

Expected: PASS、`gofmt -l` が出力しないこと。

- [ ] **Step 5: commit する**

```powershell
git add plugins/rolelevel/authz.go plugins/rolelevel/authz_test.go
git commit -m "Phase roleLevel: gate level and experience changes"
```

---

### Task 8: XP operation state machine

**Files:**
- Create: `plugins/rolelevel/xp.go`
- Create: `plugins/rolelevel/xp_test.go`

**Interfaces:**
- Consumes: Task 1 の `codedErrorf` / `Code*` / `invalidator()` / `service`, Task 3 の `MaxExperience`, Task 5 の `store` / `experienceRow` / `operation` / `auditEntry` / `Status*`, Task 6 の `nativeRole`, Task 7 の `AuthorizeXPChange`
- Produces:
  - `type Mode string` with `ModeSet/ModeAdd/ModeMultiplier`
  - `type Status string` with `StatusPending/StatusAssigning/StatusApplying/StatusCompleted/StatusFailed`
  - `type ChangeExpRequest struct { IdempotencyKey, ActorID, UserID, RoleID, Note string; Mode Mode; Operand float64 }`
  - `type ChangeExpResult struct { AssignmentID string; Experience int64; Status Status; Resumed bool }`
  - `func applyMode(mode Mode, current int64, operand float64) (int64, error)` — float64 で計算し `floor` してから収める
  - `func clampExperience(v float64) (int64, error)` — 非有限値は error
  - `func validateMode(mode Mode, operand float64) error`
  - `func validateIdempotencyKey(key string) error`
  - `func (s *service) ChangeExp(ctx context.Context, req ChangeExpRequest) (ChangeExpResult, error)`
  - `func (s *service) resume(ctx context.Context, op operation, note string) (ChangeExpResult, error)`
  - `func (s *service) assignmentIDFor(ctx context.Context, native *nativeRole, roleID, userID string) (string, bool, error)`
  - `func (s *service) experienceFor(ctx context.Context, roleID, userID, assignmentID string) (int64, error)`
  - `func (s *service) invalidateAfterXPChange(ctx context.Context, userID, roleID string)`
  - `func (s *service) invalidateRoleConfig(ctx context.Context, roleID string)`
  - `const maxAssignLookupAttempts = 3`

- [ ] **Step 1: RED — float64 演算と validation の契約を固定する (DB なし)**

`plugins/rolelevel/xp_test.go`:

```go
package rolelevel

import (
	"math"
	"testing"
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

// **非有限値の operand は保存時に落とす。** NaN や Infinity は floor しても整数に
// ならないので、計算前に落とす。
func TestApplyModeRejectsNonFiniteResult(t *testing.T) {
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
		{"set は上限を超える", ModeSet, 1e300, CodeOperandOutOfRange},
		{"set は NaN を弾く", ModeSet, math.NaN(), CodeOperandOutOfRange},
		{"set は Infinity を弾く", ModeSet, math.Inf(-1), CodeOperandOutOfRange},
		{"add は小数を許す", ModeAdd, 0.25, ""},
		{"add は上限を超える", ModeAdd, 1e300, CodeOperandOutOfRange},
		{"multiplier は小数を許す", ModeMultiplier, 0.5, ""},
		{"multiplier は 1.5 を許す", ModeMultiplier, 1.5, ""},
		{"multiplier は 1000 を許す", ModeMultiplier, 1000, ""},
		// 負の factor は「XP を消す」効果だが、**拒否はしない** — 仕様は「有限の小数を
		// 受け付ける」で、例外を名作らない。結果の clamp が 0 に落とす。
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
		t.Fatalf("= %d %v, want %d", got, MaxExperience)
	}
	if got, err := clampExperience(42.9); err != nil || got != 42 {
		t.Fatalf("= %d %v, want 42", got, err)
	}
}
```

- [ ] **Step 3: float64 演算と validation を実装する**

`plugins/rolelevel/xp.go` の先頭に追加:

```go
package rolelevel

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/shiroha-a/mk/plugin"
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
// **非有限値は error。** NaN や ±Infinity は floor しても整数にならないので、黙って 0 に
// すると「XP を消した」ように見える。原因は operand の検証 (validateMode) だが、
// route を通り越す経路に備えてここでも落とす。
func clampExperience(v float64) (int64, error) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, codedErrorf(http.StatusBadRequest, CodeOperandOutOfRange,
			"計算結果 %v が有限値になりません", v)
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
	if math.Abs(operand) > MaxExperienceFloat {
		return codedErrorf(http.StatusBadRequest, CodeOperandOutOfRange,
			"operand は %v 以上 %v 以下にしてください (%v)", -MaxExperienceFloat, MaxExperienceFloat, operand)
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
```

- [ ] **Step 4: GREEN を確認する (純関数部分)**

```powershell
$env:GOWORK = "off"
go -C plugins/rolelevel test ./... -count=1 -run "TestApplyMode|TestValidateMode|TestValidateIdempotencyKey|TestClampExperience"
```

Expected: PASS。

- [ ] **Step 5: RED — 状態機械の契約を固定する (PostgreSQL 必要)**

`plugins/rolelevel/xp_test.go` に追加:

```go
// recordingInvalidator records what the plugin asked the host to drop.
type recordingInvalidator struct {
	users []string
	roles []string
}

func (r *recordingInvalidator) InvalidateUser(_ context.Context, id string) error {
	r.users = append(r.users, id)
	return nil
}

func (r *recordingInvalidator) InvalidateRole(_ context.Context, id string) error {
	r.roles = append(r.roles, id)
	return nil
}

func newXPService(t *testing.T, api *stubAPI) (*service, *recordingInvalidator) {
	t.Helper()
	db := testDB(t)
	h := plugintest.New(t).WithName("role-level").WithDB(db).
		WithConfig(map[string]any{"actorId": "admin1", "assignmentScanPages": 50}).WithAPI(api)
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
	return ChangeExpRequest{
		IdempotencyKey: key, ActorID: "admin1", UserID: userID, RoleID: roleID,
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
		t.Fatalf("audit = %d 件, want 1 (再送でaudit.ToStringが増えない)", len(entries))
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
			"r1": {mkAssignment("asg-old", "u1"), mkAssignment("asg-new", "u1")},
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
		IdempotencyKey: "k1", ActorID: "admin1", UserID: "u1", RoleID: "r1",
		Mode: string(ModeAdd), Operand: 40, Status: string(StatusAssigning),
	}); err != nil {
		t.Fatal(err)
	}

	res, err := svc.ChangeExp(context.Background(), xpRequest("k1", "u1", "r1", ModeAdd, 40))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Resumed {
		t.Fatal("Resumed = false, want true")
	}
	if res.Experience != 40 || res.Status != StatusCompleted {
		t.Fatalf("= %+v", res)
	}
}

// **native assign が失敗したら operation は failed になって止まる。** 再試行のたびに
// 勝手にやり直さない (reconciliation job が同じ key で再開する)。
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
	if len(inv.users) != 0 || len(inv.roles) != 0 {
		t.Fatalf("commit 前に invalidate しました: users=%+v roles=%+v", inv.users, inv.roles)
	}
}
```

- [ ] **Step 6: 状態機械を実装する**

`plugins/rolelevel/xp.go` に追加:

```go
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
		return ChangeExpResult{}, s.storageError(ctx, "XP 操作の記録に失敗しました", err)
	}

	op, found, err := s.store.LoadOperation(ctx, req.IdempotencyKey)
	if err != nil {
		return ChangeExpResult{}, s.storageError(ctx, "XP 操作の読み込みに失敗しました", err)
	}
	if !found {
		return ChangeExpResult{}, s.storageError(ctx, "XP 操作の読み込みに失敗しました",
			fmt.Errorf("記録直後に %s が見つかりません", req.IdempotencyKey))
	}
	// 同じ key の再送。**completed なら結果を返すだけで何もしない。**
	if !created && op.Status == string(StatusCompleted) {
		return ChangeExpResult{
			AssignmentID: op.AssignmentID,
			Experience:   derefInt64(op.DesiredExp),
			Status:       StatusCompleted,
			Resumed:      true,
		}, nil
	}
	return s.resume(ctx, op, req.Note)
}

// resume continues an operation from whatever state the row records.
//
// It is the single implementation behind the first attempt, a client retry and the
// reconciliation job, so the three can never drift apart.
//
// note is the operator's note for the audit row. **operation テーブルに note 列が無い**
// のは、note が監査専用で操作の再開には要らないため。job からの再開は "" を渡す。
func (s *service) resume(ctx context.Context, op operation, note string) (ChangeExpResult, error) {
	native, err := s.native()
	if err != nil {
		return s.fail(ctx, op, err)
	}

	assignmentID := op.AssignmentID
	if assignmentID == "" {
		assignmentID, _, err = s.assignmentIDFor(ctx, native, op.RoleID, op.UserID)
		if err != nil {
			return s.fail(ctx, op, err)
		}
	}
	if assignmentID == "" {
		// 未付与。**先に status を書いてから native を呼ぶ。** クラッシュしても
		// 「どこまで進んだか」が残不然、reconciliation が再開 deciding できない。
		if err := s.store.SetOperationStatus(ctx, s.store.db, op.IdempotencyKey,
			StatusAssigning, "", nil, ""); err != nil {
			return ChangeExpResult{}, s.storageError(ctx, "XP 操作の記録に失敗しました", err)
		}
		if err := native.Assign(ctx, op.UserID, op.RoleID); err != nil {
			return s.fail(ctx, op, err)
		}
		assignmentID, _, err = s.assignmentIDFor(ctx, native, op.RoleID, op.UserID)
		if err != nil {
			return s.fail(ctx, op, err)
		}
		if assignmentID == "" {
			return s.fail(ctx, op, codedErrorf(http.StatusBadGateway, CodeAssignmentUnresolved,
				"付与直後に assignment を取得できませんでした (roleId=%s, userId=%s)",
				op.RoleID, op.UserID))
		}
	}

	if err := s.store.SetOperationStatus(ctx, s.store.db, op.IdempotencyKey,
		StatusApplying, assignmentID, nil, ""); err != nil {
		return ChangeExpResult{}, s.storageError(ctx, "XP 操作の記録に失敗しました", err)
	}

	current, err := s.experienceFor(ctx, op.RoleID, op.UserID, assignmentID)
	if err != nil {
		return s.fail(ctx, op, err)
	}
	desired, err := applyMode(Mode(op.Mode), current, op.Operand)
	if err != nil {
		return s.fail(ctx, op, err)
	}

	// **XP・audit・operation completion を1つの transaction で決める。**
	// これ以外の組み合わせだと「XP は増えたのに operation が pending のまま」という窓が
	// できて、reconciliation が add を二重適用する。
	tx, err := s.store.db.BeginTx(ctx, nil)
	if err != nil {
		return ChangeExpResult{}, s.storageError(ctx, "transaction の開始に失敗しました", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := s.store.SetExperience(ctx, tx, experienceRow{
		AssignmentID: assignmentID, RoleID: op.RoleID, UserID: op.UserID, Experience: desired,
	}); err != nil {
		return s.failTx(ctx, tx, op, "経験値の保存に失敗しました", err)
	}
	if err := s.store.InsertAudit(ctx, tx, auditEntry{
		ActorID: op.ActorID, Operation: "change-exp", RoleID: op.RoleID, UserID: op.UserID,
		AssignmentID: assignmentID, Note: truncate(note, 500),
		Before: map[string]any{"experience": current},
		After:  map[string]any{"experience": desired},
	}); err != nil {
		return s.failTx(ctx, tx, op, "監査の記録に失敗しました", err)
	}
	if err := s.store.SetOperationStatus(ctx, tx, op.IdempotencyKey,
		StatusCompleted, assignmentID, &desired, ""); err != nil {
		return s.failTx(ctx, tx, op, "XP 操作の記録に失敗しました", err)
	}
	if err := tx.Commit(); err != nil {
		return s.fail(ctx, op, s.storageError(ctx, "transaction の commit に失敗しました", err))
	}
	committed = true

	// **commit の後にだけ cache を落とす。** 先に落とすと、commit に失敗した変更を
	// 他ワーカーに配ってしまう。 invalidate に失敗しても XP 自体は保存済みなので、
	// warn だけで済ませる (次回の invalidation で回復する)。
	s.invalidateAfterXPChange(ctx, op.UserID, op.RoleID)

	return ChangeExpResult{
		AssignmentID: assignmentID, Experience: desired, Status: StatusCompleted,
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
// の数据和が同じ id を指したときに、静かに別の experience を上書きする。
func (s *service) experienceFor(ctx context.Context, roleID, userID, assignmentID string) (int64, error) {
	rows, err := s.store.ExperienceRowsForRoleUser(ctx, roleID, userID)
	if err != nil {
		return 0, s.storageError(ctx, "経験値の読み込みに失敗しました", err)
	}
	for _, r := range rows {
		if r.AssignmentID == assignmentID {
			return r.Experience, nil
		}
	}
	return 0, nil
}

// fail records a terminal failure and returns the error unchanged.
func (s *service) fail(ctx context.Context, op operation, cause error) (ChangeExpResult, error) {
	if err := s.store.SetOperationStatus(ctx, s.store.db, op.IdempotencyKey,
		StatusFailed, op.AssignmentID, op.DesiredExp, truncate(cause.Error(), 500)); err != nil {
		s.log.Error("role-level: XP 操作の失敗を記録できませんでした",
			"idempotencyKey", op.IdempotencyKey, "err", err)
	}
	return ChangeExpResult{}, cause
}

// failTx rolls back and records the failure outside the (already doomed) transaction.
func (s *service) failTx(ctx context.Context, tx *sql.Tx, op operation, what string, cause error) (ChangeExpResult, error) {
	_ = tx.Rollback()
	wrapped := s.storageError(ctx, what, cause)
	return s.fail(ctx, op, wrapped)
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

// derefInt64 reads a nullable bigint column.
func derefInt64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// truncate bounds a stored message. **エラー本文をそのまま入れない** — API の応答が
// 丸ごと入ると行が肥大化する (plugins/trustlevel と同じ)。
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n]) + "..."
}
```

`xp.go` の import は次の7つ (`math/bits` は要らない — 整数の飽和加算は XP が
`int64` の範囲に収ましているので、float64 の clamp に置き換えた):

```go
import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strings"
)
```

- [ ] **Step 7: GREEN を確認する**

```powershell
$env:GOWORK = "off"
go -C plugins/rolelevel test ./... -count=1 -run "TestApplyMode|TestValidateMode|TestValidateIdempotencyKey|TestClampExperience|TestChangeExp"
go -C plugins/rolelevel vet ./...
gofmt -l plugins/rolelevel
```

Expected: PASS、`gofmt -l` が出力しないこと。

- [ ] **Step 8: commit する**

```powershell
git add plugins/rolelevel/xp.go plugins/rolelevel/xp_test.go
git commit -m "Phase roleLevel: add the idempotent XP operation state machine"
```

---

### Task 9: effective-policy 置換 resolver

**Files:**
- Create: `plugins/rolelevel/resolver.go`
- Create: `plugins/rolelevel/resolver_test.go`
- Modify: `plugins/rolelevel/plugin.go` (`Plugin` に `EffectivePolicies: effectivePolicies` を追加)

**Interfaces:**
- Consumes: Task 1 の `invalidatorHandle.set` / `defaultCatalog` / `service` / `newService`, Task 2 の `Catalog.Keys`, Task 3 の `Config.Experience` / `rangeForStage` / `rangeValue`, Task 4 の `PolicyRange`, Task 5 の `LoadConfigsForRoles` / `ExperienceForAssignments`, `plugin.ActiveRoleAssignment` / `plugin.EffectivePolicyRequest` / `plugin.EffectivePolicyContribution.ReplaceRoleID`
- Produces:
  - `func effectivePolicies(pctx plugin.Context, inv plugin.EffectivePolicyInvalidator) (plugin.EffectivePolicyRegistration, error)`
  - `func (s *service) resolveReplacements(ctx context.Context, req plugin.EffectivePolicyRequest) ([]plugin.EffectivePolicyContribution, error)`

- [ ] **Step 1: RED — 置換の契約を固定する**

`plugins/rolelevel/resolver_test.go`:

```go
package rolelevel

import (
	"context"
	"testing"

	"github.com/shiroha-a/mk/plugin"
	"github.com/shiroha-a/mk/plugin/plugintest"
)

// newResolverService builds a service with one level role whose curve is
// const 100 XP and whose single range is a const policy.
func newResolverService(t *testing.T, ranges ...PolicyRange) *service {
	t.Helper()
	db := testDB(t)
	h := plugintest.New(t).WithName("role-level").WithDB(db).
		WithConfig(map[string]any{"actorId": "admin1"}).WithAPI(&stubAPI{})
	svc, err := newService(h.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(ranges) == 0 {
		ranges = []PolicyRange{{Type: RangeConst, Start: 1, End: 100,
			Key: "canPublicNote", Value: false}}
	}
	cfg := DefaultConfig()
	cfg.RoleID = "r1"
	cfg.PolicyRanges = ranges
	if err := cfg.Validate(defaultCatalog); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.store.UpsertConfig(context.Background(), cfg, 0); err != nil {
		t.Fatal(err)
	}
	return svc
}

func withAssignments(assignments ...plugin.ActiveRoleAssignment) plugin.EffectivePolicyRequest {
	return plugin.EffectivePolicyRequest{UserID: "u1", ActiveAssignments: assignments}
}

// **XP から level を出して、その stage の range で native contribution を置換する。**
func TestResolveReplacements(t *testing.T) {
	svc := newResolverService(t)
	if err := svc.store.SetExperience(context.Background(), svc.store.db,
		experienceRow{AssignmentID: "asg1", RoleID: "r1", UserID: "u1", Experience: 350}); err != nil {
		t.Fatal(err)
	}

	got, err := svc.resolveReplacements(context.Background(),
		withAssignments(plugin.ActiveRoleAssignment{RoleID: "r1", AssignmentID: "asg1"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("contribution = %d 件, want 1", len(got))
	}
	if got[0].Key != "canPublicNote" || got[0].Value != false {
		t.Fatalf("= %+v", got[0])
	}
	if got[0].ReplaceRoleID != "r1" {
		t.Fatalf("ReplaceRoleID = %q, want r1", got[0].ReplaceRoleID)
	}
}

// **level 設定が無い role は native のまま。** 触らないのが正しい (置換先が無い)。
func TestResolveReplacementsSkipsRolesWithoutConfig(t *testing.T) {
	svc := newResolverService(t)
	got, err := svc.resolveReplacements(context.Background(),
		withAssignments(plugin.ActiveRoleAssignment{RoleID: "other", AssignmentID: "asg9"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("= %+v, want empty", got)
	}
}

// **XP の行が無い = 0 XP。** level 設定があれば contribution は出る。
func TestResolveReplacementsTreatsMissingRowAsZero(t *testing.T) {
	svc := newResolverService(t)
	got, err := svc.resolveReplacements(context.Background(),
		withAssignments(plugin.ActiveRoleAssignment{RoleID: "r1", AssignmentID: "asg-none"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("contribution = %d 件, want 1", got)
	}
	if got[0].Value != false {
		t.Fatalf("= %+v, want canPublicNote=false (XP 0 の range)", got[0])
	}
}

// **base range も置換 contribution を出す。** `UseDefault: true` を立てて
// `ReplaceRoleID` を付ける。**出さないと native の静的 role contribution がそのまま
// 生き残り、level 設定が効かない** — instance default を使う =
// 「その role の contribution を取り除く」ことであり、分ける事自体が違う。
func TestResolveReplacementsReplacesBaseRangeWithUseDefault(t *testing.T) {
	svc := newResolverService(t,
		PolicyRange{Type: RangeConst, Start: 1, End: 2, Key: "canPublicNote", Value: false},
		PolicyRange{Type: RangeBase, Start: 2, End: 100, Key: "canSearchNotes"},
	)
	got, err := svc.resolveReplacements(context.Background(),
		withAssignments(plugin.ActiveRoleAssignment{RoleID: "r1", AssignmentID: "asg1"}))
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]plugin.EffectivePolicyContribution{}
	for _, c := range got {
		byKey[c.Key] = c
	}
	pub, ok := byKey["canPublicNote"]
	if !ok || pub.Value != false || pub.UseDefault {
		t.Fatalf("canPublicNote = %+v, want Value=false / UseDefault=false", pub)
	}
	search, ok := byKey["canSearchNotes"]
	if !ok {
		t.Fatalf("base range の contribution がありません: %+v", got)
	}
	if !search.UseDefault {
		t.Fatalf("base range が UseDefault=false です: %+v", search)
	}
	if search.Value != nil {
		t.Fatalf("UseDefault=true のときに Value を持ってはいけません: %+v", search)
	}
	if search.ReplaceRoleID != "r1" {
		t.Fatalf("ReplaceRoleID = %q, want r1", search.ReplaceRoleID)
	}
}

// **1つの (role, key) に2つ contribution を出さない。** host 側は競合 error にして
// native fallback するので、こちらから競合を作ってはいけない。
func TestResolveReplacementsEmitsOnePerRoleAndKey(t *testing.T) {
	svc := newResolverService(t)
	// 同じ role を持つ active assignment は host が1つしか渡さないが、
	// 万が一重複しても plugin 側で1つにまとめる。
	got, err := svc.resolveReplacements(context.Background(), withAssignments(
		plugin.ActiveRoleAssignment{RoleID: "r1", AssignmentID: "asg1"},
		plugin.ActiveRoleAssignment{RoleID: "r1", AssignmentID: "asg2"},
	))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, c := range got {
		k := c.ReplaceRoleID + "/" + c.Key
		if seen[k] {
			t.Fatalf("%s が重複しています: %+v", k, got)
		}
		seen[k] = true
	}
}

// **storage failure は error を返す。** 返さないと host は「plugin が何も貢献しなかった」
// と誤解して、level 別 policy の効果が黙って消える。
func TestResolveReplacementsFailsOnStorageError(t *testing.T) {
	db := testDB(t)
	h := plugintest.New(t).WithName("role-level").WithDB(db).
		WithConfig(map[string]any{"actorId": "admin1"}).WithAPI(&stubAPI{})
	svc, err := newService(h.Context())
	if err != nil {
		t.Fatal(err)
	}
	svc.db.Close() //nolint:errcheck // テスト終了まで使うな
	if _, err := svc.resolveReplacements(context.Background(),
		withAssignments(plugin.ActiveRoleAssignment{RoleID: "r1", AssignmentID: "asg1"})); err == nil {
		t.Fatal("storage failure で error を返していません")
	}
}

// **registration は native catalog の全 key を宣言する。** host は宣言された key しか
// 集約に混ぜないので、限定するとその key は置換できない。
func TestEffectivePoliciesDeclaresEveryNativeKey(t *testing.T) {
	db := testDB(t)
	// plugintest が host と同じ検証を通すので、未知の key が1つでもあればここで落ちる。
	reg := plugintest.New(t).WithName("role-level").WithDB(db).
		WithConfig(map[string]any{"actorId": "admin1"}).WithAPI(&stubAPI{}).
		EffectivePolicies(Plugin)
	if len(reg.Keys) != len(defaultCatalog.Keys()) {
		t.Fatalf("Keys = %d 件, want %d", len(reg.Keys), len(defaultCatalog.Keys()))
	}
	if reg.Resolve == nil {
		t.Fatal("Resolve が nil です")
	}
}
```


**plugintest の `EffectivePolicies` は `plugin.ActiveRoleAssignment` を含む request をそのまま通す。** そのため、Task 1 の Step 0 で確認した host interface が無ければこの test はコンパイルしない (それが意図)。

- [ ] **Step 2: RED を確認する**

```powershell
$env:GOWORK = "off"
go -C plugins/rolelevel test ./... -count=1 -run "TestResolveReplacements|TestEffectivePolicies"
```

Expected: FAIL — `undefined: resolveReplacements`, `undefined: effectivePolicies`、そして `Plugin.EffectivePolicies` が `nil` なので `TestEffectivePoliciesDeclaresEveryNativeKey` が `Resolve が nil` で落ちる。

- [ ] **Step 3: `resolver.go` を実装する**

`plugins/rolelevel/resolver.go`:

```go
package rolelevel

import (
	"context"
	"log/slog"

	"github.com/shiroha-a/mk/plugin"
)

// effectivePolicies declares the level-based policy replacement provider.
//
// The registration must name every key the plugin may touch, and the host only
// accepts keys that exist in its own defaults — so Keys is the whole embedded
// catalog, not just the keys one role happens to use.
func effectivePolicies(pctx plugin.Context, inv plugin.EffectivePolicyInvalidator) (plugin.EffectivePolicyRegistration, error) {
	// **Routes / Jobs は invalidator を受け取らない。** ここで公開して、XP 変更の
	// あとで user / role の policy cache を落とせるようにする。
	invalidatorHandle.set(inv)
	svc, err := newService(pctx)
	if err != nil {
		return plugin.EffectivePolicyRegistration{}, err
	}
	return plugin.EffectivePolicyRegistration{
		Keys:    defaultCatalog.Keys(),
		Resolve: svc.resolveReplacements,
	}, nil
}

// resolveReplacements turns active assignments into level-based policy replacements.
//
// **network call はしない。** host の provider timeout は1秒で、この経路は policy 解決
// のたびに走る。native へ問い合わせたいことは全部 routes と job 側に置く。
//
// **同じ (role, key) は1つだけ出す。** host 側は2つの plugin が同じ (role, key) を置換
// すると競合 error にして native fallback するので、こちらから競合を作ってはいけない。
func (s *service) resolveReplacements(ctx context.Context, req plugin.EffectivePolicyRequest) ([]plugin.EffectivePolicyContribution, error) {
	if len(req.ActiveAssignments) == 0 {
		return nil, nil
	}

	roleIDs := make([]string, 0, len(req.ActiveAssignments))
	assignmentIDs := make([]string, 0, len(req.ActiveAssignments))
	for _, a := range req.ActiveAssignments {
		if a.RoleID == "" || a.AssignmentID == "" {
			// 不正な入力は「無視」で良い。**error にすると level 別 policy の効果が
			// 皆有って native fallback になる**ので、無害な行を1つ混ぜただけで
			// 利用者全員の権限が緩んだ状態になる。
			s.log.Warn("role-level: active assignment が壊れています", "roleId", a.RoleID, "assignmentId", a.AssignmentID)
			continue
		}
		roleIDs = append(roleIDs, a.RoleID)
		assignmentIDs = append(assignmentIDs, a.AssignmentID)
	}
	if len(assignmentIDs) == 0 {
		return nil, nil
	}

	// **1クエリずつに引かない。** policy 解決は1リクエストで複数の role と
	// assignment に触るので、個別に引くと round trip がその数だけ増える。
	configs, err := s.store.LoadConfigsForRoles(ctx, roleIDs)
	if err != nil {
		return nil, s.storageError(ctx, "level 設定の読み込みに失敗しました", err)
	}
	experience, err := s.store.ExperienceForAssignments(ctx, assignmentIDs)
	if err != nil {
		return nil, s.storageError(ctx, "経験値の読み込みに失敗しました", err)
	}

	var (
		out  []plugin.EffectivePolicyContribution
		seen = make(map[string]bool, len(req.ActiveAssignments))
	)
	for i, a := range req.ActiveAssignments {
		cfg, found := configs[a.RoleID]
		if !found {
			// level 未設定の role は native のまま。
			continue
		}
		// 行が無い assignment の XP は 0。Experience(0) から level が出るので、
		// 0 XP の user を XP 0 の行で埋める必要はない。
		exp, err := cfg.Experience(experience[a.AssignmentID])
		if err != nil {
			return nil, s.storageError(ctx, "level の計算に失敗しました", err)
		}
		rule, ok := rangeForStage(cfg.PolicyRanges, exp.ProgressionStage)
		if !ok {
			// validation 済みなので、ここには来ない。取り敢えず native に落とす。
			continue
		}
		value, err := rangeValue(rule, exp.ProgressionStage)
		if err != nil {
			return nil, s.storageError(ctx, "policy range の評価に失敗しました", err)
		}
		key := a.RoleID + "/" + rule.Key
		if seen[key] {
			s.log.Warn("role-level: 同じ role / key の置換が複数あるため1つにまとめます",
				"roleId", a.RoleID, "key", rule.Key)
			continue
		}
		seen[key] = true
		out = append(out, plugin.EffectivePolicyContribution{
			Key: rule.Key,
			// **Priority は無視される。** ReplaceRoleID が非空の contribution では host は
			// native の (role, key) contribution の priority を使うので、ここは host の
			// contribution validation (0..2) を満たす値にしておく。
			Priority: 0,
			// **base range もここで contribution を出す。** `UseDefault: true` は
			// 「instance default を使う」=「その role の静的 contribution を取り除く」で
			// あるので、出さないと native の role policy が生き残って level 設定が効かない。
			// base では `value` が nil になる (rangeValue がそう返す)。
			UseDefault: value == nil,
			Value:      value,
			// **これが置換の要。** この role の、この key の native contribution を
			// 1対1で置き換える。
			ReplaceRoleID: a.RoleID,
			// Order は (Key, Order) が一意である必要がある。request 内の index を使うので
			// 天然に一意で、決定的な並びも保てる。
			Order: i,
		})
	}
	return out, nil
}
```

Task 9 は `rangeValue` を**定義しない**。**定義は Task 4 の `policyrange.go` に1つだけ**あり、
戻り値の `base` でだけ nil になることに依存している:

- signature は `func rangeValue(r PolicyRange, stage int64) (any, error)` (bool を返す必要が無い)。
- 呼び出し側は nil を「instance default を使う = `UseDefault: true` の contribution」として扱う。
- Task 4 の `validateRanges` / `validateRangeRules` も同じ `rangeValue` を呼ぶので、
  **2ファイルに定義を書かない。** `resolver.go` は Task 4 が定義済みの関数を呼ぶだけ。

`resolver.go` の import は `context` と `github.com/shiroha-a/mk/plugin` だけ。`log/slog` は使わないので外す:

```go
import (
	"context"

	"github.com/shiroha-a/mk/plugin"
)
```

- [ ] **Step 4: `Plugin` に `EffectivePolicies` を登録する**

`plugins/rolelevel/plugin.go` の `Plugin` 変数を次のように変更:

```go
var Plugin = plugin.Definition{
	Name:       "role-level",
	Version:    "1.0.0",
	APIVersion: plugin.APIVersion,
	Migrations: migrations,
	Routes:     routes,
	Jobs:       jobs,
	// **effective policy の置換を出す。** 追加だけだと boolean の OR と数値の max で
	// 結果が変わるので、level に応じて「緩い policy を上書き」できる置換経路が要る。
	EffectivePolicies: effectivePolicies,
}
```

- [ ] **Step 5: GREEN を確認する**

```powershell
$env:GOWORK = "off"
go -C plugins/rolelevel test ./... -count=1 -run "TestResolveReplacements|TestEffectivePolicies|TestPluginNameMatchesManifest"
go -C plugins/rolelevel vet ./...
gofmt -l plugins/rolelevel
```

Expected: PASS。`TestPluginNameMatchesManifest` もここで初めて通る (`Plugin.Validate` は EffectivePolicies が nil でも通るので、意味を持つのは `EffectivePolicies != nil` の確認だけ)。

- [ ] **Step 6: commit する**

```powershell
git add plugins/rolelevel/resolver.go plugins/rolelevel/resolver_test.go plugins/rolelevel/plugin.go
git commit -m "Phase roleLevel: replace native policies by level"
```

---

### Task 10: routes

**Files:**
- Create: `plugins/rolelevel/routes.go`
- Create: `plugins/rolelevel/routes_test.go`
- Modify: `plugins/rolelevel/plugin.go` (Task 1 の `routes` スタブを本実装に置き換える)

**Interfaces:**
- Consumes: Task 1 の `service` / `codedErrorf` / `validateID` / `statusError` / `Code*`, Task 5 の `store` / `auditEntry` / `RecentAudit` / `ResumableOperations`, Task 6 の `nativeRole`, Task 7 の `requireAdmin` / `requireModerator` / `AuthorizeXPChange` / `RequireConfigAdmin`, Task 8 の `ChangeExp` / `ChangeExpRequest` / `Mode`, Task 11 の `RunReconcile`
- Produces (all registered with `router.POST`, under the plugin namespace `/api/plugin/role-level`):

  | # | path | 権限 | body | 応答 |
  |---|---|---|---|---|
  | 1 | `/admin/roles/list` | moderator | `{}` | `{"roles":[<Config>], "memberCounts":{<roleId>:int>}}` |
  | 2 | `/admin/roles/show` | moderator | `{"roleId"}` | `{"role":<Config>, "memberCount":int, "membersTruncated":bool}` |
  | 3 | `/admin/roles/update` | administrator | `{roleId, baseLevel, experienceCurve, policyRanges, revision, note?}` | `{"role":<Config>}` |
  | 4 | `/admin/roles/delete` | administrator | `{roleId, revision}` | `{"roleId":string, "deleted":true}` |
  | 5 | `/admin/users/show` | moderator | `{"userId"}` | `{"userId","roles":[…],"audit":[…],"operations":[…]}` |
  | 6 | `/admin/change-exp` | moderator (role による) | `{idempotencyKey, userId, roleId, mode, operand, note?}` | `{"assignmentId","experience","status","resumed"}` |
  | 7 | `/roles/users` | 公開 | `{roleId, limit?, offset?}` | `{"roleId","total","truncated","members":[…]}` |
  | 8 | `/users/show` | 公開 | `{"userId"}` | `{"userId","roles":[…]}` |
  | 9 | `/admin/audit` | moderator | `{roleId?, userId?, limit?}` | `{"entries":[…]}` |
  | 10 | `/admin/orphans` | moderator | `{}` | `{"roles":{<roleId>:{tracked, orphans, orphanAssignmentIds}}}` |
  | 11 | `/admin/reconcile` | administrator | `{"mode"?: "resume-operations"\|"reconcile-orphans"\|"prune-orphans"}` | `{"mode":string, "result":{…}}` |

  - `const maxPageSize = 100`, `const defaultPageSize = 20`
  - `func pageBounds(body pageRequest) (limit, offset int)`
  - `type pageRequest struct { Limit, Offset int }`
  - `func (s *service) memberCount(ctx context.Context, roleID string) (int, bool, error)`
  - `func (s *service) experienceForRoleUser(ctx context.Context, native *nativeRole, roleID, userID string) (assigned bool, experience int64, assignmentID string, err error)`
  - `func (s *service) publicProfile(ctx context.Context, userID string) (any, error)`
  - `func (s *service) roleMembers(ctx context.Context, roleID string, limit, offset int) (any, error)`
  - `func (s *service) adminUser(ctx context.Context, userID string) (any, error)`
  - `func (s *service) auditTrail(ctx context.Context, roleID, userID string, limit int) (any, error)`
  - `func (s *service) invalidateRoleConfig(ctx context.Context, roleID string)` — Task 8 で `xp.go` に定義済み

  **native `admin/roles/assign` / `admin/roles/unassign` はこの表に無い。** これらは plugin
  内部の native API call であり、plugin route ではない (Task 6 / Task 8 が使う)。

- [ ] **Step 1: RED — route の契約を固定する**

`plugins/rolelevel/routes_test.go`:

```go
package rolelevel

import (
	"encoding/json"
	"testing"

	"github.com/shiroha-a/mk/plugin"
	"github.com/shiroha-a/mk/plugin/plugintest"
)

// decode re-marshals a handler result so the assertions run against the JSON the
// client actually sees, not against the plugin's Go types.
func decode(t *testing.T, res any, out any) {
	t.Helper()
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("応答を読めません: %v (%s)", err, raw)
	}
}

func levelRoleAPI() *stubAPI {
	return &stubAPI{
		roles: map[string]roleInfo{"r1": {ID: "r1", Target: "manual"}},
		assignments: map[string][]assignment{
			"r1": {mkAssignment("asg1", "u1"), mkAssignment("asg2", "u2")},
		},
	}
}

func routeHarness(t *testing.T, api plugin.API) plugintest.Handlers {
	t.Helper()
	return newHarness(t, testDB(t), api).Routes(Plugin)
}

// **全 route が POST で登録されている。** path parameter も query string 없이、
// 全部 body で受ける (Global Constraints の「設計上の判断」)。GET が1本でも残って
// いたら、frontend が POST で叩いて 404 になる。
func TestAllRoutesArePost(t *testing.T) {
	h := routeHarness(t, levelRoleAPI())
	want := []string{
		"POST /admin/roles/list",
		"POST /admin/roles/show",
		"POST /admin/roles/update",
		"POST /admin/roles/delete",
		"POST /admin/users/show",
		"POST /admin/change-exp",
		"POST /roles/users",
		"POST /users/show",
		"POST /admin/audit",
		"POST /admin/orphans",
		"POST /admin/reconcile",
	}
	if len(h) != len(want) {
		t.Fatalf("登録された route = %d 本 (%v), want %d 本", len(h), keysOf(h), len(want))
	}
	for _, key := range want {
		if _, err := h.Call(t, key, plugintest.Request{Body: `{}`}); err == nil {
			t.Fatalf("%s: body 空でも通っています (path / 権限の検証が要先)", key)
		}
	}
}

func keysOf(h plugintest.Handlers) []string {
	out := make([]string, 0, len(h))
	for k := range h {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// **level 設定の作成・更新・削除は administrator だけ。** frontend を隠しても
// 守らないので、ハンドラ側で確認する。
func TestAdminConfigRoutesRequireAdministrator(t *testing.T) {
	h := routeHarness(t, levelRoleAPI())
	for _, key := range []string{"POST /admin/roles/list", "POST /admin/roles/show"} {
		if _, err := h.Call(t, key, plugintest.Request{UserID: "u1", Body: `{"roleId":"r1"}`}); err == nil {
			t.Fatalf("%s: 未認証の一般 user を通しています", key)
		}
		if _, err := h.Call(t, key, plugintest.Request{UserID: "m1", Moderator: true, Body: `{"roleId":"r1"}`}); err != nil {
			t.Fatalf("%s: moderator を拒否しました: %v", key, err)
		}
	}
	if _, err := h.Call(t, "POST /admin/roles/update", plugintest.Request{
		UserID: "m1", Moderator: true,
		Body: `{"roleId":"r1","baseLevel":1,"experienceCurve":[{"type":"const","levelUps":9,"base":10}],"policyRanges":[{"type":"base","start":1,"end":10}],"revision":0}`,
	}); err == nil {
		t.Fatal("moderator に level 設定を保存させています")
	}
	if _, err := h.Call(t, "POST /admin/roles/delete", plugintest.Request{
		UserID: "m1", Moderator: true, Body: `{"roleId":"r1","revision":1}`,
	}); err == nil {
		t.Fatal("moderator に level 設定を消させています")
	}
	if _, err := h.Call(t, "POST /admin/reconcile", plugintest.Request{
		UserID: "m1", Moderator: true, Body: `{"mode":"resume-operations"}`,
	}); err == nil {
		t.Fatal("moderator に reconciliation を実行させています")
	}
}

// **保存の round trip。**
func TestAdminConfigRoundTrip(t *testing.T) {
	h := routeHarness(t, levelRoleAPI())
	res, err := h.Call(t, "POST /admin/roles/update", plugintest.Request{
		UserID: "a1", Administrator: true,
		Body: `{"roleId":"r1","baseLevel":1,"experienceCurve":[{"type":"const","levelUps":9,"base":10}],"policyRanges":[{"type":"base","start":1,"end":10}],"revision":0}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Role struct {
			Revision int64 `json:"revision"`
		} `json:"role"`
	}
	decode(t, res, &saved)
	if saved.Role.Revision != 1 {
		t.Fatalf("revision = %d, want 1", saved.Role.Revision)
	}

	res, err = h.Call(t, "POST /admin/roles/show", plugintest.Request{
		UserID: "a1", Administrator: true, Body: `{"roleId":"r1"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	var one struct {
		Role struct {
			BaseLevel       int64 `json:"baseLevel"`
		} `json:"role"`
		MemberCount       int  `json:"memberCount"`
		MembersTruncated  bool `json:"membersTruncated"`
	}
	decode(t, res, &one)
	if one.Role.BaseLevel != 1 || one.MemberCount != 2 || one.MembersTruncated {
		t.Fatalf("= %+v", one)
	}
}

// **list は全 level 設定と member 件数を返す。**
func TestAdminRolesList(t *testing.T) {
	h := routeHarness(t, levelRoleAPI())
	if _, err := h.Call(t, "POST /admin/roles/update", plugintest.Request{
		UserID: "a1", Administrator: true,
		Body: `{"roleId":"r1","baseLevel":1,"experienceCurve":[{"type":"const","levelUps":9,"base":10}],"policyRanges":[{"type":"base","start":1,"end":10}],"revision":0}`,
	}); err != nil {
		t.Fatal(err)
	}
	res, err := h.Call(t, "POST /admin/roles/list", plugintest.Request{
		UserID: "m1", Moderator: true, Body: `{}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Roles []struct {
			RoleID string `json:"roleId"`
		} `json:"roles"`
		MemberCounts map[string]int `json:"memberCounts"`
	}
	decode(t, res, &got)
	if len(got.Roles) != 1 || got.Roles[0].RoleID != "r1" {
		t.Fatalf("roles = %+v", got.Roles)
	}
	if got.MemberCounts["r1"] != 2 {
		t.Fatalf("memberCounts = %+v", got.MemberCounts)
	}
}

// **未知の policy key は stable code 付きで 400。**
func TestAdminConfigRejectsUnknownPolicyKey(t *testing.T) {
	h := routeHarness(t, levelRoleAPI())
	_, err := h.Call(t, "POST /admin/roles/update", plugintest.Request{
		UserID: "a1", Administrator: true,
		Body: `{"roleId":"r1","baseLevel":1,"experienceCurve":[{"type":"const","levelUps":9,"base":10}],"policyRanges":[{"type":"const","start":1,"end":10,"key":"noSuchKey","value":true}],"revision":0}`,
	})
	if err == nil {
		t.Fatal("未知の key を受理しています")
	}
	if _, code := extractCode(err); code != CodeUnknownPolicyKey {
		t.Fatalf("code = %q, want %s (%v)", code, CodeUnknownPolicyKey, err)
	}
}

// **curve の overflow は保存時に弾かれる。**
func TestAdminConfigRejectsUnsafeCurve(t *testing.T) {
	h := routeHarness(t, levelRoleAPI())
	_, err := h.Call(t, "POST /admin/roles/update", plugintest.Request{
		UserID: "a1", Administrator: true,
		Body: `{"roleId":"r1","baseLevel":0,"experienceCurve":[{"type":"const","levelUps":1000000,"base":9007199254740991}],"policyRanges":[{"type":"base","start":1,"end":1000001}],"revision":0}`,
	})
	if err == nil {
		t.Fatal("overflow する curve を受理しています")
	}
	if _, code := extractCode(err); code != CodeInvalidCurve {
		t.Fatalf("code = %q, want %s (%v)", code, CodeInvalidCurve, err)
	}
}

// **revision ecto は conflict。** 古い画面からの保存で黙って上書きしない。
func TestAdminConfigRevisionConflict(t *testing.T) {
	h := routeHarness(t, levelRoleAPI())
	body := `{"roleId":"r1","baseLevel":1,"experienceCurve":[{"type":"const","levelUps":9,"base":10}],"policyRanges":[{"type":"base","start":1,"end":10}],"revision":%d}`
	if _, err := h.Call(t, "POST /admin/roles/update", plugintest.Request{
		UserID: "a1", Administrator: true, Body: fmt.Sprintf(body, 0),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Call(t, "POST /admin/roles/update", plugintest.Request{
		UserID: "a1", Administrator: true, Body: fmt.Sprintf(body, 1),
	}); err != nil {
		t.Fatal(err)
	}
	_, err := h.Call(t, "POST /admin/roles/update", plugintest.Request{
		UserID: "a1", Administrator: true, Body: fmt.Sprintf(body, 1),
	})
	if err == nil {
		t.Fatal("古い revision で上書きできています")
	}
	if _, code := extractCode(err); code != CodeConfigConflict {
		t.Fatalf("code = %q, want %s (%v)", code, CodeConfigConflict, err)
	}
}

// **conditional role は level 対象ではない。**
func TestAdminConfigRejectsConditionalRole(t *testing.T) {
	api := levelRoleAPI()
	api.roles["r1"] = roleInfo{ID: "r1", Target: "conditional"}
	h := routeHarness(t, api)
	_, err := h.Call(t, "POST /admin/roles/update", plugintest.Request{
		UserID: "a1", Administrator: true,
		Body: `{"roleId":"r1","baseLevel":1,"experienceCurve":[{"type":"const","levelUps":9,"base":10}],"policyRanges":[{"type":"base","start":1,"end":10}],"revision":0}`,
	})
	if err == nil {
		t.Fatal("conditional role に level 設定を保存しています")
	}
	if _, code := extractCode(err); code != CodeRoleNotManual {
		t.Fatalf("code = %q, want %s (%v)", code, CodeRoleNotManual, err)
	}
}

// **XP 変更は idempotency key 必須。** 無いと二重送信で 2 回適用される。
func TestChangeExpRouteRequiresIdempotencyKey(t *testing.T) {
	h := routeHarness(t, levelRoleAPI())
	_, err := h.Call(t, "POST /admin/change-exp", plugintest.Request{
		UserID: "a1", Administrator: true,
		Body: `{"userId":"u1","roleId":"r1","mode":"add","operand":50}`,
	})
	if err == nil {
		t.Fatal("idempotencyKey 無しで受け付けています")
	}
	if _, code := extractCode(err); code != CodeIdempotencyKeyRequired {
		t.Fatalf("code = %q, want %s (%v)", code, CodeIdempotencyKeyRequired, err)
	}
}

// **operand は有限の小数を JSON から受ける。** 1.5 は ×1.5。
func TestChangeExpRouteAcceptsDecimalOperand(t *testing.T) {
	h := routeHarness(t, levelRoleAPI())
	if _, err := h.Call(t, "POST /admin/change-exp", plugintest.Request{
		UserID: "a1", Administrator: true,
		Body: `{"idempotencyKey":"k0","userId":"u1","roleId":"r1","mode":"set","operand":250}`,
	}); err != nil {
		t.Fatal(err)
	}
	res, err := h.Call(t, "POST /admin/change-exp", plugintest.Request{
		UserID: "a1", Administrator: true,
		Body: `{"idempotencyKey":"k1","userId":"u1","roleId":"r1","mode":"multiplier","operand":1.5}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Experience int64 `json:"experience"`
	}
	decode(t, res, &got)
	if got.Experience != 375 {
		t.Fatalf("experience = %d, want 375", got.Experience)
	}
}

func TestChangeExpRoute(t *testing.T) {
	h := routeHarness(t, levelRoleAPI())
	res, err := h.Call(t, "POST /admin/change-exp", plugintest.Request{
		UserID: "a1", Administrator: true,
		Body: `{"idempotencyKey":"k1","userId":"u1","roleId":"r1","mode":"add","operand":50,"note":"test"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		AssignmentID string `json:"assignmentId"`
		Experience   int64  `json:"experience"`
		Status       string `json:"status"`
	}
	decode(t, res, &got)
	if got.AssignmentID != "asg1" || got.Experience != 50 || got.Status != "completed" {
		t.Fatalf("= %+v", got)
	}
}

// **moderator は canEditMembersByModerator の role だけ。** 権限判定は route でも
// service でも同じ判定を1箇所にまとめる (Task 7)。
func TestChangeExpRouteModeratorGate(t *testing.T) {
	api := levelRoleAPI()
	api.roles["r1"] = roleInfo{ID: "r1", Target: "manual", CanEditMembersByModerator: true}
	h := routeHarness(t, api)
	if _, err := h.Call(t, "POST /admin/change-exp", plugintest.Request{
		UserID: "m1", Moderator: true,
		Body:   `{"idempotencyKey":"k1","userId":"u1","roleId":"r1","mode":"add","operand":50}`,
	}); err != nil {
		t.Fatalf("開いている role を moderator に拒否しました: %v", err)
	}

	api.roles["r1"] = roleInfo{ID: "r1", Target: "manual"}
	h2 := routeHarness(t, api)
	if _, err := h2.Call(t, "POST /admin/change-exp", plugintest.Request{
		UserID: "m1", Moderator: true,
		Body:   `{"idempotencyKey":"k2","userId":"u1","roleId":"r1","mode":"add","operand":50}`,
	}); err == nil {
		t.Fatal("閉じた role を moderator に通しています")
	}
}

// **public response は未付与の role を出さない。** native の role visibility を超える
// 情報を返さないため。
func TestPublicProfileOnlyReturnsAssignedRoles(t *testing.T) {
	h := routeHarness(t, levelRoleAPI())
	if _, err := h.Call(t, "POST /admin/roles/update", plugintest.Request{
		UserID: "a1", Administrator: true,
		Body: `{"roleId":"r1","baseLevel":1,"experienceCurve":[{"type":"const","levelUps":9,"base":10}],"policyRanges":[{"type":"base","start":1,"end":10}],"revision":0}`,
	}); err != nil {
		t.Fatal(err)
	}
	// u1 は r1 に付与済み、u9 は未付与。
	res, err := h.Call(t, "POST /users/show", plugintest.Request{Body: `{"userId":"u1"}`})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Roles []struct {
			RoleID string `json:"roleId"`
			Level  struct {
				CurrentLevel int64 `json:"currentLevel"`
			} `json:"level"`
		} `json:"roles"`
	}
	decode(t, res, &got)
	if len(got.Roles) != 1 || got.Roles[0].RoleID != "r1" || got.Roles[0].Level.CurrentLevel != 1 {
		t.Fatalf("= %+v", got)
	}

	res, err = h.Call(t, "POST /users/show", plugintest.Request{Body: `{"userId":"u9"}`})
	if err != nil {
		t.Fatal(err)
	}
	decode(t, res, &got)
	if len(got.Roles) != 0 {
		t.Fatalf("未付与の role を返しました: %+v", got)
	}
}

// **XP 順の member 一覧。** XP の無い member は 0 で並ぶ。
func TestRoleMembersOrderedByExperience(t *testing.T) {
	h := routeHarness(t, levelRoleAPI())
	if _, err := h.Call(t, "POST /admin/roles/update", plugintest.Request{
		UserID: "a1", Administrator: true,
		Body: `{"roleId":"r1","baseLevel":1,"experienceCurve":[{"type":"const","levelUps":9,"base":10}],"policyRanges":[{"type":"base","start":1,"end":10}],"revision":0}`,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Call(t, "POST /admin/change-exp", plugintest.Request{
		UserID: "a1", Administrator: true,
		Body: `{"idempotencyKey":"k1","userId":"u1","roleId":"r1","mode":"set","operand":500}`,
	}); err != nil {
		t.Fatal(err)
	}
	res, err := h.Call(t, "POST /roles/users", plugintest.Request{
		Body: `{"roleId":"r1","limit":10}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Total     int  `json:"total"`
		Truncated bool `json:"truncated"`
		Members   []struct {
			UserID     string `json:"userId"`
			Experience int64  `json:"experience"`
		} `json:"members"`
	}
	decode(t, res, &got)
	if got.Total != 2 || got.Truncated {
		t.Fatalf("= total %d / truncated %t", got.Total, got.Truncated)
	}
	if len(got.Members) != 2 {
		t.Fatalf("member = %d 人", len(got.Members))
	}
	if got.Members[0].UserID != "u1" || got.Members[0].Experience != 500 {
		t.Fatalf("XP 順になっていません: %+v", got.Members)
	}
}

// **監査を単独で引ける。** admin/user 画面の「履歴」タブ相当。
func TestAdminAuditRoute(t *testing.T) {
	h := routeHarness(t, levelRoleAPI())
	if _, err := h.Call(t, "POST /admin/change-exp", plugintest.Request{
		UserID: "a1", Administrator: true,
		Body: `{"idempotencyKey":"k1","userId":"u1","roleId":"r1","mode":"add","operand":50,"note":"first"}`,
	}); err != nil {
		t.Fatal(err)
	}
	res, err := h.Call(t, "POST /admin/audit", plugintest.Request{
		UserID: "m1", Moderator: true, Body: `{"userId":"u1","limit":10}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Entries []struct {
			Operation string `json:"operation"`
			UserID    string `json:"userId"`
		} `json:"entries"`
	}
	decode(t, res, &got)
	if len(got.Entries) != 1 || got.Entries[0].Operation != "change-exp" || got.Entries[0].UserID != "u1" {
		t.Fatalf("= %+v", got.Entries)
	}
}

// **orphan 一覧を単独で引ける。** plugin 管理画面の\u72B6\u614B\u8868\u793A\u7528\u3002
func TestAdminOrphansRoute(t *testing.T) {
	h := routeHarness(t, levelRoleAPI())
	if _, err := h.Call(t, "POST /admin/roles/update", plugintest.Request{
		UserID: "a1", Administrator: true,
		Body: `{"roleId":"r1","baseLevel":1,"experienceCurve":[{"type":"const","levelUps":9,"base":10}],"policyRanges":[{"type":"base","start":1,"end":10}],"revision":0}`,
	}); err != nil {
		t.Fatal(err)
	}
	res, err := h.Call(t, "POST /admin/orphans", plugintest.Request{
		UserID: "m1", Moderator: true, Body: `{}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Roles map[string]struct {
			Tracked int `json:"tracked"`
			Orphans int `json:"orphans"`
		} `json:"roles"`
	}
	decode(t, res, &got)
	if got.Roles["r1"].Tracked != 0 {
		t.Fatalf("= %+v", got.Roles)
	}
}

// **reconciliation をその場で走らせる。** mode 省略で全部走る。
func TestAdminReconcileRoute(t *testing.T) {
	api := levelRoleAPI()
	db := testDB(t)
	h := newHarness(t, db, api).Routes(Plugin)
	if _, err := h.Call(t, "POST /admin/roles/update", plugintest.Request{
		UserID: "a1", Administrator: true,
		Body: `{"roleId":"r1","baseLevel":1,"experienceCurve":[{"type":"const","levelUps":9,"base":10}],"policyRanges":[{"type":"base","start":1,"end":10}],"revision":0}`,
	}); err != nil {
		t.Fatal(err)
	}
	res, err := h.Call(t, "POST /admin/reconcile", plugintest.Request{
		UserID: "a1", Administrator: true, Body: `{"mode":"reconcile-orphans"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Mode   string         `json:"mode"`
		Result map[string]any `json:"result"`
	}
	decode(t, res, &got)
	if got.Mode != "reconcile-orphans" || got.Result == nil {
		t.Fatalf("= %+v", got)
	}
	// 未知の mode は 400。
	_, err = h.Call(t, "POST /admin/reconcile", plugintest.Request{
		UserID: "a1", Administrator: true, Body: `{"mode":"nope"}`,
	})
	if err == nil {
		t.Fatal("未知の mode を受理しています")
	}
	if _, code := extractCode(err); code != CodeValidationFailed {
		t.Fatalf("code = %q, want %s (%v)", code, CodeValidationFailed, err)
	}
}
```

`routes_test.go` の import に `fmt` と `sort` を追加する:

```go
import (
	"encoding/json"
	"fmt"
	"sort"
	"testing"

	"github.com/shiroha-a/mk/plugin"
	"github.com/shiroha-a/mk/plugin/plugintest"
)
```

- [ ] **Step 2: RED を確認する**

```powershell
$env:GOWORK = "off"
go -C plugins/rolelevel test ./... -count=1 -run "TestAllRoutesArePost|TestAdminConfigRoutes|TestAdminRolesList|TestAdminConfig|TestChangeExpRoute|TestPublicProfile|TestRoleMembers|TestAdminAuditRoute|TestAdminOrphansRoute|TestAdminReconcileRoute"
```

Expected: FAIL — `plugintest: %q は登録されていません` (Task 1 の `routes` は何も登録していないので `POST /admin/roles/list` などが未登録)。

- [ ] **Step 3: `routes.go` を実装する**

`plugins/rolelevel/routes.go`:

```go
package rolelevel

import (
	"context"
	"net/http"
	"sort"

	"github.com/shiroha-a/mk/plugin"
)

// maxPageSize / defaultPageSize bound the member list.
//
// **上限は native の admin/roles/users と同じ 100。** これより大きい pageSize を
// 渡すと plugin 側だけ page を増やして、1回の応答が重くなる。
const (
	maxPageSize     = 100
	defaultPageSize = 20
)

// reconcileAllMode is the `mode` that makes /admin/reconcile run every pass.
const reconcileAllMode = "all"

// pageRequest is the paging body every listing route accepts.
type pageRequest struct {
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

// pageBounds clamps the requested paging to what the native member list returns.
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

// routes registers the plugin's HTTP endpoints.
//
// **11本すべて POST。** path parameter も query string もなく、全部 body で受ける
// (Global Constraints の「設計上の判断」)。**native の admin/roles/assign と
// admin/roles/unassign はここに入らない** — これらは plugin 内部の native API call。
func routes(pctx plugin.Context, router plugin.Router) error {
	svc, err := newService(pctx)
	if err != nil {
		return err
	}

	router.POST("/admin/roles/list", func(req plugin.Request) (any, error) {
		if err := requireModerator(req); err != nil {
			return nil, err
		}
		configs, err := svc.store.ListConfigs(req.Context())
		if err != nil {
			return nil, svc.storageError(req.Context(), "level 設定の読み込みに失敗しました", err)
		}
		// member 件数は 1 設定につきページ走査が要るので、`/admin/roles/show` には出さず
		// list では「有るか無いか」だけを返す。**正確な件数は show を見る**。
		counts := make(map[string]int, len(configs))
		for _, cfg := range configs {
			counts[cfg.RoleID] = 0
		}
		if svc.cfg.ActorID != "" {
			native, err := svc.native()
			if err == nil {
				for _, cfg := range configs {
					all, truncated, lerr := native.ListAssignments(req.Context(), cfg.RoleID)
					if lerr != nil || truncated {
						continue
					}
					counts[cfg.RoleID] = len(all)
				}
			}
		}
		return map[string]any{"roles": configs, "memberCounts": counts}, nil
	})

	router.POST("/admin/roles/show", func(req plugin.Request) (any, error) {
		if err := requireModerator(req); err != nil {
			return nil, err
		}
		var body struct {
			RoleID string `json:"roleId"`
		}
		if err := req.Bind(&body); err != nil {
			return nil, codedErrorf(http.StatusBadRequest, CodeValidationFailed, "リクエストを読めません")
		}
		if err := validateID("roleId", body.RoleID); err != nil {
			return nil, err
		}
		cfg, found, err := svc.store.LoadConfig(req.Context(), body.RoleID)
		if err != nil {
			return nil, svc.storageError(req.Context(), "level 設定の読み込みに失敗しました", err)
		}
		if !found {
			return nil, codedErrorf(http.StatusNotFound, CodeConfigNotFound,
				"この role には level 設定がありません")
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
			BaseLevel       int64         `json:"baseLevel"`
			ExperienceCurve []Curve       `json:"experienceCurve"`
			PolicyRanges    []PolicyRange `json:"policyRanges"`
			Revision        int64         `json:"revision"`
			Note            string        `json:"note"`
		}
		if err := req.Bind(&body); err != nil {
			return nil, codedErrorf(http.StatusBadRequest, CodeValidationFailed, "リクエストを読めません")
		}
		if err := validateID("roleId", body.RoleID); err != nil {
			return nil, err
		}
		// **manual role だけ。** conditional には assignment が無いので XP の紐付け先が
		// 決まらない。
		if err := svc.RequireConfigAdmin(req.Context(), body.RoleID); err != nil {
			return nil, err
		}
		cfg := Config{
			RoleID:          body.RoleID,
			BaseLevel:       body.BaseLevel,
			ExperienceCurve: body.ExperienceCurve,
			PolicyRanges:    body.PolicyRanges,
			UpdatedBy:       req.UserID(),
		}
		if err := cfg.Validate(defaultCatalog); err != nil {
			return nil, statusError(err)
		}
		saved, err := svc.store.UpsertConfig(req.Context(), cfg, body.Revision)
		if err != nil {
			return nil, svc.storageError(req.Context(), "level 設定の保存に失敗しました", err)
		}
		// **commit の後に cache を落とす。** curve を変えても他のワーカーが古い curve の
		// policy を返し続けると、「保存したのに効かない」になる。
		svc.invalidateRoleConfig(req.Context(), body.RoleID)
		return map[string]any{"role": saved}, nil
	})

	router.POST("/admin/roles/delete", func(req plugin.Request) (any, error) {
		if err := requireAdmin(req); err != nil {
			return nil, err
		}
		var body struct {
			RoleID   string `json:"roleId"`
			Revision int64  `json:"revision"`
		}
		if err := req.Bind(&body); err != nil {
			return nil, codedErrorf(http.StatusBadRequest, CodeValidationFailed, "リクエストを読めません")
		}
		if err := validateID("roleId", body.RoleID); err != nil {
			return nil, err
		}
		deleted, err := svc.store.DeleteConfig(req.Context(), body.RoleID, body.Revision)
		if err != nil {
			return nil, svc.storageError(req.Context(), "level 設定の削除に失敗しました", err)
		}
		if !deleted {
			return nil, codedErrorf(http.StatusConflict, CodeConfigConflict,
				"level 設定は既に削除されているか、他の操作で更新されています")
		}
		svc.invalidateRoleConfig(req.Context(), body.RoleID)
		// **XP の行は残す。** orphan として保持期間まで残り、Task 11 の job が消す。
		return map[string]any{"roleId": body.RoleID, "deleted": true}, nil
	})

	router.POST("/admin/users/show", func(req plugin.Request) (any, error) {
		if err := requireModerator(req); err != nil {
			return nil, err
		}
		var body struct {
			UserID string `json:"userId"`
		}
		if err := req.Bind(&body); err != nil {
			return nil, codedErrorf(http.StatusBadRequest, CodeValidationFailed, "リクエストを読めません")
		}
		if err := validateID("userId", body.UserID); err != nil {
			return nil, err
		}
		return svc.adminUser(req.Context(), body.UserID)
	})

	router.POST("/admin/change-exp", func(req plugin.Request) (any, error) {
		var body struct {
			IdempotencyKey string  `json:"idempotencyKey"`
			UserID         string  `json:"userId"`
			RoleID         string  `json:"roleId"`
			Mode           string  `json:"mode"`
			Operand        float64 `json:"operand"`
			Note           string  `json:"note"`
		}
		if err := req.Bind(&body); err != nil {
			return nil, codedErrorf(http.StatusBadRequest, CodeValidationFailed, "リクエストを読めません")
		}
		if err := validateID("userId", body.UserID); err != nil {
			return nil, err
		}
		if err := validateID("roleId", body.RoleID); err != nil {
			return nil, err
		}
		// 権限は XP を書く前に落とす。**保存後の失敗より前で落とす方が安全**で、
		// 失敗した操作が監査に残らない。
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
			Operand:        body.Operand,
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
		if err := req.Bind(&body); err != nil {
			return nil, codedErrorf(http.StatusBadRequest, CodeValidationFailed, "リクエストを読めません")
		}
		return svc.auditTrail(req.Context(), body.RoleID, body.UserID, body.Limit)
	})

	router.POST("/admin/orphans", func(req plugin.Request) (any, error) {
		if err := requireModerator(req); err != nil {
			return nil, err
		}
		return svc.OrphanReport(req.Context())
	})

	router.POST("/admin/reconcile", func(req plugin.Request) (any, error) {
		if err := requireAdmin(req); err != nil {
			return nil, err
		}
		var body struct {
			Mode string `json:"mode"`
		}
		if err := req.Bind(&body); err != nil {
			return nil, codedErrorf(http.StatusBadRequest, CodeValidationFailed, "リクエストを読めません")
		}
		mode := body.Mode
		if mode == "" {
			// 空なら全部。**どれか1つだけ走ることにする理由がない** — 管理画面の
			// 「今すぐ直す」ボタンは1回で全部終わらせたい。
			mode = reconcileAllMode
		}
		result, err := svc.RunReconcile(req.Context(), mode)
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
		if err := req.Bind(&body); err != nil {
			return nil, codedErrorf(http.StatusBadRequest, CodeValidationFailed, "リクエストを読めません")
		}
		if err := validateID("roleId", body.RoleID); err != nil {
			return nil, err
		}
		limit, offset := pageBounds(pageRequest{Limit: body.Limit, Offset: body.Offset})
		return svc.roleMembers(req.Context(), body.RoleID, limit, offset)
	})

	router.POST("/users/show", func(req plugin.Request) (any, error) {
		var body struct {
			UserID string `json:"userId"`
		}
		if err := req.Bind(&body); err != nil {
			return nil, codedErrorf(http.StatusBadRequest, CodeValidationFailed, "リクエストを読めません")
		}
		if err := validateID("userId", body.UserID); err != nil {
			return nil, err
		}
		return svc.publicProfile(req.Context(), body.UserID)
	})

	return nil
}

// memberCount returns how many assignments a role has, up to the scan cap.
//
// **件数を正確にしない。** 走査はページ上限があり、それを超えたら truncated を
// 立てて「これ以上ある可能性」を返す。1 を追うだけの表示に全走査を払うのは
// 割に合わない。
func (s *service) memberCount(ctx context.Context, roleID string) (int, bool, error) {
	native, err := s.native()
	if err != nil {
		// actorId 未設定でも読み取りは成立させたいので 0 + 上限超過を返す。
		return 0, true, nil //nolint:nilerr // 設定不足は件数制限として扱う
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
// **古い行を復活させない。** unassign 後に同じ role を再付与すると行が2本になる。
// 1本ならそれを live な assignment とみなせるが、2本以上では native から現在の
// assignment ID を取り直さないと区別できない。解決できないときは XP 0 (行が無いのと
// 同じ) を返し、古い値は公開しない。
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
		return false, 0, "", s.storageError(ctx, "経験値の読み込みに失敗しました", err)
	}
	switch len(rows) {
	case 0:
		// XP 0 は「行が無い」状態で表せるので、assignment を表現する必要がない。
		return true, 0, "", nil
	case 1:
		// 1本ならそれを live な assignment とみなせる。XP が無いので experience は 0。
		return true, rows[0].Experience, rows[0].AssignmentID, nil
	default:
		// 2本以上 = 古い行が残っている。live な 1 本だけを使う。
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
// is actually assigned to.
//
// **未付与の role は返さない。** 返すと private な level role の存在まで漏れる。
// 付与の証跡は native 側 (assignment-show) で確認するので、plugin table の値だけを
// 信用しない。
func (s *service) publicProfile(ctx context.Context, userID string) (any, error) {
	native, err := s.native()
	if err != nil {
		// actorId 未設定なら level を一切返さない。**XP を出さないことが安全側**で、
		// 設定していない運営者には起動を止めない (Task 1 と同じ方針)。
		return nil, err
	}
	configs, err := s.store.ListConfigs(ctx)
	if err != nil {
		return nil, s.storageError(ctx, "level 設定の読み込みに失敗しました", err)
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
		exp, err := cfg.Experience(xp)
		if err != nil {
			return nil, s.storageError(ctx, "level の計算に失敗しました", err)
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
func (s *service) roleMembers(ctx context.Context, roleID string, limit, offset int) (any, error) {
	native, err := s.native()
	if err != nil {
		return nil, err
	}
	cfg, found, err := s.store.LoadConfig(ctx, roleID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, codedErrorf(http.StatusNotFound, CodeConfigNotFound,
			"この role には level 設定がありません")
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
		return nil, s.storageError(ctx, "経験値の読み込みに失敗しました", err)
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
	// **XP 降順、同じ XP なら user id 昇順。** 順序が不定だと2回の取得で並びが
	// 変わって、frontend が入れ替わる。
	sort.Slice(members, func(i, j int) bool {
		if members[i].experience != members[j].experience {
			return members[i].experience > members[j].experience
		}
		return members[i].userID < members[j].userID
	})

	// ページは plugin table 側ではなく、並べ替えた後の member に掛ける。
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
			return nil, s.storageError(ctx, "level の計算に失敗しました", err)
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

// auditTrail returns the newest audit rows for a role and/or user.
func (s *service) auditTrail(ctx context.Context, roleID, userID string, limit int) (any, error) {
	entries, err := s.store.RecentAudit(ctx, roleID, userID, limit)
	if err != nil {
		return nil, s.storageError(ctx, "監査の読み込みに失敗しました", err)
	}
	return map[string]any{"entries": entries}, nil
}

// adminUser is the administrator view of one user's level data.
func (s *service) adminUser(ctx context.Context, userID string) (any, error) {
	native, err := s.native()
	if err != nil {
		return nil, err
	}
	configs, err := s.store.ListConfigs(ctx)
	if err != nil {
		return nil, s.storageError(ctx, "level 設定の読み込みに失敗しました", err)
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
			return nil, s.storageError(ctx, "level の計算に失敗しました", err)
		}
		roles = append(roles, map[string]any{
			"roleId":       cfg.RoleID,
			"assignmentId": assignmentID,
			"experience":   xp,
			"level":        exp,
		})
	}
	audit, err := s.store.RecentAudit(ctx, "", userID, 20)
	if err != nil {
		return nil, s.storageError(ctx, "監査の読み込みに失敗しました", err)
	}
	ops, err := s.store.ResumableOperations(ctx, 20)
	if err != nil {
		return nil, s.storageError(ctx, "XP 操作の読み込みに失敗しました", err)
	}
	open := make([]map[string]any, 0, len(ops))
	for _, op := range ops {
		if op.UserID == userID {
			open = append(open, map[string]any{
				"idempotencyKey": op.IdempotencyKey,
				"roleId":         op.RoleID,
				"mode":           op.Mode,
				"operand":        op.Operand,
				"status":         op.Status,
				"lastError":      op.LastError,
			})
		}
	}
	return map[string]any{
		"userId":     userID,
		"roles":      roles,
		"audit":      audit,
		"operations": open,
	}, nil
}
```

`routes.go` の import は次の4つ:

```go
import (
	"context"
	"net/http"
	"sort"

	"github.com/shiroha-a/mk/plugin"
)
```

- [ ] **Step 4: `plugin.go` の `routes` スタブを削除する**

`plugins/rolelevel/plugin.go` の Task 1 由来の

```go
// routes registers the plugin's HTTP endpoints. The route table is added in
// Task 10; this task only proves the module starts, the schema is created and the
// configuration is validated.
func routes(pctx plugin.Context, router plugin.Router) error {
	_, err := newService(pctx)
	return err
}
```

を削除する。`jobs` のスタブは Task 11 まで残す。`plugin.go` の `plugin` import は
`plugin.Definition` が使っているためそのままにする (削除しない)。

- [ ] **Step 5: reconciliation handler のスタブを置き、Task 10 の GREEN を確認する**

`/admin/orphans` と `/admin/reconcile` は Task 11 の実装を呼ぶ。**Task 10 の
`routes.go` を compile 可能にするため**、この2つだけスタブを置く:

```go
// OrphanReport is implemented in Task 11 (jobs.go). The stub keeps Task 10
// compilable so the route table can be tested on its own.
func (s *service) OrphanReport(ctx context.Context) (map[string]any, error) {
	return nil, codedErrorf(http.StatusNotImplemented, CodeValidationFailed,
		"orphan 集計は Task 11 で実装します")
}

// RunReconcile is implemented in Task 11 (jobs.go). The stub keeps Task 10
// compilable so the route table can be tested on its own.
func (s *service) RunReconcile(ctx context.Context, mode string) (map[string]any, error) {
	return nil, codedErrorf(http.StatusNotImplemented, CodeValidationFailed,
		"reconciliation は Task 11 で実装します (mode=%s)", mode)
}
```

**11 本の route は Task 10 で全部登録する。** Task 11 は handler の**中身を**
差し替えるだけで、route の追加・削除・メソッド変更はしない。11 本あることは
`TestAllRoutesArePost` が固定する。

```powershell
$env:GOWORK = "off"
go -C plugins/rolelevel test ./... -count=1 -run "TestAllRoutesArePost|TestAdminConfigRoutes|TestAdminRolesList|TestAdminConfig|TestChangeExpRoute|TestPublicProfile|TestRoleMembers|TestAdminAuditRoute|TestAdminOrphansRoute|TestAdminReconcileRoute"
```

Expected: `TestAdminOrphansRoute` と `TestAdminReconcileRoute` 以外が全て PASS。
この2つは Task 10 では `501` (`ROLE_LEVEL_VALIDATION_FAILED`) で失敗するのが正しい —
stub のままなので。**この2つの GREEN は Task 11 で行う。**

```powershell
$env:GOWORK = "off"
go -C plugins/rolelevel test ./... -count=1
go -C plugins/rolelevel vet ./...
gofmt -l plugins/rolelevel
```

Expected: `TestAdminOrphansRoute` / `TestAdminReconcileRoute` 以外が PASS。
`gofmt -l` が出力しないこと。

- [ ] **Step 6: commit する**

```powershell
git add plugins/rolelevel/routes.go plugins/rolelevel/routes_test.go plugins/rolelevel/plugin.go
git commit -m "Phase roleLevel: add the administrator and public routes"
```

---

### Task 11: reconciliation job と監査

**Files:**
- Modify: `plugins/rolelevel/plugin.go` (Task 1 の `jobs` スタブを本実装に置き換える)
- Create: `plugins/rolelevel/jobs.go`
- Create: `plugins/rolelevel/jobs_test.go`

**Interfaces:**
- Consumes: Task 1 の `service` / `newService` / `config.Cron*`, Task 5 の `store.ResumableOperations` / `ExperienceRowsForRole` / `DeleteOrphanExperience`, Task 6 の `nativeRole.ListAssignments`, Task 8 の `resume` / `Status*`
- Produces:
  - `const maxResumeBatch = 200`
  - `func (s *service) ResumePendingOperations(ctx context.Context) error`
  - `func (s *service) OrphanReport(ctx context.Context) (map[string]any, error)`
  - `func (s *service) PruneOrphans(ctx context.Context) (int64, error)`
  - `func (s *service) liveAssignmentIDs(ctx context.Context) (map[string]bool, error)`
  - `func (s *service) RunReconcile(ctx context.Context, mode string) (map[string]any, error)` — `POST /admin/reconcile` が Task 10 で呼んでいる**スタブを本実装に置き換える**。route の追加はしない
  - `const reconcileModeResume / reconcileModeOrphans / reconcileModePrune` — `POST /admin/reconcile` が受ける `mode` の値。Task 10 の `reconcileAllMode` と合わせて whitelist になる

- [ ] **Step 1: RED — job の契約を固定する**

`plugins/rolelevel/jobs_test.go`:

```go
package rolelevel

import (
	"context"
	"testing"

	"github.com/shiroha-a/mk/plugin/plugintest"
)

// **3つの job が cron に登録されている。** 1つでも欠けると、止まった操作が
// 永遠に再開されず、orphan が永遠に消えない。
func TestJobsAreScheduled(t *testing.T) {
	jobs := plugintest.New(t).WithName("role-level").WithDB(testDB(t)).
		WithConfig(map[string]any{"actorId": "admin1"}).
		WithAPI(&stubAPI{}).Jobs(Plugin)

	want := map[string]string{
		"resume-operations": "*/10 * * * *",
		"reconcile-orphans": "17 3 * * *",
		"prune-orphans":     "43 4 * * *",
	}
	if len(jobs.Schedules) != len(want) {
		t.Fatalf("schedule = %d 件, want %d (%+v)", len(jobs.Schedules), len(want), jobs.Schedules)
	}
	for _, s := range jobs.Schedules {
		if got, ok := want[s.Name]; !ok {
			t.Errorf("未知の job %q が登録されています", s.Name)
		} else if got != s.Cron {
			t.Errorf("%s の cron = %q, want %q", s.Name, s.Cron, got)
		}
	}
}

// **保留中の operation は同じ key で再開される。** 1 周で全部終わらせる。
func TestResumePendingOperations(t *testing.T) {
	api := &stubAPI{
		roles:       map[string]roleInfo{"r1": {ID: "r1", Target: "manual"}},
		assignments: map[string][]assignment{"r1": {mkAssignment("asg1", "u1")}},
	}
	db := testDB(t)
	h := plugintest.New(t).WithName("role-level").WithDB(db).
		WithConfig(map[string]any{"actorId": "admin1"}).WithAPI(api)
	svc, err := newService(h.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"k1", "k2"} {
		if _, err := svc.store.InsertOperation(context.Background(), operation{
			IdempotencyKey: key, ActorID: "admin1", UserID: "u1", RoleID: "r1",
			Mode: string(ModeAdd), Operand: 10, Status: string(StatusPending),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.ResumePendingOperations(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"k1", "k2"} {
		op, found, err := svc.store.LoadOperation(context.Background(), key)
		if err != nil || !found {
			t.Fatalf("%s: found = %t %v", key, found, err)
		}
		if op.Status != string(StatusCompleted) {
			t.Fatalf("%s の status = %q, want completed", key, op.Status)
		}
	}
	// 2 回目は何もしない (冪等)。
	if err := svc.ResumePendingOperations(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows, err := svc.store.ExperienceRowsForRoleUser(context.Background(), "r1", "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Experience != 20 {
		t.Fatalf("XP = %+v, want 1行 / 20 (2周目の二重適用がない)", rows)
	}
}

// **failed は自動再開しない。** 原因を追って運営者が直すもので、勝手に直すと
// operator の判断を飛ばす。
func TestResumePendingOperationsSkipsFailed(t *testing.T) {
	api := &stubAPI{
		roles:       map[string]roleInfo{"r1": {ID: "r1", Target: "manual"}},
		assignments: map[string][]assignment{"r1": {mkAssignment("asg1", "u1")}},
	}
	db := testDB(t)
	h := plugintest.New(t).WithName("role-level").WithDB(db).
		WithConfig(map[string]any{"actorId": "admin1"}).WithAPI(api)
	svc, err := newService(h.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.store.InsertOperation(context.Background(), operation{
		IdempotencyKey: "k1", ActorID: "admin1", UserID: "u1", RoleID: "r1",
		Mode: string(ModeAdd), Operand: 10, Status: string(StatusFailed), LastError: "boom",
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.ResumePendingOperations(context.Background()); err != nil {
		t.Fatal(err)
	}
	op, _, err := svc.store.LoadOperation(context.Background(), "k1")
	if err != nil {
		t.Fatal(err)
	}
	if op.Status != string(StatusFailed) {
		t.Fatalf("status = %q, want failed", op.Status)
	}
}

// **orphan 判定は native の member 一覧と突き合わせる。** XP の行だけを見ると
// 「ある role の user か」が分からない。
func TestOrphanReport(t *testing.T) {
	api := &stubAPI{
		roles: map[string]roleInfo{"r1": {ID: "r1", Target: "manual"}},
		assignments: map[string][]assignment{
			"r1": {mkAssignment("asg1", "u1")},
		},
	}
	db := testDB(t)
	h := plugintest.New(t).WithName("role-level").WithDB(db).
		WithConfig(map[string]any{"actorId": "admin1"}).WithAPI(api)
	svc, err := newService(h.Context())
	if err != nil {
		t.Fatal(err)
	}
	seedXP(t, svc, "asg1", "r1", "u1", 100)
	seedXP(t, svc, "asg-gone", "r1", "u9", 900)

	report, err := svc.OrphanReport(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, ok := report["roles"].(map[string]any)
	if !ok {
		t.Fatalf("report = %+v", report)
	}
	entry, ok := raw["r1"].(orphanRoleReport)
	if !ok {
		t.Fatalf("r1 = %T, want orphanRoleReport", raw["r1"])
	}
	if entry.Orphans != 1 {
		t.Fatalf("orphans = %d, want 1", entry.Orphans)
	}
	if len(entry.OrphanAssignmentIDs) != 1 || entry.OrphanAssignmentIDs[0] != "asg-gone" {
		t.Fatalf("orphan ids = %+v", entry.OrphanAssignmentIDs)
	}
}

// **保持期間内の orphan は消さない。** 監査のために残すという要件をここが保証する。
// 保持期間を短くしすぎると「消したはずの XP の履歴」が消える。
func TestPruneOrphansRespectsRetention(t *testing.T) {
	api := &stubAPI{
		roles: map[string]roleInfo{"r1": {ID: "r1", Target: "manual"}},
		assignments: map[string][]assignment{
			"r1": {mkAssignment("asg1", "u1")},
		},
	}
	db := testDB(t)
	h := plugintest.New(t).WithName("role-level").WithDB(db).
		WithConfig(map[string]any{"actorId": "admin1", "orphanRetentionDays": 30}).WithAPI(api)
	svc, err := newService(h.Context())
	if err != nil {
		t.Fatal(err)
	}
	seedXP(t, svc, "asg1", "r1", "u1", 100)
	seedXP(t, svc, "asg-old", "r1", "u9", 900)
	if _, err := db.Exec(`UPDATE role_level_experience
		SET updated_at = now() - interval '90 days' WHERE assignment_id = 'asg-old'`); err != nil {
		t.Fatal(err)
	}

	n, err := svc.PruneOrphans(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("削除 = %d, want 1", n)
	}
	rows, err := svc.store.ExperienceRowsForRoleUser(context.Background(), "r1", "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("XP 行 = %+v", rows)
	}
}

// **native API が失敗したら何も消さない。** 障害中に orphan 判定르면全 XP が
// 消える。
func TestPruneOrphansKeepsDataOnNativeFailure(t *testing.T) {
	api := &stubAPI{
		roles:   map[string]roleInfo{"r1": {ID: "r1", Target: "manual"}},
		showErr: 500,
	}
	db := testDB(t)
	h := plugintest.New(t).WithName("role-level").WithDB(db).
		WithConfig(map[string]any{"actorId": "admin1"}).WithAPI(api)
	svc, err := newService(h.Context())
	if err != nil {
		t.Fatal(err)
	}
	seedXP(t, svc, "asg-old", "r1", "u9", 900)
	if _, err := db.Exec(`UPDATE role_level_experience
		SET updated_at = now() - interval '90 days'`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PruneOrphans(context.Background()); err == nil {
		t.Fatal("native 失敗でエラーになりません")
	}
	rows, err := svc.store.ExperienceRowsForRole(ctx, "r1", 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("XP 行が消えています: %+v", rows)
	}
}

// **`POST /admin/reconcile` は job と同じ関数を呼ぶ。** route と cron で別の実装に
// すると、「手動では直るが自動では直らない」ような食い違いが残る。
func TestRunReconcileDispatchesEveryMode(t *testing.T) {
	api := &stubAPI{
		roles:       map[string]roleInfo{"r1": {ID: "r1", Target: "manual"}},
		assignments: map[string][]assignment{"r1": {mkAssignment("asg1", "u1")}},
	}
	db := testDB(t)
	h := plugintest.New(t).WithName("role-level").WithDB(db).
		WithConfig(map[string]any{"actorId": "admin1"}).WithAPI(api)
	svc, err := newService(h.Context())
	if err != nil {
		t.Fatal(err)
	}
	// 停止した operation と、保持期間を超えた orphan の行を1つずつ用意する。
	if _, err := svc.store.InsertOperation(context.Background(), operation{
		IdempotencyKey: "k1", ActorID: "admin1", UserID: "u1", RoleID: "r1",
		Mode: string(ModeAdd), Operand: 10, Status: string(StatusPending),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.store.UpsertConfig(context.Background(), Config{
		RoleID:          "r1",
		BaseLevel:       1,
		ExperienceCurve: []Curve{{Type: CurveConst, LevelUps: 9, Base: 10}},
		PolicyRanges:    []PolicyRange{{Type: RangeBase, Start: 1, End: 10}},
	}, 0); err != nil {
		t.Fatal(err)
	}
	seedXP(t, svc, "asg-old", "r1", "u9", 900)
	if _, err := db.Exec(`UPDATE role_level_experience
		SET updated_at = now() - interval '90 days' WHERE assignment_id = 'asg-old'`); err != nil {
		t.Fatal(err)
	}

	// 1 mode ずつ。全部 "all" を1回呼んでも同じ結果になる。
	for _, mode := range []string{
		reconcileModeResume, reconcileModeOrphans, reconcileModePrune, reconcileAllMode,
	} {
		result, err := svc.RunReconcile(context.Background(), mode)
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		if result == nil {
			t.Fatalf("%s: result が空です", mode)
		}
	}
	// unknown mode は 400。**typo を黙って無視しない** — 運営者は「直した」と
	// 思ってしまう。
	if _, err := svc.RunReconcile(context.Background(), "nope"); err == nil {
		t.Fatal("未知の mode を受理しています")
	} else if _, code := extractCode(err); code != CodeValidationFailed {
		t.Fatalf("code = %q, want %s (%v)", code, CodeValidationFailed, err)
	}
}
```


**`TestOrphanReport` が `orphanRoleReport` 型を要求している**ので、Step 3 でその型を定義する。

- [ ] **Step 2: RED を確認する**

```powershell
$env:GOWORK = "off"
go -C plugins/rolelevel test ./... -count=1 -run "TestJobsAreScheduled|TestResumePendingOperations|TestOrphanReport|TestPruneOrphans"
```

Expected: FAIL — `TestJobsAreScheduled` が `schedule = 0 件, want 3` で落ちる (Task 1 の `jobs` は何も登録しない)。残りも `undefined: ResumePendingOperations` / `undefined: OrphanReport` / `undefined: PruneOrphans` / `undefined: RunReconcile`。

- [ ] **Step 3: `jobs.go` を実装する**

`plugins/rolelevel/jobs.go`:

```go
package rolelevel

import (
	"context"
	"encoding/json"
	"sort"
	"time"
)

// maxResumeBatch bounds one reconciliation pass.
//
// **1 周の所要時間に上限を掛ける。** ジョブには1時間の上限 (#2658) があるので、
// 大量に残っていても1周で終わらせ、残りは次回に回す。
const maxResumeBatch = 200

// orphanRoleReport is one role's orphan summary.
type orphanRoleReport struct {
	// Tracked is how many XP rows the role has.
	Tracked int `json:"tracked"`
	// Orphans is how many of them have no live native assignment.
	Orphans int `json:"orphans"`
	// OrphanAssignmentIDs lets the administrator screen see exactly which rows are
	// waiting for the retention period to pass.
	OrphanAssignmentIDs []string `json:"orphanAssignmentIds"`
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
		n, err := svc.PruneOrphans(ctx)
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
		return s.storageError(ctx, "XP 操作の読み込みに失敗しました", err)
	}
	var firstErr error
	resumed := 0
	for _, op := range ops {
		if _, err := s.resume(ctx, op, ""); err != nil {
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
// **level 設定のある role だけを見る。** level の無い role の XP 行は本来無いので、
// 走査する価値がない (native の呼び出し回数だけ増える)。
func (s *service) liveAssignmentIDs(ctx context.Context) (map[string]bool, error) {
	native, err := s.native()
	if err != nil {
		return nil, err
	}
	configs, err := s.store.ListConfigs(ctx)
	if err != nil {
		return nil, s.storageError(ctx, "level 設定の読み込みに失敗しました", err)
	}
	live := map[string]bool{}
	for _, cfg := range configs {
		all, _, err := native.ListAssignments(ctx, cfg.RoleID)
		if err != nil {
			// **1つでも失敗したら全体を失敗にする。** 一部だけ返すと、見なかった
			// role の XP 行を全部 orphan と誤判定して消してしまう。
			return nil, err
		}
		for _, a := range all {
			live[a.ID] = true
		}
	}
	return live, nil
}

// OrphanReport counts the XP rows whose native assignment is gone, per role.
//
// **削除はしない。** orphan 判定と削除を分けるのは、判定が1回失敗したときに
// まとめて消えるのを避けるため。
func (s *service) OrphanReport(ctx context.Context) (map[string]any, error) {
	live, err := s.liveAssignmentIDs(ctx)
	if err != nil {
		return nil, err
	}
	configs, err := s.store.ListConfigs(ctx)
	if err != nil {
		return nil, s.storageError(ctx, "level 設定の読み込みに失敗しました", err)
	}
	report := make(map[string]orphanRoleReport, len(configs))
	for _, cfg := range configs {
		rows, err := s.store.ExperienceRowsForRole(ctx, cfg.RoleID, 1000, 0)
		if err != nil {
			return nil, s.storageError(ctx, "経験値の読み込みに失敗しました", err)
		}
		entry := orphanRoleReport{Tracked: len(rows), OrphanAssignmentIDs: []string{}}
		for _, r := range rows {
			if !live[r.AssignmentID] {
				entry.Orphans++
				entry.OrphanAssignmentIDs = append(entry.OrphanAssignmentIDs, r.AssignmentID)
			}
		}
		sort.Strings(entry.OrphanAssignmentIDs)
		report[cfg.RoleID] = entry
	}
	return map[string]any{"roles": report}, nil
}

// PruneOrphans deletes XP rows that have been orphans longer than the retention.
//
// **保持期間内の行は残す。** 監査のために残すのが目的で、native API の障害で一度に
// 消えると operator の記録が失われる。
func (s *service) PruneOrphans(ctx context.Context) (int64, error) {
	live, err := s.liveAssignmentIDs(ctx)
	if err != nil {
		return 0, err
	}
	configs, err := s.store.ListConfigs(ctx)
	if err != nil {
		return 0, s.storageError(ctx, "level 設定の読み込みに失敗しました", err)
	}
	cutoff := time.Now().Add(-time.Duration(s.cfg.OrphanRetentionDays) * 24 * time.Hour)
	var total int64
	for _, cfg := range configs {
		rows, err := s.store.ExperienceRowsForRole(ctx, cfg.RoleID, 1000, 0)
		if err != nil {
			return total, s.storageError(ctx, "経験値の読み込みに失敗しました", err)
		}
		orphans := make([]string, 0, len(rows))
		for _, r := range rows {
			if !live[r.AssignmentID] {
				orphans = append(orphans, r.AssignmentID)
			}
		}
		n, err := s.store.DeleteOrphanExperience(ctx, cfg.RoleID, orphans, cutoff)
		if err != nil {
			return total, s.storageError(ctx, "orphan の削除に失敗しました", err)
		}
		total += n
	}
	return total, nil
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
func (s *service) RunReconcile(ctx context.Context, mode string) (map[string]any, error) {
	run := map[string]func(context.Context) (any, error){
		reconcileModeResume: func(ctx context.Context) (any, error) {
			return nil, s.ResumePendingOperations(ctx)
		},
		reconcileModeOrphans: func(ctx context.Context) (any, error) {
			return s.OrphanReport(ctx)
		},
		reconcileModePrune: func(ctx context.Context) (any, error) {
			n, err := s.PruneOrphans(ctx)
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
			// 思って画面を閉じる。
			return nil, codedErrorf(http.StatusBadRequest, CodeValidationFailed,
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
```

`jobs.go` の import は `context` / `encoding/json` / `net/http` / `sort` / `time` の5つ:

```go
import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/shiroha-a/mk/plugin"
)
```

**Task 10 の `routes.go` に置いた2つのスタブを削除する** (Step 4)。

- [ ] **Step 4: `plugin.go` の `jobs` スタブと、`routes.go` の Task 10 スタブを削除する**

`plugins/rolelevel/plugin.go` の Task 1 由来の

```go
// jobs registers the background work. The handlers are added in Task 11.
func jobs(pctx plugin.Context, j plugin.Jobs) error {
	_, err := newService(pctx)
	return err
}
```

を削除する。`routes` は Task 10 で削除済みなので、Step 3 の `jobs` が唯一の定義になる。

`plugins/rolelevel/routes.go` の **末尾にある Task 10 の2つのスタブも削除する** —
Step 3 の `jobs.go` が本実装として同じメソッドを定義する。残すと
`RunReconcile redeclared` でコンパイルが壊れる:

```go
// OrphanReport is implemented in Task 11 (jobs.go). ...
func (s *service) OrphanReport(ctx context.Context) (map[string]any, error) { ... }

// RunReconcile is implemented in Task 11 (jobs.go). ...
func (s *service) RunReconcile(ctx context.Context, mode string) (map[string]any, error) { ... }
```

**`reconcileAllMode` 定数と 11 本の route 登録は `routes.go` に残す。** Task 11 は
ハンドラの中身を差し替えるだけで、route table には触らない。

- [ ] **Step 5: GREEN を確認する**

```powershell
$env:GOWORK = "off"
go -C plugins/rolelevel test ./... -count=1 -run "TestJobsAreScheduled|TestResumePendingOperations|TestOrphanReport|TestPruneOrphans|TestRunReconcile"
go -C plugins/rolelevel test ./... -count=1
go -C plugins/rolelevel vet ./...
gofmt -l plugins/rolelevel
```

Expected: 全て PASS、`gofmt -l` が出力しないこと。**Task 10 では 501 で落ちていた
`TestAdminOrphansRoute` と `TestAdminReconcileRoute` もここで初めて通る。**

- [ ] **Step 6: commit する**

```powershell
git add plugins/rolelevel/jobs.go plugins/rolelevel/jobs_test.go plugins/rolelevel/plugin.go
git commit -m "Phase roleLevel: reconcile stopped operations and orphan rows"
```

---

### Task 12: docs・config example・build / CI 登録

**Files:**
- Create: `plugins/rolelevel/README.md`
- Modify: `Makefile:334-361` (`plugin-vet` / `plugin-test` の allowlist) と `Makefile:43` (`gates` へ gate target を追加)
- Modify: `.github/workflows/ci.yml:149-169` (`Check bundled plugins are disabled by default` に allowlist)
- Modify: `.config/default.yml.example:583-588` (role-level の設定例)
- Modify: `docs/plugins/README.md:24`
- Modify: `docs/plugins/operating.md:166-174` (同梱プラグインの既定)
- Modify: `docs/plugins/compatibility.md:101-117` (同梱一覧と gate の一覧)
- Modify: `docs/ci.md:20` (`build` job の説明)
- Modify: `CLAUDE.md:151` (`make gates` の一覧) と `CLAUDE.md:518-523` (disabled by default の説明)

**Interfaces:**
- Consumes: Task 1〜11 が作った plugin の全機能
- Produces: `make plugin-vet` / CI の `Check bundled plugins are disabled by default` が `role-level` を「意図的に既定有効」として許容する

- [ ] **Step 1: RED — allowlist を持たない今の gate が失敗することを確かめる**

```powershell
git add -A plugins/rolelevel .gitignore
git ls-files 'plugins/*/mk-plugin.yml'
make plugin-vet
```

Expected: FAIL — `FAIL plugins/rolelevel/mk-plugin.yml — 'disabled: true' の行 (完全一致) がありません。` 同様に CI の `Check bundled plugins are disabled by default` step も落ちる。**これが「既定有効の同梱プラグインを素朴に足すと CI が壊れる」ことの証明**で、以降の Step 2 と Step 3 がそれを直す。

- [ ] **Step 2: `Makefile` の `plugin-vet` に allowlist を入れる**

`Makefile` の `plugin-vet` target の先頭に、次の変数を追加する (`target` の上是じ):

```make
# **意図的に既定有効で同梱するプラグインの名前。** 空なら従来どおり全部
# `disabled: true` を要求する (検査を緩めたのではなく、意図を明示しただけ)。
#
# **CI の `Check bundled plugins are disabled by default` にも同じ一覧を置く。**
# 2箇所に書くのは、CI step を `make plugin-vet` に置き換えると「列挙が git ではなく
# ディレクトリを走査する pluginbuild に依存する」壊れ方を持ち込むため (#2701 のコメント
# と同じ判断)。**片方だけ直すと CI と手元で結果が変わる**ので、必ず両方触る。
BUNDLED_PLUGINS_ENABLED_BY_DEFAULT = rolelevel
```

`plugin-vet` の判定ループを次のように置き換える:

```make
	@set -e; \
	markers=$$(git ls-files 'plugins/*/mk-plugin.yml'); \
	if [ -z "$$markers" ]; then echo "同梱プラグインの mk-plugin.yml が見つかりません (列挙が壊れています)"; exit 1; fi; \
	enabled_by_default=" $(BUNDLED_PLUGINS_ENABLED_BY_DEFAULT) "; \
	fail=0; \
	for f in $$markers; do \
		name=$$(basename "$$(dirname "$$f")"); \
		case "$$enabled_by_default" in \
		*" $$name "*) echo "ok   $$f (意図的に既定有効: $$name)"; continue;; \
		esac; \
		if grep -qE '^disabled:[[:space:]]*true[[:space:]]*$$' "$$f"; then echo "ok   $$f"; \
		else echo "FAIL $$f — 'disabled: true' の行 (完全一致) がありません。disabled を含む行:"; \
			grep -n disabled "$$f" || echo "  (無し)"; fail=1; fi; \
	done; \
	[ "$$fail" -eq 0 ]; \
	mods=$$(git ls-files 'plugins/*/go.mod'); \
	...
```

- [ ] **Step 3: CI の `Check bundled plugins are disabled by default` に allowlist を入れる**

`.github/workflows/ci.yml` の該当 step の loop を次のように置き換える:

```yaml
      - name: Check bundled plugins are disabled by default
        run: |
          files=$(git ls-files 'plugins/*/mk-plugin.yml')
          # **空でも緑にしない。** `plugins/` の移動や marker 名の変更で列挙が
          # 空振りすると、検査していないのに通ってしまう (plugin-tests job の
          # 同型ステップと同じ guard)。
          if [ -z "$files" ]; then
            echo "同梱プラグインの mk-plugin.yml が 1 つも見つかりません (列挙が壊れています)"
            exit 1
          fi
          # **意図的に既定有効で同梱するプラグイン (#12 の role-level)。**
          # 検査を緩めたのではなく、意図を allowlist に書いただけ。判定は
          # 行ベースのままなので、allowlist への追加も同じ行一致で行う。
          enabled_by_default=" rolelevel "
          fail=0
          for f in $files; do
            name=$(basename "$(dirname "$f")")
            case "$enabled_by_default" in
            *" $name "*) echo "ok   $f (意図的に既定有効: $name)"; continue;;
            esac
            if grep -qE '^disabled:[[:space:]]*true[[:space:]]*$' "$f"; then
              echo "ok   $f"
            else
              echo "FAIL $f — 'disabled: true' の行 (完全一致) がありません。disabled を含む行:"
              grep -n disabled "$f" || echo "  (無し)"
              fail=1
            fi
          done
          [ "$fail" -eq 0 ] || exit 1
```

step の上のコメントブロックに、次の1段落を追加する:

```yaml
      # **ただし `rolelevel` だけは意図的に既定有効** (#12)。Misaki の bundled image で
      # level 機能がそのまま入るのはそれが目的なので、`disabled: true` を要求しない。
      # allowlist に入れるだけで、判定の基準は緩めていない — 判定は
      # 行ベースの完全一致のまま。
```


- [ ] **Step 4: `plugins/rolelevel/README.md` を書く**

``````markdown
# role-level

Misaki の level 機能を mk-go の素の role の上に被せる bundled plugin。

## 何をするか

mk-go 標準の **manual role** に、level・XP curve・level 別 policy を付ける。role の
本体・assignment・期限・moderator/administrator 属性は mk-go 標準のまま使うので、
mk-go の schema には何も追加しない。

| 概念 | 持ち場所 |
|---|---|
| role / assignment / 期限 | mk-go 標準 (`role`, `role_assignment`) |
| level 設定・curve・policy range | `role_level_config` |
| assignment の XP | `role_level_experience` (主キーは `assignment_id`) |
| XP 変更の操作記録 | `role_level_operation` (主キーは `idempotency_key`) |
| 監査 | `role_level_audit` |

`plugin_role_level` schema にだけ書き込む。**core table への foreign key は張らない。**

## level の決まり方

```text
minimumLevel      = baseLevel
maximumLevel      = baseLevel + sum(level-up counts)
effectiveLevel    = baseLevel + 完了した level-up 数
progressionStage  = effectiveLevel - baseLevel + 1
```

`progressionStage` は 1 始まりで、level 別 policy range の判定に使う。`baseLevel` は
負数も 0 も取れる。

既定は `baseLevel = 1` / `const 100 XP × 99 level-ups` / `effective level 1..100`。
curve を空にすると `baseLevel` 固定になる。

XP curve の型は `const` / `linear` / `exponential`。level-up コストは
`base` / `base + additional*n` / `base + additional*exponential^n` (`n` は rule 内 0 始まり)。
**コストは実数のまま保持し、level ごとに切り上げない。** 次の level へのしきい値は
小数になりうるので、整数 XP はその `ceil` で到達する。

curve は rule の列として評価する。**rule をまたぐ offset は `float64` で持ち越す**
(前の rule の累積が 2.5 なら次の rule は 2.5 から積算する)。累積和は閉形式で、
rule 内は binary search するので、level-up 数が大きくても O(S log L) 時間・O(1) メモリで
求まる。**level-up 数の上限は設けない。**

保存時に次を検証する。1 level あたりのコストは有限かつ 0 より大きいこと、累積和は有限かつ
`9007199254740991` 以下であること。コストは 1 次式か単調な指数関数なので、最小と最大は両端
(`n = 0` と `n = levelUps-1`) にある — 両端だけを見れば全 level が収まることが示せる。

XP は JSON number / Go safe integer で、`0..9007199254740991`。`role_level_experience`
の `experience` は `bigint` に range `CHECK` を付ける。

## 設定

`.config/default.yml` の `plugins.role-level`:

```yaml
plugins:
  role-level:
    enabled: true
    actorId: <管理者ユーザーの ID>
    assignmentScanPages: 50
    orphanRetentionDays: 30
    reconcileCron: "*/10 * * * *"
    orphanCron: "17 3 * * *"
    pruneCron: "43 4 * * *"
```

| キー | 既定 | 意味 |
|---|---|---|
| `actorId` | (空) | native API を呼ぶ管理者。**空でも起動する**が、XP 変更と自動付与は `ROLE_LEVEL_ACTOR_NOT_CONFIGURED` を返す |
| `assignmentScanPages` | 50 | (role, user) → assignment ID を解決、走査する `admin/roles/users` のページ数上限 (1 ページ = 100) |
| `orphanRetentionDays` | 30 | XP 行が orphan のまま保たれる日数。**これより短いと監査に足らない** |
| `reconcileCron` | `*/10 * * * *` | 停止した XP 操作を再開する job の周期 |
| `orphanCron` | `17 3 * * *` | orphan を数える job の周期 (**削除はしない**) |
| `pruneCron` | `43 4 * * *` | 保持期間を超えた orphan の XP 行を消す job の周期 |

`actorId` は `admin/server-plugins` の設定キー一覧にキー名だけ出る (値はマスクされる)。

## API

先頭は `/api/plugin/role-level`。**11本すべて `POST` で、path parameter も query string も
無く、body でパラメータを受ける。** `plugin.Router` には `PUT` / `DELETE` が無いので、
設定の変更・削除も `POST` で行う。

| path | 権限 | body | 応答 |
|---|---|---|---|
| `/admin/roles/list` | moderator | `{}` | `{"roles":[…], "memberCounts":{…}}` |
| `/admin/roles/show` | moderator | `{"roleId"}` | `{"role":…, "memberCount":int, "membersTruncated":bool}` |
| `/admin/roles/update` | administrator | `{roleId, baseLevel, experienceCurve, policyRanges, revision, note?}` | `{"role":…}` |
| `/admin/roles/delete` | administrator | `{roleId, revision}` | `{"roleId":…, "deleted":true}` |
| `/admin/users/show` | moderator | `{"userId"}` | `{"userId","roles":[…],"audit":[…],"operations":[…]}` |
| `/admin/change-exp` | moderator (role による) | `{idempotencyKey, userId, roleId, mode, operand, note?}` | `{"assignmentId","experience","status","resumed"}` |
| `/roles/users` | 公開 | `{roleId, limit?, offset?}` | `{"roleId","total","truncated","members":[…]}` |
| `/users/show` | 公開 | `{"userId"}` | `{"userId","roles":[…]}` |
| `/admin/audit` | moderator | `{roleId?, userId?, limit?}` | `{"entries":[…]}` |
| `/admin/orphans` | moderator | `{}` | `{"roles":{<roleId>:{tracked, orphans, orphanAssignmentIds}}}` |
| `/admin/reconcile` | administrator | `{"mode"?: …}` | `{"mode":…, "result":{"steps":[…]}}` |

**native の `admin/roles/assign` / `admin/roles/unassign` はこの表に無い。** これらは plugin
が内部で呼ぶ native API call で、plugin の route ではない。

エラーは `plugin.NewCodedStatusError` の code を持つ。`ROLE_LEVEL_` で始まる英大文字
stable code で、frontend はこれで分岐する。

`POST /admin/reconcile` の `mode` は `resume-operations` / `reconcile-orphans` /
`prune-orphans` / `all`。省略は `all` と同じ。**未知の mode は 400** で、黙って無視しない。

`POST /admin/change-exp` の body:

```json
{
  "idempotencyKey": "…",
  "userId": "…",
  "roleId": "…",
  "mode": "set | add | multiplier",
  "operand": 50.5,
  "note": "任意"
}
```

`idempotencyKey` は **必須**。同じ値を再送すると 2 回目は保存済みの結果を返すだけで
XP は増えない。

`operand` は**有限の小数**を受け付ける。DB は `role_level_operation.operand` を
`double precision` で持つ。`mode` 別の意味:

- `set` — operand をそのまま XP にする
- `add` — 現在の XP に加える
- `multiplier` — **raw factor**。`1.5` は ×1.5 であって百分率ではない

計算は `float64` で行い、結果を `floor` してから `0..9007199254740991` に収める。
`role_level_operation` の `desired_exp` は `bigint` のまま。

`POST /users/show` は **未付与の role を返さない**。付与の証跡は native 側で確かめる
ので、plugin table の値だけを信用しない。

## 無効化

```yaml
plugins:
  role-level:
    enabled: false
```

**再ビルド不要。** プラグインが無効でも native role と assignment は有効なまま残り、
level 別 policy の寄与だけが消えて native policy に戻る。`plugin_role_level` schema は
**自動で消えない** (不要なら `DROP SCHEMA "plugin_role_level" CASCADE` を手で実行する)。

このとき**利用者の権限が変わる**ので、無効化する前に `/admin/server-plugins` で
`effectivePolicies` を確認する (docs/plugins/operating.md の「TS へ切り戻した場合」)。

## 監査と reconciliation

- `role_level_audit` に XP 変更の before / after が入る。
- `resume-operations` (既定 10 分ごと) は `pending` / `assigning` / `applying` で
  止まった操作を**同じ idempotency key** で再開する。`completed` と `failed` は
  触らない (`failed` は原因を追って運営者が直すもの)。
- `reconcile-orphans` (1 日 1 回) は XP 行と native の member 一覧を突き合わせて
  orphan を数える。**削除はしない。** `POST /admin/orphans` も同じ集計を返す。
- `prune-orphans` (1 日 1 回) は保持期間を超えた orphan の XP 行を消す。**native API が
  失敗したときは何も消さない。**
- `POST /admin/reconcile` は上記を `mode` で選んで**その場で**走らせる。cron と同じ関数を
  呼ぶだけなので、手動と自動の経路が食い違わない。

## 既知の制約

- **curve の level-up 数に上限は無い。** 閉形式 + rule 内 binary search なので
  O(S log L) 時間・O(1) メモリで求まる。制限が効くのは**累積和**のほうで、
  保存時に有限かつ `9007199254740991` 以下であることを検証する。
- **member 一覧は 1 role あたり `assignmentScanPages × 100` 件まで。** 超えると
  `membersTruncated` / `truncated` が立つ。
- **(role, user) → assignment ID の解決は `admin/roles/users` のページ走査。** XP を
  書き込む操作 (管理者操作) だけに行う。policy 解決経路は native を呼ばない。
- **frontend plugin は本リポジトリに含まれない。** `Misaki-Project/misskey-ts` 側で
  実装する。
```

**README 内の日本語を1つ直すこと** — `actorId` の段落の「設定キー一覧にキー名だけ出る」は次の文に置き換える:

```
`actorId` は `admin/server-plugins` の設定キー一覧に**キー名だけ**出る (値はマスクされる)。
``````

- [ ] **Step 5: `docs/plugins/*` と `docs/ci.md` を更新する**

`docs/plugins/operating.md` の「### 同梱プラグインの既定」節を、次のように置き換える:

```markdown
### 同梱プラグインの既定

同梱しているのは `plugins/status/` `plugins/trustlevel/` `plugins/rolelevel/` の3つ。

**status と trustlevel は既定無効。** 動かしたい場合は該当する `mk-plugin.yml` から
`disabled: true` の行を消して再ビルドする。

**rolelevel は既定有効** (#12)。Misaki の bundled image で level 機能がそのまま入る
ことが目的なので、`disabled: true` を書いていない。止めたいときは設定ファイルで
`plugins.role-level.enabled: false` にする (再ビルド不要)。

既定無効なのは、同梱プラグインが**ビルドに含まれているだけで有効になる**ため。
`plugin_wiring.go` は Routes/Jobs の登録より先に専用 schema を開いて migration を
適用するので、`role-level` だけは設定していなくても `plugin_role_level` schema と
テーブルができる。schema を開けない環境では起動そのものが失敗する。

**この既定は `build` job の `Check bundled plugins are disabled by default` と
`make plugin-vet` が見ている** (#2701)。**ただし allowlist に載せたプラグイン
(`Makefile` の `BUNDLED_PLUGINS_ENABLED_BY_DEFAULT` と CI step の
`enabled_by_default`) は `disabled: true` を要求しない。** 判定の基準自体は
緩めておらず、行ベースの完全一致のまま。allowlist は「意図的に既定有効」の宣言で
あって、検査の除外ではない。
```

`docs/plugins/compatibility.md` の「## サンプルプラグイン」節の該当2文を、次のように置き換える:

```markdown
`plugins/status/` `plugins/trustlevel/` `plugins/rolelevel/` を**リポジトリに同梱**してある。

別リポジトリに置くと、`plugin/` を変えたときに壊れても CI で気付けない。同梱していれば公開面を壊した時点で CI が落ちる。**サンプルの一番の価値は「常に動くこと」**。

`status` と `trustlevel` は `mk-plugin.yml` で既定無効なので、clone して `make build` してもバイナリにもフロントにも入らない。**`rolelevel` だけは既定有効** (#12) — Misaki の bundled image で level 機能がそのまま入ることが目的。`build` job の `Check bundled plugins are disabled by default` と `make plugin-vet` は、allowlist (`BUNDLED_PLUGINS_ENABLED_BY_DEFAULT` / CI step の `enabled_by_default`) に載ったプラグインについては `disabled: true` を要求しない。

`plugins/*` は gitignore されているが、`!plugins/status/` `!plugins/trustlevel/` `!plugins/rolelevel/` で例外指定してある。
```

`docs/plugins/README.md:24` の「**既定では無効**（`mk-plugin.yml`の`disabled: true`）」を含む段落の末尾に、次を追加する:

```markdown

`plugins/rolelevel/` も同梱している (#12)。これは**既定で有効** — 実行するサンプルではなく、Misaki の level 機能そのものだから。停止は設定ファイルの `plugins.role-level.enabled: false` で行う。
```

`docs/ci.md:20` の `build` 行の「同梱サンプルが既定無効」を、次のように置き換える:

```
同梱プラグインの `go vet` + 同梱サンプルが既定無効 (`rolelevel` だけは allowlist で意図的に既定有効)
```

`CLAUDE.md:518-523` の「`build`ジョブ」節の該当説明に、次の1文を追加する:

```markdown
- **`rolelevel` だけは allowlist で意図的に既定有効** (#12)。`disabled: true` を要求しないのは
  仕様で、判定の基準は緩めていない (`Makefile` の `BUNDLED_PLUGINS_ENABLED_BY_DEFAULT`
  と CI step の `enabled_by_default` の2箇所に同じ一覧を置く)。
```

- [ ] **Step 6: `.config/default.yml.example` に設定例を追加する**

`.config/default.yml.example:583-588` の `plugins:` 例の直後に追加:

```yaml
#
# role-level (Misaki の level 機能) は既定で有効。止めたいときだけ enabled: false を
# 書く。actorId は native API を呼ぶ管理者で、未設定でも起動するが XP 変更と自動付与は
# 使えない (read 系の経路は動く)。
#  role-level:
#    enabled: true
#    actorId: "8bnr1gh9m2"
#    assignmentScanPages: 50
#    orphanRetentionDays: 30
#    reconcileCron: "*/10 * * * *"
#    orphanCron: "17 3 * * *"
#    pruneCron: "43 4 * * *"
```

**コメントの `#  role-level:` は `#  role-level:` に直す** (先頭の空白が2つある)。

- [ ] **Step 7: 全ての gate を通す**

```powershell
make plugin-vet
make plugin-test
gofmt -l plugins/rolelevel
git diff --check
git status --short
```

Expected:

- `make plugin-vet` が `ok plugins/rolelevel/mk-plugin.yml (意図的に既定有効: rolelevel)` を出して通る
- `make plugin-test` が `==> plugins/rolelevel` のあと全 plugin で PASS
- `gofmt -l` が空
- `git status --short` に **migration/ が無い**こと

- [ ] **Step 8: commit する**

```powershell
git add Makefile .github/workflows/ci.yml .config/default.yml.example CLAUDE.md docs/plugins/README.md docs/plugins/operating.md docs/plugins/compatibility.md docs/ci.md plugins/rolelevel/README.md
git commit -m "Phase roleLevel: register the plugin in build, CI and docs"
```

---

### Task 13: issue #12 の追跡と最終検証

**Files:**
- No repository file changes (GitHub 上の issue / PR だけ)

**Interfaces:**
- Consumes: Task 1〜12 の全成果物
- Produces: `Misaki-Project/mk` の issue #12 への進捗コメントと PR

- [ ] **Step 1: 最終検証を1通り回す**

```powershell
$env:GOWORK = "off"
go -C plugins/rolelevel test ./... -count=1 -run "TestMigrations|TestLoadConfig|TestCatalog|TestDefaultConfig|TestNegativeAndZero|TestEmptyCurve|TestCurveTypes|TestLargeLevelUp|TestCurveValidation|TestExperienceRejects|TestValidateRanges|TestRangeValue|TestRangeForStage|TestStore|TestNative|TestRequire|TestAuthorize|TestApplyMode|TestValidateMode|TestValidateIdempotencyKey|TestClampExperience|TestChangeExp|TestResolveReplacements|TestEffectivePolicies|TestAdminConfig|TestChangeExpRoute|TestPublicProfile|TestRoleMembers|TestJobsAreScheduled|TestResumePending|TestOrphanReport|TestPruneOrphans"
go -C plugins/rolelevel test ./... -count=1
go -C plugins/rolelevel vet ./...
gofmt -l plugins/rolelevel
Remove-Item Env:\GOWORK
go test ./internal/entitycompat/... -run "TestRoleLevelCatalog" -count=1
make plugin-vet
make plugin-test
```

Expected: 全て PASS / 出力なし。

- [ ] **Step 2: 本体の gate に新規 regression が無いことを確認する**

```powershell
go build ./...
go vet ./...
git diff --check
git status --short
```

Expected: 出力なし。`git status --short` に `migration/` の追加が無いこと、既存bundled plugin (`plugins/status/` `plugins/trustlevel/`) の変更が無いこと、他の plan file (`docs/superpowers/plans/2026-09-25-can-delete-account.md`) の変更が無いこと。

**`go test ./...` はこの plan の範囲外。** 既存の失敗 (PostgreSQL 未構成、plugin surface golden drift、Windows固有test) があるため、上の `go build` / `go vet` / 対象の `go test` で新規 regression を判定する。

- [ ] **Step 3: 統合バイナリに組み込まれることを確認する**

```powershell
make plugins
go build -o "$env:TEMP\mk-role-level-check.exe" ./cmd/misskey
Select-String -Path "cmd/misskey/plugins_generated.go" -Pattern "mk-plugin-rolelevel"
Remove-Item "$env:TEMP\mk-role-level-check.exe"
```

Expected:

- `pluginbuild` の出力に `dir=plugins/rolelevel name=role-level (github.com/shiroha-a/mk-plugin-rolelevel, frontend=false)` が出る
- `go build` が通る (**統合バイナリのコンパイルを見るのは `build` job の `Vet bundled plugins` には無いので、ここで確認する**)
- `cmd/misskey/plugins_generated.go` に `mk-plugin-rolelevel` が含まれる

`cmd/misskey/plugins_generated.go` と `go.work` は gitignore 済みなので **commit しない**。`git status --short` に現れないことを確認する。

- [ ] **Step 4: issue #12 に進捗を追記する**

```powershell
gh issue comment 12 --repo Misaki-Project/mk --body "## 実装状況 (feature/role-level-plugin)`n`n- [x] 汎用 Plugin API の ActiveRoleAssignment / ActiveAssignments / ReplaceRoleID (別 PR で landed 済み)`n- [x] plugins/rolelevel: plugin-owned 4 table (config / experience / operation / audit)`n- [x] level domain (curve 3 種 / 小数しきい値 / 閉形式 + binary search / validation)`n- [x] policy range の半開区間検証と multiplier offset の是正`n- [x] XP operation 状態機械 (set / add / multiplier / idempotency / 自動付与 / 再開)`n- [x] administrator / public route 11 本 (すべて POST) と stable error code`n- [x] 監査と reconciliation job (resume-operations / reconcile-orphans / prune-orphans) + 手動実行の POST /admin/reconcile`n- [x] commit 後の cross-worker cache invalidation`n- [x] PostgreSQL 統合テスト`n- [x] docs / config example / build & CI 登録`n`n## 制約と範囲`n- mk-go core schema には何も追加していない (table / column / enum なし)`n- frontend plugin は本 PR の対象外 (Misaki-Project/misskey-ts 側)`n- 旧 CherryPick からの移行 SQL は全機能実装後にローカル script として作る`n`n## curve の扱い`n- level-up コストは実数のまま保持し、level ごとに切り上げない。整数 XP は小数しきい値の ceil で到達する`n- rule をまたぐ offset は float64 で持ち越し、累積和は閉形式で求める (rule 内は binary search、O(S log L) 時間・O(1) メモリ)`n- exponential は e == 1 を専用処理し、それ以外は Log1p / Expm1 の安定式を使う`n- 保存時に「1 level あたりのコストが有限かつ正」「累積和が有限かつ 9007199254740991 以下」を検証する。level-up 数そのものに上限は無い"
```

Expected: issue #12 にコメントが追記される。

- [ ] **Step 5: PR を作る**

```powershell
git push -u origin feature/role-level-plugin
gh pr create --repo Misaki-Project/mk --base develop --head feature/role-level-plugin `
  --title "Phase roleLevel: bundled role-level plugin を追加する" `
  --body "## Summary`n`nMisaki の level / XP / level 別 policy 機能を、mk-go の core schema を変えずに bundled plugin (plugins/rolelevel) として実装する。level 設定・curve・XP・監査はすべて plugin-owned schema (plugin_role_level) にあり、native role と assignment は mk-go 標準のまま使う。`n`n## 主な変更点`n`n- plugins/rolelevel: 独立した Go module (manifest name は role-level、既定で有効)`n- plugin-owned 4 table: role_level_config / role_level_experience / role_level_operation / role_level_audit`n- 純関数の level domain: const / linear / exponential curve、safe-integer validation、段階計算 (小数しきい値対応)`n- policy range: progressionStage 上の半開区間、重複・欠落なしを保存時に強制`n- XP 状態機械: set / add / multiplier、operand は有限の小数 (multiplier は raw factor)、idempotency key 必須、未付与 user は自動付与してから XP を保存`n- route: /api/plugin/role-level に 11 本の POST だけ (path / query は使わず body で受ける)。native の admin/roles/assign と unassign は plugin 内部の native call で、route ではない`n- 監査: before/after を保存し、reconciliation job が保留中の操作を同じ key で再開する。POST /admin/reconcile が同じ関数をその場で呼べる`n- config / XP 更新の commit 後に cross-worker policy cache を invalidate する`n- build / CI: 同梱プラグインの disabled-by-default gate に意図的な allowlist を追加 (rolelevel だけ)`n`n## curve の扱い`n`n- level-up コストは実数のまま保持し、level ごとに切り上げない。整数 XP は小数しきい値の ceil で到達する`n- rule をまたぐ offset は float64 で持ち越し、累積和は閉形式で求める (rule 内は binary search、O(S log L) 時間・O(1) メモリ)`n- exponential は e == 1 を専用処理し、それ以外は Log1p / Expm1 の安定式を使う`n- 保存時に「1 level あたりのコストが有限かつ正」「累積和が有限かつ 9007199254740991 以下」を検証する。level-up 数そのものに上限は無い`n`n## テスト`n`n- \`make plugin-test\` (PostgreSQL 実 DB。CI の plugin-tests job と同じ条件)`n- \`go test ./internal/entitycompat/... -run TestRoleLevelCatalog\` (native policy catalog の drift gate)`n- \`make plugin-vet\``n- \`make plugins && go build ./cmd/misskey\` (統合バイナリへの組み込み確認)`n`n## 注意点`n`n- **mk-go の schema は変更していない。** migration/ への追加はない`n- 汎用の Plugin API 拡張 (ActiveRoleAssignment / ActiveAssignments / ReplaceRoleID) は別 issue / PR で先に landed されている前提`n- frontend plugin は対象外。admin:role-editor / admin:user / profile:info slot は Misaki-Project/misskey-ts 側で実装する`n`nCloses #12"
```

Expected: `https://github.com/Misaki-Project/mk/pull/...` の URL が出る。

- [ ] **Step 6: CI が緑であることを確認する**

```powershell
gh pr checks --repo Misaki-Project/mk --watch
```

Expected: `build` / `test` / `lint` が成功する。`plugin-tests` が失敗する場合は、PostgreSQL 接続ではなく **plugin test 自体**の問題なので、上の Step 1 のコマンドで再現して直す。

---

## Self-Review

### 1. Spec coverage

| spec の節 | 担当 task |
|---|---|
| Architecture (core schema を変えない / native manual role) | Task 1, 2, 5, 6 |
| Generic Plugin API Extensions (依存、含めない) | Global Constraints, Task 1 Step 0, Task 9 |
| Plugin Storage (4 table) | Task 1 |
| Level Model (baseLevel / stage / default / curve / validation) | Task 3 |
| Level-Based Policies (range / rule / multiplier offset) | Task 4, Task 9 |
| XP Mutation (mode / 順序 / 自動付与 / 再開) | Task 6, Task 8 |
| Authorization | Task 7, Task 10 |
| Plugin Routes (+ stable code) | Task 1, Task 10 |
| Failure Semantics (fallback / storage failure) | Task 9, Task 8 |
| Testing (18 項目) | Task 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 13 |
| Data Migration Boundary (含めない) | Global Constraints |
| Operational Notes | Task 11, Task 12 |
| Acceptance Criteria | Task 1, 12, 13 |
| Frontend (含めない) | Global Constraints |
| Issue #12 | Task 13 |

### 2. Placeholder scan

TBD / TODO / 「後で書く」型の記述は無い。型・signature・SQL・RED/GREEN コマンド・
commit メッセージはすべて plan 内に書いてある。

### 3. Type consistency

- `Mode` / `Status` は Task 8 が定義し、Task 5 の `store_test.go` が **先に**参照している
  (`TestStoreOperationLifecycle` が `ModeAdd` / `StatusPending` を使う)。**Task 5 の RED は
  `undefined: ModeAdd` でも意図どおり** (Task 5 の Expected に書いてある)。
- `operation` / `experienceRow` / `auditEntry` は Task 5 が定義し、Task 8 / Task 9 /
  Task 10 / Task 11 が使う。
- `Status` は Task 5 の `store_test.go` が `StatusAssigning` / `StatusCompleted` を使い、
  Task 8 が `type Status` を定義する。Task 5 の Step 2 の Expected に
  `undefined: StatusPending` がある。
- `auditEntry.Note` と `resume(ctx, op, note)` の note は Task 8 で整合してある
  (operation テーブルに note 列は無く、audit にだけ入る)。
- `orphanRoleReport` は Task 11 が定義し、`jobs_test.go` の Step 1 が先に参照する
  (Step 2 の Expected に `undefined: orphanRoleReport` 相当の失敗が出る)。
- `rangeValue` は `(any, error)` を返す。`base` は **nil** を返し、resolver だけが
  `UseDefault: true` の contribution を作る。Task 4 の Interfaces・Step 1 のテスト・
  Task 9 の Interfaces・`validateRanges` の `multiplier` ケースの4箇所が同じ signature。
- `operation.Operand` は `float64`、SQL は `double precision`、route の body も
  `float64`。`ChangeExpRequest.Operand` も同じ型。`desired_exp` / `experience` は
  `int64` のまま。
- `OrphanReport` と `RunReconcile` は Task 10 でスタブ、Task 11 で本実装に置き換える。
  Task 11 の Step 4 にスタブ削除の指示がある。`reconcileAllMode` は Task 10 の
  `routes.go`、`reconcileMode*` は Task 11 の `jobs.go` — **11 本の route 登録は
  Task 10 だけで**、`TestAllRoutesArePost` が固定する。
- Task 10 の `routes.go` は `nativeRole.Usernames` (Task 6) / `FindAssignment` (Task 6) /
  `ListAssignments` (Task 6) と、`store.ExperienceRowsForRoleUser` /
  `ExperienceForAssignments` / `RecentAudit` / `ResumableOperations` (Task 5) を使う。
  すべて Task 6 / Task 5 が定義してから Task 10 が消費する。

### 4. 承認済み仕様との一致 (確認済み)

- exponential curve に level-up 数の上限が無いこと。`MaxExponentialLevelUps` 相当の
  識別子は plan 内に存在しない。
- XP は JSON number / Go safe integer、`0..9007199254740991`。`role_level_experience`
  の `experience` は `bigint` + range `CHECK`。
- operation の `operand` は有限の小数を許す (`double precision`)。整数限定も
  `1,000,000` 上限も無い。`multiplier` は raw factor (`1.5` = ×1.5)。
- `base` policy rule は `UseDefault: true` + `ReplaceRoleID` の置換 contribution を返す。
- plugin route は 11 本すべて `POST`。native `admin/roles/assign` / `admin/roles/unassign`
  は route ではなく plugin 内部の native API call。
- directory は `plugins/rolelevel`、manifest name は `role-level`。
- **spec からの逸脱を主張していない。** Global Constraints は「設計上の判断」であり、
  deviation ではない。

### 5. 既知の残作業 (この plan の外)

- frontend plugin (`Misaki-Project/misskey-ts`) と `admin:role-editor` / `admin:user` /
  `profile:info` slot の利用側
- 旧 CherryPick からの移行 SQL (全機能実装後、ローカル script のみ)
- 汎用の Plugin API 拡張自体 (別 issue / PR)。upstream への PR は spec の
  「Delivery Boundaries」2〜4 として別の issue で進行する






