/*
 * SPDX-FileCopyrightText: syuilo and misskey-project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { cleanup, fireEvent, render, waitFor } from '@testing-library/vue';
import { components } from '@/components/index.js';
import { directives } from '@/directives/index.js';
import type { PluginHost, PreviewContext, PreviewInput } from '@/plugin-api.js';
import FxPreview from './FxPreview.vue';
import { initApi } from './api.js';
import type { PreviewResponse, PublicPost } from './api.js';

vi.mock('@/server-plugins.generated.js', () => ({ serverPlugins: [] }));

const input = { kind: 'url', href: 'https://x.com/alice/status/1234567890' } satisfies PreviewInput;
const context: PreviewContext = { detail: true, revealed: true, active: true, inViewport: true, source: { kind: 'note', noteId: 'note-1' } };
const globals = { components, directives };
let hostApi: ReturnType<typeof vi.fn>;
let hostFetch: ReturnType<typeof vi.fn>;
let openMedia: ReturnType<typeof vi.fn>;
let publicFetch: ReturnType<typeof vi.spyOn>;
let objectURLIndex: number;

function makePost(overrides: Partial<PublicPost> = {}): PublicPost {
	return {
		id: '1234567890', url: input.href, text: 'post text',
		author: { name: 'Alice', screenName: 'alice' }, likes: 0, reposts: 0, quotes: 0, replies: 0,
		images: [], hasVideo: false, ...overrides,
	};
}

function card(post: PublicPost, ctx = context) {
	const data: PreviewResponse = { state: 'ready', post };
	return render(FxPreview, { props: { input, data, context: ctx }, global: globals });
}

function mediaURL(index: number, ext = 'png'): string {
	return `/api/plugin/fxtwitter-preview/public-media/${String(index).padStart(32, '0')}.${ext}`;
}

beforeEach(() => {
	objectURLIndex = 0;
	hostApi = vi.fn(async (_path: string, params: Record<string, unknown>) => {
		const index = Number(params.index ?? 0);
		const isVideo = params.kind === 'video';
		return { url: mediaURL(index + (isVideo ? 100 : 0), isVideo ? 'mp4' : 'png'), contentType: isVideo ? 'video/mp4' : 'image/png', size: 3, expiresAt: '2026-11-15T00:00:00Z' };
	});
	hostFetch = vi.fn(async () => new Response(new Blob(['private'], { type: 'image/png' }), { status: 200, headers: { 'Content-Type': 'image/png' } }));
	openMedia = vi.fn(async () => undefined);
	initApi(hostApi as PluginHost['api'], hostFetch as PluginHost['fetch'], openMedia as PluginHost['openMedia']);
	vi.stubGlobal('fetch', async (url: string | URL | Request) => new Response(new Blob(['abc'], { type: String(url).endsWith('.mp4') ? 'video/mp4' : 'image/png' }), {
		status: 200,
		headers: { 'Content-Type': String(url).endsWith('.mp4') ? 'video/mp4' : 'image/png', 'Content-Length': '3' },
	}));
	publicFetch = vi.spyOn(window, 'fetch');
	Object.defineProperty(URL, 'createObjectURL', { configurable: true, value: vi.fn(() => `blob:fx-${++objectURLIndex}`) });
	Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, value: vi.fn() });
});

afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

test('loads only six visible images, then opens all seven in original order on explicit click', async () => {
	const post = makePost({ images: Array.from({ length: 7 }, (_, index) => ({ url: `/api/plugin/fxtwitter-preview/media/1234567890/image/${index}?noteId=note-1`, alt: `image-${index}` })) });
	const view = card(post);
	await waitFor(() => expect(hostApi.mock.calls.filter(call => call[1]?.kind === 'image')).toHaveLength(6));
	expect(publicFetch).toHaveBeenCalledTimes(6);
	const firstRequest = publicFetch.mock.calls[0] as unknown as [RequestInfo | URL, RequestInit?] | undefined;
	expect(firstRequest?.[1]?.credentials).toBe('omit');
	expect(firstRequest?.[1]?.headers).toBeUndefined();
	await waitFor(() => expect(view.container.querySelectorAll('[data-fx-media-grid] button')).toHaveLength(6));
	const tiles = view.container.querySelectorAll('[data-fx-media-grid] button');
	expect(tiles).toHaveLength(6);
	expect(view.container.querySelector('[data-photo-overflow]')?.textContent).toBe('+1');
	await fireEvent.click(tiles[5]);
	await waitFor(() => expect(openMedia).toHaveBeenCalledOnce());
	expect(hostApi.mock.calls.filter(call => call[1]?.kind === 'image')).toHaveLength(7);
	const [items, initialId] = openMedia.mock.calls[0];
	expect(items.map((item: { id: string }) => item.id)).toEqual(Array.from({ length: 7 }, (_, index) => `1234567890:${index}`));
	expect(initialId).toBe('1234567890:5');
});

test('keeps a failed seventh-grid slot and retries the whole viewer without opening partial media', async () => {
	const post = makePost({ images: Array.from({ length: 7 }, (_, index) => ({ url: `/api/plugin/fxtwitter-preview/media/1234567890/image/${index}?noteId=note-1`, alt: `image-${index}` })) });
	hostApi.mockImplementation(async (_path: string, params: Record<string, unknown>) => {
		if (_path.endsWith('/preview')) return { state: 'ready', post };
		if (params.kind === 'image' && params.index === 2 && hostApi.mock.calls.filter(call => call[1]?.kind === 'image' && call[1]?.index === 2).length === 1) throw Object.assign(new Error('temporary'), { code: 'TEMPORARY' });
		return { url: mediaURL(Number(params.index ?? 0)), contentType: 'image/png', size: 3, expiresAt: '2026-11-15T00:00:00Z' };
	});
	const view = card(post);
	await waitFor(() => expect(view.container.querySelector('[data-photo-state="error"]')).not.toBeNull());
	expect(view.container.querySelector('[data-photo-overflow]')?.textContent).toBe('+1');
	expect(openMedia).not.toHaveBeenCalled();
	await fireEvent.click(view.getByRole('button', { name: '写真を再試行' }));
	await waitFor(() => expect(view.container.querySelector('[data-photo-state="error"]')).toBeNull());
	await fireEvent.click(view.container.querySelectorAll('[data-fx-media-grid] button')[2]);
	await waitFor(() => expect(openMedia).toHaveBeenCalledOnce());
	const [items] = openMedia.mock.calls[0];
	expect(items.map((item: { id: string }) => item.id)).toEqual(Array.from({ length: 7 }, (_, index) => `1234567890:${index}`));
});

test('does not burst-fetch twenty main and twenty quote images; clicking quote loads that gallery in order', async () => {
	const quote = { id: '9876543210', url: 'https://x.com/bob/status/9876543210', text: 'quote', author: { name: 'Bob', screenName: 'bob' }, images: Array.from({ length: 20 }, (_, index) => ({ url: `/api/plugin/fxtwitter-preview/media/1234567890/quote-image/${index}?noteId=note-1` })), hasVideo: false };
	const post = makePost({ images: Array.from({ length: 20 }, (_, index) => ({ url: `/api/plugin/fxtwitter-preview/media/1234567890/image/${index}?noteId=note-1` })), quote });
	const view = card(post);
	await waitFor(() => expect(hostApi.mock.calls.filter(call => call[1]?.kind === 'image' || call[1]?.kind === 'quote-image')).toHaveLength(12));
	expect(publicFetch).toHaveBeenCalledTimes(12);
	const quoteGrid = view.container.querySelectorAll('[data-fx-media-grid]')[1];
	await fireEvent.click(quoteGrid.querySelectorAll('button')[5]);
	await waitFor(() => expect(openMedia).toHaveBeenCalledOnce());
	const [items, initialId] = openMedia.mock.calls[0];
	expect(items).toHaveLength(20);
	expect(items[0].id).toBe('9876543210:0');
	expect(items[19].id).toBe('9876543210:19');
	expect(initialId).toBe('9876543210:5');
});

test('plays each video through its own playback capability and viewer item', async () => {
	const video = (index: number) => ({ provider: 'x' as const, playbackAvailable: true, playbackUrl: `/api/plugin/fxtwitter-preview/media/video/1234567890/main/${index}?noteId=note-1&token=${String(index + 1).repeat(32)}`, posterUrl: `/api/plugin/fxtwitter-preview/media/1234567890/poster/${index}?noteId=note-1`, durationMs: 65000 });
	const post = makePost({ hasVideo: true, videos: [video(0), video(1)] });
	const view = card(post);
	await waitFor(() => expect(view.getAllByRole('button', { name: '動画を再生' })).toHaveLength(2));
	await fireEvent.click(view.getAllByRole('button', { name: '動画を再生' })[1]);
	await waitFor(() => expect(openMedia).toHaveBeenCalledOnce());
	expect(hostApi).toHaveBeenCalledWith(expect.stringMatching(/media\/prepare$/), expect.objectContaining({ kind: 'video', target: 'main', index: 1, playbackUrl: video(1).playbackUrl }), expect.anything());
	const [firstCallItems, firstId] = openMedia.mock.calls[0];
	expect(firstCallItems).toHaveLength(1);
	expect(firstCallItems[0]).toMatchObject({ id: '1234567890:video:1', type: 'video', filename: 'x-1234567890-2.mp4' });
	expect(firstId).toBe('1234567890:video:1');
	expect(view.getAllByText('1:05')).toHaveLength(2);
	await fireEvent.click(view.getAllByRole('button', { name: '動画を再生' })[0]);
	await waitFor(() => expect(openMedia).toHaveBeenCalledTimes(2));
	expect(openMedia.mock.calls[1][0][0].id).toBe('1234567890:video:0');
});

test('refreshes an expired video capability on explicit retry and does not retry 403', async () => {
	const firstVideo = { provider: 'x' as const, playbackAvailable: true, playbackUrl: `/api/plugin/fxtwitter-preview/media/video/1234567890/main/0?noteId=note-1&token=${'1'.repeat(32)}` };
	const freshVideo = { ...firstVideo, playbackUrl: `/api/plugin/fxtwitter-preview/media/video/1234567890/main/0?noteId=note-1&token=${'2'.repeat(32)}` };
	const post = makePost({ hasVideo: true, videos: [firstVideo] });
	const expiredView = card(post);
	hostApi.mockImplementation(async (path: string, params: Record<string, unknown>) => {
		if (path.endsWith('/preview')) return { state: 'ready', post: { ...post, videos: [freshVideo] } };
		if (params.kind === 'video' && params.playbackUrl === firstVideo.playbackUrl) throw Object.assign(new Error('expired'), { status: 404 });
		return { url: mediaURL(100, 'mp4'), contentType: 'video/mp4', size: 3, expiresAt: '2026-11-15T00:00:00Z' };
	});
	await fireEvent.click(await expiredView.findByRole('button', { name: '動画を再生' }));
	await waitFor(() => expect(expiredView.getByText('動画を読み込めませんでした。')).toBeTruthy());
	await fireEvent.click(expiredView.getByRole('button', { name: '再試行' }));
	await waitFor(() => expect(openMedia).toHaveBeenCalledOnce());
	expect(hostApi).toHaveBeenCalledWith(expect.stringMatching(/\/preview$/), expect.objectContaining({ noteId: 'note-1', postId: '1234567890' }), expect.anything());
	expect(hostApi).toHaveBeenCalledWith(expect.stringMatching(/media\/prepare$/), expect.objectContaining({ playbackUrl: freshVideo.playbackUrl }), expect.anything());

	cleanup();
	openMedia.mockClear();
	hostApi.mockImplementation(async (_path: string, params: Record<string, unknown>) => {
		if (params.kind === 'video') throw Object.assign(new Error('forbidden'), { status: 403 });
		return { url: mediaURL(Number(params.index ?? 0)), contentType: 'image/png', size: 3, expiresAt: '2026-11-15T00:00:00Z' };
	});
	const deniedView = card(post);
	await fireEvent.click(await deniedView.findByRole('button', { name: '動画を再生' }));
	await waitFor(() => expect(deniedView.getByText('動画へのアクセスが許可されていません。')).toBeTruthy());
	expect(deniedView.queryByRole('button', { name: '再試行' })).toBeNull();
});

test('does not offer repeated photo requests after a forbidden response and keeps time below media', async () => {
	const post = makePost({ createdAt: '2026-11-14T00:00:00Z', images: [{ url: '/api/plugin/fxtwitter-preview/media/1234567890/image/0?noteId=note-1' }] });
	vi.stubGlobal('fetch', async () => new Response(null, { status: 403 }));
	const view = card(post);
	await waitFor(() => expect(view.container.querySelector('[data-photo-state="forbidden"]')).not.toBeNull());
	expect(view.queryByRole('button', { name: '写真を再試行' })).toBeNull();
	const media = view.container.querySelector('[data-fx-media-grid]');
	const time = view.container.querySelector('[data-fx-post-time="main"]');
	expect(media).not.toBeNull();
	expect(time).not.toBeNull();
	expect(Boolean(media!.compareDocumentPosition(time!) & Node.DOCUMENT_POSITION_FOLLOWING)).toBe(true);
});

test('does not repeat forbidden photo requests when retrying another failed slot', async () => {
	const post = makePost({ images: [0, 1].map(index => ({ url: `/api/plugin/fxtwitter-preview/media/1234567890/image/${index}?noteId=note-1` })) });
	hostApi.mockImplementation(async (path: string, params: Record<string, unknown>) => {
		if (path.endsWith('/preview')) return { state: 'ready', post };
		return { url: mediaURL(Number(params.index ?? 0)), contentType: 'image/png', size: 3, expiresAt: '2026-11-15T00:00:00Z' };
	});
	let publicAttempt = 0;
	vi.stubGlobal('fetch', async () => {
		publicAttempt++;
		if (publicAttempt === 1) return new Response(null, { status: 403 });
		if (publicAttempt === 2) return new Response(null, { status: 503 });
		return new Response(new Blob(['abc'], { type: 'image/png' }), { status: 200, headers: { 'Content-Type': 'image/png', 'Content-Length': '3' } });
	});
	const view = card(post);
	await waitFor(() => expect(view.container.querySelector('[data-photo-state="forbidden"]')).not.toBeNull());
	await waitFor(() => expect(view.container.querySelector('[data-photo-state="error"]')).not.toBeNull());
	await fireEvent.click(view.getByRole('button', { name: '写真を再試行' }));
	await waitFor(() => expect(view.container.querySelector('[data-photo-state="error"]')).toBeNull());
	expect(publicAttempt).toBe(3);
});

test('blocks photo retry when refreshed quote identity or sensitivity changed', async () => {
	const quote = { id: '9876543210', url: 'https://x.com/bob/status/9876543210', text: 'quote', author: { name: 'Bob', screenName: 'bob' }, images: [{ url: '/api/plugin/fxtwitter-preview/media/1234567890/quote-image/0?noteId=note-1' }], hasVideo: false, isSensitive: false };
	const post = makePost({ images: [{ url: '/api/plugin/fxtwitter-preview/media/1234567890/image/0?noteId=note-1' }], quote });
	hostApi.mockImplementation(async (path: string, params: Record<string, unknown>) => {
		if (path.endsWith('/preview')) return { state: 'ready', post: { ...post, quote: { ...quote, id: '1111111111', isSensitive: true } } };
		if (params.kind === 'image') throw Object.assign(new Error('temporary'), { status: 404 });
		return { url: mediaURL(Number(params.index ?? 0)), contentType: 'image/png', size: 3, expiresAt: '2026-11-15T00:00:00Z' };
	});
	const view = card(post);
	await waitFor(() => expect(view.container.querySelector('[data-photo-state="error"]')).not.toBeNull());
	await fireEvent.click(view.getByRole('button', { name: '写真を再試行' }));
	await waitFor(() => expect(view.queryByRole('button', { name: '写真を再試行' })).toBeNull());
	expect(openMedia).not.toHaveBeenCalled();
	expect(hostApi.mock.calls.filter(call => call[1]?.kind === 'image' || call[1]?.kind === 'quote-image')).toHaveLength(2);
});

test('does not mount individual video controls in text or compact mode', () => {
	const video = { provider: 'x' as const, playbackAvailable: true, playbackUrl: `/api/plugin/fxtwitter-preview/media/video/1234567890/main/0?noteId=note-1&token=${'3'.repeat(32)}` };
	const post = makePost({ hasVideo: true, videos: [video] });
	const text = card(post, { ...context, preferenceValue: 'text' });
	expect(text.queryByRole('button', { name: '動画を再生' })).toBeNull();
	text.unmount();
	const compact = card(post, { ...context, detail: false });
	expect(compact.queryByRole('button', { name: '動画を再生' })).toBeNull();
	expect(compact.getByText('動画1件')).toBeTruthy();
});

test('does not play a refreshed quote video after the quote identity changes', async () => {
	const video = { provider: 'x' as const, playbackAvailable: true, mediaIdentity: 'video-original', playbackUrl: `/api/plugin/fxtwitter-preview/media/video/1234567890/quote/0?noteId=note-1&token=${'4'.repeat(32)}` };
	const quote = { id: '9876543210', url: 'https://x.com/bob/status/9876543210', text: 'quote', author: { name: 'Bob', screenName: 'bob' }, images: [], hasVideo: true, videos: [video] };
	const post = makePost({ quote });
	hostApi.mockImplementation(async (path: string, params: Record<string, unknown>) => {
		if (path.endsWith('/preview')) return { state: 'ready', post: { ...post, quote: { ...quote, id: '1111111111' } } };
		if (params.kind === 'video') throw Object.assign(new Error('expired'), { status: 404 });
		return { url: mediaURL(Number(params.index ?? 0), 'mp4'), contentType: 'video/mp4', size: 3, expiresAt: '2026-11-15T00:00:00Z' };
	});
	const view = card(post);
	await fireEvent.click(await view.findByRole('button', { name: '動画を再生' }));
	await waitFor(() => expect(view.getByText('動画を読み込めませんでした。')).toBeTruthy());
	await fireEvent.click(view.getByRole('button', { name: '再試行' }));
	await waitFor(() => expect(view.getByText('動画へのアクセスが許可されていません。')).toBeTruthy());
	expect(openMedia).not.toHaveBeenCalled();
	expect(hostApi.mock.calls.filter(call => call[1]?.kind === 'video')).toHaveLength(1);
});

test('aborts full-gallery loading when the card leaves the viewport without fetching later images', async () => {
	let release!: (value: { url: string; contentType: string; size: number; expiresAt: string }) => void;
	const post = makePost({ images: Array.from({ length: 20 }, (_, index) => ({ url: `/api/plugin/fxtwitter-preview/media/1234567890/image/${index}?noteId=note-1` })) });
	hostApi.mockImplementation(async (_path: string, params: Record<string, unknown>) => {
		if (params.kind === 'image' && params.index === 6) return new Promise(resolve => { release = resolve; });
		const index = Number(params.index ?? 0);
		return { url: mediaURL(index), contentType: 'image/png', size: 3, expiresAt: '2026-11-15T00:00:00Z' };
	});
	const view = card(post);
	await waitFor(() => expect(hostApi.mock.calls.filter(call => call[1]?.kind === 'image')).toHaveLength(6));
	const targetButton = view.container.querySelectorAll('[data-fx-media-grid] button')[5] as HTMLButtonElement | undefined;
	await waitFor(() => expect(targetButton?.disabled).toBe(false));
	await fireEvent.click(targetButton!);
	await waitFor(() => expect(release).toBeTypeOf('function'));
	const request = hostApi.mock.calls.find(call => call[1]?.kind === 'image' && call[1]?.index === 6);
	const signal = (request?.[2] as { signal?: AbortSignal } | undefined)?.signal;
	expect(signal).toBeDefined();
	await view.rerender({ context: { ...context, inViewport: false } });
	await waitFor(() => expect(signal?.aborted).toBe(true));
	release({ url: mediaURL(6), contentType: 'image/png', size: 3, expiresAt: '2026-11-15T00:00:00Z' });
	await new Promise(resolve => window.setTimeout(resolve));
	expect(hostApi.mock.calls.filter(call => call[1]?.kind === 'image')).toHaveLength(7);
	expect(publicFetch).toHaveBeenCalledTimes(6);
	expect(openMedia).not.toHaveBeenCalled();
});
