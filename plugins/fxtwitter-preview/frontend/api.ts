/*
 * SPDX-FileCopyrightText: syuilo and misskey-project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import { ref } from 'vue';
import type { PluginHost } from '@/plugin-api.js';

export const apiSession = ref(0);

let call: PluginHost['api'];
let rawFetch: PluginHost['fetch'];
let showMedia: PluginHost['openMedia'] = async () => undefined;

export function initApi(
	fn: PluginHost['api'],
	fetcher: PluginHost['fetch'] = (path, options) => window.fetch(path, options),
	mediaViewer: PluginHost['openMedia'] = async () => undefined,
): void {
	apiSession.value++;
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
	refetchIntervalSeconds: number;
	videoPlaybackMode: 'officialEmbed' | 'linkOnly';
	mediaMode: 'sameOriginProxy';
};

export type PublicVideo = {
	provider: 'x' | 'external';
	providerVideoId?: string;
	posterUrl?: string;
	posterExpiresAt?: string;
	durationMs?: number;
	embedToken?: string;
	embedExpiresAt?: string;
};

export type PublicPost = {
	quoteUnavailableReason?: UnavailableReason;
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
	unavailableReason?: UnavailableReason;
	state: 'pending' | 'ready' | 'stale' | 'unavailable' | 'disabled';
	error?: 'invalid-response';
	postId?: string;
	post?: PublicPost;
	fetchedAt?: string;
	nextPollAt?: string;
	stale?: boolean;
};

export type AuditSnapshot = {
	bodyFormat: 'normalized_text_v1';
	id: number;
	postId: string;
	body: string;
	contentHash: string;
	observedAt: string;
};

export type UnavailableReason = 'deleted' | 'suspended' | 'private' | 'blocked' | 'unavailable' | 'unknown' | 'fetch_failed';

export function unavailableMessage(reason?: UnavailableReason): string {
	switch (reason) {
		case 'deleted': return 'この投稿は削除されています。';
		case 'suspended': return '投稿者のアカウントは凍結されています。';
		case 'private': return 'この投稿は非公開です。';
		case 'blocked': return 'この投稿へのアクセスはブロックされています。';
		case 'unavailable': return 'この投稿は利用できません。';
		case 'fetch_failed': return '投稿の取得に失敗しました。';
		default: return '投稿の状態を確認できませんでした。';
	}
}

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
