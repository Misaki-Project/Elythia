/*
 * SPDX-FileCopyrightText: syuilo and misskey-project
 * SPDX-License-Identifier: AGPL-3.0-only
 */
import { describe, expect, it, vi } from 'vitest';
import { collectExplorePages } from '@/plugin-api.js';
import type { PluginDefinition } from '@/plugin-api.js';

vi.mock('@/utility/misskey-api.js', () => ({ misskeyApi: vi.fn() }));
vi.mock('@/i.js', () => ({ $i: null, iAmModerator: false, iAmAdmin: false }));
vi.mock('@/router.js', () => ({ useRouter: vi.fn() }));

describe('見つけるのプラグインタブ', () => {
	it('空の構成では追加タブを出さない', () => {
		expect(collectExplorePages([])).toEqual([]);
	});
	it('明示した公開ページのみを追加し、名前空間を保持する', () => {
		const plugins: PluginDefinition[] = [{ name: 'genshin', setup: () => {}, pages: [
			{ path: '/rankings', component: {}, navTitle: '原神ランキング', explore: true },
			{ path: '/hidden', component: {}, navTitle: '非表示ページ' },
			{ path: '/admin', component: {}, navTitle: '管理ページ', explore: true, admin: true },
			{ path: '/untitled', component: {}, explore: true },
		] }];
		expect(collectExplorePages(plugins).map(page => page.fullPath)).toEqual(['/plugin/genshin/rankings']);
	});
});
