/*
 * SPDX-FileCopyrightText: Misaki Project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import { describe, expect, test } from 'vitest';
import { roleLevelExperienceCost, roleLevelExperienceProgress } from '@/utility/role-level-experience.js';
import type { RoleLevelExperience } from '@/utility/role-level-api.js';

function experience(currentLevelExp: number, nextLevelExp: number | null): RoleLevelExperience {
	return { currentLevel: 57, currentLevelExp, nextLevelExp, totalExp: 372639, minLevel: 0, maxLevel: 2000, progressionStage: 58 };
}

describe('role-level experience display', () => {
	test('uses the full level cost for the reported legacy-import example', () => {
		const level = experience(7839, 11);
		expect(roleLevelExperienceCost(level)).toBe(7850);
		expect(roleLevelExperienceProgress(level)).toBeCloseTo(99.85987261146497);
		expect(level.nextLevelExp).toBe(11);
	});

	test.each([
		[0, 7850, 7850, 0],
		[7849, 1, 7850, 7849 / 7850 * 100],
		[0, 7900, 7900, 0],
		[1, 2, 3, 100 / 3],
		[0, 0, 0, 0],
	])('current %i / remaining %i gives cost %i', (current, remaining, cost, progress) => {
		const level = experience(current, remaining);
		expect(roleLevelExperienceCost(level)).toBe(cost);
		expect(roleLevelExperienceProgress(level)).toBeCloseTo(progress);
	});

	test.each([0, 123])('keeps MAX progress full with surplus %i', current => {
		const level = experience(current, null);
		expect(roleLevelExperienceCost(level)).toBeNull();
		expect(roleLevelExperienceProgress(level)).toBe(100);
	});

	test('keeps progress in bounds', () => {
		expect(roleLevelExperienceProgress()).toBe(100);
		expect(roleLevelExperienceProgress(experience(-1, 10))).toBe(0);
		expect(roleLevelExperienceProgress(experience(10, -1))).toBe(100);
	});
});
