# Misaki Release 1.5.0・2026.10.0追従版への移行

この手順は、Misakiのrole-level・XP・原神連携を保持して2026.10.0追従版へ更新する運用者向けの手順です。手順の記載は、本番適用やworkflow実行の承認を意味しません。

## 1. 適用前の条件

**Release 1.5.0の統合とCI検証、原神入り最終runtime imageの公開が完了しました。ただし実ブラウザ・本番構成での動作確認と本番適用は未実施です。公開成功だけを理由に本番を切り替えないでください。**

Release 1.5.0統合frontendの全791件と原神テスト、および本体のGitHub CI（全4分割のraceテスト・frontend整合・lint・build・plugin-tests・API互換・同梱selftest）は成功しています。利用者の指示に従い、ローカルの全体`make check`は中断し、PASS扱いしていません。最終imageの公開workflowとregistryのdigestは一致し、linux/amd64対応とrole-level・原神のビルドへの組込みを確認しました。実ブラウザと本番の動作確認は別に扱います。

検証証拠: [本体CI](https://github.com/Misaki-Project/mk/actions/runs/37203326984)、[API互換](https://github.com/Misaki-Project/mk/actions/runs/37203326977)、[同梱selftest](https://github.com/Misaki-Project/mk/actions/runs/37203327152)、[最終image公開](https://github.com/Misaki-Project/mk/actions/runs/37203818744)。初回CIの旧仕様テストのFAILとfrontendのbubble-game timeoutは記録を保持し、修正・再実行後の成功と区別しています。

対象は正式タグ`1.5.0`（`4a7eb80ef60f0bf66d4fa7ad83849a9be72d68b2`）、Misskey基準は2026.10.0です。上流タグへ単純に切り替えるとMisaki独自機能と原神連携を失うため、対応するMisaki最終imageを使用してください。

適用前に、次をすべて確認します。

- 本体の必須チェックが成功し、取得間隔対応と追従版の依存関係が解決されていること。
- 本体・frontend・原神プラグインの対応版が、同じ最終imageに組み込まれていること。
- 最終imageの公開digest、linux/amd64対応、ビルド元commit、原神の画面とAPIの組込みを確認済みであること。
- 本番DBのコピーと本番とは別のRedisを使う検証環境で、migration・起動・主要画面を確認していること。検証環境から本番の連合・メール・webhook・object storageへ書き込まないこと。
- 既存の認証情報と権限で操作できること。新しい権限やAPIキーを、この移行のためだけに発行しないこと。

追従版で参照している構成は次のとおりです。

| 対象 | 固定参照 |
|---|---|
| 本体ビルド元 | `75a50768bbcc2468642f1590c64e87f1ce75f1f3`（PR18の通常マージ） |
| frontend | `50d4490066fda6600a1dd6d92f49fca925cca19c` / `2026.10.0-mk.misaki.2`。上流基準は`9eae05e71435512c39d3838e4689be6d7437707a` |
| frontendアセット | `ghcr.io/misaki-project/misskey-ts-assets:2026.10.0-mk.misaki.2@sha256:498063ce50b2923e4211b6359c8f3c24d54fe537007d8cc9f23b5deb0c9a57cc`。workflow出力とregistryの一致を確認済み |
| 原神プラグイン | `97eefa37bab74827df764bb8f3a482903c890c98` |
| 最終runtime image | `ghcr.io/misaki-project/mk-genshin@sha256:9b4818e134b129039fae803b580692689fc31470f35ab8ea8afd3be5915c0863` |

公開タグは`rolelevel-20261002-75a50768bbcc2468642f1590c64e87f1ce75f1f3-37203818744-1`です。既存タグや`latest`を更新せず公開しました。配備には上記digestを使用します。frontendアセット公開時の意図しない`latest`更新は、承認後に旧digestへ復元し、再発防止PR8をマージ済みです。過去タグの修正前workflowは再実行しないでください。

frontendアセットimageはデータ専用です。**アプリケーションの起動imageとして使用しないでください。** 原神のfrontendを含める最終ビルドでは、プラグインを取り込んだlocal frontendのビルドが必要です。旧imageへアセットだけ差し替えても、今回の移行にはなりません。

## 2. 現在の構成とバックアップを保存する

以下の例はLinuxホストで、`sudo docker`が必要な単独コンテナ構成を想定しています。既存の更新スクリプトを使う場合も、同じ確認が必要です。

```sh
APP=mk-go-production
STAMP=$(date -u +%Y%m%dT%H%M%SZ)
BACKUP_DIR="$HOME/misaki-backups/$STAMP"
umask 077
mkdir -p "$BACKUP_DIR"
sudo docker inspect "$APP" > "$BACKUP_DIR/container-inspect.json"
sudo docker inspect --format '{{.Image}}' "$APP" > "$BACKUP_DIR/old-image-id.txt"
sudo docker inspect --format '{{.Config.Image}}' "$APP" > "$BACKUP_DIR/old-image-reference.txt"
```

inspectには環境変数などの秘密情報が含まれる可能性があります。バックアップは非公開で保管し、issue・PR・リポジトリへ添付しないでください。

1. 使用中の設定ファイル、環境変数の供給元、network、UID/GID、restart policy、mountを確認し、復旧時に同じ構成を再現できるよう保存します。
2. 更新前のimageを削除せず保持します。必要なら既存の運用方法でimageも退避します。切替後のpruneは行いません。
3. メンテナンス状態にし、アプリケーションや別workerによるDBへの書込みを止めます。旧コンテナは停止しても削除しません。
4. 既存のDB管理手順・認証情報でPostgreSQLの整合したバックアップを取得します。復元可能なこと、保存先の空き容量と読取り可否を確認します。
5. 設定とローカルdrive、またはobject storageの保全も確認します。DBだけではファイル本体を復元できません。共有Redisのqueue状態も既存の運用手順で保全し、無断でflushしません。

単独コンテナを停止する操作は次のとおりです。先にメンテナンス状態と、ほかに書込みを行うworkerがないことを確認してください。

```sh
sudo docker stop "$APP"
```

## 3. 最終imageを取得してmigrationを実行する

### Release 1.4.0の追加確認

[正式リリースの注意点](https://github.com/shiroha-a/mk/releases/tag/1.4.0)と[既存のバッチ手順](deployment.md#後始末バッチ)も確認してください。既に対応済みでも、実行記録を確認してから先へ進みます。

- 旧い版から移行する場合は、更新前に`backfill-remote-host`で保存済みリモートhostの正規化を確認します。**現在稼働している版のimage・バッチを使用し、まず`-dry-run`を実行**してください。新しいバッチを未更新のDBへ先に実行すると、まだ存在しない列を参照することがあります。DBバックアップ取得後、対象件数と変更内容を確認したうえで本実行します。
- `jobQueueDriver: asynq`が残っていないか確認します。指定がある場合は旧版で未処理ジョブを捌いてから、既存の設定手順で`mkq`へ切り替えるか指定を削除します。新しいimageではasynqのジョブを処理できません。queueの無断flushは行いません。
- アイコン・バナーの公開URLは自動で修復されません。対応版の`backfill-avatar-public-url`をまず`-dry-run`で確認します。TS版から移行したDBでは表示用画像のサイズにも影響するため、[バッチの注意点](deployment.md#backfill-avatar-public-url--アイコン--バナーの-url-を公開用へ寄せ直す)を確認してから本実行します。旧imageにバイナリがない場合は、未検証の代替バッチを実行せず運用手順を確認してください。
- ソースから構築する場合はGo1.27.1と対応するfrontendのNode/pnpmを用い、`make plugins`でworkspaceを再生成します。既存の未コミット変更を上書きしない別checkoutで構築します。frontendの更新後は本体の再起動も必要です。
- nginxの参照設定を利用している場合は、アクセスログ・`/metrics`・Originの変更を確認します。IP履歴の表示権限変更や、旧いmaintenanceジョブの失敗記録の扱いもリリース本文に従って確認します。

### Release 1.5.0の追加確認

[正式リリースの注意点](https://github.com/shiroha-a/mk/releases/tag/1.5.0)を確認します。

- 複数プロセス構成では、**queue workerを先に更新**します。旧workerは`PreserveAccount`を無視し、保持すべきアカウント行まで削除する可能性があります。単独コンテナでも、別の旧workerが残っていないことを確認します。
- `db.extra.ssl: true`はDB証明書を検証するようになります。私設CAを使う場合は`sslrootcert`の設定と証明書の読取りを確認します。起動失敗を避けるためだけに、無断で証明書検証を無効化しません。
- `trustProxy`は明示した範囲だけを信頼します。前段proxyのアドレスが含まれることを確認します。`true`・数値・解釈できない値は起動エラーになるため、[設定手順](configuration.md)に従って修正します。
- 独自クライアントは、JSONキーの大文字小文字・objectのbody・POSTのbodyを確認します。POSTのURLの`?i=`は認証に使用されません。role-levelのBearer認証と専用scopeを維持します。
- 固定ノートは1.5.0準拠です。現在固定中のpublic/homeに対する作者の時間制限の例外は`notes/show`にも適用されますが、匿名のログイン要求・visitor制限・followers/DM・引用/返信の認可は迂回しません。
- 更新後に`backfill-instance-counts`を実行します。本体の再起動と重ねず、まずdry-runで対象件数を確認し、[配備手順](deployment.md#後始末バッチ)に従って実行します。更新後の`backfill-remote-host`も再実行が必要です。更新前のバッチ実行とは別の作業として記録します。
- Misakiでは上流migration108の`user.uri`インデックスを**109**へ割り当てます。既存106・107・108は変更しません。`CONCURRENTLY`によるインデックス作成は大きなDBで時間がかかるため、途中で通常起動へ進まず完了を確認します。

### imageの取得

公開済みの固定digestを設定します。`APP_CONFIG`は実際の構成に置き換え、本番を変更する前に上記の適用条件を満たしてください。

```sh
NEW_IMAGE='ghcr.io/misaki-project/mk-genshin@sha256:9b4818e134b129039fae803b580692689fc31470f35ab8ea8afd3be5915c0863'
APP_CONFIG='/絶対パス/default.yml'
sudo docker pull "$NEW_IMAGE"
sudo docker image inspect "$NEW_IMAGE"
```

既存の更新スクリプトが旧digestを固定している場合は、設定先のimageを変更してから事前確認を実行します。スクリプトをそのまま実行しても、追従版へ更新されるとは限りません。

今回の本体migrationは`000108_note_page_count_backfill`と`000109_user_uri_index`です。適用済みのMisaki migration106・107・108は変更しません。108はページから参照されるノートの`pageCount`を増やす方向に補完し、downはno-opです。109は`user.uri`インデックスを追加します。新しい原神プラグインはmigration10を含みます。プラグインのmigrationは対応版の起動時に適用されるため、本体migrationだけ実行して完了とはしません。

旧プロセス停止後、本番と同じnetwork・UID/GID・設定の供給方法で、本体migrationを一度だけ実行します。次は**host network・UID/GID `1001:1001`・設定ファイルのみmountする構成の例**です。実際のinspectと異なる場合は、その構成に合わせてください。環境変数で設定を補っている場合は、同じ供給元も指定する必要があります。

```sh
sudo docker run --rm --network host --user 1001:1001 \
  --mount "type=bind,src=$APP_CONFIG,dst=/app/.config/default.yml,readonly" \
  --entrypoint /app/migrate "$NEW_IMAGE" \
  -config /app/.config/default.yml up
```

失敗した場合は新アプリを起動しません。ログと、`schema_migrations`のversion/dirtyを確認します。履歴を直接書き換えたり、無条件にforce/downしたりしないでください。旧版からの正しい経路で適用した後の本体versionは109、dirtyはfalseです。

## 4. 旧コンテナを残して切り替える

既存の更新スクリプトが旧コンテナを退避する場合は、その仕組みを使用します。手動の場合は停止済みの旧コンテナを別名で保持し、inspectで確認した同じ実行設定を使って新コンテナを作ります。

```sh
OLD_APP="${APP}-before-${STAMP}"
sudo docker rename "$APP" "$OLD_APP"
# 確認したnetwork・user・env・mount・restart policyで新コンテナを作成する。
# docker runの既定値で代用せず、起動imageだけをNEW_IMAGEへ変更する。
```

旧・新コンテナを同じDB/Redisへ同時に接続して起動しないでください。初回起動のログで、本体とrole-level・原神の読み込み、プラグインmigrationの成功を確認します。ログに秘密情報を含めて公開しないでください。

## 5. 切替後の確認

- 起動ログ、healthcheck、ログイン、タイムライン、投稿、通知、ページ埋込みを確認する。
- frontendが2026.10.0追従版で、原神の設定・公開設定・ランキング画面が表示されることを確認する。古いキャッシュが疑われる場合は再読込みする。
- 原神のUID表示は既定オフ、本人確認は6文字・記号1文字以上・コード全体の部分文字列一致・送信後最低60秒待機、12件対応とTTL保持が変わっていないことを確認する。ランキング閲覧だけで追加取得されないことも確認する。
- 管理画面の「原神の自動取得間隔（分）」を確認する。既定10分、整数1〜1440分。同優先度では短い間隔を採用するため、長い間隔を個別適用する場合はロールの優先度を上げる。詳しくは[取得間隔の仕様](genshin-refresh-policy.md)を参照する。
- XP表示・ロール付与・管理画面を確認する。必要なら既存の専用scopeでXP変更を確認し、`note`がrole-level監査ログだけに保存されることを確認する。確認のためにユーザーのモデレーションノートを書き換えない。
- 自己削除・完全削除ポリシー、固定ノート欄のpublic/homeだけのロックダウン例外、followers/DM・引用・返信先・通常ページの認可を確認する。削除の実操作は検証用アカウントで行い、本番の実ユーザーへ試さない。

これらは本番の運用確認です。ローカルのテスト成功だけで完了扱いにせず、確認結果を記録してからメンテナンス状態を解除します。

## 6. 問題が起きた場合

1. 新コンテナを停止し、再起動ループと追加の書込みを止めます。ログ・image digest・migration状態を保全します。
2. **まず、更新後DBで旧版を起動できるか検証環境で確認します。** 108のdownは値を戻さず、原神のmigrationも適用済みです。旧コンテナを起動するだけで必ず戻れるとは限りません。
3. 互換性を確認できた場合だけ、退避した旧コンテナを同じ設定で戻します。新・旧の同時起動はしません。
4. DB復元が必要なら運用者が別途判断します。切替後に発生した投稿・XP・UID設定などはバックアップに含まれません。復元に伴うデータ損失とファイル・queueとの整合性を確認してから実施します。

**DBの自動復元、migration履歴の改変、既存タグの上書きは行いません。** 旧コンテナ、旧image、DB・設定・ファイルのバックアップは、安定稼働を確認するまで保持します。
