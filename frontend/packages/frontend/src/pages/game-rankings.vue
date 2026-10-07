<!--
SPDX-FileCopyrightText: syuilo and misskey-project
SPDX-License-Identifier: AGPL-3.0-only
-->
<template>
<component :is="embedded ? 'div' : PageWithHeader">
	<div class="_spacer" :class="$style.tabs">
		<div role="tablist" aria-label="ゲーム" :class="$style.tabList">
			<button v-for="(page, index) in pages" :id="`game-ranking-tab-${page.plugin}`" :key="page.plugin" type="button" class="_button" role="tab" :aria-selected="selected?.plugin === page.plugin" :aria-controls="`game-ranking-panel-${page.plugin}`" :tabindex="selected?.plugin === page.plugin ? 0 : -1" :class="[$style.tab, { [$style.active]: selected?.plugin === page.plugin }]" @click="game = page.plugin" @keydown="moveTab($event, index)">{{ gameRankingLabels[page.plugin] }}</button>
		</div>
		<p v-if="!selected">利用できるゲームランキングはありません。</p>
	</div>
	<div v-if="selected" :id="`game-ranking-panel-${selected.plugin}`" role="tabpanel" :aria-labelledby="`game-ranking-tab-${selected.plugin}`">
		<component :is="selected.component" :key="selected.plugin" :embedded="true"/>
	</div>
</component>
</template>
<script lang="ts" setup>
import { computed, ref } from 'vue';
import PageWithHeader from '@/components/global/PageWithHeader.vue';
import { collectPages } from '@/plugin-api.js';
import { serverPlugins } from '@/server-plugins.generated.js';
import { definePage } from '@/page.js';
import { gameRankingPages, gameRankingLabels } from '@/game-ranking-pages.js';

const props = withDefaults(defineProps<{ embedded?: boolean; initialGame?: string }>(), { embedded: false, initialGame: 'genshin' });
const pages = gameRankingPages(collectPages(serverPlugins, false));
const game = ref(props.initialGame);
const selected = computed(() => pages.find(page => page.plugin === game.value) ?? pages[0]);

function moveTab(event: KeyboardEvent, index: number): void {
	let next: number;
	if (event.key === 'ArrowRight') next = (index + 1) % pages.length;
	else if (event.key === 'ArrowLeft') next = (index + pages.length - 1) % pages.length;
	else if (event.key === 'Home') next = 0;
	else if (event.key === 'End') next = pages.length - 1;
	else return;
	event.preventDefault();
	game.value = pages[next].plugin;
	const buttons = (event.currentTarget as HTMLElement).parentElement?.querySelectorAll<HTMLButtonElement>('[role="tab"]');
	buttons?.[next].focus();
}

if (!props.embedded) definePage(() => ({ title: 'ゲームランキング', icon: 'ti ti-trophy' }));
</script>
<style lang="scss" module>
.tabs { --MI_SPACER-w: 800px; }
.tabList { display: flex; gap: 8px; flex-wrap: wrap; }
.tab { padding: 10px 16px; border-radius: var(--MI-radius-sm); }
.active { background: var(--MI_THEME-accentedBg); color: var(--MI_THEME-accent); }
</style>
