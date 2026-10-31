<!-- SPDX-FileCopyrightText: syuilo and misskey-project -->
<!-- SPDX-License-Identifier: AGPL-3.0-only -->

# FxTwitter preview plugin v0.4.0

既存 FxTwitter plugin の現在 SDK 向け独立 Go module と、詳細画面専用の frontend です。Go 1.27.3 と、root の `github.com/elythia-network/elythia/plugin` を使います。固定版ビルド・外部取得 OFF での確認・復旧は[日本語導入手順](../../docs/plugins/fxtwitter-install-ja.md)を参照してください。

`elythia-plugin.yml` は `disabled: true`、runtime の `externalFetchEnabled` は既定で `false` です。ビルドへの明示的な選択と、運用者による別途承認された外部取得の有効化が必要です。この移植では公開 repository、タグ、リリース、本番移行を作成しません。

## 公開経路と認可

ログインした viewer ごとに `notes/show` を呼び、note の可視性と、その note に含まれる正確な X/Twitter post ID を検証してから、公開 cache の参照・lease・画像・embed を許可します。role の照会は optional `ActiveRoleRequest` を使い、照会不能時は cold fetch を許可しません。cache hit と lease 敗者は cold-fetch quota を消費しません。

Fx への送信は固定 `https://api.fxtwitter.com/2/status/{numeric-id}` のみです。host の SSRF 対策済み HTTP client を使い、redirect を拒否し、response bytes、text、media 件数と画像 decode の上限を維持します。note 本文、viewer ID、cookie、認証情報を Fx に送りません。

`externalFetchEnabled: false` は preview の外部 job 起動、queued fetch job、既存 cache descriptor の画像 proxy、embed capability の利用を止めます。capabilities と権限のある管理 read は利用できます。

画像・avatar・poster は認証済み same-origin route で毎回 note ACL を検証します。外部画像は正確な HTTPS `pbs.twimg.com` のみを許可します。response は `Cache-Control: private, no-store` とし、viewer の権限変更後に browser/shared cache から認証を迂回して再利用できない契約です。frontend は認証付き Blob 取得と活動状態の gate、abort・URL 解放を行います。

## cache と理由 DTO

post ごとの再取得間隔は既定 60 分です。これは lease/refetch の間隔で、保存行が 60 分で消えるという意味ではありません。user hourly/daily、global hourly、server-derived role 上限と PostgreSQL lease を維持します。

成功した公開 payload は、一時的な network/read/timeout/429/5xx 失敗後にだけ `state: stale` として再利用できます。非一時的な unavailable/schema/disabled 等の完了は `last_success_payload` と時刻を破棄し、その後の network 失敗でも復活させません。新しい authoritative success は公開 cache を再設定できます。media/embed capability も失敗時に無効化します。

`CacheEntry.unavailableReason` は `deleted`, `suspended`, `private`, `blocked`, `unavailable`, `unknown`, `fetch_failed` のいずれかです。既知の理由は、bounded JSON の documented `status.type: tombstone`, `provider: twitter`, `reason` からだけ保持します。HTTP 404/410 は deletion、401/403 は private/suspended の証拠にしません。欠落・未知理由・不正 schema は `unknown`、一時的取得失敗は `fetch_failed` です。terms-withheld detector はありません。内部 error text と upstream message は公開しません。

引用 tombstone は本文を公開せず `PublicPost.quoteUnavailableReason` に既知理由または `unknown` を保持します。定義の根拠は [FxEmbed の Get post API](https://docs.fxembed.com/api/twitter/operations/2statusid/) の tombstone schema です。

## 監査と migration

管理者・moderator 用の `audit_body_snapshot` は公開 cache と別です。既存 `body` は URL 展開、HTML entity decode、media facet 除去後の **normalized text** であり、Fx JSON や exact raw_text ではありません。新旧の全 audit DTO に `bodyFormat: normalized_text_v1` を明示し、body の再書き込み・raw retention の追加はしません。公開 DTO はこのラベルと監査本文を含みません。

migration 1–5 は既存 schema/data を保持します。追加 migration 6 は **non-ready の公開 cache fallback metadata と embed capability だけ**を無効化します。旧版で永久失敗した後にも残っていた payload を安全に区別できないため、既存 non-ready の一時失敗 fallback も再取得が必要です。successful ready payload、監査本文、監査 author snapshot、`x_user` / `x_post`、user note/account を変更しません。通常の新規 lease transition でも legacy permanent state の fallback を引き継ぎません。

`auditRetentionDays: 0` は無期限保持であり即時削除ではありません。明示設定した正数だけ daily bounded pruning を有効にします。既存 moderator delete の対象、確認、理由、権限、冪等なログを維持します。

## クリック発行の完全投稿 embed

native X video は `officialEmbed` の場合だけ、クリックに対応する認証済み `POST /embed/shell` で完全投稿の `https://platform.twitter.com/embed/index.html` URL を発行します。post ID、cache-bound token、有効期限、引用 target、theme/lang、per-click nonce を検証し、note ACL を再確認します。元の `Tweet.html` 動画専用 presentation、直接 video CDN bytes、YouTube player は使いません。既存 cache の YouTube descriptor も capability として拒否します。

外部 video provider は poster と元の X 投稿リンクへの fallback に留めます。`linkOnly` は embed 発行を止めます。frontend は detail/CW reveal/active/viewport gate、mount/abort の cleanup、source-link UI を持ちます。動画モード省略時は `officialEmbed` ですが、外部取得の既定は OFF です。初回手順では `linkOnly` を明示し、公式 iframe は運営者が後で許可してもクリック後だけ読み込みます。

## 検証

独立 module 内で `GOWORK=off go test ./... -count=1`、`go vet ./...`、`go build ./...` を実行します。DB test は real PostgreSQL と一意の専用 schema を使用し、`MK_PLUGIN_TESTS_REQUIRE_DB=1` で DB 不在を失敗にします。外部 HTTP は手書き fixture のみです。`go mod tidy` は使いません。DB/race を含む最終検証結果は task report と CI を確認してください。
