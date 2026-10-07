/*
 * SPDX-FileCopyrightText: syuilo and misskey-project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

/**
 * `canPurgeAccount` を管理画面の policy 編集 UI へ出す配線 (#2898 / #2900 系)。
 *
 * **なぜ静的スキャンなのか。** `roles.policy-editor.vue` は `MkFolder` / `MkSwitch` を
 * 大量に composing する管理ページで、frontend の unit test に Vue コンポーネントを
 * mount する harness が無い (`@vue/test-utils` 未導入、`test/unit` に `.vue` を
 * 読む既存の前例も無い)。backend 側の `internal/server/rolepolicy_keys_gate_test.go`
 * と同じ主旨で、**配線されていないと「枠も無い / 編集も無い / 保存もされない」の
 * まま緑になる**ことを検出する。
 *
 * **型検査では捕まらない。** autogen 型が無いキーは `Misskey.rolePolicies` にも無いので、
 * 1 箇所だけ落としても vue-tsc は通る。実際 #2898 は `roles.editor.vue` を直して
 * `roles.policy-editor.vue` に同じループを残し、#2900 はキー一覧に載せても編集
 * フォームが無い状態で保存済み override が開けなくなった。ここでは「一覧」
 * 「ループ」「フォーム」を別々に要求する。
 */

import { describe, expect, test } from 'vitest';
import { existsSync, readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { accountDeletionPolicyDefaults } from '@/utility/account-delete-policy.js';

const KEY = 'canPurgeAccount';

/**
 * monorepo のルートを探す。
 *
 * **`new URL(…, import.meta.url)` は書かない。** Vite の asset プラグインが
 * `new URL()` の第 1 引数を静的に解析し、動的テンプレートだと `undefined` に
 * 書き換えるため、この書き方だと path が `…/test/unit/undefined` になる (実測)。
 * 足跡 (`locales/`) をたどってルートを決め、変換に依存させない。
 */
function findRepoRoot(): string {
	for (const start of [process.cwd(), dirname(fileURLToPath(import.meta.url))]) {
		let dir = start;
		for (;;) {
			if (existsSync(resolve(dir, 'locales', 'ja-JP.yml'))) return dir;
			const parent = dirname(dir);
			if (parent === dir) break;
			dir = parent;
		}
	}
	throw new Error('monorepo のルート (locales/ja-JP.yml を含むディレクトリ) が見つからない');
}

const repoRoot = findRepoRoot();

const readRepoFile = (relative: string): string => readFileSync(resolve(repoRoot, ...relative.split('/')), 'utf-8');

const htmlComment = /<!--[\s\S]*?-->/g;

const rolesEditor = readRepoFile('packages/frontend/src/pages/admin/roles.editor.vue');
const policyEditor = readRepoFile('packages/frontend/src/pages/admin/roles.policy-editor.vue');
const otherSettings = readRepoFile('packages/frontend/src/pages/settings/other.vue');
const deletePolicy = readRepoFile('packages/frontend/src/utility/account-delete-policy.ts');
const autogenLocale = readRepoFile('packages/i18n/src/autogen/locale.ts');

/**
 * `const NAME: string[] = [...Misskey.rolePolicies, ...]` を読み取り、
 * 「リテラルの一覧」と「実際に回しているループ」を返す。
 *
 * **リテラルの存在だけでは足りない。** 宣言だけ残してループを
 * `Misskey.rolePolicies` に戻すと、固有 policy の meta が毎回落ちて
 * 「ベース値を使用」表示になり、再編集で priority が黙って 0 に戻る
 * (#2898 の実症状)。だからループの有無も必須にする。
 */
function parseMkGoKeyList(src: string, name: string): { keys: string[]; looped: boolean } {
	const decl = new RegExp(`const\\s+${name}\\s*:[^=]*=\\s*\\[\\s*\\.\\.\\.Misskey\\.rolePolicies\\s*,([^\\]]*)\\]`).exec(src);
	if (decl === null) {
		throw new Error(`\`const ${name} = [...Misskey.rolePolicies, ...]\` の宣言が見つからない (書式が変わった?)`);
	}
	const loop = new RegExp(`for\\s*\\(\\s*const\\s+[A-Za-z0-9_]+\\s+of\\s+${name}\\b`).test(src);
	const keys = [...decl[1].matchAll(/'([A-Za-z0-9_]+)'/g)].map(m => m[1]);
	return { keys, looped: loop };
}

/** `XFolder v-if="matchQuery([..., 'key'])"` が対象にするキーを集める。 */
function parsePolicyEditorFolderKeys(src: string): string[] {
	return [...src.replace(htmlComment, '').matchAll(/matchQuery\(\[[^\]]*?'([A-Za-z0-9_]+)'\s*\]\)/g)].map(m => m[1]);
}

/** `_mkgoRolePolicy:` ブロックだけを切り出す (locale 内の並び順を固定せずに読むため)。 */
function mkgoRolePolicyBlock(yml: string): string {
	const lines = yml.split(/\r?\n/);
	const start = lines.findIndex(line => line === '_mkgoRolePolicy:');
	if (start < 0) throw new Error('`_mkgoRolePolicy:` が見つからない (セクション名が変わった？)');
	const rest = lines.slice(start + 1);
	const end = rest.findIndex(line => /^[^\s#]/.test(line));
	return (end < 0 ? rest : rest.slice(0, end)).join('\n');
}

describe('canPurgeAccount is editable in the role policy admin UI', () => {
	test('is listed in roles.editor.vue and that list is the one being looped', () => {
		const { keys, looped } = parseMkGoKeyList(rolesEditor, 'mkGoRolePolicyKeys');
		expect(keys).toContain(KEY);
		// 宣言だけ残してループを戻すと、管理画面から設定できなくなる。
		expect(looped).toBe(true);
	});

	test('is listed in the roles.policy-editor.vue meta keys and that list is looped', () => {
		const { keys, looped } = parseMkGoKeyList(policyEditor, 'mkGoPolicyMetaKeys');
		expect(keys).toContain(KEY);
		expect(looped).toBe(true);
	});

	test('is bound to a value with the backend default of true', () => {
		// 既定は backend の internal/effectivepolicy/validation.go と揃えること。
		// ここがずれると「保存した値が無い role が別人の既定を継承する」。
		expect(accountDeletionPolicyDefaults.canPurgeAccount).toBe(true);
		expect(policyEditor).toMatch(new RegExp(`mkGoPolicyValue\\('${KEY}',\\s*accountDeletionPolicyDefaults\\.${KEY}\\)`));
	});

	test('is bound to a policyMeta (useDefault / priority)', () => {
		expect(policyEditor).toMatch(new RegExp(`mkGoPolicyMeta\\('${KEY}'\\)`));
	});

	test('has an XFolder editor form bound to the meta', () => {
		const src = policyEditor.replace(htmlComment, '');
		// 検索語 = キー名で書く前提 (backend gate と同じ前提)。
		expect(src).toMatch(new RegExp(`matchQuery\\(\\[i18n\\.ts\\._mkgoRolePolicy\\.${KEY},\\s*'${KEY}'\\]\\)`));
		expect(src).toMatch(new RegExp(`v-model:policyMeta="${KEY}Meta"`));
	});

	test('is not searched under a different key name', () => {
		// upstream のように検索語だけ単数/複数で書くと、検索で出ない枠になる。
		expect(parsePolicyEditorFolderKeys(policyEditor)).toContain(KEY);
	});
});

describe('canPurgeAccount locale entries', () => {
	test('ja-JP has a label and a caption under _mkgoRolePolicy', () => {
		// ja-JP だけが手で編集する配信元。他言語を要求すると Crowdin を待つことになる。
		const block = mkgoRolePolicyBlock(readRepoFile('locales/ja-JP.yml'));
		for (const suffix of ['', '_caption']) {
			expect(block, `ja-JP に _mkgoRolePolicy.${KEY}${suffix} が無い`).toMatch(
				new RegExp(`^\\s*${KEY}${suffix}:\\s*"\\S[^"]*"\\s*$`, 'm'),
			);
		}
	});

	test('is present in the regenerated locale interface', () => {
		// autogen は locales から再生成する。手で足しても一時的には通るが、
		// 再生成で消える = 実装漏れなので、成果物にも出ていることを要求する。
		expect(autogenLocale).toMatch(new RegExp(`"${KEY}":\\s*string;`));
		expect(autogenLocale).toMatch(new RegExp(`"${KEY}_caption":\\s*string;`));
	});
});

describe('self-service account deletion stays gated on canDeleteAccount only', () => {
	test('the settings guard still reads canDeleteAccount and not canPurgeAccount', () => {
		const src = otherSettings.replace(htmlComment, '');
		expect(src).toMatch(/import \{ shouldShowAccountDeletionSection \} from '@\/utility\/account-delete-policy\.js';/);
		expect(src).toMatch(/<MkFolder\s+v-if="shouldShowAccountDeletionSection\(\$i\.policies, \$i\.isDeleted\)"/);
		// purge を「消せるか」に混ぜない。canPurgeAccount が false でも
		// 入口 (自己削除の表示) の見え方は変わらない = 自己削除の表示と実行は別決定。
		expect(src).not.toContain(KEY);
	});

	test('the policy helper does not read canPurgeAccount', () => {
		const helper = deletePolicy.match(/export function isAccountDeletionAllowed\([^]*?\n\}/)?.[0];
		expect(helper).toBeDefined();
		expect(helper).toContain('canDeleteAccount === true');
		expect(helper).not.toContain(KEY);
	});
});
