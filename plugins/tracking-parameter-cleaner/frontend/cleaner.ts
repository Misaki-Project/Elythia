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
const openingDelimiters = new Map([...closingDelimiters].map(([closing, opening]) => [opening, closing]));

type Span = { start: number; end: number };
type BacktickRun = { start: number; end: number };

/** Clean only a small, documented set of tracking keys on supported social hosts. */
export function cleanTrackingParameters(text: string): string {
	if (!/(?:https?):\/\//i.test(text)) return text;

	const fencedRanges = findFencedCodeRanges(text);
	const backtickRuns = findBacktickRuns(text);
	const backtickClosers = indexBacktickClosers(backtickRuns);
	let backtickRunIndex = 0;
	let rangeIndex = 0;
	let output = '';

	for (let i = 0; i < text.length;) {
		while (backtickRuns[backtickRunIndex] != null && backtickRuns[backtickRunIndex].end <= i) backtickRunIndex++;
		while (fencedRanges[rangeIndex] != null && fencedRanges[rangeIndex].end <= i) rangeIndex++;
		const fenced = fencedRanges[rangeIndex];
		if (fenced != null && i >= fenced.start && i < fenced.end) {
			output += text.slice(i, fenced.end);
			i = fenced.end;
			rangeIndex++;
			continue;
		}

		if (text[i] === '`') {
			const run = backtickRuns[backtickRunIndex];
			const closing = backtickClosers.get(i);
			if (run != null && closing != null) {
				const tickLength = run.end - i;
				const end = closing + tickLength;
				output += text.slice(i, end);
				i = end;
				continue;
			}
		}

		if (isUrlStart(text, i) && isBoundaryBefore(text, i)) {
			const rawEnd = findUrlEnd(text, i);
			const trimmedEnd = trimUrlEnd(text, i, rawEnd);
			const mfmBoundary = findMfmUrlBoundary(text, i, trimmedEnd);
			if (mfmBoundary.ambiguousEnd != null) {
				output += text.slice(i, mfmBoundary.ambiguousEnd);
				i = mfmBoundary.ambiguousEnd;
				continue;
			}
			const urlEnd = mfmBoundary.urlEnd;
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
		let contentEnd = newline === -1 ? text.length : newline;
		if (contentEnd > lineStart && text[contentEnd - 1] === '\r') contentEnd--;
		const marker = readFenceMarker(text, lineStart, contentEnd);

		if (fence == null && marker != null) {
			fence = { start: lineStart, marker: marker.marker, length: marker.length };
		} else if (fence != null && marker != null && marker.marker === fence.marker && marker.length >= fence.length && text.slice(marker.suffixStart, contentEnd).trim() === '') {
			ranges.push({ start: fence.start, end: lineEnd });
			fence = undefined;
		}

		lineStart = lineEnd;
	}

	if (fence != null) ranges.push({ start: fence.start, end: text.length });
	return ranges;
}

function readFenceMarker(text: string, lineStart: number, contentEnd: number): { marker: '`' | '~'; length: number; suffixStart: number } | null {
	let markerStart = lineStart;
	while (markerStart < contentEnd && markerStart - lineStart < 3 && text[markerStart] === ' ') markerStart++;
	if (markerStart < contentEnd && text[markerStart] === ' ') return null;
	const marker = text[markerStart];
	if (marker !== '`' && marker !== '~') return null;
	let markerEnd = markerStart + 1;
	while (markerEnd < contentEnd && text[markerEnd] === marker) markerEnd++;
	if (markerEnd - markerStart < 3) return null;
	for (let i = markerEnd; i < contentEnd; i++) {
		if (text[i] === '\r' || text[i] === '\u2028' || text[i] === '\u2029') return null;
	}
	return { marker, length: markerEnd - markerStart, suffixStart: markerEnd };
}

function findBacktickRuns(text: string): BacktickRun[] {
	const runs: BacktickRun[] = [];
	for (let i = 0; i < text.length;) {
		if (text[i] !== '`') {
			i++;
			continue;
		}
		const start = i;
		while (i < text.length && text[i] === '`') i++;
		runs.push({ start, end: i });
	}
	return runs;
}

function indexBacktickClosers(runs: BacktickRun[]): Map<number, number> {
	const closers = new Map<number, number>();
	const nextRunByLength = new Map<number, number>();
	for (let runIndex = runs.length - 1; runIndex >= 0; runIndex--) {
		const run = runs[runIndex];
		for (let length = run.end - run.start; length > 0; length--) {
			const closing = nextRunByLength.get(length);
			if (closing != null) closers.set(run.end - length, closing);
		}
		nextRunByLength.set(run.end - run.start, run.start);
	}
	return closers;
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
	const balances = new Map<string, number>();
	for (const character of text.slice(start, end)) {
		if (closingDelimiters.has(character)) balances.set(character, (balances.get(character) ?? 0) + 1);
		else {
			const closing = openingDelimiters.get(character);
			if (closing != null) balances.set(closing, (balances.get(closing) ?? 0) - 1);
		}
	}

	while (end > start) {
		const last = text[end - 1];
		if (trailingPunctuation.has(last)) {
			end--;
			continue;
		}

		const opening = closingDelimiters.get(last);
		if (opening != null && (balances.get(last) ?? 0) > 0) {
			balances.set(last, (balances.get(last) ?? 0) - 1);
			end--;
			continue;
		}
		break;
	}
	return end;
}

type MfmMarkerBoundary = { urlEnd: number; ambiguousEnd?: never } | { urlEnd: number; ambiguousEnd: number };

function findMfmUrlBoundary(text: string, start: number, end: number): MfmMarkerBoundary {
	let openingStart = start;
	while (openingStart > 0 && isMfmMarker(text[openingStart - 1])) openingStart--;
	let closingStart = end;
	while (closingStart > start && isMfmMarker(text[closingStart - 1])) closingStart--;
	if (openingStart === start && closingStart === end) return { urlEnd: end };
	if (openingStart === start) return { urlEnd: end, ambiguousEnd: end };

	const opening = text.slice(openingStart, start);
	if (isEscaped(text, openingStart)) return { urlEnd: end, ambiguousEnd: end };
	if (closingStart === end) return { urlEnd: end, ambiguousEnd: end };

	const closing = text.slice(closingStart, end);
	const openingMarkers = splitMfmMarkers(opening);
	const closingMarkers = splitMfmMarkers(closing);
	if (openingMarkers == null || closingMarkers == null || closingMarkers.length !== openingMarkers.length ||
		openingMarkers.some((marker, index) => marker !== closingMarkers[closingMarkers.length - index - 1]) || isEscaped(text, closingStart)) {
		return { urlEnd: end, ambiguousEnd: end };
	}
	return { urlEnd: closingStart };
}

function isMfmMarker(character: string | undefined): boolean {
	return character === '*' || character === '~' || character === '_';
}

function isEscaped(text: string, markerStart: number): boolean {
	let backslashes = 0;
	for (let index = markerStart - 1; index >= 0 && text[index] === '\\'; index--) backslashes++;
	return backslashes % 2 !== 0;
}

function splitMfmMarkers(markers: string): string[] | undefined {
	const result: string[] = [];
	for (let index = 0; index < markers.length;) {
		const marker = markers.startsWith('***', index) ? '***'
			: markers.startsWith('**', index) ? '**'
				: markers.startsWith('__', index) ? '__'
					: markers.startsWith('~~', index) ? '~~'
						: markers.startsWith('*', index) ? '*'
							: undefined;
		if (marker == null) return undefined;
		result.push(marker);
		index += marker.length;
	}
	return result;
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
