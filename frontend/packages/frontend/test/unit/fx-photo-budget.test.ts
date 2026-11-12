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
const IMAGE_BYTES = 20 * 1024 * 1024;

type PendingFetch = { path: string; resolve: (response: Response) => void };

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
	] as const)('serializes mixed carousel and grid image requests when $largeTarget is large', async ({ largeTarget, mainCount, quoteCount }) => {
		let activeRequests = 0;
		let maxActiveRequests = 0;
		const pending: PendingFetch[] = [];
		const blob = new Blob(['test-only image'], { type: 'image/png' });
		Object.defineProperty(blob, 'size', { configurable: true, value: IMAGE_BYTES });
		const createUrl = vi.fn((_blob: Blob) => `blob:photo-budget-${Math.random()}`);
		const revokeUrl = vi.fn();
		Object.defineProperty(URL, 'createObjectURL', { configurable: true, value: createUrl });
		Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, value: revokeUrl });

		const fetcher = vi.fn((_path: string, _options?: RequestInit): Promise<Response> => new Promise(resolve => {
			activeRequests++;
			maxActiveRequests = Math.max(maxActiveRequests, activeRequests);
			pending.push({
				path: _path,
				resolve: response => { activeRequests--; resolve(response); },
			});
		}));
		initApi(vi.fn() as PluginHost['api'], fetcher as PluginHost['fetch']);
		const view = render(FxPreview, { props: { data: previewData(mainCount, quoteCount), input, context } });

		await waitFor(() => expect(pending.length).toBeGreaterThan(0));
		expect(pending).toHaveLength(1);
		expect(activeRequests).toBe(1);
		for (let completed = 0; completed < 6; completed++) {
			const request = pending.shift()!;
			request.resolve({
				ok: true,
				status: 200,
				headers: new Headers({ 'Content-Type': 'image/png', 'Content-Length': String(IMAGE_BYTES) }),
				blob: async () => blob,
			} as Response);
			if (completed < 5) {
				await waitFor(() => expect(pending.length).toBeGreaterThan(0));
				expect(pending).toHaveLength(1);
				expect(activeRequests).toBe(1);
			}
		}

		await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(6));
		expect(activeRequests).toBe(0);
		expect(maxActiveRequests).toBe(1);
		const requestPaths = fetcher.mock.calls.map(([path]) => path);
		expect(requestPaths.some(path => path.includes('/image/'))).toBe(true);
		expect(requestPaths.some(path => path.includes('/quote-image/'))).toBe(true);
		expect(view.container.querySelectorAll('[data-fx-photo-carousel]')).toHaveLength(1);
		expect(view.container.querySelectorAll('[data-fx-media-grid]')).toHaveLength(1);
	});
});
