<!--
SPDX-FileCopyrightText: Misaki Project
SPDX-License-Identifier: AGPL-3.0-only
-->

<template>
<span
	ref="rootEl"
	:class="$style.root"
	tabindex="0"
	:aria-label="accessibleLabel"
	@focus="showKeyboardTooltip"
	@blur="hideKeyboardTooltip"
><b>Lv.{{ level.level.currentLevel }}</b></span>
</template>

<script setup lang="ts">
import { computed, defineAsyncComponent, ref, useTemplateRef } from 'vue';
import type { MaybeRef } from 'vue';
import * as os from '@/os.js';
import { useTooltip } from '@/composables/use-tooltip.js';
import { i18n } from '@/i18n.js';
import type { RoleLevelUserRole } from '@/utility/role-level-api.js';
import { roleLevelExperienceCost } from '@/utility/role-level-experience.js';

const props = defineProps<{
	level: RoleLevelUserRole;
}>();

const rootEl = useTemplateRef('rootEl');
const keyboardShowing = ref(false);

const accessibleLabel = computed(() => {
	const levelLabel = i18n.tsx._roleLevel.level({ level: props.level.level.currentLevel });
	const levelCost = roleLevelExperienceCost(props.level.level);
	if (levelCost == null) return `${levelLabel}, ${i18n.ts._roleLevel.maxLevel}`;
	return `${levelLabel}, ${i18n.tsx._roleLevel.experience({
		current: props.level.level.currentLevelExp,
		next: levelCost,
	})}`;
});

function showTooltip(showing: MaybeRef<boolean>) {
	if (rootEl.value == null) return;
	const { dispose } = os.popup(defineAsyncComponent(() => import('@/components/MkRoleLevelTooltip.vue')), {
		showing,
		anchorElement: rootEl.value,
		level: props.level,
		showDescription: false,
	}, {
		closed: () => dispose(),
	});
}

useTooltip(rootEl, (showing) => {
	if (keyboardShowing.value) return;
	showTooltip(showing);
});

function showKeyboardTooltip() {
	keyboardShowing.value = true;
	showTooltip(keyboardShowing);
}

function hideKeyboardTooltip() {
	keyboardShowing.value = false;
}
</script>

<style lang="scss" module>
.root {
	color: var(--MI_THEME-accent);
	cursor: help;
}
</style>
