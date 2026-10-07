/*
 * SPDX-FileCopyrightText: syuilo and misskey-project
 * SPDX-License-Identifier: AGPL-3.0-only
 */
import { readFileSync, existsSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

let root = process.cwd();
while (!existsSync(resolve(root, 'locales/ja-JP.yml'))) {
	const parent = dirname(root);
	if (parent === root) throw new Error('frontendリポジトリのルートが見つかりません');
	root = parent;
}
const read = (path: string) => readFileSync(resolve(root, path), 'utf-8');

describe('HSR role policy wiring', () => {
	it('連携上限・取得間隔を通常ロールとrole-levelへ登録する', () => {
		const editor = read('packages/frontend/src/pages/admin/roles.policy-editor.vue');
		const ranges = read('packages/frontend/src/pages/admin/roles.policy-level-ranges.vue');
		const roles = read('packages/frontend/src/pages/admin/roles.editor.vue');
		for (const key of ['hsrUidLimit', 'hsrRefreshIntervalMinutes']) {
			expect(editor).toContain(`policyKey="${key}"`);
			expect(ranges).toContain(`'${key}'`);
			expect(roles).toContain(`'${key}'`);
			expect(editor).toContain(`'${key}'])`);
		}
		expect(editor).toContain("mkGoPolicyValue('hsrUidLimit', 1)");
		expect(editor).toContain("mkGoPolicyValue('hsrRefreshIntervalMinutes', 10)");
		expect(editor).toContain('Number.isInteger(value) && value >= 0 && value <= 100');
	});
});
