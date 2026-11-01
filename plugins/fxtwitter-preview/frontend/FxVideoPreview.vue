<!--
SPDX-FileCopyrightText: syuilo and misskey-project
SPDX-License-Identifier: AGPL-3.0-only
-->
<template>
<MkSensitiveMediaGuard v-model:revealed="revealed" :isSensitive="isSensitive" kind="video">
	<div :class="[$style.video, { [$style.videoIdle]: embedUrl == null, [$style.videoPlaying]: embedUrl != null }]" :data-fx-video-target="target">
		<img v-if="embedUrl == null && posterSrc && !posterFailed" :class="$style.poster" :src="posterSrc" alt="動画のプレビュー" loading="lazy" @error="posterFailed = true">
		<iframe v-if="embedUrl != null" ref="iframe" :class="$style.frame" :src="embedUrl" :style="{ height: frameHeight + 'px' }" scrolling="no" sandbox="allow-popups allow-popups-to-escape-sandbox allow-scripts allow-same-origin" allow="fullscreen; web-share" referrerpolicy="strict-origin-when-cross-origin" title="Xの投稿" @error="failEmbed(generation)"></iframe>
		<button v-if="canPlay && embedUrl == null" type="button" :class="$style.play" data-fx-video-play :disabled="embedState === 'loading'" @click.stop="play">{{ embedState === 'loading' ? '接続中…' : 'Xの投稿を表示' }}</button>
		<div v-if="embedState === 'failed'" :class="$style.videoError">埋め込みを表示できませんでした。</div>
	</div>
	<a :href="sourceHref" target="_blank" rel="nofollow noopener noreferrer" @click.stop>Xで投稿を開く</a>
</MkSensitiveMediaGuard>
</template>

<script lang="ts" setup>
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { api, apiSession } from './api.js';
import type { PreviewContext } from '@/plugin-api.js';
import type { PublicVideo } from './api.js';
import { MkSensitiveMediaGuard } from '@/plugin-api.js';

const props = defineProps<{ video: PublicVideo; posterSrc: string | null; cachePostId: string; contentPostId: string; target: 'main' | 'quote'; context: PreviewContext; isSensitive?: boolean }>();
const iframe = ref<HTMLIFrameElement | null>(null);
const revealed = ref(false);
const posterFailed = ref(false);
const embedUrl = ref<string | null>(null);
const embedState = ref<'idle' | 'loading' | 'failed'>('idle');
const frameHeight = ref(360);
const sourceHref = computed(() => 'https://x.com/i/status/' + props.contentPostId);
const canPlay = computed(() => props.context.detail && props.context.revealed && props.context.active && props.context.inViewport && revealed.value && props.context.source?.kind === 'note' && props.video.embedToken != null && props.video.provider === 'x');
let nonce = '';
let generation = 0;
let controller: AbortController | null = null;
let frameTimer: number | null = null;

function stop(): void {
	generation++;
	controller?.abort(); controller = null;
	if (frameTimer != null) window.clearTimeout(frameTimer);
	frameTimer = null; embedUrl.value = null; nonce = ''; embedState.value = 'idle'; frameHeight.value = 360;
}

function validatedEmbedURL(value: unknown): string | null {
	if (typeof value !== 'string' || !value.startsWith('https://platform.twitter.com/embed/index.html?')) return null;
	try {
		const parsed = new URL(value);
		if (parsed.origin !== 'https://platform.twitter.com' || parsed.protocol !== 'https:' || parsed.username !== '' || parsed.password !== '' || parsed.port !== '' || parsed.pathname !== '/embed/index.html' || parsed.hash !== '') return null;
		const expected: Record<string, string> = { id: props.contentPostId, embedId: nonce, theme: 'light', lang: 'ja', hideCard: 'false', hideThread: 'false', dnt: 'true' };
		if (Array.from(parsed.searchParams).length !== Object.keys(expected).length || Object.entries(expected).some(([key, expectedValue]) => parsed.searchParams.getAll(key).length !== 1 || parsed.searchParams.get(key) !== expectedValue)) return null;
		return parsed.href;
	} catch { return null; }
}

function failEmbed(current: number): void {
	if (current !== generation) return;
	if (frameTimer != null) window.clearTimeout(frameTimer);
	frameTimer = null; controller?.abort(); controller = null; embedUrl.value = null; embedState.value = 'failed';
}

function playbackIsActive(): boolean {
	return canPlay.value;
}

async function play(): Promise<void> {
	const source = props.context.source;
	if (!canPlay.value || source?.kind !== 'note' || embedState.value === 'loading') return;
	const current = ++generation;
	const request = new AbortController(); controller = request;
	const bytes = new Uint8Array(16); crypto.getRandomValues(bytes);
	nonce = Array.from(bytes, value => value.toString(16).padStart(2, '0')).join('');
	embedState.value = 'loading';
	try {
		const result = await api<{ url: string }>('embed/shell', { noteId: source.noteId, postId: props.cachePostId, token: props.video.embedToken, target: props.target, theme: 'light', lang: 'ja', nonce }, { signal: request.signal });
		// Both the signal and reactive context may change while the host API is pending.
		if (request.signal.aborted || current !== generation || !playbackIsActive()) return;
		const url = validatedEmbedURL(result.url);
		if (url == null) throw new Error('invalid official embed URL');
		embedUrl.value = url;
		// A load event cannot attest that the remote post rendered. Only a
		// source- and nonce-checked resize message acknowledges the embed.
		frameTimer = window.setTimeout(() => failEmbed(current), 15_000);
	} catch { failEmbed(current); }
}

function onMessage(event: MessageEvent): void {
	if (embedUrl.value == null || event.origin !== 'https://platform.twitter.com' || event.source !== iframe.value?.contentWindow) return;
	const embed = (event.data as Record<string, unknown> | null)?.['twttr.embed'] as { method?: unknown; id?: unknown; params?: Array<{ height?: unknown }> } | undefined;
	if (embed?.method !== 'twttr.private.resize' || embed.id !== nonce) return;
	const height = embed.params?.[0]?.height;
	if (typeof height !== 'number' || !Number.isFinite(height)) return;
	if (frameTimer != null) window.clearTimeout(frameTimer);
	frameTimer = null; frameHeight.value = Math.max(180, Math.min(1000, height));
}

watch(canPlay, value => { if (!value) stop(); });
watch(apiSession, stop);
watch(() => [props.video, props.contentPostId, props.cachePostId, props.target, props.context.source?.noteId], () => { posterFailed.value = false; stop(); });
onMounted(() => window.addEventListener('message', onMessage));
onBeforeUnmount(() => { stop(); window.removeEventListener('message', onMessage); });
</script>

<style lang="scss" module>
.poster { display: block; width: 100%; max-height: 420px; object-fit: cover; background: var(--MI_THEME-bg); }
.video { position: relative; background: var(--MI_THEME-bg); }
.videoPlaying { display: flex; justify-content: center; width: 100%; }
.videoIdle { min-height: 220px; aspect-ratio: 16 / 9; overflow: hidden; }
.videoIdle::after { position: absolute; z-index: 1; inset: 0; content: ''; background: rgba(0, 0, 0, 0.22); pointer-events: none; }
.videoIdle > .poster { position: absolute; inset: 0; width: 100%; height: 100%; max-height: none; object-fit: cover; }
.frame { display: block; flex: 0 1 560px; width: 100%; max-width: 560px; margin-inline: auto; overflow: hidden; border: 0; background: var(--MI_THEME-panel); }
.externalVideoLink { position: absolute; z-index: 2; top: 8px; right: 8px; padding: 6px 10px; border-radius: 999px; color: #fff; background: rgba(0, 0, 0, 0.78); font-size: 0.78em; text-decoration: none; }
.externalVideoLink:focus-visible { outline: 3px solid var(--MI_THEME-accent); outline-offset: 2px; }
.play { position: absolute; z-index: 2; top: 50%; left: 50%; transform: translate(-50%, -50%); width: max-content; max-width: calc(100% - 28px); padding: 10px 16px; border: 0; border-radius: 999px; color: white; background: rgba(0, 0, 0, 0.84); font: inherit; cursor: pointer; }
.play:focus-visible { outline: 3px solid var(--MI_THEME-accent); outline-offset: 2px; }
.videoError { position: absolute; z-index: 2; right: 12px; bottom: 10px; left: 12px; color: white; text-align: center; font-size: 0.85em; pointer-events: none; }
@media (max-width: 480px) {
	.videoIdle { min-height: 180px; }
	.play { width: calc(100% - 28px); }
}
</style>
