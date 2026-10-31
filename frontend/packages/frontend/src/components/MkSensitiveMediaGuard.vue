<!--
SPDX-FileCopyrightText: syuilo and misskey-project
SPDX-FileCopyrightText: Elythia-Network
SPDX-License-Identifier: AGPL-3.0-only
-->

<template>
<div
	:class="[$style.root, { [$style.highlight]: hidden && isSensitive && prefer.s.highlightSensitiveMedia }]"
	:data-sensitive-media-hidden="String(hidden)"
	:data-sensitive-media-kind="kind"
>
	<div :class="{ [$style.hiddenContent]: hidden }" :inert="hidden" :aria-hidden="hidden ? true : undefined">
		<slot></slot>
	</div>
	<button
		v-if="hidden"
		type="button"
		class="_button"
		:class="$style.reveal"
		data-sensitive-media-reveal
		@click.stop="reveal"
	>
		<b><i :class="isSensitive ? 'ti ti-eye-exclamation' : kind === 'video' ? 'ti ti-movie' : 'ti ti-photo'"></i> {{ isSensitive ? i18n.ts.sensitive : kind === 'video' ? i18n.ts.video : i18n.ts.image }}</b>
		<span>{{ i18n.ts.clickToShow }}</span>
	</button>
</div>
</template>

<script lang="ts" setup>
import { computed, watch } from 'vue';
import { i18n } from '@/i18n.js';
import { prefer } from '@/preferences.js';
import { canRevealSensitiveMedia, shouldHideSensitiveMediaByDefault } from '@/utility/sensitive-file.js';

const props = defineProps<{
	isSensitive?: boolean;
	kind: 'image' | 'video';
}>();

const revealed = defineModel<boolean>('revealed', { default: false });
const hidden = computed(() => !revealed.value);

watch([
	() => props.isSensitive,
	() => prefer.s.nsfw,
], () => {
	revealed.value = !shouldHideSensitiveMediaByDefault(props.isSensitive === true, true);
}, { immediate: true });

async function reveal(): Promise<void> {
	if (await canRevealSensitiveMedia(props.isSensitive === true)) revealed.value = true;
}
</script>

<style lang="scss" module>
.root {
	position: relative;
	overflow: hidden;
}

.highlight {
	outline: 2px solid var(--MI_THEME-warn);
	outline-offset: -2px;
}

.hiddenContent {
	filter: blur(20px);
	pointer-events: none;
	transform: scale(1.05);
	user-select: none;
}

.reveal {
	position: absolute;
	z-index: 3;
	inset: 0;
	display: flex;
	align-items: center;
	justify-content: center;
	flex-direction: column;
	gap: 4px;
	width: 100%;
	height: 100%;
	color: #fff;
	background: rgb(0 0 0 / 45%);
	cursor: pointer;
}

.reveal:focus-visible {
	outline: 3px solid var(--MI_THEME-accent);
	outline-offset: -4px;
}
</style>
