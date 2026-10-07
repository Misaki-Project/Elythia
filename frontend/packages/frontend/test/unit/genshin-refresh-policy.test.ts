/*
 * SPDX-FileCopyrightText: syuilo and misskey-project
 * SPDX-License-Identifier: AGPL-3.0-only
 */
import { readFileSync, existsSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { describe, expect, it } from 'vitest';
import { isGenshinRefreshInterval, validGenshinRefreshMultiplier, normalizeGenshinRefreshMultiplier } from '@/utility/genshin-refresh-policy.js';

const key = 'genshinRefreshIntervalMinutes';
let root = process.cwd();
while (!existsSync(resolve(root, 'locales/ja-JP.yml'))) {
	const parent = dirname(root);
	if (parent === root) throw new Error('frontendリポジトリのルートが見つかりません');
	root = parent;
}
const read = (path: string) => readFileSync(resolve(root, path), 'utf-8');
const editor = read('packages/frontend/src/pages/admin/roles.editor.vue');
const form = read('packages/frontend/src/pages/admin/roles.policy-editor.vue').replace(/<!--[\s\S]*?-->/g, '');
const levelForm = read('packages/frontend/src/pages/admin/roles.policy-level-ranges.vue');

describe('原神の取得間隔ポリシーの管理画面配線', () => {
	it('値と優先度の両方を固有キー一覧へ含める', () => {
		expect(editor).toContain(`'${key}'`);
		expect(editor).toMatch(/for\s*\(const ROLE_POLICY of mkGoRolePolicyKeys\)/);
		expect(form).toContain(`'${key}'`);
		expect(form).toMatch(/for\s*\(const ROLE_POLICY of mkGoPolicyMetaKeys\)/);
	});
	it('既定10分とpolicyMetaを読み書きする', () => {
		expect(form).toContain(`mkGoPolicyValue('${key}', 10)`);
		expect(form).toContain(`mkGoPolicyMeta('${key}')`);
		expect(form).toContain(`v-model:policyMeta="${key}Meta"`);
		expect(form).toContain(`policyKey="${key}"`);
	});
	it('1〜1440分の整数入力を提供する', () => {
		expect(form).toMatch(new RegExp(`v-model="${key}"[^>]*type="number"[^>]*:min="1"[^>]*:max="1440"[^>]*:step="1"`));
	});
	it('不正な値をpolicyモデルへ渡さない', () => {
		for (const value of [1, 10, 1440]) expect(isGenshinRefreshInterval(value)).toBe(true);
		for (const value of [0, 1441, 1.5, NaN, Infinity, '', null, '10', true]) expect(isGenshinRefreshInterval(value)).toBe(false);
		expect(form).toContain('if (isGenshinRefreshInterval(value)) genshinRefreshIntervalModel.value = value;');
	});
	it('レベル付きロールも数値と認識して定数・倍率の結果を検証する', () => {
		expect(levelForm).toContain(`'${key}'`);
		expect(levelForm).toContain('if (isGenshinRefresh.value && !isGenshinRefreshInterval(value)) return;');
		expect(levelForm).toContain('validGenshinRefreshMultiplier(base, additional, range.start, range.end)');
		expect(validGenshinRefreshMultiplier(10, 1, 1, 5)).toBe(true);
		expect(validGenshinRefreshMultiplier(10, -1, 1, 5)).toBe(true);
		for (const [base, additional] of [[1, -1], [1440, 1], [0, 1], [NaN, 0], [10, Infinity]]) {
			expect(validGenshinRefreshMultiplier(base, additional, 1, 5)).toBe(false);
		}
	});
	it('入力のラベルをnative inputに関連付ける', () => {
		expect(form).toContain('<template #label>{{ i18n.ts._mkgoRolePolicy.genshinRefreshIntervalMinutes }}</template>');
		expect(read('packages/frontend/src/components/MkInput.vue')).toContain(':aria-labelledby="$slots.label ? `${id}-label` : undefined"');
	});
	it('範囲拡張で上限を超えた倍率を補正する', () => {
		const range = { base: 1439, additional: 1, start: 1, end: 3 };
		expect(validGenshinRefreshMultiplier(range.base, range.additional, range.start, range.end)).toBe(true);
		range.end = 4;
		normalizeGenshinRefreshMultiplier(range);
		expect(range.additional).toBe(0);
		expect(range.base).toBe(1439);
		expect(validGenshinRefreshMultiplier(range.base, range.additional, range.start, range.end)).toBe(true);
		expect(levelForm).toContain('normalizeRefreshRanges();');
	});
	it('日本語ラベルと説明を生成localeにも含める', () => {
		const source = read('locales/ja-JP.yml');
		const generated = read('packages/i18n/src/autogen/locale.ts');
		for (const suffix of ['', '_caption']) {
			expect(source).toContain(`${key}${suffix}:`);
			expect(generated).toContain(`"${key}${suffix}": string;`);
		}
	});
});
