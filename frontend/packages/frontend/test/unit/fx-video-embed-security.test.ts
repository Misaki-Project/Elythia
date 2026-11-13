/*
 * SPDX-FileCopyrightText: syuilo and misskey-project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import { cleanup, fireEvent, render, waitFor } from '@testing-library/vue';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { defineComponent, nextTick, onMounted } from 'vue';
import type { PluginHost, PreviewContext } from '@/plugin-api.js';
import FxVideoPreview from '../../../../../plugins/fxtwitter-preview/frontend/FxVideoPreview.vue';
import type { PublicVideo } from '../../../../../plugins/fxtwitter-preview/frontend/api';
import { initApi } from '../../../../../plugins/fxtwitter-preview/frontend/api';

const context: PreviewContext = {
	detail: true,
	manual: true,
	revealed: true,
	active: true,
	inViewport: true,
	source: { kind: 'note', noteId: 'note-1' },
};

function xEmbedURL(nonce: string, mutation?: (params: URLSearchParams) => void): string {
	const params = new URLSearchParams({
		frame: 'false',
		maxWidth: '560px',
		width: '550px',
		id: '1234567890',
		embedId: nonce,
		theme: 'light',
		lang: 'ja',
		hideCard: 'false',
		hideThread: 'false',
		dnt: 'true',
	});
	mutation?.(params);
	return `https://platform.twitter.com/embed/Tweet.html?${params.toString()}`;
}

function renderVideo(fetcher: PluginHost['fetch']) {
	initApi(vi.fn() as PluginHost['api'], fetcher);
	const guard = defineComponent({
		emits: ['update:revealed'],
		setup(_props, { emit, slots }) {
			onMounted(() => emit('update:revealed', true));
			return () => slots.default?.();
		},
	});
	return render(FxVideoPreview, {
		props: {
			video: { provider: 'x', embedToken: 'short-lived-capability' } satisfies PublicVideo,
			posterSrc: null,
			cachePostId: '1234567890',
			contentPostId: '1234567890',
			target: 'main',
			context,
			isSensitive: false,
		},
		global: { stubs: { MkSensitiveMediaGuard: guard } },
	});
}

afterEach(() => {
	cleanup();
	vi.restoreAllMocks();
	vi.useRealTimers();
});

describe('official X video embed security', () => {
	test.each([
		['duplicate id', (params: URLSearchParams) => params.append('id', '1234567890')],
		['missing required key', (params: URLSearchParams) => params.delete('width')],
		['extra key', (params: URLSearchParams) => params.set('autoplay', '1')],
	])('rejects an X embed URL with %s', async (_label, mutate) => {
		const fetcher = vi.fn(async (_path: string, options?: RequestInit) => {
			const body = JSON.parse(String(options?.body)) as { nonce: string };
			return new Response(JSON.stringify({ url: xEmbedURL(body.nonce, mutate) }), { status: 200, headers: { 'Content-Type': 'application/json' } });
		});
		const result = renderVideo(fetcher as PluginHost['fetch']);
		await fireEvent.click(await waitFor(() => result.getByRole('button', { name: '動画を再生' })));
		await waitFor(() => expect(result.getByRole('button', { name: 'もう一度試す' })).toBeTruthy());
		expect(result.baseElement.querySelector('iframe')).toBeNull();
	});

	test('a generic iframe load neither verifies X resize state nor cancels the timeout', async () => {
		const fetcher = vi.fn(async (_path: string, options?: RequestInit) => {
			const body = JSON.parse(String(options?.body)) as { nonce: string };
			return new Response(JSON.stringify({ url: xEmbedURL(body.nonce) }), { status: 200, headers: { 'Content-Type': 'application/json' } });
		});
		const result = renderVideo(fetcher as PluginHost['fetch']);
		const setTimeoutSpy = vi.spyOn(window, 'setTimeout');
		const clearTimeoutSpy = vi.spyOn(window, 'clearTimeout');
		await fireEvent.click(await waitFor(() => result.getByRole('button', { name: '動画を再生' })));
		const frame = await waitFor(() => {
			const found = result.baseElement.querySelector('iframe') as HTMLIFrameElement | null;
			expect(found).not.toBeNull();
			return found!;
		});
		const timerIndex = setTimeoutSpy.mock.calls.findIndex(([, delay]) => delay === 15_000);
		expect(timerIndex).toBeGreaterThanOrEqual(0);
		const timeoutCallback = setTimeoutSpy.mock.calls[timerIndex][0] as () => void;
		const timeoutHandle = setTimeoutSpy.mock.results[timerIndex].value;

		await fireEvent.load(frame);
		expect(frame.style.height).toBe('360px');
		expect(clearTimeoutSpy).not.toHaveBeenCalledWith(timeoutHandle);

		timeoutCallback();
		await nextTick();
		await waitFor(() => expect(result.baseElement.querySelector('iframe')).toBeNull());
		expect(result.getByRole('button', { name: 'もう一度試す' })).toBeTruthy();
	});

	test('a verified X resize keeps the iframe after the 15-second timeout', async () => {
		vi.useFakeTimers();
		const fetcher = vi.fn(async (_path: string, options?: RequestInit) => {
			const body = JSON.parse(String(options?.body)) as { nonce: string };
			return new Response(JSON.stringify({ url: xEmbedURL(body.nonce) }), { status: 200, headers: { 'Content-Type': 'application/json' } });
		});
		const result = renderVideo(fetcher as PluginHost['fetch']);
		await nextTick();
		const playButton = result.container.querySelector('[data-fx-video-play]');
		expect(playButton).not.toBeNull();
		await fireEvent.click(playButton!);
		for (let step = 0; step < 4; step++) {
			await Promise.resolve();
			await nextTick();
		}
		const frame = result.baseElement.querySelector('iframe') as HTMLIFrameElement | null;
		expect(frame).not.toBeNull();
		const embedUrl = new URL(frame!.src);
		const resize = new MessageEvent('message', {
			origin: 'https://platform.twitter.com',
			source: frame!.contentWindow,
			data: { 'twttr.embed': { method: 'twttr.private.resize', id: embedUrl.searchParams.get('embedId'), params: [{ height: 427 }] } },
		});
		window.dispatchEvent(resize);
		await nextTick();
		expect(frame!.style.height).toBe('427px');
		await vi.advanceTimersByTimeAsync(15_001);
		await nextTick();
		expect(result.baseElement.querySelector('iframe')).toBe(frame);
		expect(result.queryByRole('button', { name: 'もう一度試す' })).toBeNull();
	});
});
