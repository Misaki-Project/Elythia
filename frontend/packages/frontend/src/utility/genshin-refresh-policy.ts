/*
 * SPDX-FileCopyrightText: syuilo and misskey-project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

export function isGenshinRefreshInterval(value: unknown): value is number {
	return typeof value === 'number' && Number.isInteger(value) && value >= 1 && value <= 1440;
}

export function validGenshinRefreshMultiplier(base: number, additional: number, start: number, end: number): boolean {
	return Number.isFinite(base) && Number.isFinite(additional) && end > start &&
		[0, end - start - 1].every(offset => isGenshinRefreshInterval(Math.floor(base + additional * offset)));
}

export function normalizeGenshinRefreshMultiplier(range: { base: number; additional: number; start: number; end: number }): void {
	if (validGenshinRefreshMultiplier(range.base, range.additional, range.start, range.end)) return;
	// 範囲変更で上限を超えたら、範囲先頭の値を維持して加算を止める。
	range.base = Number.isFinite(range.base) ? Math.max(1, Math.min(1440, Math.floor(range.base))) : 10;
	range.additional = 0;
}
