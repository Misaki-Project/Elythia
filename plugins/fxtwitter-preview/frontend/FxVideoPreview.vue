<!--
SPDX-FileCopyrightText: syuilo and misskey-project
SPDX-License-Identifier: AGPL-3.0-only
-->
<template>
<MkSensitiveMediaGuard v-model:revealed="revealed" :isSensitive="isSensitive" kind="video">
	<figure :class="$style.video" :data-fx-video-target="target" :data-video-index="index">
		<button
			type="button"
			class="_button"
			:class="$style.posterButton"
			data-fx-video-play
			:disabled="!canPlay || state === 'loading'"
			:aria-label="state === 'loading' ? '動画を準備しています' : '動画を再生'"
			@click.stop="play"
		>
			<img v-if="posterSrc" :class="$style.poster" :src="posterSrc" alt="" loading="lazy">
			<span v-else :class="$style.posterPlaceholder" aria-hidden="true"><i class="ti ti-video"></i></span>
			<span :class="$style.playIcon" aria-hidden="true"><i :class="state === 'loading' ? 'ti ti-loader-2' : 'ti ti-player-play-filled'"></i></span>
		</button>
		<figcaption :class="$style.details">
			<span v-if="durationLabel" :class="$style.duration">{{ durationLabel }}</span>
			<span v-else :class="$style.duration">動画 {{ index + 1 }}</span>
			<span v-if="state === 'loading'" :class="$style.status">準備中…</span>
			<span v-else-if="state === 'error'" :class="$style.status" role="status">{{ terminalError ? '動画へのアクセスが許可されていません。' : '動画を読み込めませんでした。' }}</span>
			<button v-if="state === 'error' && !terminalError" type="button" class="_button" :class="$style.retry" @click.stop="play">再試行</button>
		</figcaption>
	</figure>
</MkSensitiveMediaGuard>
</template>

<script lang="ts" setup>
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { api, apiSession, fetchPluginResource, openPluginMedia } from './api.js';
import type { PreviewContext, PluginMediaItem } from '@/plugin-api.js';
import type { PublicVideo } from './api.js';
import { loadPreview } from './preview-loader.js';
import { MkSensitiveMediaGuard } from '@/plugin-api.js';
import { fetchPreparedPublicBlob, isHttpForbidden, readBoundedMediaBlob } from './public-media.js';
import type { PreparedPublicMedia } from './public-media.js';

const props = defineProps<{
	video: PublicVideo;
	index: number;
	cachePostId: string;
	contentPostId: string;
	target: 'main' | 'quote';
	context: PreviewContext;
	isSensitive?: boolean;
}>();

const MAX_VIDEO_BYTES = 64 * 1024 * 1024;
const revealed = ref(false);
const posterSrc = ref<string | null>(null);
const state = ref<'idle' | 'loading' | 'error'>('idle');
const terminalError = ref(false);
const activeVideo = ref(props.video);
let posterController: AbortController | null = null;
let playbackController: AbortController | null = null;
let posterObjectUrl: string | null = null;
let generation = 0;

const sourceNoteId = computed(() => props.context.source?.kind === 'note' ? props.context.source.noteId : null);
const canAccess = computed(() => props.context.detail && props.context.revealed && props.context.active && revealed.value && sourceNoteId.value != null);
const canLoad = computed(() => canAccess.value && props.context.inViewport);
const canPlay = computed(() => canLoad.value && activeVideo.value.provider === 'x' && activeVideo.value.playbackAvailable === true && activeVideo.value.playbackUrl != null);
const durationLabel = computed(() => formatDuration(activeVideo.value.durationMs));

function formatDuration(value?: number): string | null {
	if (value == null || !Number.isFinite(value) || value < 0) return null;
	const total = Math.floor(value / 1000);
	return `${Math.floor(total / 60)}:${String(total % 60).padStart(2, '0')}`;
}

function validPublicUrl(value: unknown): void {
	if (typeof value !== 'string') throw new Error('invalid URL');
	const url = new URL(value, window.location.origin);
	if (url.origin !== window.location.origin || url.hash !== '' || url.search !== '' || !/^\/api\/plugin\/fxtwitter-preview\/public-media\/[a-f0-9]{32}\.(?:jpg|jpeg|png|webp|avif|mp4)$/.test(url.pathname) || !url.pathname.endsWith('.mp4')) throw new Error('invalid public video URL');
}

function revokePoster(): void {
	if (posterObjectUrl != null) URL.revokeObjectURL(posterObjectUrl);
	posterObjectUrl = null;
	posterSrc.value = null;
}

function stopPlayback(): void {
	generation++;
	playbackController?.abort();
	playbackController = null;
	if (state.value === 'loading') state.value = 'idle';
}

async function loadPoster(): Promise<void> {
	const noteId = sourceNoteId.value;
	const path = props.video.posterUrl;
	if (!canLoad.value || noteId == null || path == null || posterSrc.value != null || posterController != null) return;
	const kind = props.target === 'main' ? 'poster' : 'quote-poster';
	const expectedPath = `/api/plugin/fxtwitter-preview/media/${props.cachePostId}/${kind}/${props.index}`;
	const source = new URL(path, window.location.origin);
	if (source.origin !== window.location.origin || source.hash !== '' || source.pathname !== expectedPath || source.searchParams.size !== 1 || source.searchParams.get('noteId') !== noteId) return;
	const controller = new AbortController();
	posterController = controller;
	try {
		const prepared = await api<PreparedPublicMedia>('media/prepare', { noteId, postId: props.cachePostId, kind, index: props.index }, { signal: controller.signal });
		if (!canLoad.value || controller.signal.aborted) return;
		const blob = await fetchPreparedPublicBlob(prepared, { expectedType: 'image', maxBytes: MAX_VIDEO_BYTES, signal: controller.signal });
		if (!canLoad.value || controller.signal.aborted) return;
		posterObjectUrl = URL.createObjectURL(blob);
		posterSrc.value = posterObjectUrl;
	} catch (error) {
		// Only the backend's explicit private-YouTube response permits the old
		// authenticated poster route. Arbitrary poster URLs stay rejected.
		const code = (error as { code?: unknown } | null)?.code;
		const status = (error as { status?: unknown } | null)?.status;
		if (status === 409 && code === 'PRIVATE_MEDIA_ONLY' && props.video.provider === 'external' && canLoad.value && !controller.signal.aborted) {
			try {
				const response = await fetchPluginResource(`${source.pathname}${source.search}`, { method: 'GET', cache: 'no-store', signal: controller.signal });
				const mime = response.headers.get('Content-Type')?.split(';')[0].trim().toLowerCase();
				const length = response.headers.get('Content-Length');
				if (response.ok && (length == null || /^\d+$/.test(length) && Number(length) <= MAX_VIDEO_BYTES) && ['image/jpeg', 'image/png', 'image/gif', 'image/webp', 'image/avif'].includes(mime ?? '') && !controller.signal.aborted && canLoad.value) {
					const blob = await readBoundedMediaBlob(response, { maxBytes: MAX_VIDEO_BYTES, expectedType: mime!, signal: controller.signal });
					if (!controller.signal.aborted && blob.type.toLowerCase() === mime) {
						posterObjectUrl = URL.createObjectURL(blob);
						posterSrc.value = posterObjectUrl;
					}
				}
			} catch { /* optional poster */ }
		}
	} finally {
		if (posterController === controller) posterController = null;
	}
}

function validatePlaybackUrl(value: unknown, noteId: string): string {
	if (typeof value !== 'string' || !value.startsWith('/')) throw new Error('invalid playback capability');
	const url = new URL(value, window.location.origin);
	const expectedPath = `/api/plugin/fxtwitter-preview/media/video/${props.cachePostId}/${props.target}/${props.index}`;
	if (url.origin !== window.location.origin || url.hash !== '' || url.pathname !== expectedPath || url.searchParams.size !== 2 || url.searchParams.get('noteId') !== noteId || !url.searchParams.has('token') || url.searchParams.get('token') === '') throw new Error('invalid playback capability');
	return value;
}

async function play(): Promise<void> {
	const noteId = sourceNoteId.value;
	if (!canPlay.value || noteId == null || activeVideo.value.playbackUrl == null || state.value === 'loading' || terminalError.value) return;
	const refreshDescriptor = state.value === 'error';
	const current = ++generation;
	const controller = new AbortController();
	playbackController = controller;
	state.value = 'loading';
	try {
		if (refreshDescriptor) {
			const refreshed = await loadPreview({ kind: 'url', href: `https://x.com/i/status/${props.cachePostId}` }, props.context, controller.signal);
			if (controller.signal.aborted || current !== generation || !canLoad.value) return;
			const post = refreshed.state === 'ready' || refreshed.state === 'stale' ? refreshed.post : undefined;
			if (post?.id !== props.cachePostId) throw new Error('refreshed post ownership mismatch');
			const refreshedContent = props.target === 'quote' ? post?.quote : post;
			if (refreshedContent?.id !== props.contentPostId || (refreshedContent.isSensitive === true) !== (props.isSensitive === true)) {
				terminalError.value = true;
				state.value = 'error';
				return;
			}
			const videos = props.target === 'quote' ? refreshedContent.videos ?? (refreshedContent.video ? [refreshedContent.video] : []) : refreshedContent.videos ?? (refreshedContent.video ? [refreshedContent.video] : []);
			const refreshedVideo = videos[props.index];
			if (refreshedVideo == null || refreshedVideo.provider !== 'x' || refreshedVideo.playbackAvailable !== true || refreshedVideo.playbackUrl == null || (activeVideo.value.mediaIdentity != null && refreshedVideo.mediaIdentity !== activeVideo.value.mediaIdentity)) {
				terminalError.value = true;
				state.value = 'error';
				return;
			}
			activeVideo.value = refreshedVideo;
		}
		const playbackUrl = validatePlaybackUrl(activeVideo.value.playbackUrl, noteId);
		const prepared = await api<PreparedPublicMedia>('media/prepare', {
			noteId,
			postId: props.cachePostId,
			kind: 'video',
			target: props.target,
			index: props.index,
			playbackUrl,
		}, { signal: controller.signal });
		if (controller.signal.aborted || current !== generation || !canPlay.value) return;
		validPublicUrl(prepared.url);
		const blob = await fetchPreparedPublicBlob(prepared, { expectedType: 'video', maxBytes: MAX_VIDEO_BYTES, signal: controller.signal });
		if (controller.signal.aborted || current !== generation || !canPlay.value) return;
		const item: PluginMediaItem = { id: `${props.contentPostId}:video:${props.index}`, type: 'video', blob, filename: `x-${props.contentPostId}-${props.index + 1}.mp4`, isSensitive: props.isSensitive === true, initiallyRevealed: true };
		await openPluginMedia([item], item.id);
		if (current === generation) state.value = 'idle';
	} catch (error) {
		if (current === generation && !controller.signal.aborted) {
			terminalError.value = isHttpForbidden(error);
			state.value = 'error';
		}
	} finally {
		if (playbackController === controller) playbackController = null;
	}
}

watch(canAccess, value => {
	if (!value) {
		posterController?.abort();
		posterController = null;
		revokePoster();
		stopPlayback();
	}
});
watch(() => props.context.inViewport, value => {
	if (value && canLoad.value) void loadPoster();
	else if (!value) {
		posterController?.abort();
		posterController = null;
		stopPlayback();
	}
});
watch(apiSession, () => { revokePoster(); stopPlayback(); });
watch(() => [props.video, props.index, props.cachePostId, props.contentPostId, props.target, sourceNoteId.value], () => {
	activeVideo.value = props.video;
	terminalError.value = false;
	state.value = 'idle';
	revokePoster();
	stopPlayback();
	if (canLoad.value) void loadPoster();
});
onMounted(() => { if (canLoad.value) void loadPoster(); });
onBeforeUnmount(() => {
	posterController?.abort();
	stopPlayback();
	revokePoster();
});
</script>

<style lang="scss" module>
.video { min-width: 0; margin: 0; padding: 0 10px 10px; }
.posterButton { position: relative; display: block; width: 100%; aspect-ratio: 16 / 9; overflow: hidden; border-radius: var(--MI-radius); background: var(--MI_THEME-bg); cursor: pointer; }
.posterButton:disabled { cursor: default; }
.posterButton:focus-visible { outline: 3px solid var(--MI_THEME-accent); outline-offset: 2px; }
.poster { display: block; width: 100%; height: 100%; object-fit: cover; object-position: center; }
.posterPlaceholder { display: grid; width: 100%; height: 100%; place-items: center; color: var(--MI_THEME-fgTransparentWeak); font-size: 2em; }
.playIcon { position: absolute; inset: 0; display: grid; place-items: center; color: #fff; font-size: 3em; text-shadow: 0 1px 8px rgb(0 0 0 / 65%); background: rgb(0 0 0 / 18%); }
.details { display: flex; min-height: 2em; align-items: center; gap: 10px; padding-top: 5px; color: var(--MI_THEME-fgTransparentWeak); font-size: 0.82em; }
.duration { flex: 0 0 auto; }
.status { min-width: 0; overflow-wrap: anywhere; }
.retry { margin-inline-start: auto; color: var(--MI_THEME-accent); }
</style>
