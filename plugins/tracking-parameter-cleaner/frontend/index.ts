/*
 * SPDX-FileCopyrightText: syuilo and misskey-project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import { definePlugin } from '@/plugin-api.js';
import { lang } from '@@/js/config.js';
import { cleanTrackingParameters } from './cleaner.js';

export { cleanTrackingParameters } from './cleaner.js';

export default definePlugin({
	name: 'tracking-parameter-cleaner',
	setup(host) {
		host.postForm.registerPasteTransform('clean-links', cleanTrackingParameters, {
			enabledPreferenceKey: 'trackingParameterCleanerEnabled',
			notification: () => lang === 'ja-JP' ? '追跡パラメータを除去しました' : 'Tracking parameters removed',
		});
	},
});
