/*
 * SPDX-FileCopyrightText: syuilo and misskey-project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import { cleanup, render, waitFor } from '@testing-library/vue';
import { afterEach, describe, expect, it, vi } from 'vitest';
import FxPreview from '../../../../../plugins/fxtwitter-preview/frontend/FxPreview.vue';
import { initApi } from '../../../../../plugins/fxtwitter-preview/frontend/api';
import type { PreviewResponse } from '../../../../../plugins/fxtwitter-preview/frontend/api';
import type { PluginHost, PreviewContext, PreviewInput } from '@/plugin-api.js';

vi.mock('@/server-plugins.generated.js', () => ({ serverPlugins: [] }));

const input = { kind: 'url', href: 'https://x.com/alice/status/1234567890' } satisfies PreviewInput;
const context: PreviewContext = { detail: true, revealed: true, active: true, inViewport: true, source: { kind: 'note', noteId: 'note-1' } };
const IMAGE_BYTES = 1024 * 1024;

function mediaUrl(kind: 'image' | 'quote-image', index: number): string {
	return `/api/plugin/fxtwitter-preview/media/1234567890/${kind}/${index}?noteId=note-1`;
}

function previewData(mainCount: number, quoteCount: number): PreviewResponse {
	return {
		state: 'ready',
		post: {
			id: '1234567890', url: input.href, text: 'main fixture',
			author: { name: 'Alice', screenName: 'alice' },
			likes: 1, reposts: 0, quotes: 0, replies: 0, hasVideo: false, isSensitive: false,
			images: Array.from({ length: mainCount }, (_, index) => ({ url: mediaUrl('image', index), alt: `main ${index + 1}` })),
			quote: {
				id: '2234567890', url: 'https://x.com/bob/status/2234567890', text: 'quote fixture',
				author: { name: 'Bob', screenName: 'bob' }, hasVideo: false,
				images: Array.from({ length: quoteCount }, (_, index) => ({ url: mediaUrl('quote-image', index), alt: `quote ${index + 1}` })),
			},
		},
	};
}

afterEach(() => {
	cleanup();
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

describe('FxPreview shared photo request queue', () => {
	it.each([
		{ largeTarget: 'main', mainCount: 5, quoteCount: 4 },
		{ largeTarget: 'quote', mainCount: 4, quoteCount: 5 },
	] as const)('serializes mixed photo-grid requests when $largeTarget is large', async ({ largeTarget, mainCount, quoteCount }) => {
		let activeRequests = 0;
		let maxActiveRequests = 0;
		let descriptorId = 0;
		const blob = new Blob(['test-only image'], { type: 'image/png' });
		Object.defineProperty(blob, 'size', { configurable: true, value: IMAGE_BYTES });
		const createUrl = vi.fn((_blob: Blob) => `blob:photo-budget-${Math.random()}`);
		const revokeUrl = vi.fn();
		Object.defineProperty(URL, 'createObjectURL', { configurable: true, value: createUrl });
		Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, value: revokeUrl });

		const fetcher = vi.fn((_path: string, _options?: RequestInit) => {
			const url = `/api/plugin/fxtwitter-preview/public-media/${'a'.repeat(31)}${++descriptorId}.png`;
			return Promise.resolve(new Response(JSON.stringify({
				url,
				expiresAt: new Date(Date.now() + 60_000).toISOString(),
				contentType: 'image/png',
				size: IMAGE_BYTES,
			}), { headers: { 'Content-Type': 'application/json' } }));
		});
		const publicFetcher = vi.spyOn(window, 'fetch').mockImplementation(async (input: RequestInfo | URL): Promise<Response> => {
			activeRequests++;
			maxActiveRequests = Math.max(maxActiveRequests, activeRequests);
			await new Promise(resolve => setTimeout(resolve, 0));
			activeRequests--;
			return {
				ok: true,
				status: 200,
				headers: new Headers({ 'Content-Type': 'image/png', 'Content-Length': String(IMAGE_BYTES) }),
				blob: async () => blob,
			} as Response;
		});
		initApi(vi.fn() as PluginHost['api'], fetcher as PluginHost['fetch']);
		const view = render(FxPreview, { props: { data: previewData(mainCount, quoteCount), input, context } });

		const expectedRequests = mainCount + quoteCount;
		await waitFor(() => expect(publicFetcher).toHaveBeenCalledTimes(expectedRequests));
		await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(expectedRequests));
		await waitFor(() => expect(activeRequests).toBe(0));
		expect(activeRequests).toBe(0);
		expect(maxActiveRequests).toBe(1);
		const requestPaths = fetcher.mock.calls.map(([path]) => path);
		expect(requestPaths.every(path => path === '/api/plugin/fxtwitter-preview/media/prepare')).toBe(true);
		const preparedKinds = fetcher.mock.calls.map(([, options]) => JSON.parse(String(options?.body)).kind);
		expect(preparedKinds.some(kind => kind === 'image')).toBe(true);
		expect(preparedKinds.some(kind => kind === 'quote-image')).toBe(true);
		expect(publicFetcher.mock.calls.every(([path]) => String(path).includes('/public-media/'))).toBe(true);
		expect(view.container.querySelectorAll('[data-fx-photo-carousel]')).toHaveLength(0);
		expect(view.container.querySelectorAll('[data-fx-media-grid]').length).toBeGreaterThan(0);
	});
});
