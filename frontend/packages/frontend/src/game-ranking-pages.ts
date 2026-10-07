/*
 * SPDX-FileCopyrightText: syuilo and misskey-project
 * SPDX-License-Identifier: AGPL-3.0-only
 */
import type { PageRegistration } from '@/plugin-api.js';

export const gameRankingsPath = '/game-rankings';
export const gameRankingLabels: Record<string, string> = { genshin: '原神', hsr: 'スターレイル' };

export function isGameRankingPage(page: PageRegistration): boolean {
	return !page.admin && (page.plugin === 'genshin' || page.plugin === 'hsr') && page.path === '/rankings';
}

/** インストール済みの公開ランキングだけを、原神・スターレイルの順で表示する。 */
export function gameRankingPages(pages: PageRegistration[]): PageRegistration[] {
	return ['genshin', 'hsr'].flatMap(name => {
		const page = pages.find(candidate => candidate.plugin === name && isGameRankingPage(candidate));
		return page ? [page] : [];
	});
}
