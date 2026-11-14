/*
 * SPDX-FileCopyrightText: syuilo and misskey-project
 * SPDX-License-Identifier: AGPL-3.0-only
 */
import { expect, it } from 'vitest';
import { gameRankingPages, isGameRankingPage } from '@/game-ranking-pages.js';
import type { PageRegistration } from '@/plugin-api.js';

function page(plugin: string, path = '/rankings', admin = false): PageRegistration {
	return { plugin, path, admin, fullPath: `/plugin/${plugin}${path}`, component: {} };
}
it('公開された対応ゲームだけを重複なしで固定順に集める', () => {
	const genshin = page('genshin');
	const hsr = page('hsr');
	expect(gameRankingPages([hsr, page('other'), page('genshin', '/other'), page('hsr', '/rankings', true), genshin, genshin])).toEqual([genshin, hsr]);
	expect(isGameRankingPage(page('other'))).toBe(false);
});
it('未インストールのゲームを表示せず単独・空構成に対応する', () => {
	expect(gameRankingPages([])).toEqual([]);
	expect(gameRankingPages([page('hsr')]).map(p => p.plugin)).toEqual(['hsr']);
});
