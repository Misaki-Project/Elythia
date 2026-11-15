/*
 * SPDX-FileCopyrightText: syuilo and misskey-project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import { isHttpForbidden } from './public-media.js';

export type PhotoLoadFailure = { index: number; reason: 'fetch' | 'forbidden' | 'budget' };

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
			const reason = isBudgetError ? 'budget' : isHttpForbidden(error) ? 'forbidden' : 'fetch';
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
