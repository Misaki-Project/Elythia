# ゲームランキング統合版への本番更新

**新imageの公開とmanifest照合は完了しています。実行前に更新ツールPRの最終headの安全CI成功と配布ZIPのチェックサムを確認してください。本番適用と実DB復元試験は未実施です。**

この版にはゲームランキング統合、ゲーム別タブ、スターレイルのFolder廃止、プロフィール右側とランキングのEnka出典表記、原神のUID連携上限ポリシーが含まれます。正式Elythia 2.0.0・Misaki独自機能・role-level・原神本人確認・fedwatch・hsr本人確認を保持します。

## 配布対象

- 確定image: `ghcr.io/misaki-project/mk-genshin@sha256:de6a50f49e7298c5b418e47937007d492f6fbb5cf2a2ff2ab07deebcfcb63292`。
- 一意tag: `elythia-2-0-games-20261008-fa95351c6a634d32b7aff612dc31bd174ead8e29-37713531378-1`。
- image本体commit: `fa95351c6a634d32b7aff612dc31bd174ead8e29`（公開pin PR #26通常merge）。公開CI [37713531378](https://github.com/Misaki-Project/Elythia/actions/runs/37713531378)成功、両ゲームの固定ref実解決をログで確認。
- GHCRのtag/digest応答と本文SHA-256が一致し、linux/amd64 manifest `sha256:f259f69cd88a4c63b2e58f0364e1a244be2f3da32739676e69a632097e614711`を確認済み。latestは作成・変更せず、取得時404でした。
- 公開前head `eb42d149f17c0a2c0a5b0c974508ee00c5d73620`の本体CI `37712914513`・4plugin統合 `37712914478`・安全CI `37712914454`・新pin image build `37712914899`成功。frontend単独lint/testは変更検出によるskippedで成功扱いしません。実frontend検証は4plugin統合と実装PR #25で実行しています。
- 原神pin: `9a50079ec9d198ab44e4b9ff24d0a408109c09da`（PR #4通常マージ済み）。
- hsr pin: `820068d720654650d5dc05d4136ad9f32f677fef`（PR #2通常マージ済み）。
- fedwatch pin: `af105a020656aaba121998a0bd5a51bee232dec8`。role-levelは本体同梱です。
- 本体UI/ポリシーPR #25 merge: `336ce87e1cf03158c0f5e25784b518bdc59cb2ba`。
- 実装head `458af391b8650ef2ed11734d62cda81003143dd1`の本体CI `37638258378`・frontend lint/test `37638258344`・4plugin統合 `37638258379`・安全CI `37638258351`成功。4pluginのGo回帰skipなし、plugin UI22件、ゲーム/ポリシー回帰5件成功。最終配布headの証拠は配布READMEと更新ツールPR本文に記録します。

既存配布ZIP・旧image・既存tag・`latest`は変更しません。旧hsr対応ZIPをこの変更対応済みとして使わないでください。新ZIPの2ファイルをセットで配置します。

## 配置と事前確認

専用ディレクトリ`/home/misaki/shell/game-ranking-update`へ`update-misaki.sh`と`update-misaki-helper.py`を配置します。旧ツールは上書きしません。

```sh
sudo chown root:root /home/misaki/shell/game-ranking-update/update-misaki.sh /home/misaki/shell/game-ranking-update/update-misaki-helper.py
sudo chmod 600 /home/misaki/shell/game-ranking-update/update-misaki.sh /home/misaki/shell/game-ranking-update/update-misaki-helper.py
sudo bash /home/misaki/shell/game-ranking-update/update-misaki.sh --check
```

対象は`mk-go-production`、host network、UID/GID `1001:1001`、readonly設定mount1件、`/home/misskey/cherrypick/.config/default.yml`、DB `localhost:5432/mk1`・user `misskey`です。独自の隔離アプリ・Docker通信制限は変更せず、ホストDocker socketも渡しません。

必要: Bash、Docker、Python3、python3-yaml、互換なPostgreSQL client（psql・pg_dump・pg_restore）、tar、flock、sha256sum。

`DB_PASSWORD=''`なら既存container環境／設定YAMLから自動取得します。サーバー上の非公開コピー先頭の欄へ記入もできます。一時0600 pgpassを使い、対話入力は不要です。秘密情報入りファイル・backup内のenv/config/inspectを共有・Gitへ保存しないでください。

`--check`はpull・停止・migrationを行いません。backupディレクトリとlockは作成します。本体109/dirty=false＋旧misskeyコマンド、または117/dirty=false＋Elythia serveコマンドだけを許可します。今回すでに確認した117/dirty=falseの構成は本体migrationを再実行せず、117を維持します。ただし実行前に新版ツールで事前確認してください。

## 本番切り替え

他writer・自動更新の停止と、object storageの復元可能なsnapshot/version保全を確認してから実行します。

```sh
sudo bash /home/misaki/shell/game-ranking-update/update-misaki.sh
```

`UPDATE mk-go-production`を一度入力すると、image取得・稼働中backup・必要な本体migration・停止後最終backup・新版起動・検査まで追加の対話なしで進みます。OSは再起動しません。進捗は表示された`backup/update.log`へ保存します。

本体117/dirty=false、4plugin loaded、hsr0.2.0/migrations4と独立台帳1,2,3,4、healthcheck、再起動回数0を検査します。旧containerのexportとdoctorは正常性確認後に実行し、停止時間から外します。最終DB snapshotは省略しません。

## 切り戻しと旧hsr登録

旧hsrのmigration1〜3から更新する場合、登録はmigration4で未確認として保存されます。削除しませんが、再認証まで公開・ランキング・自動取得から除外します。すでにhsr migration4の版なら既存の確認済み連携を再度隔離しません。

`database-before/`は稼働中snapshotで後続書き込みを含みません。`database-final/`は旧container停止後、新版起動前のsnapshotで本体117です。本体117とplugin台帳は別なので、旧1.5.0や旧hsr imageへそのまま戻してはいけません。

自動DB復元・旧image自動再起動・無条件down/forceは行いません。失敗時は段階・新旧container状態・本体とpluginの台帳・保護されたログを確認し、復元対象imageとsnapshotの組合せを運用者が判断します。正常性確認後のexport/doctor失敗では正常な新版を止めません。

## 更新後に実ブラウザで確認する項目

- ハードリロードし、ログイン・投稿・画像・XP/role-level・原神・fedwatch・hsrを確認します。
- 「もっと！」／「見つける」の「ゲームランキング」で原神とスターレイルのタブ切り替えを確認します。
- スターレイルランキングにFolderがないこと、両ゲームのランキングにEnka出典があることを確認します。
- プロフィールを展開し、UIDが左・小さなEnka出典が右にあること、UID非公開ならUIDを表示しないことを確認します。
- ロールポリシー／role-levelの`genshinUidLimit`（0〜100、既定1、同優先度max）を確認します。0は新規連携禁止で、既存連携は自動解除しません。
- 再取得間隔は`genshinRefreshIntervalMinutes`／`hsrRefreshIntervalMinutes`（1〜1440分、既定10、同優先度min）で既存実装です。Enka TTLが長い場合はTTLまで待ちます。本人確認の待機を短縮する設定ではありません。

詳細な安全条件とsnapshotの扱いは[hsr更新手順](misaki-hsr-production-update.md)、正式2.0.0の仕様は[公式更新手順](upgrade/2.0.0.md)も参照してください。旧手順のimage digestや配布先を今回の代わりに使わないでください。CI成功は本番動作・実DB復元成功の証拠ではありません。
