# Role Policy Replacement Plugin API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `shiroha-a/mk` へ PR 出せる汎用Plugin APIを足す — active manual assignment の文脈と、1つのactive manual roleのnative policy contributionだけを1対1で置換する境界 — を、Misaki固有のlevel計算なしで公開面に載せる。

**Architecture:** 公開型は `plugin/` への**追加だけ**（`ActiveRoleAssignment`、`EffectivePolicyRequest.ActiveAssignments`、`EffectivePolicyContribution.ReplaceRoleID`）。hostは既存のper-user role cache entryにactive manual assignmentを一緒に積み、`ActiveAssignments` をそのcacheから出して既存 `RoleIDs` と並べて渡す。置換は「provider結果 → `rolePolicyInput`（ロールごとの集約entry）」の段で、置換entryの`priority`を**元のnative entryのpriority**にして差し替える。集約のpriority cascade・aggregator・instance/server cap・管理者判定は一切変えない。競合は(role, key)単位でnativeへ戻し、checked resolverだけが固定sentinelを返す。

**Tech Stack:** Go 1.27.1、Echo、testify、`internal/effectivepolicy`（native policy schema）、`internal/pluginspec`（公開面golden）、`plugin/plugintest`、`gh` CLI

## Global Constraints

- 対象は**upstream可能な汎用Plugin API変更だけ**。Misaki固有のroleLevel計算・XP・storage・route・UI・frontendをこの計画に混ぜない（別計画の担当）。
- 破壊的変更はゼロ。`plugin.APIVersion` は `1` のまま維持する（`docs/plugins/compatibility.md` の additive 契約に従う）。
- `RoleIDs` の意味・並び・重複除去・非nil空sliceという既存契約は**変えない**。`ActiveAssignments` は `RoleIDs` を置き換えず、并んで渡す。
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

CIが真のgate（`make test` / `make lint` / `make golangci-lint` / `make plugin-doc-check` / `make plugin-vet`）。この作業ツリーには `bash` と `make` が無いので `make plugin-doc-check` は Task 5 で CI に任せる（等価な `bash ./tests/plugin-doc/check-snippets.sh` は WSL bash が使えれば実行可）。

---

### Task 1: Active Role Assignment Context (Public Surface)

`EffectivePolicyRequest` に「どのactive manual roleに、どのassignment IDがあるか」を渡す。既存providerは `RoleIDs` だけを読むので挙動は変わらない。

**Files:**
- Modify: `plugin/policy.go:8-15` (`ActiveRoleAssignment` を新規追加)、`:15-27` (`EffectivePolicyRequest` に `ActiveAssignments` を追加)
- Test: `plugin/policy_test.go`
- Modify: `internal/entitycompat/testdata/golden_plugin_surface.txt`（`go run ./tools/pluginspec -write` で再生成）
- Modify: `docs/plugins/authoring.md:813-815`（Go公開面一覧の `EffectivePolicyRequest`）
- Modify: `docs/plugins/compatibility.md:29` の直後（additive 追記）

**Interfaces:**
- Consumes: なし（最初のtask）
- Produces: `plugin.ActiveRoleAssignment{RoleID string; AssignmentID string}`
- Produces: `plugin.EffectivePolicyRequest.ActiveAssignments []ActiveRoleAssignment`
- Preserves: `plugin.EffectivePolicyRequest.RoleIDs` の意味・並び（非nil空slice）

- [ ] **Step 1: 公開型を追加する**

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
	// 追加contributionだけ"`
	// 返す実装には RoleIDs があれば十分。
	ActiveAssignments []ActiveRoleAssignment
```

コメント末尾の2行は次の1行に置き換える（そのまま貼る）:

```go
	// **RoleIDs を置き換えない。** 既存providerはこのsliceだけを見ている。
	// 追加contributionだけ返す実装には RoleIDs があれば十分。
```

- [ ] **Step 2: 公開面の形を固定するtestを書く**

`plugin/policy_test.go` に追記する:

```go
func TestEffectivePolicyRequest_CarriesActiveAssignmentsAlongsideRoleIDs(t *testing.T) {
	// plugin作者は RoleID と AssignmentID を別々に読む。片方だけ持つ型に
	// 戻ると「XP を role に紐づけられた」と誤って付け替えられる。
	assignment := ActiveRoleAssignment{RoleID: "r1", AssignmentID: "a1"}

	request := EffectivePolicyRequest{
		UserID:            "u1",
		RoleIDs:           []string{"r1", "r2"},
		ActiveAssignments: []ActiveRoleAssignment{assignment},
	}

	assert.Equal(t, "r1", request.ActiveAssignments[0].RoleID)
	assert.Equal(t, "a1", request.ActiveAssignments[0].AssignmentID)
	// ActiveAssignments は RoleIDs を置き換えず、并んで運ぶ。
	assert.Equal(t, []string{"r1", "r2"}, request.RoleIDs)
}
```

- [ ] **Step 3: docの公開面一覧がREDであることを確認する**

まだ doc を更新する前に gate が落ちることを確かめる:

```powershell
go test ./internal/entitycompat -run TestPluginDoc -count=1
```

Expected: FAIL。`ActiveRoleAssignment` / `ActiveAssignments` が `docs/plugins/authoring.md` の公開面一覧に無いと報告される。

- [ ] **Step 4: authoring.md の公開面一覧を更新する**

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

- [ ] **Step 5: compatibility.md に additive 契約を追記する**

`docs/plugins/compatibility.md` の「`Definition.EffectivePolicies`と関連型の追加はこのadditive契約に従い…」の段落の**後**に追記する:

```markdown
`EffectivePolicyRequest.ActiveAssignments` と `plugin.ActiveRoleAssignment` の追加も同じ扱い。**`RoleIDs` は変更されない**ので既存providerの挙動は変わらず、新sliceを無視する実装は害がない。`APIVersion`は1のまま。
```

- [ ] **Step 6: golden を再生成する**

```powershell
go run ./tools/pluginspec -write
git diff --stat internal/entitycompat/testdata/golden_plugin_surface.txt
```

Expected: golden に次の3行が追加され、他の行は動かない（`SurfaceAll` はソートするので `field ActiveRoleAssignment.*` が `plugin:` ブロックの先頭付近、`type ActiveRoleAssignment struct` が `type` 群の `EffectivePolicyContribution` の直前に入る）:

```
plugin:   field ActiveRoleAssignment.AssignmentID string
plugin:   field ActiveRoleAssignment.RoleID string
plugin:   field EffectivePolicyRequest.ActiveAssignments []ActiveRoleAssignment
plugin: type ActiveRoleAssignment struct
```

- [ ] **Step 7: focused command で GREEN を確認する**

```powershell
go test ./plugin -count=1
go test ./internal/entitycompat -run TestPluginDoc -count=1
```

Expected: 両方 PASS。surface golden の確認は Global Constraints の正規化比較で行う（`go test ./internal/entitycompat -run TestPluginSurfaceDrift` はcheckout状態によって壊れる）。

- [ ] **Step 8: commit する**

```powershell
& (Join-Path (go env GOROOT) "bin\gofmt.exe") -s -l plugin\policy.go plugin\policy_test.go
git diff --check
git diff -- plugin internal/entitycompat/testdata/golden_plugin_surface.txt docs/plugins/authoring.md docs/plugins/compatibility.md
git add plugin/policy.go plugin/policy_test.go internal/entitycompat/testdata/golden_plugin_surface.txt docs/plugins/authoring.md docs/plugins/compatibility.md
git commit -m "Add plugin: hand active role assignments to effective policy providers"
```

Expected: 1 commit。`gofmt -l` が無出力、`git diff --check` が無出力。**この commit の SHA を控えておく**（Task 6 のupstream適用で使う）。

---

### Task 2: Host-Supplied Active Assignments And Assignment-Aware Cache Key

`ActiveAssignments` を**既存のper-user role cache entry**から出してproviderへ渡す。cache key にはassignment ID を含める。

**Files:**
- Modify: `internal/core/role/role_service.go:240-243` (`roleCacheEntry`)、`:511-571` (`GetUserRoles`)、`:588` の直前（`activeRoleAssignment` 型と生成helper）
- Modify: `internal/core/role/plugin_policy.go:74-77` (`policyProviderCacheKey`)、`:229-231` (request 構築)、`:333-337` (`resolvePolicyProviderCached` のkey)、`:485` の後（encoding helper）、`:661` の直前（`activeRoleAssignments` accessor）
- Test: `internal/core/role/plugin_policy_test.go`（外部テスト、`internal/repository` を import に追加）
- Test: `internal/core/role/plugin_policy_internal_test.go`（内部テスト）

**Interfaces:**
- Consumes: `plugin.ActiveRoleAssignment`, `plugin.EffectivePolicyRequest.ActiveAssignments`（Task 1）
- Produces: 非公開 `role.activeRoleAssignment{roleID string; assignmentID string}`
- Produces: 非公開 `role.activeRoleAssignmentsFrom([]*model.RoleAssignment) []activeRoleAssignment`
- Produces: 非公開 `role.cloneActiveRoleAssignments([]activeRoleAssignment) []activeRoleAssignment`
- Produces: 非公開 `(*role.Service).activeRoleAssignments(userID string) []plugin.ActiveRoleAssignment`
- Produces: 非公開 `role.pluginActiveRoleAssignments([]activeRoleAssignment) []plugin.ActiveRoleAssignment`
- Produces: 非公開 `role.encodePolicyProviderAssignments([]plugin.ActiveRoleAssignment) string`
- Produces: 外部テスト helper `countingAssignmentRepository` と `newCountingTestService`

- [ ] **Step 1: query回数カウンタ付きservice builder を追加する**

`internal/core/role/plugin_policy_test.go` の `func assign(` helper の**直後**に追記する:

```go
// countingAssignmentRepository は ListByUser の呼び出し回数を数える。**atomic
// にするのは GetUserRoles の single-flight が fetch をどの呼び出し元で走らせ
// ても良いから**で、`-race` 実行で data race を出さないため。
type countingAssignmentRepository struct {
	repository.RoleAssignmentRepository
	calls atomic.Int64
}

func (r *countingAssignmentRepository) ListByUser(userID string) ([]*model.RoleAssignment, error) {
	r.calls.Add(1)
	return r.RoleAssignmentRepository.ListByUser(userID)
}

// newCountingTestService は newTestService と同じ形の戻り値に加え、query 回数を
// 数える wrapper を挟む。`EffectivePolicyRequest.ActiveAssignments` を足す
// ことで「2 本目の query が出ないこと」を固定する。
func newCountingTestService(t *testing.T) (*role.Service, *testutil.MockRoleRepository, *testutil.MockRoleAssignmentRepository, *countingAssignmentRepository) {
	t.Helper()
	roleRepo := testutil.NewMockRoleRepository()
	assignRepo := testutil.NewMockRoleAssignmentRepository(roleRepo)
	metaRepo := newTestMetaRepository()
	idGen, _ := id.NewGenerator("aidx")
	counting := &countingAssignmentRepository{RoleAssignmentRepository: assignRepo}
	return role.NewService(roleRepo, counting, metaRepo, idGen), roleRepo, assignRepo, counting
}
```

import に `github.com/shiroha-a/mk/internal/repository` を追加する（この package は `repository` を import していない。`sync/atomic`, `internal/testutil`, `internal/misc/id`, `internal/model`, `time`, `errors`, `gorm.io/datatypes` は既にある）。

- [ ] **Step 2: 外部テストのREDを書く**

`internal/core/role/plugin_policy_test.go` の末尾に追記する:

```go
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

	var assignments []plugin.ActiveRoleAssignment
	var roleIDs []string
	registerProvider(t, svc, "ctx", []string{"canSearchNotes"},
		func(_ context.Context, req plugin.EffectivePolicyRequest) ([]plugin.EffectivePolicyContribution, error) {
			roleIDs = req.RoleIDs
			assignments = req.ActiveAssignments
			return nil, nil
		})

	_, err := svc.GetUserPoliciesChecked("u1")
	require.NoError(t, err)

	assert.Equal(t, []string{"r1", "r2", "r3"}, roleIDs, "RoleIDs は conditional を含むという既存契約を変えない")
	assert.Equal(t, []plugin.ActiveRoleAssignment{
		{RoleID: "r1", AssignmentID: "a_u1_r1"},
		{RoleID: "r2", AssignmentID: "a_u1_r2"},
	}, assignments, "conditional role と role 行が無い orphan assignment は ActiveAssignments に入らない")
}

func TestEffectivePolicy_ActiveAssignmentsAreNonNilForAnonymous(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	var assignments []plugin.ActiveRoleAssignment
	var seen bool
	registerProvider(t, svc, "ctx", []string{"canSearchNotes"},
		func(_ context.Context, req plugin.EffectivePolicyRequest) ([]plugin.EffectivePolicyContribution, error) {
			seen = true
			assignments = req.ActiveAssignments
			return nil, nil
		})

	_, err := svc.GetUserPoliciesChecked("")
	require.NoError(t, err)
	require.True(t, seen, "匿名解決でも provider は呼ばれるので ActiveAssignments は非nil空sliceである必要がある")
	assert.NotNil(t, assignments)
	assert.Empty(t, assignments)
}

func TestEffectivePolicy_ActiveAssignmentsExcludeExpiredAssignments(t *testing.T) {
	svc, roleRepo, assignRepo, _ := newTestService(t)
	roleRepo.Roles["r1"] = &model.Role{ID: "r1", Name: "A", Target: model.RoleTargetManual}
	roleRepo.Roles["r2"] = &model.Role{ID: "r2", Name: "B", Target: model.RoleTargetManual}
	assign(t, assignRepo, "u1", "r1")
	past := time.Now().Add(-time.Hour)
	assignRepo.Assignments["u1:r2"] = &model.RoleAssignment{ID: "a_u1_r2", UserID: "u1", RoleID: "r2", ExpiresAt: &past}

	var assignments []plugin.ActiveRoleAssignment
	registerProvider(t, svc, "ctx", []string{"canSearchNotes"},
		func(_ context.Context, req plugin.EffectivePolicyRequest) ([]plugin.EffectivePolicyContribution, error) {
			assignments = req.ActiveAssignments
			return nil, nil
		})

	_, err := svc.GetUserPoliciesChecked("u1")
	require.NoError(t, err)
	assert.Equal(t, []plugin.ActiveRoleAssignment{{RoleID: "r1", AssignmentID: "a_u1_r1"}}, assignments)
}

func TestEffectivePolicy_ActiveAssignmentsRideTheRoleCache(t *testing.T) {
	svc, roleRepo, assignRepo, counting := newCountingTestService(t)
	roleRepo.Roles["r1"] = &model.Role{ID: "r1", Name: "A", Target: model.RoleTargetManual}
	assign(t, assignRepo, "u1", "r1")

	var seen []plugin.ActiveRoleAssignment
	registerProvider(t, svc, "ctx", []string{"canSearchNotes"},
		func(_ context.Context, req plugin.EffectivePolicyRequest) ([]plugin.EffectivePolicyContribution, error) {
			seen = req.ActiveAssignments
			return nil, nil
		})

	_, err := svc.GetUserPoliciesChecked("u1")
	require.NoError(t, err)
	assert.Equal(t, []plugin.ActiveRoleAssignment{{RoleID: "r1", AssignmentID: "a_u1_r1"}}, seen)
	assert.Equal(t, int64(1), counting.calls.Load(), "assignment 文脈は 2 本目の query を増やさない")

	// 2 回目は provider 自身が LRU にヒットして resolver を呼ばないが、
	// activeRoleAssignments は native pass の前に必ず走るので、ここが 2 本目に
	// 出ていたら検出できる。
	_, err = svc.GetUserPoliciesChecked("u1")
	require.NoError(t, err)
	assert.Equal(t, int64(1), counting.calls.Load(), "warm role cache は role と assignment の両方を答える")
}
```

- [ ] **Step 3: 内部テストのREDを書く**

`internal/core/role/plugin_policy_internal_test.go` の末尾に追記する:

```go
func TestPolicyProviderCacheKeySeparatesAssignmentIdentities(t *testing.T) {
	runtime := newPolicyProviderRuntime(defaultEffectivePolicyProviderCacheEntries)
	provider := policyProvider{
		reg: plugin.EffectivePolicyRegistration{
			Keys: []string{"canSearchNotes"},
			// RoleIDs は両者とも空。**結果が変わるのは assignment だけ**なので、
			// これが cache key に assignment 入ったことの直接の証拠になる。
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

- [ ] **Step 4: RED を確認する**

```powershell
go vet ./internal/core/role
go test ./internal/core/role -count=1 -run "TestEffectivePolicy_ActiveAssign|TestPolicyProviderCacheKeySeparatesAssignmentIdentities|TestEncodePolicyProviderAssignments"
```

Expected: コンパイルエラー（`activeRoleAssignments` / `encodePolicyProviderAssignments` が未定義）。`ActiveAssignments` を渡さないので `assert` 側も落ちている。

- [ ] **Step 5: `roleService` 側でassignmentをcache に積む**

`internal/core/role/role_service.go` の `type roleCacheEntry struct` を次のように変更する:

```go
type roleCacheEntry struct {
	roles []*model.Role
	// assignments は同じ cache entry の active manual assignment。**roles と
	// 1 回の ListByUser から共に作られる**ので、effective policy の解決が
	// 2 本目の query を出さずに assignment を渡せる。expiry も roles と同じ
	// （最も早い assignment の expiresAt との min）。
	assignments []activeRoleAssignment
	expiresAt   time.Time
}
```

`GetUserAssigns` の**直前**に型と生成helperを追加する:

```go
// activeRoleAssignment は active な手動role assignment と、その role の組。
// effective policy の解決が plugin へ渡す assignment 同一性を、role cache
// entry の隣で保持する。
type activeRoleAssignment struct {
	roleID       string
	assignmentID string
}

// activeRoleAssignmentsFrom は active manual assignment を「1 role につき
// 高々 1 件」に還元する。
//
// **`Role` が nil なら入れない。** role 行を消した orphan assignment は
// GetUserRoles からも掉落しているので、ActiveAssignments に出すと native
// contribution の無い role を置換できてしまう。
//
// **conditional role は入れない。** `role_assignment` の行は手動割り当て
// だけが持つので、`target=conditional` の role を指す行はロールを
// manual → conditional に切り替えた直後などの残骸であり、置換対象にならない。
// `target` が空文字のものは通す — DB の `role_target` は既定 `manual` なので、
// 「conditional ではない」を読めば本番と一致する。
//
// **同じ role が複数行あれば assignment ID の最小値 1 件だけを残す。**
// `Assign` は先に `Exists` を見るので通常 1 行しか無いが、1 対 1 の置換が
// 成立するには「active な手動ロールにつき assignment は 1 つ」であることが
// host の**保証**になっている必要がある。決まっていないと置換対象が一意に
// 定まらない。
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

// cloneActiveRoleAssignments は cache entry を読んだ側が書き換えないよう値を
// 複製する（GetUserRoles が roles に対してやるのと同じ方針）。
func cloneActiveRoleAssignments(in []activeRoleAssignment) []activeRoleAssignment {
	if in == nil {
		return nil
	}
	return append(make([]activeRoleAssignment, 0, len(in)), in...)
}
```

`GetUserRoles` の `assignments, err := s.assignmentRepo.ListByUser(userID)` の**直後**に1行挿入する:

```go
	// **cache entry にも積む。** effective policy の解決は GetUserRoles の
	// 直後に active assignment を欲しがるので、ここから読むことで 2 本目の
	// query を出さない (#300 3-5 の cache 方針と同一)。
	activeAssignments := activeRoleAssignmentsFrom(assignments)
```

`s.userRoleCache[userID] = &roleCacheEntry{...}` を次のように変更する:

```go
		s.userRoleCache[userID] = &roleCacheEntry{
			roles:       snapshot,
			assignments: cloneActiveRoleAssignments(activeAssignments),
			expiresAt:   cacheExpiry,
		}
```

- [ ] **Step 6: `plugin_policy.go` 側で request を組み立てる**

`internal/core/role/plugin_policy.go` の

```go
	// provider には現在 active な native RoleID のみを、ソート + clone して渡す。
	roleIDs := activeRoleIDs(roles)
```

の**直後**に追記する:

```go
	// active manual assignment も渡す。**同じ role cache entry から読む**ので
	// 2 本目の query は出ない。RoleIDs だけでは「同じ role でもどの assignment
	// なのか」が分からず、assignment に紐づく状態を持つ plugin が作れないため。
	assignments := s.activeRoleAssignments(userID)
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

`type policyProviderCacheKey struct` にフィールドを1つ追加する:

```go
type policyProviderCacheKey struct {
	userID  string
	roleIDs string
	// assignments は ActiveAssignments の encoding。**RoleIDs が同じでも
	// assignment が違えば結果は違う** plugin がある（assignment ごとに状態を
	// 持つ）ので、付けないと付け外し / 再割り当ての直後に前の結果を返す。
	assignments string
}
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
// `{RoleID: "a", AssignmentID: "bc"}` と `{RoleID: "ab", AssignmentID: "c"}` が
// 同じ key になり、plugin には別の user の assignment ID が渡る。
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

`func activeRoleIDs(` の**直前**に accessor を追加する:

```go
// activeRoleAssignments は解決リクエストの active manual role assignment を
// 返す。**空文字 userID には非nil空sliceを返す**（匿名解決の契約）。
//
// **GetUserRoles と同じ per-user cache entry を読む。** 解決経路では直前に
// GetUserRoles が走っているので、2 本目の query が出ない。cache がないとき
// だけ repository を直接読む = 単独で呼んでも壊れないが、解決経路では起こら
// ない。
//
// **読み損ねたら空を返す。** 同じ repository 読みの失敗は直前の GetUserRoles
// が error にしているはずなので、解決経路ではここに到達しない。単独呼び出しで
// 失敗した場合も「置換しない」= native fallback 側なので、握り潰す向きは安全。
func (s *Service) activeRoleAssignments(userID string) []plugin.ActiveRoleAssignment {
	if userID == "" {
		return []plugin.ActiveRoleAssignment{}
	}
	s.userRoleCacheMu.RLock()
	entry := s.userRoleCache[userID]
	if entry != nil && time.Now().Before(entry.expiresAt) {
		cached := pluginActiveRoleAssignments(entry.assignments)
		s.userRoleCacheMu.RUnlock()
		return cached
	}
	s.userRoleCacheMu.RUnlock()
	assignments, err := s.assignmentRepo.ListByUser(userID)
	if err != nil {
		slog.Warn("role: active assignments を読み込めませんでした", "err", err)
		return []plugin.ActiveRoleAssignment{}
	}
	return pluginActiveRoleAssignments(activeRoleAssignmentsFrom(assignments))
}

// pluginActiveRoleAssignments は host 内部型を公開plugin型へ写す。戻り値は
// 常に非nil（len 0 でも make の戻りなので nil にならない）。
func pluginActiveRoleAssignments(in []activeRoleAssignment) []plugin.ActiveRoleAssignment {
	out := make([]plugin.ActiveRoleAssignment, len(in))
	for i, a := range in {
		out[i] = plugin.ActiveRoleAssignment{RoleID: a.roleID, AssignmentID: a.assignmentID}
	}
	return out
}
```

- [ ] **Step 7: focused command で GREEN を確認する**

```powershell
& (Join-Path (go env GOROOT) "bin\gofmt.exe") -s -w internal\core\role\role_service.go internal\core\role\plugin_policy.go internal\core\role\plugin_policy_test.go internal\core\role\plugin_policy_internal_test.go
go vet ./internal/core/role
go test ./internal/core/role -count=1
```

Expected: PASS。`TestEffectivePolicy_RoleIDsSortedAndCloned` を含む既存のproviderテストも引き続き緑（`RoleIDs` を変えないため）。

- [ ] **Step 8: commit する**

```powershell
git diff --check
git diff -- internal/core/role
git add internal/core/role/role_service.go internal/core/role/plugin_policy.go internal/core/role/plugin_policy_test.go internal/core/role/plugin_policy_internal_test.go
git commit -m "Add role: expose active assignments to effective policy providers"
```

Expected: 1 commit。query回数・conditional除外・orphan除外・expiry除外・非nil空sliceの test が一緒に並ぶ。**この commit の SHA を控えておく**。

---

### Task 3: Make A Role's Aggregate Entry Substitutable (Behaviour-Preserving Refactor)

`computePolicy` は「ロールごとに `map[key]override` を持つ」形なので、ロールの identity が失われていて置換を差し込めない。**挙動を変えずに**、ロール ID ごと・置換 entry 任意の `rolePolicyInput` へ持ち替える。Task 4 の差分を最小にするための土台。

**Files:**
- Modify: `internal/core/role/role_service.go:1220-1234` (`policyEntry` の直後に `rolePolicyInput` / `rolePolicyEntry`)、`:1236-1293` (`computePolicy`)、`:2091` の直前（`newRolePolicyInputs`）
- Modify: `internal/core/role/plugin_policy.go:208-215`（`roleOverrides` のループを `roleInputs` へ）、`:219`、`:280`
- Test: `internal/core/role/optout_aggregation_test.go`（`computePolicy` の呼び出し 4 箇所）

**Interfaces:**
- Consumes: なし（Task 2 の変更に依存しない純粋な refactor）
- Produces: 非公開 `role.rolePolicyInput{roleID string; overrides map[string]rolePolicyOverride; replacements map[string]policyEntry}` と `(rolePolicyInput).entry(key string, baseVal any) policyEntry`
- Produces: 非公開 `role.rolePolicyEntry(overrides map[string]rolePolicyOverride, key string, baseVal any) policyEntry`
- Produces: 非公開 `role.newRolePolicyInputs(roles []*model.Role) []rolePolicyInput`
- Produces: 内部テスト helper `policyInputs(overrides ...map[string]rolePolicyOverride) []rolePolicyInput`
- Changes: `computePolicy(key string, baseVal any, inputs []rolePolicyInput, extra []policyEntry) any`（第3引数の型が `[]map[string]rolePolicyOverride` から `[]rolePolicyInput` に変わる。internal 関数なので公開面ではない）

- [ ] **Step 1: テストを新しい形へ書き換える（RED を作る）**

`internal/core/role/optout_aggregation_test.go` の import 直後に helper を追加する:

```go
// policyInputs は「ロールごとの override だけを持つ」test 入力を
// rolePolicyInput へ包む。roleID は空のままでよい — この file の test は置換を
// 扱わない（置換は plugin_policy_test.go 側）。
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

- [ ] **Step 2: コンパイルが RED になることを確認する**

```powershell
go vet ./internal/core/role
```

Expected: `rolePolicyInput` / `rolePolicyEntry` / `newRolePolicyInputs` が未定義、`computePolicy` の引数型が不一致というエラー。

- [ ] **Step 3: `rolePolicyInput` と `rolePolicyEntry` を実装する**

`internal/core/role/role_service.go` の `type policyEntry struct` の**直後**に追加する:

```go
// rolePolicyInput は 1 つのロールが集約に贈るものの全体:
//
//	- roleID: どのロールか（置換の対象を突き合わせるため）
//	- overrides: `Role.Policies` を key ごとに decode した結果
//	- replacements: provider がこの要求で「native contribution の代わりに使う」
//	  と宣言した entry（key ごと）。provider が置換しなかったロールは nil。
//
// **aggregator は一切知らない。** entry を 1 つ選んで priority cascade に
// 積むだけなので、置換の有無は関数内で完結する。
type rolePolicyInput struct {
	roleID    string
	overrides map[string]rolePolicyOverride
	// replacements はこのロールの置換 entry。provider が置換しなかったロール
	// では nil のまま（= native entry が使われる）。
	replacements map[string]policyEntry
}

// entry は、このロールが key に対して贈る entry を返す。置換されていれば
// 置換 entry（**元 native entry の priority を引き継ぐ**）、されていなければ
// native entry。
func (in rolePolicyInput) entry(key string, baseVal any) policyEntry {
	if replacement, ok := in.replacements[key]; ok {
		return replacement
	}
	return rolePolicyEntry(in.overrides, key, baseVal)
}

// rolePolicyEntry は置換されていないロールが key に贈る entry。**この role が
// key を宣言していない場合は base 値を priority 0 で参加させる**という
// upstream 互換の既定がここに入る。
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

// newRolePolicyInputs は解決したロールを「ロールIDつきで集約できる」形へ
// 変換する。`Role.Policies` が空 / パース不能なロールは overrides が nil の
// まま入り、集約では base 参加 = 従来と同じ。
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

`computePolicy` の doc とシグネチャ、そして**先頭ループだけ**を次のように置き換える（`collected` 以降は変更しない）:

```go
// computePolicy resolves the effective value for a single policy key by
// applying upstream TS priority cascade + per-key aggregator. baseVal is
// the merged default+meta value used when a role specifies useDefault=true
// or when no role has an override. inputs carries each role's parsed policies
// plus the replacement entries an effective-policy provider supplied for it.
// extra carries effective-policy provider contributions for the key (already
// validated & type-checked by the host); it is merged into the same priority
// cascade as the role overrides.
func computePolicy(key string, baseVal any, inputs []rolePolicyInput, extra []policyEntry) any {
	// 各 role がこの policy に贈る entry を組み立てる。entry 無し =
	// priority=0, useDefault=true (= base にフォールバック) として扱う。
	collected := make([]policyEntry, 0, len(inputs)+len(extra))
	for _, in := range inputs {
		collected = append(collected, in.entry(key, baseVal))
	}
	// provider contribution は同じ priority cascade に参加させる
	// (= native と provider は同一 priority グループ内で aggregate される)。
	collected = append(collected, extra...)
```

- [ ] **Step 4: 挙動が変わっていないことを確認する（GREEN）**

```powershell
& (Join-Path (go env GOROOT) "bin\gofmt.exe") -s -w internal\core\role\role_service.go internal\core\role\plugin_policy.go internal\core\role\optout_aggregation_test.go
go vet ./internal/core/role
go test ./internal/core/role -count=1
```

Expected: PASS。**`internal/core/role` の全テスト（2000行超の `plugin_policy_test.go` / `role_service_test.go`、intersection/priority の `optout_aggregation_test.go`、`cond_formula_test.go` を含む）が1つも落ちないこと**。これが挙動保存の証拠になる。

- [ ] **Step 5: commit する**

```powershell
git diff --check
git diff --stat internal/core/role
git add internal/core/role/role_service.go internal/core/role/plugin_policy.go internal/core/role/optout_aggregation_test.go
git commit -m "Refactor role: carry role identity into policy aggregation"
```

Expected: 1 commit。`git diff --stat` は3ファイル。**この commit の SHA を控えておく**。

---

### Task 4: Role Policy Replacement (Public Contract, Application, Conflict)

`ReplaceRoleID` を公開面に足し、host が「1つのactive manual roleのnative contribution」をそのroleの**宣言済みpriorityのまま**置換できるようにする。競合は(role, key)単位でnativeへ戻し、checked resolver が固定sentinelを返す。

**Files:**
- Modify: `plugin/policy.go:17-27` (`EffectivePolicyContribution` に `ReplaceRoleID` を追加)
- Modify: `internal/effectivepolicy/validation.go:138-173` (`ValidateContributions` の署名と置換規則、末尾に `declaresActiveRole`)
- Test: `internal/effectivepolicy/validation_test.go:32-71`（既存tableに `roles` を足し、置換ケースを追加）、末尾に tie の test
- Modify: `internal/core/role/plugin_policy.go:24` の後（sentinel）、`:232-236`（map の宣言）、`:266-277`（provider ループ内）、`:279-281`（集約ループ）、`:289-293`（return）、`:642-656`（`lessPolicyContribution`）、`:661` の直前（helper 群）
- Test: `internal/core/role/plugin_policy_test.go`
- Modify: `plugin/plugintest/plugintest.go:321-353`
- Test: `plugin/plugintest/policy_test.go`
- Modify: `internal/entitycompat/testdata/golden_plugin_surface.txt`（再生成）
- Modify: `docs/plugins/authoring.md:817-822`（公開面一覧の `EffectivePolicyContribution`）

**Interfaces:**
- Consumes: `plugin.ActiveRoleAssignment` / `ActiveAssignments`（Task 1）、`(*Service).activeRoleAssignments`（Task 2）、`rolePolicyInput` / `rolePolicyEntry`（Task 3）
- Produces: `plugin.EffectivePolicyContribution.ReplaceRoleID string`
- Produces: `effectivepolicy.ValidateContributions(keys, activeRoles []string, contributions []plugin.EffectivePolicyContribution) bool`（第2引数新增）
- Produces: 非公開 `role.activeRoleIDsFromAssignments([]plugin.ActiveRoleAssignment) []string`
- Produces: `role.ErrEffectivePolicyReplacementConflict`（`errors.Is` で辿れる固定sentinel）
- Produces: 非公開 `role.collectPolicyReplacement(...)` / `role.applyPolicyReplacements(...)` / `role.hasPolicyReplacementConflict(...)`
- Preserves: `role.ErrEffectivePolicyProvider` は**単独のときは素の sentinel として**返す（既存の `require.Equal` 前提を壊さない）

- [ ] **Step 1: 公開型に `ReplaceRoleID` を追加する**

`plugin/policy.go` の `EffectivePolicyContribution` の `Order int` の**後**にフィールドを追加する:

```go
	// ReplaceRoleID turns this contribution into a **replacement** of one
	// active manual role's native contribution for Key, instead of an
	// additional contribution.
	//
	// - "" (the default) keeps the existing additive behaviour.
	// - non-empty names a role that must appear in
	//   [EffectivePolicyRequest.ActiveAssignments]. Conditional roles are
	//   never there, so they cannot be replaced.
	//
	// **置換は 1 対 1。** 対象 role/key の native contribution だけを差し替え、
	// priority は**元 native entry の宣言値を引き継ぐ**。他 role の
	// contribution と通常の priority / type 集約はそのまま行う。
	//
	// **Priority と Order は 0 のまま渡すこと。** 置き換える native entry が
	// 持った priority を引き継ぐので、plugin が選んでよいと二重定義になる。
	// host は 0 以外を malformed として provider 全体を失敗扱いにする。
	//
	// 同じ role/key を複数 provider が置換した場合は**競合**となり、host は
	// その pair だけを native contribution に戻す。checked 解決は error、
	// unchecked 解決は native fallback map を返す。
	ReplaceRoleID string
```

- [ ] **Step 2: `ValidateContributions` のテストを先に書く（RED）**

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
		// **同じ key でも role が違えば別 tie。** 複数 role を同時に置換する
		// plugin を「重複」で弾かない。
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
// **置換は (Key, ReplaceRoleID) で一意。** (Key, Order) だけで判定すると、同じ
// key の 2 つの role を同時に置換する plugin が「重複」で弾かれる。置換の
// Order は 0 しか選べないので、role ID を含めないと同時置換ができない。
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

```powershell
go test ./internal/effectivepolicy -count=1
```

Expected: コンパイルエラー（`ValidateContributions` の引数が3つでない）。

- [ ] **Step 3: `ValidateContributions` を実装する**

`internal/effectivepolicy/validation.go` の `ValidateContributions` を置き換える:

```go
// ValidateContributions reports whether contributions satisfy the host's
// native policy schema.
//
// activeRoles are the role IDs the request carried in
// [plugin.EffectivePolicyRequest.ActiveAssignments]. A contribution that sets
// ReplaceRoleID must name one of them — otherwise it would replace a
// contribution that does not exist in this request — and must leave Priority
// and Order at 0, because the replaced entry inherits the role's own declared
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

// declaresActiveRole reports whether roleID is one of the request's active
// manual roles. Only a role that carries a native contribution can have it
// replaced.
func declaresActiveRole(activeRoles []string, roleID string) bool {
	for _, active := range activeRoles {
		if active == roleID {
			return true
		}
	}
	return false
}
```

`internal/core/role/plugin_policy.go` の `resolvePolicyProviderCached` 内の呼び出しを3引数にする:

```go
	if ok {
		ok = effectivepolicy.ValidateContributions(provider.reg.Keys, activeRoleIDsFromAssignments(req.ActiveAssignments), contributions)
	}
```

`internal/core/role/plugin_policy.go` の `func activeRoleIDs(` の**直前**に helper を追加する:

```go
// activeRoleIDsFromAssignments は request の assignment が覆う role ID を
// ソート・重複除去して返す。**置換が名乗ってよい target の host 側の姿**。
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

- [ ] **Step 4: 置換適用の host テストを RED として書く**

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

// **置換でも explicit は contribution の値に従う。** explicit は intersection
// する policy だけで意味を持ち、「そのロールが明示的に設定したか」を表す。
// どちらのロールも key を宣言していないので native は base 参加 = 明示設定なし
// として intersection には何も乗らない。r1 を明示値で置換すると初めて乗る。
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

// **`UseDefault: true` の置換は「この role の override を base に戻す」** =
// native の useDefault と同じ扱い。explicit は立たない。
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
```

- [ ] **Step 5: 競合・失敗のテストを RED として書く**

同じファイルに追記する:

```go
// **競合は pair 単位。** provider 全体を失敗扱いにしてしまうと、その provider の
// 無関係な key まで native に戻ってしまう。置換したい plugin だけを黙らせる
// 形の被害を出さない。
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

// **provider 失敗は宣言 key を native へ戻す（既存挙動）。** 他 provider の
// 置換も同じ key なら巻き戻る。既存 sentinel は単独のときは素のまま。
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

// **置換が active でない role を名乗れば provider 全体が失敗扱い。** malformed
// output と同じ扱いなので、宣言 key は native へ戻る。
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

- [ ] **Step 6: `plugin_policy.go` に置換の適用と競合を実装する**

`internal/core/role/plugin_policy.go` の `ErrEffectivePolicyProvider` 定義の**直後**に sentinel を追加する:

```go
// ErrEffectivePolicyReplacementConflict is returned by GetUserPoliciesChecked
// when two providers replaced the same role and policy key. Fixed and
// identifier-free, like [ErrEffectivePolicyProvider]: which roles collided is
// an operator concern and never reaches the caller.
//
// The contested pair keeps its native contribution; every other contribution
// in the same result is still applied. It is joined with
// [ErrEffectivePolicyProvider] when a provider also failed in the same
// request, so `errors.Is` finds both.
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
	// 置換は元の native priority を引き継ぐので、role ごとの overrides を引ける
	// ようにしておく。
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
			// UseDefault のときは base を積むだけなので explicit ではない
			// (#2898、intersection の集約で「設定していない」と区別する)。
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
	// 置換が 1 件でもあれば、置換 entry 付きで集約し直す。競合した pair は
	// accepted から消してあるので、そこは native のままになる。
	if len(replacements) > 0 {
		replacedInputs := applyPolicyReplacements(roleInputs, replacements)
		for key, byRole := range replacements {
			if len(byRole) == 0 {
				// **この key の置換が全部競合した。** native 集約の結果 out[key]
				// をそのまま残し、他の provider の通常contribution だけ足し直す。
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
	// `err == ErrEffectivePolicyProvider` 相当を前提にしているため、1 つしか
	// 無いのに join すると等価性が壊れる。
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
// hasPolicyReplacementConflict reports whether any (key, role) pair was
// contested.
func hasPolicyReplacementConflict(conflicted map[string]map[string]bool) bool {
	for _, roles := range conflicted {
		if len(roles) > 0 {
			return true
		}
	}
	return false
}

// collectPolicyReplacement records one accepted replacement, or marks the
// (key, role) pair as contested when a second provider already replaced it.
//
// **置く entry は元 native entry を改変したもの**なので、declared priority と
// `explicit` の既定（その role が key を宣言していなければ base 参加 =
// explicit でない）を引き継ぎ、値だけ contrib の Value に差し替える。
//
// **競合は pair 単位で落とす。** provider 全体を失敗扱いにしてしまうと、置換
// していない無関係な key まで native へ戻すことになる。どちらの値も採らな
// い = 管理者が設定した role の値が残るので、fallback の向きは安全。
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
	// UseDefault は「この role の override を base に戻す」= native の useDefault
	// と同じ扱い。値を使う場合は explicit な設定になる。
	entry.value = clonePolicyValue(baseVal)
	entry.explicit = false
	if !contribution.UseDefault {
		entry.value = clonePolicyValue(contribution.Value)
		entry.explicit = true
	}
	byRole[contribution.ReplaceRoleID] = entry
}

// applyPolicyReplacements returns a copy of inputs with the accepted
// replacement entries attached. 元の slice は触らないので native pass は
// 自分の entry を保ったまま。
//
// **roleID が空の input には付けない。** 置換の target は必ず非空の role ID な
// ので空の role ID に一致するはずはないが、この guard で「読めないロールに置換
// が混ざる」形を1行で塞ぐ。
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
	// **置換は Order が 0 なので、置換同士は RoleID で順序を決める。** 空文字が
	// 先に来るので、ReplaceRoleID を持たない contribution の並びは従来と同じ。
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

- [ ] **Step 7: plugintest を production と同じ契約に揃える**

`plugin/plugintest/plugintest.go` の `EffectivePolicies` の resolver wrapper を次のように置き換える:

```go
	resolver := registration.Resolve
	if resolver != nil {
		registration.Resolve = func(ctx context.Context, req plugin.EffectivePolicyRequest) ([]plugin.EffectivePolicyContribution, error) {
			req.RoleIDs = append([]string(nil), req.RoleIDs...)
			// **ActiveAssignments も複製して渡す。** 本番 (core/role/plugin_policy.go) が
			// 複製しているのと同じで、テストが production より緩くないようにする。
			req.ActiveAssignments = append([]plugin.ActiveRoleAssignment(nil), req.ActiveAssignments...)
			contributions, err := resolver(ctx, req)
			if err == nil && !effectivepolicy.ValidateContributions(
				registration.Keys, plugintestActiveRoleIDs(req.ActiveAssignments), contributions,
			) {
				h.t.Errorf("plugintest: EffectivePolicies の出力が不正です")
				return nil, fmt.Errorf("plugintest: effective policy output is invalid")
			}
			return contributions, err
		}
	}
	return registration
}

// plugintestActiveRoleIDs は request の assignment が覆う role ID を
// ソート・重複除去して返す。置換が名乗ってよい target を harness 側で判定する
// ために使う（core/role の activeRoleIDsFromAssignments と同じ形）。
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
					// 宣言していない role の置換は host でも不正。harness が本番と
					// 同じ契約で弾くことを確認する。
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

- [ ] **Step 8: authoring.md の公開面一覧と golden を更新する**

`docs/plugins/authoring.md` の

```
type EffectivePolicyContribution struct
  Key string
  Priority int
  UseDefault bool
  Value any
  Order int
```

を次のように置き換える:

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
go run ./tools/pluginspec -write
git diff --stat internal/entitycompat/testdata/golden_plugin_surface.txt
```

Expected: golden に `plugin:   field EffectivePolicyContribution.ReplaceRoleID string` の1行だけ追加される。

- [ ] **Step 9: focused command で GREEN を確認する**

```powershell
& (Join-Path (go env GOROOT) "bin\gofmt.exe") -s -w plugin\policy.go plugin\plugintest\plugintest.go plugin\plugintest\policy_test.go internal\effectivepolicy\validation.go internal\effectivepolicy\validation_test.go internal\core\role\plugin_policy.go internal\core\role\plugin_policy_test.go
go vet ./plugin/... ./internal/effectivepolicy ./internal/core/role
go test ./plugin ./internal/effectivepolicy -count=1
go test ./internal/core/role -count=1
go test ./plugin/plugintest -count=1 -run "TestEffectivePolic"
go test ./internal/entitycompat -run TestPluginDoc -count=1
```

Expected: 全部 PASS。特に `internal/core/role` の既存テストで `require.Equal(t, role.ErrEffectivePolicyProvider, err)` が素の sentinel 前提で通っていること（join して既定が壊れていない証拠）。

- [ ] **Step 10: surface golden を正規化比較で確認する**

```powershell
go run ./tools/pluginspec > "$env:TEMP\rlp-surface.txt"
$golden = (Get-Content -Raw internal\entitycompat\testdata\golden_plugin_surface.txt) -replace "`r`n","`n"
$actual = (Get-Content -Raw "$env:TEMP\rlp-surface.txt") -replace "`r`n","`n"
if ($golden -ne $actual) { "SURFACE DRIFT"; Compare-Object ($golden -split "`n") ($actual -split "`n") } else { "SURFACE OK" }
```

Expected: `SURFACE OK`。

- [ ] **Step 11: commit する**

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
- Modify: `docs/plugins/authoring.md:330`（resolver の入力契約を説明する段落の末尾）、`:336` の後（contribution の Priority 段落の後）、`:342` の末尾（失敗providerの段落の末尾）、`:346`（純関数とcacheの段落）
- Modify: `docs/plugins/compatibility.md`（Task 1 で追記した段落の直後）

**Interfaces:**
- Consumes: Task 1 / 2 / 4 の実契約
- Produces: なし（ドキュメントのみ）
- Gate: `go test ./internal/entitycompat -run TestPluginDoc`（ローカル）と `make plugin-doc-check`（CI）

- [ ] **Step 1: resolver の入力契約に assignment を書き足す**

`docs/plugins/authoring.md` の「効果ポリシー」節で、`Keys`は空・空文字・重複を許さず…で始まる段落の**末尾**に追記する:

```markdown
resolverは`req.UserID`のほかに、activeな手動ロールのassignmentを`req.ActiveAssignments`で受け取る。`RoleIDs`は従来どおりconditionalロールを含むが、`ActiveAssignments`は`role_assignment`の行を持つ手動ロールだけなので、各`RoleID`は`RoleIDs`の部分集合になる。ロールごとに高々1件で、並びは`RoleID`順、匿名解決では非nilの空sliceになる。期限切れ・削除済み・`role`行が無いorphanは含まれない。`RoleIDs`は減っていないので、`ActiveAssignments`を読まないproviderの挙動は変わらない。
```

- [ ] **Step 2: 置換の契約とコンパイル可能な例を書き足す**

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
				// ロールごとに1件の置換を出す。Priority / Order は 0 のまま
				// 渡す = 元のネイティブentryのpriorityを引き継ぐ。
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

新しいGo fenceは `tests/plugin-doc/extract.py` の HEADER（`context`, `encoding/json`, `net/http`, `testing`, `plugin`, `peercache`, `plugintest`, `assert`, `require` しか import されない）で必ずコンパイルできる必要がある。上の例は新規 import を使わず、宣言済みの `ctx` / `inv` だけを使うので満たす。

- [ ] **Step 3: 競合と失敗の章を書き足す**

`docs/plugins/authoring.md` の「未宣言key、unknown key、priority範囲外…」で始まる段落の**末尾**に追記する:

```markdown
同じ`Key`と`ReplaceRoleID`を複数providerが置換した場合は**競合**になる。hostはそのpairを置換として受け入れず、ネイティブcontributionへ戻す — どちらの値も採らないので、管理者が設定したロールの値が残る。provider全体は失敗扱いにしないので、そのproviderの他のキーへの寄与は生き残る。checked解決は競合を表す固定errorを返し、unchecked解決はネイティブfallback mapを返す。provider失敗と併発した場合は両方のerrorが`errors.Is`で辿れる。provider失敗は宣言keyをネイティブへ戻すので、他のproviderの置換も同じkeyなら巻き戻る。
```

- [ ] **Step 4: 純関数とcacheの章を assignment に合わせて更新する**

`docs/plugins/authoring.md` の「`Resolve`は、明示的なinvalidationの間は`UserID`とsorted active `RoleIDs`だけで結果が決まる純粋関数として実装する…」で始まる段落を次の形に置き換える:

```markdown
`Resolve`は、明示的なinvalidationの間は`UserID`、sorted active `RoleIDs`、`ActiveAssignments`だけで結果が決まる純粋関数として実装する。時刻、request固有情報、未通知の外部状態へ依存してはならない。**同じ`RoleIDs`でも`ActiveAssignments`が違えば結果が違ってもよい**ので、hostはproviderごとの成功結果cache keyにassignment IDを含める（付け外し / 再割り当ての直後に前の結果を返さないため）。cacheはoperator設定`effectivePolicyProviderCacheEntries`（既定10000件、providerごと）のLRUであり、eviction時は同じ入力を再解決する。
```

- [ ] **Step 5: compatibility.md に replacement の additive 追記をする**

`docs/plugins/compatibility.md` の Task 1 で追記した段落の**直後**に追記する:

```markdown
`EffectivePolicyContribution.ReplaceRoleID`の追加も同じ扱い。**未設定なら追加contributionのまま**なので既存providerの挙動は変わらず、pluginは「対象ロールのネイティブcontributionを置き換える」という新しい契約にだけオプトインする。置換の`Priority`/`Order`制約はprovider作者の誤りを弾くもので、既存providerの出力形式は変えない。`APIVersion`は1のまま。
```

- [ ] **Step 6: doc gate を確認する**

```powershell
go test ./internal/entitycompat -run TestPluginDoc -count=1
```

Expected: PASS（公開面の一覧は Task 1 / 4 で更新済みなので、surface list 系は緑のまま）。

`make plugin-doc-check` はbash前提なのでこの作業ツリーでは直接回せない。**CI の `plugin-tests` job で `make plugin-doc-check` が緑になることを確認する**（これが新しいfenceのコンパイルを保証する唯一のgate）。

- [ ] **Step 7: commit する**

```powershell
git diff --check
git diff -- docs/plugins/authoring.md docs/plugins/compatibility.md
git add docs/plugins/authoring.md docs/plugins/compatibility.md
git commit -m "Docs: describe active assignment context and role policy replacement"
```

Expected: 1 commit。**この commit の SHA を控えておく**。

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

Expected: Task 1〜5 の5つの commit が見える。控えた SHA を oldest→newest 順で `$shas` に並べる。

**注意**: `git format-patch upstream/develop..feature/role-level-plugin` は範囲内の**全commit**（canDeleteAccount 相关的 Misaki commit も含む）を対象にしてしまう。**必ず Task 1〜5 の SHA を個別に指定する**（Step 4 参照）。

- [ ] **Step 2: upstream 起点のworktreeを作る**

```powershell
git fetch upstream develop
Test-Path "E:\tmp\opencode\mk-upstream-role-policy"
```

Expected: `Test-Path` が `False`。`True` なら別名を使う（`E:\tmp\opencode\mk-upstream-role-policy-2` など）。

```powershell
git worktree add "E:\tmp\opencode\mk-upstream-role-policy" -b upstream/role-policy-replacement-plugin-api upstream/develop
git -C "E:\tmp\opencode\mk-upstream-role-policy" log --oneline -1
```

Expected: `1a0f2012 Fix frontend: 復帰直後の再接続で、タイムラインの穴埋めが捨てられる (#3195)`。**これが `upstream/develop` の HEAD**。違う場合は `git fetch upstream` をやり直してからこの値を控える（以降の記録に使う）。

- [ ] **Step 3: upstream 側に Misaki 差分が既に無いことを確認する**

```powershell
git -C "E:\tmp\opencode\mk-upstream-role-policy" grep -n "canDeleteAccount" -- . | Select-Object -First 5
git -C "E:\tmp\opencode\mk-upstream-role-policy" grep -n "joinBasePolicyError" -- . | Select-Object -First 5
```

Expected: **どちらも出力なし**。出ているなら worktree を作り直す。

- [ ] **Step 4: generic commit を1つずつ patch にして `git am -3` で積む**

```powershell
$root = "$env:TEMP\rlp-patches"
Remove-Item -Recurse -Force $root -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Path $root | Out-Null
$shas = @('<task1-sha>','<task2-sha>','<task3-sha>','<task4-sha>','<task5-sha>')
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

Expected: `5`。**`git format-patch -1 A B C` は複数revでも最後の1つしか出さない**ので、必ず1つずつ別ディレクトリに出力すること（出力順を `01`〜`05` で固定し、`Sort-Object FullName` で oldest→newest を保つ）。

```powershell
git -C "E:\tmp\opencode\mk-upstream-role-policy" am -3 $patches
```

Expected: 5つとも clean apply。

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

- [ ] **Step 5: upstream 側で focused test が緑であることを確認する**

workdir を `E:\tmp\opencode\mk-upstream-role-policy` に変えて:

```powershell
go test ./plugin ./internal/effectivepolicy -count=1
go test ./internal/core/role -count=1
go test ./plugin/plugintest -count=1 -run "TestEffectivePolic"
go test ./internal/entitycompat -run TestPluginDoc -count=1
go vet ./plugin/... ./internal/effectivepolicy ./internal/core/role
```

Expected: 全部 PASS。`internal/core/role` は upstream 側にも `optout_aggregation_test.go` / `plugin_policy_test.go` / `plugin_policy_internal_test.go` があるので、**同じテストが緑になること**。これが generic diff が upstream 側で完結している証拠になる。

- [ ] **Step 6: leak 検査を実行する**

```powershell
git grep -n "canDeleteAccount" -- . | Select-Object -First 5
git grep -n "joinBasePolicyError" -- . | Select-Object -First 5
git diff --stat upstream/develop..HEAD
```

Expected: 2つの `git grep` が**出力なし**。`git diff --stat` に出るのは **Task 1〜5 のファイルだけ**:

```
 docs/plugins/authoring.md                      |  ...
 docs/plugins/compatibility.md                  |  ...
 internal/core/role/optout_aggregation_test.go  |  ...
 internal/core/role/plugin_policy.go            |  ...
 internal/core/role/plugin_policy_internal_test.go | ...
 internal/core/role/plugin_policy_test.go       |  ...
 internal/core/role/role_service.go             |  ...
 internal/effectivepolicy/validation.go         |  ...
 internal/effectivepolicy/validation_test.go    |  ...
 internal/entitycompat/testdata/golden_plugin_surface.txt | ...
 plugin/plugintest/plugintest.go                 |  ...
 plugin/plugintest/policy_test.go                |  ...
 plugin/policy.go                                |  ...
```

これ以外のファイルが1つでも出水たら **PR を出す前に upstream/develop 側の版に戻す**。

- [ ] **Step 7: origin へpush して PR を作る**

```powershell
git push -u origin upstream/role-policy-replacement-plugin-api
gh pr create --repo shiroha-a/mk --base develop --head Misaki-Project/mk:upstream/role-policy-replacement-plugin-api --title "Add generic plugin API: active role assignments and role policy replacement" --body-file "$env:TEMP\rlp-pr-body.md"
```

`$env:TEMP\rlp-pr-body.md` には次を書く:

```markdown
## What

Two additive extensions to the public plugin API, so a plugin can (a) know which native role assignment it is looking at and (b) replace one active manual role's native policy contribution instead of only adding to it. No schema change, no `APIVersion` bump.

- `plugin.ActiveRoleAssignment{RoleID, AssignmentID}` and `EffectivePolicyRequest.ActiveAssignments` — the user's active manual role assignments, one per role, sorted, non-nil for anonymous requests. Conditional roles are excluded because they have no `role_assignment` row, so every `RoleID` is a subset of `RoleIDs`. `RoleIDs` itself is unchanged and still covers conditional roles, so providers that read only `RoleIDs` behave exactly as before.
- `EffectivePolicyContribution.ReplaceRoleID` — a contribution naming an active manual role replaces that role's native contribution for `Key` one-for-one, keeping the role's own declared `priority`. `Priority` and `Order` must both stay `0`. Two providers replacing the same role and key is a conflict: the host keeps the native contribution for that pair and `GetUserPoliciesChecked` returns a fixed `ErrEffectivePolicyReplacementConflict`; the unchecked resolver returns the native fallback map. A failed provider still restores its declared keys to native, unchanged.

The host reads the assignments from the same per-user role cache entry, so resolution costs no extra query, and the per-provider LRU key includes the assignment IDs so unassign/re-assign cannot serve a stale result.

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

- [ ] **Step 8: PR の URL を Misaki branch 側に記録する**

`E:\tmp\opencode\mk-can-delete-account` に戻り、`docs/superpowers/plans/2026-09-27-role-policy-replacement-plugin-api.md` の**末尾に1節だけ**追記する:

```markdown
## Execution Record

- Upstream PR: <Step 7 で作った URL>
- Upstream branch: `Misaki-Project/mk:upstream/role-policy-replacement-plugin-api`（base `upstream/develop` @ `1a0f2012`）
- Applied commits (oldest first): `<sha1> <sha2> <sha3> <sha4> <sha5>`
```

```powershell
git add docs/superpowers/plans/2026-09-27-role-policy-replacement-plugin-api.md
git commit -m "Docs: record the upstream role policy replacement PR"
```

Expected: 1 commit。**この commit は upstream へ積まない**（Step 4 で SHA を個別指定しているので自動的に外れる）。

- [ ] **Step 9: レビューで修正が来たときの follow-up**

upstream 側で review が来たら、**同じ branch へ push する**（PR は自動更新される）:

```powershell
git -C "E:\tmp\opencode\mk-upstream-role-policy" push
```

upstream 側で contract を変えた場合は `plugin.APIVersion` の要否を**再検討する**（additive の範囲から外れる変更なら上げる）。Misaki branch 側への反映が必要なら同じ commit を `feature/role-level-plugin` に `git cherry-pick` する。

---

## Self-Review

- **Spec coverage**: `Generic Plugin API Extensions` の「Active Assignment Context」→ Task 1 + 2。「Role Policy Replacement」の契約（active role + 既知keyのみ / 1対1 / native priority維持 / 集約継続 / admin判定対象外 / 競合error / failure fallback / checked error / unchecked map）→ Task 4。「公開Plugin API変更にはcompatibility docs、surface golden、host wiring test、frontend type test」のうち **frontend type test 以外の3つ** → Task 1 / 4 / 5（frontend slot は本計画の明示スコープ外）。「No core DB migration」→ Global Constraints。`Delivery Boundaries` の upstream PR → Task 6。
- **スコープ外にしたもの**: Misaki固有のroleLevel（level / XP / curve / range / storage / route / UI）、frontend（`admin:role-editor` slot、misskey-ts）、`deliveryTargets`。spec のうち upstream PR に載らないものは別計画で扱う。
- **Placeholder scan**: 「TODO」「後で埋める」「同様に(Task N)」相当は無い。すべてのコードステップに実コードがある。実装者が判断を迫られる箇所（`SetUserRepo` の存在、formula型、upstream HEADのhash）は、該当する file/line を明示して「必ず確認すること」と書いた。
- **Type consistency**: `plugin.ActiveRoleAssignment{RoleID, AssignmentID}` / `ActiveAssignments` は Task 1 で定義し、Task 2（host）と Task 4（validation + plugintest）が同じ名前・型を使う。`role.activeRoleAssignment{roleID, assignmentID}` は Task 2 で定義し、Task 3 の `rolePolicyInput` が `overrides` として別物を持つ。`rolePolicyInput.roleID` は Task 3 で定義し、Task 4 の `applyPolicyReplacements` / `overridesByRole` が読む。`ValidateContributions(keys, activeRoles, contributions)` の3引数は Task 4 Step 2（test）→ Step 3（実装）で一致し、2つの呼び出し側（`core/role`、`plugintest`）も同じ形。`ErrEffectivePolicyReplacementConflict` は Task 4 Step 5（test）→ Step 6（実装）で一致。
- **Forward reference なし**: Task 4 が使う `roleInputs` は Task 3 Step 1 で導入済み。Task 3 は Task 1/2 の型に依存しない。Task 2 の `activeRoleAssignments` は Task 1 の型だけを必要とする。
- **信頼性**: gate コマンドはすべてこの作業ツリーで実測して緑になることを確認済み（`go test ./internal/core/role -count=1` PASS、`go test ./plugin ./internal/effectivepolicy -count=1` PASS、`go test ./plugin/plugintest -count=1 -run TestEffectivePolic` PASS、`go test ./internal/entitycompat -run TestPluginDoc -count=1` PASS、bundled plugin の `go vet` PASS）。`TestPluginSurfaceDrift` は行末差で元から赤なので、正規化比較を正式な gate にした。`git format-patch -1` の複数rev问题时も実測で把握済み（Step 4 に回避策を書いた）。
