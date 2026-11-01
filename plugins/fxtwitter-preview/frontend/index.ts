/*
 * SPDX-FileCopyrightText: syuilo and misskey-project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import { api, initApi, truncatePostText } from './api.js';
import FxPreview from './FxPreview.vue';
import FxPreviewController from './FxPreviewController.vue';
import { loadPreview, statusID } from './preview-loader.js';
import FxPreviewSkeleton from './FxPreviewSkeleton.vue';
import AdminPage from './AdminPage.vue';
import type { Capabilities, PreviewResponse } from './api.js';
import type { PreviewProvider } from '@/plugin-api.js';
import { definePlugin } from '@/plugin-api.js';

export { FxPreview, initApi, truncatePostText, loadPreview, statusID };
export type { PreviewResponse } from './api.js';

export const provider: PreviewProvider<PreviewResponse> = {
	id: 'x-status',
	priority: 100,
	requiresDetail: true,
	loadingComponent: FxPreviewSkeleton,
	match: input => statusID(input) != null,
	load: loadPreview,
	component: FxPreviewController,
};

// pluginbuild's generated registry imports the plugin definition as a default export.
// eslint-disable-next-line import/no-default-export
export default definePlugin({
	name: 'fxtwitter-preview',
	pages: [{ path: '/', component: AdminPage, admin: true, navTitle: 'FxTwitter preview', navIcon: 'ti ti-brand-x' }],
	async setup(host) {
		initApi(host.api, host.fetch, host.openMedia);
		try {
			const capabilities = await api<Capabilities>('capabilities');
			if (!capabilities.externalFetchEnabled || host.me == null) return;
			host.preview.register(provider);
		} catch (err) {
			console.warn('[plugin:fxtwitter-preview] preview provider remains disabled', err);
		}
	},
});
