# Misaki roleLevel Plugin Design

Date: 2026-09-27

Tracking issue: https://github.com/Misaki-Project/mk/issues/12

## Purpose

Misaki-Project固有のroleLevel機能を、mkのcore database schemaへ固有tableやcolumnを追加せず、bundled Pluginとして移行する。

この機能はCherryPick本来の機能ではない。移行元のローカルCherryPickにある`manualLevel`、`levelPolicies`、`policyAsLevel`、assignment XPをMisakiの旧実装として参照する。ただし既知の不整合やbugはそのまま再現せず、本specの明示的な契約を優先する。

## Scope

対象:

- 既存mk manual roleへのlevel設定の付加
- roleごとのXP curve
- native policy keyごとのlevel別policy
- assignmentごとのXP
- XPのset、add、multiplier操作
- 未assignment userへの自動assignment
- administratorおよび許可されたmoderator向け管理UI
- user profileでのlevel、XP、progress表示
- level role memberのXP順表示
- Plugin監査logとreconciliation
- 必要な汎用Plugin API拡張

対象外:

- `deliveryTargets`。将来PluginとしてIssue #11で追跡する
- coreの`role`または`role_assignment`へのMisaki固有column追加
- core role target enumへの`manualLevel`追加
- roleLevelによるadministrator/moderator権限そのものの変更
- GitHub上でのCherryPickデータ移行script管理

## Delivery Boundaries

変更は責務ごとに分割する。

1. 汎用Plugin API変更を独立Issue/PRにする。
2. 可能なら汎用変更だけを`shiroha-a/mk`へupstream PRする。
3. upstream PRにはMisaki固有のlevel計算、storage、route、UIを含めない。
4. upstream採用待ちでMisaki移行を止めず、Misaki branchで先行利用できるようにする。
5. roleLevel Plugin本体をMisakiの独立PRにする。
6. frontend Pluginを`Misaki-Project/misskey-ts`で実装・releaseし、mk側でassetsをpinする。
7. 旧データ移行SQLは全機能実装後にローカル成果物として作る。

## Architecture

role本体にはmk標準のmanual roleを使う。role assignment、期限、moderator/administrator属性もmk標準機能を使う。

Pluginは次だけを所有する。

- role IDに紐づくlevel設定
- XP curve
- level別policy rule
- native assignment IDに紐づくXP
- XP変更operation
- 監査log
- orphan reconciliation

Plugin無効時もnative roleとassignmentは有効なままにする。level別policy効果だけを停止し、native role policyへfallbackする。

Pluginはmkの`internal/` packageをimportしない。公開Plugin API、namespaced route、Plugin storage、native API callだけを利用する。

## Generic Plugin API Extensions

### Active Assignment Context

Effective policy requestへ、既存のsorted/deduplicated `RoleIDs`を残したままactive manual assignment参照を追加する。

```text
ActiveAssignments[]:
- roleId
- assignmentId
```

expiredまたは削除済みassignmentは含めない。conditional roleにはassignment IDがないため、この配列には含めない。

### Role Policy Replacement

既存effective-policy providerはnative contributionへ追加するだけである。roleLevelでは、対象roleの静的policy contributionをlevel計算結果へ置換する必要がある。追加だけではboolean ORやnumeric maxにより結果が変わるため、汎用replacement境界を追加する。

replacementの契約:

- Pluginはrequest中のactive role IDと既知native policy keyだけを対象にできる。
- replacementは指定role/keyのnative contributionを1対1で置換する。
- 元native contributionのpriorityを維持する。
- replacement後は他role contributionと通常のpriority/type aggregationを行う。
- administrator/moderator判定は対象外とする。
- 同じrole/keyを複数Pluginが置換した場合は競合errorとし、native contributionへfallbackする。
- Plugin failure、panic、timeout、malformed outputではnative contributionへfallbackする。
- checked resolverは上記failureをerrorとして返す。
- unchecked resolverは既存契約どおりfallback mapを返す。

### Frontend Slot

汎用`admin:role-editor` slotを追加する。contextには少なくともrole ID、readonly状態、保存済みroleの基本情報を渡す。

roleLevelは既存の`profile:info`および`admin:user` slotも使う。native role editorへMisaki固有componentを直接埋め込まない。

公開Plugin API変更にはcompatibility docs、surface golden、host wiring test、frontend type testを含める。破壊的変更を避け、追加APIとして提供する。

## Plugin Storage

Plugin-owned PostgreSQL schemaへ次を作成する。core tableへのforeign keyは作らず、native IDはopaque IDとして扱う。

### `role_level_config`

```text
role_id        text primary key
base_level     bigint not null
experience_curve jsonb not null
policy_ranges  jsonb not null
revision       bigint not null
created_at     timestamptz not null
updated_at     timestamptz not null
updated_by     text not null
```

### `role_level_experience`

```text
assignment_id  text primary key
role_id        text not null
user_id        text not null
experience     bigint not null check (experience between 0 and 9007199254740991)
created_at     timestamptz not null
updated_at     timestamptz not null
```

XPは`user_id + role_id`ではなくassignment IDへ紐づける。unassign後に同じroleを再assignしても、古いXPを復活させないためである。

### `role_level_operation`

```text
idempotency_key text primary key
actor_id        text not null
user_id         text not null
role_id         text not null
mode            text not null
operand         double precision not null
desired_exp     bigint
assignment_id   text
status          text not null
last_error      text
created_at      timestamptz not null
updated_at      timestamptz not null
```

未assignment userへのXP操作をnative assignment作成とPlugin XP保存の間で再開可能にする。

### `role_level_audit`

```text
id             bigserial primary key
actor_id       text not null
operation      text not null
role_id        text
user_id        text
assignment_id  text
before_state   jsonb
after_state    jsonb
note           text
created_at     timestamptz not null
```

Plugin migrationはversionedかつtransactionalにする。Plugin無効化・削除時にschemaを自動DROPしない。

## Level Model

`baseLevel`はXPが0の時点の最小実効levelである。負数、0、正数を許可し、JavaScript safe integer範囲に制限する。

```text
minimumLevel = baseLevel
maximumLevel = baseLevel + sum(experience segment level-up counts)
effectiveLevel = baseLevel + completed level-up count
progressionStage = effectiveLevel - baseLevel + 1
```

`progressionStage`は常に1始まりで、level別policy rangeの判定に使う。

例:

```text
baseLevel = -10
level-up count = 99
effective level = -10..89
progression stage = 1..100
```

### Default

全作成経路で次へ統一する。

```text
baseLevel = 1
experience curve = const 100 XP, 99 level-ups
effective level = 1..100
```

curveを明示的に空にした場合は`baseLevel`固定とする。

### XP Curves

各segment内の0始まりindexを`n`として、1回のlevel-upに必要なXPを計算する。

```text
const:       base
linear:      base + additional * n
exponential: base + additional * exponential^n
```

segmentが変わると`n`は0へ戻る。各segmentの累積必要XPは浮動小数点の閉形式で計算し、segment間offsetも丸めずに加算する。

```text
rule 1 offset = 2.5
rule 2 offset = 3.25
total offset = 5.75
```

整数XPがlevel thresholdへ到達したかは浮動小数点の累積値と比較する。thresholdが`10.25`ならXP `11`で到達する。到達後の表示上の`currentLevelExp`は整数XPと浮動小数点thresholdの差をfloorし、`0`として開始する。各level-up costを個別にceilしてから加算してはならない。

validation:

- segment typeは`const | linear | exponential`のみ。
- level-up countは1以上のsafe integer。
- `base`と`additional`はsafe integer範囲の整数。負数も入力できる。
- `exponential`は0より大きい有限数。
- 設定範囲内の各level-up必要XPは0より大きい有限値。
- ruleごとの累積値と、rule間offsetを加えた全累積必要XPは`Number.MAX_SAFE_INTEGER`以下。
- 計算途中でNaN、Infinity、overflow、0以下になるcurveは保存時に拒否する。

実装はrule setを順に評価し、const、linear、exponentialの累積値を閉形式で求め、各rule内をbinary searchする。計算量は`O(rule count * log(level-up count))`、追加memoryは`O(1)`とし、大きなlevel-up countを逐次loopしない。`exponential == 1`は専用式を使い、1に近い値では`Log1p`/`Expm1`相当で桁落ちを避ける。固定のlevel-up回数上限は追加しない。

### Experience Result

responseは次を返す。

```text
currentLevel
currentLevelExp
nextLevelExp
totalExp
minLevel
maxLevel
progressionStage
```

最大level到達後の余剰XPは`currentLevelExp`に保持する。最大levelまたはcurveなしの場合、`nextLevelExp`は`null`とする。JSONで表現できないNaNはAPIへ返さない。

## Level-Based Policies

policy rangeは実効level値ではなく`progressionStage`に対して設定する。

- rangeは重複しない半開区間として評価する。
- range長の合計は到達可能level数、すなわちlevel-up count合計+1と一致させる。
- defaultは全範囲を覆う1個の`base` rangeとする。
- range typeは`base | const | multiplier`のみ。

rule:

- `base`: instance/native defaultを使用する。
- `const`: 指定値を使用する。
- `multiplier`: numeric native policyだけで使用できる。
- multiplier値は`base + additional * range内0始まりoffset`で計算する。
- multiplier offsetは`baseLevel`の値に影響されない。
- booleanおよびstring/enum policyはnative型と一致する`const`だけを許可する。
- 未知native policy keyは拒否する。
- 計算結果がnative policy型・許容値を満たさない設定は保存時に拒否する。

旧実装のinclusive boundaryによるrange重複と、`effectiveLevel - startLevel`によるmultiplier offsetずれは再現しない。

## XP Mutation

modeは`set | add | multiplier`とする。operandは有限numberで、`multiplier`は百分率ではなく生の倍率として扱う。例として`1.5`は現在XPを1.5倍する。計算結果をfloorし、`0..Number.MAX_SAFE_INTEGER`へclampする。保存XPとAPI responseはsafe integerなのでJSON numberを使用し、文字列化しない。

既存assignmentでは、Plugin transaction内でXP、audit、operation状態を更新する。commit成功後にuserおよびroleのeffective-policy cacheをinvalidateする。commit前にcacheを更新しない。

未assignment userでは次の順序を使う。

1. idempotency key付きpending operationをPlugin DBへ保存する。
2. native `admin/roles/assign`を実行する。
3. native APIから新assignment IDを再取得する。
4. assignment IDにXPを保存する。
5. auditを記録しoperationをcompletedにする。
6. commit後にcacheをinvalidateする。

初期XP:

- `set`: operand
- `add`: operand
- `multiplier`: 0

途中で停止したpending operationはreconciliation jobが同じidempotency keyで再開する。

## Authorization

- level configの作成、更新、削除はadministratorだけが行える。
- XP変更対象roleのnative `canEditMembersByModerator`がtrueならmoderatorも操作できる。
- それ以外のXP変更はadministratorだけが行える。
- frontendの表示可否をauthorization boundaryにしない。
- public user responseはnative role visibilityを超える情報を返さない。
- role ID、user ID、assignment IDの対応を毎回検証し、Plugin storageの値だけを信用しない。

## Plugin Routes

Misskey APIとPlugin routerの契約に合わせ、routeはすべてPOSTとし、IDやpaging条件はrequest bodyで受ける。

```text
POST /api/plugin/role-level/admin/roles/list
POST /api/plugin/role-level/admin/roles/show
POST /api/plugin/role-level/admin/roles/update
POST /api/plugin/role-level/admin/roles/delete
POST /api/plugin/role-level/admin/users/show
POST /api/plugin/role-level/admin/change-exp
POST /api/plugin/role-level/roles/users
POST /api/plugin/role-level/users/show
POST /api/plugin/role-level/admin/audit
POST /api/plugin/role-level/admin/orphans
POST /api/plugin/role-level/admin/reconcile
```

native roleのassign/unassignはPlugin routeを増やさず、frontendまたはPlugin backendから既存`admin/roles/assign`と`admin/roles/unassign`を呼ぶ。

API errorにはstable codeを付け、validation、authorization、native API failure、storage failure、conflictを区別する。

## Frontend

- `admin:role-editor`: level有効化、curve、level別policy編集
- `admin:user`: assign、set、add、multiplier、unassign、監査情報
- `profile:info`: 現在level、XP、次levelまでのprogress
- Plugin管理page: level role一覧、XP順member一覧、orphan data、reconciliation状態
- native roleは通常どおりexploreとprofile badgeへ表示する。

frontend Pluginは`Misaki-Project/misskey-ts`でbundleする。direct URL、desktop、mobile、権限なし表示、Plugin disabled状態をtestする。

## Failure Semantics

- Plugin storage failureではreplacementを適用しない。
- timeout、panic、malformed output、複数Plugin競合でもreplacementを適用しない。
- unchecked policy resolutionはnative role policyへfallbackする。
- checked policy resolutionはfallback mapとerrorを返す。
- timeoutしたPluginの無効化は既存host契約に従う。
- malformed configは使用せず、管理画面、structured log、reconciliation結果へ表示する。
- stale role configまたはinactive assignment XPはpolicy計算で無視する。
- orphan rowは直ちに効果を失うが、監査のため保持期間後に削除する。

## Testing

最低限、次を自動testで固定する。

- 負数、0、正数の`baseLevel`
- default level 1..100
- const、linear、exponential curve
- rule間の浮動小数点offset保持 (`2.5 + 3.25 = 5.75`)
- 小数thresholdの到達判定と表示上のfloor、負additional、overflow、NaN、Infinity
- 大きなlevel-up countがrule内binary searchで評価されること
- range境界に重複・欠落がないこと
- multiplierが`baseLevel`に影響されないこと
- 最大levelで`nextLevelExp: null`
- set、add、multiplier、floor、clamp
- XP更新後にstale cache値が復活しないこと
- unassign/reassignで旧assignment XPが復活しないこと
- pending operationの再実行とidempotency
- moderator/administrator authorization
- priority 0/1/2と全native policy型
- Plugin failure時のchecked errorとunchecked native fallback
- 複数Plugin replacement conflict
- Plugin migrationの初回、再実行、rollback
- role editor、admin user、profile表示
- Plugin disabled時のnative-only動作
- frontend policy keyとbackend native catalogのdrift gate

Plugin storage testは実PostgreSQLで行う。unit testではcurve、validation、policy range、authorization、operation state machineをDBなしで検証する。

## Data Migration Boundary

データ移行は本機能群の最後に実施する。成果物はローカルSQL scriptだけで完結させ、GitHubへpushしない。

SQLは旧CherryPickの次をPlugin schemaへ変換する。

- `role.levelPolicies`
- `role.policies[*].policyAsLevel`
- `role_assignment.experience`
- `manualLevel` roleとassignment IDの対応

変換後、CherryPickだけに追加されたtable、column、index、enum値をDROPする。mkのschemaを正とし、CherryPick固有schemaを残さない。

SQL実行前提:

- roleLevel Plugin migrationが適用済みであること
- database backupが取得済みであること
- transaction内で実行すること
- source件数、変換件数、reject件数、target件数を照合すること
- 不正curveやorphan assignmentをreject tableまたは実行logへ明示すること
- commit前にdry-run queryで差分を確認すること

## Operational Notes

- Plugin無効化時はlevel別policyがnative fallbackへ変わるため、事前にimpactを表示する。
- Plugin schemaはPlugin削除時も自動削除しない。
- reconciliation jobはidempotentにし、native API failure時にデータを削除しない。
- policy resolution pathではnetwork callを行わず、indexed Plugin DB queryだけを使う。
- config/XP更新後のcross-worker invalidationを必須とする。
- migration SQLおよび実データをGitHub issue、PR、CI artifactへ添付しない。

## Acceptance Criteria

- mk core schemaへMisaki固有table、column、enumを追加しない。
- Plugin-owned schemaだけで完全なroleLevel機能を提供する。
- 旧Misakiのlevel、XP、level別policyをローカルSQLで移行できる。
- approved level calculation、validation、range semanticsを満たす。
- native role priority/type aggregationを維持する。
- stale cache、range overlap、JSON NaNの既知問題を解消する。
- generic Plugin API変更はMisaki固有実装から分離されている。
- upstream可能な汎用変更にはupstream PRを作成する。
- backend/frontend/plugin testsとoperational docsが揃っている。
