/*
 * SPDX-FileCopyrightText: Elythia-Network
 * SPDX-License-Identifier: AGPL-3.0-only
 */

type Api = <T>(endpoint: string, params?: Record<string, unknown>, options?: { signal?: AbortSignal }) => Promise<T>;

let call: Api | null = null;

/** Called once from the plugin's setup. */
export function initApi(fn: Api): void {
	call = fn;
}

type Instance = { host: string };

/**
 * Suggests known servers whose host contains query. 補完は便利のためのもので、
 * 失敗しても入力は続けられるので、エラーは空の候補にする。
 */
export async function suggestHosts(query: string, signal?: AbortSignal): Promise<string[]> {
	if (call == null || query.trim() === '') return [];
	try {
		const res = await call<Instance[]>('federation/instances', { host: query.trim(), limit: 8, sort: '+pubSub' }, { signal });
		return res.map(i => i.host);
	} catch {
		return [];
	}
}
