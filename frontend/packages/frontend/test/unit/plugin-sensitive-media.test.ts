/*
 * SPDX-FileCopyrightText: Elythia-Network
 * SPDX-License-Identifier: AGPL-3.0-only
 */
import { afterEach, expect, test, vi } from 'vitest';
import { cleanup, fireEvent, render, waitFor } from '@testing-library/vue';
import { defineComponent, h } from 'vue';
import LightboxItem from '@/components/MkLightbox.item.vue';
import type { Content } from '@/components/MkLightbox.item.vue';
import { prefer } from '@/preferences.js';
import { directives } from '@/directives/index.js';
import { components } from '@/components/index.js';
import { preferState } from '../setup.unit.js';
import * as pluginAPI from '@/plugin-api.js';

const mocks = vi.hoisted(() => ({ confirm: vi.fn(async () => ({ canceled: false })) }));
vi.mock('@/os.js', () => ({ confirm: mocks.confirm }));

const original = { nsfw: prefer.s.nsfw, confirm: prefer.s.confirmWhenRevealingSensitiveMedia };
afterEach(() => {
	cleanup();
	preferState.nsfw = original.nsfw;
	preferState.confirmWhenRevealingSensitiveMedia = original.confirm;
	mocks.confirm.mockClear();
});

function renderItem(isSensitive = true, initiallyRevealed = false, content: Partial<Content> = {}) {
	return render(LightboxItem, {
		props: { content: { id: 'plugin-image', type: 'image', url: 'blob:plugin-image', width: 640, height: 480, isSensitive, ...content }, activated: true, initiallyRevealed, pixelatedZoom: false },
		global: { directives, components },
	});
}

test.each([
	[undefined, 'A red kite above a lake', 'A red kite above a lake'],
	['Drive description', 'Blob description', 'Drive description'],
	['', 'Blob description', ''],
	[null, 'Blob description', 'Blob description'],
	[undefined, undefined, null],
] as const)('real viewer alt respects DriveFile comment=%s and Blob alt=%s', (comment, alt, expected) => {
	preferState.nsfw = 'respect';
	const file: Content['file'] = comment === undefined ? undefined : {
		id: 'drive-image', createdAt: '2026-10-31T00:00:00.000Z', name: 'drive.png', type: 'image/png',
		md5: '0123456789abcdef0123456789abcdef', size: 1, isSensitive: false, blurhash: null, properties: {},
		url: 'blob:drive-image', thumbnailUrl: null, comment, folderId: null, userId: null,
	};
	const result = renderItem(false, false, { alt, file, filename: 'download.png' });
	expect(result.container.querySelector('img[src="blob:plugin-image"]')?.getAttribute('alt')).toBe(expected);
	expect(result.container.textContent).toContain('download.png');
});

test('plugin-owned sensitive media hides actual image and requires confirmation before reveal', async () => {
	preferState.nsfw = 'respect';
	preferState.confirmWhenRevealingSensitiveMedia = true;
	const result = renderItem();
	expect(result.container.querySelector('img[src="blob:plugin-image"]')).toBeNull();
	const hidden = result.container.querySelector<HTMLElement>('[data-gallery-click-action="hidden"]');
	expect(hidden).not.toBeNull();
	mocks.confirm.mockResolvedValueOnce({ canceled: true });
	await fireEvent.click(hidden!);
	expect(result.container.querySelector('img[src="blob:plugin-image"]')).toBeNull();
	await fireEvent.click(hidden!);
	await waitFor(() => expect(result.container.querySelector('img[src="blob:plugin-image"]')).not.toBeNull());
});

test.each([
	['respect', true, false, true], ['ignore', true, false, false], ['force', false, false, true], ['respect', true, true, false],
] as const)('lightbox respects %s, sensitive=%s and revealed=%s', (nsfw, sensitive, revealed, hidden) => {
	preferState.nsfw = nsfw;
	const result = renderItem(sensitive, revealed);
	expect(result.container.querySelector('[data-gallery-click-action="hidden"]') !== null).toBe(hidden);
	expect(result.container.querySelector('img[src="blob:plugin-image"]') !== null).toBe(!hidden);
});

test('the exported sensitive guard hides slot content until the viewer confirms', async () => {
	preferState.nsfw = 'respect';
	preferState.confirmWhenRevealingSensitiveMedia = true;
	expect(pluginAPI.MkSensitiveMediaGuard).toBeDefined();
	const result = render(defineComponent({
		setup: () => () => h(pluginAPI.MkSensitiveMediaGuard, { isSensitive: true, kind: 'image' }, {
			default: () => h('img', { src: 'blob:guard-image', alt: 'plugin photo' }),
		}),
	}));
	await result.findByRole('button');
	expect(result.queryByRole('img', { name: 'plugin photo' })).toBeNull();
	mocks.confirm.mockResolvedValueOnce({ canceled: true });
	await fireEvent.click(result.container.querySelector('[data-sensitive-media-reveal]')!);
	expect(result.queryByRole('img', { name: 'plugin photo' })).toBeNull();
	await fireEvent.click(result.container.querySelector('[data-sensitive-media-reveal]')!);
	await waitFor(() => expect(result.getByRole('img', { name: 'plugin photo' })).toBeTruthy());
});
