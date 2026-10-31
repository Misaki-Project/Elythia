/*
 * SPDX-FileCopyrightText: Elythia-Network
 * SPDX-License-Identifier: AGPL-3.0-only
 */
import { afterEach, expect, test, vi } from 'vitest';
import { cleanup, fireEvent, render, waitFor } from '@testing-library/vue';
import { defineComponent, h } from 'vue';
import LightboxItem from '@/components/MkLightbox.item.vue';
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

function renderItem(isSensitive = true, initiallyRevealed = false) {
	return render(LightboxItem, {
		props: { content: { id: 'plugin-image', type: 'image', url: 'blob:plugin-image', width: 640, height: 480, isSensitive }, activated: true, initiallyRevealed, pixelatedZoom: false },
		global: { directives, components },
	});
}

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
