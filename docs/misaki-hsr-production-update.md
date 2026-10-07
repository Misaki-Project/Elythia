# Misakiのhsr本人確認・実績ランキング版への本番更新

**新hsr同梱imageの公開とmanifest照合は完了しています。更新ツールの最終CI確認・配布前には実行しないでください。本番適用・実DB復元試験は未実施です。**

正式Elythia 2.0.0を基準にしたMisaki本体に、hsrの本人確認・複数UID・ロール権限・実績ランキングを同梱します。原神・fedwatch・role-levelを保持し、上流最新developは取り込んでいません。既存の配布ZIP・image・`latest`は変更しません。

## 配布対象と証拠

- image: `ghcr.io/misaki-project/mk-genshin@sha256:7ca70e10836b5939ba587210144da4625cbd3c6b2882439dbdf54ebf1ac5f0df`。
- 一意tag: `elythia-2-0-hsr-20261007-7a15c2e7a383c748513895a847dd00a9579d1e02-37622351122-1`。
- image本体commit: `7a15c2e7a383c748513895a847dd00a9579d1e02`。公開CI: [37622351122](https://github.com/Misaki-Project/Elythia/actions/runs/37622351122)成功。hsr固定refの実解決と一意tagだけのpushをログで確認。
- GHCRからtagとdigestのmanifestを取得し、応答digest・本文SHA-256・`linux/amd64`を照合済み。amd64 manifestは`sha256:f09bbde8774115e328173d9d35def81b184401e1a28d4b9340a16fde1532826f`。`latest`は作成・変更していません（照合時の取得は404）。
- hsr pin: `fd90f729a5ea85f58cd76d094194468e4fadf149`（Misaki-Project/elythia-plugin-hsr）。
- 原神pin: `253a472ab09e87a471c3746dca4a6a0279022a0a`。
- fedwatch pin: `af105a020656aaba121998a0bd5a51bee232dec8`。
- role-level: 本体同梱。XPのnoteはrole-level監査ログだけへ保存する既存仕様を維持。
- hsr PR #1・本体PR #22は通常マージ済み。最終統合head `dfd5256a90851a8642f3e14a2b498112241874d3`の本体・frontend・4plugin CIが成功。
- 公開pin PR #23の本体CI `37621197564`・4plugin統合 `37621197386`・新pin image build `37621198292`・安全CI `37621197091`成功。frontend単独lint/testは変更検出によりskippedであり、成功扱いしません。frontend実検証は4plugin統合CIで実行しています。
- 更新ツール初回安全CI `37621578658`は24件中3失敗。テストの更新元version変数をスクリプト本文で上書きしていたため、偽containerが117に対応しない状態でした。変数名を分離し、検査・assertionを緩めず修正しました。最終headのCI証拠は配布時に追記します。

## 対応する更新元

スクリプトは次の2つを明示的に区別します。推測でDBのversionを変更しません。

| 本体台帳 | 既存起動コマンド | 更新経路 |
|---|---|---|
| `109/dirty=false` | `/app/misskey -config .config/default.yml` | 稼働中backup→公式オンライン本体migration117→停止→最終backup→新版起動 |
| `117/dirty=false` | `/app/elythia serve -config /app/.config/default.yml` | 稼働中backup→本体117維持→停止→最終backup→新版起動 |

両方とも、hsrの独立migrationは新版起動時に適用します。異なるversion・dirty=true・コマンドの不一致はimage取得や停止前に拒否します。

対象構成は`mk-go-production`、host network、UID/GID `1001:1001`、読み取り専用設定mount1件、設定`/home/misskey/cherrypick/.config/default.yml`、DB `localhost:5432/mk1`・user `misskey`です。隔離アプリ・Docker通信制限・ホストDocker socketの扱いは変更しません。

## 配置と事前確認

旧配布を上書きしないよう、新版の2ファイルを専用ディレクトリ`/home/misaki/shell/hsr-update`へ配置してください。実パスワードを含む既存スクリプトは非公開のまま保持し、公開やログ添付をしないでください。

```sh
sudo chown root:root /home/misaki/shell/hsr-update/update-misaki.sh /home/misaki/shell/hsr-update/update-misaki-helper.py
sudo chmod 600 /home/misaki/shell/hsr-update/update-misaki.sh /home/misaki/shell/hsr-update/update-misaki-helper.py
sudo bash /home/misaki/shell/hsr-update/update-misaki.sh --check
```

必要: Bash、Docker、Python 3とpython3-yaml、サーバーと互換なPostgreSQL client、tar、flock、sha256sum。DBパスワード欄は空欄なら既存container環境／設定から自動取得します。サーバー上の非公開コピーへ記入も可能です。0600の一時pgpassを使い、対話入力・argv・画面・Gitへパスワードを出しません。

`--check`は構成・DB接続・本体台帳・backup容量だけを確認します。pull・停止・migrationはしません。backupディレクトリとlockは作成します。

## 一度の承認後に自動で切り替える

他のwriter・自動更新を止め、object storageの復元可能なsnapshot/version保全を確認してから実行します。

```sh
sudo bash /home/misaki/shell/hsr-update/update-misaki.sh
```

`UPDATE mk-go-production`を一度入力すると、追加の入力なしでbackup・必要な本体migration・停止・最終backup・新版起動・検査まで進みます。ホストOSは再起動しません。進捗は表示された`backup/update.log`へ保存します。

image取得・稼働中backupを停止前に行い、元が109の場合は公式のオンラインindex migrationも先行します。旧containerのexportとdoctorは新版正常性確認後へ回し、停止時間から外します。停止後の最終DB snapshotは書き込みを取りこぼさないため省略しません。

正常性確認はhealthcheck、再起動回数0、4plugin loaded、hsr `version0.2.0/migrations4`、本体`117/dirty=false`、`plugin_hsr.schema_migrations`の正確な`1,2,3,4`を要求します。さらにdoctorを実行します。

## hsrの既存登録と切り戻し

hsrの旧登録はmigration4で`accounts_unverified`へ保存します。削除しませんが、再認証まで公開・ランキング・自動取得から除外し、UIDも占有しません。利用者は設定→プロフィールで6文字・記号入りコードを発行し、ゲーム内のステータスメッセージへコード全体を追加して保存・ログアウト後に認証します。最低60秒の再確認待機とEnka TTLを守ります。

本体117とplugin migration4は別の台帳です。旧hsrでは`accounts`が単一登録のschemaであり、新版から旧imageをそのまま再起動する切り戻しをしてはいけません。

- `database-before/`: 更新元の本体・plugin schemaの稼働中snapshot。snapshot後の書き込みは含みません。
- `database-final/`: 旧container停止後、新hsr migration4適用前のsnapshot。本体は117です。更新元が1.5.0なら旧1.5.0へそのまま戻せません。元が2.0.0でも、対象image・plugin schema・復元世代を確認してから運用者が判断します。
- 無条件down/force、旧imageの自動再起動、自動DB復元は行いません。失敗時は段階・ログ・新旧container状態・2つのDB台帳を確認してください。
- 新版正常性確認後のexport/doctor失敗では正常な新版を止めず、診断ログを残します。

## 実ブラウザでの確認

ハードリロード後、ログイン・投稿・画像・XP/role-level・原神・fedwatchを確認します。hsrでは旧UIDの未確認案内、コード認証、60秒待機、複数UID、UID公開/表示既定オフ、参加無効のランキング除外、`/plugin/hsr/rankings`、ロール上限と取得間隔を確認してください。

CI・manifest照合・dump目録確認は本番動作や実DB復元試験の代わりではありません。本番適用はこの準備作業には含まれていません。
