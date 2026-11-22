/*
 * SPDX-FileCopyrightText: Elythia-Network
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import { lang } from '@@/js/config.js';

const ja = {
	title: '配送先のサーバー',
	include: '指定したサーバーにだけ配送',
	exclude: '指定したサーバーには配送しない',
	includeCaption: 'ここに無いサーバーには届かず、照会されても見せません。指定しないと、すべてのサーバーに配送します。',
	excludeCaption: 'ここにあるサーバーには届かず、照会されても見せません。指定しないと、すべてのサーバーに配送します。',
	host: 'サーバー',
	hostPlaceholder: 'misskey.example',
	add: '追加',
	empty: 'まだ指定していません',
	noteCaption: 'リレーには流しません。照会してきたサーバーは、署名かUser-Agentで判定します。',
	followerHosts: 'フォロワーのいるサーバー',
	followerHostsCaption: '配送が届くのは、フォロワーのいるサーバーだけです。応答しないサーバーは出していません。',
	followerHostsEmpty: 'リモートのフォロワーはいません',
	followerHostsFailed: '一覧を読めませんでした。上の欄から入力して選べます。',
	followerHostsTruncated: 'フォロワーが多いため、一部のサーバーだけを出しています。',
	followerCount: (n: number) => `${n}人`,
};

const en: typeof ja = {
	title: 'Servers to deliver to',
	include: 'Deliver only to these servers',
	exclude: 'Do not deliver to these servers',
	includeCaption: 'Servers not listed here neither receive the note nor see it when they look it up. With no server listed, it is delivered everywhere.',
	excludeCaption: 'Servers listed here neither receive the note nor see it when they look it up. With no server listed, it is delivered everywhere.',
	host: 'Server',
	hostPlaceholder: 'misskey.example',
	add: 'Add',
	empty: 'No server yet',
	noteCaption: 'The note is not sent to relays. Servers that look it up are identified by their signature or User-Agent.',
	followerHosts: 'Servers of your followers',
	followerHostsCaption: 'Notes are delivered only to servers where you have followers. Servers that do not respond are left out.',
	followerHostsEmpty: 'You have no remote followers',
	followerHostsFailed: 'Could not load the list. You can still type a server above.',
	followerHostsTruncated: 'You have many followers, so only some servers are listed.',
	followerCount: (n: number) => `${n} ${n === 1 ? 'follower' : 'followers'}`,
};

export const messages = lang.startsWith('ja') ? ja : en;
