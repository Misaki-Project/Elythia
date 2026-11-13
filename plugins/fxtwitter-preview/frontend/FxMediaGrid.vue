<!--
SPDX-FileCopyrightText: syuilo and misskey-project
SPDX-License-Identifier: AGPL-3.0-only
-->

<template>
<MkSensitiveMediaGuard v-model:revealed="revealed" :isSensitive="isSensitive" kind="image">
	<div
		data-fx-media-grid
		:class="[$style.mediaGrid, { [$style.singleImage]: visibleImages.length === 1, [$style.twoImages]: visibleImages.length === 2, [$style.threeImages]: visibleImages.length === 3, [$style.fourImages]: visibleImages.length === 4, [$style.fiveImages]: visibleImages.length === 5, [$style.sixImages]: visibleImages.length === 6 && totalImageCount === 6, [$style.manyImages]: totalImageCount > 6 }]"
		:data-image-count="visibleImages.length"
		:data-total-image-count="totalImageCount"
		:style="singleImageStyle"
	>
		<button
			v-for="image in visibleImages"
			:key="image.index"
			type="button"
			class="_button"
			:class="$style.mediaTile"
			:disabled="image.src == null"
			:aria-label="`${image.alt || `画像 ${image.index + 1}`}${image.index === 5 && remainingCount > 0 ? `、残り ${remainingCount} 枚` : ''}`"
			@click="$emit('open', image.index)"
		>
			<img v-if="image.src" :src="image.src" :alt="image.alt" loading="lazy" style="object-fit: cover; object-position: center">
			<span v-else :class="$style.photoPlaceholder" aria-hidden="true"></span>
			<span v-if="image.index === 5 && remainingCount > 0" :class="$style.remainingOverlay" aria-hidden="true">+{{ remainingCount }}</span>
		</button>
	</div>
</MkSensitiveMediaGuard>
</template>

<script lang="ts" setup>
import { computed } from 'vue';
import { MkSensitiveMediaGuard } from '@/plugin-api.js';

export type FxRenderedImage = {
	index: number;
	src: string | null;
	alt: string;
	width?: number;
	height?: number;
};

const props = defineProps<{
	images: FxRenderedImage[];
	totalImageCount?: number;
	isSensitive?: boolean;
}>();

defineEmits<{
	(ev: 'open', index: number): void;
}>();

const revealed = defineModel<boolean>('revealed', { default: false });
const visibleImages = computed(() => props.images.slice(0, 6));
const totalImageCount = computed(() => props.totalImageCount ?? props.images.length);
const remainingCount = computed(() => Math.max(0, totalImageCount.value - 6));
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
	position: relative;
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

.twoImages {
	aspect-ratio: 16 / 9;
}

.twoImages .mediaTile {
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

.fiveImages,
.sixImages,
.manyImages {
	grid-template-rows: repeat(2, minmax(0, 1fr));
	aspect-ratio: 16 / 9;
}

.fiveImages {
	grid-template-columns: repeat(6, minmax(0, 1fr));
}

.fiveImages .mediaTile {
	aspect-ratio: auto;
}

.fiveImages .mediaTile:nth-child(-n + 2) {
	grid-column: span 3;
}

.fiveImages .mediaTile:nth-child(n + 3) {
	grid-column: span 2;
}

.sixImages,
.manyImages {
	grid-template-columns: repeat(3, minmax(0, 1fr));
}

.fiveImages .mediaTile,
.sixImages .mediaTile,
.manyImages .mediaTile {
	display: block;
	align-self: stretch;
	min-height: 0;
	width: 100%;
	height: 100%;
	padding: 0;
	text-align: inherit;
}

.photoPlaceholder {
	display: block;
	width: 100%;
	height: 100%;
	background: var(--MI_THEME-bg);
}

.remainingOverlay {
	position: absolute;
	inset: 0;
	display: grid;
	place-items: center;
	background: rgb(0 0 0 / 62%);
	color: #fff;
	font-size: clamp(1.5rem, 5vw, 3rem);
	font-weight: 700;
}
</style>
