# Misaki 1.5.0からElythia 2.0.0への更新

**統合・CI検証中です。最終Misaki imageはまだ確定していません。旧更新スクリプトをそのまま実行しないでください。本番適用はこの作業に含みません。**

先に[公式の更新手順](upgrade/2.0.0.md)を読み、実際の起動構成に対応する節を確認します。この文書はMisakiの差分です。公式のimageやソースへ単純に切り替えるとMisakiのrole-level・XP・原神の公開設定や取得間隔を失います。

## 固定する対象

- 本体の基準は正式タグ`2.0.0`、commit`3a01a6d62948725e1bc888012be9aeec6f690b86`。
- role-levelは本体同梱を維持し、SDK importとマニフェストを2.0.0へ対応させます。XPの`note`は引き続きrole-levelの監査ログにだけ保存します。
- 原神はMisaki版を継続し、上流の2.0.0対応と修正を統合します。fedwatchとhsrを追加し、すべて不変commitで取得します。
- frontendは本体の`frontend/`へ移り、Misakiの管理・XP・削除ポリシーUIもそこからビルドします。1.5.0の外部assets imageは2.0.0用として使いません。
- 最終imageの公開digest・組込み検証結果は、CI成功と公開後に確定します。`latest`や上流のbundled imageを代用しません。

## 更新前の確認

1. DB、設定、filesと現行imageのdigest、コンテナのinspectをバックアップします。ほかのwriter・自動更新処理を確認します。
2. host network・UID/GID `1001:1001`など、既存構成を推測で変更しません。設定mount・環境変数・restart policyを実構成から確認します。
3. `update-misaki.sh`が旧imageや旧バイナリを固定していないか確認します。imageのdigestだけを差し替えるのではなく、起動・migration・healthcheckの呼出しも直します。スクリプト自体を確認できていない段階では互換性を断定しません。
4. frontendを外部からmountしている場合は、公式手順に従って配信中のファイルを退避します。新frontendのビルド完了前にmount先を切り替えません。image同梱frontendを旧mountで隠さないよう確認します。

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
