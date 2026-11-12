/*
 * SPDX-FileCopyrightText: syuilo and misskey-project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import { cleanup, fireEvent, render } from '@testing-library/vue';
import { afterEach, describe, expect, it, vi } from 'vitest';
import FxPhotoCarousel from '../../../../../plugins/fxtwitter-preview/frontend/FxPhotoCarousel.vue';

vi.mock('@/plugin-api.js', async () => {
	const { defineComponent, h } = await import('vue');
	return { MkSensitiveMediaGuard: defineComponent({ setup(_props, { slots }) { return () => h('div', slots.default?.()); } }) };
});

const images = Array.from({ length: 20 }, (_, index) => ({
	index,
	src: `data:image/svg+xml,${index}`,
	alt: `Photo ${index + 1}`,
}));

afterEach(cleanup);

describe('FxPhotoCarousel', () => {
	it('renders all original slots and starts at the first photo', () => {
		const view = render(FxPhotoCarousel, { props: { images } });

		expect(view.container.querySelectorAll('[data-photo-index]')).toHaveLength(20);
		expect(view.container.querySelector('[data-photo-position]')?.textContent).toBe('1 / 20');
		expect(view.container.querySelector('[data-photo-track]')?.getAttribute('aria-label')).toContain('20件の画像');
	});

	it('moves with previous and next controls, emits the selected original index, and respects bounds', async () => {
		const visible: number[] = [];
		const view = render(FxPhotoCarousel, { props: { images }, attrs: { onVisible: (index: number) => visible.push(index) } });
		const next = view.container.querySelector<HTMLButtonElement>('[data-photo-next]')!;
		const previous = view.container.querySelector<HTMLButtonElement>('[data-photo-previous]')!;

		expect(previous.disabled).toBe(true);
		expect(next.disabled).toBe(false);
		await fireEvent.click(next);
		await fireEvent.click(next);
		expect(view.container.querySelector('[data-photo-position]')?.textContent).toBe('3 / 20');
		expect(visible).toEqual([0, 1, 2]);

		await fireEvent.click(previous);
		expect(view.container.querySelector('[data-photo-position]')?.textContent).toBe('2 / 20');
		expect(visible.at(-1)).toBe(1);
		await fireEvent.click(previous);
		await fireEvent.click(previous);
		expect(view.container.querySelector('[data-photo-position]')?.textContent).toBe('1 / 20');
		expect(previous.disabled).toBe(true);
	});

	it('moves with arrow keys and opens the original slot index', async () => {
		const visible: number[] = [];
		const opened: number[] = [];
		const view = render(FxPhotoCarousel, { props: { images }, attrs: {
			onVisible: (index: number) => visible.push(index),
			onOpen: (index: number) => opened.push(index),
		} });
		const track = view.container.querySelector<HTMLElement>('[data-photo-track]')!;

		await fireEvent.keyDown(track, { key: 'ArrowRight' });
		await fireEvent.keyDown(track, { key: 'ArrowRight' });
		expect(view.container.querySelector('[data-photo-position]')?.textContent).toBe('3 / 20');
		expect(visible.at(-1)).toBe(2);
		await fireEvent.click(view.container.querySelector<HTMLButtonElement>('[data-photo-index="2"] button')!);
		expect(opened).toEqual([2]);
	});

	it('locks navigation and active selection while the viewer gallery is loading', async () => {
		const visible: number[] = [];
		const view = render(FxPhotoCarousel, { props: { images, viewerLoading: false }, attrs: { onVisible: (index: number) => visible.push(index) } });
		const next = view.container.querySelector<HTMLButtonElement>('[data-photo-next]')!;
		const track = view.container.querySelector<HTMLElement>('[data-photo-track]')!;
		await fireEvent.click(next);
		await view.rerender({ images, viewerLoading: true });

		expect(next.disabled).toBe(true);
		expect(view.container.querySelector<HTMLButtonElement>('[data-photo-previous]')?.disabled).toBe(true);
		Object.defineProperty(track, 'clientWidth', { configurable: true, value: 320 });
		Object.defineProperty(track, 'scrollLeft', { configurable: true, writable: true, value: 1280 });
		await fireEvent.scroll(track);
		expect(view.container.querySelector('[data-photo-position]')?.textContent).toBe('2 / 20');
		expect(visible).toEqual([0, 1]);
	});
	it('updates the active slide from horizontal scroll and keeps the selection after resize', async () => {
		const visible: number[] = [];
		const view = render(FxPhotoCarousel, { props: { images }, attrs: { onVisible: (index: number) => visible.push(index) } });
		const track = view.container.querySelector<HTMLElement>('[data-photo-track]')!;
		Object.defineProperty(track, 'clientWidth', { configurable: true, value: 320 });
		Object.defineProperty(track, 'scrollLeft', { configurable: true, writable: true, value: 1280 });
		await fireEvent.scroll(track);
		expect(view.container.querySelector('[data-photo-position]')?.textContent).toBe('5 / 20');
		expect(visible.at(-1)).toBe(4);

		Object.defineProperty(track, 'clientWidth', { configurable: true, value: 390 });
		Object.defineProperty(track, 'scrollLeft', { configurable: true, writable: true, value: 1560 });
		window.dispatchEvent(new Event('resize'));
		await fireEvent.scroll(track);
		expect(view.container.querySelector('[data-photo-position]')?.textContent).toBe('5 / 20');
	});
});

