# Tracking parameter cleaner

This plugin cleans a small, known set of tracking query keys when plain text is pasted into the post form. The current build system requires every bundled plugin to have a Go module, so it includes a metadata-only Go registration with no jobs and no-op HTTP routes. The standard plugin storage bootstrap creates the empty `plugin_tracking_parameter_cleaner` schema at application startup. The plugin declares no migrations, so it creates no tables or migration rows. The frontend paste transform makes no network requests, expands no redirects, and does not rewrite the clipboard contents.

Each user can turn cleaning off in the host's personal settings; it starts on by default. When cleaning changes pasted text that is actually inserted or accepted as a long-text upload, the host shows a fixed confirmation message. The notification does not include the URL, pasted text, or removed parameter values. Quote attachments that do not insert cleaned text, unchanged pastes, and pastes while cleaning is disabled produce no notification.

## Supported links

The cleaner recognizes only exact HTTP(S) hosts listed in `frontend/cleaner.ts`:

- X and Twitter: `x.com`, `twitter.com`, and their listed `www`/`mobile` hosts; removes `s`, `t`, `ref_src`, and `ref_url` on shared status paths only.
- YouTube: `youtube.com` and `youtu.be` hosts listed in the source; removes `si`.
- Instagram: listed `instagram.com` hosts; removes `igsh` and `igshid`.
- Facebook: listed Facebook hosts; removes `fbclid`.
- Threads: listed `threads.com` and `threads.net` hosts; removes `igsh` and `igshid`.
- TikTok: listed `tiktok.com` hosts; removes `_t` and `_r`.
- Reddit: listed Reddit hosts; removes `share_id`.
- On those supported social hosts only, removes `utm_source`, `utm_medium`, `utm_campaign`, `utm_term`, `utm_content`, and `utm_id`.

The selected keys follow the scoped query-filter approach documented by [Brave](https://github.com/brave/brave-browser/wiki/Query-String-Filter) and the site rules in [ClearURLs Rules](https://github.com/ClearURLs/Rules/blob/master/data.min.json). This plugin intentionally uses a small fixed subset instead of applying the full rulesets.

## Preserved content

Unknown hosts, deceptive lookalike hosts, non-default ports, URLs with recognized signature/authentication parameters or opaque redirect/auth paths, unknown query keys, video IDs, playlist/index/start parameters, fragments, query order, and the raw encoding of retained query parts are preserved. URLs in fenced or inline code are left alone. Japanese punctuation and surrounding text are retained; a URL with non-ASCII raw query data is left unchanged to avoid swallowing prose at a sentence boundary.

## Installation

The standard bundled build discovers this plugin from `elythia-plugin.yml` and includes its `frontend/index.ts` because the manifest has no `disabled: true` entry. For a custom build, place it under `plugins/tracking-parameter-cleaner` and run `make plugins` before compiling. The host must provide the frontend paste-transform API and the per-user preference. At startup the host creates the empty plugin schema described above; the Go registration has no migrations, jobs, or route behavior.
