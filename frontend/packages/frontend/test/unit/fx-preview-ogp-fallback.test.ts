/*
 * SPDX-FileCopyrightText: syuilo and misskey-project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import { cleanup, fireEvent, render, waitFor } from '@testing-library/vue';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import plugin from '../../../../../plugins/fxtwitter-preview/frontend/index';
import type { PluginHost, PreviewProvider } from '@/plugin-api.js';
import { components } from '@/components/index.js';
import { directives } from '@/directives/index.js';
import { i18n } from '@/i18n.js';
import MkPluginUrlPreview from '@/components/MkPluginUrlPreview.vue';
import { _resetPreviewsForTest, launchServerPlugins } from '@/plugin-api.js';
import { preferReactive, preferState } from '../setup.unit.js';

vi.mock('@/server-plugins.generated.js', () => ({ serverPlugins: [] }));

const xUrl = 'https://x.com/alice/status/1234567890';
const source = { kind: 'note' as const, noteId: 'note-ogp-1' };

function host(api: PluginHost['api']): { host: PluginHost; providers: PreviewProvider[] } {
	const providers: PreviewProvider[] = [];
	return {
		providers,
		host: {
			name: 'fxtwitter-preview',
			me: { id: 'user-1', username: 'user-1', host: null },
			api,
			fetch: vi.fn() as PluginHost['fetch'],
			openMedia: vi.fn() as PluginHost['openMedia'],
			preview: { register(provider) { providers.push(provider); } },
			postForm: { registerPasteTransform: vi.fn() },
			slot() {},
		},
	};
}

function mockStandardUrlPreview(): void {
	fetchMock.mockOnceIf(request => new URL(request.url).pathname === '/url', () => ({
		status: 200,
		body: JSON.stringify({
			url: xUrl,
			title: 'Standard OGP card',
			description: 'Standard preview summary',
			player: { url: null, width: null, height: null, allow: [] },
		}),
	}));
}

function setTimelineMode(mode: 'ogp' | 'text' | 'media'): void {
	preferState.xTimelinePreviewMode = mode;
	if (preferReactive.xTimelinePreviewMode != null) preferReactive.xTimelinePreviewMode.value = mode;
}

async function mountWithRegisteredProvider(canUseFxTwitterPreview: boolean) {
	const api = vi.fn(async (path: string) => {
		if (path.endsWith('/capabilities')) return {
			externalFetchEnabled: true,
			canUseFxTwitterPreview,
			refetchIntervalSeconds: 3600,
			videoPlaybackMode: 'officialEmbed',
			mediaMode: 'sameOriginProxy',
		};
		if (path.endsWith('/preview')) return {
			state: 'ready',
			post: {
				id: '1234567890', url: xUrl, text: 'Fx preview loaded', createdAt: '2026-10-02',
				author: { name: 'Alice', screenName: 'alice' },
				likes: 1, reposts: 2, quotes: 3, replies: 4, hasVideo: false, images: [], isSensitive: false,
			},
		};
		throw new Error(`unexpected API path: ${path}`);
	}) as PluginHost['api'];
	const enabled = host(api);
	await plugin.setup(enabled.host);
	if (enabled.providers.length > 0) {
		await launchServerPlugins([{
			name: 'fx-preview-ogp-test-bridge',
			setup(pluginHost) { pluginHost.preview.register(enabled.providers[0]); },
		}]);
	}
	return enabled;
}

function renderFallback() {
	return render(MkPluginUrlPreview, {
		props: { url: xUrl, detail: false, revealed: true, source, showActions: true },
		global: { directives, components },
	});
}

beforeEach(() => {
	vi.restoreAllMocks();
	_resetPreviewsForTest();
	fetchMock.resetMocks();
	setTimelineMode('ogp');
	vi.stubGlobal('IntersectionObserver', class {
		readonly root = null;
		readonly rootMargin = '0px';
		readonly thresholds = [0];
		constructor(private callback: IntersectionObserverCallback) {}
		disconnect() {}
		observe(target: Element) { this.callback([{ isIntersecting: true, target } as IntersectionObserverEntry], this as unknown as IntersectionObserver); }
		takeRecords(): IntersectionObserverEntry[] { return []; }
		unobserve() {}
	});
});

afterEach(() => {
	cleanup();
	_resetPreviewsForTest();
	fetchMock.resetMocks();
	vi.unstubAllGlobals();
});

describe('FxTwitter OGP fallback actions', () => {
	test('keeps the standard card, presents one Fx action, and loads Fx when activated', async () => {
		mockStandardUrlPreview();
		const enabled = await mountWithRegisteredProvider(true);
		const result = renderFallback();

		await waitFor(() => expect(result.getByText('Standard OGP card')).toBeTruthy());
		const buttons = result.getAllByRole('button');
		expect(buttons).toHaveLength(1);
		expect(result.container.querySelector('article')).not.toBeNull();

		await fireEvent.click(buttons[0]);
		await waitFor(() => expect(result.getByText('Fx preview loaded')).toBeTruthy());
		expect(enabled.host.api).toHaveBeenCalledWith('plugin/fxtwitter-preview/preview', expect.anything(), expect.anything());
		expect(result.container.querySelector('iframe')).toBeNull();
	});

	test('keeps the standard X action when checked permission leaves no Fx provider', async () => {
		mockStandardUrlPreview();
		await mountWithRegisteredProvider(false);
		const result = renderFallback();

		await waitFor(() => expect(result.getByText('Standard OGP card')).toBeTruthy());
		expect(result.getByRole('button', { name: i18n.ts.expandTweet })).toBeTruthy();
		expect(result.getAllByRole('button')).toHaveLength(1);
	});
});
