/*
 * SPDX-FileCopyrightText: Misaki Project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import { afterEach, describe, expect, test, vi } from 'vitest';
import { cleanup, render } from '@testing-library/vue';
import type { RoleLevelUserRole } from '@/utility/role-level-api.js';
import MkRoleLevelTooltip from '@/components/MkRoleLevelTooltip.vue';

vi.mock('@/components/MkTooltip.vue', () => ({
	default: { template: '<div><slot /></div>' },
}));

afterEach(cleanup);

function userRole(currentLevelExp: number, nextLevelExp: number | null): RoleLevelUserRole {
	return {
		roleId: 'a8n6gydyqx6l03db',
		experience: 372639,
		level: { currentLevel: 57, currentLevelExp, nextLevelExp, totalExp: 372639, minLevel: 0, maxLevel: 2000, progressionStage: 58 },
	};
}

describe('role-level tooltip rendering', () => {
	test('renders the reported progress with the full cost, not remaining XP', async () => {
		const view = render(MkRoleLevelTooltip, {
			props: { showing: true, anchorElement: document.createElement('div'), level: userRole(7839, 11) },
			global: { stubs: { Mfm: true } },
		});
		expect(view.container.textContent).toContain('Lv. 57');
		expect(view.container.textContent).toContain('(7839 / 7850)');
		expect(view.container.textContent).not.toContain('(7839 / 11)');
		expect(Number(view.getByRole('progressbar').getAttribute('aria-valuenow'))).toBeCloseTo(7839 / 7850 * 100);

		const next = userRole(0, 7900);
		next.level.currentLevel = 58;
		await view.rerender({ level: next });
		expect(view.container.textContent).toContain('Lv. 58');
		expect(view.container.textContent).toContain('(0 / 7900)');
		expect(view.getByRole('progressbar').getAttribute('aria-valuenow')).toBe('0');
	});

	test.each([0, 123])('keeps MAX display and full gauge with surplus %i', surplus => {
		const view = render(MkRoleLevelTooltip, {
			props: { showing: true, anchorElement: document.createElement('div'), level: userRole(surplus, null) },
			global: { stubs: { Mfm: true } },
		});
		expect(view.container.textContent).toContain('MAX!!');
		if (surplus > 0) expect(view.container.textContent).toContain(`+${surplus}`);
		expect(view.container.textContent).not.toContain(' / ');
		expect(view.getByRole('progressbar').getAttribute('aria-valuenow')).toBe('100');
	});
});
