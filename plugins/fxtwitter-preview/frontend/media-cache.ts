/*
 * SPDX-FileCopyrightText: syuilo and misskey-project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

type Entry<T> = {
	refs: number;
	controller: AbortController;
	promise: Promise<T>;
	value?: T;
	dispose: (value: T) => void;
};

export type ResourceLease<T> = {
	promise: Promise<T>;
	release: () => void;
};

const entries = new Map<string, Entry<unknown>>();

export function retainResource<T>(
	key: string,
	load: (signal: AbortSignal) => Promise<T>,
	dispose: (value: T) => void,
): ResourceLease<T> {
	let entry = entries.get(key) as Entry<T> | undefined;
	if (entry == null) {
		const controller = new AbortController();
		entry = {
			refs: 0,
			controller,
			dispose,
			promise: Promise.resolve(undefined as T),
		};
		const created = entry;
		entry.promise = load(controller.signal).then(value => {
			created.value = value;
			if (created.refs === 0) {
				created.dispose(value);
				if (entries.get(key) === created) entries.delete(key);
			}
			return value;
		}).catch(error => {
			if (entries.get(key) === created) entries.delete(key);
			throw error;
		});
		entries.set(key, entry as Entry<unknown>);
	}
	entry.refs++;
	const retained = entry;
	let released = false;
	return {
		promise: retained.promise,
		release: () => {
			if (released) return;
			released = true;
			retained.refs--;
			if (retained.refs !== 0) return;
			retained.controller.abort();
			if (retained.value !== undefined) retained.dispose(retained.value);
			if (entries.get(key) === retained) entries.delete(key);
		},
	};
}
