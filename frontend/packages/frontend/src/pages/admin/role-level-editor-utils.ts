/*
 * SPDX-FileCopyrightText: Misaki Project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import type { RoleLevelCurve, RoleLevelPolicyRange } from '@/utility/role-level-api.js';

export type EditablePolicyRange = RoleLevelPolicyRange & {
	key: string;
	base: number;
	additional: number;
};

export function previewOffsets(length: number): number[] {
	const safeLength = Math.max(0, Math.trunc(length));
	if (safeLength <= 6) return Array.from({ length: safeLength }, (_, index) => index);
	return [0, 1, 2, safeLength - 3, safeLength - 2, safeLength - 1];
}

export function normalizedPolicyRangeCount(ranges: RoleLevelPolicyRange[], maxStage: number): number {
	if (ranges.length === 0) return 0;
	const finalEnd = Math.max(2, Math.trunc(maxStage) + 1);
	let cursor = 1;
	let count = 0;
	for (const range of [...ranges].sort((a, b) => a.start - b.start)) {
		const start = Math.max(1, Math.min(finalEnd - 1, Math.trunc(Number(range.start))));
		const end = Math.max(start + 1, Math.min(finalEnd, Math.trunc(Number(range.end))));
		// Opening the nested editor materializes uncovered levels as explicit
		// `base` (変更なし) ranges. Count those before mount as well so the
		// parent folder's suffix does not change merely by opening it.
		if (start > cursor) count++;
		const actualStart = Math.max(cursor, start);
		if (end > actualStart) {
			count++;
			cursor = end;
		}
		if (cursor >= finalEnd) break;
	}
	if (cursor < finalEnd) count++;
	return count;
}

export function moveRangeEnd(ranges: EditablePolicyRange[], index: number, inclusiveEnd: number): void {
	if (index < 0 || index >= ranges.length - 1) return;
	const current = ranges[index];
	const last = ranges.at(-1)!;
	const boundaryIndex = index + 1;
	const desiredBoundary = Math.max(boundaryIndex + 1, Math.trunc(inclusiveEnd) + 1);
	const maximumDelta = last.end - 1 - last.start;
	const delta = Math.min(maximumDelta, desiredBoundary - current.end);
	if (delta === 0) return;

	current.end += delta;
	for (let i = index + 1; i < ranges.length; i++) {
		ranges[i].start += delta;
		if (i < ranges.length - 1) ranges[i].end += delta;
	}
	// 終端を手前へ動かして現在区間の下限に達した場合は、必要な分だけ
	// 上流の境界も繰り下げる。先頭は 1 固定なので、全区間は最低1段を保つ。
	for (let i = index; i > 0; i--) {
		if (ranges[i].start < ranges[i - 1].start + 1) ranges[i].start = ranges[i - 1].start + 1;
		if (ranges[i - 1].end >= ranges[i].start) ranges[i - 1].end = ranges[i].start;
	}
	for (let i = index; i > 0; i--) {
		if (ranges[i].start >= ranges[i].end) {
			ranges[i].start = ranges[i].end - 1;
			ranges[i - 1].end = ranges[i].start;
		}
	}
}

export function moveRangeStart(ranges: EditablePolicyRange[], index: number, start: number): void {
	if (index <= 0 || index >= ranges.length) return;
	const previous = ranges[index - 1];
	const current = ranges[index];
	const boundary = Math.max(previous.start + 1, Math.min(current.end - 1, Math.trunc(start)));
	previous.end = boundary;
	current.start = boundary;
}

export function curveCost(curve: RoleLevelCurve, offset: number): number {
	if (curve.type === 'const') return Number(curve.base);
	if (curve.type === 'linear') return Number(curve.base) + Number(curve.additional) * offset;
	return Number(curve.base) + Number(curve.additional) * Math.pow(Number(curve.exponential), offset);
}

export function curveTotal(curve: RoleLevelCurve): number {
	const count = Math.max(0, Math.trunc(Number(curve.levelUps)));
	if (curve.type === 'const') return count * Number(curve.base);
	if (curve.type === 'linear') return count * Number(curve.base) + (count * (count - 1) / 2) * Number(curve.additional);
	const exponential = Number(curve.exponential);
	if (Number(curve.additional) === 0) return count * Number(curve.base);
	if (exponential === 1) return count * (Number(curve.base) + Number(curve.additional));
	return count * Number(curve.base) + Number(curve.additional) * ((Math.pow(exponential, count) - 1) / (exponential - 1));
}
