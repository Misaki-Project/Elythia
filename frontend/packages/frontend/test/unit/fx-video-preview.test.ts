/*
 * SPDX-FileCopyrightText: syuilo and misskey-project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import { afterEach, expect, test, vi } from 'vitest';
import { cleanup, fireEvent, render, waitFor } from '@testing-library/vue';
import { defineComponent, onMounted } from 'vue';
import FxVideoPreview from '../../../../../plugins/fxtwitter-preview/frontend/FxVideoPreview.vue';
import { initApi } from '../../../../../plugins/fxtwitter-preview/frontend/api.js';
import type { PreviewContext, PluginHost } from '@/plugin-api.js';
import { preferReactive, preferState } from '../setup.unit.js';

const video = { provider: 'x' as const, embedToken: '0123456789abcdef0123456789abcdef' };
const source = { kind: 'note' as const, noteId: 'note-1' };
const context: PreviewContext = { detail: false, revealed: true, active: true, inViewport: true, source };

function setTimelinePreference(value: 'ogp' | 'text' | 'media'): void {
	preferState.xTimelinePreviewMode = value;
	preferReactive.xTimelinePreviewMode.value = value;
}

function makeApi() {
	const call = vi.fn(async (path: string, params: Record<string, string>) => ({
		url: 'https://platform.twitter.com/embed/Tweet.html?' + new URLSearchParams({ frame: 'false', maxWidth: '560px', width: '550px', id: '1234567890', embedId: params.nonce, theme: 'light', lang: 'ja', hideCard: 'false', hideThread: 'false', dnt: 'true' }),
	}));
	initApi(call as PluginHost['api']);
	return call;
}

function renderVideo(ctx = context, overrides: Record<string, unknown> = {}) {
	const guard = defineComponent({
		emits: ['update:revealed'],
		setup(_props, { emit, slots }) {
			onMounted(() => emit('update:revealed', true));
			return () => slots.default?.();
		},
	});
	return render(FxVideoPreview, {
		props: { video, posterSrc: 'https://example.test/poster.jpg', cachePostId: '1234567890', contentPostId: '1234567890', target: 'main', context: ctx, ...overrides },
		global: { stubs: { MkSensitiveMediaGuard: guard } },
	});
}

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

test('timeline video requires explicit play and clears the embed when leaving viewport', async () => {
	setTimelinePreference('media');
	const api = makeApi();
	const view = renderVideo();
	const play = await waitFor(() => view.getByRole('button', { name: '動画を再生' }));
	const cardClass = view.container.querySelector('[data-fx-video-target="main"]')?.className;
	expect(view.container.querySelector('a')).toBeNull();
	expect(api).not.toHaveBeenCalled();
	await fireEvent.click(play);
	await waitFor(() => expect(view.baseElement.querySelector('[data-fx-video-modal] iframe')?.getAttribute('src')).toContain('/embed/Tweet.html?'));
	expect(view.container.querySelector('[data-fx-video-target="main"]')?.className).toBe(cardClass);
	expect(view.container.querySelector('[data-video-poster-count]')).toBeTruthy();
	expect(view.container.querySelector('iframe')).toBeNull();
	expect(api).toHaveBeenCalledTimes(1);
	await view.rerender({ context: { ...context, inViewport: false } });
	await waitFor(() => expect(view.baseElement.querySelector('[data-fx-video-modal]')).toBeNull());
	await view.rerender({ context: { ...context, inViewport: true } });
	expect(view.baseElement.querySelector('[data-fx-video-modal]')).toBeNull();
	expect(api).toHaveBeenCalledTimes(1);
});

test('media mode never autoplays an official video embed', async () => {
	setTimelinePreference('media');
	const api = makeApi();
	const view = renderVideo(context);
	const play = await waitFor(() => view.getByRole('button', { name: '動画を再生' }));
	expect(api).not.toHaveBeenCalled();
	await fireEvent.click(play);
	await waitFor(() => expect(view.baseElement.querySelector('[data-fx-video-modal] iframe')?.getAttribute('src')).toContain('/embed/Tweet.html?'));
	expect(api).toHaveBeenCalledTimes(1);
});

test('closing the video modal removes its iframe and stops playback', async () => {
	setTimelinePreference('media');
	const api = makeApi();
	const view = renderVideo();
	await fireEvent.click(await waitFor(() => view.getByRole('button', { name: '動画を再生' })));
	await waitFor(() => expect(view.baseElement.querySelector('[data-fx-video-modal] iframe')).toBeTruthy());
	await fireEvent.click(view.baseElement.querySelector('[data-fx-video-modal-close]')!);
	await waitFor(() => expect(view.baseElement.querySelector('[data-fx-video-modal]')).toBeNull());
	expect(view.container.querySelector('[data-fx-video-play]')).toBeTruthy();
	expect(api).toHaveBeenCalledTimes(1);
});

test('fits the modal within its available viewport and enables the play button on close', async () => {
	setTimelinePreference('media');
	makeApi();
	const view = renderVideo();
	const play = await waitFor(() => view.getByRole('button', { name: '動画を再生' }));
	play.focus();
	await fireEvent.click(play);
	const dialog = await waitFor(() => view.baseElement.querySelector('[data-fx-video-modal]') as HTMLElement | null);
	expect(dialog).not.toBeNull();
	expect(dialog!.style.boxSizing).toBe('border-box');
	expect(dialog!.style.margin).toBe('auto');
	expect(dialog!.style.width).toBe('100%');
	expect(dialog!.style.maxWidth).toBe('592px');
	expect(dialog!.style.maxHeight).toBe('100%');
	await fireEvent.click(view.baseElement.querySelector('[data-fx-video-modal-close]')!);
	await waitFor(() => expect(view.baseElement.querySelector('[data-fx-video-modal]')).toBeNull());
	await waitFor(() => expect((play as HTMLButtonElement).disabled).toBe(false));
});

test('native proxy requests only the selected video and opens it in the media viewer', async () => {
	setTimelinePreference('media');
	const payload = new Blob(['selected video'], { type: 'video/mp4' });
	const fetcher = vi.fn(async (_path: string, _options?: RequestInit) => new Response(payload, { status: 200, headers: { 'Content-Type': 'video/mp4' } }));
	const mediaViewer = vi.fn(async (_items: Parameters<PluginHost['openMedia']>[0], _initialId?: string) => undefined);
	initApi(vi.fn() as PluginHost['api'], fetcher as PluginHost['fetch'], mediaViewer as PluginHost['openMedia']);
	const view = renderVideo(context, {
		video: { provider: 'x', embedToken: '0123456789abcdef0123456789abcdef', playbackAvailable: true },
		posterSrc: 'https://example.test/poster-2.jpg', cachePostId: 'post-1', contentPostId: 'post-1',
		target: 'main', videoIndex: 1, playbackMode: 'nativeProxy',
	});
	await fireEvent.click(await waitFor(() => view.getByRole('button', { name: '動画を再生' })));
	await waitFor(() => expect(mediaViewer).toHaveBeenCalledTimes(1));
	expect(fetcher).toHaveBeenCalledTimes(1);
	expect(fetcher.mock.calls[0][0]).toBe('/api/plugin/fxtwitter-preview/media/video');
	expect(JSON.parse(String(fetcher.mock.calls[0][1]?.body))).toEqual({
		noteId: 'note-1', postId: 'post-1', target: 'main', index: 1, token: '0123456789abcdef0123456789abcdef',
	});
	expect(mediaViewer.mock.calls[0][0]).toEqual([expect.objectContaining({
		type: 'video', blob: payload, filename: 'x-post-1-2.mp4', initiallyRevealed: true,
	})]);
	expect(view.container.querySelector('[data-video-poster-count]')).toBeTruthy();
});

test('native proxy mode preserves YouTube official embed playback', async () => {
	setTimelinePreference('media');
	const api = vi.fn(async (_path: string, _params: Record<string, unknown>) => ({ url: 'https://www.youtube.com/embed/abcdefghijk?autoplay=0&playsinline=1' }));
	initApi(api as PluginHost['api']);
	const view = renderVideo(context, {
		video: { provider: 'youtube', providerVideoId: 'abcdefghijk', embedToken: '0123456789abcdef0123456789abcdef' },
		playbackMode: 'nativeProxy',
	});
	await fireEvent.click(await waitFor(() => view.getByRole('button', { name: '動画を再生' })));
	await waitFor(() => expect(view.baseElement.querySelector('[data-fx-video-modal] iframe')?.getAttribute('src')).toBe('https://www.youtube.com/embed/abcdefghijk?autoplay=0&playsinline=1'));
	expect(api).toHaveBeenCalledTimes(1);
	expect(api.mock.calls[0][0]).toBe('plugin/fxtwitter-preview/embed/shell');
});

test('link-only mode does not reuse a stale embed token to show a play button', () => {
	setTimelinePreference('media');
	const api = makeApi();
	const view = renderVideo(context, { playbackMode: 'linkOnly' });
	expect(view.queryByRole('button', { name: '動画を再生' })).toBeNull();
	expect(api).not.toHaveBeenCalled();
});

test('native proxy cancels a selected-video request when authorization context changes', async () => {
	setTimelinePreference('media');
	const fetcher = vi.fn((_path: string, _options?: RequestInit) => new Promise<Response>(() => undefined));
	initApi(vi.fn() as PluginHost['api'], fetcher as PluginHost['fetch']);
	const view = renderVideo(context, {
		video: { provider: 'x', embedToken: '0123456789abcdef0123456789abcdef', playbackAvailable: true },
		cachePostId: 'post-1', contentPostId: 'post-1', videoIndex: 1, playbackMode: 'nativeProxy', postHref: 'https://x.com/i/status/post-1',
	});
	await fireEvent.click(await waitFor(() => view.getByRole('button', { name: '動画を再生' })));
	await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(1));
	const requestSignal = fetcher.mock.calls[0][1]?.signal as AbortSignal;
	expect(requestSignal.aborted).toBe(false);
	await view.rerender({ context: { ...context, active: false } });
	await waitFor(() => expect(requestSignal.aborted).toBe(true));
	expect(view.baseElement.querySelector('[data-fx-video-modal]')).toBeNull();
});

test('native proxy failure offers a direct post fallback without changing video selection', async () => {
	setTimelinePreference('media');
	const fetcher = vi.fn(async (_path: string, _options?: RequestInit) => new Response('unavailable', { status: 403 }));
	initApi(vi.fn() as PluginHost['api'], fetcher as PluginHost['fetch']);
	const view = renderVideo(context, {
		video: { provider: 'x', embedToken: '0123456789abcdef0123456789abcdef', playbackAvailable: true },
		cachePostId: 'post-1', contentPostId: 'quoted-1', target: 'quote', videoIndex: 1, playbackMode: 'nativeProxy', postHref: 'https://x.com/i/status/quoted-1',
	});
	await fireEvent.click(await waitFor(() => view.getByRole('button', { name: '動画を再生' })));
	await waitFor(() => expect(view.getByRole('link', { name: '動画をXで開く' }).getAttribute('href')).toBe('https://x.com/i/status/quoted-1'));
	expect(JSON.parse(String(fetcher.mock.calls[0][1]?.body))).toMatchObject({ postId: 'post-1', target: 'quote', index: 1 });
	expect(view.baseElement.querySelector('iframe')).toBeNull();
});

test('clicking the modal backdrop closes the player and restores the poster', async () => {
	setTimelinePreference('media');
	makeApi();
	const view = renderVideo();
	await fireEvent.click(await waitFor(() => view.getByRole('button', { name: '動画を再生' })));
	await waitFor(() => expect(view.baseElement.querySelector('[data-fx-video-modal] iframe')).toBeTruthy());
	await fireEvent.click(view.baseElement.querySelector('[data-testid="bg"]')!);
	await waitFor(() => expect(view.baseElement.querySelector('[data-fx-video-modal]')).toBeNull());
	expect(view.container.querySelector('[data-video-poster-count]')).toBeTruthy();
});

test('a late embed response cannot reopen playback after authorization is lost', async () => {
	setTimelinePreference('media');
	let resolveApi!: (value: { url: string }) => void;
	const api = vi.fn((_path: string, params: Record<string, string>) => new Promise<{ url: string }>(resolve => {
		resolveApi = resolve;
	}));
	initApi(api as PluginHost['api']);
	const view = renderVideo();
	await fireEvent.click(await waitFor(() => view.getByRole('button', { name: '動画を再生' })));
	await waitFor(() => expect(api).toHaveBeenCalledTimes(1));
	await view.rerender({ context: { ...context, active: false } });
	resolveApi({ url: 'https://platform.twitter.com/embed/Tweet.html?' + new URLSearchParams({ frame: 'false', maxWidth: '560px', width: '550px', id: '1234567890', embedId: String(api.mock.calls[0][1].nonce), theme: 'light', lang: 'ja', hideCard: 'false', hideThread: 'false', dnt: 'true' }) });
	await Promise.resolve();
	expect(view.baseElement.querySelector('[data-fx-video-modal]')).toBeNull();
	expect(view.baseElement.querySelector('iframe')).toBeNull();
});

test('changing the active post closes an already open player modal', async () => {
	setTimelinePreference('media');
	makeApi();
	const view = renderVideo();
	await fireEvent.click(await waitFor(() => view.getByRole('button', { name: '動画を再生' })));
	await waitFor(() => expect(view.baseElement.querySelector('[data-fx-video-modal] iframe')).toBeTruthy());
	await view.rerender({ context: { ...context, source: { kind: 'note', noteId: 'note-2' } } });
	await waitFor(() => expect(view.baseElement.querySelector('[data-fx-video-modal]')).toBeNull());
	expect(view.container.querySelector('[data-video-poster-count]')).toBeTruthy();
});
