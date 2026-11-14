<!--
SPDX-FileCopyrightText: Misaki Project
SPDX-License-Identifier: AGPL-3.0-only
-->

<template>
<span ref="rootEl" class="role" :style="{ '--color': role.color ?? '' }">
	<MkA v-adaptive-bg :to="`/roles/${role.id}`">
		<img v-if="role.iconUrl" :class="$style.icon" :src="role.iconUrl"/>
		{{ role.name }}
		<b v-if="level" :class="$style.level">Lv.{{ level.level.currentLevel }}</b>
	</MkA>
</span>
</template>

<script setup lang="ts">
import { defineAsyncComponent, useTemplateRef } from 'vue';
import * as os from '@/os.js';
import { useTooltip } from '@/composables/use-tooltip.js';
import type { RoleLevelUserRole } from '@/utility/role-level-api.js';

const props = defineProps<{
	role: {
		id: string;
		name: string;
		description: string;
		color?: string | null;
		iconUrl?: string | null;
	};
	level?: RoleLevelUserRole;
}>();

const rootEl = useTemplateRef('rootEl');

useTooltip(rootEl, (showing) => {
	if (rootEl.value == null) return;
	const { dispose } = os.popup(defineAsyncComponent(() => import('@/components/MkRoleLevelTooltip.vue')), {
		showing,
		anchorElement: rootEl.value,
		role: props.role,
		level: props.level,
	}, {
		closed: () => dispose(),
	});
});
</script>

<style lang="scss" module>
.icon {
	height: 1.3em;
	vertical-align: -22%;
}

.level {
	margin-inline-start: 0.35em;
	font-size: 0.85em;
	white-space: nowrap;
}
</style>
