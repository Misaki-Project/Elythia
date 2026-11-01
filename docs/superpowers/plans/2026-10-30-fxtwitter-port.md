# FxTwitter preview port implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** prepare a separately reviewable FxTwitter plugin on the PR #29 base, with external fetching OFF.

**Architecture:** minimal generic host APIs in one PR; the independent Go module and Fx UI/CI/docs in a stacked second PR. Reuse preserved source without replacing the modern preview coordinator.

**Tech Stack:** Go 1.27.3, pgx/PostgreSQL 18, Vue/TypeScript, Vitest, Node 26.12.1, pnpm 12.14.0 (use repository pins).

**Spec:** `docs/superpowers/specs/2026-10-30-fxtwitter-port-design.md`

## Global Constraints

- Base `076c0d2f3823f316c1ccdac692fc8a3f7c7e6bb3`, dependency PR #29; never modify its branch.
- No merge, production deployment/migration/restart, external-fetch enablement, or authentication/permission change. No new public repository without separate approval. No Astra. No secrets or entire .env reads.
- Preserve source/patch backups, unrelated user work, current coordinator behavior, independent plugin module, build-disabled plus runtime external-OFF defaults.
- Fetch only on authenticated, revealed, active/visible note detail. No timeline or collection features. X-only click-to-load full-post embed and explicit failure link.
- Never infer deleted/private/withheld from generic failures; keep public data and moderator audit separate, and explicitly label audit as normalized text.
- No go mod tidy, no DB mocks, no production data operations. New regression tests must show a meaningful RED/mutation failure before fixes.

## Review Focus

1. A cached formerly public post becoming unavailable must stay absent after a later network error; test in Task 2.
2. CW collapse, tab deactivation, source-note switch and late promises must prevent fetch/display/replay; test in Task 3.
3. Sensitive media passed as plugin Blob must retain the existing lightbox protection; test in Task 1.
4. Cache/audit APIs must enforce note ACL/moderator authorization even with arbitrary IDs; test in Task 2.
5. Disabled builds and external-OFF runtime must remain inert; test generation and integration in Tasks 1/4.

## Task 1: Generic host compatibility

Files: `plugin/http.go`, `plugin/plugintest/*`, relevant `internal/server/plugin_*` adapter/tests; `frontend/packages/frontend/src/plugin-api.ts`, exports, lightbox/sensitive components/utilities and focused unit tests; `Dockerfile.bundled`, host API documentation; CI and Misaki plugin compatibility workflow pull-request base filters.

Consumes: current Request/API/PreviewProvider contracts and old-copy reference at `E:/tmp/fxtwitter-preview-e2e-20261002`.
Produces: optional authenticated active-role lookup for cold-fetch budgets, authenticated same-origin Blob fetch and media viewer host API with sensitivity preserved; only required stable component exports; selective disabled-plugin build inclusion. No Fx code in core.

- [ ] Add focused behavior tests for each missing capability and record a meaningful RED.
- [ ] Port the minimal generic API implementation onto current contracts; preserve optional-interface compatibility and fail-closed role lookup.
- [ ] Verify focused tests, typecheck and host/plugin boundary tests; do not replace current coordinator or port TL preferences/manual activation.
- [ ] Let CI and Misaki plugin compatibility run for stacked PR bases by removing only their pull_request branch filter, preserving push filters and read-only permissions.
- [ ] Review spec compliance and code quality, commit only host files and docs.

## Task 2: Independent plugin backend

Files: `plugins/fxtwitter-preview/{elythia-plugin.yml,go.mod,go.sum,*.go,README.md}` and module tests.

Consumes: Task 1 host contracts. Produces: independent module with fixed Fx client, note ACL, role-aware budgets, cache lease, normalized audit, explicit unavailable reasons and no stale resurrection.

- [ ] Preserve old code, adapt module/import/manifest names without new dependencies unless required; retain disabled/OFF defaults.
- [ ] Add success -> unavailable -> network regression and classification/authorization tests, run RED before implementation.
- [ ] Implement stale fallback clearing and authoritative tombstone parsing; ambiguous failures remain unknown. Label audit normalized; never use it as public fallback.
- [ ] Run real isolated-DB module/race tests and mutation checks. Review and commit only backend module files.

## Task 3: Detail-only Fx frontend

Files: plugin `frontend/*`, `frontend/packages/frontend/test/unit/fxtwitter-preview-plugin.test.ts`, focused component/fixture tests.

Consumes: Task 1 host Blob/sensitive APIs, Task 2 public/audit DTOs. Produces: X matcher requiring detail, gated loading and media, click-only official complete-post embed, failure source link, accurate normalized-audit display.

- [ ] Add RED tests forbidding no-CW timeline fetch, unrevealed CW fetch, inactive/aborted fetch and premature embed; test fallback link and unknown statuses.
- [ ] Port only the detail scope; omit old timeline preferences and YouTube playback. Use current coordinator and generic host APIs.
- [ ] Verify plugin tests, existing preview-provider tests, sensitivity/lightbox contracts, typecheck/lint/build. Review and commit.

## Task 4: Integration, CI, operator guide and PRs

Files: Makefile/plugin CI allowlists and explicit module DB tests, plugin docs, Japanese installation guide, minimal build/default exclusion tests.

Consumes: all preceding tasks. Produces: separate host and plugin PRs, final immutable SHA CI evidence, user-executable OFF-first installation guide.

- [ ] Add plugin to intentional disabled-plugin test allowlists; require actual DB tests rather than silent skip. Verify normal builds omit Fx and explicit selective builds include only Fx.
- [ ] Write Japanese guide for immutable refs/dependencies, backups and restore rehearsal, build inclusion, plugin schema migrations on a test DB, runtime OFF, explicit later enablement, validation and rollback. Unknown values use marked placeholders.
- [ ] Run required checks and broad review; fix findings through regression tests. Push ordinary feature branches; create/attach draft stacked PRs and describe dependency on #29.
- [ ] Confirm all final-head workflows, PR state and original working-tree preservation. No deployment or merge.
