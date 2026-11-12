/*
 * SPDX-FileCopyrightText: syuilo and misskey-project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import { cleanup, fireEvent, render, waitFor } from '@testing-library/vue';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { nextTick } from 'vue';
import plugin, { FxPreview, initApi, loadPreview, provider, statusID, truncatePostText } from '../../../../../plugins/fxtwitter-preview/frontend/index';
import FxPreviewSkeleton from '../../../../../plugins/fxtwitter-preview/frontend/FxPreviewSkeleton.vue';
import type { PreviewResponse } from '../../../../../plugins/fxtwitter-preview/frontend/api';
import { setVideoPlaybackMode } from '../../../../../plugins/fxtwitter-preview/frontend/api';
import { components } from '@/components/index.js';
import { directives } from '@/directives/index.js';
import MkPluginUrlPreview from '@/components/MkPluginUrlPreview.vue';
import { _resetPreviewsForTest, launchServerPlugins } from '@/plugin-api.js';
import type { PluginHost, PreviewContext, PreviewInput, PreviewProvider } from '@/plugin-api.js';
import { preferReactive, preferState } from '../setup.unit.js';

// The generated production registry imports this plugin too. Keep this unit
// suite focused on the explicitly imported instance and avoid a module cycle.
vi.mock('@/server-plugins.generated.js', () => ({ serverPlugins: [] }));

const input = { kind: 'url', href: 'https://x.com/alice/status/1234567890' } satisfies PreviewInput;
const context: PreviewContext = { detail: true, revealed: true, active: true, inViewport: true, source: { kind: 'note', noteId: 'note-1' } };

function officialXEmbedURL(id: string, nonce: string): string {
	return `https://platform.twitter.com/embed/Tweet.html?${new URLSearchParams({
		frame: 'false', maxWidth: '560px', width: '550px', id, embedId: nonce,
		theme: 'light', lang: 'ja', hideCard: 'false', hideThread: 'false', dnt: 'true',
	})}`;
}

function videoData(): PreviewResponse {
	return {
		state: 'ready' as const,
		post: {
			id: '1234567890', url: input.href, text: 'video post', createdAt: '2026-10-02',
			author: { name: 'Alice', screenName: 'alice' },
			likes: 1, reposts: 2, quotes: 3, replies: 4, hasVideo: true, images: [], isSensitive: false,
			video: {
				provider: 'x',
				posterUrl: '/api/plugin/fxtwitter-preview/media/1234567890/poster/0?noteId=note-1',
				embedToken: '0123456789abcdef0123456789abcdef',
			},
		},
	};
}

function host(api: PluginHost['api'], me: PluginHost['me'] = { id: 'u1', username: 'u1', host: null }): { host: PluginHost; providers: PreviewProvider[] } {
	const providers: PreviewProvider[] = [];
	return {
		providers,
		host: {
			name: 'fxtwitter-preview', me, api, fetch: vi.fn() as PluginHost['fetch'],
			openMedia: vi.fn() as PluginHost['openMedia'],
			preview: { register(value) { providers.push(value); } },
			postForm: { registerPasteTransform: vi.fn() }, slot() {},
		},
	};
}

function mockMediaFetch(): void {
	fetchMock.mockImplementation(async () => ({
		ok: true,
		headers: new Headers({ 'Content-Type': 'image/png' }),
		blob: async () => new Blob(['image bytes'], { type: 'image/png' }),
	}) as Response);
}

function setPreference(key: string, value: unknown): void {
	preferState[key] = value;
	if (preferReactive[key] != null) preferReactive[key].value = value;
}

describe('FxTwitter preview plugin', () => {
	beforeEach(() => {
		vi.restoreAllMocks();
		setPreference('nsfw', 'respect');
		setPreference('confirmWhenRevealingSensitiveMedia', false);
		vi.stubGlobal('IntersectionObserver', class {
			readonly root = null;
			readonly rootMargin = '0px';
			readonly thresholds = [0];
			constructor(private callback: IntersectionObserverCallback) {}
			disconnect() {}
			observe(target: Element) { this.callback([{ isIntersecting: true, target } as IntersectionObserverEntry], this as unknown as IntersectionObserver); }
			takeRecords(): IntersectionObserverEntry[] { return []; }
			unobserve() {}
		});
		initApi(vi.fn() as PluginHost['api'], vi.fn() as PluginHost['fetch']);
		mockMediaFetch();
		Object.defineProperty(URL, 'createObjectURL', { configurable: true, value: vi.fn((_: Blob) => `blob:fx-${crypto.randomUUID()}`) });
		Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, value: vi.fn() });
	});
	afterEach(() => {
		vi.useRealTimers();
		_resetPreviewsForTest();
		setVideoPlaybackMode('officialEmbed');
		fetchMock.resetMocks();
		vi.unstubAllGlobals();
		window.history.replaceState(null, '', '/');
		cleanup();
	});

	test('matches only exact HTTPS status URLs with numeric IDs', () => {
		expect(statusID(input)).toBe('1234567890');
		for (const href of [
			'http://x.com/a/status/1234567890',
			'https://evil.example/a/status/1234567890',
			'https://user@x.com/a/status/1234567890',
			'https://x.com:444/a/status/1234567890',
			'https://x.com/not-valid!/status/1234567890',
			'https://x.com/abcdefghijklmnop/status/1234567890',
			'https://x.com/a/status/nope',
			'https://x.com/a/status/1234567890/extra',
		]) expect(statusID({ kind: 'url', href })).toBeNull();
		expect(statusID({ kind: 'file', file: { id: 'f', name: 'x', size: 1, url: '/x', declaredMimeType: 'video/mp4', detectedMimeType: null } })).toBeNull();
	});

	test('registers for anonymous viewers when public capabilities enable external fetch', async () => {
		const disabled = host(vi.fn(async () => ({ externalFetchEnabled: false, canUseFxTwitterPreview: false })) as PluginHost['api']);
		await plugin.setup(disabled.host);
		expect(disabled.providers).toHaveLength(0);

		const anonymousAPI = vi.fn(async () => ({ externalFetchEnabled: true, canUseFxTwitterPreview: false }));
		const enabled = host(anonymousAPI as PluginHost['api'], null);
		await plugin.setup(enabled.host);
		expect(enabled.providers).toHaveLength(1);
		expect(enabled.providers[0].presentation?.(input, { ...context, detail: false })).toBe('disabled');
		await expect(enabled.providers[0].load(input, { ...context, detail: false }, new AbortController().signal)).resolves.toEqual({ state: 'disabled' });
		expect(anonymousAPI).toHaveBeenCalledTimes(1);

		vi.spyOn(console, 'warn').mockImplementation(() => undefined);
		const failed = host(vi.fn(async () => { throw new Error('404'); }) as PluginHost['api']);
		await plugin.setup(failed.host);
		expect(failed.providers).toHaveLength(0);
	});

	test('uses the checked native permission for authenticated provider registration', async () => {
		const denied = host(vi.fn(async () => ({ externalFetchEnabled: true, canUseFxTwitterPreview: false })) as PluginHost['api']);
		await plugin.setup(denied.host);
		expect(denied.providers).toHaveLength(0);

		const allowed = host(vi.fn(async () => ({ externalFetchEnabled: true, canUseFxTwitterPreview: true })) as PluginHost['api']);
		await plugin.setup(allowed.host);
		expect(allowed.providers).toHaveLength(1);
	});

	test('allows timeline loading so the generic viewport and CW gates decide when to fetch', () => {
		expect(provider.requiresDetail).toBe(false);
		expect(provider.loadingComponent).toBeDefined();
		const result = render(provider.loadingComponent!);
		expect(result.getByRole('status', { name: 'Xの投稿を取得しています' })).toBeTruthy();
	});

	test('switches timeline presentation modes while detail remains automatic', () => {
		const timeline = { ...context, detail: false };
		expect(provider.preferenceKey).toBe('xTimelinePreviewMode');
		expect(provider.presentation?.(input, { ...timeline, preferenceValue: 'ogp' })).toBe('manual');
		expect(provider.presentation?.(input, { ...context, preferenceValue: 'ogp' })).toBe('automatic');
		expect(provider.presentation?.(input, { ...timeline, preferenceValue: 'text' })).toBe('automatic');
		expect(provider.presentation?.(input, { ...timeline, preferenceValue: 'media' })).toBe('automatic');
	});

	test('truncates at exact grapheme and line boundaries', () => {
		expect(truncatePostText('あ'.repeat(139))).toEqual({ text: 'あ'.repeat(139), truncated: false });
		expect(truncatePostText('あ'.repeat(140))).toEqual({ text: 'あ'.repeat(139), truncated: true });
		const emoji = '👨‍👩‍👧‍👦';
		expect(truncatePostText(emoji.repeat(140))).toEqual({ text: emoji.repeat(139), truncated: true });
		expect(truncatePostText('1\n2\n3\n4\n5')).toEqual({ text: '1\n2\n3\n4\n5', truncated: false });
		expect(truncatePostText('1\n2\n3\n4\n5\n6')).toEqual({ text: '1\n2\n3\n4\n5', truncated: true });
	});

	test('runs through the generated plugin entry and generic preview coordinator with mocked APIs', async () => {
		const paths: string[] = [];
		const enabled = host(vi.fn(async path => {
			paths.push(path);
			if (path.endsWith('/capabilities')) return { externalFetchEnabled: true, canUseFxTwitterPreview: true, refetchIntervalSeconds: 3600, videoPlaybackMode: 'officialEmbed', mediaMode: 'sameOriginProxy' };
			if (path.endsWith('/preview')) return videoData();
			throw new Error(`unexpected API path: ${path}`);
		}) as PluginHost['api']);
		await plugin.setup(enabled.host);
		expect(enabled.providers).toHaveLength(1);
		await launchServerPlugins([{
			name: 'integration-bridge',
			setup(pluginHost) { pluginHost.preview.register(enabled.providers[0]); },
		}]);

		const result = render(MkPluginUrlPreview, {
			props: { url: input.href, detail: true, revealed: true, source: context.source },
			global: { directives, components },
		});
		await waitFor(() => expect(result.getByText('video post')).toBeTruthy());
		expect(paths).toEqual([
			'plugin/fxtwitter-preview/capabilities',
			'plugin/fxtwitter-preview/preview',
		]);
		expect(fetchMock).not.toHaveBeenCalled();
	});

	test('keeps an initial transport failure inside the Fx preview card', async () => {
		const enabled = host(vi.fn(async path => {
			if (path.endsWith('/capabilities')) return { externalFetchEnabled: true, canUseFxTwitterPreview: true, refetchIntervalSeconds: 3600, videoPlaybackMode: 'officialEmbed', mediaMode: 'sameOriginProxy' };
			throw Object.assign(new Error('secret transport detail'), { code: 'NETWORK_ERROR' });
		}) as PluginHost['api']);
		await plugin.setup(enabled.host);
		await launchServerPlugins([{
			name: 'integration-bridge',
			setup(pluginHost) { pluginHost.preview.register(enabled.providers[0]); },
		}]);

		const result = render(MkPluginUrlPreview, {
			props: { url: input.href, detail: true, revealed: true, source: context.source },
			global: { directives, components },
		});
		await waitFor(() => expect(result.getByText('一時的な取得エラーです。')).toBeTruthy());
		expect(result.getByRole('article')).toBeTruthy();
		expect(result.getByRole('button', { name: '再試行' })).toBeTruthy();
		expect(result.getByRole('link', { name: 'Xで開く' }).getAttribute('href')).toBe(input.href);
		expect(result.queryByText('secret transport detail')).toBeNull();
	});

	test('normalizes legacy null media collections at the API boundary and still loads avatar and poster', async () => {
		const raw = videoData() as unknown as { state: string; post: Record<string, unknown> };
		raw.post.images = null;
		raw.post.author = { name: 'Alice', screenName: 'alice', avatarUrl: '/api/plugin/fxtwitter-preview/media/1234567890/avatar/0?noteId=note-1' };
		raw.post.quote = {
			id: '2234567890', url: 'https://x.com/i/status/2234567890', text: 'quoted post',
			author: { name: 'Bob', screenName: 'bob' }, images: null, hasVideo: false,
		};
		const pluginFetch = vi.fn(async (path: string, options?: RequestInit) => await fetchMock(path, options));
		initApi(vi.fn(async () => raw) as PluginHost['api'], pluginFetch as PluginHost['fetch']);

		const normalized = await loadPreview(input, context, new AbortController().signal);
		expect(normalized.post?.images).toEqual([]);
		expect(normalized.post?.quote?.images).toEqual([]);
		const result = render(FxPreview, { props: { data: normalized, input, context } });
		await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
		expect(pluginFetch.mock.calls.some(([path]) => String(path).includes('/avatar/'))).toBe(true);
		expect(pluginFetch.mock.calls.some(([path]) => String(path).includes('/poster/'))).toBe(true);
		expect(result.queryByText('メディアを読み込めませんでした。')).toBeNull();
	});

	test('turns an invalid media collection into a safe preview error', async () => {
		const raw = videoData() as unknown as { state: string; post: Record<string, unknown> };
		raw.post.images = 'not-an-array';
		initApi(vi.fn(async () => raw) as PluginHost['api']);

		const normalized = await loadPreview(input, context, new AbortController().signal);
		expect(normalized).toMatchObject({ state: 'unavailable', error: 'invalid-response' });
		const result = render(FxPreview, { props: { data: normalized, input, context } });
		expect(result.getByText('プレビューを表示できませんでした。')).toBeTruthy();
	});

	test('normalizes only coherent video provider metadata', async () => {
		const accepted = videoData() as unknown as { state: string; post: Record<string, unknown> };
		accepted.post.video = {
			provider: 'youtube', providerVideoId: 'dQw4w9WgXcQ',
			posterUrl: '/api/plugin/fxtwitter-preview/media/1234567890/poster/0?noteId=note-1', embedToken: 'token',
		};
		initApi(vi.fn(async () => accepted) as PluginHost['api']);
		await expect(loadPreview(input, context, new AbortController().signal)).resolves.toMatchObject({
			state: 'ready', post: { video: { provider: 'youtube', providerVideoId: 'dQw4w9WgXcQ' } },
		});

		for (const video of [
			{ provider: 'youtube', providerVideoId: 'too-short', embedToken: 'token' },
			{ provider: 'external', providerVideoId: 'dQw4w9WgXcQ' },
			{ provider: 'external', embedToken: 'token' },
			{ provider: 'unknown' },
		]) {
			accepted.post.video = video;
			await expect(loadPreview(input, context, new AbortController().signal)).resolves.toMatchObject({ state: 'unavailable', error: 'invalid-response' });
		}

		accepted.post.video = { posterUrl: '/legacy-poster' };
		await expect(loadPreview(input, context, new AbortController().signal)).resolves.toMatchObject({
			state: 'ready', post: { video: { provider: 'external' } },
		});

		const withIdentity = videoData() as unknown as { state: string; post: Record<string, unknown> };
		withIdentity.post.video = { provider: 'x', mediaIdentity: 'a'.repeat(64), embedToken: 'token' };
		initApi(vi.fn(async () => withIdentity) as PluginHost['api']);
		await expect(loadPreview(input, context, new AbortController().signal)).resolves.toMatchObject({
			state: 'ready', post: { video: { mediaIdentity: 'a'.repeat(64) }, videos: [{ mediaIdentity: 'a'.repeat(64) }] },
		});
		withIdentity.post.video = { provider: 'x', mediaIdentity: 'not-a-hash', embedToken: 'token' };
		await expect(loadPreview(input, context, new AbortController().signal)).resolves.toMatchObject({ state: 'unavailable', error: 'invalid-response' });

		const invalidAvailability = videoData() as unknown as { state: string; post: Record<string, unknown> };
		invalidAvailability.post.videos = [{ provider: 'x', playbackAvailable: 'true' }];
		initApi(vi.fn(async () => invalidAvailability) as PluginHost['api']);
		await expect(loadPreview(input, context, new AbortController().signal)).resolves.toMatchObject({ state: 'unavailable', error: 'invalid-response' });
	});

	test('uses token-bearing legacy video as token-free poster metadata fallback', async () => {
		const raw = videoData();
		raw.post!.video!.playbackAvailable = true;
		initApi(vi.fn(async () => raw) as PluginHost['api']);
		const normalized = await loadPreview(input, context, new AbortController().signal);
		expect(normalized).toMatchObject({
			state: 'ready',
			post: {
				videos: [{ provider: 'x', posterUrl: '/api/plugin/fxtwitter-preview/media/1234567890/poster/0?noteId=note-1' }],
				video: { embedToken: '0123456789abcdef0123456789abcdef' },
			},
		});
		if (normalized.state === 'ready') expect(normalized.post?.videos?.[0]).not.toHaveProperty('embedToken');
	});

	test('preserves native playback availability through API normalization into indexed play actions', async () => {
		const raw = videoData() as unknown as { state: string; post: Record<string, unknown> };
		(raw.post.video as Record<string, unknown>).playbackAvailable = true;
		raw.post.videos = [0, 1].map(index => ({
			provider: 'x', playbackAvailable: true,
			posterUrl: `/api/plugin/fxtwitter-preview/media/1234567890/poster/${index}?noteId=note-1`,
		}));
		const fetcher = vi.fn(async (path: string, options?: RequestInit) => path.endsWith('/media/video')
			? new Response(new Blob(['selected'], { type: 'video/mp4' }), { headers: { 'Content-Type': 'video/mp4' } })
			: new Response(new Blob(['poster'], { type: 'image/png' }), { headers: { 'Content-Type': 'image/png' } }));
		const openMedia = vi.fn(async (_items: Parameters<PluginHost['openMedia']>[0], _initialId?: string) => undefined);
		initApi(vi.fn(async () => raw) as PluginHost['api'], fetcher as PluginHost['fetch'], openMedia as PluginHost['openMedia']);
		setVideoPlaybackMode('nativeProxy');
		const normalized = await loadPreview(input, context, new AbortController().signal);
		const result = render(FxPreview, { props: { data: normalized, input, context: { ...context, preferenceValue: 'media' } } });
		const buttons = await waitFor(() => {
			const found = result.container.querySelectorAll('[data-fx-video-play]');
			if (found.length !== 2) throw new Error(`expected two native video buttons, found ${found.length}`);
			return Array.from(found);
		});
		expect(normalized.post?.videos?.map(video => video.playbackAvailable)).toEqual([true, true]);
		await fireEvent.click(buttons[1]);
		await waitFor(() => expect(openMedia).toHaveBeenCalledTimes(1));
		expect(JSON.parse(String(fetcher.mock.calls.find(([path]) => path.endsWith('/media/video'))?.[1]?.body))).toMatchObject({
			noteId: 'note-1', postId: '1234567890', target: 'main', index: 1, token: '0123456789abcdef0123456789abcdef',
		});
	});

	test('refreshes an expired native capability and keeps the selected video when refreshed indexes reorder', async () => {
		const identityA = 'a'.repeat(64);
		const identityB = 'b'.repeat(64);
		const oldResponse = videoData();
		oldResponse.post!.video = {
			provider: 'x', mediaIdentity: identityA, embedToken: 'expired-token', embedExpiresAt: '2000-01-01T00:00:00.000Z',
			posterUrl: '/api/plugin/fxtwitter-preview/media/1234567890/poster/0?noteId=note-1', durationMs: 3000, playbackAvailable: true,
		};
		oldResponse.post!.videos = [
			{ provider: 'x', mediaIdentity: identityA, posterUrl: '/api/plugin/fxtwitter-preview/media/1234567890/poster/0?noteId=note-1', durationMs: 3000, playbackAvailable: true },
			{ provider: 'x', mediaIdentity: identityB, posterUrl: '/api/plugin/fxtwitter-preview/media/1234567890/poster/1?noteId=note-1', durationMs: 5000, playbackAvailable: true },
		];
		const refreshedResponse: PreviewResponse = {
			...oldResponse,
			post: {
				...oldResponse.post!,
				video: {
					provider: 'x', mediaIdentity: identityB, embedToken: 'renewed-token', embedExpiresAt: '2099-01-01T00:00:00.000Z',
					posterUrl: '/api/plugin/fxtwitter-preview/media/1234567890/poster/0?noteId=note-1', durationMs: 5000, playbackAvailable: true,
				},
				videos: [
					{ provider: 'x', mediaIdentity: identityB, posterUrl: '/api/plugin/fxtwitter-preview/media/1234567890/poster/0?noteId=note-1', durationMs: 5000, playbackAvailable: true },
					{ provider: 'x', mediaIdentity: identityA, posterUrl: '/api/plugin/fxtwitter-preview/media/1234567890/poster/1?noteId=note-1', durationMs: 3000, playbackAvailable: true },
				],
			},
		};
		const api = vi.fn(async (path: string) => {
			if (path.endsWith('/preview')) return refreshedResponse;
			throw new Error(`unexpected API call: ${path}`);
		});
		const fetcher = vi.fn(async (path: string, options?: RequestInit) => path.endsWith('/media/video')
			? new Response(new Blob(['selected video'], { type: 'video/mp4' }), { headers: new Headers({ 'Content-Type': 'video/mp4' }) })
			: new Response(new Blob(['poster'], { type: 'image/png' }), { headers: new Headers({ 'Content-Type': 'image/png' }) }));
		const openMedia = vi.fn(async () => undefined);
		initApi(api as PluginHost['api'], fetcher as PluginHost['fetch'], openMedia as PluginHost['openMedia']);
		setVideoPlaybackMode('nativeProxy');
		const result = render(FxPreview, { props: { data: oldResponse, input, context: { ...context, preferenceValue: 'media' } } });
		const buttons = await waitFor(() => {
			const found = result.container.querySelectorAll('[data-fx-video-play]');
			if (found.length !== 2) throw new Error(`expected two native video buttons, found ${found.length}`);
			return Array.from(found);
		});
		await fireEvent.click(buttons[0]);
		await waitFor(() => expect(openMedia).toHaveBeenCalledTimes(1));
		expect(api).toHaveBeenCalledTimes(1);
		expect(fetcher.mock.calls.some(([path]) => path.endsWith('/media/video'))).toBe(true);
		expect(api).toHaveBeenCalledWith('plugin/fxtwitter-preview/preview', { noteId: 'note-1', postId: '1234567890' }, expect.anything());
		const request = fetcher.mock.calls.find(([path]) => path.endsWith('/media/video'));
		expect(JSON.parse(String(request?.[1]?.body))).toMatchObject({ index: 1, token: 'renewed-token' });
	});

	test('updates an old preview without identity and requires a fresh explicit play click', async () => {
		const oldResponse = videoData();
		oldResponse.post!.video = {
			provider: 'x', embedToken: 'expired-token', embedExpiresAt: '2000-01-01T00:00:00.000Z',
			posterUrl: '/api/plugin/fxtwitter-preview/media/1234567890/poster/0?noteId=note-1', durationMs: 3000, playbackAvailable: true,
		};
		const refreshedResponse: PreviewResponse = {
			...oldResponse,
			post: {
				...oldResponse.post!,
				video: {
					provider: 'x', mediaIdentity: 'c'.repeat(64), embedToken: 'renewed-token', embedExpiresAt: '2099-01-01T00:00:00.000Z',
					posterUrl: '/api/plugin/fxtwitter-preview/media/1234567890/poster/0?noteId=note-1', durationMs: 3000, playbackAvailable: true,
				},
			},
		};
		const api = vi.fn(async (path: string) => {
			if (path.endsWith('/preview')) return refreshedResponse;
			throw new Error(`unexpected API call: ${path}`);
		});
		const fetcher = vi.fn(async (path: string, options?: RequestInit) => path.endsWith('/media/video')
			? new Response(new Blob(['selected video'], { type: 'video/mp4' }), { headers: new Headers({ 'Content-Type': 'video/mp4' }) })
			: new Response(new Blob(['poster'], { type: 'image/png' }), { headers: new Headers({ 'Content-Type': 'image/png' }) }));
		const openMedia = vi.fn(async () => undefined);
		initApi(api as PluginHost['api'], fetcher as PluginHost['fetch'], openMedia as PluginHost['openMedia']);
		setVideoPlaybackMode('nativeProxy');
		const result = render(FxPreview, { props: { data: oldResponse, input, context: { ...context, preferenceValue: 'media' } } });
		const play = await waitFor(() => result.getByRole('button', { name: '動画を再生' }));
		await fireEvent.click(play);
		await waitFor(() => expect(result.getByRole('button', { name: 'もう一度試す' })).toBeTruthy());
		expect(api).toHaveBeenCalledTimes(1);
		expect(fetcher.mock.calls.some(([path]) => path.endsWith('/media/video'))).toBe(false);
		await fireEvent.click(result.getByRole('button', { name: 'もう一度試す' }));
		await waitFor(() => expect(openMedia).toHaveBeenCalledTimes(1));
		expect(api).toHaveBeenCalledTimes(1);
		expect(JSON.parse(String(fetcher.mock.calls.find(([path]) => path.endsWith('/media/video'))?.[1]?.body))).toMatchObject({ index: 0, token: 'renewed-token' });
	});

	test('refreshes an expired official X embed without native media identity in one click', async () => {
		const oldResponse = videoData();
		oldResponse.post!.video!.embedExpiresAt = '2000-01-01T00:00:00.000Z';
		const refreshedResponse: PreviewResponse = {
			...oldResponse,
			post: {
				...oldResponse.post!,
				video: {
					provider: 'x', embedToken: 'renewed-official-token', embedExpiresAt: '2099-01-01T00:00:00.000Z',
					posterUrl: '/api/plugin/fxtwitter-preview/media/1234567890/poster/0?noteId=note-1',
				},
			},
		};
		const api = vi.fn(async (path: string) => {
			if (path.endsWith('/preview')) return refreshedResponse;
			throw new Error(`unexpected API call: ${path}`);
		});
		const fetcher = vi.fn(async (_path: string, options?: RequestInit) => {
			if (options?.method !== 'POST') return new Response(new Blob(['poster'], { type: 'image/png' }), { headers: new Headers({ 'Content-Type': 'image/png' }) });
			const body = JSON.parse(String(options.body)) as { nonce: string };
			return new Response(JSON.stringify({ url: officialXEmbedURL('1234567890', body.nonce) }), { status: 200, headers: new Headers({ 'Content-Type': 'application/json' }) });
		});
		initApi(api as PluginHost['api'], fetcher as PluginHost['fetch']);
		const result = render(FxPreview, { props: { data: oldResponse, input, context } });
		await fireEvent.click(await waitFor(() => result.getByRole('button', { name: '動画を再生' })));
		const frame = await waitFor(() => result.getByTitle('Xの投稿動画'));
		expect(api).toHaveBeenCalledTimes(1);
		expect(fetcher.mock.calls.filter(([, options]) => options?.method === 'POST')).toHaveLength(1);
		const shellRequest = fetcher.mock.calls.find(([, options]) => options?.method === 'POST');
		expect(JSON.parse(String(shellRequest?.[1]?.body))).toMatchObject({ token: 'renewed-official-token', target: 'main' });
		expect(frame.getAttribute('src')).toMatch(/^https:\/\/platform\.twitter\.com\/embed\/Tweet\.html\?/);
	});

	test('preserves independent main and quote sensitivity while leaving missing values unknown', async () => {
		const raw = videoData() as unknown as { state: string; post: Record<string, unknown> };
		raw.post.isSensitive = false;
		raw.post.quote = {
			id: '2234567890', url: 'https://x.com/i/status/2234567890', text: 'quoted post',
			author: { name: 'Bob', screenName: 'bob' }, images: [], hasVideo: false, isSensitive: true,
		};
		initApi(vi.fn(async () => raw) as PluginHost['api']);
		const normalized = await loadPreview(input, context, new AbortController().signal);
		expect(normalized.post?.isSensitive).toBe(false);
		expect(normalized.post?.quote?.isSensitive).toBe(true);

		delete raw.post.isSensitive;
		delete (raw.post.quote as Record<string, unknown>).isSensitive;
		const unknown = await loadPreview(input, context, new AbortController().signal);
		expect(unknown.post?.isSensitive).toBeUndefined();
		expect(unknown.post?.quote?.isSensitive).toBeUndefined();
	});

	test('requires a source note and returns a retryable card state after bounded pending polls', async () => {
		const signalCalls: Array<AbortSignal | undefined> = [];
		initApi(vi.fn(async (_path, _params, options) => {
			signalCalls.push(options?.signal);
			return { state: 'pending' };
		}) as PluginHost['api']);
		await expect(loadPreview(input, { ...context, source: null }, new AbortController().signal)).rejects.toThrow('source note');

		vi.useFakeTimers();
		const controller = new AbortController();
		const pending = loadPreview(input, context, controller.signal);
		const pendingResult = expect(pending).resolves.toMatchObject({ state: 'unavailable', error: 'temporary', retryable: true });
		await vi.advanceTimersByTimeAsync(750 * 7);
		await pendingResult;
		expect(signalCalls).toHaveLength(8);
		expect(signalCalls.every(signal => signal != null && signal !== controller.signal && !signal.aborted)).toBe(true);
	});

	test('maps initial request and timeout failures to safe card errors, while access failures cannot retry', async () => {
		const rawFailure = Object.assign(new Error('sensitive server details'), { code: 'NETWORK_ERROR' });
		initApi(vi.fn(async () => { throw rawFailure; }) as PluginHost['api']);
		await expect(loadPreview(input, context, new AbortController().signal)).resolves.toMatchObject({
			state: 'unavailable', error: 'temporary', retryable: true,
		});
		const networkCard = render(FxPreview, { props: { data: { state: 'unavailable', error: 'temporary', retryable: true } as PreviewResponse, input, context } });
		expect(networkCard.getByRole('article')).toBeTruthy();
		expect(networkCard.getByRole('button', { name: '再試行' })).toBeTruthy();
		expect(networkCard.queryByText('sensitive server details')).toBeNull();
		networkCard.unmount();

		vi.useFakeTimers();
		let requestSignal: AbortSignal | undefined;
		initApi(vi.fn((_path: string, _params?: Record<string, unknown>, options?: { signal?: AbortSignal }) => {
			requestSignal = options?.signal;
			return new Promise((_resolve, reject) => options?.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')), { once: true }));
		}) as PluginHost['api']);
		const timedOut = loadPreview(input, context, new AbortController().signal);
		await vi.advanceTimersByTimeAsync(15_000);
		await expect(timedOut).resolves.toMatchObject({ state: 'unavailable', error: 'temporary', retryable: true });
		expect(requestSignal?.aborted).toBe(true);
		vi.useRealTimers();

		initApi(vi.fn(async () => { throw Object.assign(new Error('do not expose'), { code: 'PREVIEW_FORBIDDEN', status: 403 }); }) as PluginHost['api']);
		const denied = await loadPreview(input, context, new AbortController().signal);
		expect(denied).toMatchObject({ state: 'unavailable', error: 'unavailable', retryable: false });
		const deniedCard = render(FxPreview, { props: { data: denied, input, context } });
		expect(deniedCard.getByRole('article')).toBeTruthy();
		expect(deniedCard.getByText(/閲覧権限/)).toBeTruthy();
		expect(deniedCard.queryByRole('button', { name: '再試行' })).toBeNull();
		expect(deniedCard.queryByText('do not expose')).toBeNull();
	});

	test('initial loading card has a readable status, X link, and motion-safe dot indicator', () => {
		const result = render(FxPreviewSkeleton, { props: { input } });
		const status = result.getByRole('status', { name: 'Xの投稿を取得しています' });
		expect(status.getAttribute('aria-busy')).toBe('true');
		expect(result.getByRole('link', { name: 'Xで開く' }).getAttribute('href')).toBe(input.href);
		expect(result.container.querySelectorAll('[aria-hidden="true"] i')).toHaveLength(3);
	});

	test('honors the server pending poll time before retrying', async () => {
		vi.useFakeTimers();
		const call = vi.fn(async () => ({ state: 'pending', nextPollAt: new Date(Date.now() + 2000).toISOString() }));
		initApi(call as PluginHost['api']);
		const controller = new AbortController();
		const pending = loadPreview(input, context, controller.signal);
		await Promise.resolve();
		expect(call).toHaveBeenCalledTimes(1);
		await vi.advanceTimersByTimeAsync(1999);
		expect(call).toHaveBeenCalledTimes(1);
		controller.abort();
		await expect(pending).rejects.toMatchObject({ name: 'AbortError' });
		expect(call).toHaveBeenCalledTimes(1);
	});

	test('stops pending polling immediately when aborted', async () => {
		vi.useFakeTimers();
		const call = vi.fn(async () => ({ state: 'pending' }));
		initApi(call as PluginHost['api']);
		const controller = new AbortController();
		const pending = loadPreview(input, context, controller.signal);
		await Promise.resolve();
		controller.abort();
		await expect(pending).rejects.toMatchObject({ name: 'AbortError' });
		expect(call).toHaveBeenCalledTimes(1);
	});

	test('normalizes safe unavailable reasons and retry time without exposing arbitrary errors', async () => {
		const apiMock = vi.fn(async () => ({
			state: 'unavailable', error: 'private', retryAt: '2026-11-12T10:00:00Z', postId: '1234567890',
		}));
		initApi(apiMock as PluginHost['api']);
		await expect(loadPreview(input, context, new AbortController().signal)).resolves.toMatchObject({
			state: 'unavailable', error: 'private', retryAt: '2026-11-12T10:00:00Z',
		});

		apiMock.mockImplementation(async () => ({
			state: 'unavailable', error: 'private-url-leak: user is hidden', retryAt: 'invalid', postId: '1234567890',
		}));
		await expect(loadPreview(input, context, new AbortController().signal)).resolves.toMatchObject({
			state: 'unavailable', error: 'unavailable',
		});
	});

	test('unavailable preview explains the result and retries only after a click', async () => {
		const apiMock = vi.fn(async (_endpoint: string, _params?: Record<string, unknown>) => videoData());
		initApi(apiMock as PluginHost['api']);
		const data = {
			state: 'unavailable', error: 'temporary', retryAt: new Date(Date.now() - 1000).toISOString(),
		} as unknown as PreviewResponse;
		const result = render(FxPreview, { props: { data, input, context } });

		expect(result.getByText(/一時的/)).toBeTruthy();
		expect(apiMock).not.toHaveBeenCalled();
		await fireEvent.click(result.getByRole('button', { name: '再試行' }));
		await waitFor(() => expect(apiMock).toHaveBeenCalledTimes(1));
		await waitFor(() => expect(result.getByText('video post')).toBeTruthy());
		expect(apiMock.mock.calls[0][1]).toMatchObject({ noteId: 'note-1', postId: '1234567890' });
	});

	test('stale content is replaced when a manual retry confirms the post is private', async () => {
		const apiMock = vi.fn(async () => ({
			state: 'unavailable', error: 'private', retryAt: new Date(Date.now() + 60_000).toISOString(), postId: '1234567890',
		}));
		initApi(apiMock as PluginHost['api']);
		const data: PreviewResponse = {
			...videoData(), state: 'stale', error: 'temporary', retryAt: new Date(Date.now() - 1000).toISOString(),
		};
		const result = render(FxPreview, { props: { data, input, context } });
		expect(result.getByText('video post')).toBeTruthy();
		await fireEvent.click(result.getByRole('button', { name: '再試行' }));

		await waitFor(() => expect(result.getByText('この投稿は非公開として確認されました。')).toBeTruthy());
		expect(result.queryByText('video post')).toBeNull();
		expect(result.getByText(/次回再試行可能:/)).toBeTruthy();
		expect((result.getByRole('button', { name: '再試行' }) as HTMLButtonElement).disabled).toBe(true);
	});

	test('retry cooldown enables manual action without automatically fetching and blocks duplicate clicks', async () => {
		let resolvePreview: ((value: unknown) => void) | undefined;
		const apiMock = vi.fn(() => new Promise(resolve => { resolvePreview = resolve; }));
		initApi(apiMock as PluginHost['api']);
		const data = {
			state: 'unavailable', error: 'temporary', retryAt: new Date(Date.now() + 50).toISOString(),
		} as unknown as PreviewResponse;
		const result = render(FxPreview, { props: { data, input, context } });
		const button = result.getByRole('button', { name: '再試行' }) as HTMLButtonElement;
		expect(button.disabled).toBe(true);
		expect(apiMock).not.toHaveBeenCalled();
		await new Promise(resolve => setTimeout(resolve, 80));
		await waitFor(() => expect(button.disabled).toBe(false));
		expect(apiMock).not.toHaveBeenCalled();

		await fireEvent.click(button);
		await fireEvent.click(button);
		expect(apiMock).toHaveBeenCalledTimes(1);
		expect(button.disabled).toBe(true);
		resolvePreview?.(videoData());
		await waitFor(() => expect(result.getByText('video post')).toBeTruthy());

		await result.rerender({ data, input, context: { ...context, inViewport: false } });
		await result.rerender({ data, input, context });
		expect(result.getByText('video post')).toBeTruthy();
		expect(apiMock).toHaveBeenCalledTimes(1);
	});

	test('an expired retryAt returned after mount enables the button instead of leaving it stuck', async () => {
		vi.useFakeTimers();
		const startedAt = new Date('2026-11-12T10:00:00.000Z');
		vi.setSystemTime(startedAt);
		let resolvePreview: ((value: unknown) => void) | undefined;
		const apiMock = vi.fn(() => new Promise(resolve => { resolvePreview = resolve; }));
		initApi(apiMock as PluginHost['api']);
		const data = {
			state: 'unavailable', error: 'temporary', retryAt: new Date(startedAt.getTime() - 1000).toISOString(),
		} as unknown as PreviewResponse;
		const result = render(FxPreview, { props: { data, input, context } });
		const button = result.getByRole('button', { name: '再試行' }) as HTMLButtonElement;
		expect(button.disabled).toBe(false);
		await fireEvent.click(button);
		await vi.advanceTimersByTimeAsync(2000);
		resolvePreview?.({
			state: 'unavailable', error: 'temporary', retryAt: new Date(startedAt.getTime() + 1000).toISOString(),
		});
		await Promise.resolve();
		await Promise.resolve();
		await vi.advanceTimersByTimeAsync(0);
		await nextTick();
		expect(button.disabled).toBe(false);
		expect(result.getByText('再試行できます。')).toBeTruthy();
		expect(apiMock).toHaveBeenCalledTimes(1);
	});

	test('source change aborts retry and discards a late response', async () => {
		let resolvePreview: ((value: unknown) => void) | undefined;
		let retrySignal: AbortSignal | undefined;
		const apiMock = vi.fn(async (_path: string, _params: Record<string, unknown>, options?: { signal?: AbortSignal }) => {
			retrySignal = options?.signal;
			return await new Promise(resolve => { resolvePreview = resolve; });
		});
		initApi(apiMock as PluginHost['api']);
		const data = { state: 'unavailable', error: 'temporary' } as unknown as PreviewResponse;
		const result = render(FxPreview, { props: { data, input, context } });
		await fireEvent.click(result.getByRole('button', { name: '再試行' }));
		expect(apiMock).toHaveBeenCalledTimes(1);

		await result.rerender({ data, input, context: { ...context, source: { kind: 'note', noteId: 'note-2' } } });
		await waitFor(() => expect(retrySignal?.aborted).toBe(true));
		resolvePreview?.(videoData());
		await Promise.resolve();
		expect(result.queryByText('video post')).toBeNull();
	});

	test.each(['NOTE_UNAVAILABLE', 'POLICY_UNAVAILABLE'])('permission failure %s removes stale content instead of leaving the last response visible', async code => {
		const apiMock = vi.fn(async () => { throw Object.assign(new Error('preview denied'), { code, status: code === 'NOTE_UNAVAILABLE' ? 404 : 500 }); });
		initApi(apiMock as PluginHost['api']);
		const data: PreviewResponse = { ...videoData(), state: 'stale', error: 'temporary' };
		const result = render(FxPreview, { props: { data, input, context } });
		expect(result.getByText('video post')).toBeTruthy();
		await fireEvent.click(result.getByRole('button', { name: '再試行' }));
		await waitFor(() => expect(apiMock).toHaveBeenCalledTimes(1));
		await waitFor(() => expect(result.queryByText('video post')).toBeNull());
		expect(result.getByText('閲覧権限を確認できないため、プレビューを表示できません。')).toBeTruthy();
		expect(result.queryByRole('button', { name: '再試行' })).toBeNull();
	});

	test('pending state keeps a canonical source link after bounded polling', async () => {
		const result = render(FxPreview, { props: { data: { state: 'pending' }, input, context } });
		const fallback = result.getByRole('link', { name: 'Xで開く' });
		expect(result.getByRole('article')).toBeTruthy();
		expect(result.getByRole('status').getAttribute('aria-label')).toBe('Xの投稿を取得しています');
		expect(fallback.getAttribute('href')).toBe(input.href);
	});

	test('describes NOT_FOUND without asserting deletion and gives X fallback inside the card', () => {
		const result = render(FxPreview, { props: { data: { state: 'unavailable', error: 'not-found' }, input, context }, global: { components } });
		expect(result.getByRole('article')).toBeTruthy();
		expect(result.getByText(/見つからない|停止/)).toBeTruthy();
		expect(result.queryByText(/削除された状態/)).toBeNull();
		const openLink = result.getByRole('link', { name: 'Xで開く' });
		expect(openLink.getAttribute('href')).toBe(input.href);
		expect(openLink.className).toContain('_button');
		expect(result.getByRole('button', { name: '再試行' }).className).toContain('_button');
	});

	test('renders decoded HTML-like main and quote text only as inert text nodes', async () => {
		const data = videoData();
		data.post!.text = '<script>globalThis.__fxXss = true</script> <img src=x onerror=globalThis.__fxXss=true> &lt;literal&gt;';
		data.post!.hasVideo = false;
		data.post!.video = undefined;
		data.post!.quote = {
			id: '2234567890', url: 'https://x.com/i/status/2234567890', text: '<b onmouseover=globalThis.__fxXss=true>quoted</b>',
			author: { name: 'Bob', screenName: 'bob' }, images: [], hasVideo: false,
		};
		const result = render(FxPreview, { props: { data, input, context } });
		await waitFor(() => expect(result.container.textContent).toContain('<script>globalThis.__fxXss = true</script>'));
		expect(result.container.textContent).toContain('<img src=x onerror=globalThis.__fxXss=true> &lt;literal&gt;');
		expect(result.container.textContent).toContain('<b onmouseover=globalThis.__fxXss=true>quoted</b>');
		expect(result.container.querySelector('script')).toBeNull();
		expect(result.container.querySelector('img')).toBeNull();
		expect(result.container.querySelector('[onerror], [onmouseover]')).toBeNull();
		expect((globalThis as typeof globalThis & { __fxXss?: boolean }).__fxXss).toBeUndefined();
	});

	test('loads same-origin media with an authorization header and revokes object URLs', async () => {
		const pluginFetch = vi.fn(async (_path: string, options?: RequestInit) => await fetchMock('/api/plugin/fxtwitter-preview/media/proxy', options));
		initApi(vi.fn() as PluginHost['api'], pluginFetch as PluginHost['fetch']);
		const data = {
			state: 'ready' as const,
			post: {
				id: '1234567890', url: input.href, text: 'hello', createdAt: '2026-10-02',
				author: { name: 'Alice', screenName: 'alice', avatarUrl: '/api/plugin/fxtwitter-preview/media/1234567890/avatar/0?noteId=note-1' },
				likes: 1, reposts: 2, quotes: 3, replies: 4, hasVideo: false, isSensitive: false,
				images: [{ url: '/api/plugin/fxtwitter-preview/media/1234567890/image/0?noteId=note-1', alt: 'photo' }],
			},
		};
		const result = render(FxPreview, { props: { data, input, context } });
		await waitFor(() => expect(result.getByText('hello')).toBeTruthy());
		expect(result.queryByRole('link', { name: 'Xで開く' })).toBeNull();
		await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
		for (const [path, init] of pluginFetch.mock.calls) {
			expect(String(path)).toMatch(/^\/api\/plugin\/fxtwitter-preview\/media\//);
			expect(init?.signal).toBeInstanceOf(AbortSignal);
		}
		await waitFor(() => expect(result.getAllByRole('img').every(image => image.getAttribute('src')?.startsWith('blob:fx-'))).toBe(true));
		result.unmount();
		expect(URL.revokeObjectURL).toHaveBeenCalledTimes(2);
		expect(fetchMock.mock.calls.every(([, init]) => init?.signal?.aborted)).toBe(true);
	});

	test('rejects media paths bound to another post or source note before making a request', async () => {
		const pluginFetch = vi.fn(async (_path: string, options?: RequestInit) => await fetchMock('/api/plugin/fxtwitter-preview/media/proxy', options));
		initApi(vi.fn() as PluginHost['api'], pluginFetch as PluginHost['fetch']);
		const data = videoData();
		data.post!.hasVideo = false;
		delete data.post!.video;
		data.post!.author.avatarUrl = '/api/plugin/fxtwitter-preview/media/9999999999/avatar/0?noteId=note-1';
		data.post!.images = [
			{ url: '/api/plugin/fxtwitter-preview/media/1234567890/image/0?noteId=another-note' },
			{ url: '/api/plugin/fxtwitter-preview/media/1234567890/image/1?noteId=note-1&noteId=note-1' },
		];
		const result = render(FxPreview, { props: { data, input, context } });
		await waitFor(() => expect(result.getByText('video post')).toBeTruthy());
		await new Promise(resolve => setTimeout(resolve, 0));
		expect(pluginFetch).not.toHaveBeenCalled();
		expect(URL.createObjectURL).not.toHaveBeenCalled();
	});

	test('keeps successful media across temporary inactive and viewport exits', async () => {
		let serverExpires = false;
		const pluginFetch = vi.fn(async (_path: string, options?: RequestInit) => {
			if (serverExpires) return new Response('expired', { status: 404 });
			return await fetchMock('/api/plugin/fxtwitter-preview/media/proxy', options);
		});
		initApi(vi.fn() as PluginHost['api'], pluginFetch as PluginHost['fetch']);
		const data = videoData();
		data.post!.author.avatarUrl = '/api/plugin/fxtwitter-preview/media/1234567890/avatar/0?noteId=note-1';
		const result = render(FxPreview, { props: { data, input, context } });
		await waitFor(() => expect(pluginFetch).toHaveBeenCalledTimes(2));
		await waitFor(() => expect(result.getAllByRole('img').every(image => image.getAttribute('src')?.startsWith('blob:fx-'))).toBe(true));
		serverExpires = true;

		await result.rerender({ data, input, context: { ...context, active: false, inViewport: false } });
		await new Promise(resolve => setTimeout(resolve, 20));
		await result.rerender({ data, input, context: { ...context, inViewport: true } });
		await Promise.resolve();
		expect(pluginFetch).toHaveBeenCalledTimes(2);
		expect(result.container.querySelectorAll('img').length).toBeGreaterThan(0);
		expect(result.container.querySelector('[role="status"]')).toBeNull();
		expect(URL.revokeObjectURL).not.toHaveBeenCalled();
		result.unmount();
		const remounted = render(FxPreview, { props: { data, input, context } });
		await waitFor(() => expect(pluginFetch).toHaveBeenCalledTimes(4));
		await waitFor(() => expect(remounted.getByText('メディアを読み込めませんでした。')).toBeTruthy());
		remounted.unmount();
	});

	test('offers an explicit media retry after a retryable endpoint failure', async () => {
		const data = videoData();
		data.post!.hasVideo = false;
		delete data.post!.video;
		data.post!.images = [{ url: '/api/plugin/fxtwitter-preview/media/1234567890/image/0?noteId=note-1', alt: 'photo' }];
		const previewApi = vi.fn(async (path: string) => {
			if (path.endsWith('/preview')) return data;
			throw new Error(`unexpected API call: ${path}`);
		});
		let mediaRequestCount = 0;
		const pluginFetch = vi.fn(async () => ++mediaRequestCount === 1
			? new Response('expired media', { status: 404 })
			: new Response(new Blob(['image bytes'], { type: 'image/png' }), { headers: new Headers({ 'Content-Type': 'image/png' }) }));
		initApi(previewApi as PluginHost['api'], pluginFetch as PluginHost['fetch']);
		const result = render(FxPreview, { props: { data, input, context } });
		await waitFor(() => expect(result.getByText('メディアを読み込めませんでした。')).toBeTruthy());
		expect(result.getByRole('button', { name: 'メディアを再取得' })).toBeTruthy();
		expect(previewApi).not.toHaveBeenCalled();
		await fireEvent.click(result.getByRole('button', { name: 'メディアを再取得' }));
		await waitFor(() => expect(result.getByRole('img').getAttribute('src')).toMatch(/^blob:fx-/));
		expect(previewApi).toHaveBeenCalledTimes(1);
		expect(previewApi).toHaveBeenCalledWith('plugin/fxtwitter-preview/preview', { noteId: 'note-1', postId: '1234567890' }, expect.anything());
		expect(pluginFetch).toHaveBeenCalledTimes(2);
		expect(result.queryByRole('button', { name: 'メディアを再取得' })).toBeNull();
	});

	test.each([401, 403])('blocks media retry after an unauthorized %i response', async status => {
		const data = videoData();
		data.post!.hasVideo = false;
		delete data.post!.video;
		data.post!.images = [{ url: '/api/plugin/fxtwitter-preview/media/1234567890/image/0?noteId=note-1', alt: 'photo' }];
		const previewApi = vi.fn() as PluginHost['api'];
		const pluginFetch = vi.fn(async () => new Response('not authorized', { status }));
		initApi(previewApi, pluginFetch as PluginHost['fetch']);
		const result = render(FxPreview, { props: { data, input, context } });
		await waitFor(() => expect(result.getByText('メディアを表示する権限を確認できません。')).toBeTruthy());
		expect(result.queryByRole('button', { name: 'メディアを再取得' })).toBeNull();
		expect(previewApi).not.toHaveBeenCalled();
		expect(pluginFetch).toHaveBeenCalledTimes(1);
	});

	test('clears attachment leases when the content warning hides media and when its source changes', async () => {
		const pluginFetch = vi.fn(async (_path: string, options?: RequestInit) => await fetchMock('/api/plugin/fxtwitter-preview/media/proxy', options));
		initApi(vi.fn() as PluginHost['api'], pluginFetch as PluginHost['fetch']);
		const data = videoData();
		data.post!.hasVideo = false;
		delete data.post!.video;
		data.post!.images = [{ url: '/api/plugin/fxtwitter-preview/media/1234567890/image/0?noteId=note-1', alt: 'photo' }];
		const result = render(FxPreview, { props: { data, input, context } });
		await waitFor(() => expect(result.getByRole('img').getAttribute('src')).toMatch(/^blob:fx-/));
		await result.rerender({ data, input, context: { ...context, revealed: false } });
		await waitFor(() => expect(result.container.querySelector('img')).toBeNull());
		await waitFor(() => expect(URL.revokeObjectURL).toHaveBeenCalledTimes(1));
		expect(pluginFetch).toHaveBeenCalledTimes(1);
		await result.rerender({ data, input, context });
		await waitFor(() => expect(pluginFetch).toHaveBeenCalledTimes(2));
		await waitFor(() => expect(result.getByRole('img').getAttribute('src')).toMatch(/^blob:fx-/));
		await result.rerender({ data, input, context: { ...context, source: { kind: 'note', noteId: 'note-2' } } });
		await waitFor(() => expect(result.container.querySelector('img')).toBeNull());
		expect(pluginFetch).toHaveBeenCalledTimes(2);
		expect(URL.revokeObjectURL).toHaveBeenCalledTimes(2);
	});

	test('ignores delayed media failures after CW and source-owner changes', async () => {
		const pendingResponses: Array<(response: Response) => void> = [];
		const pluginFetch = vi.fn(async () => await new Promise<Response>(resolve => pendingResponses.push(resolve)));
		initApi(vi.fn() as PluginHost['api'], pluginFetch as PluginHost['fetch']);
		const data = videoData();
		data.post!.hasVideo = false;
		delete data.post!.video;
		data.post!.images = [{ url: '/api/plugin/fxtwitter-preview/media/1234567890/image/0?noteId=note-1', alt: 'photo' }];
		const result = render(FxPreview, { props: { data, input, context } });
		await waitFor(() => expect(pluginFetch).toHaveBeenCalledTimes(1));
		await result.rerender({ data, input, context: { ...context, revealed: false } });
		await waitFor(() => expect(URL.revokeObjectURL).not.toHaveBeenCalled());
		pendingResponses[0](new Response('expired', { status: 404 }));
		await result.rerender({ data, input, context });
		await waitFor(() => expect(pluginFetch).toHaveBeenCalledTimes(2));
		await result.rerender({ data, input, context: { ...context, source: { kind: 'note', noteId: 'note-2' } } });
		await waitFor(() => expect(result.getByRole('status', { name: 'Xの投稿を取得しています' })).toBeTruthy());
		pendingResponses[1](new Response('denied', { status: 403 }));
		await Promise.resolve();
		await Promise.resolve();
		expect(result.queryByText('メディアを読み込めませんでした。')).toBeNull();
		expect(result.queryByText('メディアを表示する権限を確認できません。')).toBeNull();
		expect(result.getByRole('status', { name: 'Xの投稿を取得しています' })).toBeTruthy();
	});

	test('shares media between references and revokes it after the final release', async () => {
		const pluginFetch = vi.fn(async (_path: string, options?: RequestInit) => await fetchMock('/api/plugin/fxtwitter-preview/media/proxy', options));
		initApi(vi.fn() as PluginHost['api'], pluginFetch as PluginHost['fetch']);
		const data = videoData();
		const first = render(FxPreview, { props: { data, input, context } });
		const second = render(FxPreview, { props: { data, input, context } });
		await waitFor(() => expect(pluginFetch).toHaveBeenCalledTimes(1));
		await waitFor(() => expect(first.container.querySelector('img')?.getAttribute('src')).toMatch(/^blob:fx-/));
		await waitFor(() => expect(second.container.querySelector('img')?.getAttribute('src')).toMatch(/^blob:fx-/));
		first.unmount();
		expect(URL.revokeObjectURL).not.toHaveBeenCalled();
		second.unmount();
		expect(URL.revokeObjectURL).toHaveBeenCalledTimes(1);
	});

	test('shows the original X post time with the standard detailed time component', async () => {
		const data = videoData();
		data.post!.createdAt = '2026-10-02T00:00:00Z';
		const result = render(FxPreview, { props: { data, input, context }, global: { components } });
		const time = await waitFor(() => result.container.querySelector('time'));
		expect(time?.getAttribute('title')).toContain('2026');
		expect(time?.textContent).toContain('2026');
	});

	test('places main and quoted post times after their body and outside headers', async () => {
		initApi(vi.fn() as PluginHost['api'], vi.fn() as PluginHost['fetch']);
		setVideoPlaybackMode('officialEmbed');
		const data = videoData();
		data.post!.createdAt = '2026-10-02T00:00:00Z';
		data.post!.quote = {
			id: '2234567890', url: 'https://x.com/i/status/2234567890', text: 'quoted video', createdAt: '2026-10-03T00:00:00Z',
			author: { name: 'Bob', screenName: 'bob' }, images: [], hasVideo: true,
			video: { provider: 'youtube', providerVideoId: 'dQw4w9WgXcQ' },
		};
		const result = render(FxPreview, { props: { data, input, context: { ...context, detail: false, preferenceValue: 'text' } }, global: { components } });
		const times = await waitFor(() => {
			const found = result.container.querySelectorAll('time');
			if (found.length !== 2) throw new Error(`expected two post times, found ${found.length}`);
			return Array.from(found);
		});
		const mainBody = result.container.querySelector('article > p[class*="text"]')!;
		const quoteBody = result.container.querySelector('[class*="quoteText"]')!;
		expect(times[0].closest('header')).toBeNull();
		expect(times[1].closest('header')).toBeNull();
		expect((mainBody.compareDocumentPosition(times[0]) & Node.DOCUMENT_POSITION_FOLLOWING) !== 0).toBe(true);
		expect((quoteBody.compareDocumentPosition(times[1]) & Node.DOCUMENT_POSITION_FOLLOWING) !== 0).toBe(true);
		result.unmount();

		const textOnly = videoData();
		textOnly.post!.createdAt = '2026-10-02T00:00:00Z';
		textOnly.post!.hasVideo = false;
		delete textOnly.post!.video;
		textOnly.post!.images = [];
		const bodyOnly = render(FxPreview, { props: { data: textOnly, input, context }, global: { components } });
		const bodyTime = await waitFor(() => bodyOnly.container.querySelector('time'));
		const body = bodyOnly.container.querySelector('[class*="text"]')!;
		expect(bodyTime?.closest('header')).toBeNull();
		expect((body.compareDocumentPosition(bodyTime!) & Node.DOCUMENT_POSITION_FOLLOWING) !== 0).toBe(true);
	});

	test('text-only timeline mode suppresses attachment media requests', async () => {
		const pluginFetch = vi.fn(async (_path: string, options?: RequestInit) => await fetchMock('/api/plugin/fxtwitter-preview/media/proxy', options));
		initApi(vi.fn() as PluginHost['api'], pluginFetch as PluginHost['fetch']);
		const data = videoData();
		data.post!.author.avatarUrl = '/api/plugin/fxtwitter-preview/media/1234567890/avatar/0?noteId=note-1';
		data.post!.images = [{ url: '/api/plugin/fxtwitter-preview/media/1234567890/image/0?noteId=note-1', alt: 'photo' }];
		data.post!.video!.posterUrl = '/api/plugin/fxtwitter-preview/media/1234567890/poster/0?noteId=note-1';
		const result = render(FxPreview, { props: { data, input, context: { ...context, detail: false, preferenceValue: 'text' } } });
		await waitFor(() => expect(pluginFetch).toHaveBeenCalledTimes(1));
		expect(pluginFetch.mock.calls[0][0]).toContain('/avatar/0');
		expect(pluginFetch.mock.calls.some(([path]) => String(path).includes('/image/') || String(path).includes('/poster/'))).toBe(false);
		expect(result.getByText('画像1件・動画1件')).toBeTruthy();
	});

	test('text-only mode reports main and quoted media counts separately without fetching attachments', async () => {
		const pluginFetch = vi.fn(async (_path: string, options?: RequestInit) => await fetchMock('/api/plugin/fxtwitter-preview/media/proxy', options));
		initApi(vi.fn() as PluginHost['api'], pluginFetch as PluginHost['fetch']);
		const data = videoData();
		data.post!.images = Array.from({ length: 3 }, (_, index) => ({ url: `/api/plugin/fxtwitter-preview/media/1234567890/image/${index}?noteId=note-1` }));
		data.post!.quote = {
			id: '2234567890', url: 'https://x.com/i/status/2234567890', text: 'quote',
			author: { name: 'Bob', screenName: 'bob' },
			images: [{ url: '/api/plugin/fxtwitter-preview/media/1234567890/quote-image/0?noteId=note-1' }], hasVideo: true,
			video: { provider: 'x', posterUrl: '/api/plugin/fxtwitter-preview/media/1234567890/quote-poster/0?noteId=note-1' },
		};
		const result = render(FxPreview, { props: { data, input, context: { ...context, detail: false, preferenceValue: 'text' } } });
		expect(result.getByText('画像3件・動画1件')).toBeTruthy();
		expect(result.getByText('引用: 画像1件・動画1件')).toBeTruthy();
		await Promise.resolve();
		expect(pluginFetch.mock.calls.some(([path]) => /image|poster/.test(String(path)))).toBe(false);
	});

	test('media mode renders the main avatar even when attachment arrays are empty', async () => {
		const pluginFetch = vi.fn(async (_path: string, options?: RequestInit) => await fetchMock('/api/plugin/fxtwitter-preview/media/proxy', options));
		initApi(vi.fn() as PluginHost['api'], pluginFetch as PluginHost['fetch']);
		const data = videoData();
		data.post!.hasVideo = false;
		delete data.post!.video;
		data.post!.images = [];
		data.post!.author.avatarUrl = '/api/plugin/fxtwitter-preview/media/1234567890/avatar/0?noteId=note-1';
		const result = render(FxPreview, { props: { data, input, context: { ...context, detail: false, preferenceValue: 'media' } } });
		await waitFor(() => expect(result.getByRole('img').getAttribute('src')).toMatch(/^blob:fx-/));
		expect(pluginFetch).toHaveBeenCalledTimes(1);
		expect(String(pluginFetch.mock.calls[0][0])).toContain('/avatar/0');
	});

	test('loads author avatars independently from attachment media in every post shape', async () => {
		const pluginFetch = vi.fn(async (_path: string, options?: RequestInit) => await fetchMock('/api/plugin/fxtwitter-preview/media/proxy', options));
		initApi(vi.fn() as PluginHost['api'], pluginFetch as PluginHost['fetch']);
		const cases: Array<{ name: string; data: PreviewResponse; context: PreviewContext; expected: string[]; forbidden?: string[] }> = [];
		for (const name of ['plain', 'image', 'video', 'quote', 'text-mode']) {
			const data = videoData();
			const noteId = `note-${name}`;
			data.post!.author.avatarUrl = `/api/plugin/fxtwitter-preview/media/${data.post!.id}/avatar/0?noteId=${noteId}`;
			if (name === 'plain') {
				data.post!.hasVideo = false;
				delete data.post!.video;
			} else if (name === 'image') {
				data.post!.hasVideo = false;
				delete data.post!.video;
				data.post!.images = [{ url: `/api/plugin/fxtwitter-preview/media/${data.post!.id}/image/0?noteId=${noteId}` }];
			} else if (name === 'quote') {
				data.post!.hasVideo = false;
				delete data.post!.video;
				data.post!.quote = {
					id: '2234567890', url: 'https://x.com/i/status/2234567890', text: 'quote',
					author: { name: 'Bob', screenName: 'bob', avatarUrl: `/api/plugin/fxtwitter-preview/media/${data.post!.id}/quote-avatar/0?noteId=${noteId}` },
					images: [], hasVideo: false,
				};
			} else if (name === 'video') {
				data.post!.video!.posterUrl = `/api/plugin/fxtwitter-preview/media/${data.post!.id}/poster/0?noteId=${noteId}`;
			}
			const textMode = name === 'text-mode';
			cases.push({
				name,
				data,
				context: { ...context, source: { kind: 'note', noteId }, ...(textMode ? { detail: false, preferenceValue: 'text' } : {}) },
				expected: name === 'plain' ? ['/avatar/'] : name === 'image' ? ['/avatar/', '/image/'] : name === 'quote' ? ['/avatar/', '/quote-avatar/'] : textMode ? ['/avatar/'] : ['/avatar/', '/poster/'],
				forbidden: textMode ? ['/image/', '/poster/'] : undefined,
			});
		}
		for (const item of cases) {
			pluginFetch.mockClear();
			const result = render(FxPreview, { props: { data: item.data, input, context: item.context } });
			await waitFor(() => expect(pluginFetch, item.name).toHaveBeenCalledTimes(item.expected.length));
			const paths = pluginFetch.mock.calls.map(([path]) => String(path));
			for (const fragment of item.expected) expect(paths.some(path => path.includes(fragment)), item.name).toBe(true);
			for (const fragment of item.forbidden ?? []) expect(paths.some(path => path.includes(fragment)), item.name).toBe(false);
			result.unmount();
		}
	});

	test('lays out zero through four images and keeps three-image order for the lightbox', async () => {
		const pluginFetch = vi.fn(async (_path: string, options?: RequestInit) => await fetchMock('/api/plugin/fxtwitter-preview/media/proxy', options));
		const mediaViewer = vi.fn(async () => undefined);
		initApi(vi.fn() as PluginHost['api'], pluginFetch as PluginHost['fetch'], mediaViewer as PluginHost['openMedia']);
		for (let count = 0; count <= 4; count++) {
			const data = videoData();
			data.post!.hasVideo = false;
			delete data.post!.video;
			data.post!.images = Array.from({ length: count }, (_, index) => ({
				url: `/api/plugin/fxtwitter-preview/media/${data.post!.id}/image/${index}?noteId=note-images-${count}`,
				alt: `画像 ${index + 1}`,
			}));
			const itemContext = { ...context, source: { kind: 'note' as const, noteId: `note-images-${count}` } };
			const result = render(FxPreview, { props: { data, input, context: itemContext } });
			if (count > 0) await waitFor(() => expect(result.getAllByRole('button', { name: /画像/ })).toHaveLength(count));
			else expect(result.queryAllByRole('button', { name: /画像/ })).toHaveLength(0);
			const buttons = result.queryAllByRole('button', { name: /画像/ });
			if (count === 3) {
				expect(buttons[0].parentElement?.className).toContain('threeImages');
				expect(buttons.map(button => button.getAttribute('aria-label'))).toEqual(['画像 1', '画像 2', '画像 3']);
				await fireEvent.click(buttons[0]);
				expect(mediaViewer).toHaveBeenLastCalledWith(
					expect.arrayContaining([
						expect.objectContaining({ id: '1234567890:0' }),
						expect.objectContaining({ id: '1234567890:1' }),
						expect.objectContaining({ id: '1234567890:2' }),
					]),
					'1234567890:0',
				);
			}
			if (count >= 2) {
				expect(buttons[0].parentElement?.className).not.toContain('singleImage');
				expect(buttons[0].parentElement?.getAttribute('style') ?? '').not.toContain('--fx-single-aspect');
			}
			if (count === 2) expect(buttons[0].parentElement?.className).toContain('twoImages');
			if (count === 4) expect(buttons[0].parentElement?.className).toContain('fourImages');
			result.unmount();
		}
	});

	test('renders four 16:9 image buttons and opens authorized blobs in the host lightbox', async () => {
		const pluginFetch = vi.fn(async (_path: string, options?: RequestInit) => await fetchMock('/api/plugin/fxtwitter-preview/media/proxy', options));
		const openMedia = vi.fn<PluginHost['openMedia']>(async () => undefined);
		initApi(vi.fn() as PluginHost['api'], pluginFetch as PluginHost['fetch'], openMedia as PluginHost['openMedia']);
		const data = videoData();
		data.post!.hasVideo = false;
		delete data.post!.video;
		data.post!.images = Array.from({ length: 4 }, (_, index) => ({
			url: `/api/plugin/fxtwitter-preview/media/1234567890/image/${index}?noteId=note-1`,
			alt: `photo ${index + 1}`,
		}));
		const result = render(FxPreview, { props: { data, input, context: { ...context, detail: false, preferenceValue: 'media' } } });
		await waitFor(() => expect(result.getAllByRole('button', { name: /photo/ })).toHaveLength(4));
		await fireEvent.click(result.getByRole('button', { name: 'photo 3' }));
		expect(openMedia).toHaveBeenCalledTimes(1);
		expect(openMedia.mock.calls[0][0]).toHaveLength(4);
		expect(openMedia.mock.calls[0][0].every(item => item.blob instanceof Blob && item.type === 'image')).toBe(true);
		expect(openMedia.mock.calls[0][1]).toBe('1234567890:2');
	});

	test('native proxy renders an independent button for each main and quoted video slot', async () => {
		mockMediaFetch();
		initApi(vi.fn() as PluginHost['api'], (async (_path, options) => await fetchMock('/poster', options)) as PluginHost['fetch']);
		setVideoPlaybackMode('nativeProxy');
		const data = videoData();
		data.post!.videos = [0, 1].map(index => ({ provider: 'x', playbackAvailable: true, posterUrl: `/api/plugin/fxtwitter-preview/media/1234567890/poster/${index}?noteId=note-1` }));
		data.post!.quote = {
			id: '2234567890', url: 'https://x.com/i/status/2234567890', text: 'quoted videos',
			author: { name: 'Bob', screenName: 'bob' }, images: [], hasVideo: true,
			video: { provider: 'x', embedToken: 'abcdef0123456789abcdef0123456789', playbackAvailable: true },
			videos: [0, 1].map(index => ({ provider: 'x', playbackAvailable: true, posterUrl: `/api/plugin/fxtwitter-preview/media/1234567890/quote-poster/${index}?noteId=note-1` })),
		};
		const result = render(FxPreview, { props: { data, input, context: { ...context, preferenceValue: 'media' } } });
		await waitFor(() => expect(result.container.querySelectorAll('[data-fx-video-target="main"]')).toHaveLength(2));
		expect(result.container.querySelectorAll('[data-fx-video-target="quote"]')).toHaveLength(2);
		expect(result.container.querySelectorAll('[data-fx-video-play]')).toHaveLength(4);
		expect(result.container.querySelector('[data-fx-video-grid="main"]')?.getAttribute('data-video-count')).toBe('2');
		expect(result.container.querySelector('[data-fx-video-grid="quote"]')?.getAttribute('data-video-count')).toBe('2');
		expect(result.container.querySelector('[data-fx-video-target="main"][data-video-index="1"] [data-video-poster-count]')).toBeTruthy();
	});

	test('keeps one native video in a single-column media layout', async () => {
		initApi(vi.fn() as PluginHost['api'], vi.fn() as PluginHost['fetch']);
		setVideoPlaybackMode('nativeProxy');
		const result = render(FxPreview, { props: { data: videoData(), input, context: { ...context, preferenceValue: 'media' } } });
		const grid = await waitFor(() => result.container.querySelector('[data-fx-video-grid="main"]'));
		expect(grid?.getAttribute('data-video-count')).toBe('1');
		expect(grid?.querySelectorAll('[data-fx-video-target]')).toHaveLength(1);
	});

	test('timeline more carries a one-shot request that expands only the matching note detail post', async () => {
		const data = videoData();
		data.post!.text = 'あ'.repeat(140);
		const timeline = render(FxPreview, { props: { data, input, context: { ...context, detail: false } } });
		const moreHref = timeline.getByRole('link', { name: 'もっと見る' }).getAttribute('href');
		expect(moreHref).toBe('/notes/note-1?fxPreviewExpand=note-1%3A1234567890');
		timeline.unmount();

		window.history.replaceState({ preserved: true }, '', `${moreHref}&keep=1#section`);
		const detail = render(FxPreview, { props: { data, input, context } });
		expect(detail.container.textContent).toContain('あ'.repeat(140));
		expect(detail.queryByRole('button', { name: 'もっと見る' })).toBeNull();
		await detail.rerender({ data, input, context: { ...context, source: { kind: 'note', noteId: 'note-1' } } });
		expect(detail.container.textContent).toContain('あ'.repeat(140));
		expect(window.location.pathname).toBe('/notes/note-1');
		expect(window.location.search).toBe('?keep=1');
		expect(window.location.hash).toBe('#section');
		expect(window.history.state).toEqual({ preserved: true });
	});

	test('does not auto-expand direct details, a different post/path, or an unrevealed note', async () => {
		const data = videoData();
		data.post!.text = 'あ'.repeat(140);
		const marker = 'fxPreviewExpand=note-1%3A1234567890';

		const direct = render(FxPreview, { props: { data, input, context } });
		expect(direct.container.textContent).not.toContain('あ'.repeat(140));
		direct.unmount();

		window.history.replaceState(null, '', `/notes/note-1?fxPreviewExpand=note-1%3A2234567890`);
		const otherPost = render(FxPreview, { props: { data, input, context } });
		expect(otherPost.container.textContent).not.toContain('あ'.repeat(140));
		expect(window.location.search).toContain('2234567890');
		otherPost.unmount();

		window.history.replaceState(null, '', `/notes/another-note?${marker}`);
		const otherPath = render(FxPreview, { props: { data, input, context } });
		expect(otherPath.container.textContent).not.toContain('あ'.repeat(140));
		expect(window.location.pathname).toBe('/notes/another-note');
		otherPath.unmount();

		window.history.replaceState(null, '', `/notes/note-1?${marker}`);
		const hiddenByCW = render(FxPreview, { props: { data, input, context: { ...context, revealed: false } } });
		expect(hiddenByCW.container.textContent).not.toContain('あ'.repeat(140));
		expect(window.location.search).toContain('fxPreviewExpand');
	});

	test('waits for delayed matching data and collapses when the component changes posts', async () => {
		const markerURL = '/notes/note-1?fxPreviewExpand=note-1%3A1234567890';
		window.history.replaceState(null, '', markerURL);
		const delayed = render(FxPreview, { props: { data: { state: 'pending' }, input, context } });
		const data = videoData();
		data.post!.text = 'あ'.repeat(140);
		await delayed.rerender({ data, input, context });
		expect(delayed.container.textContent).toContain('あ'.repeat(140));
		expect(window.location.search).toBe('');

		const nextPost = videoData();
		nextPost.post!.id = '2234567890';
		nextPost.post!.text = 'い'.repeat(140);
		await delayed.rerender({ data: nextPost, input, context });
		expect(delayed.container.textContent).not.toContain('い'.repeat(140));
		expect(delayed.getByRole('button', { name: 'もっと見る' })).toBeTruthy();
	});

	test('renders validated expanded URLs and Unicode hashtags without bubbling to the parent note', async () => {
		const data = videoData();
		data.post!.hasVideo = false;
		delete data.post!.video;
		data.post!.text = 'See https://t.co/a #日本語';
		data.post!.textEntities = [
			{ kind: 'url', start: 4, end: 18, href: 'https://example.com/article', display: 'example.com/article' },
			{ kind: 'hashtag', start: 19, end: 23, tag: '日本語', display: '#日本語' },
		];
		data.post!.quote = {
			id: '2234567890', url: 'https://x.com/i/status/2234567890', text: '#引用',
			author: { name: 'Bob', screenName: 'bob' }, images: [], hasVideo: false,
			textEntities: [{ kind: 'hashtag', start: 0, end: 3, tag: '引用', display: '#引用' }],
		};
		const result = render(FxPreview, { props: { data, input, context } });
		const outerClick = vi.fn();
		result.container.addEventListener('click', outerClick);
		const expanded = await waitFor(() => result.getByRole('link', { name: 'example.com/article' }));
		expect(expanded.getAttribute('href')).toBe('https://example.com/article');
		expect(expanded.className).toContain('_link');
		expect(expanded.querySelector('.ti-external-link')).toBeTruthy();
		expect(result.getByRole('link', { name: '#日本語' }).getAttribute('href')).toBe('/tags/%E6%97%A5%E6%9C%AC%E8%AA%9E');
		expect(result.getByRole('link', { name: '#引用' }).getAttribute('href')).toBe('/tags/%E5%BC%95%E7%94%A8');
		expanded.addEventListener('click', event => event.preventDefault(), { once: true });
		await fireEvent.click(expanded);
		expect(outerClick).not.toHaveBeenCalled();
	});

	test('links entity mentions and author identity to validated X profiles without treating email as a mention', async () => {
		const data = videoData();
		data.post!.hasVideo = false;
		delete data.post!.video;
		data.post!.author.profileUrl = 'https://x.com/alice';
		data.post!.author.avatarUrl = '/api/plugin/fxtwitter-preview/media/1234567890/avatar/0?noteId=note-1';
		data.post!.text = 'Hi @bob and mail@example.com';
		data.post!.textEntities = [{ kind: 'mention', start: 3, end: 7, href: 'https://x.com/bob', display: '@bob' }];
		const pluginFetch = vi.fn(async (path: string, options?: RequestInit) => await fetchMock(path, options));
		initApi(vi.fn() as PluginHost['api'], pluginFetch as PluginHost['fetch']);
		const result = render(FxPreview, { props: { data, input, context } });
		const outerClick = vi.fn();
		result.container.addEventListener('click', outerClick);
		await waitFor(() => expect(result.getByRole('img').getAttribute('src')).toMatch(/^blob:fx-/));
		const profileLinks = result.getAllByRole('link', { name: /Alice|@alice|Xプロフィール/ });
		expect(profileLinks.some(link => link.getAttribute('href') === 'https://x.com/alice')).toBe(true);
		const mention = result.getByRole('link', { name: '@bob' });
		expect(mention.getAttribute('href')).toBe('https://x.com/bob');
		expect(result.queryByRole('link', { name: 'example.com' })).toBeNull();
		mention.addEventListener('click', event => event.preventDefault(), { once: true });
		await fireEvent.click(mention);
		expect(outerClick).not.toHaveBeenCalled();
	});

	test('uses source dimensions only for a centered single-image frame capped at square', async () => {
		const pluginFetch = vi.fn(async (_path: string, options?: RequestInit) => await fetchMock('/api/plugin/fxtwitter-preview/media/proxy', options));
		initApi(vi.fn() as PluginHost['api'], pluginFetch as PluginHost['fetch']);
		for (const item of [
			{ width: 300, height: 1200, expected: '0.25' },
			{ width: 1200, height: 600, expected: '2' },
			{ width: undefined, height: undefined, expected: '1.777' },
		]) {
			const data = videoData();
			data.post!.hasVideo = false;
			delete data.post!.video;
			data.post!.images = [{ url: `/api/plugin/fxtwitter-preview/media/1234567890/image/0?noteId=note-${item.expected}`, width: item.width, height: item.height, alt: 'single' }];
			const result = render(FxPreview, { props: { data, input, context: { ...context, source: { kind: 'note', noteId: `note-${item.expected}` } } } });
			const grid = await waitFor(() => result.getByRole('button', { name: 'single' }).parentElement!);
			expect(grid.className).toContain('singleImage');
			expect(grid.getAttribute('style')).toContain(item.expected);
			result.unmount();
		}
		const multiple = videoData();
		multiple.post!.hasVideo = false;
		delete multiple.post!.video;
		multiple.post!.images = [0, 1].map(index => ({ url: `/api/plugin/fxtwitter-preview/media/1234567890/image/${index}?noteId=note-multiple`, width: 300, height: 1200 }));
		const result = render(FxPreview, { props: { data: multiple, input, context: { ...context, source: { kind: 'note', noteId: 'note-multiple' } } } });
		await waitFor(() => expect(result.getAllByRole('button', { name: /画像/ })).toHaveLength(2));
		expect(result.getAllByRole('button', { name: /画像/ })[0].parentElement?.className).not.toContain('singleImage');
	});

	test('uses the shared capped image grid and lightbox for quoted media', async () => {
		const openMedia = vi.fn<PluginHost['openMedia']>(async () => undefined);
		initApi(vi.fn() as PluginHost['api'], (async (_path, options) => await fetchMock('/quote-image', options)) as PluginHost['fetch'], openMedia);
		const data = videoData();
		data.post!.hasVideo = false;
		delete data.post!.video;
		data.post!.quote = {
			id: '2234567890', url: 'https://x.com/i/status/2234567890', text: 'quoted post',
			author: { name: 'Bob', screenName: 'bob' }, hasVideo: false,
			images: [{ url: '/api/plugin/fxtwitter-preview/media/1234567890/quote-image/0?noteId=note-1', width: 300, height: 1200, alt: 'quoted single' }],
		};
		const result = render(FxPreview, { props: { data, input, context } });
		const button = await waitFor(() => result.getByRole('button', { name: 'quoted single' }));
		const grid = button.closest('[data-fx-media-grid]');
		expect(grid?.className).toContain('singleImage');
		expect(grid?.getAttribute('style')).toContain('0.25');
		await fireEvent.click(button);
		expect(openMedia).toHaveBeenCalledWith([
			expect.objectContaining({ id: '2234567890:0', type: 'image', isSensitive: false, initiallyRevealed: true }),
		], '2234567890:0');
	});

	test('obeys respect, ignore, and force for plugin image sensitivity', async () => {
		initApi(vi.fn() as PluginHost['api'], (async (_path, options) => await fetchMock('/sensitive-image', options)) as PluginHost['fetch']);
		const data = videoData();
		data.post!.hasVideo = false;
		delete data.post!.video;
		data.post!.images = [{ url: '/api/plugin/fxtwitter-preview/media/1234567890/image/0?noteId=note-1', alt: 'sensitive photo' }];
		data.post!.isSensitive = true;

		const respected = render(FxPreview, { props: { data, input, context } });
		await waitFor(() => expect(respected.container.querySelector('[data-sensitive-media-hidden="true"]')).toBeTruthy());
		await fireEvent.click(respected.container.querySelector('[data-sensitive-media-reveal]')!);
		expect(respected.container.querySelector('[data-sensitive-media-hidden="false"]')).toBeTruthy();
		respected.unmount();

		setPreference('nsfw', 'ignore');
		const ignored = render(FxPreview, { props: { data, input, context } });
		await waitFor(() => expect(ignored.container.querySelector('[data-sensitive-media-hidden="false"]')).toBeTruthy());
		ignored.unmount();

		setPreference('nsfw', 'force');
		data.post!.isSensitive = undefined;
		const forced = render(FxPreview, { props: { data, input, context } });
		await waitFor(() => expect(forced.container.querySelector('[data-sensitive-media-hidden="true"][data-sensitive-media-kind="image"]')).toBeTruthy());
	});

	test('requires separate reveal and play actions for a sensitive video', async () => {
		const fetcher = vi.fn(async (_path: string, options?: RequestInit) => {
			if (options?.method !== 'POST') return new Response(new Blob(['poster'], { type: 'image/png' }), { headers: new Headers({ 'Content-Type': 'image/png' }) });
			const body = JSON.parse(String(options?.body)) as { nonce: string };
			return new Response(JSON.stringify({ url: officialXEmbedURL('1234567890', body.nonce) }), { status: 200, headers: { 'Content-Type': 'application/json' } });
		});
		initApi(vi.fn() as PluginHost['api'], fetcher as PluginHost['fetch']);
		const data = videoData();
		data.post!.isSensitive = true;
		const result = render(FxPreview, { props: { data, input, context } });
		await waitFor(() => expect(result.container.querySelector('[data-sensitive-media-hidden="true"]')).toBeTruthy());
		expect(result.container.querySelector('iframe')).toBeNull();
		expect(fetcher.mock.calls.some(([, options]) => options?.method === 'POST')).toBe(false);
		await fireEvent.click(result.container.querySelector('[data-sensitive-media-reveal]')!);
		expect(fetcher.mock.calls.some(([, options]) => options?.method === 'POST')).toBe(false);
		await fireEvent.click(result.container.querySelector('[data-fx-video-play]')!);
		await waitFor(() => expect(result.baseElement.querySelector('[data-fx-video-modal] iframe')).toBeTruthy());
		expect(fetcher.mock.calls.filter(([, options]) => options?.method === 'POST')).toHaveLength(1);
	});

	test('uses the native reply icon and combines disjoint repost and quote counts once', () => {
		const data = videoData();
		Object.assign(data.post!, { replies: 1234, reposts: 2345, quotes: 3456, likes: 4567 });
		const result = render(FxPreview, { props: { data, input, context } });
		for (const value of ['1,234', '5,801', '4,567']) expect(result.getByText(value)).toBeTruthy();
		expect(result.queryByText('2,345')).toBeNull();
		expect(result.queryByText('3,456')).toBeNull();
		expect(result.container.querySelector('.ti-arrow-back-up')).toBeTruthy();
		expect(result.container.querySelector('.ti-quote')).toBeNull();
	});

	test('mounts one official X iframe only after an active detail click', async () => {
		const call = vi.fn() as PluginHost['api'];
		const fetcher = vi.fn(async (_path: string, options?: RequestInit) => {
			if (options?.method !== 'POST') return new Response(new Blob(['poster'], { type: 'image/png' }), { headers: new Headers({ 'Content-Type': 'image/png' }) });
			const body = JSON.parse(String(options?.body)) as { nonce: string };
			return new Response(JSON.stringify({ url: officialXEmbedURL('1234567890', body.nonce) }), { status: 200, headers: { 'Content-Type': 'application/json' } });
		});
		initApi(call, fetcher as PluginHost['fetch']);
		const result = render(FxPreview, { props: { data: videoData(), input, context } });

		expect(result.queryByTitle('Xの投稿動画')).toBeNull();
		await waitFor(() => expect(result.getByRole('img').getAttribute('src')).toMatch(/^blob:fx-/));
		expect(result.queryByTitle('Xの投稿動画')).toBeNull();
		expect(fetcher.mock.calls.some(([, options]) => options?.method === 'POST')).toBe(false);

		await fireEvent.click(await waitFor(() => result.getByRole('button', { name: '動画を再生' })));
		await waitFor(() => expect(result.getByTitle('Xの投稿動画')).toBeTruthy());
		const frame = result.getByTitle('Xの投稿動画');
		expect(frame.closest('[data-fx-video-modal]')).toBeTruthy();
		expect((frame as HTMLIFrameElement).style.maxWidth).toBe('560px');
		expect(frame.getAttribute('scrolling')).toBe('no');
		expect(frame.getAttribute('sandbox')).toBe('allow-popups allow-popups-to-escape-sandbox allow-scripts allow-same-origin');
		expect(frame.getAttribute('allow')).toBe('fullscreen; web-share');
		expect(frame.getAttribute('allow')).not.toContain('autoplay');
		expect(frame.getAttribute('src')).toMatch(/^https:\/\/platform\.twitter\.com\/embed\/Tweet\.html\?/);
		expect(result.getByRole('button', { name: '動画を再生中' }).hasAttribute('disabled')).toBe(true);
		expect(result.container.querySelector('[data-fx-video-target="main"] [data-video-poster-count]')).toBeTruthy();
		expect(fetcher).toHaveBeenCalledWith('/api/plugin/fxtwitter-preview/embed/shell', expect.objectContaining({
			body: expect.stringContaining('"token":"0123456789abcdef0123456789abcdef"'),
		}));
		const embedRequest = fetcher.mock.calls.find(([, options]) => options?.method === 'POST');
		expect(JSON.parse(String(embedRequest?.[1]?.body))).toMatchObject({ noteId: 'note-1', postId: '1234567890', target: 'main' });
	});

	test('mounts a fixed click-only YouTube embed for a main external video', async () => {
		const data = videoData();
		Object.assign(data.post!.video!, { provider: 'youtube', providerVideoId: 'M7lc1UVf-VE' });
		const call = vi.fn() as PluginHost['api'];
		const fetcher = vi.fn(async (_path: string, options?: RequestInit) => options?.method !== 'POST'
			? new Response(new Blob(['poster'], { type: 'image/png' }), { headers: new Headers({ 'Content-Type': 'image/png' }) })
			: new Response(JSON.stringify({ url: 'https://www.youtube.com/embed/M7lc1UVf-VE?autoplay=0&playsinline=1' }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
		initApi(call, fetcher as PluginHost['fetch']);
		const result = render(FxPreview, { props: { data, input, context } });

		await waitFor(() => expect(result.container.querySelector('[data-fx-video-target="main"]')).toBeTruthy());
		expect(result.queryByTitle('YouTube video player')).toBeNull();
		expect(fetcher.mock.calls.some(([, options]) => options?.method === 'POST')).toBe(false);
		await fireEvent.click(result.container.querySelector('[data-fx-video-target="main"] [data-fx-video-play]')!);
		const frame = await waitFor(() => result.getByTitle('YouTube video player'));
		expect(frame.closest('[data-fx-video-modal]')).toBeTruthy();
		expect(frame.getAttribute('src')).toBe('https://www.youtube.com/embed/M7lc1UVf-VE?autoplay=0&playsinline=1');
		expect(frame.getAttribute('referrerpolicy')).toBe('strict-origin-when-cross-origin');
		expect(frame.getAttribute('allow')).not.toContain('autoplay');
		expect(result.getByRole('link', { name: 'YouTubeで開く' }).getAttribute('href')).toBe('https://www.youtube.com/watch?v=M7lc1UVf-VE');
		const embedRequest = fetcher.mock.calls.find(([, options]) => options?.method === 'POST');
		expect(JSON.parse(String(embedRequest?.[1]?.body))).toMatchObject({ target: 'main', postId: '1234567890' });
	});

	test('plays a quoted YouTube video with the quote target and outer cache post id', async () => {
		const data = videoData();
		data.post!.hasVideo = false;
		delete data.post!.video;
		data.post!.quote = {
			id: '2234567890', url: 'https://x.com/i/status/2234567890', text: 'quoted video',
			author: { name: 'Bob', screenName: 'bob' }, images: [], hasVideo: true,
			video: {
				provider: 'youtube', providerVideoId: 'dQw4w9WgXcQ',
				posterUrl: '/api/plugin/fxtwitter-preview/media/1234567890/quote-poster/0?noteId=note-1',
				embedToken: 'quote-token',
			},
		};
		const call = vi.fn() as PluginHost['api'];
		const fetcher = vi.fn(async (_path: string, options?: RequestInit) => options?.method !== 'POST'
			? new Response(new Blob(['poster'], { type: 'image/png' }), { headers: new Headers({ 'Content-Type': 'image/png' }) })
			: new Response(JSON.stringify({ url: 'https://www.youtube.com/embed/dQw4w9WgXcQ?autoplay=0&playsinline=1' }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
		initApi(call, fetcher as PluginHost['fetch']);
		const result = render(FxPreview, { props: { data, input, context } });
		await waitFor(() => expect(result.container.querySelector('[data-fx-video-target="quote"] [data-fx-video-play]')).toBeTruthy());
		expect(fetcher.mock.calls.some(([, options]) => options?.method === 'POST')).toBe(false);
		await fireEvent.click(result.container.querySelector('[data-fx-video-target="quote"] [data-fx-video-play]')!);
		await waitFor(() => expect(result.getByTitle('YouTube video player')).toBeTruthy());
		const embedRequest = fetcher.mock.calls.find(([, options]) => options?.method === 'POST');
		expect(JSON.parse(String(embedRequest?.[1]?.body))).toMatchObject({
			noteId: 'note-1', postId: '1234567890', token: 'quote-token', target: 'quote',
		});
	});

	test('keeps unknown external videos as poster-only fallback', async () => {
		const data = videoData();
		data.post!.video = { provider: 'external', posterUrl: '/api/plugin/fxtwitter-preview/media/1234567890/poster/0?noteId=note-1' };
		const call = vi.fn();
		initApi(call as PluginHost['api']);
		const result = render(FxPreview, { props: { data, input, context } });
		await waitFor(() => expect(result.getByRole('img')).toBeTruthy());
		expect(result.container.querySelector('[data-fx-video-play]')).toBeNull();
		expect(result.container.querySelector('iframe')).toBeNull();
		expect(call).not.toHaveBeenCalled();
	});

	test('keeps one centered playback action when the poster cannot be loaded', async () => {
		fetchMock.mockReset();
		fetchMock.mockResponse('', { status: 404, headers: { 'Content-Type': 'text/plain' } });
		initApi(vi.fn() as PluginHost['api'], (async (_path, options) => await fetchMock('/poster', options)) as PluginHost['fetch']);
		const result = render(FxPreview, { props: { data: videoData(), input, context } });
		await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
		expect(result.queryByRole('img')).toBeNull();
		expect(result.getAllByRole('button', { name: '動画を再生' })).toHaveLength(1);
	});

	test('renders a quoted post returned by Fx without activating another external fetch', async () => {
		const data = videoData();
		const post = data.post!;
		post.hasVideo = false;
		delete post.video;
		post.quote = {
			id: '2234567890', url: 'https://x.com/i/status/2234567890', text: 'quoted post',
			author: { name: 'Bob', screenName: 'bob' }, images: [], hasVideo: false,
		};
		const result = render(FxPreview, { props: { data, input, context } });
		await waitFor(() => expect(result.getByText('quoted post')).toBeTruthy());
		expect(result.getByText('@bob')).toBeTruthy();
		expect(fetchMock).not.toHaveBeenCalled();
	});

	test('removes the embed immediately when the note is no longer active', async () => {
		const fetcher = vi.fn(async (_path: string, options?: RequestInit) => {
			const body = JSON.parse(String(options?.body)) as { nonce: string };
			return new Response(JSON.stringify({ url: officialXEmbedURL('1234567890', body.nonce) }), { status: 200, headers: { 'Content-Type': 'application/json' } });
		});
		initApi(vi.fn() as PluginHost['api'], fetcher as PluginHost['fetch']);
		const result = render(FxPreview, { props: { data: videoData(), input, context } });
		await fireEvent.click(await waitFor(() => result.getByRole('button', { name: '動画を再生' })));
		await waitFor(() => expect(result.getByTitle('Xの投稿動画')).toBeTruthy());

		await result.rerender({ data: videoData(), input, context: { ...context, active: false } });
		await waitFor(() => expect(result.queryByTitle('Xの投稿動画')).toBeNull());
		await waitFor(() => expect(result.queryByRole('img')).toBeNull());
		expect(result.queryByRole('button')).toBeNull();
	});

	test('does not mount a late embed response after deactivation', async () => {
		let resolve!: (value: Response) => void;
		let nonce = '';
		const fetcher = vi.fn((_path: string, options?: RequestInit) => new Promise<Response>(value => {
			nonce = (JSON.parse(String(options?.body)) as { nonce: string }).nonce;
			resolve = value;
		}));
		initApi(vi.fn() as PluginHost['api'], fetcher as PluginHost['fetch']);
		const result = render(FxPreview, { props: { data: videoData(), input, context } });
		await fireEvent.click(await waitFor(() => result.getByRole('button', { name: '動画を再生' })));
		await result.rerender({ data: videoData(), input, context: { ...context, active: false } });
		resolve(new Response(JSON.stringify({ url: officialXEmbedURL('1234567890', nonce) }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
		await Promise.resolve();
		expect(result.queryByTitle('Xの投稿動画')).toBeNull();
	});

	test('keeps one retry action when official embed loading fails', async () => {
		initApi(vi.fn() as PluginHost['api'], (async () => { throw new Error('mock failure'); }) as PluginHost['fetch']);
		const result = render(FxPreview, { props: { data: videoData(), input, context } });
		await fireEvent.click(await waitFor(() => result.getByRole('button', { name: '動画を再生' })));
		await waitFor(() => expect(result.getByText('動画を再生できませんでした。')).toBeTruthy());
		expect(result.getAllByRole('button', { name: 'もう一度試す' })).toHaveLength(1);
		expect(result.queryByText('Xで開く')).toBeNull();
	});
});
