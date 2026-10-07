# Misaki 1.5.0からElythia 2.0.0への更新

**統合CIと4プラグイン同梱imageの公開は成功しています。本番構成・実ブラウザでの適用結果は未確認です。添付された旧更新スクリプトに対応する2.0.0用の更新ツールを追加しました。旧版をdigestだけ差し替えて実行しないでください。本番適用はこの作業に含みません。**

先に[公式の更新手順](upgrade/2.0.0.md)を読み、実際の起動構成に対応する節を確認します。この文書はMisakiの差分です。公式のimageやソースへ単純に切り替えるとMisakiのrole-level・XP・原神の公開設定や取得間隔を失います。

## 固定する対象

- 本体の基準は正式タグ`2.0.0`、commit`3a01a6d62948725e1bc888012be9aeec6f690b86`。
- role-levelは本体同梱を維持し、SDK importとマニフェストを2.0.0へ対応させます。XPの`note`は引き続きrole-levelの監査ログにだけ保存します。
- 原神はMisaki版を継続し、上流の2.0.0対応と修正を統合します。fedwatchとhsrを追加し、すべて不変commitで取得します。
- frontendは本体の`frontend/`へ移り、Misakiの管理・XP・削除ポリシーUIもそこからビルドします。1.5.0の外部assets imageは2.0.0用として使いません。
- 確定imageは以下のdigestで固定します。`latest`や上流のbundled imageを代用しません。

## 確定した配布imageと検証証拠

```text
ghcr.io/misaki-project/mk-genshin@sha256:6d31d2e3b8453efff48363215e19b307d1ea4f44035915601adb8ae545a51ca0
```

- 本体commit: `83850c3d2b9eeaaeaf158278ea4449f360cdeb54`（本体PR #20の通常merge）。
- 一意tag: `elythia-2-0-20261007-83850c3d2b9eeaaeaf158278ea4449f360cdeb54-37610286696-1`。`latest`は更新していません。
- 原神pin: `253a472ab09e87a471c3746dca4a6a0279022a0a`。
- fedwatch pin: `af105a020656aaba121998a0bd5a51bee232dec8`。
- hsr pin: `07c5c1bb43cc34de5e79823238567a17623323be`。
- role-levelは本体同梱。公開ビルドログで4プラグインの配線生成を確認済みです。
- [公開CI](https://github.com/Misaki-Project/Elythia/actions/runs/37610286696)の成功・digestを確認し、GHCRのmanifest digestおよび取得したmanifestのSHA-256と一致することを照合しました。実行imageのplatformは`linux/amd64`です。
- [統合回帰CI](https://github.com/Misaki-Project/Elythia/actions/runs/37592508011)では4プラグインのDB付きrace/count1/shuffle3・vet・build、プラグインfrontend全18件、組込みfrontendのproduction build・型・lint・API reportが成功しました。[本体CI](https://github.com/Misaki-Project/Elythia/actions/runs/37592507958)、[frontend CI](https://github.com/Misaki-Project/Elythia/actions/runs/37592508066)、[API互換CI](https://github.com/Misaki-Project/Elythia/actions/runs/37592507952)も成功しています。統合検証headは`14abde66fae0002ab1877199038c36491cfe684a`です。

これは配布・CI検証の証拠であり、本番での起動・プラグインの有効化・実ブラウザでの確認が完了したことを意味しません。以前のheadで発生したCI失敗はPR #20に記録を残しています。

## 更新前の確認

1. DB、設定、filesと現行imageのdigest、コンテナのinspectをバックアップします。ほかのwriter・自動更新処理を確認します。
2. host network・UID/GID `1001:1001`など、既存構成を推測で変更しません。設定mount・環境変数・restart policyを実構成から確認します。
3. 以下の2.0.0用更新ツールを使う場合も、先に`--check`で実構成・DB台帳を確認します。旧`update-misaki.sh`は旧バイナリを呼ぶため、そのまま実行しません。
4. frontendを外部からmountしている場合は、公式手順に従って配信中のファイルを退避します。新frontendのビルド完了前にmount先を切り替えません。image同梱frontendを旧mountで隠さないよう確認します。

## 単独Dockerコンテナの自動更新ツール

`scripts/misaki/update-misaki.sh`と`update-misaki-helper.py`を同じサーバーディレクトリ（例: `/home/misaki/shell`）へ配置します。添付された旧スクリプトの専用構成（`mk-go-production`、host network、UID/GID `1001:1001`、読み取り専用設定mount1件、DB `localhost:5432/mk1`、user `misskey`）だけに対応します。別構成を推測で置き換えません。

- 必須: Bash、Docker、Python 3と`python3-yaml`、PostgreSQLサーバーと互換な`psql`/`pg_dump`/`pg_restore`、tar、flock、sha256sum。
- DBパスワードはスクリプト先頭の`DB_PASSWORD`欄へサーバー上で記入できます。空欄の場合は既存コンテナ環境または設定YAMLから読み取ります。パスワード入力はありません。0600の一時pgpass経由で使い、argv・画面・Gitへ出しません。実パスワード入りのコピーはroot所有・0600とし、公開しないでください。
- まず`sudo bash /home/misaki/shell/update-misaki.sh --check`。構成、DB接続、version109・dirty=falseを確認し、image取得・停止・migrationは行いません。バックアップ用の保護ディレクトリとlockは作成します。
- 通常実行は`sudo bash /home/misaki/shell/update-misaki.sh`。他のwriter・自動更新が動かないことと、object storageの復元可能なsnapshot/version保全を確認してから、`UPDATE mk-go-production`を一度入力します。**以後は追加の対話入力なしでバックアップ→migration→新コンテナ起動→healthcheck・4プラグイン・doctor確認まで実行します。ホストOSは再起動しません。**
- 承認後の進捗は表示された`backup/update.log`に保存します。SSH切断のSIGHUPを無視しますが、ホスト再起動・SIGKILL・電源断からの継続は保証しません。途中失敗時に自動で旧imageやDBへ戻しません。

### 停止時間を短くする順序とバックアップの違い

1. 稼働中に確定imageを取得し、新環境ファイルと設定バックアップを準備します。
2. 稼働中に`database-before/`へmigration前の整合したDB snapshotを保存します。このsnapshot以降の書き込みは含まれません。
3. 旧コンテナの自動再起動を無効化し、公式手順に従って新imageで本体migrationをオンライン適用します。大きなindexの`CONCURRENTLY`作成を停止時間から外します。失敗した場合は旧コンテナも停止して、未知のDB versionへ再起動させません。
4. 旧コンテナを停止し、`database-final/`へ切替直前の書き込みを含む整合した最終snapshotを保存します。**この時点の本体schemaは117です。** 保存時間は必要なダウンタイムで、書き込みを取りこぼして短縮することはしません。directory形式の並列dump（既定2 jobs）を使います。DB規模・IO負荷に合わせて調整してください。
5. 新コンテナを`/app/elythia serve`で起動し、healthcheck・再起動回数0・4プラグインの`plugin loaded`・本体117/dirty=falseを確認します。プラグイン独立schemaのmigrationも起動時に実行されます。
6. 正常性確認後、旧コンテナの書込層exportとdoctorを行います。これらを停止時間から外します。正常性確認後の保全/doctor失敗では新サーバーを停止せず、エラーと診断ログを残します。旧コンテナは停止・自動再起動無効のまま保管します。

バックアップ容量はDB snapshot2世代・旧コンテナfilesystem・設定・ログが収まるよう事前に確保します。dumpの目録確認は実際の復元試験ではありません。切り戻し時は復元する世代を明示して判断してください。`database-before/`はversion109ですがsnapshot以降の書込を失います。`database-final/`は停止時点のデータを保持しますがversion117なので、復元後に旧1.5.0をそのまま起動できません。本体117→109を明示的に戻す必要があり、プラグインschemaも別途判断が必要です。無条件down/forceや自動復元は行いません。

## バイナリの呼出し

| 用途 | 2.0.0の呼出し |
|---|---|
| サーバー起動 | `/app/elythia serve -config /app/.config/default.yml` |
| 本体migration | `/app/elythia migrate -config /app/.config/default.yml -direction up` |
| healthcheck | `/app/elythia healthcheck -config /app/.config/default.yml` |
| 起動後の確認 | `/app/elythia doctor -config /app/.config/default.yml` |
| 後始末バッチ | `/app/elythia backfill <名前> ...` |

設定パスは例です。実構成に合わせます。`elythia`にサブコマンドを付けないと使い方を表示して終了します。2.xの互換symlinkへ新しい更新手順を依存させません。

## migrationと切り戻し

Misakiの適用済みmigration106・107・108・109は変更しません。上流2.0.0の新規109〜116をMisakiでは**110〜117**へ割り当てます。110〜116は画像URL確認用indexの`CONCURRENTLY`作成、117は以前の既定repository/feedback URLだけの更新です。運営者が設定したURLは変更しません。本体適用完了はversion117・dirty=falseです。

大きなDBではindex作成に時間がかかります。新imageでmigrationが完了するまで新サーバーを起動しません。プラグインにも独立schemaとmigrationがあるため、本体のversionだけで更新完了と判定しません。

公式手順では1.5.0を動かしたまま本体migrationを先行適用できますが、**適用後に旧1.5.0のmigrationを起動し直すと未知のversionで失敗します。** 現行の自動更新・再起動・migration実行の構成を確認せず先行適用しません。

切り戻しは運用者の明示判断で行います。上流の「8段戻す」はMisakiでは本体117→109に対応しますが、fedwatch・hsrなどのプラグインschemaを戻す操作ではありません。まずサービスと自動migrationを止め、DBの現在versionとdirty、新プラグインのデータ、バックアップを確認します。DB履歴のforce変更や無条件downは行いません。**downで`-steps`を省略すると全段を戻すため、絶対に省略しません。** 判断できない場合は更新前のバックアップと構成へ戻す手順を検討します。復元ではバックアップ以降の書き込みが失われます。

## 更新後の確認

- `doctor`と起動ログを確認し、role-level・原神・fedwatch・hsrの`plugin loaded`を確認します。
- nodeinfoの名前は`elythia`、プラグイン宣言は`elythiaPlugins`です。1.5.0以前の相手とのプラグインPeer連携は相手が更新するまで停止します。
- ブラウザをハードリロードし、トップ・`/about-elythia`、role-level/XP管理、原神の設定・ランキング、fedwatch管理画面、hsrプロフィールを確認します。
- 原神の本人確認待機・公開設定・UID表示既定・取得間隔/TTLと、自己削除のtyped boolean認可を確認します。
- CIの成功・image公開成功と、本番構成や実ブラウザでの動作確認を区別します。未確認項目をPASSとして記録しません。
