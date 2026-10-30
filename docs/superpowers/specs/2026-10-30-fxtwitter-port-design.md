# FxTwitter preview port design

Status: implements the user's approved separate-PR, external-fetch-OFF preparation scope. Base is PR #29 head `076c0d2f3823f316c1ccdac692fc8a3f7c7e6bb3`; #29 remains unmerged. No deployment, merge, external-fetch enablement, or authentication/permission change is authorized.

## Intent and scope

Reuse the preserved local FxTwitter plugin for an authenticated viewer opening a visible note's detail, only after its CW is actually revealed and the page/preview is active and visible. Ordinary timelines never fetch Fx. A video click loads an official complete X post; failure leaves a direct X link. No X timeline, collection service, direct video CDN playback, or YouTube expansion is added.

The server independently checks authentication, note visibility, and exact validated X post membership before cache access or scheduling external work. Preserve fixed-host fetching, SSRF-safe host HTTP client, redirect refusal, response limits, authenticated image proxy, and moderator-only audit access.

## Approach

The accepted approach is a port onto the current host, rather than replacing the current preview coordinator with the older copy or implementing Fx inside core `/url`. Core receives only the generic capabilities required by the independent plugin. The plugin remains its own Go module and owns Fx fetching, state, leases, cache/audit schema, settings, and UI.

No appropriate existing Fx-specific remote was found. Avoid creating a new public repository: keep the independent module under `plugins/fxtwitter-preview` in the existing fork, in a separate plugin PR stacked on the generic host PR. This follows the existing multi-module plugin layout and can be extracted later. The shipping commit pins the plugin source as part of the immutable fork commit. Preserve `disabled: true` and runtime `externalFetchEnabled: false`; explicit selective inclusion is required to build it. Generic build support must not silently include other disabled plugins.

## Contracts and behavior

- Preserve the current `PreviewProvider` registry, `requiresDetail`, abort/fallback behavior, CW and viewport gates. No file-preview screen is added; the existing URL/file input union remains the extension boundary.
- Add only generic host Blob fetch/media-viewer/component support needed by the old plugin, with same-origin/path validation and unchanged sensitive-media controls. Keep role resolution optional, server-derived, and fail closed; do not change identities or grants.
- The per-post interval defaults to 60 minutes and remains configurable. It is a refetch/lease interval, not a guarantee that stored rows disappear after an hour.
- Public cache and moderator audit remain separate. A non-transient unavailable/schema result clears any stale-public fallback until a new success. A later network failure must not resurrect a post rejected earlier as unavailable.
- Preserve documented tombstone reasons when supplied. HTTP 404/403 alone is not proof of deletion/private status. Unsupported/ambiguous reasons, including a terms-withheld distinction not guaranteed by Fx, remain unknown/unavailable rather than guessed. Transient fetch failure remains distinct.
- Existing audit bodies are normalized text (URL expansion/entity decoding/media-facet removal), not raw Fx JSON or exact `raw_text`. State that accurately in the DTO/UI/operator guide; do not relabel historical rows or expose audit through public fallback.
- Only an explicit click requests the official full-post embed URL/capability; enforce detail/reveal/active gates again, validate the fixed X origin and post ID, clean up on abort/unmount, and retain an explicit source link on failure. Do not reuse the old video-only `Tweet.html` presentation.

## Verification and delivery

Work test-first: meaningful RED for missing host capabilities, success -> explicit unavailable -> network stale suppression, tombstone/unknown distinctions, detail-only/CW gates, click-only full-post embed, source-link fallback, audit separation and normalized-label semantics. Use real isolated PostgreSQL for DB tests and mocked upstream HTTP fixtures, never live Fx/X requests. Reuse existing frontend contracts and test actual components.

Validate module tests, generic host tests, frontend unit/type/lint/build, plugin generation/default exclusion/selective inclusion, and the final immutable SHA's CI. Static review includes authorization boundaries, SSRF/media limits, stale retention, sensitive lightbox, and publication/configuration defaults.

Deliver generic host PR based on the PR #29 branch and a separate plugin/integration PR based on the host branch. Both remain drafts unless explicitly approved otherwise. Provide Japanese install instructions with dependencies, immutable refs, isolated backup, reviewed plugin migrations, explicit build inclusion, OFF configuration, separate operator-performed external enablement, verification, and rollback. Unknown environment values are placeholders. No production commands are executed by this task.
