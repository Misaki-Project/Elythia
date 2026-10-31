# FxTwitter preview v0.4.0 導入手順

これは運営者がレビュー後に実施する手順です。本番への適用、外部取得の有効化、実際の X 投稿・公式 iframe の表示確認は、この手順の作成時には実施していません。v0.4.0 は現行 SDK 向けの詳細画面専用移植で、公開済みタグではありません。コードはこの Elythia fork の独立 Go module `plugins/fxtwitter-preview` にあり、別の公開プラグイン repository は前提にしません。

## 1. 適用条件と復旧準備

依存の順序は、[PR29（上流同期・履歴安全性）](https://github.com/Misaki-Project/Elythia/pull/29) → [PR30（host API）](https://github.com/Misaki-Project/Elythia/pull/30) → [PR31（Fx 移植）](https://github.com/Misaki-Project/Elythia/pull/31) です。2026-10-31 の GitHub 確認時点で、PR29 は同日04:11:57 UTCに merge 済み（merge commit `a0e8796c2f3911c752a25c5ac70fb001c5cb4459`）、PR30/31 は Draft・未 merge です。今後はレビューと CI を確認して PR30、PR31 の順に適用します。merge 状態と本番に導入済みであることは別です。隔離検証では、依存変更を含むレビュー済み最終 plugin commit を直接 checkout できます。

同じ確認時点で、PR30 の base はすでに `Misaki-develop` へ変更済みで、PR31 の base は `feature/fxtwitter-host-api-20261030` のままです。今後 host の PR30 を merge した後、運営者は repository の merge 方針を確認し、PR31 の base を `Misaki-develop` へ変更して差分と CI を再確認します。将来の squash/rebase によって祖先が変わり差分が膨らむ場合は、その方針に合わせた branch の再整備と再レビューが必要です。未確認の merge 方式や force-push を手順として固定しません。

確認済みの依存点は、同期 commit `076c0d2f3823f316c1ccdac692fc8a3f7c7e6bb3`（Misaki 基点 `29715b8954ea844ff2799d50a8c3903406ca7c6a`、上流 `2a026d427f9e35558b6a03eac06348d7882834dc`）、host commit `1bd68821116a6926445c9de5e154f1c052f93336` です。以下の `<REVIEWED_PLUGIN_SHA>` は、最終レビュー・CI を通過し、これらを含む **40桁の commit SHA** に置き換えます。ブランチ名や未確認のタグで代用しません。

旧117まで適用済みの既存 DB は、先に [Misaki migration 履歴照合手順](../misaki-migration-reconciliation.md) を読んでください。PR29 の guard は旧履歴を自動変換しません。バックアップをアプリから切り離した検証 DB へ復元し、全履歴と core/local 台帳の前提を照合する手順が必要です。guard の無効化や、番号だけを見た台帳の書換えで先へ進めません。

運用変更前に [バックアップ](../backup.md) と [復元・デプロイ](../deployment.md) の手順を確認し、DB、設定、ドライブ／object storage のメディア、直前の互換 image の digest と設定を保全します。DB dump だけでは設定・メディア・PostgreSQL role は戻りません。保存物には秘密情報が含まれるため、安全な保存先とアクセス制限を使い、内容をログへ出しません。

```sh
# migration/ を参照でき、対応する PostgreSQL tools がある環境で実行する
elythia backup take -config '<CONFIG>'
elythia backup list -config '<CONFIG>'
elythia backup verify -config '<CONFIG>' latest
```

`<CONFIG>` は実際のバックアップ設定ファイルです。アプリの distroless image には `pg_dump` がありません。既存ドキュメントの backup-tools image／Compose backup service、または DB と同じ PostgreSQL major の `pg_dump` / `pg_restore` / `initdb` / `pg_ctl` を備えたホストを使います。verify の結果を確認し、隔離した復元先で新しいバイナリの起動と復旧をリハーサルしてから、本番の変更日時・メンテナンス・切替手順を決めてください。

初回の選択組込みバイナリ起動では、外部取得 OFF でも plugin 所有 migration が動き得ます。旧版と現行の schema 命名は `plugin_` + hyphen を underscore にした plugin 名で一致し、`fxtwitter-preview` は `plugin_fxtwitter_preview` とその `schema_migrations` を使います。SDK import/module 名変更だけでは移動しません。ソース比較で migration 1〜5 は同一、6 が追加されています。これは実環境の schema を発見したという意味ではありません。

migration 6 は既存の non-ready public cache の fallback metadata と embed capability を無効にします。一時失敗だった古い non-ready fallback も再取得が必要です。ready payload、監査本文・author snapshot、`x_user` / `x_post`、利用者の note/account を削除する移行ではありません。

## 2. 固定 checkout と選択ビルド

Linux または Docker builder を使います。リポジトリの固定版は Go **1.27.3**、Node **26.12.1**、pnpm **12.14.0** です。Windows ネイティブでは server の Unix syscall 依存があるため、同じ手順のビルダーにはしません。以下は専用の clean checkout を用意してから実行します。運用中の checkout を上書きしません。

```sh
set -eu
git checkout --detach '<REVIEWED_PLUGIN_SHA>'
test "$(git rev-parse HEAD)" = '<REVIEWED_PLUGIN_SHA>'
git merge-base --is-ancestor 076c0d2f3823f316c1ccdac692fc8a3f7c7e6bb3 HEAD
git merge-base --is-ancestor 1bd68821116a6926445c9de5e154f1c052f93336 HEAD

# 3つとも未配置の専用 checkout でだけ実施。存在する場合は止めて由来を確認する
test ! -e plugins/genshin
test ! -e plugins/fedwatch
test ! -e plugins/hsr
printf '%s\n' \
  'genshin https://github.com/Misaki-Project/mk-plugin-genshin.git 9a50079ec9d198ab44e4b9ff24d0a408109c09da' \
  'fedwatch https://github.com/Elythia-Network/elythia-plugin-fedwatch.git af105a020656aaba121998a0bd5a51bee232dec8' \
  'hsr https://github.com/Misaki-Project/elythia-plugin-hsr.git 820068d720654650d5dc05d4136ad9f32f677fef' \
  | GOWORK=off go run ./tools/pluginresolve

GOWORK=off go run ./tools/pluginbuild -include-disabled-dir plugins/fxtwitter-preview
docker build -f Dockerfile.bundled \
  --build-arg PLUGIN_INCLUDE_DISABLED_DIR=plugins/fxtwitter-preview \
  -t '<LOCAL_IMAGE_TAG>' .
```

上記の shell は `set -eu` を有効にして実行し、途中で失敗したら止めます。`tools/pluginresolve` は標準入力の `<name> <https-url> <ref>` を読み、既存ディレクトリがあれば失敗します。既存の配置を削除・上書きして再実行せず、別の clean checkout で確認してください。3つの production plugin pin と、意図的に既定有効な `rolelevel` は維持します。`go mod tidy` は行いません。

`elythia-plugin.yml` の `disabled: true` はそのままです。これはビルドの既定選択で、runtime 設定とは別です。通常の generator では Fx を除外し、上の `-include-disabled-dir`／Docker build argument は Fx だけを追加選択します。`rolelevel` と配置した3つは含まれ、他の disabled sample は選びません。生成された `cmd/elythia/plugins_generated.go`、`frontend/packages/frontend/src/server-plugins.generated.ts` と `mk-plugins.generated.json` でこの集合を確認してください。

`<LOCAL_IMAGE_TAG>` は運営者が選ぶローカルタグです。registry、公開先、image digest は未確定で、この手順は push しません。既存の `misaki-genshin-image` workflow が変更なしで Fx を含むとは限りません。ここでは選択 argument を渡すローカルビルドを明示しています。ビルド成功後の `docker image inspect '<LOCAL_IMAGE_TAG>' --format '{{.Id}}'` とソース SHA を記録します。本番への配布・実際の image digest 固定・サービス切替は、運用環境を確認した別の手順で実施します。

## 3. 初回は外部取得 OFF

隔離検証環境の既存設定へ、次を統合します。ほかの plugin 設定を置き換えません。

```yaml
plugins:
  fxtwitter-preview:
    enabled: true
    externalFetchEnabled: false
    refetchIntervalMinutes: 60
    auditRetentionDays: 0
    videoPlaybackMode: linkOnly
```

`enabled` は plugin の runtime 有効性、`externalFetchEnabled` は Fx の外部取得許可です。未指定時の外部取得は **false**。上の `linkOnly` は初回の段階設定として明示しています。実コードの動画モード省略時値は `officialEmbed` ですが、外部取得 OFF のままでは embed を発行しません。後から変更した設定の反映には、環境の既存手順で再起動／切替が必要です。

初回の rate limit 設定は YAML の `authenticatedColdFetchesPerHour`、`authenticatedColdFetchesPerDay`、`globalColdFetchesPerHour`、`roleColdFetchLimits` を seed とし、未指定時はそれぞれ100/時、600/日、300/時、role別指定なしです。DB に設定が作られた後は管理画面に保存した値を使い、YAML を変えても保存値を上書きしません。外部許可・動画モード・再取得間隔・監査保持は runtime 設定です。初回後は管理画面で保存済みの rate limit も確認します。管理者の設定操作と moderator の監査閲覧権限は別です。

60分は **詳細要求を契機にした投稿ごとの再取得間隔** で、cache の削除 TTL、自動巡回、削除検知の SLA ではありません。詳細画面要求がなければ再取得しません。`auditRetentionDays: 0` は自動 prune なしです。監査に保存するのは URL 展開・HTML entity decode・media facet 除去後の `normalized_text_v1` で、Fx 生 JSON や原文そのままの `raw_text` ではありません。公開 cache と moderator 用監査本文は別で、監査を公開 fallback に使いません。

`<COMPOSE_FILE>`、`<APP_SERVICE>`、設定の mount 先、ホスト名、DB 接続先、image の配布先は実環境未確認です。実際の Compose／サービス定義に合わせ、バックアップ・復元リハーサルと承認済み切替手順を先に確定してください。ここから本番の migration やサービス起動を自動実行するコマンドはありません。

## 4. 隔離環境での確認

初回は外部取得 OFF のまま、選択した image と復元済み検証 DB を使います。

- 組込み一覧と `/admin/plugin/fxtwitter-preview` が存在し、監査本文に「正規化済み」の表示があること。匿名や一般利用者には監査 API が拒否され、moderator は監査を見られること。rate limit の変更は管理者だけが行えること。
- capabilities の `externalFetchEnabled` が false で、Fx preview・画像 proxy・embed と、残っていた fetch job が Fx の外部送信を起こさないこと。監査保持 job は別で、保持0なら自動 prune しません。Elythia 全体の通常 OGP 等まで通信停止する設定ではありません。
- fixture の frontend tests、型・lint/SPDX、実 PostgreSQL tests の CI が最終 SHA で通っていること。fixture 成功を実 X 投稿や実 browser の CSP 動作確認の代わりにはしません。

### 将来、運営者が意図して外部取得を有効にする場合

この節は今後の運営者の操作です。本変更の配布既定は外部取得 OFF のままです。外部送信先・利用条件・予算・監査保持方針を確認後、まず隔離環境で `externalFetchEnabled: true` にして反映します。video は `linkOnly` のままでも preview を検証できます。公式埋め込みを許す段階でだけ `videoPlaybackMode: officialEmbed` にします。

ログイン済み利用者が可視 note の詳細を表示し、CW があれば開いた後に、投稿 ID に対する Fx API (`api.fxtwitter.com`) への要求が起こり得ます。画像・avatar・poster は認証付き same-origin proxy を経由し、サーバーが許可した `pbs.twimg.com` だけを取得します。タイムラインや note の収集はしません。動画の直接配信／YouTube player は使わず、対応外は元投稿へのリンクへ戻ります。公式全投稿 iframe は明示クリック後だけ `platform.twitter.com/embed/index.html` を browser に読み込ませ、X 側への browser 通信が起こり得ます。

運営者が選んだ公開投稿で、no-CW timeline では Fx 取得なし、未開封 CW ではなし、詳細で開封後だけ取得、CW 再閉鎖・タブ非活性・画面離脱で media/iframe が片付くことを確認します。公式 embed をクリックした場合、失敗・timeout 時も元投稿リンクが残ることを確認します。実 browser の CSP、sandbox、通信環境によって表示できるかは別途確認が必要です。

表示理由は `deleted` / `suspended` / `private` / `blocked` / `unavailable` / `unknown` / `fetch_failed` です。既知の理由は明示的な upstream tombstone だけから採用し、HTTP 404/403 や不明な取得失敗を削除・非公開と断定しません。terms-withheld 専用判定もありません。引用の利用不可理由と未知理由を区別し、すべての upstream 状態が都合よく再現できるとは想定しないでください。監査を参照しても一般利用者向け表示へ過去本文が漏れないことを確認します。

## 5. ロールバック

まず運営者が `externalFetchEnabled: false` を反映し、Fx の新たな外部取得・既存 job の送信・media proxy・embed 発行を止めます。すでに browser に読み込んだ iframe は閉じるか再読込みが必要で、サーバー側の切替で過去の iframe 通信を取り消すことはできません。

その後、検証済みの互換性がある直前 image と設定へ、環境の切替手順で戻します。plugin schema、監査、note/account は保持し、schema を drop しません。core migration を含む更新では image を戻すだけで DB が戻るとは限らず、互換性のない旧 core を起動しません。自動 down migration は行いません。DB 全体の復元が必要な場合だけ、対応するバックアップ・メディア・設定を組み合わせ、[既存の復元手順](../deployment.md)で影響と履歴 guard を再確認します。
