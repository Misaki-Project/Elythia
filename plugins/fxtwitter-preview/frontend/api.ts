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
