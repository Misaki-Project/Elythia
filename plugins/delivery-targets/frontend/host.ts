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
		// 応答しない・配信を止めた・ブロック中のサーバーは、選んでも届かないので出さない。
		const res = await call<Instance[]>('federation/instances', {
			host: query.trim(),
			limit: 8,
			sort: '+pubSub',
			notResponding: false,
			suspended: false,
			blocked: false,
		}, { signal });
		return res.map(i => i.host);
	} catch {
		return [];
	}
}

export type FollowerHost = { host: string; count: number };
export type FollowerHosts = { hosts: FollowerHost[]; truncated: boolean };

/**
 * Lists the remote servers where the signed-in user has followers, most
 * followers first. 応答しないサーバーなどはバックエンドが外している。
 * 読めなければ null (一覧を出さず、入力で選べば足りる)。
 */
export async function followerHosts(signal?: AbortSignal): Promise<FollowerHosts | null> {
	if (call == null) return null;
	try {
		const res = await call<Partial<FollowerHosts> | null>('plugin/delivery-targets/follower-hosts', {}, { signal });
		if (res == null || !Array.isArray(res.hosts)) return null;
		return {
			hosts: res.hosts.filter(h => typeof h?.host === 'string' && typeof h.count === 'number'),
			truncated: res.truncated === true,
		};
	} catch {
		return null;
	}
}
