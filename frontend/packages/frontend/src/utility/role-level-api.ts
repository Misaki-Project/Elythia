/*
 * SPDX-FileCopyrightText: Misaki Project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import { apiUrl } from '@@/js/config.js';
import { $i } from '@/i.js';
import { pendingApiRequestsCount } from '@/utility/misskey-api.js';

export type RoleLevelCurveType = 'const' | 'linear' | 'exponential';
export type RoleLevelRangeType = 'base' | 'const' | 'multiplier';

export interface RoleLevelCurve {
	type: RoleLevelCurveType;
	levelUps: number;
	base: number;
	additional: number;
	exponential: number;
}

export interface RoleLevelPolicyRange {
	type: RoleLevelRangeType;
	key?: string;
	start: number;
	end: number;
	value?: unknown;
	base?: number;
	additional?: number;
}

export interface RoleLevelConfig {
	roleId: string;
	baseLevel: number;
	experienceCurve: RoleLevelCurve[];
	policyRanges: RoleLevelPolicyRange[];
	revision: number;
	updatedBy?: string;
	createdAt?: string;
	updatedAt?: string;
}

export interface RoleLevelExperience {
	currentLevel: number;
	currentLevelExp: number;
	/** 次のレベルまでの残りXP。進捗表示の分母（必要XP全体）ではない。 */
	nextLevelExp: number | null;
	totalExp: number;
	minLevel: number;
	maxLevel: number;
	progressionStage: number;
}

export interface RoleLevelUserRole {
	roleId: string;
	assignmentId?: string;
	experience: number;
	level: RoleLevelExperience;
}

export interface RoleLevelPublicProfileResponse {
	userId: string;
	roles: RoleLevelUserRole[];
}

export interface RoleLevelAdminUserResponse {
	userId: string;
	roles: RoleLevelUserRole[];
	audit: Array<Record<string, unknown>>;
	operations: Array<Record<string, unknown>>;
}

export interface RoleLevelAuditEntry {
	id: string;
	actorId: string;
	operation: string;
	roleId?: string;
	userId?: string;
	before?: unknown;
	after?: unknown;
	createdAt: string;
}

type PluginEndpoint =
	| 'plugin/role-level/admin/roles/list'
	| 'plugin/role-level/admin/roles/show'
	| 'plugin/role-level/admin/roles/update'
	| 'plugin/role-level/admin/roles/delete'
	| 'plugin/role-level/admin/users/show'
	| 'plugin/role-level/admin/change-exp'
	| 'plugin/role-level/admin/audit'
	| 'plugin/role-level/users/show'
	| 'plugin/role-level/users/profile-settings'
	| 'plugin/role-level/users/profile-hide';

export async function roleLevelApi<T>(endpoint: PluginEndpoint, body: Record<string, unknown>): Promise<T> {
	// Plugin routes are intentionally outside misskey-js' upstream endpoint map.
	// Use Bearer auth rather than misskeyApi(): that helper appends body.i, while
	// plugin request bodies are strict and reject fields outside their contract.
	pendingApiRequestsCount.value++;
	try {
		const response = await window.fetch(`${apiUrl}/${endpoint}`, {
			method: 'POST',
			body: JSON.stringify(body),
			credentials: 'omit',
			cache: 'no-cache',
			headers: {
				'Content-Type': 'application/json',
				...($i ? { Authorization: `Bearer ${$i.token}` } : {}),
			},
		});
		const result = response.status === 204 ? undefined : await response.json();
		if (!response.ok) throw result?.error ?? result;
		return result as T;
	} finally {
		pendingApiRequestsCount.value--;
	}
}

export function defaultRoleLevelConfig(roleId = ''): RoleLevelConfig {
	return {
		roleId,
		baseLevel: 1,
		experienceCurve: [{ type: 'const', levelUps: 99, base: 100, additional: 0, exponential: 1 }],
		policyRanges: [{ type: 'base', start: 1, end: 101 }],
		revision: 0,
	};
}

export function isLegacyLevelRole(role: { target: string }): boolean {
	return role.target === 'manualLevel';
}
