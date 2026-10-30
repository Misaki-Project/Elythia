# 純正Misskeyとの差分: Misaki固有の機能と履歴

[差分カタログの目次](../divergence.md)にある上流Elythiaの差分に加え、Misakiで維持する仕様を記録する。上流の領域別文書と重なる仕様はそちらを正本とし、ここではMisaki固有の接続点と旧forkの履歴を示す。

## 本人退会と物理削除

`canDeleteAccount`は本人退会を、`canPurgeAccount`は退会後の物理削除を制御する。いずれも明示的なbooleanの`true`だけを許可として扱い、本人退会では管理者のbypassを設けない。policyの解決失敗は副作用を起こさず500にする。frontendは`canDeleteAccount === true`で操作を表示し、最終的な認可はbackendが行う。

現行契約は[APIの差分](api.md)と[削除ポリシーの設計](../design/account-deletion-policies.md)、[canDeleteAccountの設計](../design/can-delete-account.md)を参照する。プロフィールの固定ノートは[ピン留めノートと作者ロックの公開境界](api.md#ピン留めノートと作者ロックの公開境界-3310)を維持する。`public` / `home`の時間制限の例外だけで、ログイン要求や通常の閲覧権限を迂回せず、公開範囲や利用者の設定も書き換えない。

## role-level

`plugins/rolelevel`に同梱する`role-level`はmanual roleへlevel・XP・level別policyを追加する。coreの`role` / `role_assignment` schemaは変更せず、独自データは`plugin_role_level`へ保存する。Misakiでは既定有効で、`plugins.role-level.enabled: false`で実行時に無効化できる。無効化してもrole・assignmentやpluginの保存済みデータは削除しない。

`/api/plugin/role-level/*`配下の13 routeは、level設定・XP操作・assignment参照・監査・孤立データの検査と整理・プロフィール表示設定を提供する。本体の静的routeとは別にプラグインが登録するため、差分カタログの静的endpointの合計へ重ねて加えない。管理操作と参照は各routeの認可契約に従い、定期jobは設定したplugin actorを使う。公開プロフィールは対象本人のself-service assignment APIで割当と公開属性を確認する。`roles/assignment-show` / `admin/roles/assignment-show`の`assignmentId`は保存済みXPとの照合に使う。

APIキーで呼べるのは`POST /api/plugin/role-level/admin/change-exp`だけで、`write:admin:role-level-experience`に加え、発行元アカウントの管理権限と対象roleの認可も必要になる。Bearer認証を使い、strict JSON bodyに`i`を混ぜない。XP操作の`note`はrole-levelの監査へ残し、同じ`idempotencyKey`の再送でXPと監査を二重に追加しない。詳細は[role-level README](../../plugins/rolelevel/README.md)と[プラグインの運用](../plugins/operating.md)にある。

## ゲームランキング

`/game-rankings`は、インストール済みの原神・スターレイルを共通ページのタブへまとめる。旧`/plugin/genshin/rankings` / `/plugin/hsr/rankings`と旧ナビゲーションのaliasは維持する。UIDの本人確認・公開設定・ランキング参加・利用者の可視性を尊重し、非公開UIDを追加公開しない。

`genshinUidLimit` / `hsrUidLimit`と`genshinRefreshIntervalMinutes` / `hsrRefreshIntervalMinutes`はnative policyとrole-levelの範囲設定に載せる。UID上限を下げても既存の連携を自動削除せず、再取得間隔よりEnka TTLが長ければTTLを優先する。詳細は[ゲームランキングとゲーム別ポリシー](../game-ranking-ui.md)、[スターレイルの本人確認・権限・実績ランキング](../hsr-security-ranking.md)にある。

## 外部プラグインの固定コミット

`.github/workflows/misaki-genshin-image.yml`は、本体を実行対象の不変commitから、外部プラグインを次のSHAからビルドする。プラグインのソースを本体へ直接取り込まず、`role-level`だけは本体に同梱する。この表はworkflowの`plugins`入力の記録であり、上流追従だけを理由にpinを動かさない。

| プラグイン | リポジトリ | 固定SHA |
|---|---|---|
| genshin | `Misaki-Project/mk-plugin-genshin` | `9a50079ec9d198ab44e4b9ff24d0a408109c09da` |
| fedwatch | `Elythia-Network/elythia-plugin-fedwatch` | `af105a020656aaba121998a0bd5a51bee232dec8` |
| hsr | `Misaki-Project/elythia-plugin-hsr` | `820068d720654650d5dc05d4136ad9f32f677fef` |

imageのタグには本体commit・実行ID・再実行回数を含め、過去のタグを上書きしない。導入時は生成されたdigestを確認する。ビルドworkflowはimageを準備するだけで、本番へ適用しない。運用手順は[Misaki版の原神入り更新イメージ](../plugins/operating.md#misaki版の原神入り更新イメージ)を参照する。

## 旧frontend forkの履歴

Misakiの最後のsubmodule pinは`2026.10.0-mk.misaki.2` (`50d4490066`)。2.0.0で上流frontendを本体へ取り込み、その上へMisaki独自差分を移植した。`777a0901c3` / `414b78f9ad` / `9e29160624` / `950bcacd06` / `0d8e656dfb`も履歴上のpinとして残す。以後の変更は本体の`frontend/`へ行う。

以下のMisaki系列は、[上流Elythiaの凍結したtag表](frontend.md#4-2-fork-frontend-の独自変更)と分けて扱う。上流とMisakiで同じ数字tagを別の変更へ付けた時期があるため、過去のtagを付け替えたり削除したりしない。上流のサマリの143 tagに、下の履歴を加算しない。

| Misakiの版 | 当時の変更 |
|---|---|
| Misaki `2026.9.1-mk.misaki.1` | 削除ポリシー、role-levelの管理・表示・XP操作・段階別policyを保持。Bearer認証の専用adapter、mountを妨げないプロフィール取得、native APIによる非表示role除外を採用。Folderの件数は未mountの変更なしの範囲も含める。 |
| Misaki `2026.9.1-mk.misaki.2` | 上流frontend `31e7eb73`を旧fork `950bcacd06`へ統合し、削除ポリシー・role-levelを維持。登録受付設定・連合管理・バブルゲームへ追従。 |
| Misaki `2026.9.1-mk.misaki.3` | 標準UI公開API・公開ユーザー取得・「見つける」の明示参加を追加。原神のソースとテストは別リポジトリで管理。当時のbubble-game timeoutには、そのマージだけの例外承認があった。 |
| Misaki `2026.9.1-mk.misaki.4` | XP表示を獲得済みXP / 必要XP全体へ修正し、XP変更専用のAPIキー権限を追加。APIの残りXPと保存済みXPは変更しない。当時の37テスト・型・lint・ビルド・API reportは成功し、bubble-gameの4件timeout (757/761成功)はそのマージだけの承認でFAILとして記録。 |
| Misaki `2026.10.0-mk.misaki.1` | 削除ポリシー・role-level・XP・原神の公開設定と取得間隔UIを維持して上流2026.10.0を統合。当時の型・lint・67件の回帰・原神18件・production build・SPDXは成功。上流と一致する4言語のlocale safety FAILは限定承認を記録して保持。 |
| Misaki `2026.10.0-mk.misaki.2` | Release 1.5.0の上流frontend `9eae05e7`を統合し、独自UIとbooleanによる削除policy判定を維持。当時の791件・原神18件・型・lint・production・SPDX・shippingは成功。GitHubのbubble-game3件timeoutはFAILとして記録し、失敗jobの再実行で成功を確認。新しい例外承認は使用していない。 |

過去の検査結果と例外承認は当時の履歴であり、今回の同期の検証結果や例外承認を示すものではない。

| Misakiの旧数字tag | 当時の変更 |
|---|---|
| Misaki `2026.9.1-mk.2` | `canDeleteAccount`の設定・認可表示と、signupの検証完了を待つE2E修正。 |
| Misaki `2026.9.1-mk.3` | `canPurgeAccount`とrole-levelの管理・表示・経験値曲線・段階別policy・探索・旧manualLevel移行UI。 |
| Misaki `2026.9.1-mk.4` | strict JSON bodyへ認証情報を混ぜず、専用adapterでBearer認証する。 |
| Misaki `2026.9.1-mk.5` | プロフィール取得をtop-level awaitからwatchへ移し、mountとユーザー切替に対応。 |
| Misaki `2026.9.1-mk.6` | 段階別policyをnative policy配下へ整理し、XP操作・進捗tooltip・表示切替・監査snapshotを改善。 |
| Misaki `2026.9.1-mk.7` | 非表示roleをクライアントで除外せず、native APIで除外済みのroles / badgeRolesを描画。 |
| Misaki `2026.9.1-mk.8` | XP操作に正数入力による減算を追加し、APIへ負のaddとして送る。 |
| Misaki `2026.9.1-mk.9` | XP操作を固定値・符号付き加減算・乗算へ整理し、管理画面にも進捗tooltipを適用。 |
| Misaki `2026.9.1-mk.10` | policy範囲を実際のlevelで表示し、通知除外・型別入力・境界同期・icon-only表示切替を追加。 |

旧数字tagの詳細は[同期前の文書](https://github.com/Misaki-Project/mk/blob/fbf8f293/docs/divergence.md)に残る。
