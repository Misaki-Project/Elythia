/*
 * SPDX-FileCopyrightText: syuilo and misskey-project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import type { PluginHost } from '@/plugin-api.js';
import { ref } from 'vue';

/** Changes whenever the host installs a new API/fetch session for this plugin. */
export const apiSessionEpoch = ref(0);
export const videoPlaybackMode = ref<'officialEmbed' | 'nativeProxy' | 'linkOnly'>('officialEmbed');

export function setVideoPlaybackMode(mode: 'officialEmbed' | 'nativeProxy' | 'linkOnly'): void {
	videoPlaybackMode.value = mode;
}

let call: PluginHost['api'];
let rawFetch: PluginHost['fetch'];
let showMedia: PluginHost['openMedia'] = async () => undefined;

export function initApi(
	fn: PluginHost['api'],
	fetcher: PluginHost['fetch'] = (path, options) => window.fetch(path, options),
	mediaViewer: PluginHost['openMedia'] = async () => undefined,
): void {
	apiSessionEpoch.value++;
	videoPlaybackMode.value = 'officialEmbed';
	call = fn;
	rawFetch = fetcher;
	showMedia = mediaViewer;
}

export function openPluginMedia(...args: Parameters<PluginHost['openMedia']>): ReturnType<PluginHost['openMedia']> {
	return showMedia(...args);
}

function graphemes(value: string): string[] {
	if (typeof Intl.Segmenter === 'function') {
		return Array.from(new Intl.Segmenter(undefined, { granularity: 'grapheme' }).segment(value), part => part.segment);
	}
	return Array.from(value);
}

export function truncatePostText(value: string): { text: string; truncated: boolean } {
	const segmented = graphemes(value);
	const lines = value.split('\n');
	if (segmented.length < 140 && lines.length < 6) return { text: value, truncated: false };

	let text = lines.length >= 6 ? lines.slice(0, 5).join('\n') : value;
	const visible = graphemes(text);
	if (visible.length >= 140) text = visible.slice(0, 139).join('');
	return { text, truncated: true };
}

export function api<T>(path: string, params: Record<string, unknown> = {}, options?: { signal?: AbortSignal }): Promise<T> {
	return call<T>(`plugin/fxtwitter-preview/${path}`, params, options);
}

export function nativeApi<T>(endpoint: string, params: Record<string, unknown> = {}): Promise<T> {
	return call<T>(endpoint, params);
}

export function fetchPluginResource(path: string, options?: RequestInit): Promise<Response> {
	return rawFetch(path, options);
}

export type PreparedPublicMedia = {
	url: string;
	expiresAt: number;
	contentType: string;
	size: number;
};

const publicMediaBasePath = '/api/plugin/fxtwitter-preview/public-media/';
const publicMediaPathPattern = /^\/api\/plugin\/fxtwitter-preview\/public-media\/[a-f0-9]{32}\.(?:jpg|png|gif|webp|avif|mp4)$/;
const imageContentTypes = new Map([
	['image/jpeg', 'jpg'],
	['image/png', 'png'],
	['image/gif', 'gif'],
	['image/webp', 'webp'],
	['image/avif', 'avif'],
]);

/** Validate server metadata before an opaque public media URL is fetched. */
export function parsePreparedPublicMedia(value: unknown, kind: 'image' | 'video', maxBytes: number): PreparedPublicMedia | null {
	if (typeof value !== 'object' || value === null || Array.isArray(value)) return null;
	const media = value as Record<string, unknown>;
	if (typeof media.url !== 'string' || typeof media.expiresAt !== 'string' || typeof media.contentType !== 'string' ||
		!Number.isSafeInteger(media.size) || (media.size as number) <= 0 || (media.size as number) > maxBytes) return null;
	const expiresAt = Date.parse(media.expiresAt);
	if (!Number.isFinite(expiresAt) || expiresAt <= Date.now()) return null;
	const url = validatedPublicMediaPath(media.url);
	if (url == null) return null;
	const contentType = media.contentType.toLowerCase();
	const extension = contentType === 'video/mp4' ? 'mp4' : imageContentTypes.get(contentType);
	if (extension == null || (kind === 'video' ? contentType !== 'video/mp4' : contentType === 'video/mp4')) return null;
	if (!url.endsWith(`.${extension}`)) return null;
	return { url, expiresAt, contentType, size: media.size as number };
}

/** Fetches a prepared, same-origin public media URL without session credentials or headers. */
export function fetchPublicMedia(value: string, signal: AbortSignal): Promise<Response> {
	const path = validatedPublicMediaPath(value);
	if (path == null) return Promise.reject(new Error('Invalid public media URL'));
	return window.fetch(path, {
		method: 'GET',
		mode: 'same-origin',
		credentials: 'omit',
		redirect: 'error',
		cache: 'default',
		signal,
	});
}

function validatedPublicMediaPath(value: string): string | null {
	if (!value.startsWith(publicMediaBasePath) || !publicMediaPathPattern.test(value)) return null;
	try {
		const parsed = new URL(value, window.location.origin);
		if (parsed.origin !== window.location.origin || parsed.pathname !== value || parsed.search !== '' || parsed.hash !== '' || parsed.username !== '' || parsed.password !== '') return null;
		return parsed.pathname;
	} catch {
		return null;
	}
}

export type Capabilities = {
	externalFetchEnabled: boolean;
	canUseFxTwitterPreview: boolean;
	refetchIntervalSeconds: number;
	videoPlaybackMode: 'officialEmbed' | 'nativeProxy' | 'linkOnly';
	mediaMode: 'sameOriginProxy';
};

export type PublicVideoMetadata = {
	provider: 'x' | 'youtube' | 'external';
	providerVideoId?: string;
	mediaIdentity?: string;
	posterUrl?: string;
	posterExpiresAt?: string;
	durationMs?: number;
	playbackAvailable?: boolean;
};

/** Legacy first-video record; embed credentials are never included in `videos`. */
export type PublicVideo = PublicVideoMetadata & {
	embedToken?: string;
	embedExpiresAt?: string;
};

export type PublicPost = {
	id: string;
	url: string;
	text: string;
	createdAt?: string;
	author: { name: string; screenName: string; avatarUrl?: string; profileUrl?: string };
	textEntities?: PublicTextEntity[];
	likes: number;
	reposts: number;
	quotes: number;
	replies: number;
	images: Array<{ url: string; width?: number; height?: number; alt?: string }>;
	hasVideo: boolean;
	isSensitive?: boolean;
	videos?: PublicVideoMetadata[];
	video?: PublicVideo;
	quote?: PublicQuotedPost;
};

export type PublicQuotedPost = {
	id: string;
	url: string;
	text: string;
	createdAt?: string;
	author: { name: string; screenName: string; avatarUrl?: string; profileUrl?: string };
	textEntities?: PublicTextEntity[];
	images: Array<{ url: string; width?: number; height?: number; alt?: string }>;
	hasVideo: boolean;
	isSensitive?: boolean;
	videos?: PublicVideoMetadata[];
	video?: PublicVideo;
};

export type PublicTextEntity = {
	kind: 'url' | 'hashtag' | 'mention';
	start: number;
	end: number;
	display: string;
	href?: string;
	tag?: string;
};

export type PreviewResponse = {
	state: 'pending' | 'ready' | 'stale' | 'unavailable' | 'disabled';
	error?: 'temporary' | 'private' | 'not-found' | 'unavailable' | 'invalid-response';
	/** Client-side retry hint; never trusted from the server response. */
	retryable?: boolean;
	postId?: string;
	post?: PublicPost;
	fetchedAt?: string;
	nextPollAt?: string;
	retryAt?: string;
	stale?: boolean;
};

export type AuditSnapshot = {
	id: number;
	postId: string;
	body: string;
	contentHash: string;
	observedAt: string;
};

export type RoleRateLimit = {
	roleId: string;
	perHour: number;
	perDay: number;
};

export type RateLimitSettings = {
	revision: number;
	authenticatedPerHour: number;
	authenticatedPerDay: number;
	globalPerHour: number;
	roleLimits: RoleRateLimit[];
	updatedAt: string;
	updatedBy?: string;
};

export type GlobalRateLimit = {
	revision: number;
	globalPerHour: number;
	updatedAt: string;
	updatedBy?: string;
};
