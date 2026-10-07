/*
 * SPDX-FileCopyrightText: syuilo and misskey-project
 * SPDX-License-Identifier: AGPL-3.0-only
 */
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/vue';
import GameRankings from '@/pages/game-rankings.vue';

const mocks = vi.hoisted(() => ({ definePage: vi.fn(), names: ['hsr', 'genshin'] }));
vi.mock('@/page.js', () => ({ definePage: mocks.definePage }));
vi.mock('@/server-plugins.generated.js', () => ({ serverPlugins: [] }));
vi.mock('@/components/global/PageWithHeader.vue', async () => {
	const { defineComponent, h } = await import('vue');
	return { default: defineComponent({ setup(_props, { slots }) { return () => h('main', slots.default?.()); } }) };
});
vi.mock('@/plugin-api.js', async () => {
	const { defineComponent, h } = await import('vue');
	return { collectPages: () => mocks.names.map(plugin => ({ plugin, path: '/rankings', fullPath: `/plugin/${plugin}/rankings`, component: defineComponent({ props: { embedded: Boolean }, setup(props) { return () => h('p', `${plugin}:${props.embedded}`); } }) })) };
});
afterEach(() => { cleanup(); mocks.definePage.mockClear(); mocks.names = ['hsr', 'genshin']; });
it('ゲームタブは原神から始まり、クリック・キーボードで切り替わる', async () => {
	render(GameRankings);
	expect(screen.getByText('genshin:true')).toBeTruthy();
	const hsr = screen.getByRole('tab', { name: 'スターレイル' });
	await fireEvent.click(hsr);
	expect(screen.getByText('hsr:true')).toBeTruthy();
	expect(screen.queryByText('genshin:true')).toBeNull();
	await fireEvent.keyDown(hsr, { key: 'ArrowRight' });
	expect(screen.getByRole('tab', { name: '原神' }).getAttribute('aria-selected')).toBe('true');
	expect(mocks.definePage).toHaveBeenCalledTimes(1);
});
it('見つけるの埋込・指定ゲーム・単独構成・空構成でも独立headerを登録しない', () => {
	mocks.names = ['hsr'];
	const view = render(GameRankings, { props: { embedded: true, initialGame: 'unknown' } });
	expect(screen.getByText('hsr:true')).toBeTruthy();
	expect(screen.queryByRole('tab', { name: '原神' })).toBeNull();
	expect(mocks.definePage).not.toHaveBeenCalled();
	view.unmount();
	mocks.names = [];
	render(GameRankings, { props: { embedded: true } });
	expect(screen.getByText('利用できるゲームランキングはありません。')).toBeTruthy();
});
