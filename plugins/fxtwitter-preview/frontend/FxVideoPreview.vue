<!--
SPDX-FileCopyrightText: syuilo and misskey-project
SPDX-License-Identifier: AGPL-3.0-only
-->
<template>
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
		<span :class="$style.playOverlay" aria-hidden="true">
			<span :class="$style.playPill" data-fx-video-play-pill>
				<i :class="state === 'loading' ? 'ti ti-loader-2' : 'ti ti-player-play'"></i>
				<span v-if="durationLabel" data-fx-video-duration>{{ durationLabel }}</span>
			</span>
		</span>
	</button>
	<figcaption v-if="state === 'error'" :class="$style.details">
		<span :class="$style.status" role="status" :data-fx-video-error="errorCode ?? undefined">{{ errorMessage }}</span>
		<a v-if="state === 'error' && postHref" :class="$style.sourceLink" :href="postHref" target="_blank" rel="nofollow noopener noreferrer" @click.stop>{{ sourceLinkLabel }}</a>
		<button v-if="state === 'error' && !terminalError" type="button" class="_button" :class="$style.retry" @click.stop="play">再試行</button>
	</figcaption>
</figure>
</template>

<script lang="ts" setup>
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { api, apiSession, fetchPluginResource, openPluginMedia } from './api.js';
import type { PreviewContext, PluginMediaItem } from '@/plugin-api.js';
import type { PublicVideo } from './api.js';
import { loadPreview } from './preview-loader.js';
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
	// センシティブの確認は、この動画を並べる格子 (FxMediaGrid) がまとめて行う。
	revealed: boolean;
}>();

const MAX_VIDEO_BYTES = 64 * 1024 * 1024;
const posterSrc = ref<string | null>(null);
const state = ref<'idle' | 'loading' | 'error'>('idle');
const terminalError = ref(false);
const errorCode = ref<string | null>(null);

// サーバーが video.twimg.com から動画を取れなかった原因 (#57)。terminal は、再試行しても直らないもの。
const sourceErrors: Record<string, { message: string; terminal: boolean }> = {
	VIDEO_REMOVED_DMCA: { message: '著作権の申し立てにより、Xで削除された動画です。', terminal: true },
	VIDEO_SOURCE_FORBIDDEN: { message: 'Xが動画の配信を拒否しました。', terminal: true },
	VIDEO_SOURCE_GONE: { message: 'Xで削除された動画です。', terminal: true },
	VIDEO_SOURCE_TIMEOUT: { message: 'Xからの応答が時間内にありませんでした。時間をおいて再試行してください。', terminal: false },
	VIDEO_SOURCE_FAILED: { message: 'Xから動画を取得できませんでした。時間をおいて再試行してください。', terminal: false },
	VIDEO_TOO_LARGE: { message: 'アプリ内で再生できる大きさを超えています。', terminal: true },
};

const errorMessage = computed(() => {
	const known = errorCode.value != null ? sourceErrors[errorCode.value] as { message: string } | undefined : undefined;
	if (known) return known.message;
	return terminalError.value ? '動画へのアクセスが許可されていません。' : '動画を読み込めませんでした。';
});
const activeVideo = ref(props.video);
let posterController: AbortController | null = null;
let playbackController: AbortController | null = null;
let posterObjectUrl: string | null = null;
let generation = 0;

const sourceNoteId = computed(() => props.context.source?.kind === 'note' ? props.context.source.noteId : null);
const postHref = computed(() => /^[0-9]{2,20}$/.test(props.contentPostId) ? `https://x.com/i/status/${props.contentPostId}` : null);
const sourceLinkLabel = computed(() => props.target === 'quote' ? 'Xで引用投稿を開く' : 'Xで投稿を開く');
const canAccess = computed(() => props.context.revealed && props.context.active && props.revealed && sourceNoteId.value != null);
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
	errorCode.value = null;
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
			const code = (error as { code?: unknown } | null)?.code;
			errorCode.value = typeof code === 'string' ? code : null;
			const known = errorCode.value != null ? sourceErrors[errorCode.value] as { terminal: boolean } | undefined : undefined;
			terminalError.value = isHttpForbidden(error) || known?.terminal === true;
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
// 表示の可否は、格子のセンシティブの部品 (非同期で読み込まれる) が決めた後に確定する。
// 確定した時点でサムネイルの取得を始めないと、最初の表示でサムネイルが出ない。
watch(canLoad, value => { if (value) void loadPoster(); });
watch(() => props.context.inViewport, value => {
	if (!value) {
		posterController?.abort();
		posterController = null;
		stopPlayback();
	}
});
watch(apiSession, () => { revokePoster(); stopPlayback(); });
watch(() => [props.video, props.index, props.cachePostId, props.contentPostId, props.target, sourceNoteId.value], () => {
	activeVideo.value = props.video;
	terminalError.value = false;
	errorCode.value = null;
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
.video { position: relative; width: 100%; height: 100%; min-width: 0; margin: 0; padding: 0; }
.posterButton { position: relative; display: block; width: 100%; height: 100%; overflow: hidden; background: var(--MI_THEME-bg); cursor: pointer; }
.posterButton:disabled { cursor: default; }
.posterButton:focus-visible { outline: 3px solid var(--MI_THEME-accent); outline-offset: 2px; }
.poster { display: block; width: 100%; height: 100%; object-fit: cover; object-position: center; }
.posterPlaceholder { display: grid; width: 100%; height: 100%; place-items: center; color: var(--MI_THEME-fgTransparentWeak); font-size: 2em; }
.playOverlay { position: absolute; inset: 0; display: grid; place-items: center; background: rgb(0 0 0 / 18%); }
.playPill { display: inline-flex; align-items: center; gap: 8px; padding: 8px 14px; border-radius: 999px; color: #fff; background: rgb(0 0 0 / 60%); font-size: 1.1em; font-variant-numeric: tabular-nums; }
.playPill > i { font-size: 1.4em; }
.details { position: absolute; right: 0; bottom: 0; left: 0; display: flex; flex-wrap: wrap; align-items: center; gap: 10px; padding: 6px 10px; color: #fff; background: rgb(0 0 0 / 65%); font-size: 0.82em; }
.status { min-width: 0; overflow-wrap: anywhere; }
.sourceLink { color: #fff; text-decoration: underline; white-space: nowrap; }
.retry { margin-inline-start: auto; color: #fff; text-decoration: underline; }
</style>
