/*
 * SPDX-FileCopyrightText: syuilo and misskey-project
 * SPDX-License-Identifier: AGPL-3.0-only
 */
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { cleanup, fireEvent, render, waitFor } from '@testing-library/vue';
import { defineComponent, nextTick, ref } from 'vue';
import { preferReactive, preferState } from '../../../frontend/packages/frontend/test/setup.unit.js';
import AdminPage from './AdminPage.vue';
import plugin, { FxPreview, initApi, loadPreview, provider, statusID } from './index.js';
import type { PreviewResponse, PublicPost } from './api.js';
import type { PluginHost, PreviewContext, PreviewInput, PreviewProvider } from '@/plugin-api.js';
import { components } from '@/components/index.js';
import { directives } from '@/directives/index.js';
import MkPluginUrlPreview from '@/components/MkPluginUrlPreview.vue';
import MkCwButton from '@/components/MkCwButton.vue';
import { _resetPreviewsForTest, launchServerPlugins } from '@/plugin-api.js';

vi.mock('@/server-plugins.generated.js', () => ({ serverPlugins: [] }));
const input = { kind: 'url', href: 'https://x.com/alice/status/1234567890' } satisfies PreviewInput;
const context: PreviewContext = { detail: true, revealed: true, active: true, inViewport: true, source: { kind: 'note', noteId: 'note-1' } };
const globals = { components, directives };
let api: ReturnType<typeof vi.fn>;
let media: ReturnType<typeof vi.fn>;
let viewer = vi.fn<PluginHost['openMedia']>();
let intersect: (visible: boolean) => void;
let allowFrameError = false;
const dispatchFrameEvent = HTMLIFrameElement.prototype.dispatchEvent;

function data(): PreviewResponse & { post: PublicPost } {
	return { state: 'ready', post: { id: '1234567890', url: input.href, text: 'public body', author: { name: 'Alice', screenName: 'alice' }, likes: 1, reposts: 2, quotes: 3, replies: 4, images: [], hasVideo: true, video: { provider: 'x', embedToken: '0123456789abcdef0123456789abcdef' } } };
}

function fixtureHost(me: PluginHost['me'] = { id: 'u1', username: 'u1', host: null }): PluginHost {
	// The public PluginHost contract names this authenticated resource method fetch.
	// eslint-disable-next-line id-denylist
	return { name: 'fxtwitter-preview', me, api: api as PluginHost['api'], fetch: media as PluginHost['fetch'], openMedia: viewer, slot() {}, preview: { register() {} } };
}

async function register() {
	await plugin.setup({ ...fixtureHost(), preview: { register(value) { providerToRegister = value; } } });
	await launchServerPlugins([{ name: 'fx-test', setup(host) { host.preview.register(must(providerToRegister)); } }]);
}

let providerToRegister: PreviewProvider | undefined;

function coordinate(props: Record<string, unknown> = {}) {
	return render(MkPluginUrlPreview, { props: { url: input.href, detail: true, revealed: true, source: context.source, ...props }, global: globals });
}

function card(response: PreviewResponse = data(), ctx = context) {
	return render(FxPreview, { props: { input, data: response, context: ctx }, global: globals });
}

function retryCard(response: PreviewResponse = { state: 'pending' }) {
	return render(provider.component as typeof FxPreview, { props: { input, data: response, context }, global: globals });
}

async function flushPreview(): Promise<void> {
	for (let step = 0; step < 12; step++) await nextTick();
}

function previewCalls() {
	return api.mock.calls.filter(call => call[0].endsWith('/preview'));
}

function retryButton(view: ReturnType<typeof retryCard>): HTMLButtonElement {
	const button = view.container.querySelector<HTMLButtonElement>('[data-fx-preview-retry]');
	expect(button).not.toBeNull();
	return must(button);
}

function mixedData() {
	const response = data();
	response.post.images = [{ url: '/api/plugin/fxtwitter-preview/media/1234567890/image/0?noteId=note-1', alt: 'main photo' }];
	response.post.quote = { id: '9876543210', url: 'https://x.com/bob/status/9876543210', text: 'quoted body', author: { name: 'Bob', screenName: 'bob' }, images: [{ url: '/api/plugin/fxtwitter-preview/media/1234567890/quote-image/0?noteId=note-1', alt: 'quote photo' }], hasVideo: true, video: { provider: 'x', embedToken: '0123456789abcdef0123456789abcdef' } };
	return response;
}

function must<T>(value: T | null | undefined): T {
	if (value == null) throw new Error('Required fixture element is missing');
	return value;
}

function fullEmbed(params: Record<string, string>) {
	return 'https://platform.twitter.com/embed/index.html?' + new URLSearchParams({ id: '1234567890', embedId: params.nonce, theme: 'light', lang: 'ja', hideCard: 'false', hideThread: 'false', dnt: 'true' });
}

beforeEach(() => {
	// happy-dom keeps remote iframe loading disabled. Its synthetic error is
	// suppressed so DOM lifetime tests can inspect the frame; no remote fetch
	// is enabled. Real error events are explicitly dispatched via the native method.
	allowFrameError = false;
	vi.spyOn(HTMLIFrameElement.prototype, 'dispatchEvent').mockImplementation(function (this: HTMLIFrameElement, event) { return event.type === 'error' && !allowFrameError ? true : dispatchFrameEvent.call(this, event); });
	const reportError = console.error;
	vi.spyOn(console, 'error').mockImplementation((...args) => { if (!String(args[0]).includes('Iframe page loading is disabled')) reportError(...args); });
	api = vi.fn(async (path: string, params: Record<string, string>) => {
		if (path.endsWith('/capabilities')) return { externalFetchEnabled: true };
		if (path.endsWith('/preview')) return data();
		if (path.endsWith('/embed/shell')) return { url: fullEmbed(params) };
		throw new Error(`unexpected ${path}`);
	});
	media = vi.fn(async () => new Response(new Blob(['png'], { type: 'image/png' }), { headers: { 'Content-Type': 'image/png' } }));
	viewer = vi.fn(async () => undefined);
	initApi(api as PluginHost['api'], media as PluginHost['fetch'], viewer);
	Object.defineProperty(URL, 'createObjectURL', { configurable: true, value: vi.fn(() => `blob:fx-${crypto.randomUUID()}`) });
	Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, value: vi.fn() });
	Object.defineProperty(window.document, 'visibilityState', { configurable: true, value: 'visible' });
	vi.stubGlobal('IntersectionObserver', class {
		constructor(callback: IntersectionObserverCallback) { intersect = visible => callback([{ isIntersecting: visible } as IntersectionObserverEntry], this as unknown as IntersectionObserver); }
		observe() { intersect(true); }
		disconnect() {}
	});
	preferState.nsfw = 'respect';
	preferState.confirmWhenRevealingSensitiveMedia = false;
	// The host test helper's Record type is wider than its lazily created runtime entries.
	const nsfwPreference = Object.entries(preferReactive).find(([key]) => key === 'nsfw')?.[1];
	if (nsfwPreference != null) nsfwPreference.value = 'respect';
});
afterEach(() => { cleanup(); vi.useRealTimers(); _resetPreviewsForTest(); vi.restoreAllMocks(); vi.unstubAllGlobals(); fetchMock.resetMocks(); });

test('no-CW timeline through the current coordinator never requests Fx', async () => {
	await register();
	coordinate({ detail: false });
	await nextTick(); await nextTick();
	expect(api.mock.calls.filter(call => call[0].endsWith('/preview'))).toHaveLength(0);
	expect(media).not.toHaveBeenCalled();
});

test('provider contract requires detail and has no timeline presentation options', () => {
	expect(provider.requiresDetail).toBe(true);
	expect(provider).not.toHaveProperty('preferenceKey');
	expect(provider).not.toHaveProperty('presentation');
});
test.each([{ detail: false }, { revealed: false }, { active: false }, { inViewport: false }, { source: null }])('direct loader refuses gated context %j', async patch => {
	await expect(loadPreview(input, { ...context, ...patch }, new AbortController().signal)).rejects.toThrow();
	expect(api).not.toHaveBeenCalled();
});
test('already aborted and abort-ignoring late responses cannot escape the loader', async () => {
	const aborted = new AbortController(); aborted.abort();
	await expect(loadPreview(input, context, aborted.signal)).rejects.toMatchObject({ name: 'AbortError' });
	let finish!: (value: PreviewResponse) => void;
	api.mockImplementation(() => new Promise(resolve => { finish = resolve; }));
	const controller = new AbortController();
	const pending = loadPreview(input, context, controller.signal);
	controller.abort(); finish(data());
	await expect(pending).rejects.toMatchObject({ name: 'AbortError' });
});

test('eight pending polls settle with an explicit bounded retry that can become ready', async () => {
	vi.useFakeTimers();
	api.mockImplementation(async path => path.endsWith('/capabilities') ? { externalFetchEnabled: true } : { state: 'pending', post: data().post, body: 'audit body' });
	await register(); const view = coordinate(); await flushPreview();
	await vi.advanceTimersByTimeAsync(7 * 750); await flushPreview();
	expect(previewCalls()).toHaveLength(8);
	expect(retryButton(view).disabled).toBe(false);
	expect(view.container.textContent).not.toContain('public body');
	expect(view.container.textContent).not.toContain('audit body');
	await vi.advanceTimersByTimeAsync(60_000); await flushPreview();
	expect(previewCalls()).toHaveLength(8);
	api.mockResolvedValue(data());
	await fireEvent.click(retryButton(view)); await flushPreview();
	expect(previewCalls()).toHaveLength(9);
	expect(view.getByText('public body')).toBeTruthy();
	expect(view.container.querySelector('[data-fx-preview-retry]')).toBeNull();
});

test('poll batches honor valid server nextPollAt and retain the minimum delay', async () => {
	vi.useFakeTimers();
	api.mockResolvedValueOnce({ state: 'pending', nextPollAt: new Date(Date.now() + 2_000).toISOString() }).mockResolvedValueOnce({ state: 'pending', nextPollAt: 'invalid' }).mockResolvedValue(data());
	const pending = loadPreview(input, context, new AbortController().signal); await flushPreview();
	await vi.advanceTimersByTimeAsync(1_999); expect(previewCalls()).toHaveLength(1);
	await vi.advanceTimersByTimeAsync(1); expect(previewCalls()).toHaveLength(2);
	await vi.advanceTimersByTimeAsync(749); expect(previewCalls()).toHaveLength(2);
	await vi.advanceTimersByTimeAsync(1); await expect(pending).resolves.toMatchObject({ state: 'ready' });
});

test('retry is single flight and a rejected batch stays retryable after cooldown', async () => {
	vi.useFakeTimers();
	let reject!: (reason: Error) => void;
	api.mockImplementationOnce(() => new Promise((_resolve, fail) => { reject = fail; }));
	const view = retryCard();
	const button = retryButton(view);
	button.click(); button.click(); button.click(); await flushPreview();
	expect(previewCalls()).toHaveLength(1); expect(retryButton(view).disabled).toBe(true);
	reject(new Error('temporary failure')); await flushPreview();
	expect(view.container.querySelector('[data-fx-preview-error]')).toBeTruthy();
	expect(retryButton(view).disabled).toBe(true);
	for (let click = 0; click < 10; click++) button.click();
	await vi.advanceTimersByTimeAsync(999); expect(previewCalls()).toHaveLength(1); expect(retryButton(view).disabled).toBe(true);
	await vi.advanceTimersByTimeAsync(1); expect(retryButton(view).disabled).toBe(false);
	api.mockResolvedValue(data()); await fireEvent.click(retryButton(view)); await flushPreview();
	expect(view.getByText('public body')).toBeTruthy(); expect(previewCalls()).toHaveLength(2);
});

test('a manually retried pending batch also stops at eight without background polling', async () => {
	vi.useFakeTimers(); api.mockResolvedValue({ state: 'pending' });
	const view = retryCard(); await fireEvent.click(retryButton(view)); await flushPreview();
	await vi.advanceTimersByTimeAsync(7 * 750); await flushPreview();
	expect(previewCalls()).toHaveLength(8); expect(retryButton(view).disabled).toBe(true);
	await vi.advanceTimersByTimeAsync(60_000); expect(previewCalls()).toHaveLength(8); expect(retryButton(view).disabled).toBe(false);
});

test.each(['unmount', 'cw', 'inactive', 'viewport', 'detail', 'source', 'input', 'session', 'parent'] as const)('retry aborts and rejects late publication on %s change', async change => {
	let finish!: (value: PreviewResponse) => void;
	api.mockImplementation(() => new Promise(resolve => { finish = resolve; }));
	const view = retryCard(); await fireEvent.click(retryButton(view)); await flushPreview();
	const signal = previewCalls()[0][2].signal;
	if (change === 'unmount') view.unmount();
	else if (change === 'session') { initApi(api as PluginHost['api'], media as PluginHost['fetch'], viewer); await nextTick(); } else if (change === 'input') await view.rerender({ input: { kind: 'url', href: 'https://x.com/bob/status/9876543210' } });
	else if (change === 'parent') await view.rerender({ data: { state: 'disabled' } });
	else await view.rerender({ context: { ...context, ...(change === 'cw' ? { revealed: false } : change === 'inactive' ? { active: false } : change === 'viewport' ? { inViewport: false } : change === 'detail' ? { detail: false } : { source: { kind: 'note', noteId: 'note-2' } }) } });
	expect(signal.aborted).toBe(true);
	finish(data()); await flushPreview();
	expect(view.queryByText('public body')).toBeNull(); expect(media).not.toHaveBeenCalled();
	if (change === 'cw' || change === 'inactive' || change === 'viewport' || change === 'detail') {
		expect(retryButton(view).disabled).toBe(true);
		await view.rerender({ context }); expect(retryButton(view).disabled).toBe(false);
		expect(previewCalls()).toHaveLength(1);
		api.mockResolvedValue(data()); await fireEvent.click(retryButton(view)); await flushPreview();
		expect(view.getByText('public body')).toBeTruthy(); expect(previewCalls()).toHaveLength(2);
	}
});

test('retry overrides are discarded on note ownership change and use only the new source', async () => {
	api.mockResolvedValue(data()); const view = retryCard();
	await fireEvent.click(retryButton(view)); await flushPreview(); expect(view.getByText('public body')).toBeTruthy();
	await view.rerender({ context: { ...context, source: { kind: 'note', noteId: 'note-2' } } });
	expect(view.queryByText('public body')).toBeNull();
	api.mockResolvedValue({ state: 'pending', post: data().post });
	await fireEvent.click(retryButton(view)); await flushPreview();
	expect(previewCalls()[1][1]).toEqual({ noteId: 'note-2', postId: '1234567890' });
	expect(view.queryByText('public body')).toBeNull(); view.unmount();
});

test.each(['success', 'failure'] as const)('main and quote photos coexist with their videos after image %s', async result => {
	if (result === 'failure') media.mockRejectedValue(new Error('image failed'));
	const view = card(mixedData()); await flushPreview();
	if (result === 'success') {
		expect(view.container.querySelector('img[alt="main photo"]')).toBeTruthy();
		expect(view.container.querySelector('img[alt="quote photo"]')).toBeTruthy();
	}
	expect(view.container.querySelector('[data-fx-video-target="main"] [data-fx-video-play]')).toBeTruthy();
	expect(view.container.querySelector('[data-fx-video-target="quote"] [data-fx-video-play]')).toBeTruthy();
	expect(view.container.querySelector('a[href="https://x.com/i/status/1234567890"]')).toBeTruthy();
	expect(view.container.querySelector('a[href="https://x.com/i/status/9876543210"]')).toBeTruthy();
	expect(view.container.querySelector('iframe')).toBeNull(); expect(api).not.toHaveBeenCalled();
});

test('a newly mounted pending card respects the server retry time without issuing work', async () => {
	vi.useFakeTimers(); const view = retryCard({ state: 'pending', nextPollAt: new Date(Date.now() + 2_000).toISOString() });
	expect(retryButton(view).disabled).toBe(true);
	await fireEvent.click(retryButton(view)); expect(previewCalls()).toHaveLength(0);
	await vi.advanceTimersByTimeAsync(1_999); expect(retryButton(view).disabled).toBe(true);
	await vi.advanceTimersByTimeAsync(1); expect(retryButton(view).disabled).toBe(false); expect(previewCalls()).toHaveLength(0);
});

test('a session change during an initial loader request rejects its late public body', async () => {
	let finish!: (value: PreviewResponse) => void;
	api.mockImplementation(() => new Promise(resolve => { finish = resolve; }));
	const pending = loadPreview(input, context, new AbortController().signal);
	initApi(api as PluginHost['api'], media as PluginHost['fetch'], viewer); finish(data());
	await expect(pending).rejects.toMatchObject({ name: 'AbortError' });
});

test('image completion cannot replace a mounted main or quote full-post iframe', async () => {
	const finish: Array<(response: Response) => void> = [];
	media.mockImplementation(() => new Promise(resolve => { finish.push(resolve); }));
	api.mockImplementation(async (_path, params) => ({ url: fullEmbed(params).replace('id=1234567890', params.target === 'quote' ? 'id=9876543210' : 'id=1234567890') }));
	const view = card(mixedData()); await flushPreview();
	await waitFor(() => expect(view.container.querySelectorAll('[data-fx-video-play]')).toHaveLength(2));
	for (const button of view.container.querySelectorAll('[data-fx-video-play]')) await fireEvent.click(button);
	await waitFor(() => expect(view.container.querySelectorAll('iframe')).toHaveLength(2));
	const frames = Array.from(view.container.querySelectorAll('iframe'));
	for (const resolve of finish) resolve(new Response(new Blob(['png'], { type: 'image/png' }), { headers: { 'Content-Type': 'image/png' } }));
	await flushPreview(); expect(Array.from(view.container.querySelectorAll('iframe'))).toEqual(frames);
	expect(view.container.querySelector('img[alt="main photo"]')).toBeTruthy(); expect(view.container.querySelector('img[alt="quote photo"]')).toBeTruthy();
});
test('unrevealed detail CW opens, collapses, aborts, and rejects its late result', async () => {
	let finish!: (value: PreviewResponse) => void;
	api.mockImplementation(async path => path.endsWith('/capabilities') ? { externalFetchEnabled: true } : new Promise(resolve => { finish = resolve; }));
	await register();
	const view = coordinate({ revealed: false });
	await nextTick(); expect(api).toHaveBeenCalledTimes(1);
	await view.rerender({ revealed: true });
	await waitFor(() => expect(api).toHaveBeenCalledTimes(2));
	const signal = api.mock.calls[1][2].signal;
	await view.rerender({ revealed: false });
	expect(signal.aborted).toBe(true);
	finish(data()); await nextTick(); await nextTick();
	expect(view.queryByText('public body')).toBeNull(); expect(media).not.toHaveBeenCalled();
});
test.each([null, false])('anonymous or disabled setup does no preview/media work: %s', async mode => {
	if (mode === false) api.mockResolvedValue({ externalFetchEnabled: false });
	const host = fixtureHost(mode === null ? null : undefined);
	const registered = vi.fn(); await plugin.setup({ ...host, preview: { register: registered } });
	expect(registered.mock.calls).toHaveLength(0); expect(media).not.toHaveBeenCalled();
	expect(api.mock.calls.every(call => call[0].endsWith('/capabilities'))).toBe(true);
});
test('full-post iframe is click-only and is removed on CW collapse', async () => {
	const view = card();
	await nextTick();
	expect(view.container.querySelector('iframe,script,link[rel="preconnect"]')).toBeNull(); expect(api).not.toHaveBeenCalled();
	await waitFor(() => expect(view.container.querySelector('[data-fx-video-play]')).toBeTruthy());
	expect(api).not.toHaveBeenCalled();
	expect(view.container.querySelector('iframe,script,link[rel="preconnect"]')).toBeNull();
	await fireEvent.click(must(view.container.querySelector('[data-fx-video-play]')));
	await waitFor(() => expect(view.container.querySelector('iframe')?.src).toContain('/embed/index.html?'));
	expect(view.container.querySelector('iframe')?.getAttribute('sandbox')).toContain('allow-scripts');
	await view.rerender({ context: { ...context, revealed: false } });
	expect(view.container.querySelector('iframe')).toBeNull();
});
test('timeline cannot start an embed even if mounted directly', async () => {
	const view = card(data(), { ...context, detail: false });
	await nextTick();
	const play = view.container.querySelector('[data-fx-video-play]');
	if (play) await fireEvent.click(play);
	expect(api).not.toHaveBeenCalled();
});

test.each(['deleted', 'suspended', 'private', 'blocked', 'unavailable', 'unknown', 'fetch_failed'])('backend unavailable reason %s survives normalization without public audit fallback', async reason => {
	api.mockResolvedValue({ state: 'unavailable', unavailableReason: reason, body: 'private audit text', post: data().post });
	const response = await loadPreview(input, context, new AbortController().signal);
	expect(response).toEqual({ state: 'unavailable', unavailableReason: reason });
	const view = card(response);
	expect(view.container.textContent).not.toContain('private audit text');
	expect(view.container.querySelector('[data-fx-unavailable-reason]')?.getAttribute('data-fx-unavailable-reason')).toBe(reason);
});
test.each(['deleted', 'unknown'])('quoted unavailable %s stays separate from the outer post', async reason => {
	const response = data(); Object.assign(response.post, { quoteUnavailableReason: reason });
	api.mockResolvedValue(response);
	const normalized = await loadPreview(input, context, new AbortController().signal);
	expect(normalized.post).toHaveProperty('quoteUnavailableReason', reason);
	const view = card(normalized);
	expect(view.getByText('public body')).toBeTruthy();
	expect(view.container.querySelector('[data-fx-quote-unavailable]')?.getAttribute('data-fx-quote-unavailable')).toBe(reason);
});
test.each([
	'https://platform.twitter.com/embed/Tweet.html',
	'https://platform.twitter.com/embed/index.html/extra',
	'https://platform.twitter.com.evil.test/embed/index.html',
	'https://www.youtube.com/embed/abcdefghijk',
])('forged complete-post embed rejected: %s', async base => {
	api.mockImplementation(async (_path, params) => ({ url: fullEmbed(params).replace('https://platform.twitter.com/embed/index.html', base) }));
	const view = card(); await nextTick();
	await waitFor(() => expect(view.container.querySelector('[data-fx-video-play]')).toBeTruthy());
	await fireEvent.click(must(view.container.querySelector('[data-fx-video-play]')));
	await waitFor(() => expect(view.container.textContent).toContain('埋め込みを表示できませんでした'));
	expect(view.container.querySelector('iframe')).toBeNull();
	expect(view.container.querySelector(`a[href="${input.href}"]`)).toBeTruthy();
});
test('media is revoked and late media cannot revive after source-note switch', async () => {
	let finish!: (value: Response) => void;
	media.mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }));
	const response = data(); response.post.images = [{ url: '/api/plugin/fxtwitter-preview/media/1234567890/image/0?noteId=note-1', alt: 'accurate alt' }];
	const view = card(response);
	await waitFor(() => expect(media).toHaveBeenCalledTimes(1));
	const signal = media.mock.calls[0][1].signal;
	await view.rerender({ context: { ...context, source: null } });
	expect(signal.aborted).toBe(true);
	finish(new Response(new Blob(['png'], { type: 'image/png' }), { headers: { 'Content-Type': 'image/png' } }));
	await nextTick(); await nextTick(); await nextTick();
	expect(view.container.querySelector('img')).toBeNull();
	expect(URL.createObjectURL).not.toHaveBeenCalled();
});
test.each(['image/svg+xml', 'text/html', 'application/octet-stream'])('unsafe Blob MIME %s never reaches an image or viewer', async mime => {
	media.mockResolvedValue(new Response(new Blob(['unsafe'], { type: mime }), { headers: { 'Content-Type': mime } }));
	const response = data(); response.post.images = [{ url: '/api/plugin/fxtwitter-preview/media/1234567890/image/0?noteId=note-1' }];
	const view = card(response); await nextTick(); await nextTick(); await nextTick();
	expect(URL.createObjectURL).not.toHaveBeenCalled();
	expect(view.container.querySelector('img')).toBeNull(); expect(viewer).not.toHaveBeenCalled();
});

test('actual CW button drives the current coordinator with the note-detail binding', async () => {
	await register();
	const showContent = ref(false);
	const note = { id: 'note-1', cw: 'warning', text: input.href };
	// Independent test wrappers exercise the real CW button and coordinator without adding production components.
	// eslint-disable-next-line vue/one-component-per-file
	const Wrapper = defineComponent({
		components: { MkCwButton, MkPluginUrlPreview },
		setup: () => ({ showContent, note, url: input.href }),
		template: '<MkCwButton v-model="showContent" :text="note.text"/><div v-show="note.cw == null || showContent"><MkPluginUrlPreview :url="url" :detail="true" :revealed="note.cw == null || showContent" :source="{ kind: \'note\', noteId: note.id }"/></div>',
	});
	const view = render(Wrapper, { global: globals });
	await nextTick(); expect(api).toHaveBeenCalledTimes(1);
	await fireEvent.click(view.getByRole('button'));
	await waitFor(() => expect(view.getByText('public body')).toBeTruthy());
	await fireEvent.click(view.getAllByRole('button')[0]);
	await nextTick(); expect(view.queryByText('public body')).toBeNull();
});
test.each(['window.document', 'viewport'])('current coordinator gates and aborts %s work', async gate => {
	api.mockImplementation(async path => path.endsWith('/capabilities') ? { externalFetchEnabled: true } : new Promise(() => undefined));
	if (gate === 'window.document') Object.defineProperty(window.document, 'visibilityState', { configurable: true, value: 'hidden' });
	else vi.stubGlobal('IntersectionObserver', class { constructor(callback: IntersectionObserverCallback) { intersect = value => callback([{ isIntersecting: value } as IntersectionObserverEntry], this as unknown as IntersectionObserver); } observe() {} disconnect() {} });
	await register(); coordinate(); await nextTick(); expect(api).toHaveBeenCalledTimes(1);
	if (gate === 'window.document') { Object.defineProperty(window.document, 'visibilityState', { configurable: true, value: 'visible' }); window.document.dispatchEvent(new Event('visibilitychange')); } else intersect(true);
	await waitFor(() => expect(api).toHaveBeenCalledTimes(2));
	const signal = api.mock.calls[1][2].signal;
	if (gate === 'window.document') { Object.defineProperty(window.document, 'visibilityState', { configurable: true, value: 'hidden' }); window.document.dispatchEvent(new Event('visibilitychange')); } else intersect(false);
	await nextTick(); expect(signal.aborted).toBe(true); expect(media).not.toHaveBeenCalled();
});
test('source-note switch aborts the old request and ignores its late result', async () => {
	const finish: Array<(value: PreviewResponse) => void> = [];
	api.mockImplementation(async path => path.endsWith('/capabilities') ? { externalFetchEnabled: true } : new Promise(resolve => { finish.push(resolve); }));
	await register(); const view = coordinate();
	await waitFor(() => expect(finish).toHaveLength(1)); const signal = api.mock.calls[1][2].signal;
	await view.rerender({ source: { kind: 'note', noteId: 'note-2' } });
	await waitFor(() => expect(finish).toHaveLength(2)); expect(signal.aborted).toBe(true);
	finish[0](data()); await nextTick(); await nextTick(); expect(view.queryByText('public body')).toBeNull();
	const next = data(); next.post.text = 'new note'; finish[1](next);
	await waitFor(() => expect(view.getByText('new note')).toBeTruthy());
});
test.each(['cw', 'inactive', 'source'])('late shell cannot revive iframe after %s change', async change => {
	let finish!: (value: { url: string }) => void;
	api.mockImplementation(() => new Promise(resolve => { finish = resolve; }));
	const view = card();
	await waitFor(() => expect(view.container.querySelector('[data-fx-video-play]')).toBeTruthy());
	await fireEvent.click(must(view.container.querySelector('[data-fx-video-play]')));
	const params = api.mock.calls[0][1]; const signal = api.mock.calls[0][2].signal;
	await view.rerender({ context: { ...context, ...(change === 'cw' ? { revealed: false } : change === 'inactive' ? { active: false } : { source: { kind: 'note', noteId: 'note-2' } }) } });
	expect(signal.aborted).toBe(true); finish({ url: fullEmbed(params) });
	await nextTick(); await nextTick(); expect(view.container.querySelector('iframe')).toBeNull();
});
test.each(['cw', 'inactive', 'viewport', 'source'])('ready media is revoked on %s change', async change => {
	const response = data(); response.post.images = [{ url: '/api/plugin/fxtwitter-preview/media/1234567890/image/0?noteId=note-1', alt: 'image alt' }];
	const view = card(response);
	await waitFor(() => expect(view.container.querySelector('img[alt="image alt"]')).toBeTruthy());
	const blobURL = must(view.container.querySelector('img')).getAttribute('src');
	await view.rerender({ context: { ...context, ...(change === 'cw' ? { revealed: false } : change === 'inactive' ? { active: false } : change === 'viewport' ? { inViewport: false } : { source: null }) } });
	expect(view.container.querySelector('img')).toBeNull(); expect(URL.revokeObjectURL).toHaveBeenCalledWith(blobURL);
});
test.each([
	'https://pbs.twimg.com/a.png', '//evil.test/api/plugin/fxtwitter-preview/media/1234567890/image/0',
	'/api/plugin/fxtwitter-preview/media/1234567890/image/0?noteId=other',
	'/api/plugin/fxtwitter-preview/media/1234567890/image/0?noteId=note-1&noteId=other',
	'/api/plugin/fxtwitter-preview/media/999/image/0?noteId=note-1',
	'/api/plugin/fxtwitter-preview/media/1234567890/image/0/extra?noteId=note-1',
	'/api/plugin/fxtwitter-preview/media/1234567890/image/0?noteId=note-1#fragment',
])('strict proxy rejects %s before any fetch', async path => {
	const response = data(); response.post.images = [{ url: path }];
	card(response); await nextTick(); await nextTick(); expect(media).not.toHaveBeenCalled();
});
test('session change clears owned media and revokes object URLs', async () => {
	const response = data(); response.post.images = [{ url: '/api/plugin/fxtwitter-preview/media/1234567890/image/0?noteId=note-1' }];
	const view = card(response); await waitFor(() => expect(view.container.querySelector('img')).toBeTruthy());
	const blobURL = must(view.container.querySelector('img')).getAttribute('src');
	initApi(api as PluginHost['api'], media as PluginHost['fetch'], viewer); await nextTick();
	expect(view.container.querySelector('img')).toBeNull(); expect(URL.revokeObjectURL).toHaveBeenCalledWith(blobURL);
});
test('source matcher rejects forged, non-HTTPS and non-status identities', () => {
	expect(statusID(input)).toBe('1234567890');
	for (const href of ['http://x.com/a/status/123', 'https://x.com.evil.test/a/status/123', 'https://user@x.com/a/status/123', 'https://x.com:444/a/status/123', 'https://x.com/a/status/nope', 'https://x.com/a/status/123/extra', 'https://x.com/invalid!/status/123']) expect(statusID({ kind: 'url', href })).toBeNull();
});

test('safe authenticated Blob opens with separate filename, accurate alt and sensitivity', async () => {
	const response = data(); response.post.images = [{ url: '/api/plugin/fxtwitter-preview/media/1234567890/image/0?noteId=note-1', alt: 'image description', width: 600, height: 400 }]; response.post.isSensitive = true;
	const view = card(response);
	await waitFor(() => expect(view.container.querySelector('img[alt="image description"]')).toBeTruthy());
	await fireEvent.click(must(view.container.querySelector('[data-sensitive-media-kind="image"] [data-sensitive-media-reveal]')));
	await waitFor(() => expect(view.queryByRole('button', { name: 'image description' })).toBeTruthy());
	await fireEvent.click(view.getByRole('button', { name: 'image description' }));
	expect(viewer).toHaveBeenCalledWith([expect.objectContaining({ type: 'image', filename: 'x-1234567890-1', alt: 'image description', isSensitive: true, initiallyRevealed: true, width: 600, height: 400 })], '1234567890:0');
	expect(viewer.mock.calls[0][0][0].blob.type).toBe('image/png');
	expect(media.mock.calls[0][1]).toMatchObject({ method: 'GET', cache: 'no-store', signal: expect.any(AbortSignal) });
});
test.each(['id', 'embedId', 'theme', 'lang', 'hideCard', 'hideThread', 'dnt', 'extra', 'duplicate'])('strict iframe query rejects changed %s', async field => {
	api.mockImplementation(async (_path, params) => {
		const url = new URL(fullEmbed(params));
		if (field === 'duplicate') url.searchParams.append('id', '1234567890'); else url.searchParams.set(field, 'forged');
		return { url: url.href };
	});
	const view = card(); await waitFor(() => expect(view.container.querySelector('[data-fx-video-play]')).toBeTruthy());
	await fireEvent.click(must(view.container.querySelector('[data-fx-video-play]')));
	await waitFor(() => expect(view.queryByText('埋め込みを表示できませんでした。')).toBeTruthy());
	expect(view.container.querySelector('iframe')).toBeNull();
});
test('iframe load is not a render attestation; timeout leaves source link', async () => {
	const view = card(); await waitFor(() => expect(view.container.querySelector('[data-fx-video-play]')).toBeTruthy());
	vi.useFakeTimers();
	await fireEvent.click(must(view.container.querySelector('[data-fx-video-play]'))); await nextTick(); await nextTick();
	const frame = must(view.container.querySelector('iframe')); expect(frame).toBeTruthy();
	dispatchFrameEvent.call(frame, new Event('load'));
	await vi.advanceTimersByTimeAsync(15_000); await nextTick();
	expect(view.container.querySelector('iframe')).toBeNull(); expect(view.getByText('埋め込みを表示できませんでした。')).toBeTruthy();
	expect(view.getByText('Xで投稿を開く').getAttribute('href')).toBe('https://x.com/i/status/1234567890');
	vi.useRealTimers();
});
test('real iframe error removes it and keeps a source link', async () => {
	const view = card(); await waitFor(() => expect(view.container.querySelector('[data-fx-video-play]')).toBeTruthy());
	await fireEvent.click(must(view.container.querySelector('[data-fx-video-play]')));
	await waitFor(() => expect(view.container.querySelector('iframe')).toBeTruthy());
	allowFrameError = true;
	await fireEvent.error(must(view.container.querySelector('iframe'))); await nextTick();
	expect(view.container.querySelector('iframe')).toBeNull(); expect(view.getByText('埋め込みを表示できませんでした。')).toBeTruthy();
});
test('unsupported external video stays a source link without an embed request', async () => {
	const response = data(); response.post.video = { provider: 'external' };
	const view = card(response); await nextTick(); await nextTick();
	expect(view.container.querySelector('[data-fx-video-play],iframe')).toBeNull(); expect(api).not.toHaveBeenCalled();
	expect(view.queryByText('メディアを読み込めませんでした。')).toBeNull();
	expect(view.getByText('Xで投稿を開く').getAttribute('href')).toBe('https://x.com/i/status/1234567890');
});

test.each([
	{ state: 'ready', target: 'main' }, { state: 'ready', target: 'quote' },
	{ state: 'stale', target: 'main' }, { state: 'stale', target: 'quote' },
] as const)('legacy $state $target video preserves the public post as a non-playable copy', async ({ state, target }) => {
	const response = data();
	response.state = state;
	response.post.video = { provider: 'external' };
	response.post.quote = { id: '9876543210', url: 'https://x.com/bob/status/9876543210', text: 'quoted public body', author: { name: 'Bob', screenName: 'bob' }, images: [], hasVideo: true };
	const legacy = { provider: 'youtube', providerVideoId: 'abcdefghijk', posterUrl: `/api/plugin/fxtwitter-preview/media/1234567890/${target === 'main' ? 'poster' : 'quote-poster'}/0?noteId=note-1`, posterExpiresAt: '2026-11-01T00:00:00Z', durationMs: 1000, embedToken: 'deprecated-token', embedExpiresAt: '2026-11-01T00:00:00Z' };
	const videoPost = target === 'main' ? response.post : response.post.quote;
	Object.assign(videoPost, { video: legacy });
	const imagePost = target === 'main' ? response.post.quote : response.post;
	imagePost.images = [{ url: `/api/plugin/fxtwitter-preview/media/1234567890/${target === 'main' ? 'quote-image' : 'image'}/0?noteId=note-1`, alt: 'retained image' }];
	const original = JSON.stringify(response);
	Object.freeze(legacy); Object.freeze(response.post.quote); Object.freeze(response.post); Object.freeze(response);
	api.mockResolvedValue(response);
	const pending = loadPreview(input, context, new AbortController().signal);
	await expect(pending).resolves.toMatchObject({ state });
	const normalized = await pending;
	expect(normalized.state).toBe(state);
	expect(normalized.post?.text).toBe('public body');
	expect(normalized.post?.url).toBe('https://x.com/alice/status/1234567890');
	expect(normalized.post?.quote?.text).toBe('quoted public body');
	expect(normalized.post?.quote?.url).toBe('https://x.com/bob/status/9876543210');
	const video = target === 'main' ? normalized.post?.video : normalized.post?.quote?.video;
	expect(video).toEqual({ provider: 'external', posterUrl: legacy.posterUrl, posterExpiresAt: '2026-11-01T00:00:00Z', durationMs: 1000 });
	expect(video).not.toBe(legacy);
	expect(normalized.post).not.toBe(response.post);
	expect(normalized.post?.quote).not.toBe(response.post.quote);
	expect(JSON.stringify(response)).toBe(original);
	const view = card(normalized);
	await waitFor(() => expect(view.container.querySelector('img[alt="retained image"]')).toBeTruthy());
	expect(view.getByText('public body')).toBeTruthy();
	expect(view.getByText('quoted public body')).toBeTruthy();
	expect(view.container.querySelector('a[href="https://x.com/alice/status/1234567890"]')).toBeTruthy();
	expect(view.container.querySelector('a[href="https://x.com/bob/status/9876543210"]')).toBeTruthy();
	expect(view.container.querySelector('[data-fx-video-play],iframe')).toBeNull();
	expect(api.mock.calls.filter(call => call[0].endsWith('/embed/shell'))).toHaveLength(0);
});

test.each(['unavailable', 'disabled', 'pending'] as const)('legacy %s response never revives its body', async state => {
	const response = data();
	Object.assign(must(response.post.video), { provider: 'youtube', providerVideoId: 'abcdefghijk' });
	const original = JSON.stringify(response);
	api.mockResolvedValue({ ...response, state, body: 'private audit body' });
	if (state === 'pending') vi.useFakeTimers();
	const pending = loadPreview(input, context, new AbortController().signal);
	if (state === 'pending') await vi.runAllTimersAsync();
	const normalized = await pending;
	expect(normalized).toEqual({ state });
	expect(JSON.stringify(response)).toBe(original);
	const view = card(normalized);
	expect(view.container.textContent).not.toContain('public body');
	expect(view.container.textContent).not.toContain('private audit body');
	expect(view.container.querySelector('iframe,img')).toBeNull();
	expect(media).not.toHaveBeenCalled();
	expect(api.mock.calls.filter(call => call[0].endsWith('/embed/shell'))).toHaveLength(0);
});

test.each([
	{ provider: 'unknown', providerVideoId: 'abcdefghijk' },
	{ provider: 'youtube' },
	{ provider: 'youtube', providerVideoId: '' },
	{ provider: 'youtube', providerVideoId: 123 },
	{ provider: 'youtube', providerVideoId: 'abcdefghijk', durationMs: 'invalid' },
	{ provider: 'external', providerVideoId: 'abcdefghijk' },
])('legacy compatibility keeps unrelated malformed video strict: %j', async video => {
	const response = data(); Object.assign(response.post, { video }); api.mockResolvedValue(response);
	await expect(loadPreview(input, context, new AbortController().signal)).resolves.toMatchObject({ state: 'unavailable', error: 'invalid-response' });
});

test.each([
	{ failure: 'rejected', kind: 'image' }, { failure: 'http', kind: 'image' }, { failure: 'mime', kind: 'image' }, { failure: 'blob', kind: 'image' },
	{ failure: 'rejected', kind: 'avatar' }, { failure: 'http', kind: 'avatar' }, { failure: 'mime', kind: 'avatar' }, { failure: 'blob', kind: 'avatar' },
] as const)('expected media $kind $failure failure is visible with body and source fallback', async ({ failure, kind }) => {
	if (failure === 'rejected') media.mockRejectedValue(new Error('fetch failed'));
	if (failure === 'http') media.mockResolvedValue(new Response('', { status: 503 }));
	if (failure === 'mime') media.mockResolvedValue(new Response(new Blob(['unsafe'], { type: 'text/html' }), { headers: { 'Content-Type': 'text/html' } }));
	if (failure === 'blob') media.mockResolvedValue({ ok: true, headers: new Headers({ 'Content-Type': 'image/png' }), blob: async () => { throw new Error('blob failed'); } });
	const response = data();
	response.post.video = { provider: 'external' };
	if (kind === 'image') response.post.images = [{ url: '/api/plugin/fxtwitter-preview/media/1234567890/image/0?noteId=note-1' }];
	if (kind === 'avatar') response.post.author.avatarUrl = '/api/plugin/fxtwitter-preview/media/1234567890/avatar/0?noteId=note-1';
	const view = card(response);
	await waitFor(() => expect(view.queryByText('メディアを読み込めませんでした。')).toBeTruthy());
	expect(view.getByText('public body')).toBeTruthy();
	expect(view.container.querySelector(`a[href="${input.href}"]`)).toBeTruthy();
	expect(view.container.querySelector('img,iframe')).toBeNull();
	expect(URL.createObjectURL).not.toHaveBeenCalled();
});

test.each(['inactive', 'source', 'session'] as const)('late media failure after %s cannot publish an old error', async change => {
	let reject!: (reason: Error) => void;
	media.mockImplementationOnce(() => new Promise((_resolve, fail) => { reject = fail; }));
	const response = data(); response.post.images = [{ url: '/api/plugin/fxtwitter-preview/media/1234567890/image/0?noteId=note-1' }];
	const view = card(response);
	await waitFor(() => expect(media).toHaveBeenCalledTimes(1));
	const signal = media.mock.calls[0][1].signal;
	if (change === 'inactive') await view.rerender({ context: { ...context, active: false } });
	if (change === 'source') await view.rerender({ data: { state: 'ready', post: { ...response.post, images: [], video: undefined } }, context: { ...context, source: { kind: 'note', noteId: 'note-2' } } });
	if (change === 'session') initApi(api as PluginHost['api'], media as PluginHost['fetch'], viewer);
	await nextTick();
	expect(signal.aborted).toBe(true);
	reject(new Error('late fetch failure'));
	// Drain the rejected fetch and resource-bundle promises before asserting absence.
	await new Promise(resolve => { window.setTimeout(resolve); }); await nextTick();
	expect(view.queryByText('メディアを読み込めませんでした。')).toBeNull();
	expect(view.container.querySelector('img')).toBeNull();
	expect(URL.createObjectURL).not.toHaveBeenCalled();
});
test('cached Blob load settling after abort cannot publish a URL', async () => {
	let finish!: (value: Blob) => void;
	media.mockResolvedValue({ ok: true, headers: new Headers({ 'Content-Type': 'image/png' }), blob: () => new Promise(resolve => { finish = resolve; }) });
	const response = data(); response.post.images = [{ url: '/api/plugin/fxtwitter-preview/media/1234567890/image/0?noteId=note-1' }];
	const view = card(response); await waitFor(() => expect(finish).toBeTypeOf('function'));
	await view.rerender({ context: { ...context, active: false } }); finish(new Blob(['png'], { type: 'image/png' }));
	await nextTick(); await nextTick(); expect(URL.createObjectURL).not.toHaveBeenCalled(); expect(view.container.querySelector('img')).toBeNull();
});
test('moderator page accurately labels normalized body and retention semantics', async () => {
	api.mockImplementation(async path => {
		if (path.endsWith('admin/settings/get')) return { revision: 1, authenticatedPerHour: 100, authenticatedPerDay: 600, globalPerHour: 300, roleLimits: [] };
		if (path.endsWith('admin/audit/list')) return { items: [{ id: 1, postId: '1234567890', body: 'normalized body', bodyFormat: 'normalized_text_v1', contentHash: 'hash', observedAt: '2026-10-31' }] };
		return [];
	});
	const view = render(AdminPage, { global: globals });
	expect(view.getByText(/再取得間隔は保存期限ではありません/)).toBeTruthy();
	expect(view.container.textContent).toContain('既定の0は無期限');
	const inputID = must(view.container.querySelector('input[pattern]'));
	await fireEvent.update(inputID, '1234567890');
	const button = must(view.getAllByRole('button').find(value => value.textContent.includes('スナップショットを表示')));
	await fireEvent.click(button);
	await waitFor(() => expect(view.getByText('normalized body')).toBeTruthy());
	expect(view.queryByText('正規化済みテキスト (normalized_text_v1)')).toBeTruthy();
});

test.each(['moderator', 'settings-error'])('audit UI remains usable with %s independently of administrator settings', async mode => {
	api.mockImplementation(async path => {
		if (path.endsWith('/admin/status')) {
			if (mode === 'settings-error') throw new Error('settings status unavailable');
			return { canManageRateLimits: false };
		}
		if (path.endsWith('/admin/audit/list')) return { items: [{ id: 1, body: 'moderator audit', bodyFormat: 'normalized_text_v1', observedAt: '2026-10-31', contentHash: 'hash' }] };
		throw new Error(`unexpected administrator call ${path}`);
	});
	const view = render(AdminPage, { global: globals });
	await fireEvent.update(must(view.container.querySelector('input[pattern]')), '1234567890');
	await fireEvent.click(must(view.getAllByRole('button').find(value => value.textContent.includes('スナップショットを表示'))));
	await waitFor(() => expect(view.getByText('moderator audit')).toBeTruthy());
	expect(view.container.querySelector('form')).toBeNull();
	if (mode === 'settings-error') expect(view.queryByText('settings status unavailable')).toBeTruthy();
	expect(api.mock.calls.some(call => call[0] === 'admin/roles/list' || call[0].endsWith('/admin/rate-limit-settings'))).toBe(false);
	expect(api.mock.calls.some(call => call[0].endsWith('/admin/rate-limit-settings/update'))).toBe(false);
});

test.each(['pending', 'rejected', 'invalid'] as const)('administrator form stays closed while settings are %s', async state => {
	let finish!: (value: unknown) => void;
	api.mockImplementation(async path => {
		if (path.endsWith('/admin/status')) return { canManageRateLimits: true };
		if (path === 'admin/roles/list') return [];
		if (path.endsWith('/admin/rate-limit-settings')) {
			if (state === 'pending') return new Promise(resolve => { finish = resolve; });
			if (state === 'rejected') throw new Error('settings unavailable');
			return { revision: 0, authenticatedPerHour: 100, authenticatedPerDay: 600, globalPerHour: 300, roleLimits: [] };
		}
		throw new Error(`unexpected administrator call ${path}`);
	});
	const view = render(AdminPage, { global: globals });
	await waitFor(() => expect(api.mock.calls.some(call => call[0].endsWith('/admin/rate-limit-settings'))).toBe(true));
	expect(view.container.querySelector('form')).toBeNull();
	if (state === 'pending') {
		finish({ revision: 1, authenticatedPerHour: 100, authenticatedPerDay: 600, globalPerHour: 300, roleLimits: [] });
		await waitFor(() => expect(view.container.querySelector('form')).toBeTruthy());
		await fireEvent.submit(must(view.container.querySelector('form')));
		expect(api.mock.calls.filter(call => call[0].endsWith('/admin/rate-limit-settings/update'))).toHaveLength(1);
	} else {
		await waitFor(() => expect(view.queryByText(state === 'rejected' ? 'settings unavailable' : '設定を取得できませんでした。')).toBeTruthy());
		expect(view.container.querySelector('form')).toBeNull();
		expect(api.mock.calls.some(call => call[0].endsWith('/admin/rate-limit-settings/update'))).toBe(false);
	}
});

test.each([{ detail: false }, { revealed: false }, { active: false }, { inViewport: false }, { source: null }])('populated media cannot start work in gated context %j', async patch => {
	const response = data();
	response.post.author.avatarUrl = '/api/plugin/fxtwitter-preview/media/1234567890/avatar/0?noteId=note-1';
	response.post.images = [{ url: '/api/plugin/fxtwitter-preview/media/1234567890/image/0?noteId=note-1' }];
	card(response, { ...context, ...patch }); await nextTick(); await nextTick();
	expect(media).not.toHaveBeenCalled(); expect(api).not.toHaveBeenCalled(); expect(URL.createObjectURL).not.toHaveBeenCalled();
});
test('current coordinator KeepAlive deactivation disposes ready media and iframe', async () => {
	const response = data();
	response.post.author.avatarUrl = '/api/plugin/fxtwitter-preview/media/1234567890/avatar/0?noteId=note-1';
	api.mockImplementation(async (path, params) => path.endsWith('/capabilities') ? { externalFetchEnabled: true } : path.endsWith('/preview') ? response : { url: fullEmbed(params) });
	await register();
	const shown = ref(true);
	// This independent wrapper exercises the real coordinator's KeepAlive lifecycle.
	// eslint-disable-next-line vue/one-component-per-file
	const Wrapper = defineComponent({ components: { MkPluginUrlPreview }, setup: () => ({ shown, context, input }), template: '<KeepAlive><MkPluginUrlPreview v-if="shown" :url="input.href" :detail="true" :revealed="true" :source="context.source"/></KeepAlive>' });
	const view = render(Wrapper, { global: globals });
	await waitFor(() => expect(view.container.querySelector('img')).toBeTruthy());
	await waitFor(() => expect(view.container.querySelector('[data-fx-video-play]')).toBeTruthy());
	await fireEvent.click(must(view.container.querySelector('[data-fx-video-play]')));
	await waitFor(() => expect(view.container.querySelector('iframe')).toBeTruthy());
	shown.value = false; await nextTick();
	expect(URL.revokeObjectURL).toHaveBeenCalled();
	shown.value = true; await nextTick();
	await waitFor(() => expect(view.container.querySelector('img')).toBeTruthy());
	expect(view.container.querySelector('iframe')).toBeNull();
	expect(api.mock.calls.filter(call => call[0].endsWith('/preview'))).toHaveLength(1);
});

test.each(['bad\u0000tag', 'bad\u001ftag', 'bad\u007ftag', 'bad tag'])('normalization rejects control/whitespace hashtag %j', async tag => {
	const response = data(); response.post.text = '#tag';
	response.post.textEntities = [{ kind: 'hashtag', start: 0, end: 4, display: '#tag', tag }];
	api.mockResolvedValue(response);
	await expect(loadPreview(input, context, new AbortController().signal)).resolves.toEqual({ state: 'unavailable', postId: '1234567890', error: 'invalid-response' });
});
test('normalization keeps safe rich text and rejects unsafe links', async () => {
	const response = data(); response.post.text = '@alice #tag';
	response.post.textEntities = [{ kind: 'mention', start: 0, end: 6, display: '@alice', href: 'https://x.com/alice' }, { kind: 'hashtag', start: 7, end: 11, display: '#tag', tag: 'tag' }];
	api.mockResolvedValue(response);
	const normalized = await loadPreview(input, context, new AbortController().signal);
	expect(normalized.post?.textEntities).toEqual(response.post.textEntities);
	response.post.textEntities[0].href = 'javascript:alert(1)';
	await expect(loadPreview(input, context, new AbortController().signal)).resolves.toMatchObject({ state: 'unavailable', error: 'invalid-response' });
});

test.each(['/embed/extra/../index.html', '/embed/%2e/index.html'])('literal fixed embed path rejects URL-normalized variant %s', async path => {
	api.mockImplementation(async (_endpoint, params) => ({ url: fullEmbed(params).replace('/embed/index.html', path) }));
	const view = card(); await waitFor(() => expect(view.container.querySelector('[data-fx-video-play]')).toBeTruthy());
	await fireEvent.click(must(view.container.querySelector('[data-fx-video-play]')));
	await waitFor(() => expect(view.queryByText('埋め込みを表示できませんでした。')).toBeTruthy());
	expect(view.container.querySelector('iframe')).toBeNull();
});
