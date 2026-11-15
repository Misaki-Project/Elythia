<!--
SPDX-FileCopyrightText: syuilo and misskey-project
SPDX-License-Identifier: AGPL-3.0-only
-->

<template>
<MkSensitiveMediaGuard v-model:revealed="revealed" :isSensitive="isSensitive" kind="image">
	<div
		data-fx-media-grid
		:data-layout="isManyImages ? 'many' : undefined"
		:data-image-count="images.length"
		:class="[$style.mediaGrid, { [$style.singleImage]: images.length === 1, [$style.threeImages]: images.length === 3, [$style.fourImages]: images.length === 4, [$style.manyImages]: isManyImages }]"
		:style="singleImageStyle"
	>
		<button
			v-for="(image, visibleIndex) in visibleImages"
			:key="image.index"
			type="button"
			class="_button"
			:class="$style.mediaTile"
			:disabled="image.src == null && image.state === 'loading'"
			:aria-label="image.alt || `画像 ${image.index + 1}`"
			@click="$emit('open', image.index)"
		>
			<img v-if="image.src != null" :src="image.src" :alt="image.alt" loading="lazy">
			<span v-else :class="$style.missingImage" :data-photo-state="image.state ?? 'loading'" aria-hidden="true">
				<i v-if="image.state === 'error' || image.state === 'forbidden'" class="ti ti-photo-x"></i>
				<i v-else class="ti ti-photo"></i>
			</span>
			<span v-if="visibleIndex === 5 && overflowCount > 0" :class="$style.overflowOverlay" data-photo-overflow aria-hidden="true">+{{ overflowCount }}</span>
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
	state?: 'loading' | 'error' | 'forbidden' | 'limit';
	width?: number;
	height?: number;
};

const props = defineProps<{
	images: FxRenderedImage[];
	isSensitive?: boolean;
}>();

const isManyImages = computed(() => props.images.length > 4);
const visibleImages = computed(() => isManyImages.value ? props.images.slice(0, 6) : props.images);
const overflowCount = computed(() => Math.max(0, props.images.length - 6));

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
	position: relative;
	min-width: 0;
	aspect-ratio: 16 / 9;
	overflow: hidden;
	background: var(--MI_THEME-bg);
}

.mediaTile:focus-visible {
	outline: 3px solid var(--MI_THEME-accent);
	outline-offset: -3px;
	z-index: 2;
}

.mediaTile img {
	display: block;
	width: 100%;
	height: 100%;
	object-fit: cover;
	object-position: center;
}

.missingImage {
	display: grid;
	width: 100%;
	height: 100%;
	place-items: center;
	color: var(--MI_THEME-fgTransparentWeak);
	background: var(--MI_THEME-bg);
	font-size: 1.4em;
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

.manyImages {
	grid-template-columns: repeat(3, minmax(0, 1fr));
	grid-template-rows: repeat(2, minmax(0, 1fr));
	aspect-ratio: 16 / 9;
	overflow: hidden;
}

.manyImages .mediaTile {
	aspect-ratio: auto;
}

.manyImages[data-image-count='5'] {
	grid-template-columns: repeat(6, minmax(0, 1fr));
}

.manyImages[data-image-count='5'] .mediaTile:nth-child(-n + 2) {
	grid-column: span 3;
}

.manyImages[data-image-count='5'] .mediaTile:nth-child(n + 3) {
	grid-column: span 2;
}

.overflowOverlay {
	position: absolute;
	inset: 0;
	display: grid;
	place-items: center;
	color: #fff;
	background: rgb(0 0 0 / 55%);
	font-size: clamp(1.15rem, 4vw, 2rem);
	font-weight: 700;
}
</style>
