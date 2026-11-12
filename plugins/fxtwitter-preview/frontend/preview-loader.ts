/*
 * SPDX-FileCopyrightText: syuilo and misskey-project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import type { PreviewContext, PreviewInput } from '@/plugin-api.js';
import { api, apiSessionEpoch } from './api.js';
import type { PreviewResponse, PublicPost, PublicQuotedPost, PublicTextEntity } from './api.js';
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
		if (kind === 'hashtag' && (tag == null || tag === '' || /[\s\u0000-\u001f\u007f]/u.test(tag))) return null;
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

function normalizedVideoMetadata(value: unknown, allowEmbedToken: boolean): PublicPost['video'] | null | undefined {
	if (value == null) return undefined;
	const video = record(value);
	if (video == null) return null;
	const provider = video.provider == null ? 'external' : video.provider;
	if (provider !== 'x' && provider !== 'youtube' && provider !== 'external') return null;
	const providerVideoId = optionalString(video.providerVideoId);
	const mediaIdentity = optionalString(video.mediaIdentity);
	const posterUrl = optionalString(video.posterUrl);
	const posterExpiresAt = optionalString(video.posterExpiresAt);
	const embedToken = optionalString(video.embedToken);
	const embedExpiresAt = optionalString(video.embedExpiresAt);
	const durationMs = video.durationMs == null ? undefined : typeof video.durationMs === 'number' && Number.isFinite(video.durationMs) ? video.durationMs : null;
	const playbackAvailable = optionalBoolean(video.playbackAvailable);
	if (providerVideoId === null || mediaIdentity === null || posterUrl === null || posterExpiresAt === null || embedToken === null || embedExpiresAt === null || durationMs === null || playbackAvailable === null) return null;
	if (mediaIdentity != null && (!/^[a-f0-9]{64}$/.test(mediaIdentity) || provider !== 'x')) return null;
	if (!allowEmbedToken && (Object.hasOwn(video, 'embedToken') || Object.hasOwn(video, 'embedExpiresAt'))) return null;
	if (provider === 'youtube' && (providerVideoId == null || !/^[A-Za-z0-9_-]{11}$/.test(providerVideoId))) return null;
	if (provider !== 'youtube' && providerVideoId != null) return null;
	if (provider === 'external' && embedToken != null) return null;
	return {
		provider,
		...(providerVideoId == null ? {} : { providerVideoId }),
		...(mediaIdentity == null ? {} : { mediaIdentity }),
		...(posterUrl == null ? {} : { posterUrl }),
		...(posterExpiresAt == null ? {} : { posterExpiresAt }),
		...(durationMs == null ? {} : { durationMs }),
		...(playbackAvailable == null ? {} : { playbackAvailable }),
		...(embedToken == null ? {} : { embedToken }),
		...(embedExpiresAt == null ? {} : { embedExpiresAt }),
	};
}

function normalizedVideo(value: unknown): PublicPost['video'] | null | undefined {
	return normalizedVideoMetadata(value, true);
}

function normalizedVideos(value: unknown, legacyVideo: PublicPost['video']): PublicPost['videos'] | null | undefined {
	if (value == null) {
		if (legacyVideo == null) return undefined;
		const first = normalizedVideoMetadata({
			provider: legacyVideo.provider,
			providerVideoId: legacyVideo.providerVideoId,
			mediaIdentity: legacyVideo.mediaIdentity,
			posterUrl: legacyVideo.posterUrl,
			posterExpiresAt: legacyVideo.posterExpiresAt,
			durationMs: legacyVideo.durationMs,
			playbackAvailable: legacyVideo.playbackAvailable,
		}, false);
		return first == null ? null : [first];
	}
	if (!Array.isArray(value) || value.length > 4) return null;
	const videos: NonNullable<PublicPost['videos']> = [];
	for (const candidate of value) {
		const video = normalizedVideoMetadata(candidate, false);
		if (video == null) return null;
		videos.push(video);
	}
	return videos;
}

function legacyVideoMatchesFirst(video: PublicPost['video'], videos: PublicPost['videos']): boolean {
	if (video == null || videos == null || videos.length === 0) return true;
	const first = videos[0];
	return video.provider === first.provider && video.providerVideoId === first.providerVideoId && video.mediaIdentity === first.mediaIdentity && video.posterUrl === first.posterUrl && video.playbackAvailable === first.playbackAvailable;
}

function normalizedQuote(value: unknown): PublicQuotedPost | null | undefined {
	if (value == null) return undefined;
	const quote = record(value);
	if (quote == null || typeof quote.id !== 'string' || typeof quote.url !== 'string' || typeof quote.text !== 'string' || typeof quote.hasVideo !== 'boolean') return null;
	const author = normalizedAuthor(quote.author);
	const images = normalizedImages(quote.images);
	const video = normalizedVideo(quote.video);
	const videos = normalizedVideos(quote.videos, video == null ? undefined : video);
	const createdAt = optionalString(quote.createdAt);
	const textEntities = normalizedTextEntities(quote.textEntities, quote.text);
	const isSensitive = optionalBoolean(quote.isSensitive);
	if (author == null || images == null || video === null || videos === null || !legacyVideoMatchesFirst(video, videos) || createdAt === null || textEntities == null || isSensitive === null) return null;
	return {
		id: quote.id, url: quote.url, text: quote.text, author, images, hasVideo: quote.hasVideo, textEntities, ...(videos == null ? {} : { videos }),
		...(createdAt == null ? {} : { createdAt }), ...(video == null ? {} : { video }), ...(isSensitive == null ? {} : { isSensitive }),
	};
}

function normalizedPost(value: unknown, expectedID: string): PublicPost | null {
	const post = record(value);
	if (post == null || post.id !== expectedID || typeof post.url !== 'string' || typeof post.text !== 'string' || typeof post.hasVideo !== 'boolean') return null;
	const author = normalizedAuthor(post.author);
	const images = normalizedImages(post.images);
	const video = normalizedVideo(post.video);
	const videos = normalizedVideos(post.videos, video == null ? undefined : video);
	const quote = normalizedQuote(post.quote);
	const createdAt = optionalString(post.createdAt);
	const textEntities = normalizedTextEntities(post.textEntities, post.text);
	const isSensitive = optionalBoolean(post.isSensitive);
	const counts = [post.likes, post.reposts, post.quotes, post.replies];
	if (author == null || images == null || video === null || videos === null || !legacyVideoMatchesFirst(video, videos) || quote === null || createdAt === null || textEntities == null || isSensitive === null || counts.some(value => typeof value !== 'number' || !Number.isFinite(value))) return null;
	return {
		id: post.id, url: post.url, text: post.text, author, images, hasVideo: post.hasVideo, textEntities, ...(videos == null ? {} : { videos }),
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
	const error = ['temporary', 'private', 'not-found', 'unavailable'].includes(String(response.error))
		? response.error as Exclude<PreviewResponse['error'], 'invalid-response' | undefined>
		: state === 'unavailable' ? 'unavailable' : state === 'stale' ? 'temporary' : undefined;
	const retryAt = typeof response.retryAt === 'string' && Number.isFinite(Date.parse(response.retryAt))
		? response.retryAt
		: undefined;
	if (state !== 'ready' && state !== 'stale') {
		return {
			state,
			...(typeof response.postId === 'string' ? { postId: response.postId } : {}),
			...(typeof response.nextPollAt === 'string' ? { nextPollAt: response.nextPollAt } : {}),
			...(state === 'unavailable' ? { error: error ?? 'unavailable', ...(retryAt == null ? {} : { retryAt }) } : {}),
		};
	}
	const post = normalizedPost(response.post, expectedID);
	if (post == null) return { state: 'unavailable', postId: expectedID, error: 'invalid-response' };
	return {
		state, postId: expectedID, post,
		...(typeof response.fetchedAt === 'string' ? { fetchedAt: response.fetchedAt } : {}),
		...(response.stale === true ? { stale: true } : {}),
		...(state === 'stale' ? { error: error ?? 'temporary', ...(retryAt == null ? {} : { retryAt }) } : {}),
	};
}

const previewRequestTimeoutMs = 15_000;

function requestFailure(error: unknown, postId: string): PreviewResponse {
	const value = typeof error === 'object' && error !== null ? error as { code?: unknown; status?: unknown; statusCode?: unknown } : {};
	const code = typeof value.code === 'string' ? value.code : '';
	const status = typeof value.status === 'number' ? value.status : typeof value.statusCode === 'number' ? value.statusCode : null;
	if (['LOGIN_REQUIRED', 'PREVIEW_FORBIDDEN', 'POLICY_UNAVAILABLE', 'NOTE_UNAVAILABLE', 'LINK_NOT_IN_NOTE', 'INVALID_REQUEST'].includes(code) || status === 401 || status === 403 || status === 404) {
		return { state: 'unavailable', postId, error: 'unavailable', retryable: false };
	}
	const retryDelayMs = code === 'RATE_LIMITED' || status === 429 ? 60_000 : 5000;
	return {
		state: 'unavailable', postId,
		error: error instanceof SyntaxError ? 'invalid-response' : 'temporary',
		retryable: true,
		retryAt: new Date(Date.now() + retryDelayMs).toISOString(),
	};
}

async function requestPreview(noteId: string, postId: string, signal: AbortSignal): Promise<unknown> {
	const controller = new AbortController();
	let timedOut = false;
	let timeout: number | null = null;
	const abortFromOwner = (): void => controller.abort();
	if (signal.aborted) throw abortError();
	signal.addEventListener('abort', abortFromOwner, { once: true });
	try {
		const request = api<unknown>('preview', { noteId, postId }, { signal: controller.signal });
		const timedOutRequest = new Promise<never>((_resolve, reject) => {
			timeout = window.setTimeout(() => {
				timedOut = true;
				controller.abort();
				reject(Object.assign(new Error(), { code: 'PREVIEW_TIMEOUT' }));
			}, previewRequestTimeoutMs);
		});
		return await Promise.race([request, timedOutRequest]);
	} catch (error) {
		if (signal.aborted) throw abortError();
		if (timedOut) throw Object.assign(new Error(), { code: 'PREVIEW_TIMEOUT' });
		throw error;
	} finally {
		if (timeout != null) window.clearTimeout(timeout);
		signal.removeEventListener('abort', abortFromOwner);
	}
}

export async function loadPreview(input: PreviewInput, context: PreviewContext, signal: AbortSignal): Promise<PreviewResponse> {
	const postId = statusID(input);
	if (postId == null || context.source?.kind !== 'note') throw new Error('A visible source note is required');
	const sessionEpoch = apiSessionEpoch.value;
	let lastResponse: PreviewResponse | null = null;
	for (let attempt = 0; attempt < 8; attempt++) {
		if (signal.aborted || apiSessionEpoch.value !== sessionEpoch) throw abortError();
		let response: PreviewResponse;
		try {
			response = normalizedPreviewResponse(await requestPreview(context.source.noteId, postId, signal), postId);
		} catch (error) {
			if (signal.aborted || apiSessionEpoch.value !== sessionEpoch) throw abortError();
			return requestFailure(error, postId);
		}
		if (signal.aborted || apiSessionEpoch.value !== sessionEpoch) throw abortError();
		if (response.state !== 'pending') return response;
		lastResponse = response;
		if (attempt < 7) {
			const serverDelay = response.nextPollAt == null ? NaN : Date.parse(response.nextPollAt) - Date.now();
			await delay(Number.isFinite(serverDelay) ? Math.max(0, Math.min(5000, serverDelay)) : 750, signal);
			if (apiSessionEpoch.value !== sessionEpoch) throw abortError();
		}
	}
	const nextPollAt = lastResponse?.nextPollAt == null ? NaN : Date.parse(lastResponse.nextPollAt);
	const retryAt = new Date(Math.max(Date.now() + 5000, Number.isFinite(nextPollAt) ? nextPollAt : 0)).toISOString();
	return { state: 'unavailable', postId, error: 'temporary', retryable: true, retryAt };
}
