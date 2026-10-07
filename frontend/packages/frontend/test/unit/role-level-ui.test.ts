/*
 * SPDX-FileCopyrightText: Misaki Project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import { describe, expect, test } from 'vitest';
import { permissions } from 'misskey-js';
import { existsSync, readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { defaultRoleLevelConfig } from '@/utility/role-level-api.js';
import { curveCost, curveTotal, moveRangeEnd, moveRangeStart, normalizedPolicyRangeCount, previewOffsets } from '@/pages/admin/role-level-editor-utils.js';
import type { EditablePolicyRange } from '@/pages/admin/role-level-editor-utils.js';

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
	throw new Error('repository root not found');
}

const root = findRepoRoot();
const read = (path: string) => readFileSync(resolve(root, ...path.split('/')), 'utf8');

describe('role-level default editor state', () => {
	test('matches the backend default curve and complete policy range', () => {
		expect(defaultRoleLevelConfig('role')).toEqual({
			roleId: 'role',
			baseLevel: 1,
			experienceCurve: [{ type: 'const', levelUps: 99, base: 100, additional: 0, exponential: 1 }],
			policyRanges: [{ type: 'base', start: 1, end: 101 }],
			revision: 0,
		});
	});
});

describe('role-level editor calculations', () => {
	test('samples all short ranges and both ends of long ranges', () => {
		expect(previewOffsets(6)).toEqual([0, 1, 2, 3, 4, 5]);
		expect(previewOffsets(9)).toEqual([0, 1, 2, 6, 7, 8]);
	});

	test('counts materialized no-change gaps before the policy folder opens', () => {
		expect(normalizedPolicyRangeCount([], 10)).toBe(0);
		expect(normalizedPolicyRangeCount([
			{ type: 'base', key: 'pinLimit', start: 1, end: 11 },
		], 10)).toBe(1);
		expect(normalizedPolicyRangeCount([
			{ type: 'const', key: 'pinLimit', start: 2, end: 5, value: 10 },
		], 10)).toBe(3);
		expect(normalizedPolicyRangeCount([
			{ type: 'const', key: 'pinLimit', start: 1, end: 5, value: 10 },
		], 10)).toBe(2);
	});

	test('keeps adjacent ranges contiguous when an end moves', () => {
		const ranges = [
			{ type: 'const', key: 'pinLimit', start: 1, end: 4, base: 0, additional: 0 },
			{ type: 'base', key: 'pinLimit', start: 4, end: 7, base: 0, additional: 0 },
			{ type: 'const', key: 'pinLimit', start: 7, end: 11, base: 0, additional: 0 },
		] satisfies EditablePolicyRange[];
		moveRangeEnd(ranges, 0, 5);
		expect(ranges.map(range => [range.start, range.end])).toEqual([[1, 6], [6, 9], [9, 11]]);
		moveRangeStart(ranges, 1, 3);
		expect(ranges.map(range => [range.start, range.end])).toEqual([[1, 3], [3, 9], [9, 11]]);
	});

	test('cascades an end reduction through ranges that reached their minimum width', () => {
		const ranges = [
			{ type: 'const', key: 'pinLimit', start: 1, end: 4, base: 0, additional: 0 },
			{ type: 'base', key: 'pinLimit', start: 4, end: 7, base: 0, additional: 0 },
			{ type: 'const', key: 'pinLimit', start: 7, end: 11, base: 0, additional: 0 },
		] satisfies EditablePolicyRange[];
		moveRangeEnd(ranges, 1, 2);
		expect(ranges.map(range => [range.start, range.end])).toEqual([[1, 2], [2, 3], [3, 11]]);
	});

	test('matches constant, linear, and exponential curve formulas', () => {
		expect(curveCost({ type: 'const', levelUps: 3, base: 10, additional: 0, exponential: 1 }, 2)).toBe(10);
		expect(curveTotal({ type: 'linear', levelUps: 3, base: 10, additional: 2, exponential: 1 })).toBe(36);
		expect(curveTotal({ type: 'exponential', levelUps: 3, base: 10, additional: 2, exponential: 2 })).toBe(44);
	});
});

describe('role-level UI integration points', () => {
	test('API key generation exposes the dedicated XP permission in the administrator group', () => {
		const scope = 'write:admin:role-level-experience';
		expect(permissions).toContain(scope);
		expect(permissions.filter(permission => permission === scope)).toHaveLength(1);
		expect(read('locales/ja-JP.yml')).toContain('"write:admin:role-level-experience": "ロールの経験値を変更する"');
		expect(read('packages/frontend/src/components/MkTokenGenerateWindow.vue')).toContain("p.startsWith('write:admin')");
	});
	test('plugin adapter keeps credentials out of strict request bodies', () => {
		const source = read('packages/frontend/src/utility/role-level-api.ts');
		expect(source).toContain('Authorization: `Bearer ${$i.token}`');
		expect(source).not.toContain('return misskeyApi(');
	});

	test.each([
		['admin role category', 'packages/frontend/src/pages/admin/roles.vue', 'plugin/role-level/admin/roles/list'],
		['role editor', 'packages/frontend/src/pages/admin/roles.edit.vue', 'plugin/role-level/admin/roles/update'],
		['profile badge', 'packages/frontend/src/pages/user/home.vue', 'plugin/role-level/users/show'],
		['admin user editor', 'packages/frontend/src/pages/admin-user.vue', 'plugin/role-level/admin/change-exp'],
		['profile visibility', 'packages/frontend/src/pages/settings/other.vue', 'plugin/role-level/users/profile-hide'],
	])('%s calls its plugin endpoint', (_name, path, endpoint) => {
		expect(read(path)).toContain(endpoint);
	});

	test('explore includes restored legacy level roles', () => {
		expect(read('packages/frontend/src/pages/explore.roles.vue')).toContain("(x.target as string) === 'manualLevel'");
	});

	test('the admin user page distinguishes deleted and suspended users', () => {
		const source = read('packages/frontend/src/pages/admin-user.vue');
		expect(source).toContain('v-if="deleted"');
		expect(source).toContain('v-if="suspended"');
	});

	test('profile hiding relies on the backend-filtered native role list', () => {
		const source = read('packages/frontend/src/pages/user/home.vue');
		expect(source).toContain('v-for="role in user.roles"');
		expect(source).not.toContain('hiddenRoleIds');
		expect(read('packages/frontend/src/utility/role-level-api.ts')).not.toContain('hiddenRoleIds');
	});

	test('profile level tooltip has progress, MAX overflow, and MFM description', () => {
		const source = read('packages/frontend/src/components/MkRoleLevelTooltip.vue');
		expect(source).toContain('MAX!!');
		expect(source).toContain('currentLevelExp');
		expect(source).toContain('nextLevelExp');
		expect(source).toContain('<Mfm');
		expect(source).toContain('role="progressbar"');
		expect(source).toContain('/ {{ levelCost }}');
		expect(source).not.toContain('/ {{ level.level.nextLevelExp }}');
		expect(source).toContain('roleLevelExperienceProgress(props.level?.level)');
	});

	test('admin user accessibility text uses the same full level cost as the tooltip', () => {
		const source = read('packages/frontend/src/pages/admin-user.role-level-value.vue');
		expect(source).toContain('roleLevelExperienceCost(props.level.level)');
		expect(source).toContain('next: levelCost');
		expect(source).not.toContain('next: props.level.level.nextLevelExp');
	});

	test('level policy ranges are attached to each native policy editor', () => {
		const editor = read('packages/frontend/src/pages/admin/roles.policy-editor.vue');
		expect(editor.match(/policyKey="/g)?.length).toBeGreaterThanOrEqual(50);
		expect(editor).toContain('policyKey="canDeleteAccount"');
		expect(read('packages/frontend/src/pages/admin/roles.policy-editor.folder.vue')).toContain('<XPolicyLevelRanges');
		expect(read('packages/frontend/src/pages/admin/roles.level-editor.vue')).not.toContain('policyKey');
	});

	test('level policy UI uses typed values, inclusive ends, and impact previews', () => {
		const folder = read('packages/frontend/src/pages/admin/roles.policy-editor.folder.vue');
		const ranges = read('packages/frontend/src/pages/admin/roles.policy-level-ranges.vue');
		expect(folder.indexOf('<XPolicyLevelRanges')).toBeLessThan(folder.indexOf('<MkRange v-if="!isBaseRole'));
		expect(folder).toContain('policyRangeCount');
		expect(folder).toContain('normalizedPolicyRangeCount');
		expect(ranges).toContain("range.end - 1");
		expect(ranges).toContain('stageToLevel(range.start)');
		expect(ranges).toContain('levelToStage(Number(value))');
		expect(ranges).toContain("range.type === 'base'");
		expect(ranges).toContain("policyKind === 'boolean'");
		expect(ranges).toContain("policyKey === 'optOutNotificationTypes'");
		expect(ranges).toContain('toggleStringSetValue');
		expect(ranges).toContain('impactInformation');
		expect(ranges).toContain('WeakMap<EditablePolicyRange');
		expect(ranges).not.toContain('JSON.parse');
	});

	test('experience curve shows its inputs in one row and cumulative previews', () => {
		const source = read('packages/frontend/src/pages/admin/roles.level-editor.vue');
		expect(source).toContain('$style.curveValues');
		expect(source).toContain('experienceSimulationMax');
		expect(source).toContain('curveTotal');
	});

	test('profile visibility action is an icon-only square button', () => {
		const source = read('packages/frontend/src/pages/settings/other.vue');
		expect(source).toContain('iconOnly');
		expect(source).not.toContain("\n\t\t\t\t\t\t\t\tsmall\n");
	});

	test('admin user exposes set, signed adjustment, and multiply XP actions directly', () => {
		const source = read('packages/frontend/src/pages/admin-user.vue');
		expect(source).toContain("changeRoleExperience(roleLevelByRoleId.get(role.id)!, 'set')");
		expect(source).toContain("changeRoleExperience(roleLevelByRoleId.get(role.id)!, 'add')");
		expect(source).not.toContain("'subtract'");
		expect(source).toContain('>±</button>');
		expect(source).toContain('>＊</button>');
		expect(source).toContain("changeRoleExperience(roleLevelByRoleId.get(role.id)!, 'multiplier')");
	});

	test('admin level hover reuses the profile tooltip without its description', () => {
		const value = read('packages/frontend/src/pages/admin-user.role-level-value.vue');
		expect(value).toContain("import('@/components/MkRoleLevelTooltip.vue')");
		expect(value).toContain('showDescription: false');
		expect(value).toContain('tabindex="0"');
		expect(value).toContain('@focus="showKeyboardTooltip"');
		expect(value).toContain('@blur="hideKeyboardTooltip"');
		expect(read('packages/frontend/src/pages/admin-user.vue')).toContain('<XRoleLevelValue');
		expect(read('packages/frontend/src/components/MkRoleLevelTooltip.vue')).toContain('showDescription: true');
	});

	test('moderation log loads plugin configuration snapshots', () => {
		const source = read('packages/frontend/src/pages/admin/modlog.ModLog.vue');
		expect(source).toContain("'plugin/role-level/admin/audit'");
		expect(source).toContain('roleLevelAudit.before');
		expect(source).toContain('roleLevelAudit.after');
	});
});
