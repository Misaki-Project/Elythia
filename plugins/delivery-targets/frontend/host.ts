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

/** How long to wait for the backend before leaving the button out. */
export const AVAILABLE_TIMEOUT_MS = 5000;

/**
 * Reports whether this plugin's backend is running on the server.
 *
 * 運営者が設定でバックエンドを止めても、frontend はビルドに含まれたまま読み込ま
 * れる。止まっていればルートも無いので、答えが返らなければボタンを出さない。
 * 起動を待たせ続けないよう、時間を区切る。未ログインでも失敗するが、投稿できない
 * のでボタンは要らない。
 */
export async function isAvailable(api: Api, timeoutMs = AVAILABLE_TIMEOUT_MS): Promise<boolean> {
	try {
		const res = await api<{ available?: unknown }>('plugin/delivery-targets/available', {}, { signal: AbortSignal.timeout(timeoutMs) });
		return res?.available === true;
	} catch {
		return false;
	}
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
