/*
 * SPDX-FileCopyrightText: Elythia-Network
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import { definePlugin } from '@/plugin-api.js';
import { initApi, isAvailable } from './host.js';
import { messages } from './messages.js';
import { isSet } from './option.js';
import TargetsEditor from './TargetsEditor.vue';

export default definePlugin({
	name: 'delivery-targets',
	async setup(host) {
		initApi(host.api);
		// バックエンドが止まっていれば、選んでも投稿が INVALID_PARAM で失敗する。
		if (!await isAvailable(host.api)) return;
		// 投稿フォームのフッターにボタンを出す。選んだ値は notes/create の
		// pluginOptions["delivery-targets"] として送られ、バックエンドが保存する。
		host.postForm.registerNoteOption({
			icon: 'ti ti-world-share',
			label: () => messages.title,
			component: TargetsEditor,
			isSet,
		});
	},
});
