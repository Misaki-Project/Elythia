/*
 * SPDX-FileCopyrightText: syuilo and misskey-project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import { isHttpForbidden } from './public-media.js';

export type PhotoFailureReason = 'fetch' | 'forbidden' | 'budget' | 'gone' | 'dmca' | 'processing' | 'unreachable';
export type PhotoLoadFailure = { index: number; reason: PhotoFailureReason };

// 写真の枠の状態。error と unreachable は再試行でき、ほかの失敗は再試行しても直らない。
export type PhotoState = 'loading' | 'error' | 'forbidden' | 'limit' | 'gone' | 'dmca' | 'processing' | 'unreachable';

// サーバーが画像を取れなかった原因 (#62)。取得先での削除、こちらでの処理の失敗、通信の失敗を分ける。
const reasonByCode: Record<string, PhotoFailureReason> = {
	MEDIA_REMOVED_DMCA: 'dmca',
	MEDIA_SOURCE_GONE: 'gone',
	MEDIA_SOURCE_FORBIDDEN: 'forbidden',
	MEDIA_PROCESSING_FAILED: 'processing',
	MEDIA_SOURCE_TIMEOUT: 'unreachable',
	MEDIA_SOURCE_FAILED: 'unreachable',
};

export function photoFailureReason(error: unknown): PhotoFailureReason {
	const code = (error as { code?: unknown } | null)?.code;
	if (typeof code === 'string' && Object.hasOwn(reasonByCode, code)) return reasonByCode[code];
	return isHttpForbidden(error) ? 'forbidden' : 'fetch';
}

export function photoStateForFailure(reason: PhotoFailureReason | undefined): PhotoState | undefined {
	if (reason == null) return undefined;
	if (reason === 'fetch') return 'error';
	if (reason === 'budget') return 'limit';
	return reason;
}

export function isRetryablePhotoState(state: PhotoState | undefined): boolean {
	return state === 'error' || state === 'unreachable';
}

/** 写真の失敗をまとめて伝える文言。再試行しても直らない原因を先に出す。 */
export function photoErrorMessage(states: readonly (PhotoState | undefined)[]): string {
	if (states.includes('dmca')) return '著作権の申し立てにより、Xで削除された画像があります。';
	if (states.includes('gone')) return 'Xで削除された画像があります。';
	if (states.includes('processing')) return 'サーバーで画像を処理できませんでした。';
	if (states.includes('unreachable')) return 'Xから画像を取得できませんでした。時間をおいて再試行してください。';
	return 'メディアを読み込めませんでした。通信または容量上限を確認して再試行してください。';
}

export type PhotoLoadResult<T> = {
	items: Array<T | null>;
	bytes: number;
	failures: PhotoLoadFailure[];
};

export class PhotoBudgetExceededError extends Error {
	constructor() {
		super('photo exceeds the remaining gallery byte budget');
		this.name = 'PhotoBudgetExceededError';
	}
}

/**
 * Load only the requested original indexes, one at a time, while sharing a
 * byte budget with earlier main or quote images. Missing descriptors are
 * retained as null slots so a failed photo never shifts the gallery order.
 */
export async function loadPhotoIndexesSequentially<T extends { blob: Pick<Blob, 'size'> }>(
	descriptors: readonly { url: string }[],
	indexes: readonly number[],
	existing: readonly (T | null)[],
	initialBytes: number,
	maxBytes: number,
	loadOne: (url: string, index: number, remainingBytes: number) => Promise<T | null>,
	stopOnFetchFailure = false,
	signal?: AbortSignal,
): Promise<PhotoLoadResult<T>> {
	const items: Array<T | null> = Array.from({ length: descriptors.length }, (_, index) => existing[index] ?? null);
	const failures: PhotoLoadFailure[] = [];
	let bytes = initialBytes;

	for (const index of indexes) {
		if (signal?.aborted) break;
		if (!Number.isInteger(index) || index < 0 || index >= descriptors.length || items[index] != null) continue;
		const remaining = maxBytes - bytes;
		if (!Number.isFinite(remaining) || remaining <= 0) {
			failures.push({ index, reason: 'budget' });
			break;
		}

		let item: T | null;
		try {
			item = await loadOne(descriptors[index].url, index, remaining);
		} catch (error) {
			const isBudgetError = error instanceof PhotoBudgetExceededError ||
				(typeof error === 'object' && error != null && 'name' in error && error.name === 'PhotoBudgetExceededError');
			const reason = isBudgetError ? 'budget' : photoFailureReason(error);
			failures.push({ index, reason });
			if (reason === 'budget' || stopOnFetchFailure) break;
			continue;
		}
		if (signal?.aborted) break;
		if (item == null) {
			failures.push({ index, reason: 'fetch' });
			if (stopOnFetchFailure) break;
			continue;
		}

		const size = item.blob.size;
		if (!Number.isSafeInteger(size) || size < 0 || size > remaining) {
			failures.push({ index, reason: 'budget' });
			break;
		}
		items[index] = item;
		bytes += size;
	}

	return { items, bytes, failures };
}

/** Prefer the complete small gallery when only one side contains more than four photos. */
export function orderPhotoPreviews(mainCount: number, quoteCount: number): Array<'main' | 'quote'> {
	const mainLarge = mainCount > 4;
	const quoteLarge = quoteCount > 4;
	if (mainLarge !== quoteLarge) return mainLarge ? ['quote', 'main'] : ['main', 'quote'];
	return ['main', 'quote'];
}
