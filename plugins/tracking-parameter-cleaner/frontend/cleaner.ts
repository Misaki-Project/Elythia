/*
 * SPDX-FileCopyrightText: syuilo and misskey-project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

const allowedHosts = new Set([
	'x.com', 'www.x.com', 'mobile.x.com',
	'twitter.com', 'www.twitter.com', 'mobile.twitter.com',
	'youtube.com', 'www.youtube.com', 'm.youtube.com', 'music.youtube.com', 'youtu.be',
	'instagram.com', 'www.instagram.com',
	'facebook.com', 'www.facebook.com', 'm.facebook.com', 'fb.watch',
	'threads.com', 'www.threads.com', 'threads.net', 'www.threads.net',
	'tiktok.com', 'www.tiktok.com', 'm.tiktok.com', 'vm.tiktok.com', 'vt.tiktok.com',
	'reddit.com', 'www.reddit.com', 'old.reddit.com', 'redd.it',
]);

const commonTrackingKeys = new Set([
	'utm_source', 'utm_medium', 'utm_campaign', 'utm_term', 'utm_content', 'utm_id',
]);

const opaqueQueryKeys = new Set([
	'url', 'redirect', 'redirect_uri', 'redirect_url', 'target', 'next', 'continue',
	'state', 'code', 'client_id', 'response_type', 'nonce',
]);

const siteTrackingKeys = new Map<string, ReadonlySet<string>>([
	['x.com', new Set(['s', 't', 'ref_src', 'ref_url'])],
	['www.x.com', new Set(['s', 't', 'ref_src', 'ref_url'])],
	['mobile.x.com', new Set(['s', 't', 'ref_src', 'ref_url'])],
	['twitter.com', new Set(['s', 't', 'ref_src', 'ref_url'])],
	['www.twitter.com', new Set(['s', 't', 'ref_src', 'ref_url'])],
	['mobile.twitter.com', new Set(['s', 't', 'ref_src', 'ref_url'])],
	['youtube.com', new Set(['si'])],
	['www.youtube.com', new Set(['si'])],
	['m.youtube.com', new Set(['si'])],
	['music.youtube.com', new Set(['si'])],
	['youtu.be', new Set(['si'])],
	['instagram.com', new Set(['igsh', 'igshid'])],
	['www.instagram.com', new Set(['igsh', 'igshid'])],
	['facebook.com', new Set(['fbclid'])],
	['www.facebook.com', new Set(['fbclid'])],
	['m.facebook.com', new Set(['fbclid'])],
	['fb.watch', new Set(['fbclid'])],
	['threads.com', new Set(['igsh', 'igshid'])],
	['www.threads.com', new Set(['igsh', 'igshid'])],
	['threads.net', new Set(['igsh', 'igshid'])],
	['www.threads.net', new Set(['igsh', 'igshid'])],
	['tiktok.com', new Set(['_t', '_r'])],
	['www.tiktok.com', new Set(['_t', '_r'])],
	['m.tiktok.com', new Set(['_t', '_r'])],
	['vm.tiktok.com', new Set(['_t', '_r'])],
	['vt.tiktok.com', new Set(['_t', '_r'])],
	['reddit.com', new Set(['share_id'])],
	['www.reddit.com', new Set(['share_id'])],
	['old.reddit.com', new Set(['share_id'])],
	['redd.it', new Set(['share_id'])],
]);

const xTwitterHosts = new Set(['x.com', 'www.x.com', 'mobile.x.com', 'twitter.com', 'www.twitter.com', 'mobile.twitter.com']);
const xStatusPath = /^\/(?:[^/]+\/status\/\d+|i\/status\/\d+|i\/web\/status\/\d+)(?:\/|$)/i;

const signatureKey = /^(?:sig|signature|token|expires|hmac|auth|authorization|key|access_token|refresh_token|x-amz-.*|x-goog-.*)$/i;
const trailingPunctuation = new Set(['.', ',', '!', '?', ':', ';', '。', '、', '，', '！', '？', '；', '：']);
const closingDelimiters = new Map([[')', '('], [']', '['], ['}', '{'], ['）', '（'], ['】', '【'], ['」', '「'], ['』', '『']]);

type Span = { start: number; end: number };

/** Clean only a small, documented set of tracking keys on supported social hosts. */
export function cleanTrackingParameters(text: string): string {
	if (!/(?:https?):\/\//i.test(text)) return text;

	const fencedRanges = findFencedCodeRanges(text);
	let rangeIndex = 0;
	let output = '';

	for (let i = 0; i < text.length;) {
		while (fencedRanges[rangeIndex] != null && fencedRanges[rangeIndex].end <= i) rangeIndex++;
		const fenced = fencedRanges[rangeIndex];
		if (fenced != null && i >= fenced.start && i < fenced.end) {
			output += text.slice(i, fenced.end);
			i = fenced.end;
			rangeIndex++;
			continue;
		}

		if (text[i] === '`') {
			const tickLength = countRun(text, i, '`');
			const closing = findMatchingBackticks(text, i + tickLength, tickLength);
			if (closing !== -1) {
				const end = closing + tickLength;
				output += text.slice(i, end);
				i = end;
				continue;
			}
		}

		if (isUrlStart(text, i) && isBoundaryBefore(text, i)) {
			const rawEnd = findUrlEnd(text, i);
			const urlEnd = trimUrlEnd(text, i, rawEnd);
			const rawUrl = text.slice(i, urlEnd);
			output += cleanUrl(rawUrl) ?? rawUrl;
			i = urlEnd;
			continue;
		}

		output += text[i];
		i++;
	}

	return output;
}

function findFencedCodeRanges(text: string): Span[] {
	const ranges: Span[] = [];
	let fence: { start: number; marker: '`' | '~'; length: number } | undefined;
	let lineStart = 0;

	while (lineStart < text.length) {
		const newline = text.indexOf('\n', lineStart);
		const lineEnd = newline === -1 ? text.length : newline + 1;
		const line = text.slice(lineStart, newline === -1 ? text.length : newline).replace(/\r$/, '');
		const marker = /^( {0,3})(`{3,}|~{3,})(.*)$/.exec(line);

		if (fence == null && marker != null) {
			fence = { start: lineStart, marker: marker[2][0] as '`' | '~', length: marker[2].length };
		} else if (fence != null && marker != null && marker[2][0] === fence.marker && marker[2].length >= fence.length && marker[3].trim() === '') {
			ranges.push({ start: fence.start, end: lineEnd });
			fence = undefined;
		}

		lineStart = lineEnd;
	}

	if (fence != null) ranges.push({ start: fence.start, end: text.length });
	return ranges;
}

function countRun(text: string, start: number, character: string): number {
	let end = start;
	while (text[end] === character) end++;
	return end - start;
}

function findMatchingBackticks(text: string, from: number, length: number): number {
	for (let i = from; i < text.length;) {
		const next = text.indexOf('`', i);
		if (next === -1) return -1;
		const runLength = countRun(text, next, '`');
		if (runLength === length) return next;
		i = next + runLength;
	}
	return -1;
}

function isUrlStart(text: string, index: number): boolean {
	return /^https?:\/\//i.test(text.slice(index, index + 8));
}

function isBoundaryBefore(text: string, index: number): boolean {
	if (index === 0) return true;
	let previousStart = index - 1;
	const lastCodeUnit = text.charCodeAt(previousStart);
	if (lastCodeUnit >= 0xdc00 && lastCodeUnit <= 0xdfff && previousStart > 0) {
		const previousCodeUnit = text.charCodeAt(previousStart - 1);
		if (previousCodeUnit >= 0xd800 && previousCodeUnit <= 0xdbff) previousStart--;
	}
	const previous = text.slice(previousStart, index);
	return !/[\p{L}\p{N}_@:]/u.test(previous);
}

function findUrlEnd(text: string, start: number): number {
	let end = start;
	while (end < text.length && !/[\s<>"'。、，！？；：]/u.test(text[end])) end++;
	return end;
}

function trimUrlEnd(text: string, start: number, end: number): number {
	while (end > start) {
		const last = text[end - 1];
		if (trailingPunctuation.has(last)) {
			end--;
			continue;
		}

		const opening = closingDelimiters.get(last);
		if (opening != null && countCharacter(text.slice(start, end), last) > countCharacter(text.slice(start, end), opening)) {
			end--;
			continue;
		}
		break;
	}
	return end;
}

function countCharacter(text: string, character: string): number {
	let count = 0;
	for (const value of text) if (value === character) count++;
	return count;
}

function cleanUrl(rawUrl: string): string | undefined {
	let url: URL;
	try {
		url = new URL(rawUrl);
	} catch {
		return undefined;
	}

	if ((url.protocol !== 'https:' && url.protocol !== 'http:') || url.username !== '' || url.password !== '' || url.port !== '') return undefined;
	const host = url.hostname.toLowerCase();
	if (!allowedHosts.has(host)) return undefined;

	const queryStart = rawUrl.indexOf('?');
	const firstHash = rawUrl.indexOf('#');
	if (queryStart === -1 || (firstHash !== -1 && firstHash < queryStart)) return undefined;
	const hashStart = firstHash;
	const queryEnd = hashStart === -1 ? rawUrl.length : hashStart;
	const query = rawUrl.slice(queryStart + 1, queryEnd);
	// Raw non-ASCII query values are ambiguous at a prose boundary. Leave the whole URL intact.
	if (Array.from(query).some(character => character.charCodeAt(0) > 0x7f)) return undefined;
	let decodedPath: string;
	try {
		decodedPath = decodeURIComponent(url.pathname);
	} catch {
		return undefined;
	}

	const rawParts = query.split('&');
	const decodedKeys: string[] = [];
	for (const part of rawParts) {
		const rawKey = part.slice(0, part.indexOf('=') === -1 ? part.length : part.indexOf('='));
		try {
			decodedKeys.push(decodeURIComponent(rawKey.replace(/\+/g, ' ')));
		} catch {
			return undefined;
		}
	}

	// Signing/authentication hints make even otherwise familiar links opaque to this cleaner.
	if (/(?:^|\/)(?:redirect|oauth|authorize|login|auth)(?:\/|$)/i.test(decodedPath)) return undefined;
	if (decodedKeys.some(key => signatureKey.test(key) || opaqueQueryKeys.has(key.toLowerCase()))) return undefined;

	const siteKeys = siteTrackingKeys.get(host) ?? new Set<string>();
	const effectiveSiteKeys = xTwitterHosts.has(host) && !xStatusPath.test(decodedPath) ? new Set<string>() : siteKeys;
	const kept: string[] = [];
	let removed = false;
	for (let i = 0; i < rawParts.length; i++) {
		const key = decodedKeys[i];
		if (commonTrackingKeys.has(key) || effectiveSiteKeys.has(key)) {
			removed = true;
		} else {
			kept.push(rawParts[i]);
		}
	}
	if (!removed) return undefined;

	return rawUrl.slice(0, queryStart) + (kept.length > 0 ? `?${kept.join('&')}` : '') + rawUrl.slice(queryEnd);
}
