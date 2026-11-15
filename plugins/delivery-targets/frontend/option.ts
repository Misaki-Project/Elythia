/*
 * SPDX-FileCopyrightText: Elythia-Network
 * SPDX-License-Identifier: AGPL-3.0-only
 */

/** The value sent as notes/create pluginOptions["delivery-targets"]. */
export type DeliveryTargetsOption = {
	mode: 'include' | 'exclude';
	hosts: string[];
};

/** Up to how many hosts one note may list (the backend's MaxHosts). */
export const MAX_HOSTS = 100;

export function emptyOption(): DeliveryTargetsOption {
	return { mode: 'include', hosts: [] };
}

/** Reads a stored value back, falling back to an empty choice for anything else. */
export function toOption(value: unknown): DeliveryTargetsOption {
	if (value == null || typeof value !== 'object') return emptyOption();
	const v = value as Partial<DeliveryTargetsOption>;
	const mode = v.mode === 'exclude' ? 'exclude' : 'include';
	const hosts = Array.isArray(v.hosts) ? v.hosts.filter((h): h is string => typeof h === 'string') : [];
	return { mode, hosts };
}

/**
 * Whether the value narrows anything. サーバーを1つも指定していなければ、
 * どちらのモードでも何も絞らない (指定が無いのと同じ) ので送らない。
 */
export function isSet(value: unknown): boolean {
	if (value == null || typeof value !== 'object') return false;
	return toOption(value).hosts.length > 0;
}

/**
 * Turns what was typed or pasted into a host, the way the backend does
 * (NormalizeHost): a URL gives its host, a leading @ and a trailing dot are
 * dropped, letters are lowered. IDN は送った先のサーバーで punycode にそろえる。
 * 形の判定もサーバーが行うので、ここでは空かどうかだけを見る。
 */
export function normalizeHost(input: string): string | null {
	let s = input.trim();
	if (s.includes('://')) {
		try {
			s = new URL(s).host;
		} catch {
			return null;
		}
	}
	s = s.replace(/^@/, '').replace(/\.$/, '').toLowerCase();
	if (s === '' || /\s|\//.test(s)) return null;
	return s;
}

/** Adds hosts (comma, space or newline separated) without duplicates. */
export function addHosts(option: DeliveryTargetsOption, input: string): DeliveryTargetsOption {
	const hosts = [...option.hosts];
	for (const part of input.split(/[\s,、]+/)) {
		const host = normalizeHost(part);
		if (host == null || hosts.includes(host) || hosts.length >= MAX_HOSTS) continue;
		hosts.push(host);
	}
	return { ...option, hosts };
}

export function removeHost(option: DeliveryTargetsOption, host: string): DeliveryTargetsOption {
	return { ...option, hosts: option.hosts.filter(h => h !== host) };
}
