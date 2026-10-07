/*
 * SPDX-FileCopyrightText: Misaki Project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import type { RoleLevelExperience } from '@/utility/role-level-api.js';

// nextLevelExpは次のレベルまでの残りXP。進捗表示の分母には獲得済みXPも足す。
export function roleLevelExperienceCost(level: RoleLevelExperience): number | null {
	if (level.nextLevelExp == null) return null;
	return level.currentLevelExp + level.nextLevelExp;
}

export function roleLevelExperienceProgress(level?: RoleLevelExperience): number {
	if (level == null) return 100;
	const cost = roleLevelExperienceCost(level);
	if (cost == null) return 100;
	if (cost <= 0) return 0;
	return Math.min(100, Math.max(0, (level.currentLevelExp / cost) * 100));
}
