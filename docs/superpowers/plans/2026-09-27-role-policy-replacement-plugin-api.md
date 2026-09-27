# Role Policy Replacement Plugin API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `shiroha-a/mk` へ PR 出せる汎用Plugin APIを足す — active manual assignment の文脈と、1つのactive manual roleのnative policy contributionだけを1対1で置換する境界 — を、Misaki固有のlevel計算なしで公開面に載せる。

**Architecture:** 公開型は `plugin/` への**追加だけ**（`ActiveRoleAssignment`、`EffectivePolicyRequest.ActiveAssignments`、`EffectivePolicyContribution.ReplaceRoleID`）。hostは native ロール解決を `userRoleSnapshot{roles, activeAssignments}` に一本化し、**1 回の `ListByUser` 読取**から両方を出して既存のper-user cache entry に一緒に積む。`resolvePolicies` はその内部スナップショットを直接使い、repository の失敗は握り潰さず provider を起動する前に checked error として返す。provider の成功結果 cache key には assignment ID を含める。置換は「provider結果 → `rolePolicyInput`（ロールごとの集約entry）」の段で、置換entryの `priority` を**元のnative entryのpriority**にして差し替える。集約のpriority cascade・aggregator・instance/server cap・管理者判定は一切変えない。競合は (role, key) 単位でnativeへ戻し、checked resolver だけが固定sentinelを返す。

**Tech Stack:** Go 1.27.1、Echo、testify、`internal/effectivepolicy`（native policy schema）、`internal/pluginspec`（公開面golden）、`plugin/plugintest`、`gh` CLI、Python 3（`tests/plugin-doc/extract.py`）

## Global Constraints

- 対象は**upstream可能な汎用Plugin API変更だけ**。Misaki固有のroleLevel計算・XP・storage・route・UI・frontendをこの計画に混ぜない（別計画の担当）。
- **全taskでREDを先に観察する。** 最小の test / gate を先に書いて実行し、期待どおりの RED を見た後に限り production / docs を書き、GREEN を確認する。実装前から緑になる test は **regression guard** としてその旨を明示し、RED と称さない。
- 破壊的変更はゼロ。`plugin.APIVersion` は `1` のまま維持する（`docs/plugins/compatibility.md` の additive 契約に従う）。
- `RoleIDs` の意味・並び・重複除去・非nil空sliceという既存契約は**変えない**。`ActiveAssignments` は `RoleIDs` を置き換えず、並んで渡す。
- **roles と activeAssignments は必ず 1 回の `ListByUser` 読取から同時に作る。** `GetUserRoles` を呼んでから別の accessor で assignment を読む、という2段構えにしない。repository の読み損ねは**握り潰さず** checked error にする（空 slice に落とさない）。
- `ActiveAssignments` には**active な手動assignmentだけ**を入れる。conditional role・期限切れ・削除済み・`role` 行が無いorphanは入れない。よって `ActiveAssignments` の role ID は常に `RoleIDs` の部分集合になる。
- `ActiveAssignments` は**1つのactive manual roleにつき高々1件**（同じroleのassignmentが複数あればassignment IDの最小値1件だけ残す）。1対1置換が成立するために必要なhost側の保証。
- 置換は**active manual roleだけ**を対象にできる。conditional role にはassignment IDが無く `ActiveAssignments` に現れないので、置換先としては指定できない。
- replacement contribution は `Priority == 0` かつ `Order == 0` でなければならない。置換entryは元のnative priorityを**引き継ぐ**ので、pluginが自分で選ぶと二重定義になる。0 以外は malformed として provider 全体を失敗扱いにする。
- 置換の集約結果は他のrole contribution・providerの通常contribution・instance / server capと同じ規則で処理する。`explicit` は置換contributionの `UseDefault` に従う。`administrator` / `moderator` 判定は対象外のまま。
- provider失敗 / panic / timeout / malformed output の既存挙動は変えない。失敗providerは**宣言したkeyをnative値へ戻す**（他providerの置換も巻き戻る）。provider全体失敗とpair競合は別の固定sentinelで返す。
- providerごとの成功結果cache keyには**assignment ID を含める**。同じ `RoleIDs` でもassignmentが違えば結果は同じとは限らないnamespaceだから。
- **core DB migration を追加しない。** `role` / `role_assignment` に列・enum・indexを増やさず、既存schemaのreadだけで完結させる。
- 既存provider（`plugins/trustlevel`）は `RoleIDs` しか読まないため無変更で動く。`ReplaceRoleID` 未設定は追加contributionのまま。
- 変更は `Misaki-Project/mk` の `feature/role-level-plugin` で進め、upstream PR用のbranchを**別worktree**に作る（`upstream/develop` 起点）。`upstream/develop` へ直接pushしない。
- upstream PR には Misaki 固有の差分（`canDeleteAccount` policy、`applyMetaBasePolicies` のerror化、`joinBasePolicyError`）を**入れない**。Task 6 のleak検査で機械的に確かめる。
- **upstream の HEAD をハードコードしない。** `git fetch upstream develop` 後に `git rev-parse upstream/develop` を取り、その値を以降の Step と記録に使う。
- この作業ツリーの**full suiteは元から落ちている**。新規regressionの判定は下記のfocused commandだけで行う。

### Baseline failures（変更前から起きているもの。**修正しない**）

`go test ./... -count=1` は以下のpackageがFAILする。いずれもWindows作業ツリー（`core.autocrlf=true`、PostgreSQL未構成、`internal/core/serverstats` がLinux専用、unix socket系がWindows非対応）由来で、この計画の変更とは無関係。

- `FAIL ... [build failed]`: `cmd/misskey`, `internal/api/admin`, `internal/api/meta`, `internal/core/serverstats`, `internal/server`, `test/e2e`, `test/e2e_federation`, `tools/plugindev`
  （`internal/core/serverstats/stats.go` が `syscall.Statfs` を使うため。`internal/server` はその依存で**Windowsでtestすら動かない**ので、この計画では `internal/server` のtestを gate にしない。）
- `FAIL`（PostgreSQL未接続）: `internal/pluginstore`, `internal/repository`, `internal/core/{chat,drive,emojiimport,ephemeral,fsck,mediaproxy,procstats,selfcheck,signupapplication,timeline}`, `internal/api/{avatardecorations,gallery,hashtags,notifications,signup,stats}`, `internal/config`, `internal/maintenance`, `internal/queue/driver/mkqdriver`, `internal/testutil`, `tools/pluginbuild`, `plugin/plugintest`（`TestWithDB_*` 3件のみ）
- `FAIL`（Windows固有）: `internal/api/signin`（`TestSigninWithPasskey_RejectsMalformedContext` ほか）
- `FAIL`（`make` が無い）: `internal/entitycompat` の `TestGateRunPatternsResolve`
- `FAIL`（**行末差**）: `internal/entitycompat` の `TestPluginSurfaceDrift`
  - 原因は**`git config core.autocrlf=true` でcheckoutされたgoldenがCRLF**のこと。goldenの中身は完全に一致している（`Compare-Object` の差分は0）。`go run ./tools/pluginspec -write` はLFで書き直すので**書き直した直後は緑になる**が、次のcheckoutでまた赤に戻る。判定は下の正規化比較で行うこと。
- `FAIL`: `internal/entitycompat` の `TestCriticalWiringCountMatchesTable` — `internal/server` 側の `const criticalWiringCount` を読むgateで、`internal/server` がWindowsでビルドできないため。今回の変更は wiring を増やさないので放置。

### Focused commands（これだけを gate にする）

```powershell
# 1. 公開面goldenのdiff（checkout状態不管で信頼できる）
go run ./tools/pluginspec > "$env:TEMP\rlp-surface.txt"
$golden = (Get-Content -Raw internal\entitycompat\testdata\golden_plugin_surface.txt) -replace "`r`n","`n"
$actual = (Get-Content -Raw "$env:TEMP\rlp-surface.txt") -replace "`r`n","`n"
if ($golden -ne $actual) { "SURFACE DRIFT"; Compare-Object ($golden -split "`n") ($actual -split "`n") } else { "SURFACE OK" }

# 2. authoring.md の公開面一覧がgoldenと一致するか（ローカルで緑に信用できる）
go test ./internal/entitycompat -run TestPluginDoc -count=1

# 3. 公開型とhost契約
go test ./plugin ./internal/effectivepolicy -count=1
go test ./internal/core/role -count=1

# 4. plugintest のprovider契約（DBテストを回避する）
go test ./plugin/plugintest -count=1 -run "TestEffectivePolic"

# 5. 静的検査
go vet ./plugin/... ./internal/effectivepolicy ./internal/core/role
$env:GOWORK="off"
Push-Location plugins/trustlevel; go vet ./...; Pop-Location
Push-Location plugins/status;     go vet ./...; Pop-Location
Remove-Item Env:GOWORK
```

`gofmt` は `make fmt` と同じ物を使う（PATHの `gofmt` は古いことがある）:

```powershell
& (Join-Path (go env GOROOT) "bin\gofmt.exe") -s -w internal\core\role\role_service.go
```

CIが真のgate（`make test` / `make lint` / `make golangci-lint` / `make plugin-doc-check` / `make plugin-vet`）。この作業ツリーには `bash` と `make` が無いので、`make plugin-doc-check` の判定を Windows で再現したものを下に置く。

### authoring.md の Go fence コンパイル gate（Windows で `make plugin-doc-check` 相当）

`PYTHONUTF8=1` を付けないと `extract.py` が cp932 で死ぬので必ず付ける。`go.mod` は here-string を使わず配列結合で組み立てている（入れ子 here-string を避ける）。

**判定の3点だけは `tests/plugin-doc/check-snippets.sh` と必ず一致させる:**

1. **fence ID は実際に抽出されたディレクトリから取る。** `extract.py` は `snippets/<id>_<variant>` という**variant 付き**ディレクトリを作る。`snippets/<id>/` のようなものは存在しないので、その形の検索は1件もHITしない。`1..40` の決め打ちも同じ理由で壊れる（fence 数が増えると取りこぼす）。
2. **noise を除いた行だけを見る。** `check-snippets.sh` は `undefined: [A-Za-z_][A-Za-z0-9_]*$` に一致する行を捨てている。`extract.py` の HEADER が宣言した `db` / `Plugin` / `time` などが無いのは**意図された**shape なので、これを数えると複数の fence を誤って NG にする（実測で何件になるかは Self-Review の実行記録を参照）。
3. **NG になるのは top/any/err の3 variant が**すべて**non-noise diagnostic を持つ fence だけ。** 1 variant でも生き残れば、その fence は合格（`check-snippets.sh` と同じ「3 個全部落ちたものだけ出す」規則）。

`go build` の出力は `Out-String` を通さない。コンソール幅で折り返されると path が行をまたいで壊れる。`ForEach-Object { $_.ToString() }` で1行ずつ要素として取り、`-match` を要素に適用する。

```powershell
$w = "$env:TEMP\rlp-docgate"
Remove-Item -Recurse -Force $w -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Path $w | Out-Null
$env:PYTHONUTF8 = "1"
python tests/plugin-doc/extract.py docs/plugins/authoring.md $w
Remove-Item Env:PYTHONUTF8
$testify = (Select-String -Path go.mod -Pattern '^\tgithub.com/stretchr/testify v' | Select-Object -First 1).Line.Trim()
$gov = (Select-String -Path go.mod -Pattern '^go ' | Select-Object -First 1).Line -replace '^go ', ''
$modLines = @(
  'module docsnipcheck',
  '',
  "go $gov",
  '',
  'require (',
  "`tgithub.com/shiroha-a/mk v0.0.0",
  "`t$testify",
  ')',
  '',
  "replace github.com/shiroha-a/mk => $((Get-Location).Path)",
  ''
)
Set-Content -Path "$w\go.mod" -Value ($modLines -join "`n") -Encoding utf8NoBOM
Copy-Item go.sum "$w\go.sum"
Push-Location $w
$env:GOFLAGS = "-mod=mod"
$env:GOWORK = "off"
$buildLog = @(& go build -gcflags=-e ./snippets/... 2>&1 | ForEach-Object { $_.ToString() })
Remove-Item Env:GOFLAGS
Remove-Item Env:GOWORK
Pop-Location

# variant 名の接尾辞を落として、実在する fence ID を列挙する。
$variants = @("top", "any", "err")
$fences = @(Get-ChildItem -Directory (Join-Path $w "snippets") | ForEach-Object {
  $_.Name -replace '_(top|any|err)$', ''
} | Sort-Object -Unique)

$bad = @()
foreach ($id in $fences) {
  $diagnosed = @()
  foreach ($v in $variants) {
    $pattern = "snippets[\\/]${id}_${v}[\\/]x\.go:\d+:\d+:"
    $hit = @($buildLog | Where-Object { $_ -match $pattern -and $_ -notmatch 'undefined: [A-Za-z_][A-Za-z0-9_]*$' })
    if ($hit.Count -gt 0) { $diagnosed += $v }
  }
  if ($diagnosed.Count -eq $variants.Count) { $bad += $id }
}

"fences: $($fences.Count)"
if ($bad.Count -eq 0) { "SNIPPET GATE OK" } else { "NG: all variants failed for " + ($bad -join ", ") }
```

Expected: `fences: <n>` の行に続けて `SNIPPET GATE OK`。`<n>` は fence 数なので、doc を触る前後で変わる — **数値は固定しない**。**このブロックは Task 5 Step 1 で一度 RED になることを確認してから使う。**

---

### Task 1: Active Role Assignment Context (Public Surface)

`EffectivePolicyRequest` に「どのactive manual roleに、どのassignment IDがあるか」を渡す。既存providerは `RoleIDs` だけを読むので挙動は変わらない。

**Files:**
- Test: `plugin/policy_test.go`（新規 test）
- Modify: `plugin/policy.go`（`ActiveRoleAssignment` を新規追加、`EffectivePolicyRequest` に `ActiveAssignments` を追加）
- Modify: `internal/entitycompat/testdata/golden_plugin_surface.txt`（`go run ./tools/pluginspec -write` で再生成。**派生物**）
- Modify: `docs/plugins/authoring.md`（Go公開面一覧）、`docs/plugins/compatibility.md`（additive 追記）

**Interfaces:**
- Consumes: なし（最初のtask）
- Produces: `plugin.ActiveRoleAssignment{RoleID string; AssignmentID string}`
- Produces: `plugin.EffectivePolicyRequest.ActiveAssignments []ActiveRoleAssignment`
- Preserves: `plugin.EffectivePolicyRequest.RoleIDs` の意味・並び（非nil空slice）

- [ ] **Step 1: 最小の失敗 test を先に書く**

`plugin/policy_test.go` に追記する:

```go
func TestEffectivePolicyRequest_CarriesActiveAssignmentsAlongsideRoleIDs(t *testing.T) {
	// plugin作者は RoleID と AssignmentID を別々に読む。片方だけを持つ型に
	// 戻ると「XP を role に紐づけられた」と誤って付け替えられる。
	assignment := ActiveRoleAssignment{RoleID: "r1", AssignmentID: "a1"}

	request := EffectivePolicyRequest{
		UserID:            "u1",
		RoleIDs:           []string{"r1", "r2"},
		ActiveAssignments: []ActiveRoleAssignment{assignment},
	}

	assert.Equal(t, "r1", request.ActiveAssignments[0].RoleID)
	assert.Equal(t, "a1", request.ActiveAssignments[0].AssignmentID)
	// ActiveAssignments は RoleIDs を置き換えず、並んで運ぶ。
	assert.Equal(t, []string{"r1", "r2"}, request.RoleIDs)
}
```

- [ ] **Step 2: RED を観察する**

```powershell
go test ./plugin -count=1 -run TestEffectivePolicyRequest_CarriesActiveAssignmentsAlongsideRoleIDs
```

Expected: **コンパイルエラー**。`undefined: ActiveRoleAssignment`。型がまだ無いので当然 red。

- [ ] **Step 3: production に公開型を追加する**

`plugin/policy.go` の `// EffectivePolicyRequest is the input to an effective policy resolver.` の**直前**に次の型を追加する:

```go
// ActiveRoleAssignment is one active manual role assignment paired with the
// role it grants.
//
// **conditional role は含まれない。** 条件つきロールには assignment row が
// 無いので、plugin が見えるのは手動で割り当てられた active なロールだけになる。
// [EffectivePolicyRequest.RoleIDs] は conditional を含むので、plugin は
// 「何の権限があるか」と「どの assignment に紐づくか」を別々に読む。
type ActiveRoleAssignment struct {
	// RoleID is the role the assignment grants. RoleIDs に必ず含まれる。
	RoleID string
	// AssignmentID is the native `role_assignment.id`。
	//
	// **不透明な ID として扱うこと。** plugin はこれを Plugin storage の行と
	// 対応させるだけで、内容を解釈しない。unassign → re-assign で別 ID に
	// なるので、assignment に紐づく状態は復活しない。
	AssignmentID string
}
```

続けて `EffectivePolicyRequest` の `RoleIDs []string` の**後**にフィールドを追加する:

```go
	// ActiveAssignments are the user's active manual role assignments, sorted
	// by RoleID. Each role appears at most once.
	//
	// - expired / deleted assignments are excluded
	// - conditional roles are never included (they have no assignment)
	// - anonymous requests receive a non-nil empty slice
	//
	// **RoleIDs を置き換えない。** 既存providerはこのsliceだけを見ている。
	// 追加contributionだけ返す実装には RoleIDs があれば十分。
	ActiveAssignments []ActiveRoleAssignment
```

- [ ] **Step 4: GREEN を確認する**

```powershell
& (Join-Path (go env GOROOT) "bin\gofmt.exe") -s -l plugin\policy.go plugin\policy_test.go
go test ./plugin -count=1
```

Expected: `gofmt -l` が無出力、`go test ./plugin` が PASS。

- [ ] **Step 5: golden を再生成して doc gate を RED にする**

golden は production から生成される**派生物**。先に golden を更新すると、doc 側の公開面一覧が古くなることが gate として見える:

```powershell
go run ./tools/pluginspec -write
git diff --stat internal/entitycompat/testdata/golden_plugin_surface.txt
go test ./internal/entitycompat -run TestPluginDoc -count=1
```

Expected: golden に4行が追加される（下の Expected 参照）。**`go test` は FAIL** する — `ActiveRoleAssignment` / `ActiveAssignments` が `docs/plugins/authoring.md` の公開面一覧に無いと報告される。

```
plugin:   field ActiveRoleAssignment.AssignmentID string
plugin:   field ActiveRoleAssignment.RoleID string
plugin:   field EffectivePolicyRequest.ActiveAssignments []ActiveRoleAssignment
plugin: type ActiveRoleAssignment struct
```

- [ ] **Step 6: docs を書いて GREEN にする**

`docs/plugins/authoring.md` の `### Go (github.com/shiroha-a/mk/plugin)` 節にある

```
type EffectivePolicyRequest struct
  UserID string
  RoleIDs []string
```

を、`type EffectivePolicyResolver func` の**直前**に `ActiveRoleAssignment` を置いて次の形にする:

```
type ActiveRoleAssignment struct
  RoleID string
  AssignmentID string

type EffectivePolicyRequest struct
  UserID string
  RoleIDs []string
  ActiveAssignments []ActiveRoleAssignment
```

**型は golden の文字列と一字一句同じに書くこと。** `TestPluginDoc_SurfaceFieldsMatchGolden` が `Type.Field 型` を逐語照合する。

`docs/plugins/compatibility.md` の「`Definition.EffectivePolicies`と関連型の追加はこのadditive契約に従い…」の段落の**後**に追記する:

```markdown
`EffectivePolicyRequest.ActiveAssignments` と `plugin.ActiveRoleAssignment` の追加も同じ扱い。**`RoleIDs` は変更されない**ので既存providerの挙動は変わらず、新sliceを無視する実装は害がない。`APIVersion`は1のまま。
```

- [ ] **Step 7: GREEN を確認する**

```powershell
go test ./plugin -count=1
go test ./internal/entitycompat -run TestPluginDoc -count=1
go run ./tools/pluginspec > "$env:TEMP\rlp-surface.txt"
$golden = (Get-Content -Raw internal\entitycompat\testdata\golden_plugin_surface.txt) -replace "`r`n","`n"
$actual = (Get-Content -Raw "$env:TEMP\rlp-surface.txt") -replace "`r`n","`n"
if ($golden -ne $actual) { "SURFACE DRIFT" } else { "SURFACE OK" }
```

Expected: 両方 PASS、`SURFACE OK`。

- [ ] **Step 8: commit する**

```powershell
git diff --check
git diff -- plugin internal/entitycompat/testdata/golden_plugin_surface.txt docs/plugins/authoring.md docs/plugins/compatibility.md
git add plugin/policy.go plugin/policy_test.go internal/entitycompat/testdata/golden_plugin_surface.txt docs/plugins/authoring.md docs/plugins/compatibility.md
git commit -m "Add plugin: hand active role assignments to effective policy providers"
```

Expected: 1 commit。`git diff --check` が無出力。**この commit の SHA を控えておく**（Task 6 のupstream適用で使う）。

---

### Task 2: One-Read Role Snapshot (Roles + Active Assignments) And Assignment-Aware Cache Key

native ロール解決を**1 回の `ListByUser` 読取**から `{roles, activeAssignments}` を作る 1 本の操作に束ねる。`GetUserRoles` は互換ラッパとして roles だけを返す。`resolvePolicies` は内部スナップショットを直接使い、repository の失敗を握り潰さず provider 起動前に checked error として返す。

**Files:**
- Test: `internal/core/role/plugin_policy_test.go`（外部）、`internal/core/role/plugin_policy_internal_test.go`（内部）
- Modify: `internal/core/role/role_service.go`（`roleCacheEntry`、`userRoleSnapshot`、`resolveUserRoleSnapshot` + `GetUserRoles` ラッパ、`activeRoleAssignment` 型と helper）
- Modify: `internal/core/role/plugin_policy.go`（`policyProviderCacheKey`、`resolvePolicies` の role 入力段、request 構築、cache key、`encodePolicyProviderAssignments`、`pluginActiveRoleAssignments`）

**Interfaces:**
- Consumes: `plugin.ActiveRoleAssignment`, `plugin.EffectivePolicyRequest.ActiveAssignments`（Task 1）
- Produces: 非公開 `role.userRoleSnapshot{roles []*model.Role; activeAssignments []activeRoleAssignment}` と `(userRoleSnapshot).clone() userRoleSnapshot`
- Produces: 非公開 `(*role.Service).resolveUserRoleSnapshot(userID string) (userRoleSnapshot, error)` — **戻り値は常に cache と非共有の複製。失敗時は zero snapshot + error（partial を返さない）**
- Preserves: `(*role.Service).GetUserRoles(userID string) ([]*model.Role, error)` — 戻り値は roles のみ、署名も挙動も互換
- Produces: 非公開 `role.activeRoleAssignment{roleID, assignmentID string}` / `role.activeRoleAssignmentsFrom([]*model.RoleAssignment) []activeRoleAssignment` / `role.cloneActiveRoleAssignments(...)` / `role.pluginActiveRoleAssignments(...)`
- Produces: 非公開 `role.encodePolicyProviderAssignments([]plugin.ActiveRoleAssignment) string`
- Reuses: `internal/core/role/role_service_test.go` の `countingAssignmentRepo` と `internal/core/role/plugin_policy_test.go` の `failingPolicyAssignmentRepo`（**同じ用途の型を新規に作らない**）

- [ ] **Step 1: 外部 test（ホスト経由）を先に書く**

`internal/core/role/plugin_policy_test.go` の末尾に追記する。`countingAssignmentRepo` は同じ package の `role_service_test.go` にある既存型を再利用し、呼び出しを数える型が2つにならないようにする:

```go
// newCountingTestService は newTestService と同じ形の戻り値に、既存の
// countingAssignmentRepo (role_service_test.go) を挟んだもの。**型を使い回す**
// ことで「1 query」の主張が全 test で同じ数え方になる。
func newCountingTestService(t *testing.T) (*role.Service, *testutil.MockRoleRepository, *testutil.MockRoleAssignmentRepository, *countingAssignmentRepo) {
	t.Helper()
	roleRepo := testutil.NewMockRoleRepository()
	assignRepo := testutil.NewMockRoleAssignmentRepository(roleRepo)
	metaRepo := newTestMetaRepository()
	idGen, _ := id.NewGenerator("aidx")
	counting := &countingAssignmentRepo{MockRoleAssignmentRepository: assignRepo}
	return role.NewService(roleRepo, counting, metaRepo, idGen), roleRepo, assignRepo, counting
}

func TestEffectivePolicy_ActiveAssignmentsCoverOnlyActiveManualRoles(t *testing.T) {
	svc, roleRepo, assignRepo, _ := newTestService(t)
	roleRepo.Roles["r1"] = &model.Role{ID: "r1", Name: "A", Target: model.RoleTargetManual}
	roleRepo.Roles["r2"] = &model.Role{ID: "r2", Name: "B", Target: model.RoleTargetManual}
	// conditional role は condFormula で一致する。assignment row は無い。
	roleRepo.Roles["r3"] = &model.Role{ID: "r3", Name: "C", Target: model.RoleTargetConditional,
		CondFormula: datatypes.JSON([]byte(`{"type":"isLocal"}`))}
	assign(t, assignRepo, "u1", "r1")
	assign(t, assignRepo, "u1", "r2")
	// orphan assignment: role_assignment の行だけが残っている。
	assignRepo.Assignments["u1:r9"] = &model.RoleAssignment{ID: "a_u1_r9", UserID: "u1", RoleID: "r9"}
	userRepo := testutil.NewMockUserRepository()
	userRepo.Users["u1"] = &model.User{ID: "u1"} // Host nil なので isLocal が真
	svc.SetUserRepo(userRepo)

	var request plugin.EffectivePolicyRequest
	registerProvider(t, svc, "ctx", []string{"canSearchNotes"},
		func(_ context.Context, req plugin.EffectivePolicyRequest) ([]plugin.EffectivePolicyContribution, error) {
			request = req
			return nil, nil
		})

	_, err := svc.GetUserPoliciesChecked("u1")
	require.NoError(t, err)

	assert.Equal(t, []string{"r1", "r2", "r3"}, request.RoleIDs, "RoleIDs は conditional を含むという既存契約を変えない")
	assert.Equal(t, []plugin.ActiveRoleAssignment{
		{RoleID: "r1", AssignmentID: "a_u1_r1"},
		{RoleID: "r2", AssignmentID: "a_u1_r2"},
	}, request.ActiveAssignments, "conditional role と role 行が無い orphan assignment は ActiveAssignments に入らない")
	// **部分集合の不変条件。** RoleIDs にあるのに ActiveAssignments に入らない、あるいは
	// 逆が起きたら host 側で契約が壊れている。
	for _, a := range request.ActiveAssignments {
		assert.Contains(t, request.RoleIDs, a.RoleID)
	}
}

func TestEffectivePolicy_ActiveAssignmentsAreNonNilForAnonymous(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	var request plugin.EffectivePolicyRequest
	var seen bool
	registerProvider(t, svc, "ctx", []string{"canSearchNotes"},
		func(_ context.Context, req plugin.EffectivePolicyRequest) ([]plugin.EffectivePolicyContribution, error) {
			seen = true
			request = req
			return nil, nil
		})

	_, err := svc.GetUserPoliciesChecked("")
	require.NoError(t, err)
	require.True(t, seen, "匿名解決でも provider は呼ばれるので ActiveAssignments は非nil空sliceである必要がある")
	assert.NotNil(t, request.ActiveAssignments)
	assert.Empty(t, request.ActiveAssignments)
}

func TestEffectivePolicy_ActiveAssignmentsExcludeExpiredAssignments(t *testing.T) {
	svc, roleRepo, assignRepo, _ := newTestService(t)
	roleRepo.Roles["r1"] = &model.Role{ID: "r1", Name: "A", Target: model.RoleTargetManual}
	roleRepo.Roles["r2"] = &model.Role{ID: "r2", Name: "B", Target: model.RoleTargetManual}
	assign(t, assignRepo, "u1", "r1")
	past := time.Now().Add(-time.Hour)
	assignRepo.Assignments["u1:r2"] = &model.RoleAssignment{ID: "a_u1_r2", UserID: "u1", RoleID: "r2", ExpiresAt: &past}

	var request plugin.EffectivePolicyRequest
	registerProvider(t, svc, "ctx", []string{"canSearchNotes"},
		func(_ context.Context, req plugin.EffectivePolicyRequest) ([]plugin.EffectivePolicyContribution, error) {
			request = req
			return nil, nil
		})

	_, err := svc.GetUserPoliciesChecked("u1")
	require.NoError(t, err)
	assert.Equal(t, []plugin.ActiveRoleAssignment{{RoleID: "r1", AssignmentID: "a_u1_r1"}}, request.ActiveAssignments)
}

// **1 回の解決で ListByUser は 1 回だけ。** roles と activeAssignments を別々に
// 読む設計に戻ると 2 回になり、hot path に query が 1 本増える。
func TestEffectivePolicy_ResolutionIssuesExactlyOneQuery(t *testing.T) {
	svc, roleRepo, assignRepo, counting := newCountingTestService(t)
	roleRepo.Roles["r1"] = &model.Role{ID: "r1", Name: "A", Target: model.RoleTargetManual}
	assign(t, assignRepo, "u1", "r1")
	var seen int
	registerProvider(t, svc, "ctx", []string{"canSearchNotes"},
		func(context.Context, plugin.EffectivePolicyRequest) ([]plugin.EffectivePolicyContribution, error) {
			seen++
			return nil, nil
		})

	_, err := svc.GetUserPoliciesChecked("u1")
	require.NoError(t, err)
	assert.Equal(t, 1, seen)
	assert.Equal(t, 1, counting.listByUserCalls, "roles と activeAssignments は 1 回の ListByUser から共に作られる")

	// warm cache: provider 自身が LRU に当たって resolver を呼ばないが、role
	// スナップショットは native pass の前に必ず通る。ここで 2 本目が出たら検出できる。
	_, err = svc.GetUserPoliciesChecked("u1")
	require.NoError(t, err)
	assert.Equal(t, 1, counting.listByUserCalls, "warm cache は 2 つとも答える")
}

// **invalidation は「読み直す」ために存在する。** 空の assignments を返して黙る、
// という別の故障を許さない。
func TestEffectivePolicy_InvalidationRefetchesBothInsteadOfEmptyingAssignments(t *testing.T) {
	svc, roleRepo, assignRepo, counting := newCountingTestService(t)
	roleRepo.Roles["r1"] = &model.Role{ID: "r1", Name: "A", Target: model.RoleTargetManual}
	assign(t, assignRepo, "u1", "r1")
	want := []plugin.ActiveRoleAssignment{{RoleID: "r1", AssignmentID: "a_u1_r1"}}
	var got [][]plugin.ActiveRoleAssignment
	registerProvider(t, svc, "ctx", []string{"canSearchNotes"},
		func(_ context.Context, req plugin.EffectivePolicyRequest) ([]plugin.EffectivePolicyContribution, error) {
			got = append(got, req.ActiveAssignments)
			return nil, nil
		})

	_, err := svc.GetUserPoliciesChecked("u1")
	require.NoError(t, err)
	svc.InvalidateUserRoleCache("u1")
	_, err = svc.GetUserPoliciesChecked("u1")
	require.NoError(t, err)

	assert.Equal(t, 2, counting.listByUserCalls, "invalidated な user は読み直す")
	require.Len(t, got, 2, "provider は 2 回とも呼ばれる")
	assert.Equal(t, want, got[0])
	assert.Equal(t, want, got[1], "invalidation 後の解決で assignments が空に堕ちてはいけない")
}

// **repository の読み損ねは握り潰さない。** activeAssignments を空で埋めた partial
// snapshot を渡すと、plugin には「その role に active な assignment が無い」という
// 嘘が見えるので、provider を起動する前に error にする。
//
// 既存の `TestEffectivePolicy_RoleLookupErrorSkipsProvidersAndRemainsDistinct` は同じ
// 経路を「provider を起動しない」角度で固定している。こちらは assignments 側の契約を
// 明示する **regression guard** で、実装前から緑になる（既存の `GetUserRoles` が既に
// error を返すため）。RED は Step 2 の内部 test で観測する。
func TestEffectivePolicy_AssignmentRepositoryFailureSkipsProviders(t *testing.T) {
	roleRepo := testutil.NewMockRoleRepository()
	assignRepo := &failingPolicyAssignmentRepo{
		MockRoleAssignmentRepository: testutil.NewMockRoleAssignmentRepository(roleRepo),
		err:                          errors.New("assignment lookup failed"),
	}
	metaRepo := newTestMetaRepository()
	idGen, _ := id.NewGenerator("aidx")
	svc := role.NewService(roleRepo, assignRepo, metaRepo, idGen)
	var providerCalls atomic.Int32
	var assignments []plugin.ActiveRoleAssignment
	registerProvider(t, svc, "p", []string{"canSearchNotes"},
		func(_ context.Context, req plugin.EffectivePolicyRequest) ([]plugin.EffectivePolicyContribution, error) {
			providerCalls.Add(1)
			assignments = req.ActiveAssignments
			return nil, nil
		})

	_, err := svc.GetUserPoliciesChecked("u1")

	require.Error(t, err)
	assert.ErrorContains(t, err, "role: effective policy inputs")
	assert.Zero(t, providerCalls.Load(), "role 入力を読めないとき provider を起動しない")
	assert.Nil(t, assignments, "assignment を空で埋めた値で provider を起動しない")
}
```

`atomic`, `errors`, `time`, `datatypes`, `testutil`, `model`, `plugin`, `role`, `id` はすべて既存の import。**`internal/repository` は追加しない**（`countingAssignmentRepo` は `role_service_test.go` 側で `*testutil.MockRoleAssignmentRepository` を埋めている）。

- [ ] **Step 2: 内部 test を先に書いて、RED を観察する**

`internal/core/role/plugin_policy_internal_test.go` に追記する。内部 API を直接叩くので「partial を返さない」「cache hit が両方を返す」をここで固定する:

```go
// countingInternalAssignmentRepo は内部 test から ListByUser の回数と失敗を制御
// する。**plugin_policy_test.go の countingAssignmentRepo とは package が違うので
// 別型になる**が、外から見える契約は増やさない。
type countingInternalAssignmentRepo struct {
	*testutil.MockRoleAssignmentRepository
	err   error
	calls int
}

func (r *countingInternalAssignmentRepo) ListByUser(userID string) ([]*model.RoleAssignment, error) {
	r.calls++
	if r.err != nil {
		return nil, r.err
	}
	return r.MockRoleAssignmentRepository.ListByUser(userID)
}

func newInternalServiceWithCountingRepo(t *testing.T) (*Service, *testutil.MockRoleRepository, *countingInternalAssignmentRepo) {
	t.Helper()
	roleRepo := testutil.NewMockRoleRepository()
	assignRepo := &countingInternalAssignmentRepo{MockRoleAssignmentRepository: testutil.NewMockRoleAssignmentRepository(roleRepo)}
	metaRepo := testutil.NewMockMetaRepository()
	metaRepo.Meta = &model.Meta{ID: "x"}
	idGen, _ := id.NewGenerator("aidx")
	return NewService(roleRepo, assignRepo, metaRepo, idGen), roleRepo, assignRepo
}

// 1 回の ListByUser から roles と activeAssignments が同時に得られる。
func TestResolveUserRoleSnapshotReadsOnceAndReturnsBoth(t *testing.T) {
	svc, roleRepo, assignRepo := newInternalServiceWithCountingRepo(t)
	roleRepo.Roles["r1"] = &model.Role{ID: "r1", Name: "A", Target: model.RoleTargetManual}
	assignRepo.Assignments["u1:r1"] = &model.RoleAssignment{ID: "a1", UserID: "u1", RoleID: "r1"}

	snapshot, err := svc.resolveUserRoleSnapshot("u1")

	require.NoError(t, err)
	require.Len(t, snapshot.roles, 1)
	assert.Equal(t, "r1", snapshot.roles[0].ID)
	assert.Equal(t, []activeRoleAssignment{{roleID: "r1", assignmentID: "a1"}}, snapshot.activeAssignments)
	assert.Equal(t, 1, assignRepo.calls, "roles と activeAssignments は 1 回の読取から共に作られる")
}

// warm cache は 0 query で両方を返す。**片方だけ返さない。**
func TestResolveUserRoleSnapshotCacheHitReturnsBothWithoutReading(t *testing.T) {
	svc, roleRepo, assignRepo := newInternalServiceWithCountingRepo(t)
	roleRepo.Roles["r1"] = &model.Role{ID: "r1", Name: "A", Target: model.RoleTargetManual}
	assignRepo.Assignments["u1:r1"] = &model.RoleAssignment{ID: "a1", UserID: "u1", RoleID: "r1"}

	_, err := svc.resolveUserRoleSnapshot("u1")
	require.NoError(t, err)
	cached, err := svc.resolveUserRoleSnapshot("u1")

	require.NoError(t, err)
	require.Len(t, cached.roles, 1)
	assert.Equal(t, []activeRoleAssignment{{roleID: "r1", assignmentID: "a1"}}, cached.activeAssignments,
		"cache hit は roles と activeAssignments の両方を返す")
	assert.Equal(t, 1, assignRepo.calls, "cache hit は repository を読まない")
}

// **cache entry は書き込み済みのスナップショットを返す。** 後から repository を壊しても、
// cache が assignments を空に堕ちさせない。
func TestResolveUserRoleSnapshotCacheHitSurvivesRepositoryFailure(t *testing.T) {
	svc, roleRepo, assignRepo := newInternalServiceWithCountingRepo(t)
	roleRepo.Roles["r1"] = &model.Role{ID: "r1", Name: "A", Target: model.RoleTargetManual}
	assignRepo.Assignments["u1:r1"] = &model.RoleAssignment{ID: "a1", UserID: "u1", RoleID: "r1"}
	_, err := svc.resolveUserRoleSnapshot("u1")
	require.NoError(t, err)

	assignRepo.err = errors.New("assignment lookup failed")
	cached, err := svc.resolveUserRoleSnapshot("u1")

	require.NoError(t, err)
	assert.Equal(t, []activeRoleAssignment{{roleID: "r1", assignmentID: "a1"}}, cached.activeAssignments)
	assert.Equal(t, 1, assignRepo.calls, "cache hit は repository に触れない")
}

// **partial snapshot を返さない。** error のとき zero snapshot が返る。
func TestResolveUserRoleSnapshotPropagatesRepositoryErrorWithoutPartialResult(t *testing.T) {
	svc, _, assignRepo := newInternalServiceWithCountingRepo(t)
	readErr := errors.New("assignment lookup failed")
	assignRepo.err = readErr

	snapshot, err := svc.resolveUserRoleSnapshot("u1")

	require.ErrorIs(t, err, readErr)
	assert.Empty(t, snapshot.roles, "roles だけ埋まった partial snapshot を返さない")
	assert.Empty(t, snapshot.activeAssignments, "assignments を空で埋めない")
}

// 匿名は非nil空の snapshot を error なしで返す（repository にも触らない）。
func TestResolveUserRoleSnapshotAnonymousIsNonNilAndErrorFree(t *testing.T) {
	svc, _, assignRepo := newInternalServiceWithCountingRepo(t)

	snapshot, err := svc.resolveUserRoleSnapshot("")

	require.NoError(t, err)
	assert.NotNil(t, snapshot.activeAssignments)
	assert.Empty(t, snapshot.activeAssignments)
	assert.Empty(t, snapshot.roles)
	assert.Zero(t, assignRepo.calls, "匿名解決は repository を読まない")
}

// GetUserRoles は互換ラッパ。roles だけを返し、2 回読まない。
func TestGetUserRolesKeepsItsSignatureAndHidesAssignments(t *testing.T) {
	svc, roleRepo, assignRepo := newInternalServiceWithCountingRepo(t)
	roleRepo.Roles["r1"] = &model.Role{ID: "r1", Name: "A", Target: model.RoleTargetManual}
	assignRepo.Assignments["u1:r1"] = &model.RoleAssignment{ID: "a1", UserID: "u1", RoleID: "r1"}

	roles, err := svc.GetUserRoles("u1")

	require.NoError(t, err)
	require.Len(t, roles, 1)
	assert.Equal(t, "r1", roles[0].ID)
	assert.Equal(t, 1, assignRepo.calls, "ラッパが 2 回読まない")
}

// 返した snapshot を書き換えても cache は変わらない。
func TestResolveUserRoleSnapshotReturnsACopy(t *testing.T) {
	svc, roleRepo, assignRepo := newInternalServiceWithCountingRepo(t)
	roleRepo.Roles["r1"] = &model.Role{ID: "r1", Name: "A", Target: model.RoleTargetManual}
	assignRepo.Assignments["u1:r1"] = &model.RoleAssignment{ID: "a1", UserID: "u1", RoleID: "r1"}

	first, err := svc.resolveUserRoleSnapshot("u1")
	require.NoError(t, err)
	first.activeAssignments[0].assignmentID = "mutated"

	second, err := svc.resolveUserRoleSnapshot("u1")
	require.NoError(t, err)
	assert.Equal(t, "a1", second.activeAssignments[0].assignmentID, "cache は非共有の複製を返す")
	assert.Equal(t, 1, assignRepo.calls)
}

// assignment 同一性が provider の cache key に入らないと付け外しの直後に古い結果を
// 返す。**RoleIDs は両者とも空**なので差の出所は assignment だけ。
func TestPolicyProviderCacheKeySeparatesAssignmentIdentities(t *testing.T) {
	runtime := newPolicyProviderRuntime(defaultEffectivePolicyProviderCacheEntries)
	provider := policyProvider{
		reg: plugin.EffectivePolicyRegistration{
			Keys: []string{"canSearchNotes"},
			Resolve: func(_ context.Context, req plugin.EffectivePolicyRequest) ([]plugin.EffectivePolicyContribution, error) {
				granted := req.ActiveAssignments[0].AssignmentID == "a1"
				return []plugin.EffectivePolicyContribution{{Key: "canSearchNotes", Priority: 2, Value: granted}}, nil
			},
		},
		runtime: runtime,
	}
	first := plugin.EffectivePolicyRequest{UserID: "u1", ActiveAssignments: []plugin.ActiveRoleAssignment{{RoleID: "r1", AssignmentID: "a1"}}}
	second := plugin.EffectivePolicyRequest{UserID: "u1", ActiveAssignments: []plugin.ActiveRoleAssignment{{RoleID: "r1", AssignmentID: "a2"}}}

	firstResult, ok := resolvePolicyProviderCached(provider, first)
	require.True(t, ok)
	secondResult, ok := resolvePolicyProviderCached(provider, second)
	require.True(t, ok)

	assert.Equal(t, true, firstResult[0].Value)
	assert.Equal(t, false, secondResult[0].Value, "別の assignment の結果を再利用してはいけない")
	assert.Len(t, runtime.cache, 2, "assignment が違えば別 entry")
}

func TestEncodePolicyProviderAssignmentsPreventsConcatenationCollision(t *testing.T) {
	assert.NotEqual(t,
		encodePolicyProviderAssignments([]plugin.ActiveRoleAssignment{{RoleID: "a", AssignmentID: "bc"}}),
		encodePolicyProviderAssignments([]plugin.ActiveRoleAssignment{{RoleID: "ab", AssignmentID: "c"}}),
		"role/assignment の境界が曖昧だと別入力を同じ key に押し込める")
	assert.NotEqual(t,
		encodePolicyProviderAssignments([]plugin.ActiveRoleAssignment{{RoleID: "r", AssignmentID: "a1"}}),
		encodePolicyProviderAssignments([]plugin.ActiveRoleAssignment{{RoleID: "r", AssignmentID: "a"}, {RoleID: "1", AssignmentID: ""}}),
		"entry の境界が曖昧だと別入力を同じ key に押し込める")
}
```

必要な import は `internal/testutil` と `internal/model`。`errors`, `context`, `plugin`, `require`, `assert`, `id` は既にある。

RED を観察する:

```powershell
go vet ./internal/core/role
go test ./internal/core/role -count=1 -run "TestEffectivePolicy_ActiveAssign|TestEffectivePolicy_ResolutionIssuesExactlyOneQuery|TestEffectivePolicy_InvalidationRefetchesBoth|TestEffectivePolicy_AssignmentRepositoryFailure|TestResolveUserRoleSnapshot|TestGetUserRolesKeepsItsSignature|TestPolicyProviderCacheKeySeparatesAssignmentIdentities|TestEncodePolicyProviderAssignments"
```

Expected: **コンパイルエラー**。`undefined: resolveUserRoleSnapshot` / `undefined: activeRoleAssignment` / `undefined: encodePolicyProviderAssignments` / `unknown field ActiveAssignments`（外部 test 側）。production はまだ1行も変えていない。

- [ ] **Step 3: production を実装する（role 側）**

`internal/core/role/role_service.go` の `cloneRoles` の**直後**に snapshot 型を追加する:

```go
// userRoleSnapshot は 1 ユーザーの role 解決結果: 解決済み roles と、それを支撑する
// active manual assignment。
//
// **2 つを返す。1 回で読む。** assignment の唯一の供給源は `ListByUser` なので、
// roles と同じ操作で読むことが「policy 解決が 1 query のまま」という性質と、
// 「片方にだけ現れる role がない」という整合性を同時に守る。
type userRoleSnapshot struct {
	roles             []*model.Role
	activeAssignments []activeRoleAssignment
}

// clone は cache entry と呼び出し側のどちらかが他の変更を観測しないよう値を複製
// する（GetUserRoles が roles に対してやっていたことと同じ）。
func (s userRoleSnapshot) clone() userRoleSnapshot {
	return userRoleSnapshot{
		roles:             cloneRoles(s.roles),
		activeAssignments: cloneActiveRoleAssignments(s.activeAssignments),
	}
}
```

`type roleCacheEntry struct` を次のように変更する:

```go
type roleCacheEntry struct {
	// snapshot は roles と activeAssignments の組。**1 回の ListByUser から共に
	// 積まれる**ので、2 者の整合性が壊れることがなく、解決は 1 query で済む。
	snapshot  userRoleSnapshot
	expiresAt time.Time
}
```

`GetUserRoles` の実装全体を、**内部操作 + 互換ラッパ**の2つに置き換える（doc も一緒に書く）:

```go
// resolveUserRoleSnapshot resolves the user's active roles and their active
// manual assignments, serving the per-user cache when it is warm.
//
// **戻り値は常に cache と非共有の複製。** 呼び出し側が書き換えても cache は
// 変わらない。
//
// **partial snapshot を返さない。** `ListByUser` が失敗したら roles も
// activeAssignments も一切埋めずに error を返す。片方だけ返すと、plugin には
// 「その role に active な assignment が無い」という嘘が見える。
func (s *Service) resolveUserRoleSnapshot(userID string) (userRoleSnapshot, error) {
	if userID == "" {
		return userRoleSnapshot{roles: nil, activeAssignments: []activeRoleAssignment{}}, nil
	}
	s.userRoleCacheMu.RLock()
	if entry := s.userRoleCache[userID]; entry != nil && time.Now().Before(entry.expiresAt) {
		snapshot := entry.snapshot.clone()
		s.userRoleCacheMu.RUnlock()
		return snapshot, nil
	}
	s.userRoleCacheMu.RUnlock()

	s.userRoleCacheMu.Lock()
	allEpoch := s.allUserRoleEpoch
	userEpoch := s.userRoleEpoch[userID]
	s.userRoleFlights[userID]++
	s.userRoleCacheMu.Unlock()
	defer s.finishUserRoleFlight(userID)

	// **唯一の読取点。** roles と activeAssignments はこの 1 回から共に作られる。
	assignments, err := s.assignmentRepo.ListByUser(userID)
	if err != nil {
		return userRoleSnapshot{}, err
	}
	assignedRoles := make([]*model.Role, 0, len(assignments))
	for _, a := range assignments {
		if a.Role != nil {
			assignedRoles = append(assignedRoles, a.Role)
		}
	}
	activeAssignments := activeRoleAssignmentsFrom(assignments)

	// Conditional role 評価: 全 role を fetch して target=conditional のみを formula
	// 評価で絞り込む。assigned roles と matched conditional の和集合を返す。upstream
	// TS の getUserRoles と同じ順 (assigned が先)。
	//
	// roleRepo / userRepo どちらかが未配線なら、conditional 評価は skip して
	// assigned のみを返す (= 旧挙動)。test 経路で userRepo 未注入のケースに
	// 配慮した soft-fail。
	condRoles := s.evaluateConditionalRoles(userID, assignedRoles)
	roles := append(assignedRoles, condRoles...)

	// #2106 S5: cache 失効を「TTL」と「最も早い assignment expiresAt」の min にする。
	// ListByUser は fetch 時点で有効な assignment しか返さないが、TTL 中に期限切れに
	// なる time-limited assignment はそのまま cache に残り、最大 roleCacheTTL の間
	// role/policy として効き続けてしまう (upstream は getUserAssigns で read 毎に
	// expiresAt を再 filter)。entry を soonest expiry で drop すれば、次 read が
	// 再 fetch して DB filter が期限切れ assignment を除外する。
	cacheExpiry := time.Now().Add(roleCacheTTL)
	for _, a := range assignments {
		if a.ExpiresAt != nil && a.ExpiresAt.Before(cacheExpiry) {
			cacheExpiry = *a.ExpiresAt
		}
	}

	snapshot := userRoleSnapshot{roles: roles, activeAssignments: activeAssignments}
	s.userRoleCacheMu.Lock()
	if s.allUserRoleEpoch == allEpoch && s.userRoleEpoch[userID] == userEpoch {
		s.userRoleCache[userID] = &roleCacheEntry{snapshot: snapshot.clone(), expiresAt: cacheExpiry}
	}
	s.userRoleCacheMu.Unlock()
	return snapshot.clone(), nil
}

// GetUserRoles returns all active roles applied to the user. The returned
// slice is the union of (a) manually assigned roles that have not expired and
// (b) `target=conditional` roles whose `condFormula` evaluates to true for the
// user. Mirrors upstream Misskey TS `RoleService.getUserRoles` so
// admin-authored conditional roles (e.g. "base role for all local users")
// take effect at gate sites like HasRolePolicy (#1020).
//
// 結果は roleCacheTTL 期間 in-memory にキャッシュされる (#300 3-5)。
// Conditional 評価結果も同じ cache に乗るので、user の followers/notes count
// などが変動しても最大 5 分の反映遅延がある点に注意 (= upstream TS と同じ
// trade-off)。
//
// **active manual assignment も同時に解決している**が、この公開 API は外へ返さない。
// assignment を露出するのは resolveUserRoleSnapshot を直接使う effective policy
// 解決だけ。
func (s *Service) GetUserRoles(userID string) ([]*model.Role, error) {
	snapshot, err := s.resolveUserRoleSnapshot(userID)
	if err != nil {
		return nil, err
	}
	return snapshot.roles, nil
}
```

`GetUserAssigns` の**直前**に型と生成helperを追加する:

```go
// activeRoleAssignment は active な手動role assignment と、その role の組。role cache
// entry の隣で保持し、effective policy の解決が plugin へ渡す assignment 同一性を表す。
type activeRoleAssignment struct {
	roleID       string
	assignmentID string
}

// activeRoleAssignmentsFrom は active manual assignment を「1 role につき高々 1 件」
// に還元する。
//
// **`Role` が nil なら入れない。** role 行を消した orphan assignment は resolved roles
// からも落ちるので、入れないと「置換対象なのに native contribution の無い role」になる。
//
// **conditional role は入れない。** `role_assignment` の行は手動割り当てだけが持つので、
// `target=conditional` の role を指す行はロールを manual → conditional に切り替えた
// 直後などの残骸であり、置換対象にならない。`target` が空文字のものは通す — DB の
// `role_target` は既定 `manual` なので、「conditional ではない」を読めば本番と一致する。
//
// **同じ role が複数行あれば assignment ID の最小値 1 件だけを残す。** `Assign` は先に
// `Exists` を見るので通常 1 行しか無いが、1 対 1 の置換が成立するには「active な手動
// ロールにつき assignment は 1 つ」であることが host の**保証**になっている必要がある。
// 決まっていないと置換対象が一意に定まらない。
func activeRoleAssignmentsFrom(assignments []*model.RoleAssignment) []activeRoleAssignment {
	out := make([]activeRoleAssignment, 0, len(assignments))
	at := make(map[string]int, len(assignments))
	for _, a := range assignments {
		if a == nil || a.Role == nil || a.ID == "" || a.RoleID == "" {
			continue
		}
		if a.Role.Target == model.RoleTargetConditional {
			continue
		}
		if i, duplicate := at[a.RoleID]; duplicate {
			if a.ID < out[i].assignmentID {
				out[i].assignmentID = a.ID
			}
			continue
		}
		at[a.RoleID] = len(out)
		out = append(out, activeRoleAssignment{roleID: a.RoleID, assignmentID: a.ID})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].roleID != out[j].roleID {
			return out[i].roleID < out[j].roleID
		}
		return out[i].assignmentID < out[j].assignmentID
	})
	return out
}

// cloneActiveRoleAssignments は値を複製して返す。cache entry を読み出した側が
// 書き換えないようにする。
func cloneActiveRoleAssignments(in []activeRoleAssignment) []activeRoleAssignment {
	if in == nil {
		return nil
	}
	return append(make([]activeRoleAssignment, 0, len(in)), in...)
}
```

- [ ] **Step 4: production を実装する（host 側）**

`internal/core/role/plugin_policy.go` の `type policyProviderCacheKey struct` にフィールドを1つ追加する:

```go
	// assignments は ActiveAssignments の encoding。**RoleIDs が同じでも assignment が
	// 違えば結果は違う** plugin がある（assignment ごとに状態を持つ）ので、付けないと
	// 付け外し / 再割り当ての直後に前の結果を返す。
	assignments string
```

`resolvePolicies` の

```go
	roles := []*model.Role{}
	if userID != "" {
		var err error
		roles, err = s.GetUserRoles(userID)
		if err != nil {
			return s.applyServerCaps(base), fmt.Errorf("role: effective policy inputs: %w", err)
		}
	}
```

を次のように置き換える:

```go
	roles := []*model.Role{}
	activeAssignments := []activeRoleAssignment{}
	if userID != "" {
		// **1 回の読取から roles と activeAssignments を共に得る。** repository が読めない
		// ときは activeAssignments を空で埋めた snapshot を渡さず、provider を起動する
		// 前に checked error にする。
		snapshot, err := s.resolveUserRoleSnapshot(userID)
		if err != nil {
			return s.applyServerCaps(base), fmt.Errorf("role: effective policy inputs: %w", err)
		}
		roles = snapshot.roles
		activeAssignments = snapshot.activeAssignments
	}
```

`// provider には現在 active な native RoleID のみを、ソート + clone して渡す。` の**直後**に追記する:

```go
	// active manual assignment も渡す。**同じ snapshot から写す**ので 2 本目の query は
	// 出ない。RoleIDs だけでは「同じ role でもどの assignment なのか」が分からず、
	// assignment に紐づく状態を持つ plugin が作れないため。戻り値は常に非nil（匿名は空slice）。
	assignments := pluginActiveRoleAssignments(activeAssignments)
```

provider を呼ぶ goroutine 内の request 構築を次のように変更する:

```go
			providerRoleIDs := make([]string, len(roleIDs))
			copy(providerRoleIDs, roleIDs)
			providerAssignments := make([]plugin.ActiveRoleAssignment, len(assignments))
			copy(providerAssignments, assignments)
			resolved[i].contributions, resolved[i].ok = resolvePolicyProviderCached(
				p,
				plugin.EffectivePolicyRequest{
					UserID:            userID,
					RoleIDs:           providerRoleIDs,
					ActiveAssignments: providerAssignments,
				},
			)
```

`resolvePolicyProviderCached` の先頭にある

```go
	key := policyProviderCacheKey{userID: req.UserID, roleIDs: encodePolicyProviderRoleIDs(req.RoleIDs)}
```

を次のように変更する:

```go
	key := policyProviderCacheKey{
		userID:      req.UserID,
		roleIDs:     encodePolicyProviderRoleIDs(req.RoleIDs),
		assignments: encodePolicyProviderAssignments(req.ActiveAssignments),
	}
```

`encodePolicyProviderRoleIDs` の**直後**に helper を追加する:

```go
// encodePolicyProviderAssignments は role ID と assignment ID の**両方を**
// length-prefix して key にする。片方だけ prefix すると
// `{RoleID: "a", AssignmentID: "bc"}` と `{RoleID: "ab", AssignmentID: "c"}` が同じ key に
// なり、plugin には別の user の assignment ID が渡る。
func encodePolicyProviderAssignments(assignments []plugin.ActiveRoleAssignment) string {
	var encoded strings.Builder
	for _, a := range assignments {
		encoded.WriteString(strconv.Itoa(len(a.RoleID)))
		encoded.WriteByte(':')
		encoded.WriteString(a.RoleID)
		encoded.WriteString(strconv.Itoa(len(a.AssignmentID)))
		encoded.WriteByte(':')
		encoded.WriteString(a.AssignmentID)
	}
	return encoded.String()
}
```

`func activeRoleIDs(` の**直前**に型変換helperを追加する:

```go
// pluginActiveRoleAssignments は host 内部型を公開plugin型へ写す。戻り値は常に非nil
//（len 0 でも make の戻りなので nil にならない）— 匿名解決の契約。
func pluginActiveRoleAssignments(in []activeRoleAssignment) []plugin.ActiveRoleAssignment {
	out := make([]plugin.ActiveRoleAssignment, len(in))
	for i, a := range in {
		out[i] = plugin.ActiveRoleAssignment{RoleID: a.roleID, AssignmentID: a.assignmentID}
	}
	return out
}
```

- [ ] **Step 5: GREEN を確認する**

```powershell
& (Join-Path (go env GOROOT) "bin\gofmt.exe") -s -w internal\core\role\role_service.go internal\core\role\plugin_policy.go internal\core\role\plugin_policy_test.go internal\core\role\plugin_policy_internal_test.go
go vet ./internal/core/role
go test ./internal/core/role -count=1
```

Expected: PASS。**`internal/core/role` の全テストが緑**。特に次の既存テストが落ちないこと:

- `TestEffectivePolicy_RoleLookupErrorSkipsProvidersAndRemainsDistinct`（role 入力 error で provider を起動しない）
- `TestEffectivePolicy_MetaBaseAndRoleInputFailuresBothReported`（`role: effective policy inputs` の wrap 文面）
- `TestGetUserRoles_CacheExpiryCappedAtAssignmentExpiry` / `TestGetUserRoles_ExpiredEntryPublishesAndReturnsIndependentSnapshot`（`role_s5_internal_test.go` は `entry.expiresAt` を読むだけなので snapshot 化で壊れない）
- `role_service_test.go` の cache テスト（`listByUserCalls` の既存主張）

- [ ] **Step 6: commit する**

```powershell
git diff --check
git diff -- internal/core/role
git add internal/core/role/role_service.go internal/core/role/plugin_policy.go internal/core/role/plugin_policy_test.go internal/core/role/plugin_policy_internal_test.go
git commit -m "Add role: expose active assignments to effective policy providers"
```

Expected: 1 commit。query回数・conditional除外・orphan除外・expiry除外・非nil空slice・partial非返却の test が一緒に並ぶ。**この commit の SHA を控えておく**。

---

### Task 3: Make A Role's Aggregate Entry Substitutable (Behaviour-Preserving Refactor)

`computePolicy` は「ロールごとに `map[key]override` を持つ」形なので、ロールの identity が失われていて置換を差し込めない。**挙動を変えずに**、ロール ID ごと・置換 entry 任意の `rolePolicyInput` へ持ち替える。Task 4 の差分を最小にするための土台。

**Files:**
- Test: `internal/core/role/optout_aggregation_test.go`（`policyInputs` helper と `computePolicy` の呼び出し 4 箇所）
- Modify: `internal/core/role/role_service.go`（`rolePolicyInput` / `rolePolicyEntry` / `newRolePolicyInputs` / `computePolicy`）
- Modify: `internal/core/role/plugin_policy.go`（`roleOverrides` のループを `roleInputs` へ、呼び出し 2 箇所）

**Interfaces:**
- Consumes: なし（Task 2 の変更に依存しない純粋な refactor）
- Produces: 非公開 `role.rolePolicyInput{roleID string; overrides map[string]rolePolicyOverride; replacements map[string]policyEntry}` と `(rolePolicyInput).entry(key string, baseVal any) policyEntry`
- Produces: 非公開 `role.rolePolicyEntry(overrides map[string]rolePolicyOverride, key string, baseVal any) policyEntry`
- Produces: 非公開 `role.newRolePolicyInputs(roles []*model.Role) []rolePolicyInput`
- Produces: 内部テスト helper `policyInputs(overrides ...map[string]rolePolicyOverride) []rolePolicyInput`
- Changes: `computePolicy(key string, baseVal any, inputs []rolePolicyInput, extra []policyEntry) any`（第3引数の型が `[]map[string]rolePolicyOverride` から `[]rolePolicyInput` に変わる。internal 関数なので公開面ではない）

- [ ] **Step 1: test を先に新しい形へ書き換える（RED を作る）**

`internal/core/role/optout_aggregation_test.go` の import 直後に helper を追加する:

```go
// policyInputs は「ロールごとの override だけを持つ」test 入力を rolePolicyInput へ
// 包む。roleID は空のままでよい — この file の test は置換を扱わない（置換は
// plugin_policy_test.go 側）。
func policyInputs(overrides ...map[string]rolePolicyOverride) []rolePolicyInput {
	out := make([]rolePolicyInput, 0, len(overrides))
	for _, m := range overrides {
		out = append(out, rolePolicyInput{overrides: m})
	}
	return out
}
```

`computePolicy` の呼び出し 4 箇所を書き換える:

```go
// 1) TestOptOutNotificationTypes_UnsetRolesDoNotCancel の table 実行部
got := computePolicy(PolicyOptOutNotificationTypes, base, policyInputs(tt.overrides...), nil)

// 2) TestUploadableFileTypes_UnsetRolesStillParticipate
got := computePolicy("uploadableFileTypes", base, policyInputs(
	map[string]rolePolicyOverride{"uploadableFileTypes": {Priority: 0, Value: []string{"video/*"}}},
	map[string]rolePolicyOverride{},
), nil)

// 3) TestOptOutNotificationTypes_PriorityCascadeMatchesOtherPolicies
gotOptOut := computePolicy(PolicyOptOutNotificationTypes, optOutBase,
	policyInputs(overrides(PolicyOptOutNotificationTypes, []string{"abuseReport"})...), nil)
gotUpload := computePolicy("uploadableFileTypes", uploadBase,
	policyInputs(overrides("uploadableFileTypes", []string{"video/*"})...), nil)

// 4) TestOptOutNotificationTypes_HighPriorityExplicitWins
got := computePolicy(PolicyOptOutNotificationTypes, []string{},
	policyInputs(
		map[string]rolePolicyOverride{PolicyOptOutNotificationTypes: {Priority: 2, Value: []string{"abuseReport"}}},
		map[string]rolePolicyOverride{PolicyOptOutNotificationTypes: {Priority: 0, Value: []string{"note"}}},
	), nil)
```

**この時点では production に手を入れていない。** `plugin_policy.go` の `roleOverrides` ループも `computePolicy` の呼び出しも、まだ元の形のままにする。

- [ ] **Step 2: RED を観察する**

```powershell
go vet ./internal/core/role
```

Expected: コンパイルエラー。`undefined: rolePolicyInput`、および `computePolicy` の引数型が不一致というエラー。production を触っていないので、`rolePolicyInput` が無いことだけが原因になる。

- [ ] **Step 3: production を実装する（型と entry ビルダー）**

`internal/core/role/role_service.go` の `type policyEntry struct` の**直後**に追加する:

```go
// rolePolicyInput は 1 つのロールが集約に贈るものの全体:
//
//	- roleID: どのロールか（置換の対象を突き合わせるため）
//	- overrides: `Role.Policies` を key ごとに decode した結果
//	- replacements: provider がこの要求で「native contribution の代わりに使う」と
//	  宣言した entry（key ごと）。provider が置換しなかったロールは nil。
//
// **aggregator は一切知らない。** entry を 1 つ選んで priority cascade に積むだけなので、
// 置換の有無は関数内で完結する。
type rolePolicyInput struct {
	roleID    string
	overrides map[string]rolePolicyOverride
	// replacements はこのロールの置換 entry。provider が置換しなかったロールでは
	// nil のまま（= native entry が使われる）。
	replacements map[string]policyEntry
}

// entry は、このロールが key に対して贈る entry を返す。置換されていれば置換 entry
//（**元 native entry の priority を引き継ぐ**）、されていなければ native entry。
func (in rolePolicyInput) entry(key string, baseVal any) policyEntry {
	if replacement, ok := in.replacements[key]; ok {
		return replacement
	}
	return rolePolicyEntry(in.overrides, key, baseVal)
}

// rolePolicyEntry は置換されていないロールが key に贈る entry。**この role が key を
// 宣言していない場合は base 値を priority 0 で参加させる**という upstream 互換の既定が
// ここに入る。
func rolePolicyEntry(overrides map[string]rolePolicyOverride, key string, baseVal any) policyEntry {
	p, ok := overrides[key]
	if !ok {
		return policyEntry{priority: 0, value: baseVal}
	}
	if p.UseDefault {
		return policyEntry{priority: p.Priority, value: baseVal}
	}
	return policyEntry{priority: p.Priority, value: p.Value, explicit: true}
}

// newRolePolicyInputs は解決したロールを「ロールIDつきで集約できる」形へ変換する。
// `Role.Policies` が空 / パース不能なロールは overrides が nil のまま入り、集約では
// base 参加 = 従来と同じ。
func newRolePolicyInputs(roles []*model.Role) []rolePolicyInput {
	out := make([]rolePolicyInput, 0, len(roles))
	for _, r := range roles {
		if r == nil {
			out = append(out, rolePolicyInput{})
			continue
		}
		if len(r.Policies) == 0 {
			out = append(out, rolePolicyInput{roleID: r.ID})
			continue
		}
		out = append(out, rolePolicyInput{roleID: r.ID, overrides: parseRolePolicies(r.Policies)})
	}
	return out
}
```

`computePolicy` の doc・シグネチャ・**先頭ループだけ**を次のように置き換える（`collected` 以降は変更しない）:

```go
// computePolicy resolves the effective value for a single policy key by
// applying upstream TS priority cascade + per-key aggregator. baseVal is the
// merged default+meta value used when a role specifies useDefault=true or when
// no role has an override. inputs carries each role's parsed policies plus the
// replacement entries an effective-policy provider supplied for it. extra
// carries effective-policy provider contributions for the key (already validated
// & type-checked by the host); it is merged into the same priority cascade as the
// role overrides.
func computePolicy(key string, baseVal any, inputs []rolePolicyInput, extra []policyEntry) any {
	// 各 role がこの policy に贈る entry を組み立てる。entry 無し = priority=0,
	// useDefault=true (= base にフォールバック) として扱う。
	collected := make([]policyEntry, 0, len(inputs)+len(extra))
	for _, in := range inputs {
		collected = append(collected, in.entry(key, baseVal))
	}
	// provider contribution は同じ priority cascade に参加させる (= native と provider は
	// 同一 priority グループ内で aggregate される)。
	collected = append(collected, extra...)
```

- [ ] **Step 4: production を実装する（呼び出し側）**

`internal/core/role/plugin_policy.go` の `roleOverrides` を組み立てるループ

```go
	roleOverrides := make([]map[string]rolePolicyOverride, 0, len(roles))
	for _, r := range roles {
		if r == nil || len(r.Policies) == 0 {
			roleOverrides = append(roleOverrides, nil)
			continue
		}
		roleOverrides = append(roleOverrides, parseRolePolicies(r.Policies))
	}
```

を削除して、次の1行に置き換える:

```go
	roleInputs := newRolePolicyInputs(roles)
```

そして 2 箇所の呼び出しを次のように変更する:

```go
		out[key] = computePolicy(key, baseVal, roleInputs, nil)
		out[key] = computePolicy(key, base[key], roleInputs, entries)
```

- [ ] **Step 5: GREEN を確認する**

```powershell
& (Join-Path (go env GOROOT) "bin\gofmt.exe") -s -w internal\core\role\role_service.go internal\core\role\plugin_policy.go internal\core\role\optout_aggregation_test.go
go vet ./internal/core/role
go test ./internal/core/role -count=1
```

Expected: PASS。**`internal/core/role` の全テスト（2000行超の `plugin_policy_test.go` / `role_service_test.go`、intersection/priority の `optout_aggregation_test.go`、`cond_formula_test.go`、`users_with_policy_test.go` を含む）が1つも落ちないこと**。これが挙動保存の証拠になる。

- [ ] **Step 6: commit する**

```powershell
git diff --check
git diff --stat internal/core/role
git add internal/core/role/role_service.go internal/core/role/plugin_policy.go internal/core/role/optout_aggregation_test.go
git commit -m "Refactor role: carry role identity into policy aggregation"
```

Expected: 1 commit。`git diff --stat` は3ファイル。**この commit の SHA を控えておく**。

---

### Task 4: Role Policy Replacement (Public Contract, Application, Conflict)

`ReplaceRoleID` を公開面に足し、host が「1つのactive manual roleのnative contribution」をそのroleの**宣言済みpriorityのまま**置換できるようにする。競合は (role, key) 単位でnativeへ戻し、checked resolver が固定sentinelを返す。

**Files:**
- Test: `internal/effectivepolicy/validation_test.go`
- Modify: `plugin/policy.go`（`EffectivePolicyContribution` に `ReplaceRoleID`）
- Modify: `internal/effectivepolicy/validation.go`（`ValidateContributions` の署名と置換規則、末尾に `declaresActiveRole`）
- Modify: `internal/core/role/plugin_policy.go`（`activeRoleIDsFromAssignments`、sentinel、map 宣言、provider ループ内、集約ループ、return、`lessPolicyContribution`、helper 群）
- Modify: `plugin/plugintest/plugintest.go`（3引数化 + `plugintestActiveRoleIDs`、後段で `ActiveAssignments` の複製）
- Test: `internal/core/role/plugin_policy_test.go`、`plugin/plugintest/policy_test.go`
- Modify: `internal/entitycompat/testdata/golden_plugin_surface.txt`（再生成）
- Modify: `docs/plugins/authoring.md`（公開面一覧の `EffectivePolicyContribution`）

**Interfaces:**
- Consumes: `plugin.ActiveRoleAssignment` / `ActiveAssignments`（Task 1）、`resolveUserRoleSnapshot` / `pluginActiveRoleAssignments`（Task 2）、`rolePolicyInput` / `rolePolicyEntry`（Task 3）
- Produces: `plugin.EffectivePolicyContribution.ReplaceRoleID string`
- Produces: `effectivepolicy.ValidateContributions(keys, activeRoles []string, contributions []plugin.EffectivePolicyContribution) bool`（第2引数新增）
- Produces: 非公開 `role.activeRoleIDsFromAssignments([]plugin.ActiveRoleAssignment) []string`
- Produces: `role.ErrEffectivePolicyReplacementConflict`（`errors.Is` で辿れる固定sentinel）
- Produces: 非公開 `role.collectPolicyReplacement(...)` / `role.applyPolicyReplacements(...)` / `role.hasPolicyReplacementConflict(...)`
- Preserves: `role.ErrEffectivePolicyProvider` は**単独のときは素の sentinel として**返す（既存の `require.Equal` 前提を壊さない）

- [ ] **Step 1: validation の失敗 test を先に書く**

`internal/effectivepolicy/validation_test.go` の `TestValidateContributions` の table struct に `roles []string` フィールドを追加し、実行部を

```go
			assert.Equal(t, tc.valid, ValidateContributions(tc.keys, tc.roles, tc.contributions))
```

に、その上で次の case を table の末尾に追加する:

```go
		// --- 置換 ---
		{name: "valid replacement", keys: []string{"mentionLimit"}, roles: []string{"r1"},
			contributions: []plugin.EffectivePolicyContribution{{Key: "mentionLimit", Value: 40, ReplaceRoleID: "r1"}}, valid: true},
		{name: "replacement of a role that is not active", keys: []string{"mentionLimit"}, roles: []string{"r1"},
			contributions: []plugin.EffectivePolicyContribution{{Key: "mentionLimit", Value: 40, ReplaceRoleID: "r2"}}},
		{name: "replacement with no active roles at all", keys: []string{"mentionLimit"},
			contributions: []plugin.EffectivePolicyContribution{{Key: "mentionLimit", Value: 40, ReplaceRoleID: "r1"}}},
		{name: "replacement choosing a priority", keys: []string{"mentionLimit"}, roles: []string{"r1"},
			contributions: []plugin.EffectivePolicyContribution{{Key: "mentionLimit", Value: 40, Priority: 1, ReplaceRoleID: "r1"}}},
		{name: "replacement choosing an order", keys: []string{"mentionLimit"}, roles: []string{"r1"},
			contributions: []plugin.EffectivePolicyContribution{{Key: "mentionLimit", Value: 40, Order: 3, ReplaceRoleID: "r1"}}},
		{name: "replacement of an undeclared key", keys: []string{"canSearchNotes"}, roles: []string{"r1"},
			contributions: []plugin.EffectivePolicyContribution{{Key: "canSearchUsers", Value: 40, ReplaceRoleID: "r1"}}},
		{name: "replacement of an unknown native key", keys: []string{"unknown"}, roles: []string{"r1"},
			contributions: []plugin.EffectivePolicyContribution{{Key: "unknown", Value: 40, ReplaceRoleID: "r1"}}},
		{name: "replacement with a wrong value type", keys: []string{"mentionLimit"}, roles: []string{"r1"},
			contributions: []plugin.EffectivePolicyContribution{{Key: "mentionLimit", Value: "40", ReplaceRoleID: "r1"}}},
		// **同じ key でも role が違えば別 tie。** 複数 role を同時に置換する plugin を
		// 「重複」で弾かない。
		{name: "two roles replaced for the same key", keys: []string{"mentionLimit"}, roles: []string{"r1", "r2"},
			contributions: []plugin.EffectivePolicyContribution{
				{Key: "mentionLimit", Value: 40, ReplaceRoleID: "r1"},
				{Key: "mentionLimit", Value: 50, ReplaceRoleID: "r2"},
			}, valid: true},
		{name: "same role replaced twice", keys: []string{"mentionLimit"}, roles: []string{"r1"},
			contributions: []plugin.EffectivePolicyContribution{
				{Key: "mentionLimit", Value: 40, ReplaceRoleID: "r1"},
				{Key: "mentionLimit", Value: 50, ReplaceRoleID: "r1"},
			}},
```

`internal/effectivepolicy/validation_test.go` の末尾に追加する:

```go
// **置換は (Key, ReplaceRoleID) で一意。** (Key, Order) だけで判定すると、同じ key の
// 2 つの role を同時に置換する plugin が「重複」で弾かれる。置換の Order は 0 しか
// 選べないので、role ID を含めないと同時置換ができない。
func TestValidateContributionsReplacementTieUsesRoleID(t *testing.T) {
	two := []plugin.EffectivePolicyContribution{
		{Key: "mentionLimit", Value: 40, ReplaceRoleID: "r1"},
		{Key: "mentionLimit", Value: 50, ReplaceRoleID: "r2"},
	}
	assert.True(t, ValidateContributions([]string{"mentionLimit"}, []string{"r1", "r2"}, two))

	same := []plugin.EffectivePolicyContribution{
		{Key: "mentionLimit", Value: 40, ReplaceRoleID: "r1"},
		{Key: "mentionLimit", Value: 50, ReplaceRoleID: "r1"},
	}
	assert.False(t, ValidateContributions([]string{"mentionLimit"}, []string{"r1"}, same))
}
```

- [ ] **Step 2: RED を観察する**

```powershell
go test ./internal/effectivepolicy -count=1
```

Expected: **コンパイルエラー**。`unknown field ReplaceRoleID` と `not enough arguments in call to ValidateContributions` の両方。公開型も引数もまだ無いので当然 red。

- [ ] **Step 3: production を実装する（公開型 + validation + 呼び出し側配線）**

`plugin/policy.go` の `EffectivePolicyContribution` の `Order int` の**後**にフィールドを追加する:

```go
	// ReplaceRoleID turns this contribution into a **replacement** of one active
	// manual role's native contribution for Key, instead of an additional
	// contribution.
	//
	// - "" (the default) keeps the existing additive behaviour.
	// - non-empty names a role that must appear in
	//   [EffectivePolicyRequest.ActiveAssignments]. Conditional roles are never
	//   there, so they cannot be replaced.
	//
	// **置換は 1 対 1。** 対象 role/key の native contribution だけを差し替え、priority は
	// **元 native entry の宣言値を引き継ぐ**。他 role の contribution と通常の
	// priority / type 集約はそのまま行う。
	//
	// **Priority と Order は 0 のまま渡すこと。** 置き換える native entry が持った priority
	// を引き継ぐので、plugin が選んでよいと二重定義になる。host は 0 以外を malformed
	// として provider 全体を失敗扱いにする。
	//
	// 同じ role/key を複数 provider が置換した場合は**競合**となり、host はその pair だけを
	// native contribution に戻す。checked 解決は error、unchecked 解決は native fallback
	// map を返す。
	ReplaceRoleID string
```

`internal/effectivepolicy/validation.go` の `ValidateContributions` を置き換える:

```go
// ValidateContributions reports whether contributions satisfy the host's native
// policy schema.
//
// activeRoles are the role IDs the request carried in
// [plugin.EffectivePolicyRequest.ActiveAssignments]. A contribution that sets
// ReplaceRoleID must name one of them — otherwise it would replace a
// contribution that does not exist in this request — and must leave Priority and
// Order at 0, because the replaced entry inherits the role's own declared
// priority. Breaking either rule fails the whole provider, like any other
// malformed output.
func ValidateContributions(keys, activeRoles []string, contributions []plugin.EffectivePolicyContribution) bool {
	type contributionTie struct {
		key string
		// order only distinguishes additive contributions; a replacement always
		// carries 0, so replacements are told apart by roleID instead.
		order  int
		roleID string
	}
	seen := make(map[contributionTie]struct{}, len(contributions))
	for _, contribution := range contributions {
		if !declaresKey(keys, contribution.Key) || contribution.Priority < 0 || contribution.Priority > 2 {
			return false
		}
		if contribution.ReplaceRoleID != "" &&
			(contribution.Priority != 0 || contribution.Order != 0 || !declaresActiveRole(activeRoles, contribution.ReplaceRoleID)) {
			return false
		}
		native, ok := defaults[contribution.Key]
		if !ok {
			return false
		}
		tie := contributionTie{key: contribution.Key, order: contribution.Order, roleID: contribution.ReplaceRoleID}
		if _, duplicate := seen[tie]; duplicate {
			return false
		}
		seen[tie] = struct{}{}
		if !contribution.UseDefault && !valueValid(contribution.Key, native, contribution.Value) {
			return false
		}
	}
	return true
}

// declaresActiveRole reports whether roleID is one of the request's active manual
// roles. Only a role that carries a native contribution can have it replaced.
func declaresActiveRole(activeRoles []string, roleID string) bool {
	for _, active := range activeRoles {
		if active == roleID {
			return true
		}
	}
	return false
}
```

`internal/core/role/plugin_policy.go` の `func activeRoleIDs(` の**直前**に helper を追加し、`resolvePolicyProviderCached` 内の呼び出しを3引数にする:

```go
// activeRoleIDsFromAssignments は request の assignment が覆う role ID をソート・重複
// 除去して返す。**置換が名乗ってよい target の host 側の姿**。
func activeRoleIDsFromAssignments(assignments []plugin.ActiveRoleAssignment) []string {
	if len(assignments) == 0 {
		return nil
	}
	out := make([]string, 0, len(assignments))
	seen := make(map[string]struct{}, len(assignments))
	for _, a := range assignments {
		if a.RoleID == "" {
			continue
		}
		if _, duplicate := seen[a.RoleID]; duplicate {
			continue
		}
		seen[a.RoleID] = struct{}{}
		out = append(out, a.RoleID)
	}
	sort.Strings(out)
	return out
}
```

```go
	if ok {
		ok = effectivepolicy.ValidateContributions(provider.reg.Keys, activeRoleIDsFromAssignments(req.ActiveAssignments), contributions)
	}
```

`plugin/plugintest/plugintest.go` の wrapper 内で `ValidateContributions(registration.Keys, contributions)` を3引数にし、ファイル末尾に helper を追加する（`sort` は既に使っている）:

```go
			if err == nil && !effectivepolicy.ValidateContributions(
				registration.Keys, plugintestActiveRoleIDs(req.ActiveAssignments), contributions,
			) {
```

```go
// plugintestActiveRoleIDs は request の assignment が覆う role ID をソート・重複除去
// して返す。置換が名乗ってよい target を harness 側で判定するために使う
//（core/role の activeRoleIDsFromAssignments と同じ形）。
func plugintestActiveRoleIDs(assignments []plugin.ActiveRoleAssignment) []string {
	if len(assignments) == 0 {
		return nil
	}
	out := make([]string, 0, len(assignments))
	seen := make(map[string]struct{}, len(assignments))
	for _, a := range assignments {
		if a.RoleID == "" {
			continue
		}
		if _, duplicate := seen[a.RoleID]; duplicate {
			continue
		}
		seen[a.RoleID] = struct{}{}
		out = append(out, a.RoleID)
	}
	sort.Strings(out)
	return out
}
```

**この段階では置換はまだ適用されない。** validation が通るようになっただけで、host は置換を additive contribution として扱う（既存挙動のまま）。

- [ ] **Step 4: validation の GREEN を確認する**

```powershell
& (Join-Path (go env GOROOT) "bin\gofmt.exe") -s -w plugin\policy.go plugin\plugintest\plugintest.go internal\effectivepolicy\validation.go internal\effectivepolicy\validation_test.go
go test ./internal/effectivepolicy -count=1
go test ./internal/core/role -count=1
go test ./plugin/plugintest -count=1 -run "TestEffectivePolic"
```

Expected: 全部 PASS。`internal/core/role` の既存 provider テストが緑であること（まだ置換を適用していないので、既存 contribution の意味は変わっていない）。

- [ ] **Step 5: host 適用の失敗 test を先に書く**

`internal/core/role/plugin_policy_test.go` の末尾に追記する:

```go
// 置換は native priority を引き継ぐ。**priority 0 で押し込まない。**
//
//	r1: mentionLimit priority 1 = 10（native 結果は 10）
//	r2: mentionLimit priority 0 = 100
//	r1 を 40 に置換 → priority 1 の group だけが集約されるので 40。
//	priority 0 で押し込んでいたら priority 1 group が消えて max(40, 100) = 100。
func TestEffectivePolicy_ReplacementKeepsTheNativePriority(t *testing.T) {
	svc, roleRepo, assignRepo, _ := newTestService(t)
	roleRepo.Roles["r1"] = &model.Role{ID: "r1", Name: "A", Target: model.RoleTargetManual,
		Policies: datatypes.JSON([]byte(`{"mentionLimit":{"priority":1,"value":10}}`))}
	roleRepo.Roles["r2"] = &model.Role{ID: "r2", Name: "B", Target: model.RoleTargetManual,
		Policies: datatypes.JSON([]byte(`{"mentionLimit":{"priority":0,"value":100}}`))}
	assign(t, assignRepo, "u1", "r1")
	assign(t, assignRepo, "u1", "r2")

	// 置換が無ければ priority 1 の group が 10 で決まる。
	policies, err := svc.GetUserPoliciesChecked("u1")
	require.NoError(t, err)
	require.Equal(t, 10, policies["mentionLimit"], "native は priority 1 の group だけで決まる")

	registerProvider(t, svc, "level", []string{"mentionLimit"},
		func(_ context.Context, req plugin.EffectivePolicyRequest) ([]plugin.EffectivePolicyContribution, error) {
			return []plugin.EffectivePolicyContribution{{
				Key: "mentionLimit", Value: 40, ReplaceRoleID: req.ActiveAssignments[0].RoleID,
			}}, nil
		})

	policies, err = svc.GetUserPoliciesChecked("u1")
	require.NoError(t, err)
	assert.Equal(t, 40, policies["mentionLimit"], "置換は native の priority を引き継ぐので r2 の 100 に負けない")
}

// **置換でも explicit は contribution の値に従う。** explicit は intersection する policy
// だけで意味を持ち、「そのロールが明示的に設定したか」を表す。どちらのロールも key を
// 宣言していないので native は base 参加 = 明示設定なしとして intersection には何も
// 乗らない。r1 を明示値で置換すると初めて乗る。
func TestEffectivePolicy_ReplacementCarriesTheExplicitFlagForIntersection(t *testing.T) {
	svc, roleRepo, assignRepo, _ := newTestService(t)
	roleRepo.Roles["r1"] = &model.Role{ID: "r1", Name: "A", Target: model.RoleTargetManual}
	roleRepo.Roles["r2"] = &model.Role{ID: "r2", Name: "B", Target: model.RoleTargetManual}
	assign(t, assignRepo, "u1", "r1")
	assign(t, assignRepo, "u1", "r2")

	policies, err := svc.GetUserPoliciesChecked("u1")
	require.NoError(t, err)
	require.Equal(t, []string{}, policies[role.PolicyOptOutNotificationTypes], "どちらも未設定なので intersection は空")

	registerProvider(t, svc, "level", []string{role.PolicyOptOutNotificationTypes},
		func(_ context.Context, req plugin.EffectivePolicyRequest) ([]plugin.EffectivePolicyContribution, error) {
			return []plugin.EffectivePolicyContribution{{
				Key: role.PolicyOptOutNotificationTypes, Value: []string{"note"}, ReplaceRoleID: "r1",
			}}, nil
		})

	policies, err = svc.GetUserPoliciesChecked("u1")
	require.NoError(t, err)
	assert.Equal(t, []string{"note"}, policies[role.PolicyOptOutNotificationTypes],
		"明示値での置換だけが intersection に参加する")
}

// **`UseDefault: true` の置換は「この role の override を base に戻す」** = native の
// useDefault と同じ扱い。explicit は立たない。
func TestEffectivePolicy_ReplacementWithUseDefaultStaysUnset(t *testing.T) {
	svc, roleRepo, assignRepo, _ := newTestService(t)
	roleRepo.Roles["r1"] = &model.Role{ID: "r1", Name: "A", Target: model.RoleTargetManual}
	roleRepo.Roles["r2"] = &model.Role{ID: "r2", Name: "B", Target: model.RoleTargetManual}
	assign(t, assignRepo, "u1", "r1")
	assign(t, assignRepo, "u1", "r2")
	registerProvider(t, svc, "level", []string{role.PolicyOptOutNotificationTypes},
		func(_ context.Context, req plugin.EffectivePolicyRequest) ([]plugin.EffectivePolicyContribution, error) {
			return []plugin.EffectivePolicyContribution{{
				Key: role.PolicyOptOutNotificationTypes, UseDefault: true, Value: []string{"note"}, ReplaceRoleID: "r1",
			}}, nil
		})

	policies, err := svc.GetUserPoliciesChecked("u1")
	require.NoError(t, err)
	assert.Equal(t, []string{}, policies[role.PolicyOptOutNotificationTypes],
		"UseDefault の置換は明示設定にならず intersection に参加しない")
}

// **競合は pair 単位。** provider 全体を失敗扱いにしてしまうと、その provider の無関係な
// key まで native に戻ってしまう。置換したい plugin だけを黙らせる形の被害を出さない。
func TestEffectivePolicy_ReplacementConflictFallsBackToNative(t *testing.T) {
	svc, roleRepo, assignRepo, _ := newTestService(t)
	roleRepo.Roles["r1"] = &model.Role{ID: "r1", Name: "A", Target: model.RoleTargetManual,
		Policies: datatypes.JSON([]byte(`{"mentionLimit":{"priority":1,"value":10}}`))}
	assign(t, assignRepo, "u1", "r1")
	replace := func(value int) plugin.EffectivePolicyResolver {
		return func(_ context.Context, req plugin.EffectivePolicyRequest) ([]plugin.EffectivePolicyContribution, error) {
			return []plugin.EffectivePolicyContribution{{
				Key: "mentionLimit", Value: value, ReplaceRoleID: req.ActiveAssignments[0].RoleID,
			}}, nil
		}
	}
	registerProvider(t, svc, "level-a", []string{"mentionLimit"}, replace(40))
	registerProvider(t, svc, "level-b", []string{"mentionLimit"}, replace(90))

	policies, err := svc.GetUserPoliciesChecked("u1")
	require.ErrorIs(t, err, role.ErrEffectivePolicyReplacementConflict)
	assert.Equal(t, 10, policies["mentionLimit"], "競合した pair は管理者が設定した native 値が残る")
	assert.NotErrorIs(t, err, role.ErrEffectivePolicyProvider, "競合は provider 失敗と区別する")
}

// 競合した key 以外は、**両 provider の通常contributionを通常通り集約する。**
func TestEffectivePolicy_ReplacementConflictKeepsOtherContributions(t *testing.T) {
	svc, roleRepo, assignRepo, _ := newTestService(t)
	roleRepo.Roles["r1"] = &model.Role{ID: "r1", Name: "A", Target: model.RoleTargetManual,
		Policies: datatypes.JSON([]byte(`{"mentionLimit":{"priority":1,"value":10}}`))}
	assign(t, assignRepo, "u1", "r1")
	build := func(mentionLimit, userListLimit int) plugin.EffectivePolicyResolver {
		return func(_ context.Context, req plugin.EffectivePolicyRequest) ([]plugin.EffectivePolicyContribution, error) {
			return []plugin.EffectivePolicyContribution{
				{Key: "mentionLimit", Value: mentionLimit, ReplaceRoleID: req.ActiveAssignments[0].RoleID},
				{Key: "userListLimit", Priority: 1, Value: userListLimit},
			}, nil
		}
	}
	registerProvider(t, svc, "level-a", []string{"mentionLimit", "userListLimit"}, build(40, 50))
	registerProvider(t, svc, "level-b", []string{"mentionLimit", "userListLimit"}, build(90, 60))

	policies, err := svc.GetUserPoliciesChecked("u1")
	require.ErrorIs(t, err, role.ErrEffectivePolicyReplacementConflict)
	assert.Equal(t, 10, policies["mentionLimit"], "競合した pair だけ native")
	assert.Equal(t, 60, policies["userListLimit"], "競合していない key は両 provider の contribution を集約する")
}

// **provider 失敗は宣言 key を native へ戻す（既存挙動）。** 他 provider の置換も同じ key
// なら巻き戻る。既存 sentinel は単独のときは素のまま。
func TestEffectivePolicy_FailedProviderWinsOverAnotherProvidersReplacement(t *testing.T) {
	svc, roleRepo, assignRepo, _ := newTestService(t)
	roleRepo.Roles["r1"] = &model.Role{ID: "r1", Name: "A", Target: model.RoleTargetManual,
		Policies: datatypes.JSON([]byte(`{"mentionLimit":{"priority":1,"value":10}}`))}
	assign(t, assignRepo, "u1", "r1")
	registerProvider(t, svc, "broken", []string{"mentionLimit"},
		func(context.Context, plugin.EffectivePolicyRequest) ([]plugin.EffectivePolicyContribution, error) {
			return nil, errors.New("provider storage failure")
		})
	registerProvider(t, svc, "level", []string{"mentionLimit"},
		func(_ context.Context, req plugin.EffectivePolicyRequest) ([]plugin.EffectivePolicyContribution, error) {
			return []plugin.EffectivePolicyContribution{{
				Key: "mentionLimit", Value: 40, ReplaceRoleID: req.ActiveAssignments[0].RoleID,
			}}, nil
		})

	policies, err := svc.GetUserPoliciesChecked("u1")
	require.Equal(t, role.ErrEffectivePolicyProvider, err, "単独の provider 失敗は素の sentinel")
	assert.Equal(t, 10, policies["mentionLimit"], "失敗 provider の宣言 key は native へ戻る")
}

// **provider 失敗と競合が同時に起きたら両方の error が errors.Is で辿れる。**
func TestEffectivePolicy_JoinsProviderFailureAndReplacementConflict(t *testing.T) {
	svc, roleRepo, assignRepo, _ := newTestService(t)
	roleRepo.Roles["r1"] = &model.Role{ID: "r1", Name: "A", Target: model.RoleTargetManual,
		Policies: datatypes.JSON([]byte(`{"mentionLimit":{"priority":1,"value":10}}`))}
	assign(t, assignRepo, "u1", "r1")
	replace := func(value int) plugin.EffectivePolicyResolver {
		return func(_ context.Context, req plugin.EffectivePolicyRequest) ([]plugin.EffectivePolicyContribution, error) {
			return []plugin.EffectivePolicyContribution{{
				Key: "mentionLimit", Value: value, ReplaceRoleID: req.ActiveAssignments[0].RoleID,
			}}, nil
		}
	}
	registerProvider(t, svc, "broken", []string{"canSearchNotes"},
		func(context.Context, plugin.EffectivePolicyRequest) ([]plugin.EffectivePolicyContribution, error) {
			return nil, errors.New("provider storage failure")
		})
	registerProvider(t, svc, "level-a", []string{"mentionLimit"}, replace(40))
	registerProvider(t, svc, "level-b", []string{"mentionLimit"}, replace(90))

	_, err := svc.GetUserPoliciesChecked("u1")
	require.ErrorIs(t, err, role.ErrEffectivePolicyProvider)
	require.ErrorIs(t, err, role.ErrEffectivePolicyReplacementConflict)
}

// **unchecked 解決は fallback map を返すだけ。** error は地表に出さない。
func TestEffectivePolicy_ReplacementConflictUncheckedResolutionFallsBack(t *testing.T) {
	svc, roleRepo, assignRepo, _ := newTestService(t)
	roleRepo.Roles["r1"] = &model.Role{ID: "r1", Name: "A", Target: model.RoleTargetManual,
		Policies: datatypes.JSON([]byte(`{"mentionLimit":{"priority":1,"value":10}}`))}
	assign(t, assignRepo, "u1", "r1")
	replace := func(value int) plugin.EffectivePolicyResolver {
		return func(_ context.Context, req plugin.EffectivePolicyRequest) ([]plugin.EffectivePolicyContribution, error) {
			return []plugin.EffectivePolicyContribution{{
				Key: "mentionLimit", Value: value, ReplaceRoleID: req.ActiveAssignments[0].RoleID,
			}}, nil
		}
	}
	registerProvider(t, svc, "level-a", []string{"mentionLimit"}, replace(40))
	registerProvider(t, svc, "level-b", []string{"mentionLimit"}, replace(90))

	policies := svc.GetUserPolicies("u1")
	assert.Equal(t, 10, policies["mentionLimit"])
}

// **置換が active でない role を名乗れば provider 全体が失敗扱い。** malformed output と
// 同じ扱いなので、宣言 key は native へ戻る。
func TestEffectivePolicy_ReplacementOfInactiveRoleFailsTheProvider(t *testing.T) {
	svc, roleRepo, assignRepo, _ := newTestService(t)
	roleRepo.Roles["r1"] = &model.Role{ID: "r1", Name: "A", Target: model.RoleTargetManual,
		Policies: datatypes.JSON([]byte(`{"mentionLimit":{"priority":1,"value":10}}`))}
	assign(t, assignRepo, "u1", "r1")
	registerProvider(t, svc, "level", []string{"mentionLimit"},
		func(context.Context, plugin.EffectivePolicyRequest) ([]plugin.EffectivePolicyContribution, error) {
			return []plugin.EffectivePolicyContribution{{Key: "mentionLimit", Value: 40, ReplaceRoleID: "r-other"}}, nil
		})

	policies, err := svc.GetUserPoliciesChecked("u1")
	require.Equal(t, role.ErrEffectivePolicyProvider, err)
	assert.Equal(t, 10, policies["mentionLimit"])
}

// **置換で priority を選ぶと provider 全体が失敗扱い。** 0 以外は malformed。
func TestEffectivePolicy_ReplacementChoosingAPriorityFailsTheProvider(t *testing.T) {
	svc, roleRepo, assignRepo, _ := newTestService(t)
	roleRepo.Roles["r1"] = &model.Role{ID: "r1", Name: "A", Target: model.RoleTargetManual,
		Policies: datatypes.JSON([]byte(`{"mentionLimit":{"priority":1,"value":10}}`))}
	assign(t, assignRepo, "u1", "r1")
	registerProvider(t, svc, "level", []string{"mentionLimit"},
		func(_ context.Context, req plugin.EffectivePolicyRequest) ([]plugin.EffectivePolicyContribution, error) {
			return []plugin.EffectivePolicyContribution{{
				Key: "mentionLimit", Value: 40, Priority: 1, ReplaceRoleID: req.ActiveAssignments[0].RoleID,
			}}, nil
		})

	policies, err := svc.GetUserPoliciesChecked("u1")
	require.Equal(t, role.ErrEffectivePolicyProvider, err)
	assert.Equal(t, 10, policies["mentionLimit"])
}
```

- [ ] **Step 6: RED を観察する**

```powershell
go test ./internal/core/role -count=1 -run "TestEffectivePolicy_Replacement|TestEffectivePolicy_FailedProviderWins|TestEffectivePolicy_JoinsProviderFailure"
```

Expected: **コンパイルエラー**（`undefined: role.ErrEffectivePolicyReplacementConflict`）が最初の RED。sentinel を足した後も、置換がまだ additive 扱いのままなので `TestEffectivePolicy_ReplacementKeepsTheNativePriority` の `assert.Equal(t, 40, ...)` が 100 あたりで落ちている。

- [ ] **Step 7: host の置換適用と競合を実装する**

`internal/core/role/plugin_policy.go` の `ErrEffectivePolicyProvider` 定義の**直後**に sentinel を追加する:

```go
// ErrEffectivePolicyReplacementConflict is returned by GetUserPoliciesChecked
// when two providers replaced the same role and policy key. Fixed and
// identifier-free, like [ErrEffectivePolicyProvider]: which roles collided is an
// operator concern and never reaches the caller.
//
// The contested pair keeps its native contribution; every other contribution in
// the same result is still applied. It is joined with [ErrEffectivePolicyProvider]
// when a provider also failed in the same request, so `errors.Is` finds both.
var ErrEffectivePolicyReplacementConflict = errors.New("effective policy replacement conflict")
```

`resolvePolicies` の

```go
	// key -> provider contribution entries (同一 priority cascade に参加させる)。
	contribs := make(map[string][]policyEntry)
	// 失敗したproviderの宣言keyはplugin貢献をすべて破棄してnative結果へ戻す。
	failed := make(map[string]bool)
```

を次のように置き換える:

```go
	// key -> provider contribution entries (同一 priority cascade に参加させる)。
	contribs := make(map[string][]policyEntry)
	// key -> roleID -> この要求で受理した置換 entry。
	replacements := make(map[string]map[string]policyEntry)
	// key -> roleID: 複数 provider が同じ role/key を置換した競合。
	conflicted := make(map[string]map[string]bool)
	// 失敗したproviderの宣言keyはplugin貢献をすべて破棄してnative結果へ戻す。
	failed := make(map[string]bool)
	// 置換は元の native priority を引き継ぐので、role ごとの overrides を引けるように
	// しておく。
	overridesByRole := make(map[string]map[string]rolePolicyOverride, len(roleInputs))
	for _, in := range roleInputs {
		if in.roleID != "" {
			overridesByRole[in.roleID] = in.overrides
		}
	}
```

provider 結果を畳むループの**内側**を次のように変更する:

```go
		for _, c := range res {
			if c.ReplaceRoleID != "" {
				collectPolicyReplacement(replacements, conflicted, overridesByRole, c, base[c.Key])
				continue
			}
			baseVal := base[c.Key]
			value := c.Value
			if c.UseDefault {
				value = baseVal
			}
			value = clonePolicyValue(value)
			// UseDefault のときは base を積むだけなので explicit ではない (#2898、
			// intersection の集約で「設定していない」と区別する)。
			contribs[c.Key] = append(contribs[c.Key], policyEntry{priority: c.Priority, value: value, explicit: !c.UseDefault})
		}
```

provider ループの**後**にある集約ループ

```go
	for key, entries := range contribs {
		out[key] = computePolicy(key, base[key], roleInputs, entries)
	}
```

を次のように置き換える:

```go
	// 置換が 1 件でもあれば、置換 entry 付きで集約し直す。競合した pair は accepted から
	// 消してあるので、そこは native のままになる。
	if len(replacements) > 0 {
		replacedInputs := applyPolicyReplacements(roleInputs, replacements)
		for key, byRole := range replacements {
			if len(byRole) == 0 {
				// **この key の置換が全部競合した。** native 集約の結果 out[key] を
				// そのまま残し、他の provider の通常contribution だけ足し直す。
				continue
			}
			out[key] = computePolicy(key, base[key], replacedInputs, contribs[key])
		}
	}
	for key, entries := range contribs {
		if len(replacements[key]) > 0 {
			continue // 上の loop で置換込みで計算済み
		}
		out[key] = computePolicy(key, base[key], roleInputs, entries)
	}
```

`resolvePolicies` の最後（`out = s.applyServerCaps(out)` のあと、旧 `if len(failed) > 0 { return out, ErrEffectivePolicyProvider }` の位置）を次のように置き換える:

```go
	out = s.applyServerCaps(out)
	providerFailed := len(failed) > 0
	conflict := hasPolicyReplacementConflict(conflicted)
	// **単独のときは素の sentinel を返す。** 既存テストも既存呼び出し側も
	// `err == ErrEffectivePolicyProvider` 相当を前提にしているため、1 つしか無いのに join
	// すると等価性が壊れる。
	switch {
	case providerFailed && conflict:
		return out, errors.Join(ErrEffectivePolicyProvider, ErrEffectivePolicyReplacementConflict)
	case providerFailed:
		return out, ErrEffectivePolicyProvider
	case conflict:
		return out, ErrEffectivePolicyReplacementConflict
	default:
		return out, nil
	}
```

`func activeRoleIDs(` の**直前**に helper 群を追加する:

```go
// hasPolicyReplacementConflict reports whether any (key, role) pair was contested.
func hasPolicyReplacementConflict(conflicted map[string]map[string]bool) bool {
	for _, roles := range conflicted {
		if len(roles) > 0 {
			return true
		}
	}
	return false
}

// collectPolicyReplacement records one accepted replacement, or marks the (key,
// role) pair as contested when a second provider already replaced it.
//
// **置く entry は元 native entry を改変したもの**なので、declared priority と `explicit`
// の既定（その role が key を宣言していなければ base 参加 = explicit でない）を引き継ぎ、
// 値だけ contrib の Value に差し替える。
//
// **競合は pair 単位で落とす。** provider 全体を失敗扱いにしてしまうと、置換していない
// 無関係な key まで native へ戻すことになる。どちらの値も採らない = 管理者が設定した
// role の値が残るので、fallback の向きは安全。
func collectPolicyReplacement(
	accepted map[string]map[string]policyEntry,
	conflicted map[string]map[string]bool,
	overridesByRole map[string]map[string]rolePolicyOverride,
	contribution plugin.EffectivePolicyContribution,
	baseVal any,
) {
	byRole := accepted[contribution.Key]
	if byRole == nil {
		byRole = make(map[string]policyEntry, 1)
		accepted[contribution.Key] = byRole
	}
	if conflicted[contribution.Key][contribution.ReplaceRoleID] {
		return
	}
	if _, taken := byRole[contribution.ReplaceRoleID]; taken {
		delete(byRole, contribution.ReplaceRoleID)
		roles := conflicted[contribution.Key]
		if roles == nil {
			roles = make(map[string]bool, 1)
			conflicted[contribution.Key] = roles
		}
		roles[contribution.ReplaceRoleID] = true
		return
	}
	entry := rolePolicyEntry(overridesByRole[contribution.ReplaceRoleID], contribution.Key, baseVal)
	// UseDefault は「この role の override を base に戻す」= native の useDefault と同じ
	// 扱い。値を使う場合は explicit な設定になる。
	entry.value = clonePolicyValue(baseVal)
	entry.explicit = false
	if !contribution.UseDefault {
		entry.value = clonePolicyValue(contribution.Value)
		entry.explicit = true
	}
	byRole[contribution.ReplaceRoleID] = entry
}

// applyPolicyReplacements returns a copy of inputs with the accepted replacement
// entries attached. 元の slice は触らないので native pass は自分の entry を保ったまま。
//
// **roleID が空の input には付けない。** 置換の target は必ず非空の role ID なので空の
// role ID に一致するはずはないが、この guard で「読めないロールに置換が混ざる」形を1行で
// 塞ぐ。
func applyPolicyReplacements(inputs []rolePolicyInput, replacements map[string]map[string]policyEntry) []rolePolicyInput {
	out := make([]rolePolicyInput, len(inputs))
	copy(out, inputs)
	for i := range out {
		if out[i].roleID == "" {
			continue
		}
		attached := make(map[string]policyEntry, len(replacements))
		for key, byRole := range replacements {
			if entry, ok := byRole[out[i].roleID]; ok {
				attached[key] = entry
			}
		}
		if len(attached) > 0 {
			out[i].replacements = attached
		}
	}
	return out
}
```

`lessPolicyContribution` に `ReplaceRoleID` の比較を足す:

```go
func lessPolicyContribution(a, b plugin.EffectivePolicyContribution) bool {
	if a.Order != b.Order {
		return a.Order < b.Order
	}
	// **置換は Order が 0 なので、置換同士は RoleID で順序を決める。** 空文字が先に来るので、
	// ReplaceRoleID を持たない contribution の並びは従来と同じ。
	if a.ReplaceRoleID != b.ReplaceRoleID {
		return a.ReplaceRoleID < b.ReplaceRoleID
	}
	if a.Key != b.Key {
		return a.Key < b.Key
	}
	if a.Priority != b.Priority {
		return a.Priority < b.Priority
	}
	if a.UseDefault != b.UseDefault {
		return !a.UseDefault
	}
	return false
}
```

- [ ] **Step 8: host 適用の GREEN を確認する**

```powershell
& (Join-Path (go env GOROOT) "bin\gofmt.exe") -s -w internal\core\role\plugin_policy.go internal\core\role\plugin_policy_test.go
go vet ./internal/core/role
go test ./internal/core/role -count=1
```

Expected: PASS。特に既存の `require.Equal(t, role.ErrEffectivePolicyProvider, err)` が素の sentinel 前提で通っていること（join して既定が壊れていない証拠）。

- [ ] **Step 9: plugintest の失敗 test を先に書いて RED を観察する**

`plugin/plugintest/policy_test.go` に追記する:

```go
func TestEffectivePoliciesCaptureActiveAssignments(t *testing.T) {
	assignments := []plugin.ActiveRoleAssignment{{RoleID: "role-a", AssignmentID: "assign-1"}}
	definition := plugin.Definition{
		Name: "policy", APIVersion: plugin.APIVersion,
		EffectivePolicies: func(plugin.Context, plugin.EffectivePolicyInvalidator) (plugin.EffectivePolicyRegistration, error) {
			return plugin.EffectivePolicyRegistration{
				Keys: []string{"canSearchNotes"},
				Resolve: func(_ context.Context, req plugin.EffectivePolicyRequest) ([]plugin.EffectivePolicyContribution, error) {
					req.ActiveAssignments[0].RoleID = "mutated"
					return nil, nil
				},
			}, nil
		},
	}
	registration := plugintest.New(t).EffectivePolicies(definition)
	require.NoError(t, registration.Validate())

	_, err := registration.Resolve(context.Background(), plugin.EffectivePolicyRequest{ActiveAssignments: assignments})
	require.NoError(t, err)
	assert.Equal(t, "role-a", assignments[0].RoleID, "resolver must not alias the caller's ActiveAssignments")
}

func TestEffectivePoliciesAcceptsValidReplacement(t *testing.T) {
	definition := plugin.Definition{
		Name: "policy", APIVersion: plugin.APIVersion,
		EffectivePolicies: func(plugin.Context, plugin.EffectivePolicyInvalidator) (plugin.EffectivePolicyRegistration, error) {
			return plugin.EffectivePolicyRegistration{
				Keys: []string{"mentionLimit"},
				Resolve: func(_ context.Context, req plugin.EffectivePolicyRequest) ([]plugin.EffectivePolicyContribution, error) {
					return []plugin.EffectivePolicyContribution{{
						Key: "mentionLimit", Value: 40, ReplaceRoleID: req.ActiveAssignments[0].RoleID,
					}}, nil
				},
			}, nil
		},
	}
	registration := plugintest.New(t).EffectivePolicies(definition)
	contributions, err := registration.Resolve(context.Background(), plugin.EffectivePolicyRequest{
		ActiveAssignments: []plugin.ActiveRoleAssignment{{RoleID: "role-a", AssignmentID: "assign-1"}},
	})
	require.NoError(t, err)
	require.Len(t, contributions, 1)
	assert.Equal(t, 40, contributions[0].Value)
	assert.Equal(t, "role-a", contributions[0].ReplaceRoleID)
}

func TestEffectivePoliciesRejectsInvalidReplacement(t *testing.T) {
	definition := plugin.Definition{
		Name: "policy", APIVersion: plugin.APIVersion,
		EffectivePolicies: func(plugin.Context, plugin.EffectivePolicyInvalidator) (plugin.EffectivePolicyRegistration, error) {
			return plugin.EffectivePolicyRegistration{
				Keys: []string{"mentionLimit"},
				Resolve: func(context.Context, plugin.EffectivePolicyRequest) ([]plugin.EffectivePolicyContribution, error) {
					// 宣言していない role の置換は host でも不正。harness が本番と同じ契約で
					// 弾くことを確認する。
					return []plugin.EffectivePolicyContribution{{
						Key: "mentionLimit", Value: 40, ReplaceRoleID: "role-not-active",
					}}, nil
				},
			}, nil
		},
	}
	if os.Getenv("MK_PLUGINTEST_INVALID_REPLACEMENT_CHILD") == "1" {
		registration := plugintest.New(t).EffectivePolicies(definition)
		_, _ = registration.Resolve(context.Background(), plugin.EffectivePolicyRequest{
			ActiveAssignments: []plugin.ActiveRoleAssignment{{RoleID: "role-a", AssignmentID: "assign-1"}},
		})
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestEffectivePoliciesRejectsInvalidReplacement$")
	cmd.Env = append(os.Environ(), "MK_PLUGINTEST_INVALID_REPLACEMENT_CHILD=1")
	output, err := cmd.CombinedOutput()
	require.Error(t, err, "an invalid replacement must fail the harness test process")
	assert.Contains(t, string(output), "EffectivePolicies の出力が不正です")
}

func TestEffectivePoliciesRejectsReplacementChoosingAPriority(t *testing.T) {
	definition := plugin.Definition{
		Name: "policy", APIVersion: plugin.APIVersion,
		EffectivePolicies: func(plugin.Context, plugin.EffectivePolicyInvalidator) (plugin.EffectivePolicyRegistration, error) {
			return plugin.EffectivePolicyRegistration{
				Keys: []string{"mentionLimit"},
				Resolve: func(context.Context, plugin.EffectivePolicyRequest) ([]plugin.EffectivePolicyContribution, error) {
					return []plugin.EffectivePolicyContribution{{
						Key: "mentionLimit", Value: 40, Priority: 1, ReplaceRoleID: "role-a",
					}}, nil
				},
			}, nil
		},
	}
	if os.Getenv("MK_PLUGINTEST_REPLACEMENT_PRIORITY_CHILD") == "1" {
		registration := plugintest.New(t).EffectivePolicies(definition)
		_, _ = registration.Resolve(context.Background(), plugin.EffectivePolicyRequest{
			ActiveAssignments: []plugin.ActiveRoleAssignment{{RoleID: "role-a", AssignmentID: "assign-1"}},
		})
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestEffectivePoliciesRejectsReplacementChoosingAPriority$")
	cmd.Env = append(os.Environ(), "MK_PLUGINTEST_REPLACEMENT_PRIORITY_CHILD=1")
	output, err := cmd.CombinedOutput()
	require.Error(t, err, "a replacement that chooses its own priority must fail the harness test process")
	assert.Contains(t, string(output), "EffectivePolicies の出力が不正です")
}
```

```powershell
go test ./plugin/plugintest -count=1 -run "TestEffectivePolic"
```

Expected: **`TestEffectivePoliciesCaptureActiveAssignments` だけが FAIL**（`role-a` が `mutated` になる = harness が `ActiveAssignments` を複製していない）。他の3本は Task 4 Step 3 の配線で既に緑なので **regression guard** として扱う。

- [ ] **Step 10: plugintest の実装を入れて GREEN にする**

`plugin/plugintest/plugintest.go` の wrapper 内で `req.RoleIDs = append([]string(nil), req.RoleIDs...)` の**直後**に1行追加する:

```go
			// **ActiveAssignments も複製して渡す。** 本番 (core/role/plugin_policy.go) が
			// 複製しているのと同じで、テストが production より緩くないようにする。
			req.ActiveAssignments = append([]plugin.ActiveRoleAssignment(nil), req.ActiveAssignments...)
```

```powershell
& (Join-Path (go env GOROOT) "bin\gofmt.exe") -s -w plugin\plugintest\plugintest.go plugin\plugintest\policy_test.go plugin\policy.go
go test ./plugin/plugintest -count=1 -run "TestEffectivePolic"
go test ./plugin ./internal/effectivepolicy -count=1
go test ./internal/core/role -count=1
```

Expected: 全部 PASS。

- [ ] **Step 11: golden を再生成して doc gate を RED にする**

**golden を先に書いて doc gate を赤くする**（doc を先に書くと `TestPluginDoc` は緑のままになる）:

```powershell
go run ./tools/pluginspec -write
git diff --stat internal/entitycompat/testdata/golden_plugin_surface.txt
go test ./internal/entitycompat -run TestPluginDoc -count=1
```

Expected: golden に `plugin:   field EffectivePolicyContribution.ReplaceRoleID string` の1行が加わり、**`go test` は FAIL** する（`ReplaceRoleID` が authoring.md の公開面一覧に無い）。

- [ ] **Step 12: docs を書いて GREEN にする**

`docs/plugins/authoring.md` の `type EffectivePolicyContribution struct` ブロックを次のように置き換える:

```
type EffectivePolicyContribution struct
  Key string
  Priority int
  UseDefault bool
  Value any
  Order int
  ReplaceRoleID string
```

```powershell
go test ./internal/entitycompat -run TestPluginDoc -count=1
go run ./tools/pluginspec > "$env:TEMP\rlp-surface.txt"
$golden = (Get-Content -Raw internal\entitycompat\testdata\golden_plugin_surface.txt) -replace "`r`n","`n"
$actual = (Get-Content -Raw "$env:TEMP\rlp-surface.txt") -replace "`r`n","`n"
if ($golden -ne $actual) { "SURFACE DRIFT" } else { "SURFACE OK" }
go vet ./plugin/... ./internal/effectivepolicy ./internal/core/role
```

Expected: PASS / `SURFACE OK`。

- [ ] **Step 13: commit する**

```powershell
git diff --check
git diff -- plugin internal/effectivepolicy internal/core/role internal/entitycompat/testdata docs/plugins/authoring.md
git add plugin/policy.go plugin/plugintest/plugintest.go plugin/plugintest/policy_test.go internal/effectivepolicy/validation.go internal/effectivepolicy/validation_test.go internal/core/role/plugin_policy.go internal/core/role/plugin_policy_test.go internal/entitycompat/testdata/golden_plugin_surface.txt docs/plugins/authoring.md
git commit -m "Add role: replace one active role's native policy contribution"
```

Expected: 1 commit。**この commit の SHA を控えておく**。

---

### Task 5: Authoring And Compatibility Documentation

公開面の一覧（Task 1 / 4 で更新済み）とは別に、**意味と運用**を `docs/plugins/authoring.md` の「効果ポリシー」節に書き足す。置換を書こうとした人が「優先度を書いたら落ちる」「conditional role は置換できない」を知らずに踏み抜けないようにする。

**Files:**
- Test/gate: Global Constraints の「authoring.md の Go fence コンパイル gate」
- Modify: `docs/plugins/authoring.md`（効果ポリシー節 4 箇所）、`docs/plugins/compatibility.md`（replacement の additive 追記）

**Interfaces:**
- Consumes: Task 1 / 2 / 4 の実契約
- Produces: なし（ドキュメントのみ）

- [ ] **Step 1: gate が生きていることを RED で証明する**

新しい Go fence を書く前に、**この gate が本当に赤くなる**ことを確認する（以降の Step で使う gate が空振りしていないことの担保）。

**関数名だけ書き換えては RED にならない。** `func effectivePolicies` を別名にリネームしても、その関数はどこからも呼ばれないので**有効な Go のまま**で、gate は赤くならない（最初の案はこれで RED を作れなかった）。**型エラー**を1つだけ入れて、3 variant すべてが落ちることを使う:

```powershell
$doc = Get-Content -Raw docs/plugins/authoring.md
# 効果ポリシー節の例。`Priority` は int なので、文字列を代入すると型エラーになる。
# この literal は authoring.md の中に1箇所しか無いので、他 fence を巻き込まない。
$broken = $doc -replace '\{Key: "driveCapacityMb", Priority: 1, Value: 1000\}', '{Key: "driveCapacityMb", Priority: "1", Value: 1000}'
if ($broken -eq $doc) { throw "mutation target not found - authoring.md の driveCapacityMb の例を探す" }
Set-Content -Path docs/plugins/authoring.md -Value $broken -Encoding utf8NoBOM
$mutated = @(Select-String -Path docs/plugins/authoring.md -Pattern 'Priority: "1", Value: 1000' -Encoding utf8)
if ($mutated.Count -ne 1) { throw "mutation must hit exactly one literal, got $($mutated.Count)" }
"mutated: $($mutated.Count) literal"
```

Expected: `mutated: 1 literal`。**0件なら** `-replace` の pattern が literal とずれている、**2件以上なら** target literal が一意ではないので、どちらも先に解消する（どちらも throw で止まる）。

Global Constraints の「authoring.md の Go fence コンパイル gate」ブロックをそのまま実行する。

Expected: `fences: <n>` の行に続けて、`NG: all variants failed for s<NN>`（1件以上）。**`SNIPPET GATE OK` を出してはいけない。** fence ID は doc を触る前後で変わるので固定しない。壊した fence の `_top` variant には noise でない型エラーの行が出る（形は概ね次のとおり。行番号・ID は固定しない）:

```
snippets\s<NN>_top\x.go:<line>:<col>: cannot use "1" (untyped string constant) as int value in struct literal
```

`NG:` が出ない、`SNIPPET GATE OK` が出る、あるいは `NG:` が出るのに上の型エラー行が見えない場合は、gate がまだ空振りしている（あるいは noise 判定が壊れている）ので、Global Constraints 側の判定ロジックを直してから次に進む。`fences: <n>` の行は必ず出るので、`fences:` の行そのものが無いなら `extract.py` の起動自体を疑う。

```powershell
git checkout -- docs/plugins/authoring.md
git status --short docs/plugins/authoring.md
```

Expected: 出力が空（ファイルが元に戻っている）。戻った後にもう1回 gate を実行し、`SNIPPET GATE OK` に戻ることを確認する。

- [ ] **Step 2: docs を書く（resolver の入力契約）**

`docs/plugins/authoring.md` の「効果ポリシー」節で、`Keys`は空・空文字・重複を許さず…で始まる段落の**末尾**に追記する:

```markdown
resolverは`req.UserID`のほかに、activeな手動ロールのassignmentを`req.ActiveAssignments`で受け取る。`RoleIDs`は従来どおりconditionalロールを含むが、`ActiveAssignments`は`role_assignment`の行を持つ手動ロールだけなので、各`RoleID`は`RoleIDs`の部分集合になる。ロールごとに高々1件で、並びは`RoleID`順、匿名解決では非nilの空sliceになる。期限切れ・削除済み・`role`行が無いorphanは含まれない。rolesとassignmentはhostの同じ1回の読取から同時に作られるので、両者の間に食い違いの窓は無い。`RoleIDs`は減っていないので、`ActiveAssignments`を読まないproviderの挙動は変わらない。
```

- [ ] **Step 3: docs を書く（置換の契約とコンパイル可能な例）**

contribution の `Priority` を説明した段落の**直後**に追加する:

````markdown
`ReplaceRoleID`にロールIDを入れると、そのcontributionは「追加」ではなく**置換**になる — 指定したactive manualロールが`Key`にネイティブに持つcontributionだけを、1対1で差し替える。追加だけだとboolのORや数値のmaxでネイティブの値が生き残ってしまうので、計算した値を対象ロールの値として出したい用途には置換が必要になる。

**置換の契約**（`ReplaceRoleID`が空でない場合）:

- 対象は`req.ActiveAssignments`に現れるロールだけ。conditionalロールは`role_assignment`の行を持たないため置換対象にできない
- 置換後のentryは**元のネイティブentryの`priority`を引き継ぐ**。自分で選ぶと二重定義になるので`Priority`と`Order`はどちらも`0`のまま書く
- 置換後も、他のロールのcontribution・providerの通常contribution・instance / server capと同じpriority cascadeと型集約を行う
- `explicit`も`UseDefault`に従う。`UseDefault: true`の置換は「このロールのoverrideをベース値に戻す」= ネイティブの`useDefault`と同じ扱い
- administrator / moderator判定は対象外のまま

```go
func effectivePolicies(ctx plugin.Context, inv plugin.EffectivePolicyInvalidator) (plugin.EffectivePolicyRegistration, error) {
	return plugin.EffectivePolicyRegistration{
		Keys: []string{"mentionLimit"},
		Resolve: func(c context.Context, req plugin.EffectivePolicyRequest) ([]plugin.EffectivePolicyContribution, error) {
			out := []plugin.EffectivePolicyContribution{}
			for _, a := range req.ActiveAssignments {
				// ロールごとに1件の置換を出す。Priority / Order は 0 のまま渡す =
				// 元のネイティブentryのpriorityを引き継ぐ。
				out = append(out, plugin.EffectivePolicyContribution{
					Key:           "mentionLimit",
					Value:         40,
					ReplaceRoleID: a.RoleID,
				})
			}
			return out, nil
		},
	}, nil
}
```
````

新しい Go fence は `tests/plugin-doc/extract.py` の HEADER（`context`, `encoding/json`, `net/http`, `testing`, `plugin`, `peercache`, `plugintest`, `assert`, `require` しか import されない）でコンパイルできなければならない。上の例は新規 import を使わず、宣言済みの `ctx` / `inv` だけを使うので満たす。

- [ ] **Step 4: docs を書く（競合と失敗）**

`docs/plugins/authoring.md` の「未宣言key、unknown key、priority範囲外…」で始まる段落の**末尾**に追記する:

```markdown
同じ`Key`と`ReplaceRoleID`を複数providerが置換した場合は**競合**になる。hostはそのpairを置換として受け入れず、ネイティブcontributionへ戻す — どちらの値も採らないので、管理者が設定したロールの値が残る。provider全体は失敗扱いにしないので、そのproviderの他のキーへの寄与は生き残る。checked解決は競合を表す固定errorを返し、unchecked解決はネイティブfallback mapを返す。provider失敗と併発した場合は両方のerrorが`errors.Is`で辿れる。provider失敗は宣言keyをネイティブへ戻すので、他のproviderの置換も同じkeyなら巻き戻る。
```

- [ ] **Step 5: docs を書く（純関数とcache）**

`docs/plugins/authoring.md` の「`Resolve`は、明示的なinvalidationの間は`UserID`とsorted active `RoleIDs`だけで結果が決まる純粋関数として実装する…」で始まる段落を次の形に置き換える:

```markdown
`Resolve`は、明示的なinvalidationの間は`UserID`、sorted active `RoleIDs`、`ActiveAssignments`だけで結果が決まる純粋関数として実装する。時刻、request固有情報、未通知の外部状態へ依存してはならない。**同じ`RoleIDs`でも`ActiveAssignments`が違えば結果が違ってもよい**ので、hostはproviderごとの成功結果cache keyにassignment IDを含める（付け外し / 再割り当ての直後に前の結果を返さないため）。cacheはoperator設定`effectivePolicyProviderCacheEntries`（既定10000件、providerごと）のLRUであり、eviction時は同じ入力を再解決する。
```

- [ ] **Step 6: docs を書く（compatibility.md）**

`docs/plugins/compatibility.md` の Task 1 で追記した段落の**直後**に追記する:

```markdown
`EffectivePolicyContribution.ReplaceRoleID`の追加も同じ扱い。**未設定なら追加contributionのまま**なので既存providerの挙動は変わらず、pluginは「対象ロールのネイティブcontributionを置き換える」という新しい契約にだけオプトインする。置換の`Priority`/`Order`制約はprovider作者の誤りを弾くもので、既存providerの出力形式は変えない。`APIVersion`は1のまま。
```

- [ ] **Step 7: GREEN を確認する**

Global Constraints の「authoring.md の Go fence コンパイル gate」ブロックを実行する。

Expected: `fences: <n>` の行に続けて `SNIPPET GATE OK`。**`NG: all variants failed for s<NN>` が出てはいけない** — 出ていれば、それは Step 3 で追加した fence が3 variant すべてでコンパイルできないということなので、fence を直すか noise 判定を確認してから次に進む。`<n>` は fence 数なので固定しない（doc を触る前後で変わる）。

noise 判定について: 新しい fence は `Value: 40` を使い、戻り値も返すので3 variant すべてでコンパイルできる。`declared and not used` のような noise でない診断が1つでも出るとその variant は「落ちた」と数えられるので、`Keys` と `out` の両方を必ず使っていること（どちらか片方だけだと未使用変数で落ちる）を再確認する。

```powershell
go test ./internal/entitycompat -run TestPluginDoc -count=1
git status --short
```

Expected: `go test` が PASS、`git status` に `docs/plugins/authoring.md` と `docs/plugins/compatibility.md` だけが変更されていること（Step 1 の `git checkout` が効いていることも確認する）。

- [ ] **Step 8: commit する**

```powershell
git diff --check
git diff -- docs/plugins/authoring.md docs/plugins/compatibility.md
git add docs/plugins/authoring.md docs/plugins/compatibility.md
git commit -m "Docs: describe active assignment context and role policy replacement"
```

Expected: 1 commit。**この commit の SHA を控えておく**。CI の `make plugin-doc-check` が緑であることも PR 前に確認する。

---

### Task 6: Upstream Pull Request Against shiroha-a/mk

汎用差分だけを `upstream/develop` 起点のbranchに積み、`shiroha-a/mk` へのPRを出す。Misaki固有の差分が1行でも混ざっていないことを機械的に検査してから出す。

**Files:**
- No repository file changes on `feature/role-level-plugin`（この plan ファイルと Task 6 の実行記録だけ除く）
- Create: worktree `E:\tmp\opencode\mk-upstream-role-policy`（同じリポジトリの別worktreeなので `.git` を共有する）

**Interfaces:**
- Consumes: Task 1〜5 の commit SHA
- Produces: `https://github.com/shiroha-a/mk/pull/<n>` のURL
- Produces: branch `upstream/role-policy-replacement-plugin-api`（push先は `Misaki-Project/mk` = remote `origin`）

- [ ] **Step 1: Misaki branch 側の generic commit SHA を控える**

`E:\tmp\opencode\mk-can-delete-account` で:

```powershell
git log --oneline -8
git status --short --branch
```

Expected: **Task 1〜5 の generic commit** が見える。task 数が5 でも commit 数が5 とは限らない — レビュー指摘の fix も別commitにして積んでいるので、generic系列は **13 commit**（`24514ca2` 〜 `9abbe34f`）になる。控えた SHA を oldest→newest 順に並べ、Step 5 の `$shas` に並べる。

**注意**: `git format-patch upstream/develop..feature/role-level-plugin` は範囲内の**全commit**（canDeleteAccount 関連の Misaki commit も含む）を対象にしてしまう。**必ず Task 1〜5 の SHA を個別に指定する**（Step 5 参照）。

- [ ] **Step 2: upstream を fetch して実際の HEAD SHA を控える**

```powershell
git fetch upstream develop
$upstreamSha = (git rev-parse upstream/develop).Trim()
$upstreamSha
git log --oneline -1 $upstreamSha
```

Expected: SHA が1つ出力され、commit subject が表示される。**この値を以降の Step と記録に使う。ハードコードしない。**（`$upstreamSha` はこの Step のセッション内でだけ有効なので、他 Step へ渡すときは `git rev-parse upstream/develop` で取り直す。）

- [ ] **Step 3: upstream 起点のworktreeを作る**

```powershell
Test-Path "E:\tmp\opencode\mk-upstream-role-policy"
```

Expected: `False`。`True` なら別名を使う（`E:\tmp\opencode\mk-upstream-role-policy-2` など）。

```powershell
$upstreamSha = (git rev-parse upstream/develop).Trim()
git worktree add "E:\tmp\opencode\mk-upstream-role-policy" -b upstream/role-policy-replacement-plugin-api $upstreamSha
git -C "E:\tmp\opencode\mk-upstream-role-policy" log --oneline -1
git -C "E:\tmp\opencode\mk-upstream-role-policy" rev-parse HEAD
```

Expected: HEAD が `$upstreamSha` と一致する。

- [ ] **Step 4: upstream 側に Misaki 差分が既に無いことを確認する**

```powershell
git -C "E:\tmp\opencode\mk-upstream-role-policy" grep -n "canDeleteAccount" -- . | Select-Object -First 5
git -C "E:\tmp\opencode\mk-upstream-role-policy" grep -n "joinBasePolicyError" -- . | Select-Object -First 5
```

Expected: **どちらも出力なし**。出ているなら worktree を作り直す。

- [ ] **Step 5: generic commit を1つずつ patch にして `git am -3` で積む**

```powershell
$root = "$env:TEMP\rlp-patches"
Remove-Item -Recurse -Force $root -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Path $root | Out-Null
$shas = @('<generic-sha-01>','<generic-sha-02>',...)  # Task 1〜5 + レビュー fix。全 generic commit を oldest→newest で列挙する
$i = 1
foreach ($sha in $shas) {
  $dir = Join-Path $root ("{0:d2}" -f $i)
  New-Item -ItemType Directory -Path $dir | Out-Null
  git format-patch -1 -o $dir $sha
  $i++
}
$patches = Get-ChildItem -Recurse $root -Filter *.patch | Sort-Object FullName | ForEach-Object { $_.FullName }
$patches.Count
```

Expected: `$shas` の要素数と一致する（実測は **13**）。**`git format-patch -1 A B C` は複数revでも最後の1つしか出さない**ので、必ず1つずつ別ディレクトリに出力すること（出力順を `01`〜`13` で固定し、`Sort-Object FullName` で oldest→newest を保つ）。

```powershell
git -C "E:\tmp\opencode\mk-upstream-role-policy" am -3 $patches
```

Expected: 13つとも clean apply。**hunk 単位の競合が出ることがある** — 全体を `git am --abort` するのではなく、generic な hunk だけを残して解決する（下の表の対象は upstream の形を**残すべき**もの＝持ち越さないもの）。

**reject が出たときの対処**:

```powershell
git -C "E:\tmp\opencode\mk-upstream-role-policy" am --abort
git show <task-sha> -- <file>
```

差分を確認して**手で当て直す**。当て直すときに**upstream の形をそのまま残すべき3箇所**（これらは Misaki 固有なので upstream 側に持っていかない）:

| file | upstream の形を残すもの |
|---|---|
| `internal/core/role/role_service.go` | `PolicyCanDeleteAccount` 定数、`GetUserPolicies` / `applyMetaBasePolicies` の error 化 |
| `internal/core/role/plugin_policy.go` | `joinBasePolicyError`、`resolvePolicies` の `out = make(...)`（upstream は `out := make(...)`）と base 読み損ね時の `defer` |
| `internal/effectivepolicy/validation.go` | `defaults` の `"canDeleteAccount": true` |

当て直したら `git commit -C <sha>` で **Author と message を保ったまま** commit する。

- [ ] **Step 6: upstream 側で focused test が緑であることを確認する**

workdir を `E:\tmp\opencode\mk-upstream-role-policy` に変えて:

```powershell
go test ./plugin ./internal/effectivepolicy -count=1
go test ./internal/core/role -count=1
go test ./plugin/plugintest -count=1 -run "TestEffectivePolic"
go test ./internal/entitycompat -run TestPluginDoc -count=1
go vet ./plugin/... ./internal/effectivepolicy ./internal/core/role
```

Expected: 全部 PASS。`internal/core/role` は upstream 側にも `optout_aggregation_test.go` / `plugin_policy_test.go` / `plugin_policy_internal_test.go` / `role_s5_internal_test.go` / `role_service_test.go` があるので、**同じテストが緑になること**。これが generic diff が upstream 側で完結している証拠になる。

- [ ] **Step 7: leak 検査を実行する**

```powershell
$upstreamSha = (git rev-parse upstream/develop).Trim()
git grep -n "canDeleteAccount" -- . | Select-Object -First 5
git grep -n "joinBasePolicyError" -- . | Select-Object -First 5
git diff --stat "$upstreamSha..HEAD"
```

Expected: 2つの `git grep` が**出力なし**。`git diff --stat` に出るのは **Task 1〜5 のファイルだけ**:

```
 docs/plugins/authoring.md                            |  ...
 docs/plugins/compatibility.md                        |  ...
 internal/core/role/optout_aggregation_test.go        |  ...
 internal/core/role/plugin_policy.go                  |  ...
 internal/core/role/plugin_policy_internal_test.go    |  ...
 internal/core/role/plugin_policy_test.go             |  ...
 internal/core/role/role_service.go                   |  ...
 internal/effectivepolicy/validation.go               |  ...
 internal/effectivepolicy/validation_test.go          |  ...
 internal/entitycompat/testdata/golden_plugin_surface.txt | ...
 plugin/plugintest/plugintest.go                       |  ...
 plugin/plugintest/policy_test.go                      |  ...
 plugin/policy.go                                      |  ...
```

これ以外のファイルが1つでも出水たら **PR を出す前に upstream/develop 側の版に戻す**。

- [ ] **Step 8: origin へpush して PR を作る**

```powershell
$branch = "upstream/role-policy-replacement-plugin-api"
git push -u origin $branch
```

cross-repository PR の head は **`<owner>:<branch>`** 形式で指定する（fork のリポジトリ名ではなく owner 名）:

```powershell
$body = "$env:TEMP\rlp-pr-body.md"
gh pr create --repo shiroha-a/mk --base develop --head "Misaki-Project:$branch" --title "Add generic plugin API: active role assignments and role policy replacement" --body-file $body
```

`gh pr create --head` が owner:branch を要する場合はこの形式が正なので、push 先が `Misaki-Project/mk` でも `--head Misaki-Project:<branch>` を使う（`Misaki-Project/mk:<branch>` は owner ではなくリポジトリ名を渡す形なので使わない）。実行して弾かれたら、エラーメッセージが示している形式に合わせる。

`$body` には次を書く:

```markdown
## What

Two additive extensions to the public plugin API, so a plugin can (a) know which native role assignment it is looking at and (b) replace one active manual role's native policy contribution instead of only adding to it. No schema change, no `APIVersion` bump.

- `plugin.ActiveRoleAssignment{RoleID, AssignmentID}` and `EffectivePolicyRequest.ActiveAssignments` — the user's active manual role assignments, one per role, sorted, non-nil for anonymous requests. Conditional roles are excluded because they have no `role_assignment` row, so every `RoleID` is a subset of `RoleIDs`. `RoleIDs` itself is unchanged and still covers conditional roles, so providers that read only `RoleIDs` behave exactly as before.
- `EffectivePolicyContribution.ReplaceRoleID` — a contribution naming an active manual role replaces that role's native contribution for `Key` one-for-one, keeping the role's own declared `priority`. `Priority` and `Order` must both stay `0`. Two providers replacing the same role and key is a conflict: the host keeps the native contribution for that pair and `GetUserPoliciesChecked` returns a fixed `ErrEffectivePolicyReplacementConflict`; the unchecked resolver returns the native fallback map. A failed provider still restores its declared keys to native, unchanged.

Roles and active assignments are produced by one internal snapshot read from a single `role_assignment` query, so resolution costs no extra query and the two answers cannot disagree. A failure to read assignments is reported as a checked role-input error before any provider is invoked, never as an empty assignment list. The per-provider LRU key includes the assignment IDs so unassign/re-assign cannot serve a stale result.

## Why

An additional contribution cannot express "this role's value is now X": bool OR and numeric max keep the native value alive. A plugin that computes a value (a level, a quota derived from a level) needs a 1:1 replacement boundary.

## Compatibility

- Additive only. `plugin.APIVersion` stays 1.
- `RoleIDs` semantics, ordering and the non-nil empty slice for anonymous requests are unchanged.
- `plugins/trustlevel` needs no change: it reads `RoleIDs` only and leaves `ReplaceRoleID` empty.
- No DB migration. The assignments are read from the existing `role_assignment` table.

## Tests

- `go test ./plugin ./internal/effectivepolicy ./internal/core/role -count=1`
- `go test ./plugin/plugintest -count=1 -run TestEffectivePolic`
- `go test ./internal/entitycompat -run TestPluginDoc -count=1`
- `go run ./tools/pluginspec -write` (golden diff in this PR)
- `make plugin-doc-check` (the new authoring.md snippet compiles)
```

Expected: PR URL が出る。**この URL を控えておく**。

- [ ] **Step 9: PR の URL を Misaki branch 側に記録する**

`E:\tmp\opencode\mk-can-delete-account` に戻り、`docs/superpowers/plans/2026-09-27-role-policy-replacement-plugin-api.md` の**末尾に1節だけ**追記する:

```markdown
## Execution Record

- Upstream PR: <Step 8 で作った URL>
- Upstream branch: `Misaki-Project/mk` の `upstream/role-policy-replacement-plugin-api`（PR head は `Misaki-Project:upstream/role-policy-replacement-plugin-api`）
- Base: `upstream/develop` @ `<Step 2 で控えた SHA>`
- Applied commits (oldest first): generic 系列は **13 commit**（Task 1〜5 + レビュー fix）。`<source-sha-1> → <upstream-sha-1>` の対応と、競合解決で追加した upstream 側 commit をすべて書く
```

```powershell
git add docs/superpowers/plans/2026-09-27-role-policy-replacement-plugin-api.md
git commit -m "Docs: record the upstream role policy replacement PR"
```

Expected: 1 commit。**この commit は upstream へ積まない**（Step 5 で SHA を個別指定しているので自動的に外れる）。

- [ ] **Step 10: レビューで修正が来たときの follow-up**

upstream 側で review が来たら、**同じ branch へ push する**（PR は自動更新される）:

```powershell
git -C "E:\tmp\opencode\mk-upstream-role-policy" push
```

upstream 側で contract を変えた場合は `plugin.APIVersion` の要否を**再検討する**（additive の範囲から外れる変更なら上げる）。Misaki branch 側への反映が必要なら同じ commit を `feature/role-level-plugin` に `git cherry-pick` する。

---

## Self-Review

- **Spec coverage**: `Generic Plugin API Extensions` の「Active Assignment Context」→ Task 1 + 2。「Role Policy Replacement」の契約（active role + 既知keyのみ / 1対1 / native priority維持 / 集約継続 / admin判定対象外 / 競合error / failure fallback / checked error / unchecked map）→ Task 4。「公開Plugin API変更にはcompatibility docs、surface golden、host wiring test、frontend type test」のうち **frontend type test 以外の3つ** → Task 1 / 4 / 5（frontend slot は本計画の明示スコープ外）。「No core DB migration」→ Global Constraints。`Delivery Boundaries` の upstream PR → Task 6。
- **スコープ外にしたもの**: Misaki固有のroleLevel（level / XP / curve / range / storage / route / UI）、frontend（`admin:role-editor` slot、misskey-ts）、`deliveryTargets`。spec のうち upstream PR に載らないものは別計画で扱う。
- **Test-first ordering**: 全 task で「最小 test / gate を書く → RED を実行して観察 → production / docs を書く → GREEN」を満たす。Task 1 は plugin package のコンパイル ERROR を RED として観測し、doc gate は golden 更新で RED にしてから docs を書く。Task 2 は内部 API が無いことによるコンパイル ERROR を RED として観測してから `resolveUserRoleSnapshot` を実装する。Task 3 は test を先に新形へ書き換えて RED にしてから型を足す。Task 4 は validation → host 適用 → plugintest の3区画それぞれについて test → RED → 実装 → GREEN の順。Task 5 は gate の生存を RED で証明してから docs を書く。
- **Regression guard の明示**: 実装前から緑になる test には plan 上でその旨を記した — `TestEffectivePolicy_AssignmentRepositoryFailureSkipsProviders`（Task 2 Step 1）と、plugintest の `TestEffectivePoliciesAcceptsValidReplacement` / `TestEffectivePoliciesRejectsInvalidReplacement` / `TestEffectivePoliciesRejectsReplacementChoosingAPriority`（Task 4 Step 9）。これらは RED を作らない代わりに、実装変更が既存契約を守ったことの固定として残す。
- **Placeholder scan**: 「TODO」「後で埋める」「同様に(Task N)」相当は無い。すべてのコードステップに実コードがある。実装者が判断を迫られる箇所（`SetUserRepo` の存在、`{"type":"isLocal"}` の可用性、upstream HEAD の SHA、PR head の形式）は file/line またはコマンドを明示して「必ず確認すること」と書いた。
- **Type consistency**: `plugin.ActiveRoleAssignment{RoleID, AssignmentID}` / `ActiveAssignments` は Task 1 で定義し、Task 2（host）と Task 4（validation + plugintest）が同じ名前・型を使う。`role.userRoleSnapshot{roles, activeAssignments}` と `role.activeRoleAssignment{roleID, assignmentID}` は Task 2 の Interfaces で定義し、Step 3 のコードと一致する。`rolePolicyInput.roleID` / `rolePolicyEntry` は Task 3 で定義し、Task 4 の `overridesByRole` / `applyPolicyReplacements` / `collectPolicyReplacement` が同じ名前で読む。`ValidateContributions(keys, activeRoles, contributions)` の3引数は Task 4 Step 1（test）→ Step 3（実装）で一致し、2つの呼び出し側（`core/role`、`plugintest`）も同じ形。`ErrEffectivePolicyReplacementConflict` は Task 4 Step 5（test）→ Step 6（RED のコンパイルエラー）→ Step 7（実装）で一致。
- **Forward reference なし**: Task 4 が使う `roleInputs` は Task 3 で導入済み。Task 4 Step 3 が使う `plugintestActiveRoleIDs` / `activeRoleIDsFromAssignments` は同じ Step で定義する。Task 3 は Task 1/2 の型に依存しない。Task 2 の `resolveUserRoleSnapshot` は Task 1 の型だけを使う。
- **Task 間の型重複の排除**: 呼び出し回数カウンタに `countingAssignmentRepo`（`role_service_test.go`、外部 test package）を、repository 失敗に `failingPolicyAssignmentRepo`（`plugin_policy_test.go`、外部 test package）を再利用する。内部 test package からは既存の mock を直接触れないので `countingInternalAssignmentRepo` だけが新しい型で、これは「外から見える契約を持たない内部 test double」であることをコメントで明示している。
- **信頼性**: `go test` 系の gate は全てこの作業ツリーで実測して緑（`go test ./internal/core/role -count=1` PASS、`go test ./plugin ./internal/effectivepolicy -count=1` PASS、`go test ./plugin/plugintest -count=1 -run TestEffectivePolic` PASS、`go test ./internal/entitycompat -run TestPluginDoc -count=1` PASS、bundled plugin の `go vet` PASS）。`TestPluginSurfaceDrift` は行末差で元から赤なので、正規化比較を正式な gate にした。`git format-patch -1` の複数rev問題と `extract.py` の cp932 問題も実測で把握し、回避策を Step に書いた。
- **authoring.md の snippet gate は「壊れていたので直した」。最初の版は3つとも壊れていて、clean な docs に対して `SNIPPET GATE OK` を出すことすら保証していなかった:** (1) `snippets/<id>/` を検索しており `extract.py` が作る `snippets/<id>_<variant>/` に1件もHITしない、(2) `1..40` の決め打ち、(3) noise を数える上に NG 条件が反転していた。判定ロジックは Global Constraints のブロックを読み直せば分かるのでここでは繰り返さない。**実行証拠（この計画の tree での実測ログであり、計画の期待値ではない）:** 修正後のブロックは **clean docs で `SNIPPET GATE OK`**、Step 1 の型エラー変異を掛けると **`NG: all variants failed for s<NN>`**（当該 fence の `_top` variant が `cannot use "1" (untyped string constant) as int value in struct literal` を出す）、復元後は **`SNIPPET GATE OK`** に戻ることを実測で確認した。Task 5 の新しい fence を含む実装後treeでもGREENを確認済み。**このとき観測された fence 数と ID（`fences: 19` / `s10`）はそのときのtreeでの値にすぎない**ので、Task 5のStep 1 / Step 7には数やIDを書いていない。

---

## Execution Record

Task 6 を実行した結果。**Task 1〜5 の generic 系列は 13 commit** である — task 数と commit 数は一致しない。レビュー指摘の fix（`014ed8f1` / `355469d8` / `817c02e0` / `4b526808` / `09710080` / `f27e71c2` / `9abbe34f`）も別 commit にして積んでいるため。さらに Task 6 実行中に 2 つの follow-up（競合解決 1 + レビュー prose 1）が加わり、**upstream へ出した source generic commit は 14 個**になった。

- Upstream PR: <https://github.com/shiroha-a/mk/pull/3197>
- Upstream branch: `Misaki-Project/mk` の `upstream/role-policy-replacement-plugin-api`（PR head は `Misaki-Project:upstream/role-policy-replacement-plugin-api`、base は `shiroha-a/mk:develop`）
- Upstream worktree: `E:\tmp\opencode\mk-upstream-role-policy`（source worktree `E:\tmp\opencode\mk-can-delete-account` と `.git` を共有する別 worktree）
- Base: `upstream/develop` @ `1a0f2012bc13f3a34c92703c7ae3d5a6765a2f43`。`git fetch upstream develop` → `git rev-parse upstream/develop` で取得し、`git ls-remote upstream develop` と一致することも確認した（ハードコードしていない）
- Applied range: `1a0f2012bc13f3a34c92703c7ae3d5a6765a2f43..bd268aea4e48eadebbbd9f1c19d74ec0c3a12e6f`（**1 commit**）

### upstream branch の履歴を 1 commit に畳んだ（finding 3 の是正）

- 旧 published HEAD: `d347fa1c946c20f108444fe62b3e46c4abdaab27`（15 commit）を `--force-with-lease` で置換した。**人が明示承認した cleanup のみ。**
- 理由: **commit metadata の破損**。競合解決で追加した `8e1c4dcf` の commit message に制御文字が混入していた。実測: offset 249 に TAB (0x09)、offset 289 に BEL (0x07)。原因は `git commit -m "..."` を PowerShell のダブルクォートで書き、message 中の `` `testutil `` / `` `applyMetaBasePolicies `` が PowerShell のエスケープ（`` `t `` = TAB, `` `a `` = BEL）と解釈されたこと。**丸括弧を PowerShell のダブルクォートで書くと静かに壊れる**ので、message はシングルクォートか subject だけにする。
- 新しい upstream commit: **`bd268aea4e48eadebbbd9f1c19d74ec0c3a12e6f`**、subject は **`Add generic role policy replacement plugin API`** の1行だけ（bodyなし）。
- 手順（source 側の 14 commit を 1 commit に squash）: 一時 safety ref を旧 HEAD に張る → `git checkout --detach $base` → `git merge --squash <safety-ref>` → `git commit -m 'Add generic role policy replacement plugin API'`（シングルクォート）。**tree が一致することを確認してから** branch を移動し push した。remote 検証後に safety ref のみ削除済み。

### tree 同一性の証拠（diff を1バイトも変えていない）

| 項目 | 旧 `d347fa1c` | 新 `bd268aea` | 判定 |
|---|---|---|---|
| tree object | `9a16a9d699f3fe8c1a2679e9237b23057c31f90d` | `9a16a9d699f3fe8c1a2679e9237b23057c31f90d` | **一致** |
| parent | `1a0f2012bc13f3a34c92703c7ae3d5a6765a2f43` | `1a0f2012bc13f3a34c92703c7ae3d5a6765a2f43` | 一致 |
| commit 数 | 15 | 1 | 意図どおり |
| 変更 file 数 | 14 | 14 | 一致 |
| `--numstat` 合計 | +1738 / -86 | +1738 / -86 | 一致 |
| `git diff --binary` の SHA256 | `B026B6F0F3DD773E4A61C66E0E7FBA2AF9F20D060A6D02AEB275239258D4B503` | 同左 | **完全一致** |
| `diff --check` | clean | clean | 一致 |
| `--name-only` | 14 file | 14 file | `Compare-Object` 差分 0 |

- `git merge --squash` 直後の **index tree がすでに `9a16a9d6...` で一致していた**ので、commit を作る前から同一性が保証されていた。
- 新しい commit の **raw commit object は 300 byte、制御文字 0 個**（LF 除く）。message は 47 byte の純 ASCII:
  `41 64 64 20 67 65 6e 65 72 69 63 ...` = `Add generic role policy replacement plugin API`。`git fsck` も clean。
- push は `--force-with-lease`（remote-tracking ref = 旧 published HEAD に固定）。**`<expect>:<ref>` 明示形式はこの git 2.49.1 / PowerShell 環境で force にならず（`stale info` ではなく `non-fast-forward` で落ちる）ので使えず**、lease が値に固定されていることを2回の dry-run で証明した: lease を別の実在 SHA にすると `stale info` で拒否、期待値 `d347fa1c...` に戻すと `+ d347fa1c...bd268aea (forced update)` で許可。**source / Misaki 側の branch には一切 force を使っていない。**
- push 後の remote 検証: PR #3197 は **OPEN / base `develop` / head `Misaki-Project:upstream/role-policy-replacement-plugin-api` のまま、commit 1 個・14 file・+1738/-86・MERGEABLE、body と title は変更なし**。GitHub 側が算出した additions/deletions も 1738/86 でローカル `numstat` と一致。CI は force-push により再起動（呼び出し時点では pending / in-progress、**結果は本記録時点では未確認**）。

### Source → upstream commit 対応

upstream branch は **1 commit に squash 済み**なので、source 側の generic commit 14 個がどの upstream SHA に対応する、という対応表は**もう存在しない**。全部が次の 1 commit にになっている:

| source `feature/role-level-plugin` | upstream `upstream/role-policy-replacement-plugin-api` |
|---|---|
| 以下の **14 commit**（oldest first） | **`bd268aea4e48eadebbbd9f1c19d74ec0c3a12e6f`**（`Add generic role policy replacement plugin API`） |

| # | source `feature/role-level-plugin`（oldest first） |
|---|---|
| 1 | `24514ca270ca6958151c985e0b3b94ef0509af2c` |
| 2 | `014ed8f1c65d66cf89e9910b388e56185b9a4df3` |
| 3 | `b675535b425f5354d4b3a9ebd55ea0dd9e4036d5` |
| 4 | `5e3303be4f46980ab2b2b61184ec6aeec4051497` |
| 5 | `355469d8eed3202d25c3592eb30ecc53b24cef67` |
| 6 | `ca8544dac61dd80994bbad68e7a43208eda39cdf` |
| 7 | `9fab2ca5e6bacd5c87650f7673dfe3d5d6910fc5` |
| 8 | `817c02e0db0a44614bedb7177a298f9bbc700a77` |
| 9 | `f862dd2c52b5388202c778b2d1d8877259b515b6` |
| 10 | `4b52680830f13c2aaeb4b59cb152a4bf4b3226e6` |
| 11 | `0971008078c9f59ad4b95a5353b4975596be1793` |
| 12 | `f27e71c2d4be154a42f6e7b6767bb3cc7b1dafc1` |
| 13 | `9abbe34fcd805a68af38f669b7b95b56a9aac9ba` |
| 14 | `81a34aee7c241c54c56b31450cc6e37d058c8be9` |

内訳: 1〜13 は Task 1〜5 の generic 系列（task 数と commit 数が一致しない理由と、レビュー指摘 fix を別 commit にしたものは冒頭に記載）、14 は Task 6 の upstream PR レビュー指摘（`plugin/policy_test.go` のコメントに `XP` 語彙が漏れていた / `ReplaceRoleID` の GoDoc が unchecked 解決を `native fallback map` と説明していた / authoring.md の包含関係の言い換え / 中国語断片の除去）を 1 commit にまとめた prose 修正。

この squash より前に upstream 側で競合解決のために追加した commit（`8e1c4dcf Fix role: use upstream's bare meta mock in assignment tests`）も、同じく 1 commit に畳まれている。**Author と Author Date を保持しないのは意図的で、歴史の commit message に混入していた制御文字をここで消すため。**

### 除外した commit / ファイル

`d9cc7d71`（roleLevel 設計）、`08f7e32c`（別計画 + 本 plan）、`0584864e` / `9cc0d2c0`（plan の記述修正）、canDeleteAccount / Misaki 固有の commit 群、未追跡の `typecheck-task4.txt`。PR の変更 file は Task 1〜5 の generic 14 file のみで、`docs/superpowers/**` は載っていない。

### upstream 側で解決した競合（2箇所）

1. **`internal/effectivepolicy/validation_test.go`**（`9fab2ca5` 由来）: 源 commit の hunk は Misaki 専用の `TestCanDeleteAccountPolicyContract` に `ValidateContributions` の第2引数 `nil,` を足すもの。upstream にはその test が存在しないので、**test ごと落として** generic な `TestValidateContributionsReplacementTieUsesRoleID` だけを残した。
2. **`internal/core/role/plugin_policy_test.go`**: 追加した2つの test が呼ぶ `newTestMetaRepository()` は `d452634e`（canDeleteAccount 系の base policy error 化）が入れた helper で、generic 13 commit には含まれないため upstream では `undefined` になる。upstream 自身の `newTestService` と同じ `testutil.NewMockMetaRepository()` に置き換え、upstream 側だけの follow-up commit として積んだ（`role_service_test.go` は upstream の形のまま変更していない）。upstream の `applyMetaBasePolicies` は meta row 不在を「override なし」として fail-soft 処理するので、bare mock で挙動は変わらない。

### 検証（upstream worktree、base `1a0f2012`）

適用前（pristine upstream）の baseline を先に取ってから適用した:

| gate | baseline（適用前） | 適用後 |
|---|---|---|
| `go test ./plugin ./internal/effectivepolicy -count=1` | PASS | PASS |
| `go test ./internal/core/role -count=1` | PASS | PASS |
| `go test ./plugin/plugintest -count=1 -run TestEffectivePolic` | PASS | PASS |
| `go test ./internal/entitycompat -run TestPluginDoc -count=1` | PASS | PASS |
| surface 正規化比較（`tools/pluginspec` vs golden） | `SURFACE OK` | `SURFACE OK` |
| `go vet ./plugin/... ./internal/effectivepolicy ./internal/core/role` | exit 0 | exit 0 |
| bundled plugin `go vet ./...`（`plugins/trustlevel`, `plugins/status`） | exit 0 | exit 0 |
| authoring.md snippet gate | `fences: 18` / `SNIPPET GATE OK` | `fences: 19` / `SNIPPET GATE OK` |
| `git diff --check` | — | 無出力 |

- **surface の差分は generic な5行だけ**: `plugin: type ActiveRoleAssignment struct` / `field ActiveRoleAssignment.AssignmentID string` / `field ActiveRoleAssignment.RoleID string` / `field EffectivePolicyRequest.ActiveAssignments []ActiveRoleAssignment` / `field EffectivePolicyContribution.ReplaceRoleID string`。
- **leak 検査**: 先の1回目の記録は `git diff` を PowerShell の既定の cp932 で decode して走らせており、**ASCII のパターン文字が日本語文字に続く行では DBCS の後続バイトとして飲み込まれて false negative になっていた**。`XP` を 0 hit と書いたのはこの誤り（`plugin/policy_test.go` に実在した）。修復後、**`[Console]::OutputEncoding = UTF8` を設定したうえで追加行（`+` 行）だけを case-sensitive で走らせる**方法に一本化した。`base..HEAD` = 2303 行（うち追加 1738 行）に対し、`canDeleteAccount` / `joinBasePolicyError` / `roleLevel` / `RoleLevel` / `PolicyCanDeleteAccount` / `Misaki` / `can-delete-account` / `newTestMetaRepository` / `superpowers` / `typecheck-task4` / `AutoMigrate` / `migrations` / `experience` / `Experience` / `XP` / `进来` / `native fallback map` すべて **0 hit**。`部分集合` の 1 hit は `plugin_policy_test.go` の包含不変条件の test コメント（正しい書き方）で、finding 4 の対象ではない。作業ツリー側の `git grep` でも同-pattern が 0。
- **変更 path**（14）: `docs/plugins/authoring.md`, `docs/plugins/compatibility.md`, `internal/core/role/optout_aggregation_test.go`, `internal/core/role/plugin_policy.go`, `internal/core/role/plugin_policy_internal_test.go`, `internal/core/role/plugin_policy_test.go`, `internal/core/role/role_service.go`, `internal/effectivepolicy/validation.go`, `internal/effectivepolicy/validation_test.go`, `internal/entitycompat/testdata/golden_plugin_surface.txt`, `plugin/plugintest/plugintest.go`, `plugin/plugintest/policy_test.go`, `plugin/policy.go`, `plugin/policy_test.go`。**Task 6 Step 7 の想定リストは `plugin/policy_test.go` も落ちている**ので、この点だけ記録で補足する。
- **`gofmt`**: 変更した Go 11 file の **blob 内容（LF で checkout して判定）** に対し `gofmt -s -l` が無出力。作業ツリーで `gofmt -l` を走らせると変更していない upstream file（`can_chat_lookup.go` / `cond_formula.go` / `policy_number.go` / `user_roles_lookup.go`）まで全部列挙される。`core.autocrlf=true` の CRLF checkout 由来の**元からある環境条件**なので、判定には blob を使う。

### この環境で守るべき既知 baseline（変更由来と混同しないこと）

- この作業ツリーの `go test ./...` は元から PostgreSQL 未接続 / Windows 非対応（`internal/server` は `syscall.Statfs` 依存で test すら動かない）/ `make` 不在で落ちる。新規 regression の判定は上の focused gate だけで行う。
- `TestPluginSurfaceDrift` は CRLF checkout で元から赤。判定は正規化比較で行う。
- **`[Console]::OutputEncoding` が cp932（このホストの既定）の場合**、native command の stdout が Shift-JIS として解釈される。2 つの影響が実測で確認されている: (1) `go run ./tools/pluginspec > file` では CRLF の `0x0D` が DBCS の後続バイトとして消費されて**行が結合された擬似的な drift**が出る。(2) diff を変数に受けて `Select-String` で leak 検査すると、**行内の日本語に続く ASCII のパターン文字（`XP` など）が DBCS の後続バイトとして飲み込まれ false negative になる** — この 2 番目で PR 1 版の leak 検査が `XP` を見落としていた。比較・検査の前に必ず `[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)` を設定する。設定しないと `SURFACE OK` を通らず、leak 検査も信用できない。
- **leak 検査は追加行（`+` 行）だけに走らせる。** 削除行（`-` 行）は直前に漏れていた文字列を必ず含むので、0 hit の証拠として使えない。

### この記録の commit

Task 6 で積んだのは plan だけ（`4713bba3`: Execution Record + 「5 commits」表記の訂正）と、このレビュー対応の記録訂正（plan だけ）。**generic コード修正の `81a34aee` は upstream 側にも積んでおり**、`docs/superpowers/**` は PR に載らない。source の `feature/role-level-plugin` は push しない。
