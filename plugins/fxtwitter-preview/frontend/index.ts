/*
 * SPDX-FileCopyrightText: syuilo and misskey-project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import { api, initApi, truncatePostText } from './api.js';
import FxPreview from './FxPreview.vue';
import FxPreviewSkeleton from './FxPreviewSkeleton.vue';
import AdminPage from './AdminPage.vue';
import type { Capabilities, PreviewResponse, PublicPost, PublicQuotedPost, PublicTextEntity, UnavailableReason } from './api.js';
import type { PreviewContext, PreviewInput, PreviewProvider } from '@/plugin-api.js';
import { definePlugin } from '@/plugin-api.js';

export { FxPreview, initApi, truncatePostText };
export type { PreviewResponse } from './api.js';

const statusHosts = new Set(['x.com', 'www.x.com', 'mobile.x.com', 'twitter.com', 'www.twitter.com', 'mobile.twitter.com']);

export function statusID(input: PreviewInput): string | null {
	if (input.kind !== 'url') return null;
	try {
		const url = new URL(input.href);
		if (url.protocol !== 'https:' || url.username !== '' || url.password !== '' || url.port !== '' || !statusHosts.has(url.hostname.toLowerCase())) return null;
		const parts = url.pathname.replace(/^\/+|\/+$/g, '').split('/');
		if (parts.length !== 3 || !/^[A-Za-z0-9_]{1,15}$/.test(decodeURIComponent(parts[0])) || parts[1] !== 'status' || !/^[0-9]{2,20}$/.test(parts[2])) return null;
		return parts[2];
	} catch {
		return null;
	}
}

function abortError(): DOMException {
	return new DOMException('Aborted', 'AbortError');
}

function delay(ms: number, signal: AbortSignal): Promise<void> {
	if (signal.aborted) return Promise.reject(abortError());
	return new Promise((resolve, reject) => {
		const timer = window.setTimeout(() => {
			signal.removeEventListener('abort', aborted);
			resolve();
		}, ms);
		const aborted = (): void => {
			window.clearTimeout(timer);
			reject(abortError());
		};
		signal.addEventListener('abort', aborted, { once: true });
	});
}

function record(value: unknown): Record<string, unknown> | null {
	return typeof value === 'object' && value !== null && !Array.isArray(value) ? value as Record<string, unknown> : null;
}

function optionalString(value: unknown): string | undefined | null {
	return value == null ? undefined : typeof value === 'string' ? value : null;
}

function optionalBoolean(value: unknown): boolean | undefined | null {
	return value == null ? undefined : typeof value === 'boolean' ? value : null;
}

function unavailableReason(value: unknown): UnavailableReason | undefined {
	return ['deleted', 'suspended', 'private', 'blocked', 'unavailable', 'unknown', 'fetch_failed'].includes(String(value)) ? value as UnavailableReason : undefined;
}

function normalizedImages(value: unknown): PublicPost['images'] | null {
	if (value == null) return [];
	if (!Array.isArray(value)) return null;
	const images: PublicPost['images'] = [];
	for (const candidate of value) {
		const image = record(candidate);
		if (image == null || typeof image.url !== 'string') return null;
		const width = image.width == null ? undefined : typeof image.width === 'number' && Number.isFinite(image.width) ? image.width : null;
		const height = image.height == null ? undefined : typeof image.height === 'number' && Number.isFinite(image.height) ? image.height : null;
		const alt = optionalString(image.alt);
		if (width === null || height === null || alt === null) return null;
		images.push({ url: image.url, ...(width == null ? {} : { width }), ...(height == null ? {} : { height }), ...(alt == null ? {} : { alt }) });
	}
	return images;
}

function normalizedAuthor(value: unknown): PublicPost['author'] | null {
	const author = record(value);
	if (author == null || typeof author.name !== 'string' || typeof author.screenName !== 'string') return null;
	const avatarUrl = optionalString(author.avatarUrl);
	const profileUrl = optionalString(author.profileUrl);
	if (avatarUrl === null || profileUrl === null || (profileUrl != null && !validXProfileURL(profileUrl, author.screenName))) return null;
	return { name: author.name, screenName: author.screenName, ...(avatarUrl == null ? {} : { avatarUrl }), ...(profileUrl == null ? {} : { profileUrl }) };
}

function validXProfileURL(value: string, screenName: string): boolean {
	if (!/^[A-Za-z0-9_]{1,15}$/.test(screenName)) return false;
	try {
		const url = new URL(value);
		return url.protocol === 'https:' && url.hostname === 'x.com' && url.username === '' && url.password === '' && url.port === '' && url.pathname === `/${screenName}` && url.search === '' && url.hash === '';
	} catch {
		return false;
	}
}

function normalizedTextEntities(value: unknown, text: string): PublicTextEntity[] | null {
	if (value == null) return [];
	if (!Array.isArray(value)) return null;
	const entities: PublicTextEntity[] = [];
	let previousEnd = 0;
	for (const candidate of value) {
		const entity = record(candidate);
		if (entity == null || !['url', 'hashtag', 'mention'].includes(String(entity.kind)) || !Number.isInteger(entity.start) || !Number.isInteger(entity.end) ||
			(entity.start as number) < previousEnd || (entity.start as number) < 0 || (entity.end as number) <= (entity.start as number) || (entity.end as number) > text.length || typeof entity.display !== 'string') return null;
		const kind = entity.kind as PublicTextEntity['kind'];
		const href = optionalString(entity.href);
		const tag = optionalString(entity.tag);
		if (href === null || tag === null) return null;
		if (kind === 'url' && (href == null || !validHTTPURL(href))) return null;
		if (kind === 'mention' && (href == null || !validMentionURL(href, text.slice(entity.start as number, entity.end as number)))) return null;
		if (kind === 'hashtag' && (tag == null || tag === '' || /\s/u.test(tag) || Array.from(tag).some(character => character.charCodeAt(0) < 32 || character.charCodeAt(0) === 127))) return null;
		entities.push({ kind, start: entity.start as number, end: entity.end as number, display: entity.display, ...(href == null ? {} : { href }), ...(tag == null ? {} : { tag }) });
		previousEnd = entity.end as number;
	}
	return entities;
}

function validHTTPURL(value: string): boolean {
	try {
		const url = new URL(value);
		return (url.protocol === 'http:' || url.protocol === 'https:') && url.hostname !== '' && url.username === '' && url.password === '';
	} catch {
		return false;
	}
}

function validMentionURL(value: string, original: string): boolean {
	const handle = original.replace(/^[@＠]/u, '');
	return /^[A-Za-z0-9_]{1,15}$/.test(handle) && value === `https://x.com/${handle}`;
}

function normalizedVideo(value: unknown): PublicPost['video'] | null | undefined {
	if (value == null) return undefined;
	const video = record(value);
	if (video == null) return null;
	const provider = video.provider ?? 'external';
	if (provider !== 'x' && provider !== 'external') return null;
	const providerVideoId = optionalString(video.providerVideoId);
	const posterUrl = optionalString(video.posterUrl);
	const posterExpiresAt = optionalString(video.posterExpiresAt);
	const embedToken = optionalString(video.embedToken);
	const embedExpiresAt = optionalString(video.embedExpiresAt);
	const durationMs = video.durationMs == null ? undefined : typeof video.durationMs === 'number' && Number.isFinite(video.durationMs) ? video.durationMs : null;
	if (providerVideoId === null || posterUrl === null || posterExpiresAt === null || embedToken === null || embedExpiresAt === null || durationMs === null) return null;
	if (providerVideoId != null) return null;
	if (provider === 'external' && embedToken != null) return null;
	return {
		provider,
		...(posterUrl == null ? {} : { posterUrl }),
		...(posterExpiresAt == null ? {} : { posterExpiresAt }),
		...(durationMs == null ? {} : { durationMs }),
		...(embedToken == null ? {} : { embedToken }),
		...(embedExpiresAt == null ? {} : { embedExpiresAt }),
	};
}

function normalizedQuote(value: unknown): PublicQuotedPost | null | undefined {
	if (value == null) return undefined;
	const quote = record(value);
	if (quote == null || typeof quote.id !== 'string' || typeof quote.url !== 'string' || typeof quote.text !== 'string' || typeof quote.hasVideo !== 'boolean') return null;
	const author = normalizedAuthor(quote.author);
	const images = normalizedImages(quote.images);
	const video = normalizedVideo(quote.video);
	const createdAt = optionalString(quote.createdAt);
	const textEntities = normalizedTextEntities(quote.textEntities, quote.text);
	const isSensitive = optionalBoolean(quote.isSensitive);
	if (author == null || images == null || video === null || createdAt === null || textEntities == null || isSensitive === null) return null;
	return {
		id: quote.id, url: quote.url, text: quote.text, author, images, hasVideo: quote.hasVideo, textEntities,
		...(createdAt == null ? {} : { createdAt }), ...(video == null ? {} : { video }), ...(isSensitive == null ? {} : { isSensitive }),
	};
}

function normalizedPost(value: unknown, expectedID: string): PublicPost | null {
	const post = record(value);
	if (post == null || post.id !== expectedID || typeof post.url !== 'string' || typeof post.text !== 'string' || typeof post.hasVideo !== 'boolean') return null;
	const author = normalizedAuthor(post.author);
	const images = normalizedImages(post.images);
	const video = normalizedVideo(post.video);
	const quote = normalizedQuote(post.quote);
	const createdAt = optionalString(post.createdAt);
	const textEntities = normalizedTextEntities(post.textEntities, post.text);
	const isSensitive = optionalBoolean(post.isSensitive);
	const counts = [post.likes, post.reposts, post.quotes, post.replies];
	if (author == null || images == null || video === null || quote === null || createdAt === null || textEntities == null || isSensitive === null || counts.some(count => typeof count !== 'number' || !Number.isFinite(count))) return null;
	return {
		id: post.id, url: post.url, text: post.text, author, images, hasVideo: post.hasVideo, textEntities,
		...(post.quoteUnavailableReason == null ? {} : { quoteUnavailableReason: unavailableReason(post.quoteUnavailableReason) ?? 'unknown' }),
		likes: post.likes as number, reposts: post.reposts as number, quotes: post.quotes as number, replies: post.replies as number,
		...(createdAt == null ? {} : { createdAt }), ...(video == null ? {} : { video }), ...(quote == null ? {} : { quote }), ...(isSensitive == null ? {} : { isSensitive }),
	};
}

function normalizedPreviewResponse(value: unknown, expectedID: string): PreviewResponse {
	const response = record(value);
	if (response == null || !['pending', 'ready', 'stale', 'unavailable', 'disabled'].includes(String(response.state))) {
		return { state: 'unavailable', postId: expectedID, error: 'invalid-response' };
	}
	const state = response.state as PreviewResponse['state'];
	if (state !== 'ready' && state !== 'stale') {
		return {
			state,
			...(response.unavailableReason == null ? {} : { unavailableReason: unavailableReason(response.unavailableReason) ?? 'unknown' }),
			...(typeof response.postId === 'string' ? { postId: response.postId } : {}),
			...(typeof response.nextPollAt === 'string' ? { nextPollAt: response.nextPollAt } : {}),
		};
	}
	const post = normalizedPost(response.post, expectedID);
	if (post == null) return { state: 'unavailable', postId: expectedID, error: 'invalid-response' };
	return {
		state, postId: expectedID, post,
		...(typeof response.fetchedAt === 'string' ? { fetchedAt: response.fetchedAt } : {}),
		...(response.stale === true ? { stale: true } : {}),
	};
}

export async function loadPreview(input: PreviewInput, context: PreviewContext, signal: AbortSignal): Promise<PreviewResponse> {
	const postId = statusID(input);
	if (postId == null || context.source?.kind !== 'note') throw new Error('A visible source note is required');
	if (!context.detail || !context.revealed || !context.active || !context.inViewport) throw abortError();
	for (let attempt = 0; attempt < 8; attempt++) {
		if (signal.aborted) throw abortError();
		const response = normalizedPreviewResponse(await api<unknown>('preview', { noteId: context.source.noteId, postId }, { signal }), postId);
		// The host may ignore cancellation and settle after abort; the late-preview mutation proves this guard is needed.
		// eslint-disable-next-line @typescript-eslint/no-unnecessary-condition
		if (signal.aborted) throw abortError();
		if (response.state !== 'pending') return response;
		if (attempt < 7) await delay(750, signal);
		else return response;
	}
	throw new Error('unreachable');
}

export const provider: PreviewProvider<PreviewResponse> = {
	id: 'x-status',
	priority: 100,
	requiresDetail: true,
	loadingComponent: FxPreviewSkeleton,
	match: input => statusID(input) != null,
	load: loadPreview,
	component: FxPreview,
};

// pluginbuild's generated registry imports the plugin definition as a default export.
// eslint-disable-next-line import/no-default-export
export default definePlugin({
	name: 'fxtwitter-preview',
	pages: [{ path: '/', component: AdminPage, admin: true, navTitle: 'FxTwitter preview', navIcon: 'ti ti-brand-x' }],
	async setup(host) {
		initApi(host.api, host.fetch, host.openMedia);
		try {
			const capabilities = await api<Capabilities>('capabilities');
			if (!capabilities.externalFetchEnabled || host.me == null) return;
			host.preview.register(provider);
		} catch (err) {
			console.warn('[plugin:fxtwitter-preview] preview provider remains disabled', err);
		}
	},
});
