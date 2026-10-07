/*
 * SPDX-FileCopyrightText: syuilo and misskey-project
 * SPDX-License-Identifier: AGPL-3.0-only
 */
import fs from 'node:fs';
import { describe, expect, it } from 'vitest';

describe('HSR role policy wiring', () => {
	it('連携上限・取得間隔を通常ロールとrole-levelへ登録する', () => {
		const editor = fs.readFileSync(new URL('../../src/pages/admin/roles.policy-editor.vue', import.meta.url), 'utf8');
		const ranges = fs.readFileSync(new URL('../../src/pages/admin/roles.policy-level-ranges.vue', import.meta.url), 'utf8');
		const roles = fs.readFileSync(new URL('../../src/pages/admin/roles.editor.vue', import.meta.url), 'utf8');
		for (const key of ['hsrUidLimit', 'hsrRefreshIntervalMinutes']) {
			expect(editor).toContain(`policyKey="${key}"`);
			expect(ranges).toContain(`'${key}'`);
			expect(roles).toContain(`'${key}'`);
		}
		expect(editor).toContain("mkGoPolicyValue('hsrUidLimit', 1)");
		expect(editor).toContain("mkGoPolicyValue('hsrRefreshIntervalMinutes', 10)");
		expect(editor).toContain('Number.isInteger(value) && value >= 0 && value <= 100');
	});
});
