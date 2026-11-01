<!--
SPDX-FileCopyrightText: syuilo and misskey-project
SPDX-License-Identifier: AGPL-3.0-only
-->

<template>
<MkSensitiveMediaGuard v-model:revealed="revealed" :isSensitive="isSensitive" kind="image">
	<div
		data-fx-media-grid
		:class="[$style.mediaGrid, { [$style.singleImage]: images.length === 1, [$style.threeImages]: images.length === 3, [$style.fourImages]: images.length === 4 }]"
		:style="singleImageStyle"
	>
		<button
			v-for="image in images"
			:key="image.index"
			type="button"
			class="_button"
			:class="$style.mediaTile"
			:aria-label="image.alt || `画像 ${image.index + 1}`"
			@click="$emit('open', image.index)"
		>
			<img :src="image.src" :alt="image.alt" loading="lazy">
		</button>
	</div>
</MkSensitiveMediaGuard>
</template>

<script lang="ts" setup>
import { computed } from 'vue';
import { MkSensitiveMediaGuard } from '@/plugin-api.js';

export type FxRenderedImage = {
	index: number;
	src: string;
	alt: string;
	width?: number;
	height?: number;
};

const props = defineProps<{
	images: FxRenderedImage[];
	isSensitive?: boolean;
}>();

defineEmits<{
	(ev: 'open', index: number): void;
}>();

const revealed = defineModel<boolean>('revealed', { default: false });
const singleImageStyle = computed<Record<string, string> | undefined>(() => {
	const image = props.images.length === 1 ? props.images[0] : null;
	if (image == null) return undefined;
	const natural = image.width != null && image.height != null && image.width > 0 && image.height > 0 ? image.width / image.height : 16 / 9;
	const ratio = Number.isFinite(natural) && natural >= 0.25 && natural <= 4 ? natural : 16 / 9;
	return { '--fx-single-aspect': String(ratio) };
});
</script>

<style lang="scss" module>
.mediaGrid {
	display: grid;
	grid-template-columns: repeat(2, minmax(0, 1fr));
	gap: 2px;
	background: var(--MI_THEME-divider);
}

.mediaTile {
	min-width: 0;
	aspect-ratio: 16 / 9;
	overflow: hidden;
	background: var(--MI_THEME-bg);
}

.mediaTile img {
	display: block;
	width: 100%;
	height: 100%;
	object-fit: cover;
	object-position: center;
}

.singleImage {
	grid-template-columns: minmax(0, 1fr);
	aspect-ratio: var(--fx-single-aspect, 16 / 9);
	max-height: min(360px, 50vh);
}

.singleImage .mediaTile {
	width: 100%;
	height: 100%;
	aspect-ratio: auto;
}

.threeImages {
	grid-template-rows: repeat(2, minmax(0, 1fr));
	aspect-ratio: 16 / 9;
}

.threeImages .mediaTile {
	aspect-ratio: auto;
}

.threeImages .mediaTile:first-child {
	grid-row: 1 / 3;
}

.fourImages {
	grid-template-rows: repeat(2, minmax(0, 1fr));
}
</style>
