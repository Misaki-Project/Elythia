/*
 * SPDX-FileCopyrightText: Elythia-Network
 * SPDX-License-Identifier: AGPL-3.0-only
 */
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { launchServerPlugins } from '@/plugin-api.js';
import type { PluginHost } from '@/plugin-api.js';

const mocks = vi.hoisted(() => ({
	account: null as { id: string; username: string; host: null; token: string } | null,
	popup: vi.fn(async (_component: unknown, _props: unknown, _events: unknown) => ({ dispose: vi.fn() })),
}));
vi.mock('@/i.js', () => ({ get $i() { return mocks.account; }, iAmModerator: false, iAmAdmin: false }));
vi.mock('@/os.js', () => ({ popupAsyncWithDialog: mocks.popup }));

beforeEach(() => { mocks.account = { id: 'viewer', username: 'viewer', host: null, token: 'test-session' }; });

async function host(): Promise<PluginHost> {
	let result!: PluginHost;
	await launchServerPlugins([{ name: 'example', setup: value => { result = value; } }]);
	return result;
}

afterEach(() => {
	vi.restoreAllMocks();
	mocks.account = { id: 'viewer', username: 'viewer', host: null, token: 'test-session' };
	mocks.popup.mockClear();
});

describe('plugin Blob fetch', () => {
	test('uses the host session, preserves cancellation and refuses redirects', async () => {
		const response = new Response(new Blob(['image'], { type: 'image/png' }));
		const fetch = vi.spyOn(window, 'fetch').mockResolvedValue(response);
		const signal = new AbortController().signal;
		const api = await host();
		expect(await api.fetch('/api/plugin/example/media/1?size=large', { signal, headers: { Authorization: 'Bearer forged', Accept: 'image/png' }, credentials: 'include', redirect: 'follow' })).toBe(response);
		const [url, options] = fetch.mock.calls[0];
		expect(url).toBe('/api/plugin/example/media/1?size=large');
		expect(options?.signal).toBe(signal);
		expect(options?.credentials).toBe('omit');
		expect(options?.redirect).toBe('error');
		expect(new Headers(options?.headers).get('Authorization')).toBe('Bearer test-session');
		expect(new Headers(options?.headers).get('Accept')).toBe('image/png');
	});

	test.each([
		'https://other.example/api/plugin/example/media', '//other.example/api/plugin/example/media',
		'/api/plugin/another/media', '/api/plugin/example-other/media', '/api/plugin/example/../another/media',
		'/api/plugin/example/%2e%2e/another/media', '/api/plugin/example/media%2f..%2f..%2fanother',
		'/api/plugin/example/media%5c..%5c..%5canother', '/api/plugin/example/media#fragment',
		'/api/plugin/example/%252e%252e/another', '/api/plugin/example/media?i=forged',
	])('rejects paths outside the strict plugin namespace: %s', async path => {
		const fetch = vi.spyOn(window, 'fetch').mockResolvedValue(new Response());
		const api = await host();
		await expect(async () => api.fetch(path)).rejects.toThrow();
		expect(fetch).not.toHaveBeenCalled();
	});

	test('fails before network access without an authenticated session', async () => {
		mocks.account = null;
		const fetch = vi.spyOn(window, 'fetch').mockResolvedValue(new Response());
		const api = await host();
		await expect(async () => api.fetch('/api/plugin/example/media')).rejects.toThrow();
		expect(fetch).not.toHaveBeenCalled();
	});
});

describe('plugin media viewer', () => {
	test('passes explicit sensitivity and selection to the lightbox and revokes URLs on close', async () => {
		const create = vi.spyOn(URL, 'createObjectURL').mockReturnValueOnce('blob:first').mockReturnValueOnce('blob:second');
		const revoke = vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {});
		const api = await host();
		await api.openMedia([
			{ id: 'first', type: 'image', blob: new Blob(['a'], { type: 'image/png' }), isSensitive: true },
			{ id: 'second', type: 'image', blob: new Blob(['b'], { type: 'image/jpeg' }), initiallyRevealed: true },
		], 'second');
		expect(create).toHaveBeenCalledTimes(2);
		const [component, props, events] = mocks.popup.mock.calls[0];
		await component;
		expect(props).toMatchObject({ defaultIndex: 1, contents: [{ id: 'first', isSensitive: true }, { id: 'second', isSensitive: false }], initiallyRevealedContentIds: ['second'] });
		expect(revoke).not.toHaveBeenCalled();
		(events as { closed(): void }).closed();
		(events as { closed(): void }).closed();
		expect(revoke.mock.calls).toEqual([['blob:first'], ['blob:second']]);
	});

	test.each(['text/html', 'image/svg+xml', 'image/not-a-real-format', 'video/mp4'])('rejects unsafe or mismatched image MIME %s before allocating URLs', async mime => {
		const create = vi.spyOn(URL, 'createObjectURL');
		const api = await host();
		await expect(api.openMedia([{ id: 'bad', type: 'image', blob: new Blob(['x'], { type: mime }) }])).rejects.toThrow();
		expect(create).not.toHaveBeenCalled();
		expect(mocks.popup).not.toHaveBeenCalled();
	});

	test('cleans up allocated URLs when opening the viewer fails', async () => {
		vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:temporary');
		const revoke = vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {});
		mocks.popup.mockRejectedValueOnce(new Error('viewer failed'));
		const api = await host();
		await expect(api.openMedia([{ id: 'one', type: 'image', blob: new Blob(['x'], { type: 'image/png' }) }])).rejects.toThrow('viewer failed');
		expect(revoke).toHaveBeenCalledWith('blob:temporary');
	});
});
