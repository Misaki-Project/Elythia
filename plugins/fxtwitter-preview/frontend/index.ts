/*
 * SPDX-FileCopyrightText: syuilo and misskey-project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import { definePlugin } from '@/plugin-api.js';
import type { PreviewProvider } from '@/plugin-api.js';
import { api, apiSessionEpoch, initApi, setVideoPlaybackMode, truncatePostText } from './api.js';
import type { Capabilities, PreviewResponse } from './api.js';
import { loadPreview, statusID } from './preview-loader.js';
import FxPreview from './FxPreview.vue';
import FxPreviewSkeleton from './FxPreviewSkeleton.vue';
import AdminPage from './AdminPage.vue';

export { FxPreview, initApi, truncatePostText };
export { loadPreview, statusID } from './preview-loader.js';
export type { PreviewResponse } from './api.js';

export const provider: PreviewProvider<PreviewResponse> = {
	id: 'x-status',
	priority: 100,
	requiresDetail: false,
	loadingComponent: FxPreviewSkeleton,
	preferenceKey: 'xTimelinePreviewMode',
	presentation: (_input, context) => context.detail || context.preferenceValue !== 'ogp' ? 'automatic' : 'manual',
	match: input => statusID(input) != null,
	load: loadPreview,
	component: FxPreview,
};

const anonymousProvider: PreviewProvider<PreviewResponse> = {
	...provider,
	presentation: () => 'disabled',
	load: async () => ({ state: 'disabled' }),
};

export default definePlugin({
	name: 'fxtwitter-preview',
	pages: [{ path: '/', component: AdminPage, admin: true, navTitle: 'FxTwitter preview', navIcon: 'ti ti-brand-x' }],
	async setup(host) {
		initApi(host.api, host.fetch, host.openMedia);
		const sessionEpoch = apiSessionEpoch.value;
		try {
			const capabilities = await api<Capabilities>('capabilities');
			if (apiSessionEpoch.value !== sessionEpoch) return;
			setVideoPlaybackMode(capabilities.videoPlaybackMode);
			if (!capabilities.externalFetchEnabled) return;
			if (host.me == null) {
				host.preview.register(anonymousProvider);
			} else if (capabilities.canUseFxTwitterPreview === true) {
				host.preview.register(provider);
			}
		} catch (err) {
			console.warn('[plugin:fxtwitter-preview] preview provider remains disabled', err);
		}
	},
});
