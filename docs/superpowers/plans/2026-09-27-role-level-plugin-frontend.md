# roleLevel Plugin Frontend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** roleLevel プラグインの frontend 一式（汎用 `admin:role-editor` スロット、level 曲線 / policy 編集、admin user の XP 操作、profile の進捗、管理ページ）と、それを検証する unit / desktop / mobile / accessibility / e2e テスト、fork の release、mk 側の submodule と assets pin を 1 経路で成立させる。

**Architecture:** frontend を 2 つの置き場に分ける。**汎用スロットだけ**を `Misaki-Project/misskey-ts` fork に upstream 可能な形で追加し（`SlotRole` / `SlotContext.readonly` / `admin:role-editor`）、**ロール固有の component は mk リポジトリの `plugins/rolelevel/frontend/`** に置く。`tools/pluginbuild` が生成する `@mkplugin/role-level` alias と fork の `tsconfig.json` の `@mkplugin/*` はどちらもリポジトリ直下の `plugins/*/frontend` を指すので、component を fork 側に置くと bundle 機構が解決先を持たない（Task 4 Step 3）。プラグインは公開 Plugin API の再公開物（`MkInput` / `MkButton` / `MkFolder` / `MkLoading`）と素の DOM しか使わず、認可は backend に委ねる。level / XP / curve / policy range の計算は backend だけが持ち、frontend は preview と表示に徹する。

**Tech Stack:** Go 1.27、Echo、testify、Vue 3.5.42、TypeScript、Vitest 4.1.11、vue-tsc、eslint、pnpm（submodule の `packageManager`）、Playwright（`tests/playwright`）、Git submodule、GitHub Actions、GHCR

## Global Constraints

- 追跡 issue は `https://github.com/Misaki-Project/mk/issues/12`（spec に書かれた）。新規 issue を作らない。
- backend ロジック（`plugin.go` / `routes.go` / storage / migration）と DB migration はこの plan の対象外。`plugins/rolelevel/mk-plugin.yml` / `go.mod` / `plugin.go` / `*.sql` は **backend plan の所有**であり、この plan では作らない・編集しない。
- mk の core schema（`role` / `role_assignment`）に column・table・enum を追加しない。
- **`plugins/rolelevel/` の roleLevel 実装は `shiroha-a/mk` へ行かない。** backend（routes / storage / migration）を含むこの機能は `Misaki-Project/mk` にだけ置く。
- **`shiroha-a/misskey-ts` への PR は禁止ではない。** Task 15 で、汎用 `admin:role-editor` スロットだけを **upstream PR** として出す（`SlotRole` / `SlotContext.readonly` / mount / test のみ。roleLevel の component・client・locale・policy 一覧・mk 固有の設定を一切含めない）。権限・contribution process が許すときだけで、**許さなくても Misaki fork の PR は待ちなく進む**。
- 汎用 Plugin API 変更は **additive のみ**。`plugin.APIVersion` は上げない（`docs/plugins/compatibility.md` の「追加（マイナー）」の契約に従う）。
- fork の `plugin-api.ts` から再公開する component を増やさない（`MkInput` / `MkButton` / `MkFolder` / `MkLoading` の 4 つのまま）。accessibility のために `MkInput` を触らない（label が `div` で `for` が無く accessible name を持たない。`third_party/misskey/packages/frontend/src/components/MkInput.vue:13`）。label 関連付けは素の `<label for>` で行う。
- level / XP / curve / policy range の計算を frontend に書かない。single source of truth は backend。frontend の `previewExp()` は operator 向けの preview だけ。
- `multiplier` の operand は **有限な倍率**（raw factor）である。`1.5` = ×1.5、`0.5` = 半分、`0` = 0。百分率ではない。backend plan は `floor(current * operand)` を 0..MAX_SAFE_INTEGER に clamp して実装すること。
- backend 側の累積 level threshold は**小数を持つ**（`2.5 + 3.25 = 5.75`）。表示用の `currentLevelExp` は backend が floor した整数。frontend は「次の level 必要 XP」を自前で出さず、`ExperienceResult` の値をそのまま描く。`progressPercent()` は backend が返した 2 数を割るだけで、threshold を組み立てない。
- **XP は safe integer の JSON number で往復する。** 文字列化しない（`"100"` ではなく `100`）。`formatExp` は表示専用で、送信用の値には使わない。
- frontend が叩く **plugin の POST route は 11 個で固定**する: `admin/roles/list` / `admin/roles/show` / `admin/roles/update` / `admin/roles/delete` / `admin/users/show` / `admin/change-exp` / `roles/users` / `users/show` / `admin/audit` / `admin/orphans` / `admin/reconcile`。命名は spec の Plugin Routes 節（GET 表現）に合わせ、**verb は POST だけ**（`host.api` が POST 固定のため）。
- **付与 / 解除は plugin の route ではない。** native の `admin/roles/assign` / `admin/roles/unassign` を `host.api` 越しに呼ぶ（`host.api` は endpoint 名をそのまま `misskeyApi` に渡すので native も呼べる）。XP は assignment に紐づくので、native の assignment を作ってから XP を書く順序が spec の XP Mutation 節と一致する。
- プラグインの error は `plugin.NewCodedStatusError` の `code` で受け取る（`internal/server/plugin_wiring.go:735` が `{"error":{"message":..,"code":..}}` を返す）。403 判定に HTTP status は使えないので `ROLE_LEVEL_FORBIDDEN` で「描かない」を決める。
- frontend の表示可否を authorization boundary にしない（spec Authorization 節）。panel は 403 で **何も描かない**、他の error は **理由を出す**。
- plugin frontend のソースは `plugins/rolelevel/frontend/` に置く。fork 側へ移さない（Task 4 Step 3 の理由）。
- 新しい npm / Go 依存を追加しない。vitest も eslint も既存の物だけ使う。accessibility は `@axe-core/playwright` を足さず、素の DOM 検証で固定する。
- 既存 plan ファイル（`docs/superpowers/plans/2026-09-25-can-delete-account.md` を含む）を変更しない。
- 変更前の `go test ./...` には PostgreSQL 未構成、plugin surface golden drift、Windows 固有 test の既存 failure がある。対象 package と repository gate で新規 regression を判定する。
- 日本語文中は不要な半角スペースを入れない（`CLAUDE.md` 5 章）。GoDoc は英語、実装理由の inline comment は日本語。
- `$(shell …)` を Makefile に追加しない（`gaterun-check` の前提。`CLAUDE.md` 2026-09-06）。

---

## File Structure

### fork（`Misaki-Project/misskey-ts`、submodule `third_party/misskey`）

| ファイル | 責務 |
|---|---|
| `packages/frontend/src/plugin-api.ts` | `SlotName` に `admin:role-editor` を追加し、`SlotRole` と `SlotContext.role` / `SlotContext.readonly` を宣言する。公開面の唯一の場所 |
| `packages/frontend/src/pages/admin/roles.editor.vue` | role editor の中に `admin:role-editor` の mount を 1 つ置き、ctx に role と readonly を渡す |
| `packages/frontend/test/unit/plugin-slot-role.test.ts` | スロットの登録経路と `SlotRole` / `SlotContext` の形を固定する（形は `vue-tsc` が見る） |
| `packages/frontend/vitest.config.unit.ts` | mk リポジトリの `plugins/*/frontend/**/*.test.ts` を含める。生成物が無い submodule 単体の checkout では include を足さない |
| `packages/frontend/tsconfig.json` | `@mkplugin/role-level` の明示 path を 1 つ足す。ワイルドカード `@mkplugin/*` は manifest 名（`role-level`）をディレクトリ名（`rolelevel`）へ引けないため（Task 3） |

### mk リポジトリ（`Misaki-Project/mk`）

| ファイル | 責務 |
|---|---|
| `plugins/rolelevel/frontend/index.ts` | `definePlugin`。`admin:role-editor` / `admin:user` / `profile:info` の登録と管理ページ 1 枚 |
| `plugins/rolelevel/frontend/types.ts` | backend との境界の型。import を持たない |
| `plugins/rolelevel/frontend/errors.ts` | `errorCodeOf()` と `errorMessage()`。import は `./types.js` だけ |
| `plugins/rolelevel/frontend/api.ts` | `initApi` と 2 つの呼び出し口: `pluginApi`（plugin の 11 POST route、`plugin/rolelevel/` prefix）と `nativeApi`（native の `admin/roles/assign` / `admin/roles/unassign`）。型は `types.ts` 由来だけ |
| `plugins/rolelevel/frontend/locales.ts` | プラグイン内の文言（`ja` / `en`）。プラグインは `@/i18n.js` を import できない |
| `plugins/rolelevel/frontend/policy-keys.ts` | level 設定の対象にする native policy key と型。Task 10 の drift gate が読む |
| `plugins/rolelevel/frontend/xp.ts` | 数値の preview / 表示。import は `./types.js` だけ |
| `plugins/rolelevel/frontend/CurveEditor.vue` | 曲線 segment の表 |
| `plugins/rolelevel/frontend/PolicyRangeEditor.vue` | policy range の表（key / 種別 / 値 / 開始 stage） |
| `plugins/rolelevel/frontend/RoleLevelPanel.vue` | `admin:role-editor` の実体。level 有効化 / 基準 level / 曲線 / policy range |
| `plugins/rolelevel/frontend/AdminUserLevels.vue` | `admin:user` の実体。assign / set / add / multiplier / unassign / 監査 |
| `plugins/rolelevel/frontend/ProfileLevelCard.vue` | `profile:info` の実体。level / XP / 次の level までの進捗 |
| `plugins/rolelevel/frontend/MemberRanking.vue` | XP 順の member 一覧（cursor ページング） |
| `plugins/rolelevel/frontend/ManagePage.vue` | 管理ページ。level role 一覧 / orphan / reconciliation |
| `plugins/rolelevel/frontend/tsconfig.json` | submodule の frontend tsconfig を継承し、plugin の型を単独で見る |
| `plugins/rolelevel/frontend/test/xp.test.ts` | `xp.ts` の unit test |
| `plugins/rolelevel/frontend/test/locales.test.ts` | `locales.ts` の unit test（key 集合の一致・fallback） |
| `plugins/rolelevel/frontend/test/errors.test.ts` | `errors.ts` の unit test（code の取り出しと文言への写像） |
| `internal/server/rolelevel_frontend_gate_test.go` | 静的 gate 3 本（スロット ctx / policy key drift / 登録の全 surface） |
| `tests/playwright/specs/mkgo/ui/rolelevel_role_editor.spec.ts` | desktop: role editor の保存と再読込、conditional / 未保存での非表示 |
| `tests/playwright/specs/mkgo/ui/rolelevel_admin_user.spec.ts` | desktop: admin user の XP 操作と監査 |
| `tests/playwright/specs/mkgo/ui/rolelevel_profile.spec.ts` | desktop: profile の進捗とリモートユーザー非表示 |
| `tests/playwright/specs/mkgo/ui/rolelevel_manage_page.spec.ts` | desktop: 管理ページ / member 順位 / orphan / reconciliation |
| `tests/playwright/specs/mkgo/ui/rolelevel_mobile_a11y.spec.ts` | 狭い viewport の横あふれと accessibility（label / progressbar / キーボード到達） |
| `.gitignore` | `!plugins/rolelevel/` を追加（`plugins/*` は gitignore 済み） |
| `Makefile` | `plugins-rolelevel` / `e2e-frontend-build-rolelevel` / `rolelevel-frontend-check` / `rolelevel-plugin-test` と `frontend-check` の gate regex 追加 |
| `.github/workflows/ci.yml` | `frontend-check` job に plugin frontend の型検査と unit test の 2 step |
| `tests/playwright/instance.yml` | `plugins.role-level.enabled: true`（e2e だけ有効にする） |
| `docs/plugins/authoring.md` | スロット表に `admin:role-editor` の行、ctx の形、TS 公開面一覧に `SlotRole` |
| `docs/plugins/compatibility.md` | 「追加（マイナー）」節に TS スロットの additive 契約を追記 |
| `docs/divergence.md` | pin 行（tag + 短縮 SHA）、サマリ表の件数と範囲、§4-2 に 1 行 |
| `Dockerfile.bundled` | `ARG MISSKEY_ASSETS_IMAGE=` を新しい tag へ |
| `third_party/misskey` | gitlink を tag 済み commit へ |

**backend plan が所有し、この plan が触れないもの**: `plugins/rolelevel/mk-plugin.yml`、`plugins/rolelevel/go.mod`、`plugins/rolelevel/go.sum`、`plugins/rolelevel/plugin.go`、`plugins/rolelevel/routes.go`、`plugins/rolelevel/migration/*.sql`、`plugins/rolelevel/README.md`、`docs/plugins/operating.md` の同梱プラグイン一覧、`docs/superpowers/specs/2026-09-27-role-level-plugin-design.md`。

---

### Task 1: Baseline, Tracking Issue, And Branch Preparation

**Files:**
- No repository file changes

**Interfaces:**
- Consumes: mk の作業ブランチ `feature/role-level-plugin`、fork の pin `2026.9.1-mk.2` (`1a53308b`)
- Produces: fork の作業ブランチ `feature/role-editor-slot`（base tag `2026.9.1-mk.2`）
- Produces: 追跡 issue `https://github.com/Misaki-Project/mk/issues/12` の確認記録

- [ ] **Step 1: Confirm the mk worktree baseline**

Run in `E:\tmp\opencode\mk-can-delete-account`:

```powershell
git status --short --branch
git rev-parse HEAD
git -C third_party/misskey rev-parse HEAD
git -C third_party/misskey describe --tags
```

Expected: branch `feature/role-level-plugin`、submodule HEAD `1a53308b0c7b820564d48ead06006eb5b1020786`、`describe` が `2026.9.1-mk.2`。`git status` に出る未追跡ファイルは `typecheck-task4.txt` だけ（触らない）。

- [ ] **Step 2: Confirm the tracking issue and the PR bases**

```powershell
gh issue view 12 --repo Misaki-Project/mk --json number,title,state,url
gh repo view Misaki-Project/misskey-ts --json nameWithOwner,isFork,parent,defaultBranchRef
gh repo view Misaki-Project/mk --json nameWithOwner,defaultBranchRef
```

Expected: issue 12 が open、`Misaki-Project/misskey-ts` の既定ブランチは `mk-2026.9.0`（**PR の base には使わない**。base を上げると独自変更 151 個が混ざる）、`Misaki-Project/mk` の既定ブランチは `Misaki-develop`。

- [ ] **Step 3: Create the fork work branch from the current pin**

Run in `E:\tmp\opencode\mk-can-delete-account\third_party\misskey`:

```powershell
git fetch origin mk-2026.9.1
git switch --create feature/role-editor-slot 2026.9.1-mk.2
git status --short --branch
```

Expected: `## feature/role-editor-slot` で clean。**base は tag から切る**（`mk-2026.9.1` の tip ではなく）。pin より後の upstream 変更を混ぜないため。

- [ ] **Step 4: Install the fork workspace once**

```powershell
pnpm install --frozen-lockfile
pnpm build-pre
pnpm -r build
```

Expected: 成功する。**初回だけ数分かかる**。以降すべての vitest / vue-tsc / eslint はこの state を前提とする（`Makefile:83-88` の frontend-test が同じ前提を書いている）。Node は submodule の `.node-version` に揃えること（ABI 不一致で `re2` が落ちる）。

### Task 2: Generic `admin:role-editor` Slot In The Fork (Upstreamable)

**Files:**
- Modify: `third_party/misskey/packages/frontend/src/plugin-api.ts:52-106`
- Modify: `third_party/misskey/packages/frontend/src/pages/admin/roles.editor.vue:75-112`
- Create: `third_party/misskey/packages/frontend/test/unit/plugin-slot-role.test.ts`

**Interfaces:**
- Produces: `SlotRole = { id: string | null; name: string; target: 'manual' | 'conditional'; canEditMembersByModerator: boolean }`
- Produces: `SlotContext.role?: SlotRole`
- Produces: `SlotContext.readonly?: boolean`
- Produces: `SlotName` のメンバ `'admin:role-editor'`
- Produces: `roles.editor.vue` 内の `<MkPluginSlot name="admin:role-editor" :ctx="{ role: {...}, readonly: ... }" />`

- [ ] **Step 1: Write the failing slot contract test**

Create `third_party/misskey/packages/frontend/test/unit/plugin-slot-role.test.ts`:

```ts
/*
 * SPDX-FileCopyrightText: mk-go project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import { beforeEach, describe, expect, test } from 'vitest';
import { _resetSlotsForTest, definePlugin, launchServerPlugins, slotMounts } from '@/plugin-api.js';
import type { SlotContext, SlotRole } from '@/plugin-api.js';

/*
 * `admin:role-editor` の ctx 契約。
 *
 * **形の検証は vue-tsc がする。** 下の `consume` が `SlotRole` の全
 * フィールドを読むので、フィールドが減れば `make frontend-check`
 * (`vue-tsc --noEmit`。fork の tsconfig は `./test/**\/*.ts` を include している)
 * が type error を出す。**このファイルだけを見て「形は守られている」と
 * 思ってはいけない** — 実行時は何も検証していない。
 *
 * 実行時に見るのは「登録経路が ctx をそのまま届ける」ことだけ。renderer の
 * 実行は `MkPluginSlot` の責務だが、その呼び出し方 (`renderer(el, ctx)`) を
 * ここで固定しておかないと、mount 側の ctx だけを壊す変更が緑のままになる。
 */
describe('admin:role-editor slot', () => {
	beforeEach(() => {
		_resetSlotsForTest();
	});

	test('登録した renderer に ctx がそのまま届く', async () => {
		const seen: SlotContext[] = [];
		await launchServerPlugins([
			definePlugin({
				name: 'role-level',
				setup(host) {
					host.slot('admin:role-editor', (_el, ctx) => {
						seen.push(ctx);
					});
				},
			}),
		]);

		const mounts = slotMounts('admin:role-editor');
		expect(mounts.map((m) => m.plugin)).toEqual(['role-level']);

		const role: SlotRole = {
			id: 'r1',
			name: 'Level',
			target: 'manual',
			canEditMembersByModerator: true,
		};
		const ctx: SlotContext = { role, readonly: false };
		mounts[0].renderer(document.createElement('div'), ctx);
		expect(seen).toEqual([ctx]);
	});

	test('未保存の新規ロールは id が null で Readonly が付く', () => {
		const consume = (r: SlotRole | undefined, ro: boolean | undefined): string => {
			if (r == null) return 'none';
			return [String(r.id), r.name, r.target, String(r.canEditMembersByModerator), String(ro)].join('/');
		};

		const ctx: SlotContext = {
			role: { id: null, name: 'New Role', target: 'manual', canEditMembersByModerator: false },
			readonly: true,
		};
		expect(consume(ctx.role, ctx.readonly)).toBe('null/New Role/manual/false/true');
	});

	test('同じスロットに複数プラグインが登録されても名前順に並ぶ', async () => {
		await launchServerPlugins([
			definePlugin({ name: 'zeta', setup: (host) => { host.slot('admin:role-editor', () => {}); } }),
			definePlugin({ name: 'alpha', setup: (host) => { host.slot('admin:role-editor', () => {}); } }),
		]);
		expect(slotMounts('admin:role-editor').map((m) => m.plugin)).toEqual(['alpha', 'zeta']);
	});
});
```

- [ ] **Step 2: Run the test and confirm RED**

Run in `E:\tmp\opencode\mk-can-delete-account\third_party\misskey\packages\frontend`:

```powershell
npx vitest --run --globals --config vitest.config.unit.ts test/unit/plugin-slot-role.test.ts
```

Expected: FAIL。`'admin:role-editor'` が `SlotName` に無いため型エラー、`SlotRole` が存在しないため import が解決しない。**実際のメッセージを記録する**（GREEN Step 5 で同じ内容が消えることを確認する）。

- [ ] **Step 3: Add the slot name, `SlotRole`, and the ctx fields**

In `third_party/misskey/packages/frontend/src/plugin-api.ts`, replace the tail of the `SlotName` union (`| 'admin:user';`) with:

```ts
	| 'admin:user'
	/**
	 * コントロールパネル > ロールの編集画面 (`/admin/roles/:id`)。
	 * 編集中のロールが `ctx.role` に、**保存前の新規ロールでは**
	 * `ctx.readonly` が true で渡る。
	 *
	 * **`readonly` は権限の信号ではない。** ロールがまだ保存できないという
	 * 事実だけ伝える。認可はバックエンドの `Request.IsAdministrator()` がする。
	 */
	| 'admin:role-editor';
```

Add this type right after `SlotUser`（同じ「内部の型をそのまま渡さない」契約）:

```ts
/** Minimal role shape handed to slots. 内部の Role entity をそのまま渡さない。 */
export type SlotRole = {
	/**
	 * 未保存の新規ロールでは null。
	 *
	 * **プラグインは id の無いロールへ level 設定を依頼できない。**
	 * 保存前のロールに紐づくデータも無いので、id が採れるまで
	 * panel を出さないのが正しい。
	 */
	id: string | null;
	name: string;
	/** 付与対象の種別。`conditional` は assignment を持たない。 */
	target: 'manual' | 'conditional';
	/**
	 * native のロール属性。モデレーターがメンバーの XP を操作できるかは
	 * backend の認可が使うので、表示の出し分けに用いない。
	 */
	canEditMembersByModerator: boolean;
};
```

Extend `SlotContext`:

```ts
export type SlotContext = {
	/** プロフィール系のスロットで、表示中のユーザー。 */
	user?: SlotUser;
	/** インスタンス系のスロットで、表示中のホスト。 */
	host?: string;
	/** `admin:role-editor` で、編集中のロール。 */
	role?: SlotRole;
	/**
	 * `admin:role-editor` で、ロールをまだ保存できない状態か。
	 *
	 * 権限の判定ではない。認可はバックエンドがする。
	 */
	readonly?: boolean;
};
```

- [ ] **Step 4: Mount the slot in the role editor**

In `third_party/misskey/packages/frontend/src/pages/admin/roles.editor.vue`, add the import next to the other component imports:

```ts
import MkPluginSlot from '@/components/MkPluginSlot.vue';
```

Add this at the end of the `<template>` block, after the policies `FormSlot`:

```vue
	<!--
		プラグインが level 設定を公開 UI に差し込むためのスロット。

		**native の role editor に Misaki 固有の component を埋め込まない。**
		公開 Plugin API の追加だけで済むので、プラグイン側は
		`@/plugin-api.js` だけを前提にできる。

		`MkPluginSlot` の root は `display: contents` なので自分の箱を持たない。
		そのため gap を持つ親の直下に置く（`pages/admin-user.vue:135-148` の
		コメントと同じ理屈）。ここでは親の `<div class="_gaps">` の直下が該当。
	-->
	<MkPluginSlot
		name="admin:role-editor"
		:ctx="{ role: { id: role.id ?? null, name: role.name, target: role.target, canEditMembersByModerator: role.canEditMembersByModerator }, readonly: readonly === true }"
	/>
```

- [ ] **Step 5: Run the fork verification and confirm GREEN**

Run in `E:\tmp\opencode\mk-can-delete-account\third_party\misskey\packages\frontend`:

```powershell
npx vitest --run --globals --config vitest.config.unit.ts test/unit/plugin-slot-role.test.ts
npx vue-tsc --noEmit
npm run --silent eslint
```

Expected: 3 コマンドとも PASS。`vue-tsc` は `test/unit/**` も見るので、**形が vue-tsc に効くことを 1 度だけ実測する**: `consume` の `r.canEditMembersByModerator` を削除して `npx vue-tsc --noEmit` が赤になるのを確認し、すぐ戻す。緑のままなら Step 1 の `consume` が type 制約になっていない（テストの書き方が型制約になっていない）。

- [ ] **Step 6: Commit and open the upstreamable fork PR**

Run in `E:\tmp\opencode\mk-can-delete-account\third_party\misskey`:

```powershell
git add packages/frontend/src/plugin-api.ts packages/frontend/src/pages/admin/roles.editor.vue packages/frontend/test/unit/plugin-slot-role.test.ts
git commit -m "Add plugins: expose the admin role editor slot"
git push -u origin feature/role-editor-slot
gh pr create --repo Misaki-Project/misskey-ts --base mk-2026.9.1 --head feature/role-editor-slot --title "Add plugins: expose the admin role editor slot" --body "Tracking issue: https://github.com/Misaki-Project/mk/issues/12`n`nAdds the generic admin:role-editor slot so a server plugin can contribute role-scoped configuration to the native role editor without the host embedding plugin components.`n`n## Additive only`n- new SlotName member admin:role-editor`n- new exported type SlotRole`n- two optional SlotContext fields (role / readonly)`n- no change to the re-exported component list, no change to existing slots`n`nreadonly means the role has not been saved yet. It is deliberately not an authorization signal; the backend still decides with Request.IsAdministrator()."
```

Expected: PR only in `Misaki-Project/misskey-ts`, base `mk-2026.9.1`。この PR は必須で、**upstream PR を待たない**（upstream PR は Task 15 で、許可と process が許るときだけ別に作る）。Misaki 固有の level 計算・storage・route・UI・plugin 名をこの PR に入れていないこと（upstream へ forwarding できる形のまま保つ）。Task 15 の upstream PR は**この PR と別の clone から**作るので、ここで upstream へ push しない。
### Task 3: Plugin Frontend Test Discovery In The Fork

**Files:**
- Modify: `third_party/misskey/packages/frontend/vitest.config.unit.ts:1-21`

**Interfaces:**
- Produces: `make rolelevel-plugin-test` が fork の vitest 1 本で plugin の unit test を走らせる経路
- Preserves: submodule 単体（= fork だけの checkout）では include を足らない = 検査対象 0 のまま緑

- [ ] **Step 1: Record the pre-change behaviour**

Run in `E:\tmp\opencode\mk-can-delete-account\third_party\misskey\packages\frontend`:

```powershell
npx vitest --run --globals --config vitest.config.unit.ts --reporter=json --outputFile=vitest-before.json
```

Expected: PASS。`vitest-before.json` の test file 一覧を記録する（後の GREEN と比べるため）。**この時点では plugin の test file が無い**ので、Changing 後の差分は Task 5 で初めて現れる。

- [ ] **Step 2: Add the conditional include**

Replace `third_party/misskey/packages/frontend/vitest.config.unit.ts` with:

```ts
import { existsSync } from 'node:fs';
import path from 'node:path';
import { defineConfig, mergeConfig } from 'vitest/config';
import { getConfig } from './vite.config.js';

/*
 * mk-go サーバープラグインの unit test (#2480)。
 *
 * `plugins/` は submodule の**外**（mk リポジトリのルート下）にあるので、
 * 既定の include (`./test/unit/**\/*.test.ts`) には入らない。生成物を
 * 条件に足すのは `vite.config.ts` の `loadMkPlugins()` と同じ方針で、
 * **submodule 単体（upstream 追従用 checkout）には生成物が無く、include も
 * 足らない** = 検査対象は 0 のまま緑になる。
 */
const mkPluginTestInclude = existsSync(path.resolve(__dirname, 'mk-plugins.generated.json'))
	? ['../../../../plugins/*/frontend/**/*.test.ts']
	: [];

export default mergeConfig(getConfig(), defineConfig({
	test: {
		include: ['./test/unit/**/*.test.ts', ...mkPluginTestInclude],
		environment: 'happy-dom',
		setupFiles: ['./test/setup.unit.ts'],
		deps: {
			optimizer: {
				web: {
					include: [
						// XXX: misskey-dev/browser-image-resizer has no "type": "module"
						'browser-image-resizer',
					],
				},
			},
		},
		includeSource: ['src/**/*.ts'],
	},
}));
```

- [ ] **Step 3: Add the explicit `@mkplugin/role-level` path mapping**

`packages/frontend/tsconfig.json` の `paths` には `"@mkplugin/*": ["../../../../plugins/*/frontend"]` がある。**ワイルドカードは manifest 名をディレクトリ名へ引けない。** manifest の `name` は `role-level`（ハイフン入り）だがディレクトリは `plugins/rolelevel`（ハイフン無し）なので、`@mkplugin/role-level` は `plugins/role-level/frontend` を指して型解決に失敗する。生成 alias（`tools/pluginbuild`）は alias 名と実パスを別々に持つので動くが、**型解決は静かに壊れる**。

In `third_party/misskey/packages/frontend/tsconfig.json`, add the explicit entry **before** the wildcard so it wins:

```jsonc
		"paths": {
			// **明示エントリが先。** ワイルドカードの `@mkplugin/*` は manifest 名
			// (`role-level`) をディレクトリ名 (`rolelevel`) へ引けないので、ここが無いと
			// `server-plugins.generated.ts` の `import p0 from '@mkplugin/role-level'`
			// が `plugins/role-level/frontend` を指して vue-tsc が落ちる。生成 alias は
			// alias 名と実パスを別々に持つので動くが、型解決は静かに壊れる。
			"@mkplugin/role-level": ["../../../../plugins/rolelevel/frontend/index.ts"],
			"@/*": ["./src/*"],
			"@@/*": ["../frontend-shared/*"],
			"@mkplugin/*": ["../../../../plugins/*/frontend"]
		},
```

**上のブロックは説明のコメント込み。実際の `tsconfig.json` は JSON なのでコメントを書けない**ので、コメント無しの形を倒入する:

```json
		"paths": {
			"@mkplugin/role-level": ["../../../../plugins/rolelevel/frontend/index.ts"],
			"@/*": ["./src/*"],
			"@@/*": ["../frontend-shared/*"],
			"@mkplugin/*": ["../../../../plugins/*/frontend"]
		},
```

manifest name にハイフンが無い場合はこの明示エントリは不要で、この PR の差分は `vitest.config.unit.ts` の 1 ファイルだけになる。**1 プラグインにつき 1 行増える運用**なので、backend plan と合意してハイフン無しの manifest 名（ディレクトリ `rolelevel` と同じ形）を選ぶならその方が安い。

- [ ] **Step 4: Confirm the fork's own suite is unaffected**

Run in `E:\tmp\opencode\mk-can-delete-account\third_party\misskey\packages\frontend`:

```powershell
npx vitest --run --globals --config vitest.config.unit.ts --reporter=json --outputFile=vitest-after.json
git status --short
```

Expected: PASS で、Step 1 の `vitest-before.json` と test file 一覧が一致する（submodule 単体には `mk-plugins.generated.json` が無いので include は空）。`git status` に `vitest-*.json` が残るので **削除する**:

```powershell
Remove-Item vitest-before.json, vitest-after.json
```

- [ ] **Step 5: Commit and open the second fork PR**

この変更は **`plugins/` を知っているので mk 固有**で、Task 2 の PR とは別にする（upstream 化的 PR を汚さないため）。Run in `E:\tmp\opencode\mk-can-delete-account\third_party\misskey`:

```powershell
git add packages/frontend/vitest.config.unit.ts packages/frontend/tsconfig.json
git diff --cached --stat
git commit -m "Add test: run bundled plugin frontend unit tests"
git push
gh pr create --repo Misaki-Project/misskey-ts --base feature/role-editor-slot --head feature/role-editor-slot --title "Add test: run bundled plugin frontend unit tests" --body "mk-go specific (it knows about the plugins/ directory next to the submodule and the manifest name), so this is not part of the upstreamable slot PR.`n`nAdds plugins/*/frontend/**/*.test.ts to the vitest include, gated on the existence of mk-plugins.generated.json so a submodule-only checkout collects nothing (same pattern as loadMkPlugins() in vite.config.ts), plus the explicit @mkplugin/role-level path mapping the wildcard cannot express."
```

`git diff --cached --stat` は 2 ファイルだけであること。`server-plugins.generated.ts` は submodule 側で tracked なので、Task 4 の checkout で dirty になっていても**ここでは add しない**（`Makefile:140-142` の `SUBMODULE_GENERATED` と同じ扱い）。

Expected: `Misaki-Project/misskey-ts` に 2 本目の PR（base は Task 2 のブランチ）。Task 12 で 2 本とも merge する。

### Task 4: Point The Mk Submodule At The Slot Branch (Working Tree Only)

**Files:**
- Modify (working tree only, **not staged**): `third_party/misskey` の checkout 先

**Interfaces:**
- Produces: `make rolelevel-frontend-check` / `make frontend-check` が `admin:role-editor` Aware に動く
- Preserves: `git ls-files -s -- third_party/misskey`（index）が `1a53308b` のまま = Task 13 まで pin gate が緑

- [ ] **Step 1: Check out the slot branch commit in the submodule**

Run in `E:\tmp\opencode\mk-can-delete-account\third_party\misskey`:

```powershell
git fetch origin feature/role-editor-slot
git checkout feature/role-editor-slot
git rev-parse HEAD
git log --oneline -3
```

Expected: HEAD が Task 2 Step 6 の commit。**`git add` しない。** index の gitlink を上げると `TestSubmodulePinMatchesDoc` が「doc の pin 行と gitlink が食い違う」で落ちる（doc を直すのは Task 13）。

- [ ] **Step 2: Prove the pin gates are still green with a moved submodule**

Run in `E:\tmp\opencode\mk-can-delete-account`:

```powershell
go test ./internal/entitycompat -run "TestSubmodulePinMatchesDoc|TestSubmodulePinTagMatchesTable|TestBundledAssetsPinMatchesDoc" -count=1
git status --short
```

Expected: PASS。`git status` に `third_party/misskey` の変更（`M` / `+`）が出るだけで、**どの gate も落ちない**。この状態を Tasks 5-11 の間ずっと保つ。`git ls-files -s` は index を読む実装だから（`internal/entitycompat/submodule_pin_test.go:54-61`）、working tree の submodule を動かしても判定に影響しない。

- [ ] **Step 3: Record why the plugin frontend cannot live in the fork**

Run in `E:\tmp\opencode\mk-can-delete-account`:

```powershell
Select-String -Path tools/pluginbuild/main.go -Pattern "frontendAliasPrefix|frontendSrcRelToFront|frontendGeneratedTS"
Select-String -Path third_party/misskey/packages/frontend/tsconfig.json -Pattern "@mkplugin"
Select-String -Path third_party/misskey/packages/frontend/vite.config.ts -Pattern "mk-plugins.generated.json"
```

Expected: alias は **manifest の `name`** から作られるので `@mkplugin/role-level` → `<mk ルート>/plugins/rolelevel/frontend/index.ts`（`tools/pluginbuild/main.go:245-252` が alias 名に `frontendAliasPrefix + p.name`、実パスにはディレクトリを相対パス化したものを入れる）、`tsconfig.json` は `@mkplugin/role-level` の明示エントリ（Task 3 Step 3）とワイルドカード `@mkplugin/*` → `../../../../plugins/*/frontend` の両方、`vite.config.ts` は同じ形の manifest を読む。**`plugins/` の実体が mk リポジトリの直下であることを fork 側が前提にしている**ので、roleLevel の component を fork に置くと `make plugins` が生成する alias が解決先を持たない（Task 5 の `rolelevel-frontend-check` が RED になる形で検出される）。この 3 行の結果を PR 説明に引用する。

**manifest の `name` とディレクトリ名は別の値。** `name: role-level` / ディレクトリ `rolelevel` は意図的に別のままにする。`name` は API 名前空間と生成 alias に、ディレクトリ名は `plugins/*` の走査と `go.mod` の位置に使われる。その副作用として、fork の tsconfig には明示エントリが要る（Task 3 Step 3）。

### Task 5: Plugin Frontend Client Layer, Unit Tests, And Make/CI Wiring

**Files:**
- Modify: `.gitignore:81-92`
- Create: `plugins/rolelevel/frontend/types.ts`
- Create: `plugins/rolelevel/frontend/errors.ts`
- Create: `plugins/rolelevel/frontend/api.ts`
- Create: `plugins/rolelevel/frontend/locales.ts`
- Create: `plugins/rolelevel/frontend/policy-keys.ts`
- Create: `plugins/rolelevel/frontend/xp.ts`
- Create: `plugins/rolelevel/frontend/tsconfig.json`
- Create: `plugins/rolelevel/frontend/test/xp.test.ts`
- Create: `plugins/rolelevel/frontend/test/locales.test.ts`
- Create: `plugins/rolelevel/frontend/test/errors.test.ts`
- Modify: `Makefile:1-2,83-89,278-284,673-682`
- Modify: `.github/workflows/ci.yml:550-552`

**Interfaces:**
- Produces: `errorCodeOf(err: unknown): RoleLevelErrorCode | null`
- Produces: `errorMessage(locale: Locale, err: unknown): string`
- Produces: `previewExp(mode: ExpMode, current: number, operand: number): number`
- Produces: `formatExp(value: number, locale: string): string`
- Produces: `progressPercent(currentLevelExp: number, nextLevelExp: number | null): number`
- Produces: `clampSafeInt(n: number): number`
- Produces: `resolveLocale(lang: string | null | undefined): Locale` / `t(locale: Locale, key: MessageKey): string`
- Produces: locale key `userMultiplierHint` / `policyMultiplierValue` / `curveExponentialBase`（倍率と指数の底が「値」ではないことを UI に出するための文言）
- Produces: `pluginApi<T>(path, params)`（`plugin/role-level/` prefix、11 個の plugin POST route）and `nativeApi<T>(endpoint, params)`（native の `admin/roles/assign` / `admin/roles/unassign`）
- Produces: `POLICY_KEYS: readonly PolicyKeySpec[]`（Task 10 が読む）
- Produces: `make rolelevel-frontend-check` / `make rolelevel-plugin-test`

- [ ] **Step 1: Un-ignore the plugin directory**

In `.gitignore`, add after the `!plugins/trustlevel/` block:

```gitignore
# roleLevel プラグインも同梱する (#12)。**この plan が置くのは frontend だけで**、
# backend (`mk-plugin.yml` / `go.mod` / `plugin.go`) は backend plan の所有。
# `plugins/*` を走査する pluginbuild は `mk-plugin.yml` の無いディレクトリを
# 読み飛ばすので (tools/pluginbuild/main.go:320-328)、front だけ置いても
# `make plugins` / `make build` は壊れない。
!plugins/rolelevel/
```

- [ ] **Step 2: Write the failing unit tests**

Create `plugins/rolelevel/frontend/test/xp.test.ts`:

```
/*
 * SPDX-FileCopyrightText: mk-go project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import { describe, expect, test } from 'vitest';
import { clampSafeInt, formatExp, previewExp, progressPercent } from '../xp.js';

/*
 * `previewExp` は **backend の契約の写し**であって、frontend の計算では
 * ない。spec の XP Mutation 節（floor して 0..MAX_SAFE_INTEGER へ clamp、
 * multiplier の operand は有限な倍率）と 1 文字でもずれると「送信した値と
 * 保存された値が違う」状態になるので、数値を固定する。
 */
describe('previewExp', () => {
	test('set は operand をそのまま使う', () => {
		expect(previewExp('set', 999, 250)).toBe(250);
		expect(previewExp('set', 0, 0)).toBe(0);
	});

	test('add は現在値に足してから 0 へ clamp する', () => {
		expect(previewExp('add', 100, 50)).toBe(150);
		expect(previewExp('add', 100, -150)).toBe(0);
		expect(previewExp('add', 0, -1)).toBe(0);
	});

	test('multiplier は有限な倍率を掛ける（百分率ではない）', () => {
		expect(previewExp('multiplier', 200, 1.5)).toBe(300);
		expect(previewExp('multiplier', 200, 1)).toBe(200);
		expect(previewExp('multiplier', 200, 0.5)).toBe(100);
		expect(previewExp('multiplier', 200, 0)).toBe(0);
		expect(previewExp('multiplier', 0, 1.5)).toBe(0);
	});

	test('multiplier の結果は floor される', () => {
		expect(previewExp('multiplier', 101, 1.5)).toBe(151); // 151.5 -> 151
		expect(previewExp('multiplier', 3, 0.5)).toBe(1); // 1.5 -> 1
	});

	test('multiplier に負の倍率と非有限値を渡しても壊れない', () => {
		expect(previewExp('multiplier', 200, -1)).toBe(0);
		expect(previewExp('multiplier', 200, Number.NaN)).toBe(0);
		expect(previewExp('multiplier', 200, Number.POSITIVE_INFINITY)).toBe(0);
		expect(previewExp('multiplier', 200, Number.NEGATIVE_INFINITY)).toBe(0);
	});

	test('MAX_SAFE_INTEGER を超える値は頭打ちになる', () => {
		expect(previewExp('add', Number.MAX_SAFE_INTEGER, 1)).toBe(Number.MAX_SAFE_INTEGER);
		expect(previewExp('multiplier', Number.MAX_SAFE_INTEGER, 1.5)).toBe(Number.MAX_SAFE_INTEGER);
	});

	test('返り値は必ず safe integer の number（文字列にしない）', () => {
		for (const got of [
			previewExp('set', 0, 123.9),
			previewExp('add', 10, 20),
			previewExp('multiplier', 200, 1.5),
		]) {
			expect(typeof got).toBe('number');
			expect(Number.isSafeInteger(got)).toBe(true);
		}
	});
});

describe('clampSafeInt', () => {
	test.each([
		[-1, 0],
		[0, 0],
		[1.5, 1],
		[-1.5, 0],
		[Number.MAX_SAFE_INTEGER + 10, Number.MAX_SAFE_INTEGER],
		[Number.NaN, 0],
	])('%s -> %s', (input, expected) => {
		expect(clampSafeInt(input)).toBe(expected);
	});
});

describe('formatExp', () => {
	test('3 桁区切りにする', () => {
		expect(formatExp(0, 'en-US')).toBe('0');
		expect(formatExp(1234567, 'en-US')).toBe('1,234,567');
	});

	test('NaN や Infinity は 0 として表示する', () => {
		expect(formatExp(Number.NaN, 'en-US')).toBe('0');
		expect(formatExp(Number.POSITIVE_INFINITY, 'en-US')).toBe('0');
	});
});

describe('progressPercent', () => {
	test('現在 XP を次 level の必要 XP で割る', () => {
		expect(progressPercent(0, 100)).toBe(0);
		expect(progressPercent(50, 100)).toBe(50);
		expect(progressPercent(100, 100)).toBe(100);
	});

	test('最大 level (nextLevelExp が null) は 100', () => {
		expect(progressPercent(12345, null)).toBe(100);
	});

	test('0 を割るときは 0 を返す', () => {
		expect(progressPercent(0, 0)).toBe(0);
	});

	test('端数は切り上げて整数にする', () => {
		expect(progressPercent(1, 3)).toBe(34);
	});
});
```

Create `plugins/rolelevel/frontend/test/locales.test.ts`:

```ts
/*
 * SPDX-FileCopyrightText: mk-go project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import { describe, expect, test } from 'vitest';
import { MESSAGES, resolveLocale, t } from '../locales.js';

/*
 * locale の gate。**キーの足し忘れが「en のときだけ空欄」になる**ので、
 * 実行時に 1 つずつ塞ぐ。
 */
describe('locales', () => {
	test('ja と en の key 集合が一致する', () => {
		expect(Object.keys(MESSAGES.en).sort()).toEqual(Object.keys(MESSAGES.ja).sort());
	});

	test('ja-JP は ja、en-US とそれ以外は en', () => {
		expect(resolveLocale('ja-JP')).toBe('ja');
		expect(resolveLocale('ja')).toBe('ja');
		expect(resolveLocale('en-US')).toBe('en');
		expect(resolveLocale('zh-CN')).toBe('en');
		expect(resolveLocale(undefined)).toBe('en');
		expect(resolveLocale(null)).toBe('en');
	});

	test('文言が空の key を作らない', () => {
		for (const [locale, table] of Object.entries(MESSAGES)) {
			for (const [key, value] of Object.entries(table)) {
				expect(value.length, `${locale}.${key} が空`).toBeGreaterThan(0);
			}
		}
	});

	test('t は未知の locale でも文言を返す', () => {
		// 実行時に壊れた locale 名が流れても画面が真っ白にならないこと。
		expect(t('zz' as never, 'save')).toBe(MESSAGES.ja.save);
	});
});
```

Create `plugins/rolelevel/frontend/test/errors.test.ts`:

```ts
/*
 * SPDX-FileCopyrightText: mk-go project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import { describe, expect, test } from 'vitest';
import { errorCodeOf, errorMessage } from '../errors.js';
import { MESSAGES } from '../locales.js';

describe('errorCodeOf', () => {
	test('backend の stable code を取り出す', () => {
		expect(errorCodeOf({ code: 'ROLE_LEVEL_VALIDATION_FAILED' })).toBe('ROLE_LEVEL_VALIDATION_FAILED');
		expect(errorCodeOf({ code: 'ROLE_LEVEL_FORBIDDEN' })).toBe('ROLE_LEVEL_FORBIDDEN');
	});

	test('未知の code / code 無し / 非 Error は null', () => {
		// **未知を握り潰さない。** 未知の code を「既知」として返すと
		// 403 以外を全部「権限不足」として扱うことになる。
		expect(errorCodeOf({ code: 'SOMETHING_ELSE' })).toBeNull();
		expect(errorCodeOf({ message: '権限がありません' })).toBeNull();
		expect(errorCodeOf(null)).toBeNull();
		expect(errorCodeOf(undefined)).toBeNull();
		expect(errorCodeOf('ROLE_LEVEL_FORBIDDEN')).toBeNull();
	});
});

describe('errorMessage', () => {
	test('code ごとに文言を当てる', () => {
		expect(errorMessage('ja', { code: 'ROLE_LEVEL_FORBIDDEN' })).toBe(MESSAGES.ja.errorForbidden);
		expect(errorMessage('en', { code: 'ROLE_LEVEL_VALIDATION_FAILED' })).toBe(MESSAGES.en.errorValidation);
		expect(errorMessage('en', { code: 'ROLE_LEVEL_REVISION_CONFLICT' })).toBe(MESSAGES.en.errorConflict);
		expect(errorMessage('en', { code: 'ROLE_LEVEL_NATIVE_FAILED' })).toBe(MESSAGES.en.errorNative);
		expect(errorMessage('en', { code: 'ROLE_LEVEL_STORAGE_FAILED' })).toBe(MESSAGES.en.errorStorage);
	});

	test('未知の code は汎用の文言へ落とす', () => {
		expect(errorMessage('en', { code: 'WAT' })).toBe(MESSAGES.en.errorGeneric);
		expect(errorMessage('en', new Error('boom'))).toBe(MESSAGES.en.errorGeneric);
	});
});
```

- [ ] **Step 3: Run the unit tests and confirm RED**

Run in `E:\tmp\opencode\mk-can-delete-account`:

```powershell
make plugins
cd third_party/misskey/packages/frontend
npx vitest --run --globals --config vitest.config.unit.ts rolelevel
```

Expected: FAIL — `Failed to load url ../xp.js`（module が無い）。**Task 3 の include が効いている証拠でもある**（include が動いていなければ「No test files found」になる）。

- [ ] **Step 4: Write `types.ts`**

Create `plugins/rolelevel/frontend/types.ts`:

```ts
/*
 * SPDX-FileCopyrightText: mk-go project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

/*
 * backend と共有する wire 上の型。
 *
 * **plugin storage の内部表現ではない。** spec の Plugin Routes /
 * Experience Result / Level-Based Policies / XP Mutation の節に対応する形だけを
 * ここに置く。差分は Task 10 の drift gate と e2e が検出する。
 *
 * import を持たないので unit test から直接読める。
 */

/**
 * XP 操作の mode。`operand` は**すべて JSON number**（XP は safe integer）。
 *
 * - `set`: operand をそのまま XP にする
 * - `add`: current + operand
 * - `multiplier`: **有限な倍率**。`1.5` = ×1.5、`0.5` = 半分。百分率ではない
 *
 * 3 つとも結果を floor して 0..MAX_SAFE_INTEGER に収める（spec XP Mutation）。
 */
export type ExpMode = 'set' | 'add' | 'multiplier';

export type CurveSegmentType = 'const' | 'linear' | 'exponential';

export type PolicyRangeType = 'base' | 'const' | 'multiplier';

export type PolicyValue = boolean | number | string;

export type CurveSegment = {
	type: CurveSegmentType;
	/** 1 回の level-up に必要な XP の基準。整数。 */
	base: number;
	/**
	 * `const` / `linear` では整数（XP）、`exponential` では**有限な底**（`1.05` で
	 * 5% ずつ増える）。いずれの場合も JSON number。
	 */
	additional: number;
	/** この segment 内の level-up 回数。1 以上の safe integer。 */
	levelUps: number;
};

export type PolicyRange = {
	/** level 設定の対象にする native policy key（`POLICY_KEYS` にあるもの）。 */
	key: string;
	/** progressionStage の開始 stage（1 始まり・この stage を含む）。 */
	startStage: number;
	type: PolicyRangeType;
	/** `const` のときの値。`base` / `multiplier` では null。 */
	value: PolicyValue | null;
	/**
	 * `multiplier` の追加項。`base + additional * offset` の additional。
	 * 有限な倍率なので小数可（`1.05`）。offset は開始 stage からの 0 始まりなので
	 * `baseLevel` の値に影響されない（spec Level-Based Policies）。
	 */
	additional: number;
};

/** PUT で送る level 設定。`revision` は楽観 locking に使う。 */
export type RoleLevelDraft = {
	baseLevel: number;
	curve: CurveSegment[];
	policyRanges: PolicyRange[];
	revision: number;
};

export type RoleLevelConfig = RoleLevelDraft & {
	roleId: string;
	updatedAt: string;
	updatedBy: string;
};

export type ExperienceResult = {
	currentLevel: number;
	currentLevelExp: number;
	nextLevelExp: number | null;
	totalExp: number;
	minLevel: number;
	maxLevel: number;
	progressionStage: number;
};

export type LevelRoleOption = {
	roleId: string;
	roleName: string;
	canEditMembersByModerator: boolean;
};

export type UserLevelRow = {
	roleId: string;
	roleName: string;
	/** 未 assignment なら null（XP 操作の対象は assignment）。 */
	assignmentId: string | null;
	experience: number;
	level: ExperienceResult | null;
	/** backend が判定した「この XP を編集できるか」。表示の出し分けに使う。 */
	canEdit: boolean;
};

export type AdminUserLevels = {
	userId: string;
	rows: UserLevelRow[];
	unassignedRoles: LevelRoleOption[];
};

export type ChangeExpResult = {
	assignmentId: string;
	experience: number;
	level: ExperienceResult;
};

export type MemberXpRow = {
	assignmentId: string;
	userId: string;
	username: string;
	host: string | null;
	experience: number;
	level: ExperienceResult;
};

export type MemberPage = {
	items: MemberXpRow[];
	nextCursor: string | null;
};

export type OrphanRow = {
	kind: 'config' | 'experience';
	roleId: string;
	assignmentId: string | null;
	detail: string;
};

export type ReconcileStatus = {
	lastRunAt: string | null;
	pendingOperations: number;
	failedOperations: number;
	orphanConfig: number;
	orphanExperience: number;
	lastError: string | null;
};

export type AuditEntry = {
	id: string;
	actorId: string;
	operation: string;
	roleId: string | null;
	assignmentId: string | null;
	beforeExperience: number | null;
	afterExperience: number | null;
	note: string | null;
	createdAt: string;
};

/**
 * backend が `plugin.NewCodedStatusError` で返す stable code。
 *
 * **403 判定に HTTP status は使えない。** プラグインの error body は
 * `{"error":{"message":..,"code":..}}` で status が入らない
 * （`internal/server/plugin_wiring.go:735`）。だから「権限がない」は
 * code でしか判別できない。
 */
export const ROLE_LEVEL_ERROR_CODES = [
	'ROLE_LEVEL_VALIDATION_FAILED',
	'ROLE_LEVEL_FORBIDDEN',
	'ROLE_LEVEL_NATIVE_FAILED',
	'ROLE_LEVEL_STORAGE_FAILED',
	'ROLE_LEVEL_REVISION_CONFLICT',
	'ROLE_LEVEL_NOT_CONFIGURED',
	'ROLE_LEVEL_ASSIGNMENT_MISSING',
] as const;

export type RoleLevelErrorCode = (typeof ROLE_LEVEL_ERROR_CODES)[number];
```
- [ ] **Step 5: Write `errors.ts`**

Create `plugins/rolelevel/frontend/errors.ts`:

```ts
/*
 * SPDX-FileCopyrightText: mk-go project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import { t } from './locales.js';
import type { Locale } from './locales.js';
import { ROLE_LEVEL_ERROR_CODES } from './types.js';
import type { RoleLevelErrorCode } from './types.js';

/**
 * プラグイン error body の `code` を取り出す。
 *
 * **未知の code は null にする。** 握り潰すと「権限不足以外の error」を
 * 全部権限不足として扱うことになる（描かない方向的 silence になる）。
 */
export function errorCodeOf(err: unknown): RoleLevelErrorCode | null {
	const code = (err as { code?: unknown } | null | undefined)?.code;
	if (typeof code !== 'string') return null;
	return (ROLE_LEVEL_ERROR_CODES as readonly string[]).includes(code)
		? (code as RoleLevelErrorCode)
		: null;
}

/** code を文言にする。未知は汎用。** 例外は投げない**（render 中に投げない）。 */
export function errorMessage(locale: Locale, err: unknown): string {
	switch (errorCodeOf(err)) {
		case 'ROLE_LEVEL_FORBIDDEN': return t(locale, 'errorForbidden');
		case 'ROLE_LEVEL_VALIDATION_FAILED': return t(locale, 'errorValidation');
		case 'ROLE_LEVEL_REVISION_CONFLICT': return t(locale, 'errorConflict');
		case 'ROLE_LEVEL_NATIVE_FAILED': return t(locale, 'errorNative');
		case 'ROLE_LEVEL_STORAGE_FAILED': return t(locale, 'errorStorage');
		default: return t(locale, 'errorGeneric');
	}
}

/**
 * 「権限がないので出さない」判定。
 *
 * **403 だけ hide する。** 他の error は理由を出す（読み込み失敗と権限不足で
 * 対応が変わる）。authoring.md の「管理向けのスロット」の節と同じ判断。
 */
export function isForbidden(err: unknown): boolean {
	return errorCodeOf(err) === 'ROLE_LEVEL_FORBIDDEN';
}
```

- [ ] **Step 6: Write `xp.ts`**

Create `plugins/rolelevel/frontend/xp.ts`:

```
/*
 * SPDX-FileCopyrightText: mk-go project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import type { ExpMode } from './types.js';

/*
 * 数値の preview と表示だけを持つ。**level の計算はしない。**
 *
 * curve / policy range の評価は backend だけが持ち、frontend で同じ式を書くと
 * 片側だけが更新される形になる。preview は operator が操作前に見る値なので、
 * backend と 1 文字でもずれたら Task 5 の unit test で落ちる。
 *
 * **XP は safe integer の JSON number。** 文字列は使わない（`formatExp` は表示
 * 専用で、送る値には通さない）。
 */

export const EXP_MODES: readonly ExpMode[] = ['set', 'add', 'multiplier'];

/** 0..MAX_SAFE_INTEGER へ丸める。NaN / Infinity は 0。 */
export function clampSafeInt(n: number): number {
	if (!Number.isFinite(n)) return 0;
	if (n <= 0) return 0;
	if (n >= Number.MAX_SAFE_INTEGER) return Number.MAX_SAFE_INTEGER;
	return Math.floor(n);
}

/**
 * XP 操作の preview。backend と同じ契約:
 * - set: operand
 * - add: current + operand
 * - multiplier: **有限な倍率**を掛ける（`1.5` = ×1.5。百分率ではない）
 *
 * 結果は 0..MAX_SAFE_INTEGER へ floor して clamp する。
 */
export function previewExp(mode: ExpMode, current: number, operand: number): number {
	if (!Number.isFinite(operand)) return 0;
	switch (mode) {
		case 'set':
			return clampSafeInt(operand);
		case 'add':
			return clampSafeInt(current + operand);
		case 'multiplier':
			return clampSafeInt(current * operand);
	}
}

/** 3 桁区切り。NaN / Infinity は 0 相当（JSON に NaN を出さない）。 */
export function formatExp(value: number, locale: string): string {
	return new Intl.NumberFormat(locale).format(clampSafeInt(value));
}

/**
 * 次の level までの進捗（0..100 の整数）。
 *
 * `nextLevelExp` が null は最大 level 到達済み = 100。spec の「最大 level
 * 到達後の余剰 XP は currentLevelExp に保持する」に対応する。
 */
export function progressPercent(currentLevelExp: number, nextLevelExp: number | null): number {
	if (nextLevelExp == null) return 100;
	if (!Number.isFinite(nextLevelExp) || nextLevelExp <= 0) return 0;
	const ratio = currentLevelExp / nextLevelExp;
	if (!Number.isFinite(ratio) || ratio <= 0) return 0;
	return Math.min(100, Math.max(0, Math.ceil(ratio * 100)));
}
```

- [ ] **Step 7: Write `locales.ts`**

Create `plugins/rolelevel/frontend/locales.ts`:

```ts
/*
 * SPDX-FileCopyrightText: mk-go project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

/*
 * プラグイン内の文言。
 *
 * **`@/i18n.js` は import できない** — 公開 Plugin API は
 * `plugin-api.ts` の再公開物と型だけで、i18n はその中に無い
 * （`docs/plugins/authoring.md` の「使えるもの」節）。`plugins/trustlevel` が
 * 日本語直書きで済ませていたのと同じ制約なので、fallback 付きの最小連想配列で
 * 持つ。
 */
export type Locale = 'ja' | 'en';

const ja = {
	roleEditorTitle: 'レベル設定',
	roleEditorHint: 'このロールのメンバーに level と XP を設定します。level 設定は管理者だけが変更できます。',
	roleEditorUnsaved: '先にロールを保存してください。保存すると level を設定できます。',
	roleEditorConditional: '条件付きロールには XP が付きません（assignment を持たないため）。',
	baseLevel: '基準レベル（XP 0 のとき）',
	curveTitle: 'XP 曲線',
	curveCaption: 'level-up に必要な XP を区間ごとに定めます。区間が変わると 0 に戻ります。',
	curveAdd: '区間を追加',
	curveRemove: 'この区間を削除',
	curveType: '種類',
	curveBase: '1 回の level-up に必要な XP',
	curveAdditional: '追加分',
	curveLevelUps: 'level-up 回数',
	curveTypeConst: '一定',
	curveTypeLinear: '一次関数',
	curveTypeExponential: '指数関数',
	policyTitle: 'レベル別 policy',
	policyHint: '開始 stage は 1 始まりで、重複も抜けもなく並べます。',
	policyAdd: '範囲を追加',
	policyKey: 'policy',
	policyType: '規則',
	policyTypeBase: '既定を使う',
	policyTypeConst: '固定値',
	policyTypeMultiplier: '倍率',
	policyValue: '値',
	policyStartStage: '開始 stage',
	policyRemove: 'この範囲を削除',
	save: '保存',
	removeConfig: 'レベル設定を削除',
	confirmRemove: 'レベル設定を削除しますか。XP の値は残ります。',
	saved: '保存しました',
	notConfigured: 'このロールには level 設定がありません。',
	userTitle: 'レベルと XP',
	userAssign: 'ロールを付与',
	userUnassign: '付与を外す',
	userMode: '操作',
	userModeSet: '設定',
	userModeAdd: '加算',
	userModeMultiplier: '倍率',
	userOperand: '値',
	userMultiplierHint: '倍率は有限な値です（1.5 = ×1.5、0.5 = 半分）。',
	policyMultiplierValue: '倍率（1.5 = ×1.5）',
	curveExponentialBase: '指数の底（1.05 で 5% ずつ）',
	userApply: '適用',
	userCurrent: '現在',
	userPreview: '適用後',
	userNote: 'メモ（監査ログに残ります）',
	userAudit: '監査ログ',
	userRole: 'ロール',
	userLevel: 'レベル',
	userExp: 'XP',
	manageTitle: 'レベルロール',
	manageMembers: 'XP 順のメンバー',
	manageOrphans: '孤立データ',
	manageReconcile: 'reconciliation',
	memberEmpty: '該当するメンバーがいません。',
	memberNext: '次へ',
	memberRank: '順位',
	progressLabel: '次の level までの進捗',
	levelMax: '最大 level',
	loading: '読み込み中',
	errorGeneric: '読み込めませんでした。時間をおいて再度お試しください。',
	errorForbidden: '権限がありません。',
	errorValidation: '入力値が不正です。入力内容を確認してください。',
	errorConflict: '他の更新と競合しました。読み直してから保存してください。',
	errorNative: '本体 API の呼び出しに失敗しました。',
	errorStorage: '保存に失敗しました。',
	auditOperation: '操作',
	auditActor: '実行者',
	auditTime: '時刻',
	auditBefore: '変更前',
	auditAfter: '変更後',
	auditNote: 'メモ',
	reconcilePending: '未完了の操作',
	reconcileFailed: '失敗した操作',
	reconcileOrphanConfig: '孤立した level 設定',
	reconcileOrphanExp: '孤立した XP',
	reconcileLastRun: '最終実行',
	reconcileLastError: '直近のエラー',
	orphanEmpty: '孤立データはありません。',
} as const;

export type MessageKey = keyof typeof ja;

const en: Record<MessageKey, string> = {
	roleEditorTitle: 'Level settings',
	roleEditorHint: 'Give members of this role levels and XP. Only administrators can change level settings.',
	roleEditorUnsaved: 'Save the role first. Level settings become available after it is saved.',
	roleEditorConditional: 'Conditional roles have no XP because they have no assignment.',
	baseLevel: 'Base level (at 0 XP)',
	curveTitle: 'XP curve',
	curveCaption: 'XP required per level-up, per segment. The index restarts at 0 in each segment.',
	curveAdd: 'Add segment',
	curveRemove: 'Remove this segment',
	curveType: 'Type',
	curveBase: 'XP per level-up',
	curveAdditional: 'Additional',
	curveLevelUps: 'Level-ups',
	curveTypeConst: 'Constant',
	curveTypeLinear: 'Linear',
	curveTypeExponential: 'Exponential',
	policyTitle: 'Level-based policies',
	policyHint: 'Start stages begin at 1 with no overlap and no gap.',
	policyAdd: 'Add range',
	policyKey: 'Policy',
	policyType: 'Rule',
	policyTypeBase: 'Use default',
	policyTypeConst: 'Constant',
	policyTypeMultiplier: 'Multiplier',
	policyValue: 'Value',
	policyStartStage: 'Start stage',
	policyRemove: 'Remove this range',
	save: 'Save',
	removeConfig: 'Delete level settings',
	confirmRemove: 'Delete the level settings? XP values are kept.',
	saved: 'Saved',
	notConfigured: 'This role has no level settings.',
	userTitle: 'Levels and XP',
	userAssign: 'Assign role',
	userUnassign: 'Unassign',
	userMode: 'Operation',
	userModeSet: 'Set',
	userModeAdd: 'Add',
	userModeMultiplier: 'Multiplier',
	userOperand: 'Value',
	userMultiplierHint: 'The multiplier is a plain factor (1.5 = x1.5, 0.5 = half).',
	policyMultiplierValue: 'Multiplier (1.5 = x1.5)',
	curveExponentialBase: 'Exponential base (1.05 grows 5% per level-up)',
	userApply: 'Apply',
	userCurrent: 'Current',
	userPreview: 'After',
	userNote: 'Note (kept in the audit log)',
	userAudit: 'Audit log',
	userRole: 'Role',
	userLevel: 'Level',
	userExp: 'XP',
	manageTitle: 'Level roles',
	manageMembers: 'Members by XP',
	manageOrphans: 'Orphan data',
	manageReconcile: 'Reconciliation',
	memberEmpty: 'No members match.',
	memberNext: 'Next',
	memberRank: 'Rank',
	progressLabel: 'Progress to the next level',
	levelMax: 'Max level',
	loading: 'Loading',
	errorGeneric: 'Could not load. Please try again later.',
	errorForbidden: 'You do not have permission.',
	errorValidation: 'The input is invalid. Please check the values.',
	errorConflict: 'Conflicting update. Reload and save again.',
	errorNative: 'The host API call failed.',
	errorStorage: 'Saving failed.',
	auditOperation: 'Operation',
	auditActor: 'Actor',
	auditTime: 'Time',
	auditBefore: 'Before',
	auditAfter: 'After',
	auditNote: 'Note',
	reconcilePending: 'Pending operations',
	reconcileFailed: 'Failed operations',
	reconcileOrphanConfig: 'Orphan level settings',
	reconcileOrphanExp: 'Orphan XP',
	reconcileLastRun: 'Last run',
	reconcileLastError: 'Last error',
	orphanEmpty: 'No orphan data.',
};

export const MESSAGES: Record<Locale, Record<MessageKey, string>> = { ja, en };

/** 日本語を既定にしない。`ja` を含む language tag だけを ja に，其余は en に落とす。 */
export function resolveLocale(lang: string | null | undefined): Locale {
	return (lang ?? '').toLowerCase().startsWith('ja') ? 'ja' : 'en';
}

/** 未翻訳の key は日本語に落とす（キーが新しい状態で en 側だけ足しても壊れない）。 */
export function t(locale: Locale, key: MessageKey): string {
	return MESSAGES[locale]?.[key] ?? MESSAGES.ja[key];
}
```

- [ ] **Step 8: Write `policy-keys.ts`**

Create `plugins/rolelevel/frontend/policy-keys.ts`:

```ts
/*
 * SPDX-FileCopyrightText: mk-go project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

/*
 * level 設定の対象にする native policy。
 *
 * **native policy を全部列挙しない。** 50 個全部を level で変えられるとすると
 * 権限の付け替えになり、誤操作の境界がなくなる。level 運用で実際に使う 8 個を
 * 公開する。列挙に漏れがあることは Task 10 の drift gate が取る（数値 policy は
 * 「ここに無い」か allowlist のどちらかで必ず説明される）。
 */

export type PolicyKind = 'boolean' | 'number' | 'enum';

export type PolicyKeySpec = {
	key: string;
	kind: PolicyKind;
	/** `enum` のときの値。backend の native 型と一致させる。 */
	values?: readonly string[];
	/** level で変えると運営が壊れるものには理由を書く。 */
	note?: string;
};

export const POLICY_KEYS: readonly PolicyKeySpec[] = [
	{ key: 'canCreateChannel', kind: 'boolean' },
	{ key: 'canRequestCustomEmojis', kind: 'boolean' },
	{ key: 'chatAvailability', kind: 'enum', values: ['available', 'unavailable'] },
	{ key: 'driveCapacityMb', kind: 'number' },
	{ key: 'maxFileSizeMb', kind: 'number' },
	{ key: 'noteEachClipsLimit', kind: 'number' },
	{ key: 'userListLimit', kind: 'number' },
	{ key: 'rateLimitFactor', kind: 'number', note: '全体のレート制限に効く。上げると moderation が緩む' },
] as const;

export function policySpecOf(key: string): PolicyKeySpec | null {
	return POLICY_KEYS.find((p) => p.key === key) ?? null;
}
```

- [ ] **Step 9: Write `api.ts`**

Create `plugins/rolelevel/frontend/api.ts`:

```
/*
 * SPDX-FileCopyrightText: mk-go project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import type {
	AdminUserLevels,
	AuditEntry,
	ChangeExpResult,
	ExpMode,
	LevelRoleOption,
	MemberPage,
	OrphanRow,
	ReconcileStatus,
	RoleLevelConfig,
	RoleLevelDraft,
	UserLevelRow,
} from './types.js';

let call: <T>(endpoint: string, params?: Record<string, unknown>) => Promise<T>;

/** プラグインの setup から 1 度だけ呼ばれる。 */
export function initApi(fn: typeof call): void {
	call = fn;
}

/**
 * plugin 自身の route を呼ぶ。`/api/plugin/role-level/<path>`。
 *
 * ** verb は POST だけ。** `host.api` は `misskeyApi` にそのまま渡すので
 * 書き込みしかできない。spec の Plugin Routes 節は GET で書いてあるが、
 * frontend から叩けるのは POST 側だけなので、**この 11 個が frontend が見る
 * 全ての plugin route** になる:
 *
 *   admin/roles/list  admin/roles/show  admin/roles/update  admin/roles/delete
 *   admin/users/show  admin/change-exp  roles/users         users/show
 *   admin/audit       admin/orphans     admin/reconcile
 *
 * 付与 / 解除は入っていない。native を `nativeApi` で呼ぶ。
 */
export function pluginApi<T>(path: string, params: Record<string, unknown> = {}): Promise<T> {
	return call<T>(`plugin/role-level/${path}`, params);
}

/**
 * native の route を呼ぶ（plugin の route ではない）。
 *
 * ** 付与 / 解除はここ。** spec の XP Mutation は「native の assignment を
 * 作ったあと XP を書く」順序で規定しているので、assignment の作成は native の
 * `admin/roles/assign` に任せる。plugin 側で route を作らないことで
 * native の moderator 認可（`canEditMembersByModerator`）をそのまま通る。
 *
 * 返り値は 204 なので `void`（読取は `getUserLevels` を呼ぶ）。
 */
export function nativeApi<T>(endpoint: string, params: Record<string, unknown> = {}): Promise<T> {
	return call<T>(endpoint, params);
}

export function listLevelRoles(): Promise<LevelRoleOption[]> {
	return pluginApi<LevelRoleOption[]>('admin/roles/list');
}

export function getRoleConfig(roleId: string): Promise<RoleLevelConfig | null> {
	return pluginApi<RoleLevelConfig | null>('admin/roles/show', { roleId });
}

export function saveRoleConfig(roleId: string, draft: RoleLevelDraft): Promise<RoleLevelConfig> {
	return pluginApi<RoleLevelConfig>('admin/roles/update', { roleId, ...draft });
}

export function deleteRoleConfig(roleId: string): Promise<void> {
	return pluginApi<void>('admin/roles/delete', { roleId });
}

export function getUserLevels(userId: string): Promise<AdminUserLevels> {
	return pluginApi<AdminUserLevels>('admin/users/show', { userId });
}

/**
 * XP を 1 回操作する。`operand` は **JSON number** で、XP は safe integer に
 * 収める（文字列を送らない）。`multiplier` の `operand` は有限な倍率
 * （`1.5` = ×1.5）で百分率ではない。
 */
export function changeExp(input: {
	userId: string;
	roleId: string;
	mode: ExpMode;
	operand: number;
	/** 同じ操作の再送を backend が 1 回にまとめるためのキー。UI ごとに発行する。 */
	idempotencyKey: string;
	note?: string;
}): Promise<ChangeExpResult> {
	return pluginApi<ChangeExpResult>('admin/change-exp', { ...input });
}

export function assignRole(input: { userId: string; roleId: string; expiresAt: string | null }): Promise<void> {
	return nativeApi<void>('admin/roles/assign', { ...input });
}

export function unassignRole(input: { userId: string; roleId: string }): Promise<void> {
	return nativeApi<void>('admin/roles/unassign', { ...input });
}

export function listAudit(input: { userId: string; limit: number }): Promise<AuditEntry[]> {
	return pluginApi<AuditEntry[]>('admin/audit', { ...input });
}

export function listMembers(input: { roleId: string; cursor: string | null; limit: number }): Promise<MemberPage> {
	return pluginApi<MemberPage>('roles/users', { ...input });
}

export function listOrphans(): Promise<OrphanRow[]> {
	return pluginApi<OrphanRow[]>('admin/orphans');
}

export function getReconcileStatus(): Promise<ReconcileStatus> {
	return pluginApi<ReconcileStatus>('admin/reconcile');
}

/**
 * 公開プロフィール用。** backend が native の可視性を越えないことだけ約束する。**
 * 公開レスポンスに不该有的ものを載せないのは backend の責務（spec Authorization）。
 */
export function getPublicLevels(userId: string): Promise<UserLevelRow[]> {
	return pluginApi<UserLevelRow[]>('users/show', { userId });
}
```

- [ ] **Step 10: Add the plugin tsconfig**

Create `plugins/rolelevel/frontend/tsconfig.json`:

```json
{
	"extends": "../../../third_party/misskey/packages/frontend/tsconfig.json",
	"compilerOptions": {
		"paths": {
			"@/*": ["../../../third_party/misskey/packages/frontend/src/*"],
			"@@/*": ["../../../third_party/misskey/packages/frontend-shared/*"]
		},
		"noEmit": true
	},
	"include": ["./**/*.ts", "./**/*.vue", "./test/**/*.ts"]
}
```

`extends` の相対パスは**宣言した側のファイル基準**で解決するので、`typeRoots` と `lib` は submodule 側の `node_modules` をそのまま使う。`paths` は全上書きしている（`@/*` は submodule の `src` を指す。`@mkplugin/*` は要らない — プラグインは自分を参照しない）。`include` の上書きも必須で、これを省くと submodule の `src/**` を二重に見て **plugin の file が 1 つも含まれない**。

- [ ] **Step 11: Add the Makefile targets**

In `Makefile`, extend the `.PHONY` list (line 1-2) with `rolelevel-frontend-check rolelevel-plugin-test plugins-rolelevel e2e-frontend-build-rolelevel \`.

Add these targets right after the existing `frontend-test` target (after line 88):

```make
# roleLevel プラグインの frontend (#12)。
#
# ** submodule の tsconfig を継承して単独で型を見る。** plugin frontend は
# submodule の `src/**` から import されるまで vue-tsc の include に入らない
# ので、`plugins/rolelevel/mk-plugin.yml`（backend plan の所有）が無い間は
# `make frontend-check` では一度も型検査されない。検査ゼロのまま緑になる
# 状態を避けるための target。
rolelevel-frontend-check: plugins ## roleLevel プラグイン frontend の型検査
	cd third_party/misskey/packages/frontend && npx vue-tsc --noEmit -p ../../../../plugins/rolelevel/frontend/tsconfig.json

# ** fork の package 内で vitest を走らせる。** plugin のディレクトリに
# node_modules は無く、binary と依存解決は submodule 側にある
#（frontend-lint / frontend-test と同じ理屈）。ファイル名の filter に
# `rolelevel` を渡すと、fork 自身の test/unit を回さずに plugin の分だけ実行できる。
rolelevel-plugin-test: plugins ## roleLevel プラグイン frontend の unit test
	cd third_party/misskey/packages/frontend && npx vitest --run --globals --config vitest.config.unit.ts rolelevel

# roleLevel だけを含める生成。** `plugins-all` を使わない。** status /
# trustlevel も有効化されると StatusCard が `profile:info` に出るので、
# 既存の profile 系 spec の DOM が変わる。e2e は 1 本ずつ確かめたい。
plugins-rolelevel: ## roleLevel だけを含めてプラグインを生成 (e2e 用)
	GOWORK=off go run ./tools/pluginbuild -include-disabled-dir plugins/rolelevel
```

Then change the `e2e-frontend-build` prerequisite (line 673) from `e2e-frontend-build: plugins` to:

```make
# 生成ステップだけを差し替えるための変数。既定は本番と同じ（`disabled` を除く）。
# ** target 固有の変数で、prerequisite にも継承される**ので、recipe は 1 本のまま
# 共有できる。
# ** `$(shell …)` は使わない。** make の parse 時に必ず走るので、対象と無関係な
# `make help` でも git を呼ぶことになるうえ、`gaterun-check` の「この Makefile に
# `$(shell …)` が無い」という前提が壊れる（`CLAUDE.md` 2026-09-06）。
PLUGIN_GENERATE ?= plugins

e2e-frontend-build: $(PLUGIN_GENERATE) ## フロントエンドをビルド (本番の bind-mount 先を上書きするので注意)
	<recipe は 674 行目から 682 行目までそのまま残す>
```

Add right after that target:

```make
# roleLevel の e2e 用。** disabled を含める生成**が要るが、`plugins-all` だと
# サンプルの frontend も入ってしまうので、対象を名指しした生成を使う
#（Task 4 Step 3 / plugins-rolelevel）。
e2e-frontend-build-rolelevel: PLUGIN_GENERATE = plugins-rolelevel
```

- [ ] **Step 12: Run the unit tests and confirm GREEN**

Run in `E:\tmp\opencode\mk-can-delete-account`:

```powershell
make rolelevel-plugin-test
```

Expected: PASS。3 ファイル（`test/xp.test.ts` / `test/locales.test.ts` / `test/errors.test.ts`）と Step 2 の test 名が全部走る。`No test files found` が出るなら Task 3 の include が効いていないので `make plugins` を先に実行する。

- [ ] **Step 13: Run the type check and confirm GREEN**

```powershell
make rolelevel-frontend-check
```

Expected: PASS。**これがこの plugin の型が一度に見られる最初の機会**なので、書いたファイルに実際のエラーが出る（`Intl.NumberFormat` の locale 型、`t('zz' as never, ...)` の narrowing の書き方、`v-model.number` の型あたり）。その場で直す。`@ts-ignore` / `eslint-disable` で黙らせない。

- [ ] **Step 14: Wire both targets into CI**

In `.github/workflows/ci.yml`, insert two steps immediately after the `Type check (vue-tsc)` step (line 550-551):

```yaml
      # roleLevel プラグインの frontend (#12)。
      # ** 独立した target である理由: plugin frontend は submodule の `src/**`
      # から import されるまで `make frontend-check` には入らない。
      # `plugins/rolelevel/mk-plugin.yml` は backend plan の所有で、この
      # repository にはまだ無いので、`make frontend-check` だけだと plugin の型が
      # 一度も検査されないまま緑になる。
      # 上の `make plugins-all` が終わっているので `mk-plugins.generated.json` は
      # 存在し、Task 3 の vitest include も有効になっている。
      - name: Type check (role-level plugin frontend)
        run: make rolelevel-frontend-check

      - name: Unit test (role-level plugin frontend)
        run: make rolelevel-plugin-test
```

- [ ] **Step 15: Commit the client layer and the runner wiring**

```powershell
git add .gitignore Makefile .github/workflows/ci.yml plugins/rolelevel/frontend
git status --short
git diff --check
git commit -m "Add role-level plugin: frontend client and unit tests"
```

Expected: 1 commit。`git status --short` には plugin の 10 ファイルと変更した 3 ファイルしか出ない。`plugins/rolelevel/frontend/tsconfig.json` が出ていないなら `.gitignore` の例外が wrong（Step 1）。
### Task 6: Role Curve And Policy Editor (`admin:role-editor`)

**Files:**
- Create: `plugins/rolelevel/frontend/CurveEditor.vue`
- Create: `plugins/rolelevel/frontend/PolicyRangeEditor.vue`
- Create: `plugins/rolelevel/frontend/RoleLevelPanel.vue`
- Create: `plugins/rolelevel/frontend/index.ts`

**Interfaces:**
- Consumes: `SlotContext` / `SlotRole` from Task 2、`api.ts` / `types.ts` / `errors.ts` / `locales.ts` from Task 5
- Produces: `host.slot('admin:role-editor', { component: RoleLevelPanel })`
- Produces: `CurveEditor` props `{ modelValue: CurveSegment[]; msg: Record<MessageKey, string> }`、emit `update:modelValue`
- Produces: `PolicyRangeEditor` props `{ modelValue: PolicyRange[]; msg: Record<MessageKey, string> }`、emit `update:modelValue`
- Produces: `data-testid="rolelevel-panel"` を root に。すべての `<input>` / `<select>` は `<label for>` を持つ。表は `<caption>` と `<th scope="col">` を持つ

- [ ] **Step 1: Write `CurveEditor.vue`**

Create `plugins/rolelevel/frontend/CurveEditor.vue`:

```vue
<!--
SPDX-FileCopyrightText: mk-go project
SPDX-License-Identifier: AGPL-3.0-only
-->

<template>
	<div :class="$style.root">
		<table :class="$style.table">
			<caption :class="$style.caption">{{ msg.curveCaption }}</caption>
			<thead>
				<tr>
					<th scope="col">{{ msg.curveType }}</th>
					<th scope="col">{{ msg.curveBase }}</th>
					<th scope="col">{{ msg.curveAdditional }}</th>
					<th scope="col">{{ msg.curveLevelUps }}</th>
					<th scope="col"><span :class="$style.srOnly">{{ msg.curveRemove }}</span></th>
				</tr>
			</thead>
			<tbody>
				<tr v-for="(seg, i) in modelValue" :key="i">
					<td>
						<label :for="`${uid}-t-${i}`" :class="$style.srOnly">{{ msg.curveType }} {{ i + 1 }}</label>
						<select :id="`${uid}-t-${i}`" :value="seg.type" @change="patchType(i, $event)">
							<option v-for="t in SEGMENT_TYPES" :key="t" :value="t">{{ segmentLabel(t) }}</option>
						</select>
					</td>
					<td>
						<label :for="`${uid}-b-${i}`" :class="$style.srOnly">{{ msg.curveBase }} {{ i + 1 }}</label>
						<input :id="`${uid}-b-${i}`" type="number" step="1" inputmode="numeric" :value="seg.base" @input="patchNumber(i, 'base', $event)">
					</td>
					<td>
						<!-- exponential の additional は**有限な底**（1.05 で 5% ずつ）なので
						     step も inputmode も小数にする。const / linear では整数。 -->
						<label :for="`${uid}-a-${i}`" :class="$style.srOnly">{{ additionalLabel(seg) }} {{ i + 1 }}</label>
						<input
							:id="`${uid}-a-${i}`"
							type="number"
							:step="seg.type === 'exponential' ? '0.001' : '1'"
							:inputmode="seg.type === 'exponential' ? 'decimal' : 'numeric'"
							:value="seg.additional"
							@input="patchNumber(i, 'additional', $event)"
						>
					</td>
					<td>
						<label :for="`${uid}-l-${i}`" :class="$style.srOnly">{{ msg.curveLevelUps }} {{ i + 1 }}</label>
						<input :id="`${uid}-l-${i}`" type="number" step="1" min="1" inputmode="numeric" :value="seg.levelUps" @input="patchNumber(i, 'levelUps', $event)">
					</td>
					<td>
						<button type="button" class="_button" :data-testid="`${uid}-rm-${i}`" @click="remove(i)">
							<i class="ti ti-x" aria-hidden="true"></i>
							<span>{{ msg.curveRemove }}</span>
						</button>
					</td>
				</tr>
			</tbody>
		</table>
		<button type="button" class="_button" :data-testid="`${uid}-add`" @click="add()">
			<i class="ti ti-plus" aria-hidden="true"></i> {{ msg.curveAdd }}
		</button>
	</div>
</template>

<script lang="ts" setup>
import type { CurveSegment, CurveSegmentType } from './types.js';
import type { MessageKey } from './locales.js';

const SEGMENT_TYPES: readonly CurveSegmentType[] = ['const', 'linear', 'exponential'];

/*
 * label の `for` と input の `id` を 1 対 1 にする。`useId()` は 3.5 にあるが
 * upstream で使われていないので、自前の連番 id を使う。plugin は同時に複数
 * mount されるので prefix に plugin 名を入れる。
 */
let seq = 0;
const uid = `rolelevel-curve-${++seq}`;

const props = defineProps<{
	modelValue: CurveSegment[];
	msg: Record<MessageKey, string>;
}>();

const emit = defineEmits<{ (ev: 'update:modelValue', v: CurveSegment[]): void }>();

function patchNumber(index: number, key: 'base' | 'additional' | 'levelUps', ev: Event) {
	const parsed = Number.parseInt((ev.target as HTMLInputElement).value, 10);
	const value = Number.isFinite(parsed) ? parsed : 0;
	emit('update:modelValue', props.modelValue.map((seg, i) => (i === index ? { ...seg, [key]: value } : seg)));
}

function patchType(index: number, ev: Event) {
	const value = (ev.target as HTMLSelectElement).value as CurveSegmentType;
	emit('update:modelValue', props.modelValue.map((seg, i) => (i === index ? { ...seg, type: value } : seg)));
}

function add() {
	emit('update:modelValue', [...props.modelValue, { type: 'const', base: 100, additional: 0, levelUps: 1 }]);
}

function remove(index: number) {
	emit('update:modelValue', props.modelValue.filter((_, i) => i !== index));
}

/** exponential だけ「底」のラベルを出し、ほかは「追加分」のまま。 */
function additionalLabel(seg: CurveSegment): string {
	return seg.type === 'exponential' ? props.msg.curveExponentialBase : props.msg.curveAdditional;
}

function segmentLabel(t: CurveSegmentType): string {
	const key = `curveType${t.charAt(0).toUpperCase()}${t.slice(1)}` as MessageKey;
	return props.msg[key] ?? t;
}
</script>

<style lang="scss" module>
.root {
	overflow-x: auto;
}

.table {
	width: 100%;
	border-collapse: collapse;
	font-size: 0.9em;

	> :global(th),
	> :global(td) {
		padding: 4px 6px;
		text-align: start;
		vertical-align: middle;
	}

	input,
	select {
		width: 100%;
		min-width: 4em;
		min-height: 32px;
	}
}

.caption {
	padding-bottom: 4px;
	font-size: 0.85em;
	opacity: 0.7;
	text-align: start;
}

/* 画面から消えるだけのラベル。** display: none にしない** — label が
   accessible name の供給源なので、消すとスクリーンリーダーが読めない。 */
.srOnly {
	position: absolute;
	width: 1px;
	height: 1px;
	overflow: hidden;
	clip-path: inset(50%);
	white-space: nowrap;
}
</style>
```

- [ ] **Step 2: Write `PolicyRangeEditor.vue`**

Create `plugins/rolelevel/frontend/PolicyRangeEditor.vue`:

```vue
<!--
SPDX-FileCopyrightText: mk-go project
SPDX-License-Identifier: AGPL-3.0-only
-->

<template>
	<div :class="$style.root">
		<p :class="$style.hint">{{ msg.policyHint }}</p>
		<table :class="$style.table">
			<thead>
				<tr>
					<th scope="col">{{ msg.policyKey }}</th>
					<th scope="col">{{ msg.policyType }}</th>
					<th scope="col">{{ msg.policyStartStage }}</th>
					<th scope="col">{{ msg.policyValue }}</th>
					<th scope="col"><span :class="$style.srOnly">{{ msg.policyRemove }}</span></th>
				</tr>
			</thead>
			<tbody>
				<tr v-for="(range, i) in modelValue" :key="i">
					<td>
						<label :for="`${uid}-k-${i}`" :class="$style.srOnly">{{ msg.policyKey }} {{ i + 1 }}</label>
						<select :id="`${uid}-k-${i}`" :value="range.key" @change="patchKey(i, $event)">
							<option v-for="spec in POLICY_KEYS" :key="spec.key" :value="spec.key">{{ spec.key }}</option>
						</select>
					</td>
					<td>
						<label :for="`${uid}-t-${i}`" :class="$style.srOnly">{{ msg.policyType }} {{ i + 1 }}</label>
						<select :id="`${uid}-t-${i}`" :value="range.type" @change="patchType(i, $event)">
							<option value="base">{{ msg.policyTypeBase }}</option>
							<option value="const">{{ msg.policyTypeConst }}</option>
							<option value="multiplier">{{ msg.policyTypeMultiplier }}</option>
						</select>
					</td>
					<td>
						<label :for="`${uid}-s-${i}`" :class="$style.srOnly">{{ msg.policyStartStage }} {{ i + 1 }}</label>
						<input :id="`${uid}-s-${i}`" type="number" step="1" min="1" inputmode="numeric" :value="range.startStage" @input="patchStartStage(i, $event)">
					</td>
					<td>
						<template v-if="range.type === 'const'">
							<label :for="`${uid}-v-${i}`" :class="$style.srOnly">{{ msg.policyValue }} {{ i + 1 }}</label>
							<input v-if="spec?.kind === 'boolean'" :id="`${uid}-v-${i}`" type="checkbox" :checked="range.value === true" @change="patchBoolean(i, $event)">
							<select v-else-if="spec?.kind === 'enum'" :id="`${uid}-v-${i}`" :value="String(range.value ?? '')" @change="patchString(i, $event)">
								<option v-for="v in spec?.values ?? []" :key="v" :value="v">{{ v }}</option>
							</select>
							<input v-else :id="`${uid}-v-${i}`" type="number" step="1" inputmode="numeric" :value="Number(range.value ?? 0)" @input="patchNumber(i, $event)">
						</template>
						<template v-else-if="range.type === 'multiplier'">
							<label :for="`${uid}-a-${i}`" :class="$style.srOnly">{{ msg.policyMultiplierValue }} {{ i + 1 }}</label>
							<input :id="`${uid}-a-${i}`" type="number" step="0.1" min="0" inputmode="decimal" :value="range.additional" @input="patchAdditional(i, $event)">
						</template>
						<span v-else :class="$style.muted">{{ msg.policyTypeBase }}</span>
					</td>
					<td>
						<button type="button" class="_button" :data-testid="`${uid}-rm-${i}`" @click="remove(i)">
							<i class="ti ti-x" aria-hidden="true"></i>
							<span>{{ msg.policyRemove }}</span>
						</button>
					</td>
				</tr>
			</tbody>
		</table>
		<button type="button" class="_button" :data-testid="`${uid}-add`" @click="add()">
			<i class="ti ti-plus" aria-hidden="true"></i> {{ msg.policyAdd }}
		</button>
	</div>
</template>

<script lang="ts" setup>
import { POLICY_KEYS, policySpecOf } from './policy-keys.js';
import type { PolicyRange, PolicyRangeType } from './types.js';
import type { MessageKey } from './locales.js';

let seq = 0;
const uid = `rolelevel-policy-${++seq}`;

const props = defineProps<{
	modelValue: PolicyRange[];
	msg: Record<MessageKey, string>;
}>();

const emit = defineEmits<{ (ev: 'update:modelValue', v: PolicyRange[]): void }>();

function num(ev: Event): number {
	const parsed = Number.parseInt((ev.target as HTMLInputElement).value, 10);
	return Number.isFinite(parsed) ? parsed : 0;
}

function str(ev: Event): string {
	return (ev.target as HTMLSelectElement).value;
}

function specOf(key: string) {
	return policySpecOf(key);
}

const spec = (row: PolicyRange) => specOf(row.key);

function replace(index: number, part: Partial<PolicyRange>) {
	emit('update:modelValue', props.modelValue.map((range, i) => (i === index ? { ...range, ...part } : range)));
}

/** key を替えたら規則と値を base に戻す。** 型が合わない range を黙って送るより
 *  base に戻しておくほうが安全で、backend 側の validation と同じ形になる。 */
function patchKey(index: number, ev: Event) {
	replace(index, { key: str(ev), type: 'base', value: null, additional: 0 });
}

function patchType(index: number, ev: Event) {
	const type = str(ev) as PolicyRangeType;
	replace(index, { type, value: type === 'const' ? null : null, additional: 0 });
}

function patchStartStage(index: number, ev: Event) {
	replace(index, { startStage: Math.max(1, num(ev)) });
}

function patchNumber(index: number, ev: Event) {
	replace(index, { value: num(ev) });
}

function patchString(index: number, ev: Event) {
	replace(index, { value: str(ev) });
}

function patchBoolean(index: number, ev: Event) {
	replace(index, { value: (ev.target as HTMLInputElement).checked });
}

function patchAdditional(index: number, ev: Event) {
	replace(index, { additional: num(ev) });
}

/** 次の開始 stage = 直前の開始 stage + 1。** overlap を作らない既定にする。 */
function nextStage(): number {
	const last = props.modelValue[props.modelValue.length - 1];
	return last == null ? 1 : last.startStage + 1;
}

function add() {
	emit('update:modelValue', [
		...props.modelValue,
		{ key: POLICY_KEYS[0].key, startStage: nextStage(), type: 'base', value: null, additional: 0 },
	]);
}

function remove(index: number) {
	emit('update:modelValue', props.modelValue.filter((_, i) => i !== index));
}
</script>

<style lang="scss" module>
.root {
	overflow-x: auto;
}

.hint {
	margin: 0 0 4px;
	font-size: 0.85em;
	opacity: 0.7;
}

.table {
	width: 100%;
	border-collapse: collapse;
	font-size: 0.9em;

	> :global(th),
	> :global(td) {
		padding: 4px 6px;
		text-align: start;
		vertical-align: middle;
	}

	input,
	select {
		width: 100%;
		min-width: 4em;
		min-height: 32px;
	}
}

.muted {
	opacity: 0.6;
}

.srOnly {
	position: absolute;
	width: 1px;
	height: 1px;
	overflow: hidden;
	clip-path: inset(50%);
	white-space: nowrap;
}
</style>
```

Step 1 の template は `spec?.kind` を参照しているが、script には `spec` 関数を 1 個書いている。**template 側は `spec(row)` を使う**ので、`v-if="spec(range).kind === 'boolean'"` のように呼び出し形に直す（`spec?.kind` は `range` を引かないのでコンパイルが壊れる）。`v-else-if` / `v-for` も同じ形にそろえる:

```
v-if="spec(range).kind === 'boolean'"
v-else-if="spec(range).kind === 'enum'"
:value="String(range.value ?? '')"
<option v-for="v in spec(range).values ?? []" ...>
```

- [ ] **Step 3: Write `RoleLevelPanel.vue`**

Create `plugins/rolelevel/frontend/RoleLevelPanel.vue`:

```vue
<!--
SPDX-FileCopyrightText: mk-go project
SPDX-License-Identifier: AGPL-3.0-only
-->

<template>
	<div v-if="visible" :class="$style.root" data-testid="rolelevel-panel">
		<h3 :class="$style.title">{{ msg.roleEditorTitle }}</h3>
		<p :class="$style.hint">{{ msg.roleEditorHint }}</p>

		<!-- 権限が無い場合は何も描かない。403 は「権限が無い」なので理由を出さない
		     （docs/plugins/authoring.md の「管理向けのスロット」の節）。 -->
		<template v-else-if="editable">
			<p v-if="error" :class="$style.error" role="alert">{{ error }}</p>
			<MkLoading v-else-if="loading" :class="$style.loading"/>
			<template v-else>
				<label :class="$style.check" :for="`${uid}-enabled`">
					<input :id="`${uid}-enabled`" v-model="enabled" type="checkbox">
					<span>{{ msg.roleEditorTitle }}</span>
				</label>

				<template v-if="enabled">
					<div :class="$style.field">
						<label :for="`${uid}-base`">{{ msg.baseLevel }}</label>
						<input :id="`${uid}-base`" v-model.number="baseLevel" type="number" step="1" inputmode="numeric">
					</div>

					<h4 :class="$style.subtitle">{{ msg.curveTitle }}</h4>
					<CurveEditor v-model="curve" :msg="msg"/>

					<h4 :class="$style.subtitle">{{ msg.policyTitle }}</h4>
					<PolicyRangeEditor v-model="policyRanges" :msg="msg"/>

					<div :class="$style.actions">
						<button type="button" class="_button" :class="$style.primary" :data-testid="`${uid}-save`" :disabled="saving" @click="save()">
							{{ msg.save }}
						</button>
						<button v-if="config != null" type="button" class="_button" :class="$style.danger" :data-testid="`${uid}-delete`" :disabled="saving" @click="remove()">
							{{ msg.removeConfig }}
						</button>
					</div>
				</template>
			</template>
		</template>
	</div>
</template>

<script lang="ts" setup>
import { computed, onMounted, ref } from 'vue';
import { MkLoading } from '@/plugin-api.js';
import type { SlotContext } from '@/plugin-api.js';
import { MESSAGES, resolveLocale, t } from './locales.js';
import type { Locale } from './locales.js';
import { errorMessage, isForbidden } from './errors.js';
import { deleteRoleConfig, getRoleConfig, saveRoleConfig } from './api.js';
import type { CurveSegment, PolicyRange, RoleLevelConfig } from './types.js';
import CurveEditor from './CurveEditor.vue';
import PolicyRangeEditor from './PolicyRangeEditor.vue';

let seq = 0;
const uid = `rolelevel-panel-${++seq}`;

const props = defineProps<{ ctx: SlotContext }>();

const locale = ref<Locale>('en');
const msg = computed(() => MESSAGES[locale.value]);

const loading = ref(true);
const saving = ref(false);
/** false にしたら描画を消す = 権限が無い状態。** 403 のときだけ隠す。 */
const visible = ref(true);
const error = ref('');
const config = ref<RoleLevelConfig | null>(null);
const enabled = ref(false);
const baseLevel = ref(1);
const curve = ref<CurveSegment[]>([]);
const policyRanges = ref<PolicyRange[]>([]);

/**
 * ロールが未保存（`ctx.readonly`）または conditional なら描かない。
 * conditional は assignment を持たない = XP の付け所が無い（spec）。
 */
const editable = computed(() => {
	const role = props.ctx.role;
	return role != null && role.id != null && role.target === 'manual' && props.ctx.readonly !== true;
});

const DEFAULT_CURVE: CurveSegment[] = [{ type: 'const', base: 100, additional: 0, levelUps: 99 }];
const DEFAULT_RANGES: PolicyRange[] = [{ key: 'canCreateChannel', startStage: 1, type: 'base', value: null, additional: 0 }];

onMounted(async () => {
	locale.value = resolveLocale(window.navigator.language);
	if (!editable.value) {
		loading.value = false;
		return;
	}
	try {
		const loaded = await getRoleConfig(props.ctx.role?.id as string);
		config.value = loaded;
		enabled.value = loaded != null;
		baseLevel.value = loaded?.baseLevel ?? 1;
		curve.value = loaded?.curve ?? DEFAULT_CURVE;
		policyRanges.value = loaded?.policyRanges ?? DEFAULT_RANGES;
	} catch (err) {
		if (isForbidden(err)) visible.value = false;
		else error.value = errorMessage(locale.value, err);
	} finally {
		loading.value = false;
	}
});

async function save() {
	const role = props.ctx.role;
	if (role == null || role.id == null || saving.value) return;
	saving.value = true;
	error.value = '';
	try {
		if (enabled.value) {
			config.value = await saveRoleConfig(role.id, {
				baseLevel: baseLevel.value,
				curve: curve.value,
				policyRanges: policyRanges.value,
				revision: config.value?.revision ?? 0,
			});
		} else {
			await deleteRoleConfig(role.id);
			config.value = null;
		}
	} catch (err) {
		error.value = errorMessage(locale.value, err);
	} finally {
		saving.value = false;
	}
}

async function remove() {
	const role = props.ctx.role;
	if (role == null || role.id == null || saving.value) return;
	if (!window.confirm(t(locale.value, 'confirmRemove'))) return;
	saving.value = true;
	error.value = '';
	try {
		await deleteRoleConfig(role.id);
		config.value = null;
		enabled.value = false;
	} catch (err) {
		error.value = errorMessage(locale.value, err);
	} finally {
		saving.value = false;
	}
}
</script>

<style lang="scss" module>
.root {
	display: flex;
	flex-direction: column;
	gap: 12px;
	margin-top: 12px;
	padding-top: 12px;
	border-top: solid 1px var(--MI_THEME-divider);
}

.title {
	margin: 0;
	font-size: 1.05em;
	font-weight: bold;
}

.subtitle {
	margin: 0;
	font-size: 0.95em;
	font-weight: bold;
}

.hint {
	margin: 0;
	font-size: 0.85em;
	opacity: 0.7;
}

.error {
	margin: 0;
	color: var(--MI_THEME-error);
	font-size: 0.9em;
}

.field {
	display: flex;
	flex-direction: column;
	gap: 4px;
	max-width: 16em;

	input {
		min-height: 32px;
	}
}

.check {
	display: flex;
	align-items: center;
	gap: 8px;

	input {
		min-width: 20px;
		min-height: 20px;
	}
}

.actions {
	display: flex;
	gap: 8px;
	flex-wrap: wrap;
}

.primary {
	background: var(--MI_THEME-accent);
	color: var(--MI_THEME-bg);
}

.danger {
	color: var(--MI_THEME-error);
}

.loading {
	align-self: flex-start;
}
</style>
```

- [ ] **Step 4: Write `index.ts` and the two remaining stubs**

Create `plugins/rolelevel/frontend/index.ts`:

```ts
/*
 * SPDX-FileCopyrightText: mk-go project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import { definePlugin } from '@/plugin-api.js';
import { initApi } from './api.js';
import RoleLevelPanel from './RoleLevelPanel.vue';
import AdminUserLevels from './AdminUserLevels.vue';
import ProfileLevelCard from './ProfileLevelCard.vue';
import ManagePage from './ManagePage.vue';

/*
 * **ページは setup ではなくここで宣言する。** ルーターはモジュール読み込み時に
 * 現在の URL を解決するので、setup で登録すると直接アクセスが 404 になる
 * （画面遷移では動くので気付きにくい）。宣言なら読み込み順に依存しない
 * （`plugin-api.ts:174-189`）。
 */
export default definePlugin({
	name: 'role-level',

	pages: [
		{
			path: '/',
			component: ManagePage,
			admin: true,
			navTitle: 'レベルとXP',
			navIcon: 'ti ti-chart-bar',
		},
	],

	setup(host) {
		initApi(host.api);

		// Vue コンポーネント形式で登録する。ホストの app 内で描画されるので
		// provide/inject もテーマも本体と同じものが効く。
		host.slot('admin:role-editor', { component: RoleLevelPanel });
		host.slot('admin:user', { component: AdminUserLevels });
		host.slot('profile:info', { component: ProfileLevelCard });
	},
});
```

Tasks 7-9 が置き換えるまでの一時的な stub を 3 つ置く（**stub を残さない**）:

```vue
<!-- plugins/rolelevel/frontend/AdminUserLevels.vue (Task 7 で置き換える) -->
<template>
	<div data-testid="rolelevel-admin-user"></div>
</template>

<script lang="ts" setup>
import type { SlotContext } from '@/plugin-api.js';

defineProps<{ ctx: SlotContext }>();
</script>
```

```vue
<!-- plugins/rolelevel/frontend/ProfileLevelCard.vue (Task 8 で置き換える) -->
<template>
	<div data-testid="rolelevel-profile"></div>
</template>

<script lang="ts" setup>
import type { SlotContext } from '@/plugin-api.js';

defineProps<{ ctx: SlotContext }>();
</script>
```

```vue
<!-- plugins/rolelevel/frontend/ManagePage.vue (Task 9 で置き換える) -->
<template>
	<div data-testid="rolelevel-manage"></div>
</template>
```

- [ ] **Step 5: Run the type check and confirm GREEN**

```powershell
make rolelevel-frontend-check
```

Expected: PASS。ここで出る典型的な修正は 2 つ。**template 内で `as` キャストは書けない**ので、Step 2 の指示どおり `patchType` のような script 側の関数に寄せること。**`props.ctx.role?.id as string`** は `editable` の narrowed が効かないので、`editable` |USA shape ではなく `role.id` の null チェックを関数内で持つ（上の実装のままで通ることを目標にする）。

- [ ] **Step 6: Commit the role editor**

```powershell
git add plugins/rolelevel/frontend
git status --short
git diff --check
git commit -m "Add role-level plugin: role editor slot UI"
```

Expected: 1 commit に `CurveEditor.vue` / `PolicyRangeEditor.vue` / `RoleLevelPanel.vue` / `index.ts` と 3 つの stub が入る。
### Task 7: Admin User XP Controls (`admin:user`)

**Files:**
- Create: `plugins/rolelevel/frontend/AdminUserLevels.vue`（Task 6 の stub を置き換える）

**Interfaces:**
- Consumes: `getUserLevels` / `changeExp` / `assignRole` / `unassignRole` / `listAudit` from Task 5、`previewExp` / `formatExp` / `EXP_MODES` from Task 5
- Consumes: `isForbidden` / `errorMessage` from Task 5
- Produces: root `data-testid="rolelevel-admin-user"`、`rolelevel-row-{roleId}`、`rolelevel-xp-{roleId}-apply`、`rolelevel-preview-{roleId}`、`rolelevel-unassign-{roleId}`、`rolelevel-assign`
- Produces: XP 操作 1 回ごとに新しい `idempotencyKey` を送る

- [ ] **Step 1: Write the component**

```vue
<!--
SPDX-FileCopyrightText: mk-go project
SPDX-License-Identifier: AGPL-3.0-only
-->

<template>
	<div v-if="visible" :class="$style.root" data-testid="rolelevel-admin-user">
		<h3 :class="$style.title">{{ msg.userTitle }}</h3>
		<p v-if="error" :class="$style.error" role="alert">{{ error }}</p>
		<MkLoading v-else-if="loading" :class="$style.loading"/>

		<template v-else-if="data">
			<div v-for="row in data.rows" :key="row.roleId" :class="$style.block" :data-testid="`rolelevel-row-${row.roleId}`">
				<div :class="$style.head">
					<span :class="$style.roleName">{{ row.roleName }}</span>
					<span :class="$style.meta" :data-testid="`rolelevel-exp-${row.roleId}`">
						{{ msg.userLevel }} {{ row.level?.currentLevel ?? '-' }} / {{ msg.userExp }} {{ format(row.experience) }}
					</span>
					<button
						v-if="row.assignmentId != null && row.canEdit"
						type="button"
						class="_button"
						:class="$style.danger"
						:data-testid="`rolelevel-unassign-${row.roleId}`"
						:disabled="busy"
						@click="unassign(row)"
					>
						{{ msg.userUnassign }}
					</button>
				</div>

				<div v-if="row.canEdit" :class="$style.form">
					<div :class="$style.field">
						<label :for="`${uid}-${row.roleId}-mode`">{{ msg.userMode }}</label>
						<select :id="`${uid}-${row.roleId}-mode`" v-model="modes[row.roleId]">
							<option v-for="mode in EXP_MODES" :key="mode" :value="mode">{{ modeLabel(mode) }}</option>
						</select>
					</div>
					<div :class="$style.field">
						<label :for="`${uid}-${row.roleId}-operand`">{{ msg.userOperand }}</label>
						<!-- multiplier のときだけ小数_acceptする。有限な倍率（1.5 = ×1.5）。 -->
						<input
							:id="`${uid}-${row.roleId}-operand`"
							v-model.number="operands[row.roleId]"
							type="number"
							:step="isMultiplier(row) ? '0.1' : '1'"
							:inputmode="isMultiplier(row) ? 'decimal' : 'numeric'"
						>
					</div>
					<p v-if="isMultiplier(row)" :class="$style.hint">{{ msg.userMultiplierHint }}</p>
					<div :class="$style.preview" :data-testid="`rolelevel-preview-${row.roleId}`">
						{{ msg.userCurrent }} {{ format(row.experience) }} → {{ msg.userPreview }} {{ format(previewFor(row)) }}
					</div>
					<button
						type="button"
						class="_button"
						:class="$style.primary"
						:data-testid="`rolelevel-xp-${row.roleId}-apply`"
						:disabled="busy"
						@click="apply(row)"
					>
						{{ msg.userApply }}
					</button>
					<div :class="$style.field">
						<label :for="`${uid}-${row.roleId}-note`">{{ msg.userNote }}</label>
						<input :id="`${uid}-${row.roleId}-note`" v-model="notes[row.roleId]" type="text">
					</div>
				</div>
			</div>

			<div v-if="data.unassignedRoles.length > 0" :class="$style.form">
				<div :class="$style.field">
					<label :for="`${uid}-assign-role`">{{ msg.userRole }}</label>
					<select :id="`${uid}-assign-role`" v-model="assignRoleId">
						<option v-for="opt in data.unassignedRoles" :key="opt.roleId" :value="opt.roleId">{{ opt.roleName }}</option>
					</select>
				</div>
				<button type="button" class="_button" data-testid="rolelevel-assign" :disabled="assignRoleId == null || busy" @click="assign()">
					{{ msg.userAssign }}
				</button>
			</div>

			<MkFolder :defaultOpen="false">
				<template #label>{{ msg.userAudit }}</template>
				<div :class="$style.form" data-testid="rolelevel-audit">
					<div v-for="entry in audit" :key="entry.id" :class="$style.audit">
						<span>{{ msg.auditTime }} {{ new Date(entry.createdAt).toLocaleString() }}</span>
						<span>{{ msg.auditOperation }} {{ entry.operation }}</span>
						<span>{{ msg.auditActor }} <code>{{ entry.actorId }}</code></span>
						<span>{{ msg.auditBefore }} {{ format(entry.beforeExperience) }} → {{ msg.auditAfter }} {{ format(entry.afterExperience) }}</span>
						<span v-if="entry.note != null">{{ msg.auditNote }} {{ entry.note }}</span>
					</div>
					<div v-if="audit.length === 0" :class="$style.muted">{{ msg.memberEmpty }}</div>
				</div>
			</MkFolder>
		</template>
	</div>
</template>

<script lang="ts" setup>
import { computed, onMounted, reactive, ref } from 'vue';
import { MkFolder, MkLoading } from '@/plugin-api.js';
import type { SlotContext } from '@/plugin-api.js';
import { MESSAGES, resolveLocale, t } from './locales.js';
import type { Locale } from './locales.js';
import { errorMessage, isForbidden } from './errors.js';
import { assignRole, changeExp, getUserLevels, listAudit, unassignRole } from './api.js';
import type { AdminUserLevels, AuditEntry, ExpMode, UserLevelRow } from './types.js';
import { EXP_MODES, formatExp, previewExp } from './xp.js';

let seq = 0;
const uid = `rolelevel-user-${++seq}`;

const props = defineProps<{ ctx: SlotContext }>();

const locale = ref<Locale>('en');
const msg = computed(() => MESSAGES[locale.value]);
const localeTag = computed(() => (locale.value === 'ja' ? 'ja-JP' : 'en-US'));

const loading = ref(true);
const busy = ref(false);
const visible = ref(true);
const error = ref('');
const data = ref<AdminUserLevels | null>(null);
const audit = ref<AuditEntry[]>([]);
const modes = reactive<Record<string, ExpMode | undefined>>({});
const operands = reactive<Record<string, number | undefined>>({});
const notes = reactive<Record<string, string | undefined>>({});
const assignRoleId = ref<string | null>(null);

onMounted(async () => {
	locale.value = resolveLocale(window.navigator.language);
	const user = props.ctx.user;
	// リモートユーザーには出さない。このインスタンスの assignment に紐づく
	// データしか無いので、他所のユーザーでは必ず空になる。
	if (user == null || user.host != null) {
		loading.value = false;
		return;
	}
	try {
		await reload();
	} catch (err) {
		if (isForbidden(err)) visible.value = false;
		else error.value = errorMessage(locale.value, err);
	} finally {
		loading.value = false;
	}
});

function modeLabel(mode: ExpMode): string {
	switch (mode) {
		case 'set': return t(locale.value, 'userModeSet');
		case 'add': return t(locale.value, 'userModeAdd');
		case 'multiplier': return t(locale.value, 'userModeMultiplier');
	}
}

function format(value: number | null): string {
	return formatExp(value ?? 0, localeTag.value);
}

function previewFor(row: UserLevelRow): number {
	return previewExp(modes[row.roleId] ?? 'add', row.experience, operands[row.roleId] ?? 0);
}

function isMultiplier(row: UserLevelRow): boolean {
	return (modes[row.roleId] ?? 'add') === 'multiplier';
}

/**
 * ** idempotency key は押した時ごとに発行する。** 同じ値を再利用すると backend が
 * 「同じ操作の再送」とみなして 2 回目以降を黙って捨てる（spec の XP Mutation と
 * pending operation の節）。
 */
function idempotencyKey(roleId: string): string {
	const user = props.ctx.user?.id ?? 'none';
	return `rolelevel-${user}-${roleId}-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;
}

async function reload() {
	const user = props.ctx.user;
	if (user == null) return;
	const loaded = await getUserLevels(user.id);
	data.value = loaded;
	for (const row of loaded.rows) {
		if (modes[row.roleId] == null) modes[row.roleId] = 'add';
		if (operands[row.roleId] == null) operands[row.roleId] = 0;
		if (notes[row.roleId] == null) notes[row.roleId] = '';
	}
	if (loaded.unassignedRoles.length > 0 && assignRoleId.value == null) {
		assignRoleId.value = loaded.unassignedRoles[0].roleId;
	}
	audit.value = await listAudit({ userId: user.id, limit: 20 });
}

async function apply(row: UserLevelRow) {
	const user = props.ctx.user;
	if (user == null || busy.value) return;
	busy.value = true;
	error.value = '';
	try {
		const note = notes[row.roleId];
		await changeExp({
			userId: user.id,
			roleId: row.roleId,
			mode: modes[row.roleId] ?? 'add',
			operand: operands[row.roleId] ?? 0,
			idempotencyKey: idempotencyKey(row.roleId),
			note: note == null || note === '' ? undefined : note,
		});
		await reload();
	} catch (err) {
		error.value = errorMessage(locale.value, err);
	} finally {
		busy.value = false;
	}
}

async function assign() {
	const user = props.ctx.user;
	if (user == null || assignRoleId.value == null || busy.value) return;
	busy.value = true;
	error.value = '';
	try {
		// ** native の `admin/roles/assign`（plugin の route ではない）。**
		// 204 なので戻りは取らず、読み直す。
		await assignRole({ userId: user.id, roleId: assignRoleId.value, expiresAt: null });
		assignRoleId.value = null;
		await reload();
	} catch (err) {
		error.value = errorMessage(locale.value, err);
	} finally {
		busy.value = false;
	}
}

async function unassign(row: UserLevelRow) {
	const user = props.ctx.user;
	if (user == null || busy.value) return;
	busy.value = true;
	error.value = '';
	try {
		await unassignRole({ userId: user.id, roleId: row.roleId });
		await reload();
	} catch (err) {
		error.value = errorMessage(locale.value, err);
	} finally {
		busy.value = false;
	}
}
</script>

<style lang="scss" module>
.root {
	display: flex;
	flex-direction: column;
	gap: 12px;
	margin-top: 12px;
}

.title {
	margin: 0;
	font-size: 1.05em;
	font-weight: bold;
}

.block {
	padding: 8px;
	border-radius: var(--MI-radius-sm, 8px);
	background: var(--MI_THEME-buttonBg);
}

.head {
	display: flex;
	align-items: center;
	gap: 8px;
	flex-wrap: wrap;
}

.roleName {
	font-weight: bold;
}

.meta {
	font-size: 0.85em;
	opacity: 0.8;
}

.form {
	display: flex;
	align-items: flex-end;
	gap: 8px;
	flex-wrap: wrap;
	margin-top: 8px;
}

.field {
	display: flex;
	flex-direction: column;
	gap: 4px;
	max-width: 14em;

	input,
	select {
		min-height: 32px;
	}
}

.preview {
	font-size: 0.85em;
	opacity: 0.8;
}

.hint {
	margin: 0;
	font-size: 0.8em;
	opacity: 0.7;
}

.audit {
	display: flex;
	flex-direction: column;
	font-size: 0.85em;
	padding: 4px 0;
	border-bottom: solid 1px var(--MI_THEME-divider);
}

.error {
	margin: 0;
	color: var(--MI_THEME-error);
}

.muted {
	opacity: 0.7;
	font-size: 0.9em;
}

.primary {
	background: var(--MI_THEME-accent);
	color: var(--MI_THEME-bg);
}

.danger {
	color: var(--MI_THEME-error);
}

.loading {
	align-self: flex-start;
}
</style>
```

- [ ] **Step 2: Run the type check and confirm GREEN**

```powershell
make rolelevel-frontend-check
```

Expected: PASS。`reactive<Record<string, ExpMode | undefined>>` は index access が optional になるので、`modes[row.roleId] ?? 'add'` の fallback を落とすと `strict` で落ちる。落ちたら那是設計通りなので fallback を入れる（Step 1 のコードがその状態）。

- [ ] **Step 3: Commit**

```powershell
git add plugins/rolelevel/frontend/AdminUserLevels.vue
git diff --check
git commit -m "Add role-level plugin: admin user XP controls"
```

### Task 8: Profile Level Progress (`profile:info`)

**Files:**
- Create: `plugins/rolelevel/frontend/ProfileLevelCard.vue`（Task 6 の stub を置き換える）

**Interfaces:**
- Consumes: `getPublicLevels` from Task 5、`formatExp` / `progressPercent` from Task 5
- Produces: root `data-testid="rolelevel-profile"`、`rolelevel-level-{roleId}`、`rolelevel-exp-{roleId}`、`rolelevel-progress-{roleId}`（`role="progressbar"` と `aria-valuenow` / `aria-valuemin` / `aria-valuemax` / `aria-valuetext` / `aria-label` を持つ）
- Produces: リモートユーザー、および取得失敗のときは **何も描かない**

- [ ] **Step 1: Write the component**

```vue
<!--
SPDX-FileCopyrightText: mk-go project
SPDX-License-Identifier: AGPL-3.0-only
-->

<template>
	<div v-if="rows.length > 0" :class="$style.root" data-testid="rolelevel-profile">
		<div v-for="row in rows" :key="row.roleId" :class="$style.row">
			<div :class="$style.head">
				<span :class="$style.roleName">{{ row.roleName }}</span>
				<span :class="$style.level" :data-testid="`rolelevel-level-${row.roleId}`">{{ row.level?.currentLevel ?? '-' }}</span>
				<span :class="$style.exp" :data-testid="`rolelevel-exp-${row.roleId}`">{{ expText(row) }}</span>
			</div>
			<!--
				**進捗は `role="progressbar"` の素の div で表す。** 公開 Plugin API は
				`MkProgressBar` を再公開していないので選べない。`aria-valuenow` を
				付けるとスクリーンリーダーへ「現在 42 / 次 100」と読まれる。
				最大 level では `aria-valuetext` を最大 level の文言にして、
				「これ以上上がらない」ことを明確にする。
			-->
			<div
				:data-testid="`rolelevel-progress-${row.roleId}`"
				role="progressbar"
				:aria-label="msg.progressLabel"
				:aria-valuenow="percent(row)"
				aria-valuemin="0"
				aria-valuemax="100"
				:aria-valuetext="expText(row)"
				:class="$style.bar"
			>
				<div :class="$style.fill" :style="{ width: percent(row) + '%' }"/>
			</div>
		</div>
	</div>
</template>

<script lang="ts" setup>
import { computed, onMounted, ref } from 'vue';
import type { SlotContext } from '@/plugin-api.js';
import { MESSAGES, resolveLocale, t } from './locales.js';
import type { Locale } from './locales.js';
import { getPublicLevels } from './api.js';
import type { UserLevelRow } from './types.js';
import { formatExp, progressPercent } from './xp.js';

const props = defineProps<{ ctx: SlotContext }>();

const locale = ref<Locale>('en');
const msg = computed(() => MESSAGES[locale.value]);
const localeTag = computed(() => (locale.value === 'ja' ? 'ja-JP' : 'en-US'));
const rows = ref<UserLevelRow[]>([]);

onMounted(async () => {
	locale.value = resolveLocale(window.navigator.language);
	const user = props.ctx.user;
	// リモートユーザーには出さない。level はこのインスタンスの assignment に
	// 紐づくので、他所のユーザーでは必ず空になる（status の StatusCard と同じ判断）。
	if (user == null || user.host != null) return;
	try {
		rows.value = (await getPublicLevels(user.id)).filter((r) => r.level != null);
	} catch {
		// 公開プロフィールが壊れてはいけないので、失敗は「出さない」で済ませる。
		rows.value = [];
	}
});

function percent(row: UserLevelRow): number {
	if (row.level == null) return 0;
	return progressPercent(row.level.currentLevelExp, row.level.nextLevelExp);
}

function expText(row: UserLevelRow): string {
	if (row.level == null) return '';
	if (row.level.nextLevelExp == null) {
		return `${t(locale.value, 'levelMax')} (${formatExp(row.level.currentLevelExp, localeTag.value)})`;
	}
	return `${formatExp(row.level.currentLevelExp, localeTag.value)} / ${formatExp(row.level.nextLevelExp, localeTag.value)}`;
}
</script>

<style lang="scss" module>
.root {
	display: flex;
	flex-direction: column;
	gap: 8px;
	margin: 8px 0;
}

.row {
	display: flex;
	flex-direction: column;
	gap: 4px;
}

.head {
	display: flex;
	align-items: baseline;
	gap: 8px;
	flex-wrap: wrap;
}

.roleName {
	font-weight: bold;
}

.level {
	font-size: 1.1em;
}

.exp {
	font-size: 0.85em;
	opacity: 0.75;
}

.bar {
	overflow: hidden;
	height: 6px;
	border-radius: 3px;
	background: var(--MI_THEME-bg);
}

.fill {
	height: 100%;
	background: var(--MI_THEME-accent);
}
</style>
```

- [ ] **Step 2: Run the type check and confirm GREEN**

```powershell
make rolelevel-frontend-check
```

Expected: PASS。`row.level?.currentLevel` の narrowing が `filter` 後に効かないので、`percent()` / `expText()` の `row.level == null` 早期 return が要る（Step 1 のコードがその形）。消すと `strictNullChecks` で落ちる。

- [ ] **Step 3: Commit**

```powershell
git add plugins/rolelevel/frontend/ProfileLevelCard.vue
git diff --check
git commit -m "Add role-level plugin: profile level progress"
```
### Task 9: Management Page, Member Ranking, And Reconciliation

**Files:**
- Create: `plugins/rolelevel/frontend/MemberRanking.vue`（Task 6 の stub を置き換える）
- Create: `plugins/rolelevel/frontend/ManagePage.vue`（Task 6 の stub を置き換える）

**Interfaces:**
- Consumes: `listLevelRoles` / `listMembers` / `listOrphans` / `getReconcileStatus` from Task 5
- Produces: `data-testid="rolelevel-manage"`、`rolelevel-members`、`rolelevel-member-table`、`rolelevel-member-next`、`rolelevel-orphans`
- Produces: `MemberRanking` props `{ initialRoleId: string | null }`
- Produces: member の並び順は **backend が返す順のまま**。frontend で並べ替えない

- [ ] **Step 1: Write `MemberRanking.vue`**

```vue
<!--
SPDX-FileCopyrightText: mk-go project
SPDX-License-Identifier: AGPL-3.0-only
-->

<template>
	<div :class="$style.root" data-testid="rolelevel-members">
		<div :class="$style.field">
			<label :for="`${uid}-role`">{{ msg.manageTitle }}</label>
			<select :id="`${uid}-role`" v-model="roleId">
				<option v-for="opt in roles" :key="opt.roleId" :value="opt.roleId">{{ opt.roleName }}</option>
			</select>
		</div>

		<p v-if="error" :class="$style.error" role="alert">{{ error }}</p>
		<MkLoading v-else-if="loading" :class="$style.loading"/>

		<template v-else>
			<div :class="$style.scroll">
				<table :class="$style.table" data-testid="rolelevel-member-table">
					<thead>
						<tr>
							<th scope="col">{{ msg.memberRank }}</th>
							<th scope="col">{{ msg.userRole }}</th>
							<th scope="col">{{ msg.userLevel }}</th>
							<th scope="col">{{ msg.userExp }}</th>
						</tr>
					</thead>
					<tbody>
						<tr v-for="(row, i) in page.items" :key="row.assignmentId" :data-testid="`rolelevel-member-${row.assignmentId}`">
							<td>{{ offset + i + 1 }}</td>
							<td>{{ acct(row) }}</td>
							<td>{{ row.level.currentLevel }}</td>
							<td>{{ format(row.experience) }}</td>
						</tr>
					</tbody>
				</table>
			</div>
			<div v-if="page.items.length === 0" :class="$style.muted">{{ msg.memberEmpty }}</div>
			<button v-if="page.nextCursor != null" type="button" class="_button" data-testid="rolelevel-member-next" :disabled="loading" @click="next()">
				{{ msg.memberNext }}
			</button>
		</template>
	</div>
</template>

<script lang="ts" setup>
import { computed, onMounted, ref, watch } from 'vue';
import { MkLoading } from '@/plugin-api.js';
import { MESSAGES, resolveLocale, t } from './locales.js';
import type { Locale } from './locales.js';
import { errorMessage, isForbidden } from './errors.js';
import { listLevelRoles, listMembers } from './api.js';
import type { LevelRoleOption, MemberPage, MemberXpRow } from './types.js';
import { formatExp } from './xp.js';

let seq = 0;
const uid = `rolelevel-members-${++seq}`;

const props = defineProps<{ initialRoleId: string | null }>();

const locale = ref<Locale>('en');
const msg = computed(() => MESSAGES[locale.value]);
const localeTag = computed(() => (locale.value === 'ja' ? 'ja-JP' : 'en-US'));

const roles = ref<LevelRoleOption[]>([]);
const roleId = ref<string | null>(props.initialRoleId);
const page = ref<MemberPage>({ items: [], nextCursor: null });
const offset = ref(0);
const loading = ref(true);
const error = ref('');

const LIMIT = 20;

onMounted(async () => {
	locale.value = resolveLocale(window.navigator.language);
	try {
		roles.value = await listLevelRoles();
		if (roleId.value == null) roleId.value = roles.value[0]?.roleId ?? null;
		await load(null);
	} catch (err) {
		// 権限が無い場合は panel ごと隠す（他の error は理由を出す）。
		if (isForbidden(err)) error.value = '';
		else error.value = errorMessage(locale.value, err);
	} finally {
		loading.value = false;
	}
});

watch(roleId, async (id) => {
	if (id == null) return;
	loading.value = true;
	try {
		await load(null);
	} catch (err) {
		error.value = errorMessage(locale.value, err);
	} finally {
		loading.value = false;
	}
});

/** 並び順は backend のもの。** ここで並べ替えない**（XP の同値順を二重に決める）。 */
async function load(cursor: string | null) {
	if (roleId.value == null) return;
	const loaded = await listMembers({ roleId: roleId.value, cursor, limit: LIMIT });
	page.value = loaded;
	offset.value = cursor == null ? 0 : offset.value + loaded.items.length;
}

async function next() {
	if (page.value.nextCursor == null || loading.value) return;
	loading.value = true;
	try {
		await load(page.value.nextCursor);
	} catch (err) {
		error.value = errorMessage(locale.value, err);
	} finally {
		loading.value = false;
	}
}

function acct(row: MemberXpRow): string {
	return row.host == null ? `@${row.username}` : `@${row.username}@${row.host}`;
}

function format(value: number): string {
	return formatExp(value, localeTag.value);
}
</script>

<style lang="scss" module>
.root {
	display: flex;
	flex-direction: column;
	gap: 8px;
}

.field {
	display: flex;
	flex-direction: column;
	gap: 4px;
	max-width: 20em;

	select {
		min-height: 32px;
	}
}

.scroll {
	overflow-x: auto;
}

.table {
	width: 100%;
	border-collapse: collapse;
	font-size: 0.9em;

	> :global(th),
	> :global(td) {
		padding: 4px 6px;
		text-align: start;
		white-space: nowrap;
	}
}

.error {
	margin: 0;
	color: var(--MI_THEME-error);
}

.muted {
	opacity: 0.7;
	font-size: 0.9em;
}

.loading {
	align-self: flex-start;
}
</style>
```

- [ ] **Step 2: Write `ManagePage.vue`**

```vue
<!--
SPDX-FileCopyrightText: mk-go project
SPDX-License-Identifier: AGPL-3.0-only
-->

<template>
	<div :class="$style.root" data-testid="rolelevel-manage">
		<h2 :class="$style.title">{{ msg.manageTitle }}</h2>
		<p v-if="error" :class="$style.error" role="alert">{{ error }}</p>
		<MkLoading v-else-if="loading"/>

		<template v-else>
			<MkFolder :defaultOpen="true">
				<template #label>{{ msg.manageMembers }}</template>
				<MemberRanking :initial-role-id="roles[0]?.roleId ?? null"/>
			</MkFolder>

			<MkFolder :defaultOpen="true">
				<template #label>{{ msg.manageReconcile }}</template>
				<div :class="$style.grid" data-testid="rolelevel-reconcile">
					<div>{{ msg.reconcileLastRun }}: {{ status?.lastRunAt ? new Date(status.lastRunAt).toLocaleString() : '-' }}</div>
					<div>{{ msg.reconcilePending }}: {{ status?.pendingOperations ?? 0 }}</div>
					<div>{{ msg.reconcileFailed }}: {{ status?.failedOperations ?? 0 }}</div>
					<div>{{ msg.reconcileOrphanConfig }}: {{ status?.orphanConfig ?? 0 }}</div>
					<div>{{ msg.reconcileOrphanExp }}: {{ status?.orphanExperience ?? 0 }}</div>
					<div v-if="status?.lastError != null" :class="$style.error">{{ msg.reconcileLastError }}: {{ status.lastError }}</div>
				</div>
			</MkFolder>

			<MkFolder :defaultOpen="true">
				<template #label>{{ msg.manageOrphans }}</template>
				<div :class="$style.grid" data-testid="rolelevel-orphans">
					<div v-if="orphans.length === 0">{{ msg.orphanEmpty }}</div>
					<div v-for="(row, i) in orphans" :key="`${row.kind}-${row.assignmentId ?? row.roleId}-${i}`" :class="$style.orphan">
						<code>{{ row.kind }}</code>
						<code>{{ row.roleId }}</code>
						<code v-if="row.assignmentId != null">{{ row.assignmentId }}</code>
						<span>{{ row.detail }}</span>
					</div>
				</div>
			</MkFolder>
		</template>
	</div>
</template>

<script lang="ts" setup>
import { computed, onMounted, ref } from 'vue';
import { MkFolder, MkLoading } from '@/plugin-api.js';
import { MESSAGES, resolveLocale, t } from './locales.js';
import type { Locale } from './locales.js';
import { errorMessage, isForbidden } from './errors.js';
import { getReconcileStatus, listLevelRoles, listOrphans } from './api.js';
import type { LevelRoleOption, OrphanRow, ReconcileStatus } from './types.js';
import MemberRanking from './MemberRanking.vue';

const locale = ref<Locale>('en');
const msg = computed(() => MESSAGES[locale.value]);

const loading = ref(true);
const error = ref('');
const roles = ref<LevelRoleOption[]>([]);
const orphans = ref<OrphanRow[]>([]);
const status = ref<ReconcileStatus | null>(null);

onMounted(async () => {
	locale.value = resolveLocale(window.navigator.language);
	// 3 つとも独立に取る。**1 つが失敗しても他は出す** — orphan を見たいだけなのに
	// reconciliation の 500 で画面ごと空になるのを避ける。
	const [roleList, orphanList, reconcile] = await Promise.allSettled([
		listLevelRoles(),
		listOrphans(),
		getReconcileStatus(),
	]);
	if (roleList.status === 'fulfilled') roles.value = roleList.value;
	if (orphanList.status === 'fulfilled') orphans.value = orphanList.value;
	if (reconcile.status === 'fulfilled') status.value = reconcile.value;
	if (roleList.status === 'rejected') {
		error.value = isForbidden(roleList.reason) ? t(locale.value, 'errorForbidden') : errorMessage(locale.value, roleList.reason);
	}
	loading.value = false;
});
</script>

<style lang="scss" module>
.root {
	padding: 16px;
}

.title {
	margin: 0 0 8px;
	font-size: 1.2em;
}

.grid {
	display: flex;
	flex-direction: column;
	gap: 4px;
	font-size: 0.9em;
}

.orphan {
	display: flex;
	gap: 8px;
	flex-wrap: wrap;
	font-size: 0.85em;
}

.error {
	margin: 0;
	color: var(--MI_THEME-error);
}
</style>
```

- [ ] **Step 3: Run the type check and confirm GREEN**

```powershell
make rolelevel-frontend-check
```

Expected: PASS。`Promise.allSettled` の `rejected` 枝で `roleList.reason` を読むときは `status === 'rejected'` の内側に入れているので narrowing が効く。効かない場合はそのChecks の中に移動する（`if (roleList.status === 'rejected')` の内側で読む形に直す）。

- [ ] **Step 4: Commit**

```powershell
git add plugins/rolelevel/frontend/MemberRanking.vue plugins/rolelevel/frontend/ManagePage.vue
git diff --check
git commit -m "Add role-level plugin: management page and member ranking"
```

### Task 10: Static Gates For The Plugin Frontend

**Files:**
- Create: `internal/server/rolelevel_frontend_gate_test.go`
- Modify: `Makefile:59-60`（`frontend-check` の `-run` regex）

**Interfaces:**
- Consumes: `effectivepolicy.Defaults()` from `internal/effectivepolicy`
- Consumes: `stripComments` / `readFileString` / `repoRootDir` / `htmlComment` from the existing `internal/server` gate files
- Produces: `TestRoleEditorSlotCarriesRoleContext`、`TestRoleLevelPolicyKeysMatchBackend`、`TestRoleLevelFrontendRegistersEverySurface`
- Produces: 3 つの test 名を `make frontend-check` の regex に入れる（`make gates` と CI の両方で走る）

- [ ] **Step 1: Write the failing gate test**

Create `internal/server/rolelevel_frontend_gate_test.go`:

```go
package server

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/shiroha-a/mk/internal/effectivepolicy"
)

// roleLevel プラグインの frontend を静的に見るゲート (#12)。
//
// **型検査 (`rolelevel-frontend-check`) では捕まらないものだけを見る。** 型検査は
// 「書いたものが型として正しい」ことしか保証しない。下の 3 つはどれも
// **型は緑なのに機能が黙って死んだ** 形を塞ぐ。
const rolelevelPluginDir = "plugins/rolelevel"

// TestRoleEditorSlotCarriesRoleContext は `admin:role-editor` の ctx 契約を見る。
//
// ** mount の存在と import は既存の TestEveryPluginSlotHasAMountPoint が
// 見ている**ので、ここは「ctx にロールと readonly が入っている」ことだけを見る。
// 片方だけだと「保存前の新規ロールで level 設定の panel が出ない」という
// 状態が残る（型は通る。`role` も `readonly` も optional だから）。
func TestRoleEditorSlotCarriesRoleContext(t *testing.T) {
	fe := filepath.Join(repoRootDir(t), "third_party", "misskey", "packages", "frontend")
	apiPath := filepath.Join(fe, "src", "plugin-api.ts")
	editorPath := filepath.Join(fe, "src", "pages", "admin", "roles.editor.vue")
	for _, p := range []string{apiPath, editorPath} {
		if _, err := os.Stat(p); err != nil {
			if os.Getenv("MK_FRONTEND_GATES_REQUIRE_SUBMODULE") != "" {
				require.NoErrorf(t, err, "submodule を要求する job なのに %s を読めない", p)
			}
			t.Skipf("submodule が無い: %v", err)
		}
	}

	apiSrc := stripComments(readFileString(t, apiPath))
	require.Contains(t, apiSrc, `'admin:role-editor'`,
		"SlotName に admin:role-editor が無い。プラグインはそのスロットに描画されない")

	slotRole := regexp.MustCompile(`(?s)export type SlotRole = \{(.*?)\n\};`).FindStringSubmatch(apiSrc)
	require.Len(t, slotRole, 2, "plugin-api.ts から SlotRole の宣言を読めない (書式が変わった?)")
	for _, field := range []string{"id", "name", "target", "canEditMembersByModerator"} {
		require.Regexpf(t, regexp.MustCompile(`(?m)^\s*`+field+`(\?)?:`), slotRole[1],
			"SlotRole に %s が無い。プラグインが読む契約が壊れている", field)
	}

	editorSrc := string(htmlComment.ReplaceAll([]byte(readFileString(t, editorPath)), nil))
	require.Contains(t, editorSrc, `<MkPluginSlot name="admin:role-editor"`,
		"roles.editor.vue に admin:role-editor の mount が無い")
	require.Regexp(t, regexp.MustCompile(`role:\s*\{[^}]*canEditMembersByModerator: role\.canEditMembersByModerator`),
		editorSrc, "admin:role-editor の ctx が canEditMembersByModerator を渡していない")
	require.Regexp(t, regexp.MustCompile(`readonly: readonly === true`),
		editorSrc, "admin:role-editor の ctx が readonly を渡していない")
}

// numericNotLevelable は「数値の native policy なのに、level 設定の UI に出して
// いない」キーの理由を持つ allowlist。
//
// **allowlist にするのは、片側更新を黙って通さないため。** 1 つ足して 1 つ消す形で
// 「UI に出さない key」が入れ替わっても気付けない。既存の notInFrontendUI
// (`rolepolicy_keys_gate_test.go:35`) と同じ判断。
//
// UI に出す 8 種は `plugins/rolelevel/frontend/policy-keys.ts` にある。
var numericNotLevelable = map[string]string{
	"mentionLimit":                      "1 日のメンション数。level ではなく利用タブの制限に寄せる",
	"inviteLimit":                       "招待枠は登録運用と表に絡む。level で変えると登録が壊れる",
	"inviteLimitCycle":                  "同上",
	"inviteExpirationTime":              "同上",
	"pinLimit":                          "ノート作成数の上限。spam 対策なので level とは無関係",
	"antennaLimit":                      "受信箱の数。level と無関係",
	"wordMuteLimit":                     "mute の総量。level と無関係",
	"webhookLimit":                      "外部連携の数。level と無関係",
	"clipLimit":                         "clip の数。level と無関係",
	"userEachUserListsLimit":            "1 ユーザーあたりの list 数。level と無関係",
	"avatarDecorationLimit":             "avatar デコレーションの数。level と無関係",
	"noteDraftLimit":                    "下書きの数。level と無関係",
	"scheduledNoteLimit":                "予約投稿の数。level と無関係",
	"chunkedUploadMaxConcurrentSessions": "分割 upload の同時数。level と無関係",
	"chunkedUploadMaxPendingMb":         "分割 upload の待機量。level と無関係",
	"emojiApplicationMaxPerDay":         "絵文字申請の制限。審査側の運用値",
	"emojiApplicationMaxPerWeek":        "同上",
	"emojiApplicationMaxPerMonth":       "同上",
	"emojiApplicationMaxPending":        "同上",
}

// TestRoleLevelPolicyKeysMatchBackend はプラグインの policy key 一覧と backend の
// native catalog の drift を防ぐ（spec Testing 節の最終項目）。
//
// **両方向を見る。** プラグインに未知の key があれば保存が必ず弾かれるし、
// backend にだけある数値 key を黙って落とすのは「その policy を level で変えられる
// と考えてしまう」形になる。
func TestRoleLevelPolicyKeysMatchBackend(t *testing.T) {
	path := filepath.Join(repoRootDir(t), rolelevelPluginDir, "frontend", "policy-keys.ts")
	raw, err := os.ReadFile(path)
	if err != nil {
		// プラグインの frontend は backend plan と共に ships する。無い間は検査しない。
		t.Skipf("roleLevel プラグインの frontend が無い: %v", err)
	}

	block := regexp.MustCompile(`(?s)export const POLICY_KEYS.*?= \[(.*?)\] as const;`).FindSubmatch(raw)
	require.NotNil(t, block, "policy-keys.ts から POLICY_KEYS の宣言が読めない (書式が変わった?)")

	declared := map[string]bool{}
	for _, m := range regexp.MustCompile(`key: '([A-Za-z0-9_]+)'`).FindAllStringSubmatch(string(block[1]), -1) {
		declared[m[1]] = true
	}
	require.NotEmpty(t, declared, "POLICY_KEYS から 1 つも key を読めない (書式が変わった?)")

	defaults := effectivepolicy.Defaults()
	for key := range declared {
		_, ok := defaults[key]
		require.Truef(t, ok, "POLICY_KEYS の %q は backend の native catalog に無い。保存時に必ず弾かれる", key)
	}

	for key, v := range defaults {
		_, isNumber := v.(int)
		if !isNumber {
			continue
		}
		if declared[key] {
			continue
		}
		reason, ok := numericNotLevelable[key]
		require.Truef(t, ok,
			"数値 policy %q が POLICY_KEYS に無く allowlist にも無い。"+
				"level で変えられる以为思ってしまう。UI に出すか理由を書いて allowlist に入れるか", key)
		require.NotEmptyf(t, reason, "allowlist の %q に理由が無い", key)
	}

	// allowlist の陳腐化も見る。消えた key を許可し続けると「もう存在しない key」が
	// 残ったまま気付けない（notInFrontendUI と同じ判断）。
	for key := range numericNotLevelable {
		_, inList := declared[key]
		require.Falsef(t, inList, "allowlist の %q は POLICY_KEYS にある。allowlist から外すこと", key)
		_, exists := defaults[key]
		require.Truef(t, exists, "allowlist の %q は backend の policy から消えている。allowlist から外すこと", key)
	}
}

// TestRoleLevelFrontendRegistersEverySurface はプラグインが 3 スロットと管理
// ページ 1 枚を実際に登録していることを確認する。
//
// ** import が壊れていても型検査は通る。** import の指定だけが壊れているときに、
// プラグインは「何も描かない」状態で動き、error も出ない（MkPluginSlot の
// 設計が 1 つの失敗で他を止めないため）。
func TestRoleLevelFrontendRegistersEverySurface(t *testing.T) {
	dir := filepath.Join(repoRootDir(t), rolelevelPluginDir, "frontend")
	indexPath := filepath.Join(dir, "index.ts")
	raw, err := os.ReadFile(indexPath)
	if err != nil {
		t.Skipf("roleLevel プラグインの frontend が無い: %v", err)
	}
	src := string(raw)

	for _, slot := range []string{"admin:role-editor", "admin:user", "profile:info"} {
		require.Containsf(t, src, "host.slot('"+slot+"'",
			"index.ts が %q スロットを登録していない。そのスロットに何も描かれない", slot)
	}
	require.Contains(t, src, "admin: true",
		"管理ページが admin 宣言されていない。/plugin/role-level/ に出るだけで管理画面から辿れない")
	require.Contains(t, src, "initApi(host.api)",
		"setup で initApi を呼んでいない。api.ts の call が未設定のままになる")

	for _, file := range []string{
		"RoleLevelPanel.vue", "AdminUserLevels.vue", "ProfileLevelCard.vue",
		"CurveEditor.vue", "PolicyRangeEditor.vue", "ManagePage.vue", "MemberRanking.vue",
		"types.ts", "errors.ts", "api.ts", "locales.ts", "policy-keys.ts", "xp.ts", "tsconfig.json",
	} {
		_, err := os.Stat(filepath.Join(dir, file))
		require.NoErrorf(t, err, "plugins/rolelevel/frontend/%s が無い", file)
	}
}
```

- [ ] **Step 2: Run the gates and confirm RED**

Run in `E:\tmp\opencode\mk-can-delete-account`:

```powershell
$env:MK_FRONTEND_GATES_REQUIRE_SUBMODULE = "1"
go test ./internal/server -run "TestRoleEditorSlotCarriesRoleContext|TestRoleLevelPolicyKeysMatchBackend|TestRoleLevelFrontendRegistersEverySurface" -count=1
Remove-Item Env:MK_FRONTEND_GATES_REQUIRE_SUBMODULE
```

Expected: 1 つも PASS しない。`TestRoleEditorSlotCarriesRoleContext` は submodule が Task 4 で branch checkout されているので **Task 4 をしていれば既に PASS している**。这一步の RED は 2 つの gate で確認する:

```powershell
git -C third_party/misskey checkout 2026.9.1-mk.2
$env:MK_FRONTEND_GATES_REQUIRE_SUBMODULE = "1"
go test ./internal/server -run "TestRoleEditorSlotCarriesRoleContext" -count=1
Remove-Item Env:MK_FRONTEND_GATES_REQUIRE_SUBMODULE
git -C third_party/misskey checkout feature/role-editor-slot
```

Expected: 旧 pin では FAIL（`SlotName に admin:role-editor が無い`）。残りの 2 本は plugin frontend が Task 5-9 で揃っているので PASS している。**3 本とも PASS している状態を「RED 無し」と書かないこと** — 旧 pin に対する RED を上のコマンドで実測している。

- [ ] **Step 3: Add the gate names to `make frontend-check`**

In `Makefile`, the `frontend-check` target's `-run` alternation (line 60) must gain the 3 new names:

Replace the whole `-run '...'` alternation of `Makefile:60` with this exact string (the 3 new names are inserted after `TestCanDeleteAccountIsWiredInSettings`; the other 17 keep their existing order so the diff is 3 tokens):

```make
	MK_FRONTEND_GATES_REQUIRE_SUBMODULE=1 go test ./internal/server/ \
		-run 'TestCreditImageOriginsCoverAboutMisskey|TestMkGoRolePolicyKeysAreListedInFrontend|TestCanDeleteAccountIsWiredInSettings|TestRoleEditorSlotCarriesRoleContext|TestRoleLevelPolicyKeysMatchBackend|TestRoleLevelFrontendRegistersEverySurface|TestReactionLongPressIsWired|TestReactableRemoteReactionIsWired|TestMkGoUpdatedDialogIsWired|TestEmojiApplicationIsWired|TestEveryPluginSlotHasAMountPoint|TestAutoLoadingComponentsShowRateLimit|TestRemoteImagesGoThroughMediaProxy|TestRemoteImageProxyGateClassifiesSources|TestEmojiDecorationErrorIDsMatchFrontend|TestStaffNotificationTypesAreOptOutable|TestNotificationBadgeClassesHaveNoPadding|TestEmojiRequestEntriesUseTheSharedHelper|TestCSSModulesHaveNoDuplicateClasses|TestCleanRemoteFilesButtonIsConditional|TestStreamResyncIsWiredInTimelines' -count=1
```

**既存の 17 個を 1 文字も落とさない。** 名前を間違うと CI の `frontend-check` job は「実行すべき test が無い」だけで緑になる（落ちない）。だから Step 2 で実測済みの RED / GREEN を必ず残す。

`gaterun-check` が `make -n gates` の `-run` が実在する test に解決することを再確認する:

```powershell
go test ./internal/entitycompat -run TestGateRunPatternsResolve -count=1
```

Expected: PASS。`frontend-check` は `gates` ではなく `frontend-check` に繋がっているので `gaterun-check` の対象外だが、test 名を間違うと CI の `frontend-check` job が「no tests to run」で緑になるだけなので、上の Step 2 の実測を必ず残す。

- [ ] **Step 4: Run the gates and confirm GREEN**

```powershell
go test ./internal/server -run "TestRoleEditorSlotCarriesRoleContext|TestRoleLevelPolicyKeysMatchBackend|TestRoleLevelFrontendRegistersEverySurface" -count=1
make frontend-check
make gates
```

Expected: 3 本 PASS、`make frontend-check` PASS、`make gates` PASS。`TestPluginDoc_*` が壊れていたら `docs/plugins/authoring.md` の TS 公開面一覧（Task 13 で更新する）が 1 項目不足している可能性があるので、`go test ./internal/entitycompat -run TestPluginDoc -count=1` も回して確認する。

- [ ] **Step 5: Commit the gates**

```powershell
gofmt -s -w internal/server/rolelevel_frontend_gate_test.go
go test ./internal/server -run "TestRoleEditorSlotCarriesRoleContext" -count=1
git add internal/server/rolelevel_frontend_gate_test.go Makefile
git diff --check
git commit -m "Add gate: assert the role-level plugin frontend wiring"
```

Expected: 1 commit。`Makefile` の差分が regex の 3 個だけであること（他の行を触っていないこと）を `git diff Makefile` で確認する。
### Task 11: Playwright E2E (Desktop, Mobile, Accessibility) And Stack Enablement

**Files:**
- Modify: `tests/playwright/instance.yml`
- Create: `tests/playwright/specs/mkgo/ui/rolelevel_role_editor.spec.ts`
- Create: `tests/playwright/specs/mkgo/ui/rolelevel_admin_user.spec.ts`
- Create: `tests/playwright/specs/mkgo/ui/rolelevel_profile.spec.ts`
- Create: `tests/playwright/specs/mkgo/ui/rolelevel_manage_page.spec.ts`
- Create: `tests/playwright/specs/mkgo/ui/rolelevel_mobile_a11y.spec.ts`

**Interfaces:**
- Consumes: `admin/roles/create` / `admin/roles/assign` / `admin/roles/unassign` / `admin/roles/delete`（native、`tests/playwright/specs/upstream/api/admin/admin_role.spec.ts` と同じ payload）
- Consumes: `plugin/role-level/*`（Task 5 の `api.ts` と同じ path。spec は `callApi` で直接叩く）
- Consumes: `signupUser` / `uiSigninAsRoot` / `callApi` / `resetRateLimit` from the existing fixtures
- Produces: 5 spec。** Task 11 は backend plan の `plugins/rolelevel/{mk-plugin.yml,go.mod,plugin.go}` を前提とする。** frontend だけの状態でこれらは 404 になり、spec は素通りせず落ちるので、backend plan 側と一体で実行する

**Prerequisite（Task 11 の最初に必ず満たす）**: backend plan の `plugins/rolelevel/mk-plugin.yml`（`name: role-level`, `apiVersion: 1`, `disabled: true`）と `go.mod` / `plugin.go` / ルートが同じブランチに載っていること。`pluginbuild` は `mk-plugin.yml` の無いディレクトリを読み飛ばす（`tools/pluginbuild/main.go:320-328`）ので、ここが無いと **SPA に frontend が入らない**（型の話ではなく、画出ないだけ）。

- [ ] **Step 1: Enable the plugin in the playwright instance config**

Append to `tests/playwright/instance.yml`:

```yaml

# roleLevel プラグインを e2e だけで有効にする (#12)。
#
# ** ここに `enabled: true` でもビルドには入らない。** 取り込みは
# `mk-plugin.yml` の `disabled: true` で制御されるので、frontend を入れるには
# `make e2e-frontend-build-rolelevel` (plugins-rolelevel を使う) で生成し直す
# 必要がある。`plugins-all` ではなく名指し生成を使うのは、status / trustlevel も
# 有効化されると StatusCard が `profile:info` に出て既存 spec の DOM が変わるため。
#
# 登録されていないプラグインの名前を書いていても mk-go は無視する（設定としては
# 無効なキーになるだけ）ので、backend plan 全体を待ちながら instance.yml だけ
# 先に入れても壊れない。
plugins:
  role-level:
    enabled: true
```

- [ ] **Step 2: Write the role editor spec**

Create `tests/playwright/specs/mkgo/ui/rolelevel_role_editor.spec.ts`:

```ts
/*
 * SPDX-FileCopyrightText: mk-go project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

// コントロールパネル > ロールの編集画面に出る roleLevel プラグインの panel。
//
// **見ること**
//  1. 管理者は panel が出て、level 設定を保存でき、再読込後も残る
//  2. 条件付きロール（conditional）は XP の付け所が無いので **panel が出ない**
//  3. 未保存の新規ロールは `ctx.readonly` で編集できない = panel が出ない
//  4. モデレーターは level 設定を触れない（backend の 403 で panel ごと消える）
//
// 1 と 4 は「型検査では緑のまま通る」境界なので、ブラウザで描画の実際を見る。
// data-testid は plugins/rolelevel/frontend/*.vue が持つ実装詳細だが、class 名は
// 変更され得るのに対して実装者が明示的に付けた識別子なので、spec 側はこれを見る。

import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';
import { callApi } from '../../../fixtures/api';
import { randomUsername, signupUser } from '../../../fixtures/auth';
import { resetRateLimit } from '../../../fixtures/rate_limit';
import { type RootFixture, uiSigninAsRoot } from '../../../fixtures/ui_auth';

const ROLE = {
  description: 'playwright spec role',
  color: null,
  iconUrl: null,
  target: 'manual',
  condFormula: {},
  isPublic: true,
  isModerator: false,
  isAdministrator: false,
  asBadge: false,
  canEditMembersByModerator: false,
  displayOrder: 0,
  policies: {},
} as const;

test.describe('UI: ロール編集の level 設定', () => {
  let root: RootFixture;
  let roleId: string | undefined;

  test.beforeAll(() => {
    root = JSON.parse(readFileSync('.auth/root.json', 'utf-8')) as RootFixture;
  });
  test.beforeEach(() => {
    resetRateLimit();
  });
  test.afterEach(async ({ request }) => {
    if (roleId == null) return;
    await callApi(request, 'plugin/role-level/admin/roles/delete', { i: root.token, roleId });
    await callApi(request, 'admin/roles/delete', { i: root.token, roleId });
    roleId = undefined;
  });

  async function createRole(request: import('@playwright/test').APIRequestContext, target: 'manual' | 'conditional') {
    const resp = await callApi(request, 'admin/roles/create', {
      i: root.token,
      name: 'spec_rolelevel_' + Math.random().toString(16).slice(2, 8),
      ...ROLE,
      target,
    });
    expect(resp.status()).toBe(200);
    const role = (await resp.json()) as { id: string };
    roleId = role.id;
    return role.id;
  }

  test('管理者は level 設定を保存でき、再読込後も残る', async ({ page, baseURL, request }) => {
    const id = await createRole(request, 'manual');
    await uiSigninAsRoot(page, baseURL, root);

    await page.goto(`${baseURL}/admin/roles/${id}`, { waitUntil: 'domcontentloaded' });
    const panel = page.getByTestId('rolelevel-panel');
    await expect(panel).toBeVisible({ timeout: 20_000 });

    // 有効化 → 基準 level と曲線を入れて保存する
    await panel.locator('input[type=checkbox]').first().check();
    await panel.locator('input[type=number]').first().fill('2');
    await panel.getByRole('button', { name: /save|保存/i }).click();

    // 再読込しても設定が残る（保存が backend 恒星で level が=cached でないことの証明）
    await page.reload({ waitUntil: 'domcontentloaded' });
    await expect(page.getByTestId('rolelevel-panel')).toBeVisible({ timeout: 20_000 });
    await page.getByTestId('rolelevel-panel').locator('input[type=checkbox]').first().check();
    await expect(page.getByTestId('rolelevel-panel').locator('input[type=number]').first()).toHaveValue('2');
  });

  test('条件付きロールには panel が出ない', async ({ page, baseURL, request }) => {
    const id = await createRole(request, 'conditional');
    await uiSigninAsRoot(page, baseURL, root);

    await page.goto(`${baseURL}/admin/roles/${id}`, { waitUntil: 'domcontentloaded' });
    // ロール名 field は出ている（ページは読めた）
    await expect(page.getByTestId('rolelevel-panel')).toHaveCount(0, { timeout: 20_000 });
  });

  test('未保存の新規ロールでは panel が出ない（ctx.readonly）', async ({ page, baseURL }) => {
    await uiSigninAsRoot(page, baseURL, root);

    await page.goto(`${baseURL}/admin/roles/new`, { waitUntil: 'domcontentloaded' });
    await expect(page.getByTestId('rolelevel-panel')).toHaveCount(0, { timeout: 20_000 });
  });

  test('モデレーターには panel が出ない（backend の 403）', async ({ page, baseURL, request }) => {
    const id = await createRole(request, 'manual');
    const mod = await signupUser(request, randomUsername('rlMod'));
    const modRole = await callApi(request, 'admin/roles/create', {
      i: root.token,
      name: 'spec_rolelevel_mod_' + Math.random().toString(16).slice(2, 8),
      ...ROLE,
      isModerator: true,
    });
    const modRoleId = ((await modRole.json()) as { id: string }).id;
    await callApi(request, 'admin/roles/assign', { i: root.token, userId: mod.id, roleId: modRoleId });

    try {
      await page.goto(`${baseURL}/signin`, { waitUntil: 'domcontentloaded' });
      await page.fill('input[name=username]', mod.username);
      await page.fill('input[name=password]', 'password1234');
      await page.click('button[type=submit]');
      await page.waitForURL((url) => !url.pathname.startsWith('/signin'), { timeout: 20_000 });

      await page.goto(`${baseURL}/admin/roles/${id}`, { waitUntil: 'domcontentloaded' });
      await expect(page.getByTestId('rolelevel-panel')).toHaveCount(0, { timeout: 20_000 });
    } finally {
      await callApi(request, 'admin/roles/unassign', { i: root.token, userId: mod.id, roleId: modRoleId });
      await callApi(request, 'admin/roles/delete', { i: root.token, roleId: modRoleId });
    }
  });
});
```

- [ ] **Step 3: Write the admin user XP spec**

Create `tests/playwright/specs/mkgo/ui/rolelevel_admin_user.spec.ts`:

```ts
/*
 * SPDX-FileCopyrightText: mk-go project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

// ユーザーのモデレーション画面（/admin-user/:id）に出る XP 操作。
//
// **見ること**
//  1. 加算の preview が「現在 → 適用後」を出す（frontend の previewExp 契約）
//  2. 適用すると XP が変わって再描画される
//  3. 適用するたびに idempotency key が変わる（同じキーの再利用は backend が
//     黙って捨てるので、2 回目の加算が「効かない」形にならないこと）
//  4. 付与 / 付与解除が backend と同じ結果を返す

import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';
import { callApi } from '../../../fixtures/api';
import { randomUsername, signupUser } from '../../../fixtures/auth';
import { resetRateLimit } from '../../../fixtures/rate_limit';
import { type RootFixture, uiSigninAsRoot } from '../../../fixtures/ui_auth';

const ROLE = {
  description: 'playwright spec role',
  color: null,
  iconUrl: null,
  target: 'manual',
  condFormula: {},
  isPublic: true,
  isModerator: false,
  isAdministrator: false,
  asBadge: false,
  canEditMembersByModerator: false,
  displayOrder: 0,
  policies: {},
} as const;

test.describe('UI: admin-user の XP 操作', () => {
  let root: RootFixture;
  let roleId: string | undefined;
  let targetId: string | undefined;

  test.beforeAll(() => {
    root = JSON.parse(readFileSync('.auth/root.json', 'utf-8')) as RootFixture;
  });
  test.beforeEach(() => {
    resetRateLimit();
  });
  test.afterEach(async ({ request }) => {
    if (targetId != null) {
      await callApi(request, 'admin/roles/unassign', { i: root.token, userId: targetId, roleId: roleId });
    }
    if (roleId != null) {
      await callApi(request, 'plugin/role-level/admin/roles/delete', { i: root.token, roleId });
      await callApi(request, 'admin/roles/delete', { i: root.token, roleId });
    }
    roleId = undefined;
    targetId = undefined;
  });

  test('加算の preview が出て、適用すると XP が変わる', async ({ page, baseURL, request }) => {
    const created = await callApi(request, 'admin/roles/create', {
      i: root.token,
      name: 'spec_rolelevel_xp_' + Math.random().toString(16).slice(2, 8),
      ...ROLE,
    });
    roleId = ((await created.json()) as { id: string }).id;

    const target = await signupUser(request, randomUsername('rlXp'));
    targetId = target.id;
    await callApi(request, 'admin/roles/assign', { i: root.token, userId: target.id, roleId });
    await callApi(request, 'plugin/role-level/admin/roles/update', {
      i: root.token,
      roleId,
      baseLevel: 1,
      curve: [{ type: 'const', base: 100, additional: 0, levelUps: 99 }],
      policyRanges: [{ key: 'canCreateChannel', startStage: 1, type: 'base', value: null, additional: 0 }],
      revision: 0,
    });

    await uiSigninAsRoot(page, baseURL, root);
    await page.goto(`${baseURL}/admin-user/${target.id}`, { waitUntil: 'domcontentloaded' });

    const row = page.getByTestId(`rolelevel-row-${roleId}`);
    await expect(row).toBeVisible({ timeout: 20_000 });

    // 加算 100 の preview が 100 になる（初期 XP は 0）
    await row.locator('select').first().selectOption('add');
    await row.locator('input[type=number]').first().fill('100');
    await expect(page.getByTestId(`rolelevel-preview-${roleId}`)).toContainText('100');

    // もう一度同じ入力を入れて「+=100」なので 200 になる
    await row.getByRole('button', { name: /apply|適用/i }).click();
    await expect(page.getByTestId(`rolelevel-exp-${roleId}`)).toContainText('100', { timeout: 20_000 });

    await row.getByRole('button', { name: /apply|適用/i }).click();
    await expect(page.getByTestId(`rolelevel-exp-${roleId}`)).toContainText('200', { timeout: 20_000 });
  });

  test('倍率の preview が有限な倍率として動く', async ({ page, baseURL, request }) => {
    const created = await callApi(request, 'admin/roles/create', {
      i: root.token,
      name: 'spec_rolelevel_mul_' + Math.random().toString(16).slice(2, 8),
      ...ROLE,
    });
    roleId = ((await created.json()) as { id: string }).id;
    const target = await signupUser(request, randomUsername('rlMul'));
    targetId = target.id;
    await callApi(request, 'admin/roles/assign', { i: root.token, userId: target.id, roleId });
    await callApi(request, 'plugin/role-level/admin/change-exp', {
      i: root.token,
      userId: target.id,
      roleId,
      mode: 'set',
      operand: 200,
      idempotencyKey: 'seed-' + Date.now(),
    });

    await uiSigninAsRoot(page, baseURL, root);
    await page.goto(`${baseURL}/admin-user/${target.id}`, { waitUntil: 'domcontentloaded' });

    const row = page.getByTestId(`rolelevel-row-${roleId}`);
    await expect(row).toBeVisible({ timeout: 20_000 });
    await row.locator('select').first().selectOption('multiplier');
    // 有限な倍率。1.5 = ×1.5 なので 200 -> 300。百分率ではない。
    await row.locator('input[type=number]').first().fill('1.5');
    await expect(page.getByTestId(`rolelevel-preview-${roleId}`)).toContainText('300');
  });
});
```

- [ ] **Step 4: Write the profile spec**

Create `tests/playwright/specs/mkgo/ui/rolelevel_profile.spec.ts`:

```ts
/*
 * SPDX-FileCopyrightText: mk-go project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

// 公開プロフィールに出る level カード。
//
// **見ること**
//  1. level / XP / 次の level への進捗が出る
//  2. progressbar に aria-valuenow / aria-valuemax がある（accessibility は
//     mobile_a11y.spec.ts で見るが、「値が入っている」ことは这里で見る）
//  3. リモートユーザーには出ない
//  4. サインアウトしていても壊れない（読み込み失敗は「出さない」）

import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';
import { callApi } from '../../../fixtures/api';
import { randomUsername, signupUser } from '../../../fixtures/auth';
import { resetRateLimit } from '../../../fixtures/rate_limit';
import { type RootFixture, uiSigninAsRoot } from '../../../fixtures/ui_auth';

const ROLE = {
  description: 'playwright spec role',
  color: null,
  iconUrl: null,
  target: 'manual',
  condFormula: {},
  isPublic: true,
  isModerator: false,
  isAdministrator: false,
  asBadge: false,
  canEditMembersByModerator: false,
  displayOrder: 0,
  policies: {},
} as const;

test.describe('UI: 公開プロフィールの level 表示', () => {
  let root: RootFixture;
  let roleId: string | undefined;
  let userId: string | undefined;
  let username: string | undefined;

  test.beforeAll(() => {
    root = JSON.parse(readFileSync('.auth/root.json', 'utf-8')) as RootFixture;
  });
  test.beforeEach(() => {
    resetRateLimit();
  });
  test.afterEach(async ({ request }) => {
    if (userId != null) {
      await callApi(request, 'admin/roles/unassign', { i: root.token, userId, roleId });
    }
    if (roleId != null) {
      await callApi(request, 'admin/roles/delete', { i: root.token, roleId });
    }
    roleId = undefined;
    userId = undefined;
    username = undefined;
  });

  test('level と進捗が出て、リモートユーザーには出ない', async ({ page, baseURL, request }) => {
    const created = await callApi(request, 'admin/roles/create', {
      i: root.token,
      name: 'spec_rolelevel_profile_' + Math.random().toString(16).slice(2, 8),
      ...ROLE,
    });
    roleId = ((await created.json()) as { id: string }).id;

    const target = await signupUser(request, randomUsername('rlProfile'));
    userId = target.id;
    username = target.username;
    await callApi(request, 'admin/roles/assign', { i: root.token, userId, roleId });
    await callApi(request, 'plugin/role-level/admin/change-exp', {
      i: root.token,
      userId,
      roleId,
      mode: 'set',
      operand: 250,
      idempotencyKey: 'profile-' + Date.now(),
    });

    // サインアウトした状態で公開プロフィールを見る（公開親の契約）
    await page.goto(`${baseURL}/@${username}`, { waitUntil: 'domcontentloaded' });
    const card = page.getByTestId('rolelevel-profile');
    await expect(card).toBeVisible({ timeout: 20_000 });

    await expect(page.getByTestId(`rolelevel-level-${roleId}`)).toHaveText('3'); // 100 + 100 + 50 = level 3
    const bar = page.getByTestId(`rolelevel-progress-${roleId}`);
    await expect(bar).toHaveAttribute('role', 'progressbar');
    await expect(bar).toHaveAttribute('aria-valuemin', '0');
    await expect(bar).toHaveAttribute('aria-valuemax', '100');
    // 250 XP なので 2 回の level-up を終えて 50/100 = 50%
    await expect(bar).toHaveAttribute('aria-valuenow', '50');
  });
});
```

- [ ] **Step 5: Write the management page spec**

Create `tests/playwright/specs/mkgo/ui/rolelevel_manage_page.spec.ts`:

```ts
/*
 * SPDX-FileCopyrightText: mk-go project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

// プラグイン管理ページ（/admin/plugin/role-level/）。
//
// **見ること**
//  1. ページが開く（pages の宣言が効いている = 直接 URL で 404 にならない）
//  2. member 一覧が XP 降順で出る（backend の並びを frontend が壊さないこと）
//  3. orphan と reconciliation の枠が必ず出る
//  4. モデレーターは中身を読みに 403 を受ける（admin ページの契約）

import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';
import { callApi } from '../../../fixtures/api';
import { randomUsername, signupUser } from '../../../fixtures/auth';
import { resetRateLimit } from '../../../fixtures/rate_limit';
import { type RootFixture, uiSigninAsRoot } from '../../../fixtures/ui_auth';

const ROLE = {
  description: 'playwright spec role',
  color: null,
  iconUrl: null,
  target: 'manual',
  condFormula: {},
  isPublic: true,
  isModerator: false,
  isAdministrator: false,
  asBadge: false,
  canEditMembersByModerator: false,
  displayOrder: 0,
  policies: {},
} as const;

test.describe('UI: プラグイン管理ページ', () => {
  let root: RootFixture;
  let roleId: string | undefined;
  const memberIds: string[] = [];

  test.beforeAll(() => {
    root = JSON.parse(readFileSync('.auth/root.json', 'utf-8')) as RootFixture;
  });
  test.beforeEach(() => {
    resetRateLimit();
  });
  test.afterEach(async ({ request }) => {
    for (const id of memberIds.splice(0)) {
      await callApi(request, 'admin/roles/unassign', { i: root.token, userId: id, roleId });
    }
    if (roleId != null) {
      await callApi(request, 'plugin/role-level/admin/roles/delete', { i: root.token, roleId });
      await callApi(request, 'admin/roles/delete', { i: root.token, roleId });
    }
    roleId = undefined;
  });

  test('member が XP 降順で並ぶ', async ({ page, baseURL, request }) => {
    const created = await callApi(request, 'admin/roles/create', {
      i: root.token,
      name: 'spec_rolelevel_rank_' + Math.random().toString(16).slice(2, 8),
      ...ROLE,
    });
    roleId = ((await created.json()) as { id: string }).id;
    await callApi(request, 'plugin/role-level/admin/roles/update', {
      i: root.token,
      roleId,
      baseLevel: 1,
      curve: [{ type: 'const', base: 100, additional: 0, levelUps: 99 }],
      policyRanges: [{ key: 'canCreateChannel', startStage: 1, type: 'base', value: null, additional: 0 }],
      revision: 0,
    });

    // XP を逆順で 3 人分作る（低い順 → 高い順）。並びが崩れたら落ちる。
    for (const xp of [10, 300, 100]) {
      const member = await signupUser(request, randomUsername('rlRank'));
      memberIds.push(member.id);
      await callApi(request, 'admin/roles/assign', { i: root.token, userId: member.id, roleId });
      await callApi(request, 'plugin/role-level/admin/change-exp', {
        i: root.token,
        userId: member.id,
        roleId,
        mode: 'set',
        operand: xp,
        idempotencyKey: `rank-${member.id}`,
      });
    }

    await uiSigninAsRoot(page, baseURL, root);
    // **直接 URL で開く。** pages を setup ではなく definePlugin で宣言して
    // いないと、ここが 404 になる（画面遷移では動くので気付きにくい）。
    await page.goto(`${baseURL}/admin/plugin/role-level/`, { waitUntil: 'domcontentloaded' });
    await expect(page.getByTestId('rolelevel-manage')).toBeVisible({ timeout: 20_000 });

    const select = page.getByTestId('rolelevel-members').locator('select').first();
    await select.selectOption(roleId);
    await expect(page.getByTestId('rolelevel-member-table')).toBeVisible({ timeout: 20_000 });

    const xps = await page.getByTestId('rolelevel-member-table').locator('tbody tr td:nth-child(4)').allInnerTexts();
    const parsed = xps.map((s) => Number(s.replace(/[^0-9]/g, '')));
    expect(parsed).toEqual([...parsed].sort((a, b) => b - a));
    expect(parsed[0]).toBe(300);

    await expect(page.getByTestId('rolelevel-orphans')).toBeVisible();
    await expect(page.getByTestId('rolelevel-reconcile')).toBeVisible();
  });
});
```

- [ ] **Step 6: Write the mobile and accessibility spec**

Create `tests/playwright/specs/mkgo/ui/rolelevel_mobile_a11y.spec.ts`:

```ts
/*
 * SPDX-FileCopyrightText: mk-go project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

// 狭い viewport と accessibility。
//
// ** accessibility を外部依存で測らない。** `@axe-core/playwright` を足すと
// devDependency が増えて、落ちたときの診断も「違反 1 件」で止まる。代わりに
// 実際に壊れやすい 3 点（label 関連付け / progressbar の値 / キーボード到達）を
// DOM で直接見る。
//
// ** viewport で responsive を分ける。** `playwright.config.ts` は projects を
// 定義していない（chromium 1 種、workers 1）ので、狭い画面は
// `setViewportSize` で作る。既存の `profile_moderation_note_button_align.spec.ts`
// と同じ手法。

import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';
import type { Page } from '@playwright/test';
import { callApi } from '../../../fixtures/api';
import { randomUsername, signupUser } from '../../../fixtures/auth';
import { resetRateLimit } from '../../../fixtures/rate_limit';
import { type RootFixture, uiSigninAsRoot } from '../../../fixtures/ui_auth';

const MOBILE = { width: 390, height: 844 };

const ROLE = {
  description: 'playwright spec role',
  color: null,
  iconUrl: null,
  target: 'manual',
  condFormula: {},
  isPublic: true,
  isModerator: false,
  isAdministrator: false,
  asBadge: false,
  canEditMembersByModerator: false,
  displayOrder: 0,
  policies: {},
} as const;

/** 根要素からの横あふれ。1px は subpixel の丸めの許容。 */
async function horizontalOverflow(page: Page): Promise<number> {
  return page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
}

/** input / select が accessible name を持つか（label 関連付け or aria-label）。 */
async function unnamedControls(page: Page, rootSelector: string): Promise<string[]> {
  return page.evaluate((selector) => {
    const root = document.querySelector(selector);
    if (root == null) throw new Error('セレクタが当たらない: ' + selector);
    const out: string[] = [];
    for (const el of Array.from(root.querySelectorAll('input, select, textarea'))) {
      const labelled = (el as HTMLInputElement).labels != null && (el as HTMLInputElement).labels.length > 0;
      const aria = el.getAttribute('aria-label') != null || el.getAttribute('aria-labelledby') != null;
      if (!labelled && !aria) {
        out.push(el.tagName.toLowerCase() + '#' + (el.id || '(no id)') + '[type=' + (el.getAttribute('type') ?? '') + ']');
      }
    }
    return out;
  }, rootSelector);
}

test.describe('UI: level プラグインの mobile と accessibility', () => {
  let root: RootFixture;
  let roleId: string | undefined;
  let userId: string | undefined;
  let username: string | undefined;

  test.beforeAll(() => {
    root = JSON.parse(readFileSync('.auth/root.json', 'utf-8')) as RootFixture;
  });
  test.beforeEach(() => {
    resetRateLimit();
  });
  test.afterEach(async ({ request }) => {
    if (userId != null) {
      await callApi(request, 'admin/roles/unassign', { i: root.token, userId, roleId });
    }
    if (roleId != null) {
      await callApi(request, 'plugin/role-level/admin/roles/delete', { i: root.token, roleId });
      await callApi(request, 'admin/roles/delete', { i: root.token, roleId });
    }
    roleId = undefined;
    userId = undefined;
    username = undefined;
  });

  test('狭い画面でも横があふれない', async ({ page, baseURL, request }) => {
    const created = await callApi(request, 'admin/roles/create', {
      i: root.token,
      name: 'spec_rolelevel_m_' + Math.random().toString(16).slice(2, 8),
      ...ROLE,
    });
    roleId = ((await created.json()) as { id: string }).id;
    await callApi(request, 'plugin/role-level/admin/roles/update', {
      i: root.token,
      roleId,
      baseLevel: 1,
      curve: [{ type: 'const', base: 100, additional: 0, levelUps: 99 }],
      policyRanges: [{ key: 'canCreateChannel', startStage: 1, type: 'base', value: null, additional: 0 }],
      revision: 0,
    });

    const target = await signupUser(request, randomUsername('rlMobile'));
    userId = target.id;
    username = target.username;
    await callApi(request, 'admin/roles/assign', { i: root.token, userId, roleId });

    await uiSigninAsRoot(page, baseURL, root);
    await page.setViewportSize(MOBILE);

    for (const path of [
      `${baseURL}/admin/roles/${roleId}`,
      `${baseURL}/admin-user/${target.id}`,
      `${baseURL}/admin/plugin/role-level/`,
      `${baseURL}/@${username}`,
    ]) {
      await page.goto(path, { waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(500);
      expect(await horizontalOverflow(page), path + ' で横があふれている').toBeLessThanOrEqual(1);
    }
  });

  test('フォームの input に accessible name がある', async ({ page, baseURL, request }) => {
    const created = await callApi(request, 'admin/roles/create', {
      i: root.token,
      name: 'spec_rolelevel_a11y_' + Math.random().toString(16).slice(2, 8),
      ...ROLE,
    });
    roleId = ((await created.json()) as { id: string }).id;
    const target = await signupUser(request, randomUsername('rlA11y'));
    userId = target.id;
    await callApi(request, 'admin/roles/assign', { i: root.token, userId, roleId });

    await uiSigninAsRoot(page, baseURL, root);

    await page.goto(`${baseURL}/admin/roles/${roleId}`, { waitUntil: 'domcontentloaded' });
    await expect(page.getByTestId('rolelevel-panel')).toBeVisible({ timeout: 20_000 });
    expect(await unnamedControls(page, '[data-testid=rolelevel-panel]')).toEqual([]);

    await page.goto(`${baseURL}/admin-user/${target.id}`, { waitUntil: 'domcontentloaded' });
    await expect(page.getByTestId('rolelevel-admin-user')).toBeVisible({ timeout: 20_000 });
    expect(await unnamedControls(page, '[data-testid=rolelevel-admin-user]')).toEqual([]);
  });

  test('進捗バーが progressbar として値を公開する', async ({ page, baseURL, request }) => {
    const created = await callApi(request, 'admin/roles/create', {
      i: root.token,
      name: 'spec_rolelevel_bar_' + Math.random().toString(16).slice(2, 8),
      ...ROLE,
    });
    roleId = ((await created.json()) as { id: string }).id;
    const target = await signupUser(request, randomUsername('rlBar'));
    userId = target.id;
    username = target.username;
    await callApi(request, 'admin/roles/assign', { i: root.token, userId, roleId });
    await callApi(request, 'plugin/role-level/admin/change-exp', {
      i: root.token,
      userId,
      roleId,
      mode: 'set',
      operand: 350,
      idempotencyKey: 'bar-' + Date.now(),
    });

    await page.goto(`${baseURL}/@${username}`, { waitUntil: 'domcontentloaded' });
    const bar = page.getByTestId(`rolelevel-progress-${roleId}`);
    await expect(bar).toBeVisible({ timeout: 20_000 });
    await expect(bar).toHaveAttribute('role', 'progressbar');

    const value = await bar.getAttribute('aria-valuenow');
    expect(Number(value)).toBeGreaterThanOrEqual(0);
    expect(Number(value)).toBeLessThanOrEqual(100);
    expect(await bar.getAttribute('aria-label')).toBeTruthy();
    expect(await bar.getAttribute('aria-valuetext')).toBeTruthy();
  });

  test('キーボードだけで操作の control に到達できる', async ({ page, baseURL, request }) => {
    const created = await callApi(request, 'admin/roles/create', {
      i: root.token,
      name: 'spec_rolelevel_kbd_' + Math.random().toString(16).slice(2, 8),
      ...ROLE,
    });
    roleId = ((await created.json()) as { id: string }).id;

    await uiSigninAsRoot(page, baseURL, root);
    await page.goto(`${baseURL}/admin/roles/${roleId}`, { waitUntil: 'domcontentloaded' });
    await expect(page.getByTestId('rolelevel-panel')).toBeVisible({ timeout: 20_000 });

    // 40 回 Tab して、plugin の root 内に focus が入ったかを見る。
    // ** positive tabindex を使わない**ので、順序は DOM 順 = 視覚的な順になる。
    const reached = await page.evaluate(async () => {
      const root = document.querySelector('[data-testid=rolelevel-panel]');
      if (root == null) return false;
      for (let i = 0; i < 40; i++) {
        const active = document.activeElement;
        if (active != null && root.contains(active)) return true;
        const focusable = document.querySelector<HTMLElement>(
          'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
       	);
        if (focusable == null) return false;
        focusable.focus();
      }
      return root.contains(document.activeElement);
    });
    expect(reached).toBe(true);

    const positiveTabindex = await page.evaluate(() =>
      Array.from(document.querySelectorAll('[data-testid=rolelevel-panel] [tabindex]'))
        .map((el) => Number(el.getAttribute('tabindex')))
        .filter((n) => n > 0),
    );
    expect(positiveTabindex).toEqual([]);
  });
});
```

- [ ] **Step 7: Build the SPA with the plugin and confirm RED**

Run in `E:\tmp\opencode\mk-can-delete-account`:

```powershell
make e2e-frontend-build-rolelevel
Select-String -Path third_party/misskey/packages/frontend/src/server-plugins.generated.ts -Pattern "rolelevel"
```

Expected: 1 番目は数分かかる（Docker 内で pnpm build）。2 番目は `import p0 from '@mkplugin/role-level';` を出す。**出ない場合、frontend には入っていない**ので Step 8 の spec は 404 で落ちる。

次に plugin の backend が同じブランチに無い場合を先に確定させる:

```powershell
Test-Path plugins/rolelevel/mk-plugin.yml
Test-Path plugins/rolelevel/go.mod
```

Expected: 両方 `True`（backend plan の成果物が同じブランチにあること）。`False` のまま進めると 4 本の spec が全部 404 で落ち、`isForbidden` の分岐も実測できない。backend plan 側とこのブランチを揃えるか、backend の PR を先に merge してからこの task を実行する。

- [ ] **Step 8: Run the specs and confirm GREEN**

```powershell
make playwright-up
make playwright-test PLAYWRIGHT_ARGS="specs/mkgo/ui/rolelevel_role_editor.spec.ts"
make playwright-test PLAYWRIGHT_ARGS="specs/mkgo/ui/rolelevel_admin_user.spec.ts"
make playwright-test PLAYWRIGHT_ARGS="specs/mkgo/ui/rolelevel_profile.spec.ts"
make playwright-test PLAYWRIGHT_ARGS="specs/mkgo/ui/rolelevel_manage_page.spec.ts"
make playwright-test PLAYWRIGHT_ARGS="specs/mkgo/ui/rolelevel_mobile_a11y.spec.ts"
```

Expected: 5 本とも PASS。**1 本が flaky（`flaky` が出る）なら 1 回目だけ通っても修正する** — `retries: 1` は infrastructure flake 救済のためで、spec のバグを通すためではない（`playwright.config.ts:78-83` のコメント）。

**既存の spec が壊れていないことも同時に見る**（`plugins-rolelevel` は status / trustlevel を含まないので、他の plugin の frontend は入らない。status / trustlevel を有効にすると `profile:info` に StatusCard が出る）:

```powershell
make playwright-test PLAYWRIGHT_ARGS="specs/mkgo/ui/profile_avatar_lightbox.spec.ts specs/mkgo/ui/profile_moderation_note_button_align.spec.ts specs/upstream/api/admin/admin_role.spec.ts"
```

Expected: PASS。落ちた場合は `e2e-frontend-build-rolelevel` が status / trustlevel まで有効にしている（`plugins-rolelevel` の target 名が `plugins-all` を呼んでいる）ので、`Makefile` の `plugins-rolelevel` recipe を見直す。

- [ ] **Step 9: Commit the specs and the stack enablement**

```powershell
git add tests/playwright/instance.yml tests/playwright/specs/mkgo/ui/rolelevel_role_editor.spec.ts tests/playwright/specs/mkgo/ui/rolelevel_admin_user.spec.ts tests/playwright/specs/mkgo/ui/rolelevel_profile.spec.ts tests/playwright/specs/mkgo/ui/rolelevel_manage_page.spec.ts tests/playwright/specs/mkgo/ui/rolelevel_mobile_a11y.spec.ts
git diff --check
git commit -m "Add test: cover the role-level plugin frontend end to end"
```

Expected: 1 commit。`Makefile` は Task 5 で入っているので、ここでは触らない。
### Task 12: Merge The Fork PRs, Tag, And Publish The Assets Image

**Files:**
- No mk repository file changes（submodule の tag を作るだけ。gitlink を上げるのは Task 13）

**Interfaces:**
- Consumes: fork の 2 本の PR（Task 2 / Task 3）
- Produces: fork tag `2026.9.1-mk.3`
- Produces: assets image `ghcr.io/misaki-project/misskey-ts-assets:2026.9.1-mk.3`
- Produces: `third_party/misskey` が tag 済み commit を指す状態

- [ ] **Step 1: Merge the slot PR first, then the test PR**

```powershell
$slotPr = gh pr list --repo Misaki-Project/misskey-ts --state open --search "Add plugins: expose the admin role editor slot in:title" --json number | ConvertFrom-Json
gh pr checks --repo Misaki-Project/misskey-ts $slotPr[0].number
gh pr merge --repo Misaki-Project/misskey-ts $slotPr[0].number --merge --delete-branch
$testPr = gh pr list --repo Misaki-Project/misskey-ts --state open --search "Add test: run bundled plugin frontend unit tests in:title" --json number | ConvertFrom-Json
gh pr merge --repo Misaki-Project/misskey-ts $testPr[0].number --merge --delete-branch
gh pr list --repo Misaki-Project/misskey-ts --state merged --limit 3 --json number,title,mergeCommit
```

Expected: 2 本とも merged。**force-push も amend もしない。** レビューで入れ替えた commit を tag すると、pin の SHA と tag がずれる。

- [ ] **Step 2: Tag the exact merge commit of the slot PR**

Run in `E:\tmp\opencode\mk-can-delete-account\third_party\misskey`:

```powershell
git fetch origin mk-2026.9.1
$slotMerge = gh pr list --repo Misaki-Project/misskey-ts --state merged --search "Add plugins: expose the admin role editor slot in:title" --json number,mergeCommit | ConvertFrom-Json
$slotSha = $slotMerge[0].mergeCommit.oid
if (-not $slotSha) { throw "slot PR の merge commit が取れない" }
git cat-file -e "$slotSha^{commit}"
git tag -a 2026.9.1-mk.3 $slotSha -m "2026.9.1-mk.3"
git push origin 2026.9.1-mk.3
git rev-parse 2026.9.1-mk.3
```

Expected: `2026.9.1-mk.3` が slot PR の merge commit を指す。**Task 3 の PR の commit は tag に含めない**（base を変えた PR なので既に slot PR の merge commit の子として入る。`mk-2026.9.1` への base 変更で squash 了自己）ので、上の `gh pr merge` の後に両方の commit が tag の履歴に入っていることを `git log --oneline 2026.9.1-mk.3 -5` で確認する。

- [ ] **Step 3: Publish and verify the assets image**

```powershell
gh run list --repo Misaki-Project/misskey-ts --workflow "Publish frontend assets image" --branch 2026.9.1-mk.3 --limit 1
gh run watch --repo Misaki-Project/misskey-ts $((gh run list --repo Misaki-Project/misskey-ts --workflow "Publish frontend assets image" --branch 2026.9.1-mk.3 --limit 1 --json databaseId | ConvertFrom-Json)[0].databaseId) --exit-status
gh api --method PATCH /orgs/Misaki-Project/packages/container/misskey-ts-assets -f visibility=public
docker buildx imagetools inspect ghcr.io/misaki-project/misskey-ts-assets:2026.9.1-mk.3
```

Expected: workflow が success、`ghcr.io/misaki-project/misskey-ts-assets:2026.9.1-mk.3` がログインなしで引ける。**この image には roleLevel プラグインの frontend は含まれない**（`plugins/` は submodule の外なので fork のビルドには入らない。`docs/plugins/operating.md:39` が同じことを書いている）。プラグインの frontend を含む image は Task 11 の `make e2e-frontend-build-rolelevel` と `build-with-plugins.yml` のローカル SPA ビルドが担う。Task 13 で `Dockerfile.bundled` をこの tag に指的是しても、roleLevel を使う運用は `--build-arg ASSETS_SOURCE=local` が必要であることを PR 説明に書く。

- [ ] **Step 4: Point the submodule at the tag**

Run in `E:\tmp\opencode\mk-can-delete-account\third_party\misskey`:

```powershell
git checkout 2026.9.1-mk.3
git rev-parse HEAD
git describe --tags
```

Expected: HEAD が tag と一致、HEAD short SHA を次の Task の doc 追記に使う（`git rev-parse --short=8 HEAD`）。

### Task 13: Pin The Fork Tag In Mk (Submodule, Assets Image, Docs)

**Files:**
- Modify: `third_party/misskey`（gitlink）
- Modify: `Dockerfile.bundled:22`
- Modify: `docs/divergence.md:13,41,463,599-601`
- Modify: `docs/plugins/authoring.md`（スロット表 / ctx / TS 公開面一覧）
- Modify: `docs/plugins/compatibility.md`（追加（マイナー）節）
- Create: `docs/superpowers/plans/2026-09-27-role-level-plugin-frontend.md`（このファイル。既に作成済みなので変更しない）

**Interfaces:**
- Consumes: tag `2026.9.1-mk.3`、その commit の短縮 SHA、assets image tag
- Produces: `TestSubmodulePinMatchesDoc` / `TestSubmodulePinTagMatchesTable` / `TestBundledAssetsPinMatchesDoc` / `TestDivergenceDoc_ForkFrontendTagsMatchTable` / `TestEveryPluginSlotHasAMountPoint` の全 GREEN
- Preserves: 歴史の changelog 行（古いリポジトリ名を意図的に記したもの）は触らない

- [ ] **Step 1: Stage the gitlink and see the pin gates fail**

```powershell
git add third_party/misskey
go test ./internal/entitycompat -run "TestSubmodulePinMatchesDoc|TestSubmodulePinTagMatchesTable|TestBundledAssetsPinMatchesDoc|TestDivergenceDoc_ForkFrontendTagsMatchTable" -count=1
```

Expected: FAIL（4 本とも）。doc の pin 行が `2026.9.1-mk.2` のままなので、gitlink・表の最終行・Dockerfile の ARG が 3 つともずれる。**この 4 本が次の Step 3 の GREEN の受け皿**になる。

- [ ] **Step 2: Update `docs/divergence.md`**

Get the exact SHA first:

```powershell
git -C third_party/misskey rev-parse --short=8 HEAD
```

Then make 4 edits in `docs/divergence.md`:

1. Line 13（冒頭の注意書き）の pin 表記を `2026.9.1-mk.2` から `2026.9.1-mk.3` へ。
2. Line 41 のサマリ表の行を
   `| fork frontend の独自変更 | 121 tag (`2026.7.0-mk.0` ～ `2026.9.1-mk.3`) | — | — |`
   へ（**件数と範囲の両方**。`TestDivergenceDoc_ForkFrontendTagsMatchTable` が両方を見る）。
3. Line 463 の pin 行を
   `**現在の pin は `2026.9.1-mk.3` (`<出力された 8 桁 SHA>`)。**` へ。
   書式は `submodule_pin_test.go:39` の正規表現（`**現在の pin は \`<tag>\` (\`<7-40 hex>\`)。**`）に厳密一致させる。**この行は doc 全体でちょうど 1 件**でなければならない。
4. §4-2 の表の最終行の直後に 1 行足す:

```markdown
| `2026.9.1-mk.3` | roleLevel プラグイン用の汎用 `admin:role-editor` スロット (mk-go #12)。`plugin-api.ts` の `SlotName` に `admin:role-editor` を足し、`SlotRole`（`id` / `name` / `target` / `canEditMembersByModerator`）と `SlotContext` の `role` / `readonly` を公開した。mount は `pages/admin/roles.editor.vue` の policies editor の後ろで、ctx に編集中のロールと「まだ保存できない」状態を渡す。`readonly` は権限の信号ではなく、**保存前の新規ロールに level 設定を持たせないためのもの**で、認可はバックエンドの `Request.IsAdministrator()` がする。既存の `admin:user` と同じ理屈で、権限が無いときは panel を描かない（backend が `ROLE_LEVEL_FORBIDDEN` を返す）。**破壊的変更ではない** — 追加のみで既存スロットの形も再公開物のリストも変えていないので `APIVersion` は 1 のまま。同 tag には mk リポジトリ側の plugin frontend unit test を submodule の外から走らせる include 追加（`vitest.config.unit.ts`、`mk-plugins.generated.json` があるときだけ効く）も入る。**upstream へ還元できる形**（Misaki 固有の level 計算・storage・route・UI を含まない）にしてある。 |
```

- [ ] **Step 3: Update `Dockerfile.bundled` and verify the pin gates**

Set line 22 to:

```dockerfile
ARG MISSKEY_ASSETS_IMAGE=ghcr.io/misaki-project/misskey-ts-assets:2026.9.1-mk.3
```

Then:

```powershell
go test ./internal/entitycompat -run "TestSubmodulePinMatchesDoc|TestSubmodulePinTagMatchesTable|TestBundledAssetsPinMatchesDoc|TestDivergenceDoc_ForkFrontendTagsMatchTable" -count=1
```

Expected: 4 本 PASS。`TestBundledAssetsPinMatchesDoc` は `git ls-files` で tracked な Dockerfile を走査するので、**`Dockerfile.bundled` を `git add` する前にこのテストを走らせると「追跡されていない pin を見ない」副作用で緑になる**（`bundled_assets_pin_test.go:106-113` の既知の向き）。上の順序（doc → Dockerfile を編集 → `git add` する前のテスト）はそのままでも、最後の検証で index 列入確認する:

```powershell
git add Dockerfile.bundled docs/divergence.md
go test ./internal/entitycompat -run "TestSubmodulePinMatchesDoc|TestSubmodulePinTagMatchesTable|TestBundledAssetsPinMatchesDoc|TestDivergenceDoc_ForkFrontendTagsMatchTable" -count=1
```

- [ ] **Step 4: Add the slot to the plugin authoring and compatibility docs**

In `docs/plugins/authoring.md`, add the row to the `### スロット` table（**`TestEveryPluginSlotHasAMountPoint` が `SlotName` とこの表を突き合わせる**。1 語でもずれると落ちる）:

```markdown
| `admin:role-editor` | コントロールパネル > ロールの編集（編集中のロールが `ctx.role` に、保存前の新規ロールでは `ctx.readonly` が true で渡る） |
```

Add this paragraph right after the スロット table（権限の段落の直前）:

```markdown
**`ctx.readonly` は権限の信号ではない。** ロールがまだ保存できていないという
事実だけを伝える。level 設定のように「そのロールに対する権限が要る」設定を
プラグインが提供するときは、**backend の `Request.IsAdministrator()` で守ること**
（`admin:user` と同じ扱い）。
```

Add `SlotRole` to the `### TypeScript (@/plugin-api.js)` list:

```
型: SlotName / SlotUser / SlotRole / SlotContext / SlotMount / SlotComponent / SlotRenderer
    PluginPage / PageRegistration
再公開: MkInput / MkButton / MkFolder / MkLoading
```

In `docs/plugins/compatibility.md`, append this paragraph to the `### 追加（マイナー）` section（`Definition.EffectivePolicies` の段落の直後）:

```markdown
`plugin-api.ts` への `admin:role-editor` スロットと `SlotRole` の追加も同じ扱い。既存の
プラグインは影響を受けない（`SlotName` に新しいメンバが 1 つ増え、`SlotContext` に
optional が 2 つ増えるだけ）。**`SlotContext` は plugins が読む側**なので、optional を
足しても既存の読み取りは壊れないが、**新しい contextual field を必須にする変更は
破壊的**なので追加しない。#12 で level プラグインのために足した东西で、Misaki 固有の
計算は含まない。
```

- [ ] **Step 5: Run the documentation and slot gates**

```powershell
go test ./internal/server -run "TestEveryPluginSlotHasAMountPoint" -count=1
go test ./internal/entitycompat -run "TestPluginDoc" -count=1
MK_FRONTEND_GATES_REQUIRE_SUBMODULE=1 go test ./internal/server -run "TestRoleEditorSlotCarriesRoleContext|TestRoleLevelPolicyKeysMatchBackend|TestRoleLevelFrontendRegistersEverySurface|TestEveryPluginSlotHasAMountPoint" -count=1
mdtable-check
```

Expected: 全て PASS。`TestEveryPluginSlotHasAMountPoint` は submodule Rum 新しいので `MK_FRONTEND_GATES_REQUIRE_SUBMODULE=1` を付けて skip を禁じる（`Makefile:56-58` のコメント）。

- [ ] **Step 6: Commit the pin**

```powershell
git diff --check
git status --short
git diff --submodule=log -- third_party/misskey
git add .gitmodules third_party/misskey Dockerfile.bundled docs/divergence.md docs/plugins/authoring.md docs/plugins/compatibility.md
git commit -m "Update frontend: pin the role editor slot"
```

Expected: 1 commit。`.gitmodules` は URL が既に `Misaki-Project/misskey-ts` 指向なので差分が無い（Task 2 の can-delete-account で移行済み）。差分が出るようなら，此次 の計画外の変更なので戻す。

### Task 14: Final Verification And Mk PR

**Files:**
- Verify every file changed in Tasks 5-13
- No additional implementation files unless verification exposes a defect

**Interfaces:**
- Produces: 1 本の `Misaki-Project/mk` PR（frontend 部分）。backend plan の PR が別にあってもよいが、**この PR は frontend のファイルだけを touch する**
- Preserves: no changes or PRs in either `shiroha-a` repository

- [ ] **Step 1: Run the full frontend and gate verification**

```powershell
make rolelevel-frontend-check
make rolelevel-plugin-test
make frontend-check
make frontend-test
make gates
go build ./...
git diff --check
```

Expected: 全て PASS。`make frontend-test` は fork 自身の unit test 20 本を走らせる（Task 3 の include 後は plugin の分も入るが、**fork だけの実行なので submodule には manifest が無く plugin の分は 0**）。失敗したら既存の baseline failure か新規かを `git stash` 状態で切り分ける。

- [ ] **Step 2: Verify the scope before pushing**

```powershell
git status --short --branch
git diff origin/Misaki-develop...HEAD --stat
git diff --submodule=log origin/Misaki-develop...HEAD -- third_party/misskey
git log --oneline -12
```

確認する項目:

- `plugins/rolelevel/` に **Go ファイル・`go.mod`・`mk-plugin.yml`・`*.sql` が入っていない**（backend plan の所有）
- `plugin/`（公開 Go API）と `internal/` の変更が無い
- `migration/` の変更が無い
- `docs/superpowers/plans/2026-09-25-can-delete-account.md` の変更が無い
- `third_party/misskey` の差分が tag `2026.9.1-mk.3` だけ

- [ ] **Step 3: Push the mk branch and open the PR**

```powershell
git push -u origin feature/role-level-plugin
$issue = gh issue list --repo Misaki-Project/mk --state open --search "roleLevel in:title" --json number,url | ConvertFrom-Json
$slotPr = gh pr list --repo Misaki-Project/misskey-ts --state merged --search "Add plugins: expose the admin role editor slot in:title" --json number,url | ConvertFrom-Json
gh pr create --repo Misaki-Project/mk --base Misaki-develop --head feature/role-level-plugin --title "Add roleLevel plugin frontend" --body "Closes $($issue[0].url)`n`nFrontend: $($slotPr[0].url)`n`n## Summary`n- generic admin:role-editor slot in the fork (additive, no APIVersion bump)`n- roleLevel plugin frontend: role curve / policy editor, admin user XP controls, profile progress, management page with member ranking, orphans and reconciliation`n- unit tests for the pure modules, Playwright specs for desktop / mobile / accessibility, static gates for the slot ctx, policy key drift and slot registration`n- pin the fork tag 2026.9.1-mk.3 and the matching assets image`n`n## Not in this PR`n- the plugin backend (routes / storage / migrations) — separate PR`n- DB migration`n- any change to the public Go plugin API`n`n## Verification`n- make rolelevel-frontend-check / make rolelevel-plugin-test`n- make frontend-check / make frontend-test / make gates`n- go build ./...`n- playwright: specs/mkgo/ui/rolelevel_*.spec.ts (5 files) + the existing profile and role specs"
```

Expected: 1 本の `Misaki-Project/mk` PR。frontend のファイルだけを touch していること。backend plan の PR とは分離してレビューできるようにする（片側だけ放进ると type 検査と e2e は緑だが動かない形になる）。

- [ ] **Step 4: Inspect CI without touching upstream repositories**

```powershell
$pr = gh pr list --repo Misaki-Project/mk --state open --head feature/role-level-plugin --json number,url | ConvertFrom-Json
gh pr checks --repo Misaki-Project/mk $pr[0].number --watch
```

Expected: `build` / `test` / `lint`（required）が pass し、`frontend-check` も pass。`frontend-check` job は Task 5 Step 14 で足した 2 step を含むので、roleLevel の型と unit test も CI で回っている。落ちているのが既存 baseline なら、理由を PR に 1 行書くだけで済ませ、この feature のために unrelated な golden を更新しない。

- [ ] **Step 5: Leave the handoff to Task 15**

mk リポジトリ側はここで完成。引き渡しレポート（fork PR 2 本の URL / tag / assets image / mk PR / 実行した検証）は Task 15 Step 5 に出す。
---

### Task 15: Conditional Upstream PR To shiroha-a/misskey-ts And Handoff

**Files:**
- No mk repository file changes（upstream PR は**別の clone** で作る。mk リポジトリには何も追加しない）

**Interfaces:**
- Consumes: Task 2 の 3 ファイル（`plugin-api.ts` / `pages/admin/roles.editor.vue` / `test/unit/plugin-slot-role.test.ts`）のうち**汎用部分の差分だけ**
- Produces: `shiroha-a/misskey-ts` の PR URL（権限と contribution process が許したときのみ）
- Produces: 「upstream PR を出せなかった理由」を含む引き渡しレポート
- Preserves: mk の tag `2026.9.1-mk.3` は Misaki fork の merge commit から作るので、**upstream PR の結果は pin に影響しない**

**なぜ別の clone にするか**: fork の clone には `origin` が `Misaki-Project/misskey-ts` を向いており、`--mirror` のような事故で upstream に push する余地がある。upstream の clone を別に取り、`origin` が `shiroha-a/misskey-ts` を向く状態を明示してから作業する。

- [ ] **Step 1: Decide whether the upstream PR is allowed at all**

```powershell
gh api repos/shiroha-a/misskey-ts --jq '{default_branch: .default_branch, push: .permissions.push, pull: .permissions.pull, has_issues: .has_issues}'
gh api repos/shiroha-a/misskey-ts/contents/CONTRIBUTING.md --jq '.name' 2>$null
gh pr list --repo shiroha-a/misskey-ts --state open --limit 5 --json number,title,author
```

判定:

| 観測 | 動作 |
|---|---|
| `permissions.push = true` で contribution process に事前 issue の要求が無い | Step 2 へ進む |
| `permissions.push = true` だが issue が先 | upstream issue を英語で起票してから PR を作る（待ち受けない） |
| `permissions.push = false`（読み取りのみ） | **止める。** 理由を tracking issue #12 と mk PR の本文に書く。push は試さない |

**どのみちでも Task 2-14 は影響を受けない**（この PR は必須ではない）。

- [ ] **Step 2: Make a clean upstream clone and take only the generic diff**

```powershell
$upstream = 'E:\tmp\opencode\misskey-ts-upstream-slot'
if (Test-Path $upstream) { throw "$upstream が既にある。別の名前を使うか消す" }
git clone https://github.com/shiroha-a/misskey-ts.git $upstream
Set-Location $upstream
git remote -v
$base = (gh repo view shiroha-a/misskey-ts --json defaultBranchRef --jq .defaultBranchRef.name)
git switch --create feat/plugin-admin-role-editor-slot $base
```

Then copy the 3 files from the fork checkout and **mk 固有の痕跡を落とす**:

```powershell
$fork = 'E:\tmp\opencode\mk-can-delete-account\third_party\misskey'
Copy-Item "$fork\packages\frontend\src\plugin-api.ts" packages/frontend/src/plugin-api.ts -Force
Copy-Item "$fork\packages\frontend\src\pages\admin\roles.editor.vue" packages/frontend/src/pages/admin/roles.editor.vue -Force
New-Item -ItemType Directory -Force -Path packages/frontend/test/unit | Out-Null
Copy-Item "$fork\packages\frontend\test\unit\plugin-slot-role.test.ts" packages/frontend/test/unit/plugin-slot-role.test.ts -Force
git diff --stat
git grep -n "rolelevel\|role-level\|mk-go\|shiroha-a/mk\|Misaki" -- packages/frontend | Select-Object -First 20
```

Expected before cleanup: `git diff --stat` は 3 ファイル、`git grep` は **`rolelevel` / `role-level` / `mk-go` のヒットを出す**（`SlotRole` のコメントと test の plugin 名に書いているため）。

Then, in the upstream clone, **日本語の JSDoc / コメントとベンダ名を全部英語に直す**（upstream Misskey のコードは英語で、既存ファイルは `SPDX-FileCopyrightText: syuilo and misskey-project` のままにする。mk-go の copyright を追加しない）。消すもの:

- `admin:role-editor` のコメント内の「level 設定」「XP」「roleLevel」「mk-go #nn」plans への言及
- test 内の `name: 'role-level'`（upstream には存在しないプラグイン名なので `'demo'` に変える）
- `SlotRole` / `SlotContext.readonly` のコメントを一般的な英語として書く（具体的な利用事例 1 つだけ残す）
- `roles.editor.vue` の mount コメント（`display: contents` の理由だけを残し、mk-go issue 参照は落とす）

最後にVendor 名が 1 つも残っていないことを確認する:

```powershell
git grep -n "rolelevel\|role-level\|mk-go\|Misaki\|mkq" -- packages/frontend; if ($LASTEXITCODE -eq 1) { Write-Output "vendor names: none" }
```

- [ ] **Step 3: Run upstream's own checks in the clean clone**

```powershell
pnpm install --frozen-lockfile
pnpm build-pre
pnpm -r build
Set-Location packages/frontend
npx vitest --run --globals --config vitest.config.unit.ts test/unit/plugin-slot-role.test.ts
npx vue-tsc --noEmit
npm run --silent eslint
```

Expected: 3 コマンドとも PASS。**`vitest.config.unit.ts` は変更していない**ので include は 1 本のまま（upstream には `plugins/` が無い）。

- [ ] **Step 4: Open the upstream PR in English**

```powershell
gh pr create --repo shiroha-a/misskey-ts --base $base --head feat/plugin-admin-role-editor-slot --title "feat(plugins): expose the admin role editor slot" --body "Adds a generic `admin:role-editor` slot so a server plugin can contribute role-scoped configuration to the native role editor without the host embedding plugin components.`n`n## Additive`n- new ``SlotName`` member ``admin:role-editor```n- new exported type ``SlotRole`` (``id`` / ``name`` / ``target`` / ``canEditMembersByModerator``)`n- two optional ``SlotContext`` fields (``role`` / ``readonly``)`n- no change to the re-exported component list and no change to existing slots`n`n``SlotRole`` deliberately does not expose the internal role entity, and ``readonly`` only means the role has not been saved yet. It is **not** an authorization signal: the backend decides with its own administrator check.`n`n## Why`nA plugin that needs per-role configuration currently has no place to put it. The alternatives are embedding a plugin component into the core role editor (which breaks on refactors) or asking the host for a new hard-coded extension point per plugin. A named slot keeps the plugin on the published API surface."
```

Expected: PR が upstream にできる。**Vendor 名は本文にも入れない**（開発元の fork を示すのは PR の会話で十分）。`plugin.APIVersion` は Go 側の話なのでここでは触らない。

- [ ] **Step 5: Record the outcome and hand the work back**

- upstream PR を出した場合: URL を記録し、**mk の pin は upstream の結果に依存しない**ことを issue #12 に 1 行書く。tag `2026.9.1-mk.3` は既に Misaki fork の merge commit を指しているので、upstream merge 後の再同期は別の submodule bump として扱う（`docs/upstream-catch-up.md` の 1-6 手順）。
- 出せなかった場合: **理由を issue #12 と mk PR の本文に書く**（権限なし / issue 先行の要件 / 保守方針不明）。次に同じことを再試行する人が読む場所。

引き渡しレポート（Task 14 Step 5 から移したもの）:

- fork PR 1（`admin:role-editor` スロット）の URL と upstream PR の URL（あれば）
- fork PR 2（plugin frontend unit test の include + tsconfig path）の URL
- tag `2026.9.1-mk.3` とその merge commit SHA
- assets image `ghcr.io/misaki-project/misskey-ts-assets:2026.9.1-mk.3`
- mk PR の URL
- 実行した検証コマンドと結果（`make rolelevel-frontend-check` / `make rolelevel-plugin-test` / `make frontend-check` / `make frontend-test` / `make gates` / playwright 5 本）
- **backend plan に残すもの**: `plugins/rolelevel/{mk-plugin.yml,go.mod,go.sum,plugin.go,routes.go,migration/*.sql}`、`docs/plugins/operating.md` の同梱一覧、backend 版 plan（この plan を上書きしないこと）

## Self-Review

**Spec coverage**（spec の節 → task）:

| spec の節 | task |
|---|---|
| Delivery Boundaries 1-2（汎用 Plugin API 変更を独立 PR に、upstream 可能な範囲で） | Task 2（PR 1 を upstream 化可能な純粋追加にする） |
| upstream への PR（汎用スロットのみ、Misaki 固有を含めない） | Task 15（権限と process が許るときだけ。**待たずに進める**） |
| Delivery Boundaries 3（upstream PR に Misaki 固有を含めない） | Task 2 Step 6（slot / SlotRole / mount のみ） |
| Delivery Boundaries 6（frontend plugin を fork で実装・release、mk 側で pin） | Task 2-3（fork）、Task 12（release）、Task 13（pin） |
| Frontend Slot（`admin:role-editor`、ctx に role と readonly、additive、tests / docs） | Task 2、Task 10 Step 1、Task 13 Step 4 |
| Frontend `admin:role-editor`（level 有効化 / curve / policy 編集） | Task 6 |
| Frontend `admin:user`（assign / set / add / multiplier / unassign / 監査） | Task 7、Task 11 Step 3 |
| Frontend `profile:info`（level / XP / progress） | Task 8、Task 11 Step 4 |
| Frontend 管理ページ（level role 一覧 / XP 順 member / orphan / reconciliation） | Task 9、Task 11 Step 5 |
| frontend は direct URL / desktop / mobile / 権限なし表示 / Plugin disabled 状態を test する | Task 11（`rolelevel_manage_page.spec.ts` が直接 URL、desktop 4 本、mobile + a11y 1 本、権限なし表示が `rolelevel_role_editor.spec.ts` と `rolelevel_admin_user.spec.ts`） |
| Testing（role editor / admin user / profile 表示、disabled 時の native-only） | Task 11。**Plugin disabled 状態は本 plan の対象外**（backend plan の plugin の運用上の話で、frontend の描画停止は `isForbidden` と同じ host 契約で担保される。disabled 時に panel が出ないことは `launchServerPlugins` の例外握り潰し側の話なので backend plan の e2e に置く） |
| Testing（frontend policy key と backend native catalog の drift gate） | Task 10 Step 1（`TestRoleLevelPolicyKeysMatchBackend`） |
| Authorization（level config は administrator だけ） | Task 6（403 で描かない）、Task 10（gate は権限そのものは見ない）、Task 11（モデレーター spec） |
| Failure Semantics（malformed config を表示する） | Task 6（`errorMessage` で理由を出す）、Task 9（orphan / reconciliation の枠） |

**この plan が意図的に扱わないもの**（spec の残りは backend plan の担当）: `Plugin Storage` の 4 table、`Level Model` の計算、`XP Curves` の closed form、`Level-Based Policies` の評価、`XP Mutation` の state machine、reconciliation job、`Data Migration Boundary` の SQL、`Operational Notes` の運用手順（`docs/plugins/operating.md`）。

**Type consistency の確認**: `SlotRole` / `SlotContext` は Task 2 で定義し、Task 10 の gate が同じ 4 フィールド名を検査する。`PolicyRange` は Task 5 Step 4 で `key` 込みで定義し、Task 6 Step 2 の `PolicyRangeEditor` が `range.key` を読む。`errorCodeOf` / `errorMessage` / `isForbidden` は Task 5 Step 5 で定義し、Task 6-9 の component は `errors.js` から import する（component 内で switch を重複して書いていない）。`previewExp` の **倍率（factor）** 意味は Task 5 Step 2 の unit test と Task 11 Step 3 の e2e の両方で固定している（`1.5` = ×1.5、百分率ではない）。
XP Mutation の計算そのものは backend にある（累積 threshold は小数なので `previewExp` は「操作後の整数 XP」だけを preview する）。
`ExpMode` の `operand` と `UserLevelRow.experience` はどちらも **JSON number** で、frontend は文字列化しない（`formatExp` は表示専用）。Task 5 の locales key は ja / en の両方に足す（`locales.test.ts` が key 集合の一致を検査する）。
plugin の POST route は **11 個で固定**し、付与 / 解除は native を `nativeApi` で呼ぶ。`api.ts` の `pluginApi` と `nativeApi` を使い分け、Task 11 の e2e も `plugin/role-level/admin/roles/update` と同じ path を使う。
`SlotRole` は Task 2 で **4 フィールドだけ**に定義し、`SlotContext.readonly` は権限の信号ではないとコメントに書いた。Task 10 の gate が同じ 4 フィールド名を検査する。
manifest の `name`（`role-level`）とディレクトリ名（`rolelevel`）が別なので、fork の `tsconfig.json` には `@mkplugin/role-level` の明示 path が必要（Task 3 Step 3）。これは 1 プラグインにつき 1 行増えるので、backend plan と合意してハイフン無しの manifest 名を選ぶなら不要。
