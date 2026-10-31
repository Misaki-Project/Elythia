# Misaki版の移行履歴を本体とlocalへ分ける

この手順は、旧`Misaki-develop`の`29715b8954ea844ff2799d50a8c3903406ca7c6a`から、上流`2a026d427f9e35558b6a03eac06348d7882834dc`を取り込む際の一度だけの台帳移行です。**新しい版の自動migrateを、既存DBにいきなり流さないでください。** この変更では本番DBへの適用を行いません。運用時はバックアップを複製した使い捨てDBで確認した後に適用を判断します。

## 何が変わるか

旧forkは独自のデコレーション削除処理を`000098`へ追加し、上流の登録停止処理を`000106`、その後の上流の処理を1番ずつ後ろへ置いていました。このまま上流を取り込むと`000117`が二重になり、上流の正規番号の処理も欠けます。ファイル名だけを直しても、適用済みDBの`schema_migrations`は旧番号のままなので、上流の新しい`000117`が飛ばされます。

今回、**本体のファイルは上流の内容・正規番号へ戻し**、独自処理だけを`migration/local/`へ移します。過去のSQLに新しい処理を継ぎ足すことはしません。独自のup/downのSQL本文もそのまま移します。

| 旧Misaki番号・内容 | 今回の系列・番号 |
|---|---|
| `000001`〜`000097` | coreの同じ番号 |
| `000098_drop_remote_avatar_decorations` | localの`000001_misaki_drop_remote_avatar_decorations` |
| `000099`〜`000105` | coreの同じ番号 |
| `000106_registration_closed` | coreの`000098_registration_closed` |
| `000107_abuse_report_notification_recipient_fk_cascade` | coreの`000106` |
| `000108_note_page_count_backfill` | coreの`000107` |
| `000109_user_uri_index` | coreの`000108` |
| `000110`〜`000116`のURL用index | coreの`000109`〜`000115` |
| `000117_transferred_repository_url` | coreの`000116_transferred_repository_url` |
| 上流の新しい`000117`〜`000138` | coreの`000117`〜`000138` |

本体は138本、localは1本です。新しい空DBは通常の`elythia migrate -direction up`でcore→localの順に適用します。空DBの台帳を手で変更する必要はありません。localは新しいオブジェクトを作らず、CherryPick由来の`avatar_decoration.host IS NOT NULL`の行だけを削除します。元の値を保存しないため復元できず、downは`SELECT 1`です。

## 旧117まで適用済みのDBだけを移す

通常のmigration、バックアップ復元、管理画面のrollbackは、正規core117のファイルを使い、台帳がcleanな117なのに`user.accountCreatedAt`が無い場合、台帳を進めずこの手順へ案内して停止する。DB名の切り替え前にも同じ履歴を検査する。CLIの`backup restore -rollback`は自動migrationを行わず、切り替え前の検査だけを行う。旧履歴の自動変換は行わない。正規core117を適用済みのDBと、別の117を持つ独自系列はこの停止条件に当たらない。

旧117を含むバックアップも、先にアプリから切り離した使い捨てDBへ復元し、以下の全履歴照合と台帳分離を行う。swapで拒否された場合は、一時的に作った復元先を削除し、元の稼働DBは切り替えない。

汎用手順は[本体のmigrationに混ぜてしまった場合](fork-migrations.md#本体のmigrationに混ぜてしまった場合)を参照してください。今回のforkには既に上流のファイル名変更があったため、その文書の「独自ファイルの追加だけ」という前提には収まりません。この対応表と旧版のSQLを使って、適用状態を個別に確認します。

1. 全てのアプリ・queue workerを止め、自動migrateも止める。バックアップを取り、その復元先の使い捨てDBを用意する。以降は最初にその復元先で行う。
2. 使用した旧版と対応表を照合する。`schema_migrations`は1行、`version=117, dirty=false`であることを確認する。番号だけでは適用の証明にならないため、上流coreの`000001`から`000116`までの**全て**のSQLについて、表・列・index・制約・型・データ変換の効果を確認する。特に旧106/core98の`meta.registrationClosed`、旧107/core106の3本のCASCADE外部キー、旧108/core107の`note.pageCount`、旧109〜116/core108〜115の有効なindexを調べる。indexは名前だけでなく対象表・列・方式・条件・有効性を確認する。
3. 旧117/core116は`meta.repositoryUrl`と`feedbackUrl`の旧既定値だけを変更する。独自のURLは変更しない。旧版の適用記録とデータを照合し、未適用なら「適用済み」として116へ進めない。core117以降の効果が既にある、履歴が欠ける、dirtyがある、local台帳が既にあるなど、ここで説明していない状態ではこのSQLを使わない。
4. 旧98の独自処理も確認する。`avatar_decoration`に`host`列がある場合、`host IS NOT NULL`の行が残っていないことを確認する。列が無ければ元の処理はno-op。適用記録と照合する。
5. [専用SQL](sql/misaki-117-to-tracks.sql)を、同じDB・同じschema・同じ接続ユーザーで実行する。1つのDO文の中で検査と2台帳の変更を行う。検査失敗時は台帳を変更しない。SQLの検査は境界の取り違えを防ぐ補助であり、手順2〜4の全履歴確認を代替しない。

   ```bash
   psql -h <host> -p <port> -U <user> -d <restored-database> \
     -v ON_ERROR_STOP=1 --single-transaction -f docs/sql/misaki-117-to-tracks.sql
   ```

   確認済みの状態は`core=116, local=1`へ変わる。データやschemaの本体には触れない。SQLを繰り返すと前提検査で止まる。成功後は次を読み、2行ともcleanであることを確認する。

   ```sql
   SELECT 'core' AS track, version, dirty FROM schema_migrations
   UNION ALL
   SELECT 'local', version, dirty FROM schema_migrations_local;
   ```

6. 新しい版の`elythia migrate -direction up`を実行する。core117〜138が当たり、既に記録したlocal1は再実行されない。`elythia doctor`、データの維持、core/localのオブジェクトを確認する。上流の新規移行も含めて新しい空DBへ適用した場合と、schemaを比較する。
7. 復元先で成功し、バックアップと切り戻し手順を確認した後に、本番への適用を判断する。本番へ移す際も停止・確認・台帳変換・up・検証の順を守る。

旧版で117まで適用していないDBには、この固定値のSQLを使いません。特に旧99〜105はcore98が未適用のままcore99以降が当たった飛び地になりえます。小さい番号へ戻して全てを再実行するとデータ移行も再実行されるため、個別に確認して別の手順を作ります。旧106〜116も実際の適用状態に応じた境界が必要です。`-force`は2台帳をまとめて変更できず、cleanな台帳の変換にも使いません。downで旧状態へ戻すこともしません。

## 検証

`internal/cli/migrate/misaki_history_test.go`は本体/localの番号の一意性と連続性、旧117を分離してから新しい上流へ進める経路、空DBとの差、誤った境界・dirty・既存local台帳の拒否を検査します。既存のデコレーション移行テストはlocalのSQLを直接検証します。実行には本番と分離した使い捨てPostgreSQLを使ってください。
