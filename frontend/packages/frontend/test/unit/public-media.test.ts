/*
 * SPDX-FileCopyrightText: Elythia-Network
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import { afterEach, describe, expect, test, vi } from 'vitest';
import { fetchPublicMedia, parsePreparedPublicMedia } from '../../../../../plugins/fxtwitter-preview/frontend/api.js';

const filename = 'a'.repeat(32);

afterEach(() => vi.restoreAllMocks());

describe('prepared public media', () => {
	test('validates expiry, size, MIME type, and the extension-bound opaque URL', () => {
		const prepared = parsePreparedPublicMedia({
			url: `/api/plugin/fxtwitter-preview/public-media/${filename}.png`,
			expiresAt: new Date(Date.now() + 60_000).toISOString(),
			contentType: 'image/png',
			size: 12,
		}, 'image', 1024);
		expect(prepared).toEqual({
			url: `/api/plugin/fxtwitter-preview/public-media/${filename}.png`,
			expiresAt: expect.any(Number),
			contentType: 'image/png',
			size: 12,
		});
	});

	test.each([
		['expired', { expiresAt: '2000-01-01T00:00:00.000Z' }],
		['oversized', { size: 1025 }],
		['MIME mismatch', { contentType: 'image/jpeg' }],
		['extension mismatch', { url: `/api/plugin/fxtwitter-preview/public-media/${filename}.jpg` }],
		['query string', { url: `/api/plugin/fxtwitter-preview/public-media/${filename}.png?noteId=n` }],
		['absolute URL', { url: `https://example.test/api/plugin/fxtwitter-preview/public-media/${filename}.png` }],
	])('rejects %s metadata', (_label, override) => {
		const value = {
			url: `/api/plugin/fxtwitter-preview/public-media/${filename}.png`,
			expiresAt: new Date(Date.now() + 60_000).toISOString(),
			contentType: 'image/png',
			size: 12,
			...override,
		};
		expect(parsePreparedPublicMedia(value, 'image', 1024)).toBeNull();
	});

	test('fetches only the validated same-origin path without credentials, redirects, or authorization', async () => {
		const fetch = vi.spyOn(window, 'fetch').mockResolvedValue(new Response('image'));
		const signal = new AbortController().signal;
		const path = `/api/plugin/fxtwitter-preview/public-media/${filename}.png`;
		await fetchPublicMedia(path, signal);
		expect(fetch).toHaveBeenCalledTimes(1);
		expect(fetch).toHaveBeenCalledWith(path, {
			method: 'GET',
			mode: 'same-origin',
			credentials: 'omit',
			redirect: 'error',
			cache: 'default',
			signal,
		});
	});

	test.each([
		`https://example.test/api/plugin/fxtwitter-preview/public-media/${filename}.png`,
		`//example.test/api/plugin/fxtwitter-preview/public-media/${filename}.png`,
		`/api/plugin/fxtwitter-preview/public-media/${filename}.png?noteId=n`,
		`/api/plugin/fxtwitter-preview/public-media/${filename}.png#fragment`,
		`/api/plugin/fxtwitter-preview/public-media/${filename.toUpperCase()}.png`,
	])('does not request an invalid public URL: %s', async url => {
		const fetch = vi.spyOn(window, 'fetch');
		await expect(fetchPublicMedia(url, new AbortController().signal)).rejects.toThrow();
		expect(fetch).not.toHaveBeenCalled();
	});
});
