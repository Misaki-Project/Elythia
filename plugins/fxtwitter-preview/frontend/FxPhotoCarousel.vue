<!--
 * SPDX-FileCopyrightText: syuilo and misskey-project
 * SPDX-License-Identifier: AGPL-3.0-only
 -->

<template>
	<MkSensitiveMediaGuard v-model:revealed="revealed" :isSensitive="isSensitive" kind="image">
		<section
			data-fx-photo-carousel
			:class="$style.carousel"
			:aria-busy="viewerLoading || undefined"
			:aria-label="`${images.length}件の画像`"
		>
			<div
				ref="viewport"
				data-photo-track
				:class="$style.viewport"
				tabindex="0"
				role="region"
				aria-roledescription="carousel"
				:aria-label="`${images.length}件の画像。左右の矢印キーで移動できます`"
				@scroll.passive="onScroll"
				@keydown.left.prevent="move(-1)"
				@keydown.right.prevent="move(1)"
			>
				<div :class="$style.track">
					<div
						v-for="image in images"
						:key="image.index"
						:data-photo-index="image.index"
						:class="$style.slide"
						role="group"
						aria-roledescription="スライド"
						:aria-label="`${image.index + 1} / ${images.length}`"
					>
						<button
							v-if="image.src"
							type="button"
							class="_button"
							:class="$style.imageButton"
							:aria-label="`${image.alt || `画像 ${image.index + 1}`}を拡大`"
							@click="$emit('open', image.index)"
						>
							<img :src="image.src" :alt="image.alt" loading="lazy">
						</button>
						<div v-else :class="$style.placeholder" :aria-label="image.alt || `画像 ${image.index + 1}`">
							<span v-if="image.state === 'loading'" role="status">画像を読み込み中…</span>
							<span v-else-if="image.state === 'blocked'" role="status">画像を表示できません</span>
							<span v-else-if="image.state === 'budget'" role="status">メモリ上限に達しました</span>
							<span v-else-if="image.state === 'refresh'" role="status">画像情報を再取得してください</span>
							<span v-else-if="image.state === 'error'" role="status">画像を読み込めません</span>
							<span v-else aria-hidden="true">画像 {{ image.index + 1 }}</span>
							<button v-if="image.state === 'error' || image.state === 'refresh'" type="button" class="_button" :class="$style.retry" :disabled="retryDisabled" @click.stop="$emit('retry', image.index)">再試行</button>
						</div>
					</div>
				</div>
			</div>
			<div :class="$style.controls">
				<button data-photo-previous type="button" class="_button" :class="$style.control" :disabled="viewerLoading || activeIndex <= 0" aria-label="前の画像" @click="move(-1)"><i class="ti ti-chevron-left" aria-hidden="true"></i></button>
				<span data-photo-position role="status" aria-live="polite">{{ activeIndex + 1 }} / {{ images.length }}</span>
				<button data-photo-next type="button" class="_button" :class="$style.control" :disabled="viewerLoading || activeIndex >= images.length - 1" aria-label="次の画像" @click="move(1)"><i class="ti ti-chevron-right" aria-hidden="true"></i></button>
			</div>
			<p v-if="viewerLoading" :class="$style.viewerStatus" role="status">ギャラリーを準備しています…</p>
			<p v-else-if="viewerError" :class="$style.viewerStatus" role="status">{{ viewerError }}</p>
		</section>
	</MkSensitiveMediaGuard>
</template>

<script lang="ts" setup>
import { onBeforeUnmount, onMounted, ref } from 'vue';
import { MkSensitiveMediaGuard } from '@/plugin-api.js';

export type FxCarouselImage = {
	index: number;
	src: string | null;
	alt: string;
	width?: number;
	height?: number;
	state?: 'loading' | 'blocked' | 'budget' | 'error' | 'refresh';
};

const props = defineProps<{
	images: FxCarouselImage[];
	isSensitive?: boolean;
	viewerLoading?: boolean;
	viewerError?: string | null;
	retryDisabled?: boolean;
}>();

const emit = defineEmits<{
	(ev: 'visible', index: number): void;
	(ev: 'open', index: number): void;
	(ev: 'retry', index: number): void;
}>();

const revealed = defineModel<boolean>('revealed', { default: false });
const viewport = ref<HTMLElement | null>(null);
const activeIndex = ref(0);

function boundedIndex(index: number): number {
	return Math.max(0, Math.min(props.images.length - 1, index));
}

function setActive(index: number): void {
	const next = boundedIndex(index);
	if (next === activeIndex.value) return;
	activeIndex.value = next;
	emit('visible', next);
}

function move(delta: number): void {
	if (props.viewerLoading) return;
	const next = boundedIndex(activeIndex.value + delta);
	if (next === activeIndex.value) return;
	activeIndex.value = next;
	emit('visible', next);
	const element = viewport.value;
	if (element == null) return;
	if (typeof element.scrollTo === 'function') element.scrollTo({ left: next * element.clientWidth, behavior: 'auto' });
	else element.scrollLeft = next * element.clientWidth;
}

function onScroll(): void {
	const element = viewport.value;
	if (element == null || element.clientWidth <= 0) return;
	if (props.viewerLoading) {
		element.scrollLeft = activeIndex.value * element.clientWidth;
		return;
	}
	setActive(Math.round(element.scrollLeft / element.clientWidth));
}

let resizeObserver: ResizeObserver | null = null;
onMounted(() => {
	emit('visible', activeIndex.value);
	if (typeof ResizeObserver !== 'undefined' && viewport.value != null) {
		resizeObserver = new ResizeObserver(() => {
			const element = viewport.value;
			if (element != null) element.scrollLeft = activeIndex.value * element.clientWidth;
		});
		resizeObserver.observe(viewport.value);
	}
});
onBeforeUnmount(() => resizeObserver?.disconnect());
</script>

<style lang="scss" module>
.carousel { width: 100%; overflow: visible; background: var(--MI_THEME-bg); }
.viewport { width: 100%; height: clamp(120px, 50vh, 360px); overflow-x: auto; overflow-y: hidden; scroll-snap-type: x mandatory; overscroll-behavior-inline: contain; scrollbar-width: thin; }
.track { display: flex; width: 100%; height: 100%; }
.slide { position: relative; flex: 0 0 100%; min-width: 0; height: 100%; scroll-snap-align: start; display: grid; place-items: center; overflow: hidden; background: var(--MI_THEME-bg); }
.imageButton, .placeholder { display: grid; place-items: center; width: 100%; height: 100%; overflow: hidden; background: var(--MI_THEME-bg); }
.imageButton img { display: block; width: 100%; height: 100%; object-fit: contain; }
.placeholder { color: var(--MI_THEME-fgTransparentWeak); }
.controls { display: flex; align-items: center; justify-content: center; gap: 14px; min-height: 42px; color: var(--MI_THEME-fg); }
.control { display: grid; place-items: center; width: 34px; height: 32px; border: 1px solid var(--MI_THEME-divider); border-radius: var(--MI-radius); background: var(--MI_THEME-panel); color: var(--MI_THEME-fg); }
.control:disabled { opacity: .45; }
.viewerStatus { margin: 0; padding: 6px 10px; text-align: center; color: var(--MI_THEME-warn); font-size: .85em; }
.retry { margin-inline-start: 8px; color: var(--MI_THEME-accent); }
</style>
