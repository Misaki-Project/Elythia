# role-level

Misakiのmanual roleにlevel・XP・level別policyを追加するbackend-only bundled pluginです。coreの`role`/`role_assignment` schema、column、enumは変更せず、native APIでroleとassignmentを参照します。

## Build and storage

`mk-plugin.yml`のmanifest nameは`role-level`、plugin versionは`1.0.0`、API versionは1です。`tools/pluginbuild`が`plugins/rolelevel/mk-plugin.yml`と`go.mod`を発見し、backend registration (`cmd/misskey/plugins_generated.go`)に含めます。frontendはありません。

plugin-owned schemaは`plugin_role_level`だけです。Definition内のtransactional migration 1〜4が4 tables (`role_level_config`, `role_level_experience`, `role_level_operation`, `role_level_audit`)を作ります。XPの主キーは`assignment_id`、operationの主キーは`idempotency_key`です。XPはJSON/Go safe integer `0..9007199254740991`、operandは有限な`double precision`です。core tableへのforeign keyは張りません。plugin無効化・削除でschemaやmigration dataを自動DROPしません。

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

`RangeBase`は`nil` contributionを返します。したがってそのstageではnative policyを置換せず、native default/static contributionが残ります。置換が必要なpolicyだけが`ReplaceRoleID`を使い、追加 contributionと取り違えません。

## API

全11 routeはPOSTで、query/path parameterはなくstrict JSON bodyです。未知field・trailing JSON・不正JSONは400 `ROLE_LEVEL_VALIDATION_FAILED`。stable error codeは`ROLE_LEVEL_*`です。

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

`/admin/reconcile` modeは`resume-operations`、`reconcile-orphans`、`prune-orphans`、`all`（省略時は`all`）だけです。unknown modeは400です。公開応答はnative visibilityを超えず、未付与roleを返しません。

`/admin/roles/update` の `baseLevel` / `revision` と `/admin/change-exp` の `operand` は必須です。明示的な `0` は有効ですが、省略または `null` は `ROLE_LEVEL_VALIDATION_FAILED` になります（revision `0` は新規作成）。

XP modeは`set`、`add`、`multiplier`です。multiplierのoperandはraw factor（`1.5`は1.5倍）。結果はfloat64計算後floorし、safe integer範囲に収めます。同じidempotency keyの再送は保存済み結果を返し二重適用しません。payload違いは`ROLE_LEVEL_IDEMPOTENCY_CONFLICT`、`failed`を含むterminal operationは`ROLE_LEVEL_OPERATION_FAILED`で再試行できず、新しいkeyが必要です。pending/assigning/applyingだけが同じkeyでreconciliation再開対象です。

## Jobs, audit, and disablement

`resume-operations`は停止したoperationを再開し、`reconcile-orphans`はnative member一覧とXP rowを照合してorphanを記録するだけで削除しません。`prune-orphans`はretention期間を超えたorphanだけを削除し、削除とprune auditを同一transactionで記録します。native API failure時は削除しません。auditのactionは `config-create`、`config-update`、`config-delete`、`change-exp`、`prune-orphans` です。auditにはXP before/after、operation、orphan/pruneの結果を残します。

設定のcreate/update/deleteは、revision確認付きの設定読み書きと対応するaudit insertを1つのDB transactionで行います。どちらかが失敗した場合は設定とauditの両方をrollbackします。XP変更もXP row、audit、operation完了状態を1 transactionでcommitします（native API呼び出し中はtransactionを保持しません）。手動操作のactorは要求者 (`req.UserID()`)、XP操作のactorはidempotency operationに保存した `ActorID`、cronのpruneは設定された `actorId`（要求者なし）です。

設定で`plugins.role-level.enabled: false`にすると再buildなしでlevel policy contributionだけを無効化できます。native role/assignmentは残り、plugin schemaは自動削除されません。
