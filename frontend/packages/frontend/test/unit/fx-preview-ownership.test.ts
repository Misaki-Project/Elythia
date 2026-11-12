/*
 * SPDX-FileCopyrightText: syuilo and misskey-project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import { cleanup, render, waitFor } from '@testing-library/vue';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { defineComponent } from 'vue';
import FxPreview from '../../../../../plugins/fxtwitter-preview/frontend/FxPreview.vue';
import type { PreviewResponse } from '../../../../../plugins/fxtwitter-preview/frontend/api';
import { apiSessionEpoch, initApi } from '../../../../../plugins/fxtwitter-preview/frontend/api';
import type { PluginHost, PreviewContext, PreviewInput } from '@/plugin-api.js';

const postUrl = 'https://x.com/alice/status/1234567890';
const input: PreviewInput = { kind: 'url', href: postUrl };

function context(noteId = 'note-1'): PreviewContext {
	return {
		detail: true,
		revealed: true,
		active: true,
		inViewport: true,
		source: { kind: 'note', noteId },
	};
}

function response(imageUrl: string, postId = '1234567890'): PreviewResponse {
	return {
		state: 'ready',
		post: {
			id: postId,
			url: postUrl,
			text: 'ownership test post',
			author: { name: 'Alice', screenName: 'alice' },
			images: [{ url: imageUrl, alt: 'ownership image' }],
			hasVideo: false,
			likes: 0,
			reposts: 0,
			quotes: 0,
			replies: 0,
		},
	};
}

function renderPreview(data: PreviewResponse, previewContext: PreviewContext, fetcher: PluginHost['fetch']) {
	initApi(vi.fn() as PluginHost['api'], fetcher);
	const guard = defineComponent({
		emits: ['update:revealed'],
		setup(_props, { emit, slots }) {
			return () => slots.default?.({ revealed: true, 'onUpdate:revealed': (value: boolean) => emit('update:revealed', value) });
		},
	});
	return render(FxPreview, {
		props: { data, input, context: previewContext },
		global: { stubs: { MkSensitiveMediaGuard: guard, MkTime: true, MkUrl: true, MkA: true } },
	});
}

function imageResponse(blob: Blob): Response {
	return {
		ok: true,
		headers: new Headers({ 'Content-Type': 'image/png' }),
		blob: async () => blob,
	} as Response;
}

afterEach(() => {
	cleanup();
	vi.restoreAllMocks();
});

describe('FxTwitter media ownership', () => {
	test.each([
		['post ID mismatch', '/api/plugin/fxtwitter-preview/media/9999999999/image/0?noteId=note-1'],
		['note ID mismatch', '/api/plugin/fxtwitter-preview/media/1234567890/image/0?noteId=note-other'],
	])('rejects a media route with a %s before fetching', async (_label, imageUrl) => {
		const fetcher = vi.fn(async () => imageResponse(new Blob(['image'])));
		const result = renderPreview(response(imageUrl), context(), fetcher as PluginHost['fetch']);

		await waitFor(() => expect(result.getByText('ownership test post')).toBeTruthy());
		expect(fetcher).not.toHaveBeenCalled();
		expect(result.container.querySelector('[data-fx-media-grid]')).toBeNull();
	});

	test('ignores a late media response after the plugin API session changes', async () => {
		let resolveFirst!: (value: Response) => void;
		const firstBlob = vi.fn(async () => new Blob(['stale-session'], { type: 'image/png' })) as Response['blob'];
		const firstResponse = new Promise<Response>(resolve => { resolveFirst = resolve; });
		const fetcher = vi.fn()
			.mockImplementationOnce(async () => await firstResponse)
			.mockImplementationOnce(async () => imageResponse(new Blob(['new-session'], { type: 'image/png' })));
		const createObjectUrl = vi.fn((blob: Blob) => blob.size > 11 ? 'blob:stale-session' : 'blob:new-session');
		const NativeURL = URL;
		vi.stubGlobal('URL', class extends NativeURL {
			static override createObjectURL = createObjectUrl;
			static override revokeObjectURL = vi.fn();
		});
		const data = response('/api/plugin/fxtwitter-preview/media/1234567890/image/0?noteId=note-1');
		const result = renderPreview(data, context(), fetcher as PluginHost['fetch']);

		await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(1));
		const firstSignal = fetcher.mock.calls[0][1]?.signal;
		const previousEpoch = apiSessionEpoch.value;
		initApi(vi.fn() as PluginHost['api'], fetcher as PluginHost['fetch']);
		expect(apiSessionEpoch.value).toBe(previousEpoch + 1);
		await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(2));
		expect(firstSignal?.aborted).toBe(true);
		await waitFor(() => expect(result.container.querySelector('[data-fx-media-grid] img')?.getAttribute('src')).toBe('blob:new-session'));

		resolveFirst({
			ok: true,
			headers: new Headers({ 'Content-Type': 'image/png' }),
			blob: firstBlob,
		} as Response);
		await Promise.resolve();
		await Promise.resolve();

		expect(firstBlob).not.toHaveBeenCalled();
		expect(createObjectUrl).toHaveBeenCalledTimes(1);
		expect(result.container.querySelector('[data-fx-media-grid] img')?.getAttribute('src')).toBe('blob:new-session');
	});

	test('rejects an image whose blob MIME does not match its response header', async () => {
		const blobReader = vi.fn(async () => new Blob(['not an image'], { type: 'text/html' }));
		const fetcher = vi.fn(async () => ({
			ok: true,
			headers: new Headers({ 'Content-Type': 'image/png' }),
			blob: blobReader,
		} as unknown as Response));
		const createObjectUrl = vi.fn(() => 'blob:unexpected');
		const NativeURL = URL;
		vi.stubGlobal('URL', class extends NativeURL {
			static override createObjectURL = createObjectUrl;
			static override revokeObjectURL = vi.fn();
		});
		const imageUrl = '/api/plugin/fxtwitter-preview/media/1234567890/image/0?noteId=note-1';
		const result = renderPreview(response(imageUrl), context(), fetcher as PluginHost['fetch']);

		await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(1));
		await waitFor(() => expect(blobReader).toHaveBeenCalledTimes(1));
		await new Promise(resolve => setTimeout(resolve, 0));
		expect(createObjectUrl).not.toHaveBeenCalled();
		expect(result.container.querySelector('[data-fx-media-grid]')).toBeNull();
	});
});
