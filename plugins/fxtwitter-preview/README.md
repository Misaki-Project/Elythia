<!-- SPDX-FileCopyrightText: syuilo and misskey-project -->
<!-- SPDX-License-Identifier: AGPL-3.0-only -->

# FxTwitter preview plugin

## v0.3.0 external YouTube playback

- Fx external-video cards that contain a strict canonical YouTube watch, short, or `youtu.be` URL are classified server-side using only their validated 11-character video ID. The public payload never exposes the original external URL or its playlist/tracking parameters.
- Main and quoted YouTube videos share the plugin-local click-to-load player. The server returns only `https://www.youtube.com/embed/{validated-id}?autoplay=0&playsinline=1`, and the client independently checks the exact scheme, host, path, ID, and query before mounting the iframe. No autoplay permission is granted. While the iframe is mounted, a canonical `youtube.com/watch?v={validated-id}` link remains available so videos whose owners disable embedding do not leave the viewer at a dead-end player.
- Native X videos keep the existing official X embed flow. Unknown or non-canonical external videos remain poster-only, with no embed capability. Existing authentication, note visibility, CW/sensitive reveal, active-tab, viewport, and unmount gates apply to both providers.
- YouTube's documented iframe form and player parameters are described at <https://developers.google.com/youtube/player_parameters>. Loading the iframe contacts YouTube only after the viewer clicks play; deployment owners should account for that third-party request in privacy and CSP review.

## v0.2.3 entity rendering

- Fx HTML character references are decoded exactly once after raw UTF-16 facet matching. Entity ranges are recalculated for the decoded output, URL targets still require validated HTTP(S), and decoded HTML-like text remains inert Vue text rather than markup.

## v0.2.2 media and text rendering

- Fx `raw_text.facets` are normalized with UTF-16 offsets. Valid URL replacements use Misskey's standard URL component, media facets are removed from visible text, and invalid replacements remain plain text.
- Fx `possibly_sensitive` is kept independently for the main and quoted posts. Media follows the existing Misskey `respect` / `ignore` / `force` preference and confirmation flow; revealing a video does not start playback.
- Main and quoted images use the existing grid for 1–4 images and an independent horizontal carousel for 5–20 images. The carousel fetches the current image and its adjacent images on demand; selecting an image explicitly loads the remaining gallery before opening the existing viewer. When either side has 5 or more images, main and quoted photos share a 64 MiB retained-blob limit per card. If the selected gallery exceeds that limit or a photo cannot be loaded, the viewer stays closed and the card explains why. Official X playback remains click-to-load and is centered at a maximum width of 560 px.

This Misaki-only plugin replaces ordinary X/Twitter URL previews when a visible,
active note card enters the viewport. It is intentionally disabled at two independent gates:

1. `elythia-plugin.yml` has `disabled: true`, so ordinary bundled builds omit it.
2. If explicitly included in a reviewed build, `externalFetchEnabled` still
   defaults to `false`, so the frontend provider is not registered and Fx is
   not contacted.

It is not an upstream shiroha-a feature. Do not remove either gate as part of a
routine build or deploy.

## Request and data flow

The preview API requires a logged-in user. Before reading or scheduling a cache
entry it calls `notes/show` as that user and checks that the visible note
contains the exact validated X/Twitter status ID. Fx receives only that numeric
ID at the fixed endpoint `https://api.fxtwitter.com/2/status/{id}` through the
host-provided SSRF-aware HTTP client. Local note text, cookies, secrets, and
user identifiers are not sent to Fx.

Fetching starts only when the URL is on an active, revealed note card that has
entered the viewport. Hidden CW content, offscreen cards, inactive tabs, and
unmounted components do not initiate the plugin request. A bounded loading card
is shown during the request and disables its pulse animation for reduced-motion
users. A PostgreSQL lease plus checked native role policies authorize the viewer
and set per-user hourly/daily cold-fetch limits. The existing PostgreSQL
`global_per_hour` value is an additional database-wide safety cap shared by
all workers using the same plugin schema. Administrators can change it in the
plugin admin page. Clients
cannot force a refresh. Unavailable and stale responses expose a safe `error`
code and `retryAt`, without the internal fetch class. `temporary` covers
transient network, rate-limit, and upstream server failures. `private` and
`not-found` are returned only for an exact, typed Fx error (`PRIVATE_TWEET` or
`NOT_FOUND` in a consistent HTTP 4xx response) or a matching typed tombstone
that explicitly reports `private` or `deleted`. Bare HTTP statuses, mismatched
response envelopes, and older ambiguous error classes are reported as
`unavailable`; raw upstream messages are never returned. Once `retryAt` has passed, the
viewer can retry with the normal `/preview` request. That request still uses the
same note ACL, role check, quota, lease, and refetch interval; there is no
forced-refresh path.

The current public preview cache is separate from moderator-only text audit
snapshots. Audit text is never a public fallback. A previously successful
public payload may be served as explicitly marked `stale` only after transient
network, timeout, rate-limit, or upstream 5xx failures. Explicit not-found,
private, or invalid responses never use stale fallback. Already collected audit
snapshots remain separate in either case.

Avatar, image, and poster responses are same-origin plugin URLs. The server
resolves their source from the unexpired cache, permits only exact HTTPS
`pbs.twimg.com` for X media and `img.youtube.com` for validated YouTube posters,
rejects redirects, enforces byte and decoded-image limits, and returns a
`private, no-store` response. Arbitrary media hosts and URLs are not accepted.

Fx video format URLs are kept only in private plugin cache metadata and are
never exposed through public preview data. In `nativeProxy` mode, a selected
video is fetched only after its play action, checked against the exact
cache-bound source, bounded by the configured byte limit, and returned as an
authenticated `private, no-store` Blob response; the bytes are not persisted.
In `officialEmbed` mode, a playable poster has an explicit play button. Only
that click requests an authenticated, cache-bound provider URL. Native X videos
use the fixed `platform.twitter.com/embed/Tweet.html` presentation generated by
X Publish's Embedded Video flow (`maxWidth=560px`); validated YouTube external
cards use only the fixed `www.youtube.com/embed/{id}` form. The client validates
the provider-specific scheme, host, path, identity, query, and X per-click nonce
before mounting it. Deactivation removes either iframe. Use `linkOnly` to
disable playback. The sandbox matches Misskey's existing official X embed
(`allow-scripts` and `allow-same-origin`, plus popup escape for external links)
but omits autoplay permission. No `srcdoc` is used, so the widget does not
depend on relaxing the parent page's `script-src` CSP.

In `text` presentation, attachment bytes are not requested. The card instead
shows compact Japanese counts such as `画像3件・動画1件`. Author avatars use an independent media lease so posts with
no attachment images still show an avatar. Leaving and re-entering the viewport
retains both identity and attachment Blob URLs while the owning note remains
mounted, avoiding duplicate proxy requests; authorization/reveal loss or final
unmount still aborts and releases them.

Fx `raw_text` facets are normalized on the server. Only validated HTTP(S)
replacement URLs become external links; hashtags route to Misskey's local tag
page, and mentions use a fixed `https://x.com/{validated-handle}` target. Missing
or invalid facets remain plain text. Author and quoted-author identities link
only when their handles pass the same X-handle validation. This does not perform
an additional URL-resolution request.

Validated poster metadata from Fx's `media.videos[]` is returned in token-free
main and quote arrays with index-specific same-origin proxy URLs. The legacy
`video` field remains the first item and carries the single post-level embed
capability; existing single-video cached payloads continue to resolve index 0.
Official X
iframes declare `scrolling="no"` and continue to use X's resize messages rather
than clipping the frame. Engagement displays combine Fx's distinct repost and
quote counters exactly once. One-image media preserves its natural aspect ratio
up to Misskey's `min(360px, 50vh)` height convention; unusually tall media is
center-cropped at that cap. Existing two-, three-, and four-image grids are
unchanged.

Legacy cached payloads with JSON `images: null` are normalized to an empty list
at the frontend boundary. New public responses serialize empty image collections
as `[]`, preventing attachment loading from rejecting before avatar or video
poster rendering can complete.

## Example configuration

This is a review template, not a production-enable recommendation:

```yaml
plugins:
  fxtwitter-preview:
    enabled: true
    externalFetchEnabled: false
    refetchIntervalMinutes: 60
    auditRetentionDays: 0
    requestTimeoutSeconds: 5
    maxResponseBytes: 1048576
    staticImageMaxBytes: 10485760
    # Legacy limit settings are retained for migration review; native user
    # permissions and per-role quotas are managed in Admin -> Roles.
    authenticatedColdFetchesPerHour: 100
    authenticatedColdFetchesPerDay: 600
    globalColdFetchesPerHour: 300
    roleColdFetchLimits: {}
    # linkOnly / officialEmbed / nativeProxy; keep linkOnly until reviewed.
    videoPlaybackMode: linkOnly
```

`auditRetentionDays: 0` means unlimited retention, not immediate deletion.
Positive values enable bounded daily pruning. Preview, media, embed, cache-miss,
and stale revalidation paths remain login-only; anonymous requests cannot
schedule or perform outbound fetches.

Legacy YAML and database values for authenticated defaults and per-role quotas
remain available for migration review; they are not automatically converted to
native role policy and there is no bulk rewrite. Administrators can manually
translate needed values in Admin -> Roles. `canUseFxTwitterPreview`,
`fxTwitterColdFetchesPerHour`, and `fxTwitterColdFetchesPerDay` are resolved
through the host's checked native policy API; resolver errors fail closed. The
plugin admin page keeps the legacy settings read-only and writes only the
database-backed safety cap shared by all processes using the same plugin schema.
Cache hits and callers that lose the per-post
lease do not consume a cold-fetch budget.

The legacy `/admin/rate-limit-settings` endpoint is an administrator-only
read/export for migration review. The only settings write is the administrator
endpoint `/admin/global-rate-limit/update`, with `revision` and `globalPerHour`;
per-role permission and quota configuration stays in the host role editor.

## Moderator deletion

The plugin admin page requires an explicit numeric post ID. A moderator chooses
`public`, `audit`, or `both`, supplies a reason, and types `DELETE`. Server-side
moderator authorization, confirmation, and an idempotent deletion log are
always enforced. This implementation adds the mechanism but performs no
production deletion or configuration change by itself.

## Before production enablement

- Decide whether applicable X/Fx terms and local law permit public caching and
  the chosen moderator-only audit retention. FxEmbed's MIT license covers code;
  it does not grant rights to retain or redistribute X content.
- Define deletion/change handling, privacy notice, escalation, and backup
  treatment. X's content-compliance policy includes deletion/change duties,
  but its application to indirect Fx use and private audit snapshots is not
  established here.
- Review cold-fetch and media-proxy capacity, outbound proxy/CSP settings, and
  the fixed host allowlists.
- Exercise timeout, unavailable, moderator deletion, kill-switch, and restore
  procedures against a non-production database.
- Keep `externalFetchEnabled: false` until the above review is approved. Start
  with `videoPlaybackMode: linkOnly`; enable `officialEmbed` only after a
  separately authorized live compatibility/privacy check succeeds without
  weakening the sandbox.
