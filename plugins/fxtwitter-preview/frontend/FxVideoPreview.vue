<!--
SPDX-FileCopyrightText: syuilo and misskey-project
SPDX-License-Identifier: AGPL-3.0-only
-->

<template>
<MkSensitiveMediaGuard v-model:revealed="revealed" :isSensitive="isSensitive" kind="video" :class="{ [$style.videoGuardTile]: tileAspect }">
	<div
		:class="[$style.video, $style.videoIdle, { [$style.videoTile]: tileAspect }]"
		:data-fx-video-target="target"
		:data-video-index="videoIndex"
	>
		<div :class="$style.posterGrid" :data-video-poster-count="posterSlots.length">
			<template v-for="poster in posterSlots" :key="poster.index">
				<img v-if="poster.src != null && !failedPosterIndices.has(poster.index)" :class="$style.poster" :src="poster.src" alt="動画のプレビュー" loading="lazy" @error="markPosterFailed(poster.index)">
				<div v-else :class="$style.posterPlaceholder" aria-hidden="true"/>
			</template>
	</div>
		<button v-if="canPlay" ref="playButton" type="button" :class="$style.play" data-fx-video-play :aria-label="playerModalOpen ? '動画を再生中' : embedState === 'loading' ? '接続中…' : embedState === 'failed' ? 'もう一度試す' : '動画を再生'" :disabled="embedState === 'loading' || embedUrl != null" @click.stop="play">
			<i class="ti ti-player-play" aria-hidden="true"></i>
			<span>{{ embedState === 'loading' ? '接続中…' : embedState === 'failed' ? 'もう一度試す' : '動画を再生' }}</span>
		</button>
		<div v-if="embedState === 'failed'" :class="$style.videoError">{{ errorMessage }}</div>
		<a v-if="showPostFallback" :class="$style.videoFallback" :href="postHref" target="_blank" rel="nofollow noopener noreferrer">動画をXで開く</a>
	</div>
	<Teleport to="body">
		<MkModal v-if="playerModalOpen" ref="modal" :preferType="'dialog'" :zPriority="'high'" :returnFocusTo="playButton" @click="closePlayer()" @esc="closePlayer()" @closed="onModalClosed">
			<div v-if="embedUrl != null" :class="$style.playerDialog" :style="playerDialogLayout" data-fx-video-modal role="dialog" aria-modal="true" aria-label="動画プレビュー" @click.stop>
				<button type="button" :class="$style.modalClose" data-fx-video-modal-close aria-label="動画を閉じる" @click="closePlayer()">
					<i class="ti ti-x" aria-hidden="true"></i>
				</button>
				<iframe
					ref="iframe"
					:class="$style.frame"
					:src="embedUrl"
					:style="{ height: `${frameHeight}px`, maxWidth: '560px' }"
					scrolling="no"
					sandbox="allow-popups allow-popups-to-escape-sandbox allow-scripts allow-same-origin"
					allow="fullscreen; web-share"
					referrerpolicy="strict-origin-when-cross-origin"
					:title="video.provider === 'youtube' ? 'YouTube video player' : 'Xの投稿動画'"
					@load="onFrameLoad"
				/>
				<a v-if="youtubeWatchUrl != null" :class="$style.externalVideoLink" :href="youtubeWatchUrl" target="_blank" rel="nofollow noopener noreferrer" aria-label="YouTubeで開く" @click.stop>YouTubeで開く</a>
			</div>
		</MkModal>
	</Teleport>
</MkSensitiveMediaGuard>
</template>

<script lang="ts" setup>
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import MkModal from '@/components/MkModal.vue';
import { MkSensitiveMediaGuard } from '@/plugin-api.js';
import type { PreviewContext } from '@/plugin-api.js';
import { apiSessionEpoch, fetchPluginResource, openPluginMedia } from './api.js';
import type { PluginMediaItem } from '@/plugin-api.js';
import type { PublicVideo } from './api.js';

type RefreshedPlayback = { video: PublicVideo; videoIndex: number };
type RefreshPlayback = (request: {
	target: 'main' | 'quote';
	video: PublicVideo;
	videoIndex?: number;
	contentPostId: string;
	signal: AbortSignal;
}) => Promise<RefreshedPlayback | null>;

const props = defineProps<{
	video: PublicVideo;
	posterSrc?: string | null;
	posterSources?: Array<string | null>;
	cachePostId: string;
	contentPostId: string;
	target: 'main' | 'quote';
	context: PreviewContext;
	isSensitive?: boolean;
	playbackMode?: 'officialEmbed' | 'nativeProxy' | 'linkOnly';
	videoIndex?: number;
	postHref?: string;
	tileAspect?: boolean;
	refreshPlayback?: RefreshPlayback;
}>();

const iframe = ref<HTMLIFrameElement | null>(null);
const modal = ref<{ close: () => void } | null>(null);
const playButton = ref<HTMLElement | null>(null);
const revealed = ref(false);
const failedPosterIndices = ref<Set<number>>(new Set());
const embedUrl = ref<string | null>(null);
const embedState = ref<'idle' | 'loading' | 'ready' | 'failed'>('idle');
const errorMessage = ref('動画を再生できませんでした。');
const playerModalOpen = ref(false);
const frameHeight = ref(360);
let nonce = '';
let generation = 0;
let frameLoadTimer: ReturnType<typeof setTimeout> | null = null;
let mediaAbort: AbortController | null = null;
let refreshingCapability = false;

const playerDialogLayout: import('vue').CSSProperties = {
	boxSizing: 'border-box',
	width: '100%',
	maxWidth: '592px',
	maxHeight: '100%',
	margin: 'auto',
};

const posterSlots = computed(() => {
	const sources = props.posterSources ?? (props.posterSrc == null ? [] : [props.posterSrc]);
	return sources.map((src, index) => ({ src, index }));
});
const effectivePlaybackMode = computed(() => props.playbackMode ?? 'officialEmbed');
const youtubeWatchUrl = computed(() => {
	const id = props.video.providerVideoId;
	if (props.video.provider !== 'youtube' || id == null || !/^[A-Za-z0-9_-]{11}$/.test(id)) return null;
	return `https://www.youtube.com/watch?v=${id}`;
});
const canPlay = computed(() => props.context.revealed && props.context.active && props.context.inViewport && revealed.value &&
	props.context.source?.kind === 'note' && props.video.embedToken != null &&
	(effectivePlaybackMode.value === 'nativeProxy'
		? props.video.provider === 'youtube' || (props.video.provider === 'x' && props.video.playbackAvailable === true)
		: effectivePlaybackMode.value === 'officialEmbed' && (props.video.provider === 'x' || props.video.provider === 'youtube')));
const showPostFallback = computed(() => effectivePlaybackMode.value === 'nativeProxy' && props.video.provider === 'x' && props.postHref != null &&
	(props.video.playbackAvailable !== true || embedState.value === 'failed'));

function newNonce(): string {
	const bytes = new Uint8Array(16);
	crypto.getRandomValues(bytes);
	return Array.from(bytes, value => value.toString(16).padStart(2, '0')).join('');
}

function stop(): void {
	generation++;
	mediaAbort?.abort();
	mediaAbort = null;
	if (frameLoadTimer != null) clearTimeout(frameLoadTimer);
	frameLoadTimer = null;
	embedUrl.value = null;
	nonce = '';
	embedState.value = 'idle';
	errorMessage.value = '動画を再生できませんでした。';
	frameHeight.value = props.video.provider === 'youtube' ? 315 : 360;
}

function closePlayer(): void {
	const modalInstance = modal.value;
	stop();
	if (modalInstance != null) modalInstance.close();
	else playerModalOpen.value = false;
}

function onModalClosed(): void {
	stop();
	playerModalOpen.value = false;
}

function validatedEmbedURL(value: unknown): string | null {
	if (typeof value !== 'string') return null;
	try {
		const parsed = new URL(value);
		if (parsed.username !== '' || parsed.password !== '' || parsed.port !== '' || parsed.hash !== '') return null;
		if (props.video.provider === 'x') {
			if (parsed.protocol !== 'https:' || parsed.hostname !== 'platform.twitter.com' || parsed.pathname !== '/embed/Tweet.html') return null;
			const expected: Record<string, string> = {
				frame: 'false', maxWidth: '560px', width: '550px', id: props.contentPostId, embedId: nonce,
				theme: 'light', lang: 'ja', hideCard: 'false', hideThread: 'false', dnt: 'true',
			};
			const entries = Array.from(parsed.searchParams.entries());
			if (entries.length !== Object.keys(expected).length || entries.some(([key, value]) => expected[key] !== value) ||
				Object.entries(expected).some(([key, value]) => parsed.searchParams.getAll(key).length !== 1 || parsed.searchParams.get(key) !== value)) return null;
			return parsed.href;
		}
		if (props.video.provider === 'youtube' && props.video.providerVideoId != null) {
			const expected = `https://www.youtube.com/embed/${props.video.providerVideoId}?autoplay=0&playsinline=1`;
			return parsed.href === expected ? expected : null;
		}
		return null;
	} catch {
		return null;
	}
}

function failEmbed(current: number): void {
	if (current !== generation) return;
	if (frameLoadTimer != null) clearTimeout(frameLoadTimer);
	frameLoadTimer = null;
	embedState.value = 'failed';
	embedUrl.value = null;
	if (playerModalOpen.value) {
		modal.value?.close();
		playerModalOpen.value = false;
	}
}

function onFrameLoad(): void {
	if (embedUrl.value == null || props.video.provider !== 'youtube') return;
	if (frameLoadTimer != null) clearTimeout(frameLoadTimer);
	frameLoadTimer = null;
	embedState.value = 'ready';
}

function markPosterFailed(index: number): void {
	failedPosterIndices.value = new Set(failedPosterIndices.value).add(index);
}

async function play(): Promise<void> {
	const source = props.context.source;
	if (!canPlay.value || playerModalOpen.value || props.video.embedToken == null || source?.kind !== 'note') return;
	const current = ++generation;
	embedState.value = 'loading';
	errorMessage.value = '動画を再生できませんでした。';
	let selectedVideo = props.video;
	let selectedIndex = props.videoIndex;
	let refreshed = false;
	const controller = new AbortController();
	mediaAbort = controller;
	try {
		if (capabilityExpired(selectedVideo) || selectedVideo.embedToken == null) {
			const renewed = await refreshCapability(selectedVideo, selectedIndex, current, controller.signal);
			if (renewed == null) throw new Error('video capability could not be renewed');
			selectedVideo = renewed.video;
			selectedIndex = renewed.videoIndex;
			refreshed = true;
		}
		if (effectivePlaybackMode.value === 'nativeProxy' && selectedVideo.provider === 'x') {
			await playNativeVideo(current, source.noteId, selectedVideo, selectedIndex, controller.signal, !refreshed);
			return;
		}
		await playOfficialVideo(current, source.noteId, selectedVideo, controller.signal, !refreshed);
	} catch {
		failEmbed(current);
	} finally {
		if (mediaAbort === controller) mediaAbort = null;
	}
}

function capabilityExpired(video: PublicVideo): boolean {
	if (video.embedExpiresAt == null) return false;
	const expiresAt = Date.parse(video.embedExpiresAt);
	return Number.isFinite(expiresAt) && expiresAt <= Date.now();
}

async function refreshCapability(video: PublicVideo, videoIndex: number | undefined, current: number, signal: AbortSignal): Promise<RefreshedPlayback | null> {
	if (props.refreshPlayback == null || signal.aborted || current !== generation) return null;
	refreshingCapability = true;
	try {
		const renewed = await props.refreshPlayback({
			target: props.target,
			video,
			videoIndex,
			contentPostId: props.contentPostId,
			signal,
		});
		await nextTick();
		if (signal.aborted || current !== generation || !canPlay.value || renewed?.video.embedToken == null) return null;
		return renewed;
	} finally {
		refreshingCapability = false;
	}
}

async function playOfficialVideo(current: number, noteId: string, video: PublicVideo, signal: AbortSignal, refreshOnRejection: boolean): Promise<void> {
	nonce = newNonce();
	const response = await fetchPluginResource('/api/plugin/fxtwitter-preview/embed/shell', {
		method: 'POST',
		cache: 'no-store',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ noteId, postId: props.cachePostId, token: video.embedToken, target: props.target, theme: 'light', lang: 'ja', nonce }),
		signal,
	});
	if (response.status === 404 && refreshOnRejection && !signal.aborted && current === generation) {
		const renewed = await refreshCapability(video, props.videoIndex, current, signal);
		if (renewed != null) return await playOfficialVideo(current, noteId, renewed.video, signal, false);
	}
	if (!response.ok) throw new Error('Embed capability is unavailable');
	const result = await response.json() as { url?: unknown };
	if (signal.aborted || current !== generation || !canPlay.value) return;
	const url = validatedEmbedURL(result.url);
	if (url == null) throw new Error('invalid official embed URL');
	embedUrl.value = url;
	playerModalOpen.value = true;
	frameLoadTimer = setTimeout(() => failEmbed(current), 15_000);
}

async function playNativeVideo(current: number, noteId: string, video: PublicVideo, videoIndex: number | undefined, signal: AbortSignal, refreshOnRejection: boolean): Promise<void> {
	if (!Number.isSafeInteger(videoIndex) || (videoIndex ?? -1) < 0) throw new Error('video index is unavailable');
	try {
		const response = await fetchPluginResource('/api/plugin/fxtwitter-preview/media/video', {
			method: 'POST',
			cache: 'no-store',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ noteId, postId: props.cachePostId, target: props.target, index: videoIndex, token: video.embedToken }),
			signal,
		});
		if (response.status === 404 && refreshOnRejection && !signal.aborted && current === generation) {
			const renewed = await refreshCapability(video, videoIndex, current, signal);
			if (renewed != null) return await playNativeVideo(current, noteId, renewed.video, renewed.videoIndex, signal, false);
		}
		const contentType = response.headers.get('Content-Type')?.split(';', 1)[0]?.trim().toLowerCase();
		const contentLength = response.headers.get('Content-Length');
		if (response.status === 429) {
			errorMessage.value = '動画の再生が混み合っています。少し待ってからお試しください。';
			throw new Error('Video playback is rate limited');
		}
		if (!response.ok || contentType !== 'video/mp4' || (contentLength != null && (!/^\d+$/.test(contentLength) || Number(contentLength) > 32 * 1024 * 1024))) {
			throw new Error('Video response is unavailable or invalid');
		}
		const blob = await readBoundedVideo(response, signal);
		if (blob.size === 0 || blob.size > 32 * 1024 * 1024 || blob.type.split(';', 1)[0]?.trim().toLowerCase() !== 'video/mp4') throw new Error('Video response is too large or invalid');
		if (current !== generation || signal.aborted || !canPlay.value) return;
		const item: PluginMediaItem = {
			id: `${props.contentPostId}:${videoIndex}`,
			type: 'video',
			blob,
			filename: `x-${props.contentPostId}-${(videoIndex ?? 0) + 1}.mp4`,
			isSensitive: props.isSensitive === true,
			initiallyRevealed: true,
		};
		await openPluginMedia([item], item.id);
		if (current === generation) embedState.value = 'idle';
	} catch (error) {
		if (!signal.aborted) throw error;
	}
}

async function readBoundedVideo(response: Response, signal: AbortSignal): Promise<Blob> {
	const reader = response.body?.getReader();
	if (reader == null) throw new Error('Video response has no stream');
	const chunks: ArrayBuffer[] = [];
	let size = 0;
	try {
		while (true) {
			if (signal.aborted) throw new DOMException('Aborted', 'AbortError');
			const { done, value } = await reader.read();
			if (done) break;
			size += value.byteLength;
			if (size > 32 * 1024 * 1024) {
				await reader.cancel();
				throw new Error('Video response exceeds the limit');
			}
			chunks.push(new Uint8Array(value).buffer);
		}
	} finally {
		reader.releaseLock();
	}
	return new Blob(chunks, { type: 'video/mp4' });
}

function onMessage(event: MessageEvent): void {
	if (props.video.provider !== 'x' || embedUrl.value == null || event.origin !== 'https://platform.twitter.com' || event.source !== iframe.value?.contentWindow) return;
	const embed = (event.data as Record<string, unknown> | null)?.['twttr.embed'] as { method?: unknown; id?: unknown; params?: Array<{ height?: unknown }> } | undefined;
	if (embed?.method !== 'twttr.private.resize' || embed.id !== nonce) return;
	const height = embed.params?.[0]?.height;
	if (typeof height !== 'number' || !Number.isFinite(height) || height <= 0) return;
	if (frameLoadTimer != null) clearTimeout(frameLoadTimer);
	frameLoadTimer = null;
	embedState.value = 'ready';
	frameHeight.value = Math.max(180, Math.min(1000, height));
}

watch(canPlay, enabled => { if (!enabled) closePlayer(); });
watch(() => [props.contentPostId, props.cachePostId, props.target, apiSessionEpoch.value, props.context.source?.noteId], () => {
	failedPosterIndices.value = new Set();
	closePlayer();
});
watch(() => [props.video.provider, props.video.providerVideoId, props.video.mediaIdentity, props.video.durationMs, props.video.playbackAvailable], () => {
	if (refreshingCapability) return;
	failedPosterIndices.value = new Set();
	closePlayer();
});
onMounted(() => {
	frameHeight.value = props.video.provider === 'youtube' ? 315 : 360;
	window.addEventListener('message', onMessage);
});
onBeforeUnmount(() => {
	stop();
	modal.value?.close();
	window.removeEventListener('message', onMessage);
});
</script>

<style lang="scss" module>
.posterGrid { position: absolute; inset: 0; display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); grid-template-rows: repeat(2, minmax(0, 1fr)); gap: 2px; overflow: hidden; }
.posterGrid[data-video-poster-count="1"] { grid-template-columns: 1fr; grid-template-rows: 1fr; }
.posterGrid[data-video-poster-count="2"] { grid-template-columns: repeat(2, minmax(0, 1fr)); grid-template-rows: 1fr; }
.poster { display: block; width: 100%; height: 100%; object-fit: cover; background: var(--MI_THEME-bg); }
.posterPlaceholder { min-width: 0; min-height: 0; background: var(--MI_THEME-bg); }
.video { position: relative; background: var(--MI_THEME-bg); }
.videoIdle { width: 100%; box-sizing: border-box; aspect-ratio: 16 / 9; overflow: hidden; }
.videoGuardTile { height: 100%; }
.videoGuardTile > div { height: 100%; }
.videoTile { width: 100%; height: 100%; min-height: 0; aspect-ratio: auto; }
.videoIdle::after { position: absolute; z-index: 1; inset: 0; content: ''; background: rgba(0, 0, 0, 0.22); pointer-events: none; }
.frame { display: block; flex: 0 1 560px; width: 100%; max-width: 560px; margin-inline: auto; overflow: hidden; border: 0; background: var(--MI_THEME-panel); }
.externalVideoLink { position: absolute; z-index: 2; top: 8px; right: 8px; padding: 6px 10px; border-radius: 999px; color: #fff; background: rgba(0, 0, 0, 0.78); font-size: 0.78em; text-decoration: none; }
.externalVideoLink:focus-visible { outline: 3px solid var(--MI_THEME-accent); outline-offset: 2px; }
.playerDialog { position: relative; display: grid; place-items: center; padding: 16px; overflow: auto; border-radius: 12px; background: var(--MI_THEME-panel); box-shadow: 0 8px 40px rgba(0, 0, 0, 0.35); }
.modalClose { position: sticky; z-index: 2; top: 0; justify-self: end; width: 40px; height: 40px; margin: -8px -8px -40px 0; border: 0; border-radius: 50%; color: var(--MI_THEME-fg); background: var(--MI_THEME-panel); font: inherit; font-size: 20px; cursor: pointer; }
.modalClose:focus-visible { outline: 3px solid var(--MI_THEME-accent); outline-offset: 2px; }
/* MkMediaVideo.vue の再生アイコンの寸法・配色・hover scaleをplugin内で再現する。 */
.play { position: absolute; z-index: 2; top: 50%; left: 50%; display: grid; width: 50px; height: 50px; place-items: center; translate: -50% -50%; border: 0; border-radius: 50%; color: var(--MI_THEME-fgOnAccent); background: var(--MI_THEME-accent); font: inherit; font-size: 120%; scale: 1; transition: scale 100ms ease; cursor: pointer; }
.play:hover { scale: 1.2; }
.play span { position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; overflow: hidden; clip: rect(0, 0, 0, 0); white-space: nowrap; border: 0; }
.play:focus-visible { outline: 3px solid var(--MI_THEME-accent); outline-offset: 2px; }
.videoError { position: absolute; z-index: 2; right: 12px; bottom: 10px; left: 12px; color: white; text-align: center; font-size: 0.85em; pointer-events: none; }
.videoFallback { position: absolute; z-index: 2; right: 8px; bottom: 8px; padding: 5px 8px; border-radius: 999px; color: white; background: rgba(0, 0, 0, 0.78); font-size: 0.78em; text-decoration: none; }
</style>
