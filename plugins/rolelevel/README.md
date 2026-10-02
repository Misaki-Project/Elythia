# role-level

Misakiのmanual roleにlevel・XP・level別policyを追加するbackend-only bundled pluginです。coreの`role`/`role_assignment` schema、column、enumは変更せず、native APIでroleとassignmentを参照します。

## Build and storage

`mk-plugin.yml`のmanifest nameは`role-level`、plugin versionは`1.0.0`、API versionは1です。`tools/pluginbuild`が`plugins/rolelevel/mk-plugin.yml`と`go.mod`を発見し、backend registration (`cmd/misskey/plugins_generated.go`)に含めます。frontendはありません。

plugin-owned schemaは`plugin_role_level`だけです。Definition内のtransactional migration 1〜6が設定・XP・operation・audit・プロフィール表示設定・旧データ保管用の6 tablesを作り、migration 7がoperationに最初の監査用noteの保存列を追加します。XPの主キーは`assignment_id`、operationの主キーは`idempotency_key`です。XPはJSON/Go safe integer `0..9007199254740991`、operandは有限な`double precision`です。core tableへのforeign keyは張りません。plugin無効化・削除でschemaやmigration dataを自動DROPしません。

migration 6はCherryPickの`manualLevel`列が存在するときだけ、curve、対応中の段階別policy、assignment XP、プロフィール非表示設定をplugin schemaへ冪等に取り込み、native roleを`manual`へ変換します。現行mk-goに存在しない旧policy keyを含む完全な原本は`role_level_legacy_import.source`へ保存し、未知keyをeffective policyとして返すことはしません。

## Configuration

```yaml
plugins:
  role-level:
    enabled: true
    actorId: ""
    assignmentScanPages: 50
    orphanRetentionDays: 30
    reconcileCron: "*/10 * * * *"
    orphanCron: "17 3 * * *"
    pruneCron: "43 4 * * *"
```

`actorId`はnative APIを呼ぶ管理者です。空でもplugin DBだけで完結する処理は起動できますが、native role/assignmentを読む経路、XP変更、自動付与は`ROLE_LEVEL_ACTOR_NOT_CONFIGURED`になります。`assignmentScanPages`は1〜200、`orphanRetentionDays`は1〜3650、cronは5-field UTCです。`/admin/server-plugins`では設定キーだけを表示し、値はマスクされます。

## Level and policy

既定は`baseLevel=1`、`const 100 XP × 99 level-ups`、effective level 1〜100です。curveは`const`/`linear`/`exponential`で、実数コストを保持し、整数XPはthresholdを`ceil`で到達します。`progressionStage = effectiveLevel - baseLevel + 1`を半開区間rangeで評価します。range typeは`base`/`const`/`multiplier`です。

`currentLevelExp`は現在のレベル内で獲得済みのXP、`nextLevelExp`は次のレベルまでの残りXPです。画面の「獲得済み / 必要量」は`currentLevelExp / (currentLevelExp + nextLevelExp)`で表示します。例えば総XP372639、linearのbase5000/additional50、baseLevel0ではLv.57、獲得済み7839、残り11なので、表示は`7839 / 7850`です。最大レベルでは`nextLevelExp`は`null`です。

`RangeBase`は`nil` contributionを返します。したがってそのstageではnative policyを置換せず、native default/static contributionが残ります。rangeはpolicy keyごとに独立しており、異なるkeyは同じstageで重なれます。同じkeyのrangeだけは重複不可で、未指定stageはnative policyのままです。置換が必要なpolicyだけが`ReplaceRoleID`を使い、追加 contributionと取り違えません。

## API

全13 routeはPOSTで、query/path parameterはなくstrict JSON bodyです。未知field・trailing JSON・不正JSONは400 `ROLE_LEVEL_VALIDATION_FAILED`。stable error codeは`ROLE_LEVEL_*`です。

| route | auth | body | response shape |
|---|---|---|---|
| `/admin/roles/list` | moderator | `{}` | `{"roles":[…],"memberCounts":{roleId:int},"memberCountsTruncated":{roleId:bool}}` |
| `/admin/roles/show` | moderator | `{"roleId"}` | `{"role":…,"memberCount":int,"membersTruncated":bool}` |
| `/admin/roles/update` | administrator | `{"roleId","baseLevel","experienceCurve","policyRanges","revision","note"?}` | `{"role":…}` |
| `/admin/roles/delete` | administrator | `{"roleId","revision"}` | `{"roleId":…,"deleted":true}` |
| `/admin/users/show` | moderator | `{"userId"}` | `{"userId","roles":[…],"audit":[…],"operations":[…]}` |
| `/admin/change-exp` | role-aware moderator | `{"idempotencyKey","userId","roleId","mode","operand","note"?}` | `{"assignmentId","experience","status","resumed"}` |
| `/admin/audit` | moderator | `{"roleId"?,"userId"?,"limit"?}` | `{"entries":[…]}` |
| `/admin/orphans` | moderator | `{}` | `{"roles":{roleId:{tracked,orphans,prunable,orphanAssignmentIds}}}` |
| `/admin/reconcile` | administrator | `{"mode"?}` | `{"mode","result":{"steps":[…]}}` |
| `/roles/users` | public | `{"roleId","limit"?,"offset"?}` | `{"roleId","total","truncated","members":[…]}` |
| `/users/show` | public | `{"userId"}` | `{"userId","roles":[…]}` |
| `/users/profile-settings` | signed-in user | `{}` | `{"userId","roles":[…]}` |
| `/users/profile-hide` | signed-in user | `{"roleId","hidden"}` | `{"roleId","hidden"}` |

`/admin/reconcile` modeは`resume-operations`、`reconcile-orphans`、`prune-orphans`、`all`（省略時は`all`）だけです。unknown modeは400です。公開応答はnative visibilityを超えず、未付与roleを返しません。

`/admin/roles/update` の `baseLevel` / `revision` と `/admin/change-exp` の `operand` は必須です。明示的な `0` は有効ですが、省略または `null` は `ROLE_LEVEL_VALIDATION_FAILED` になります（revision `0` は新規作成）。

Misaki本体では`POST /api/plugin/role-level/admin/change-exp`だけ、専用権限`write:admin:role-level-experience`付きのAPIキーから呼び出せます。管理者の「API」設定で「ロールの経験値を変更する」を選択してください。APIキーの権限だけでは足りず、発行元アカウントが管理者/モデレーターであり、対象がmanual roleであることも必要です。モデレーターは`canEditMembersByModerator`が有効な非管理者ロールだけ変更できます。`write:admin:roles`や`read:account`で代用はできません。他のrole-levelルートや他プラグインはAPIキーから引き続き拒否します。認証は`Authorization: Bearer <APIキー>`で渡し、strict JSON bodyに`i`を追加しないでください。

`note`はXP変更のrole-level監査ログ（`/admin/audit`）に保存します。ユーザーのモデレーションノートや本体モデレーションログは変更しません。同じ`idempotencyKey`で再送すると、XPと監査ログを二重に追加しません。最初に受理したnoteに既存の長さ制限を適用してoperationへ保存し、再送や自動再開でも置き換えません。長い文面は500バイト以内の完全なUTF-8文字までに切り詰め、末尾に`...`を付けます。旧版の未完了operationにはnoteが保存されていないため、更新前の文面は復元できません。

対象ユーザーが未付与なら、既存のXP操作と同様に発行元アカウントとしてロールの付与を試み、本体の認可が許可した場合だけ付与します。この専用scopeはAPIルートを制限するもので、特定の`roleId`だけを許可する設定ではありません。`canEditMembersByModerator`は全モデレーターに共通のロール設定です。外部連携では必要なロールだけこの設定を有効にし、モデレーターのAPIキーを使ってください。管理者のログイントークンは渡さないでください。

XP modeは`set`、`add`、`multiplier`です。multiplierのoperandはraw factor（`1.5`は1.5倍）。結果はfloat64計算後floorし、safe integer範囲に収めます。同じidempotency keyの再送は保存済み結果を返し二重適用しません。payload違いは`ROLE_LEVEL_IDEMPOTENCY_CONFLICT`、`failed`を含むterminal operationは`ROLE_LEVEL_OPERATION_FAILED`で再試行できず、新しいkeyが必要です。pending/assigning/applyingだけが同じkeyでreconciliation再開対象です。

## Jobs, audit, and disablement

`resume-operations`は停止したoperationを再開し、`reconcile-orphans`はnative member一覧とXP rowを照合してorphanを記録するだけで削除しません。`prune-orphans`はretention期間を超えたorphanだけを削除し、削除とprune auditを同一transactionで記録します。native API failure時は削除しません。auditのactionは `config-create`、`config-update`、`config-delete`、`change-exp`、`prune-orphans` です。auditにはXP before/after、operation、orphan/pruneの結果を残します。

設定のcreate/update/deleteは、revision確認付きの設定読み書きと対応するaudit insertを1つのDB transactionで行います。どちらかが失敗した場合は設定とauditの両方をrollbackします。XP変更もXP row、audit、operation完了状態を1 transactionでcommitします（native API呼び出し中はtransactionを保持しません）。手動操作のactorは要求者 (`req.UserID()`)、XP操作のactorはidempotency operationに保存した `ActorID`、cronのpruneは設定された `actorId`（要求者なし）です。

設定で`plugins.role-level.enabled: false`にすると再buildなしでlevel policy contributionだけを無効化できます。native role/assignmentは残り、plugin schemaは自動削除されません。
