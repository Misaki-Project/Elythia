<!--
SPDX-FileCopyrightText: syuilo and misskey-project
SPDX-License-Identifier: AGPL-3.0-only
-->

<template>

<article :class="$style.card" :aria-label="viewData.state === 'pending' ? 'Xの投稿を取得しています' : viewData.state === 'unavailable' ? 'X投稿プレビューの状態' : undefined">
	<template v-if="(viewData.state === 'ready' || viewData.state === 'stale') && viewData.post">
	<header :class="$style.header">
		<a v-if="avatarSrc && authorProfileUrl" :class="$style.avatarLink" :href="authorProfileUrl" target="_blank" rel="nofollow noopener noreferrer" :aria-label="`${viewData.post.author.name}のXプロフィール`" @click.stop>
			<img :class="$style.avatar" :src="avatarSrc" alt="" loading="lazy">
		</a>
		<img v-else-if="avatarSrc" :class="$style.avatar" :src="avatarSrc" alt="" loading="lazy">
		<div :class="$style.identity">
			<a v-if="authorProfileUrl" :class="$style.identityLink" :href="authorProfileUrl" target="_blank" rel="nofollow noopener noreferrer" @click.stop>
				<strong>{{ viewData.post.author.name }}</strong>
				<span>@{{ viewData.post.author.screenName }}</span>
			</a>
			<template v-else><strong>{{ viewData.post.author.name }}</strong><span>@{{ viewData.post.author.screenName }}</span></template>
		</div>
	</header>
	<p :class="$style.text"><template v-for="segment in displayedTextSegments" :key="segment.key"><MkUrl v-if="segment.kind === 'url' && segment.href" :url="segment.href" :aria-label="segment.text" @click.stop/><MkA v-else-if="segment.kind === 'hashtag' && segment.href" class="_link" :to="segment.href" @click.stop>{{ segment.text }}</MkA><a v-else-if="segment.kind === 'mention' && segment.href" class="_link" :href="segment.href" target="_blank" rel="nofollow noopener noreferrer" @click.stop>{{ segment.text }}</a><span v-else>{{ segment.text }}</span></template><span v-if="textSummary.truncated && !expanded">…</span></p>
	<a v-if="textSummary.truncated && !context.detail" :class="$style.more" :href="noteHref">もっと見る</a>
	<button v-else-if="textSummary.truncated && !expanded" type="button" class="_button" :class="$style.more" @click="expanded = true">もっと見る</button>
	<div v-if="viewData.state === 'stale' || viewData.stale" :class="$style.stale" role="status">
		<p>{{ retryBlocked ? '閲覧権限を確認できないため、プレビューを表示できません。' : retryTransportFailed ? (retryRateLimited ? '取得上限に達しました。時間をおいて再試行してください。' : '一時的な通信エラーが発生しました。') : errorMessage(viewData.error) }} 前回取得した内容を表示しています。</p>
		<p v-if="retryWaitMs > 0">次回再試行可能: {{ formatRetryAt(retryDeadline) }}</p>
		<p v-else-if="canRetryPreview">再試行できます。</p>
		<button v-if="canRetryPreview" type="button" class="_button" :disabled="retryLoading || retryWaitMs > 0" @click.stop="retryPreview">
			{{ retryLoading ? '再取得中…' : '再試行' }}
		</button>
	</div>
	<p v-if="!showAttachmentMedia && mediaSummary" :class="$style.mediaSummary">{{ mediaSummary }}</p>
	<FxMediaGrid v-if="showAttachmentMedia && renderedImages.length" :key="`images:${currentOwnerKey}:${viewData.post.id}`" v-model:revealed="imagesRevealed" :images="renderedImages" :totalImageCount="viewData.post.images.length" :isSensitive="viewData.post.isSensitive" @open="openImage"/>
	<p v-if="mainPhotoGridStatus" :class="$style.mediaError" role="status">{{ mainPhotoGridStatus }} <button v-if="mainPhotoGridRetryable" type="button" class="_button" :disabled="retryLoading || retryWaitMs > 0 || !canRetryMedia" @click.stop="retryPhotoGrid('main')">再試行</button></p>
	<p v-if="photoViewerLoading.main" :class="$style.mediaError" role="status" aria-busy="true">ギャラリーを準備しています…</p>
	<p v-else-if="photoViewerError.main" :class="$style.mediaError" role="status">{{ photoViewerError.main }}</p>
	<FxVideoPreview
		v-if="showAttachmentMedia && mainVideo && videoPlaybackMode !== 'nativeProxy'"
		:key="`video:${viewData.post.id}`"
		:video="mainVideo"
		:posterSrc="videoPosterSources[0]"
		:posterSources="videoPosterSources"
		:cachePostId="viewData.post.id"
		:contentPostId="viewData.post.id"
		target="main"
		:context="context"
		:isSensitive="viewData.post.isSensitive"
		:playbackMode="videoPlaybackMode"
		:refreshPlayback="refreshPlaybackCapability"
	/>
	<div v-else-if="showAttachmentMedia && videoPlaybackMode === 'nativeProxy' && mainVideos.length > 0" :class="$style.videoGrid" data-fx-video-grid="main" :data-video-count="mainVideos.length">
			<FxVideoPreview v-for="(video, index) in mainVideos" :key="`video:${viewData.post.id}:${index}`" :video="video" :posterSrc="videoPosterSources[index]" :cachePostId="viewData.post.id" :contentPostId="viewData.post.id" target="main" :videoIndex="index" :playbackMode="videoPlaybackMode" :postHref="viewData.post.url" :context="context" :isSensitive="viewData.post.isSensitive" :tileAspect="mainVideos.length === 2" :refreshPlayback="refreshPlaybackCapability"/>
	</div>
	<MkTime v-if="postCreatedAt" :class="$style.postTime" :time="postCreatedAt" mode="detail"/>
	<section
		v-if="viewData.post.quote"
		:class="$style.quote"
	>
		<header :class="$style.quoteHeader">
			<a v-if="quoteAvatarSrc && quoteAuthorProfileUrl" :href="quoteAuthorProfileUrl" target="_blank" rel="nofollow noopener noreferrer" :aria-label="`${viewData.post.quote.author.name}のXプロフィール`" @click.stop><img :class="$style.quoteAvatar" :src="quoteAvatarSrc" alt="" loading="lazy"></a>
			<img v-else-if="quoteAvatarSrc" :class="$style.quoteAvatar" :src="quoteAvatarSrc" alt="" loading="lazy">
			<div :class="$style.identity">
				<a v-if="quoteAuthorProfileUrl" :class="$style.identityLink" :href="quoteAuthorProfileUrl" target="_blank" rel="nofollow noopener noreferrer" @click.stop><strong>{{ viewData.post.quote.author.name }}</strong><span>@{{ viewData.post.quote.author.screenName }}</span></a>
				<template v-else><strong>{{ viewData.post.quote.author.name }}</strong><span>@{{ viewData.post.quote.author.screenName }}</span></template>
			</div>
		</header>
		<p :class="$style.quoteText"><template v-for="segment in quoteTextSegments" :key="segment.key"><MkUrl v-if="segment.kind === 'url' && segment.href" :url="segment.href" :aria-label="segment.text" @click.stop/><MkA v-else-if="segment.kind === 'hashtag' && segment.href" class="_link" :to="segment.href" @click.stop>{{ segment.text }}</MkA><a v-else-if="segment.kind === 'mention' && segment.href" class="_link" :href="segment.href" target="_blank" rel="nofollow noopener noreferrer" @click.stop>{{ segment.text }}</a><span v-else>{{ segment.text }}</span></template></p>
		<p v-if="!showAttachmentMedia && quoteMediaSummary" :class="$style.quoteMediaSummary">引用: {{ quoteMediaSummary }}</p>
		<FxMediaGrid v-if="showAttachmentMedia && renderedQuoteImages.length" :key="`quote-images:${currentOwnerKey}:${viewData.post.quote.id}`" v-model:revealed="quoteImagesRevealed" :images="renderedQuoteImages" :totalImageCount="viewData.post.quote.images.length" :isSensitive="viewData.post.quote.isSensitive" @open="openQuoteImage"/>
		<p v-if="quotePhotoGridStatus" :class="$style.mediaError" role="status">{{ quotePhotoGridStatus }} <button v-if="quotePhotoGridRetryable" type="button" class="_button" :disabled="retryLoading || retryWaitMs > 0 || !canRetryMedia" @click.stop="retryPhotoGrid('quote')">再試行</button></p>
		<p v-if="photoViewerLoading.quote" :class="$style.mediaError" role="status" aria-busy="true">ギャラリーを準備しています…</p>
		<p v-else-if="photoViewerError.quote" :class="$style.mediaError" role="status">{{ photoViewerError.quote }}</p>
		<FxVideoPreview
			v-if="showAttachmentMedia && quoteVideo && videoPlaybackMode !== 'nativeProxy'"
			:key="`quote-video:${viewData.post.quote.id}`"
			:video="quoteVideo"
			:posterSrc="quoteVideoPosterSources[0]"
			:posterSources="quoteVideoPosterSources"
			:cachePostId="viewData.post.id"
			:contentPostId="viewData.post.quote.id"
			target="quote"
			:context="context"
			:isSensitive="viewData.post.quote.isSensitive"
			:playbackMode="videoPlaybackMode"
		:refreshPlayback="refreshPlaybackCapability"
		/>
		<div v-else-if="showAttachmentMedia && videoPlaybackMode === 'nativeProxy' && quoteVideos.length > 0" :class="$style.videoGrid" data-fx-video-grid="quote" :data-video-count="quoteVideos.length">
			<FxVideoPreview v-for="(video, index) in quoteVideos" :key="`quote-video:${viewData.post.quote.id}:${index}`" :video="video" :posterSrc="quoteVideoPosterSources[index]" :cachePostId="viewData.post.id" :contentPostId="viewData.post.quote.id" target="quote" :videoIndex="index" :playbackMode="videoPlaybackMode" :postHref="viewData.post.quote.url" :context="context" :isSensitive="viewData.post.quote.isSensitive" :tileAspect="quoteVideos.length === 2" :refreshPlayback="refreshPlaybackCapability"/>
		</div>
		<MkTime v-if="quoteCreatedAt" :class="$style.postTime" :time="quoteCreatedAt" mode="detail"/>
	</section>
	<div v-if="mediaLoadFailed || photoHasRetryableFailure" :class="$style.mediaError" role="status">
		<p>{{ mediaLoadBlocked ? 'メディアを表示する権限を確認できません。' : 'メディアを読み込めませんでした。' }}</p>
		<button v-if="canRetryMedia" type="button" class="_button" :disabled="retryLoading || retryWaitMs > 0" @click.stop="retryPreview">
			{{ retryLoading ? '再取得中…' : 'メディアを再取得' }}
		</button>
	</div>
	<footer :class="$style.footer">
		<span aria-label="返信"><i class="ti ti-arrow-back-up"></i> {{ formatCount(viewData.post.replies) }}</span>
		<span aria-label="リポストと引用"><i class="ti ti-repeat"></i> {{ formatCount(repostCount) }}</span>
		<span aria-label="いいね"><i class="ti ti-heart"></i> {{ formatCount(viewData.post.likes) }}</span>
	</footer>
	<footer :class="$style.legacyFooter" aria-hidden="true">
		<span>返信 {{ viewData.post.replies }}</span><span>リポストと引用 {{ repostCount }}</span><span>いいね {{ viewData.post.likes }}</span>
	</footer>
	</template>
	<section v-else-if="viewData.state === 'pending'" :class="$style.pending" role="status" aria-label="Xの投稿を取得しています" aria-busy="true">
		<span>Xの投稿を取得しています</span><span :class="$style.dots" aria-hidden="true"><i></i><i></i><i></i></span>
		<a :href="sourceHref" target="_blank" rel="nofollow noopener noreferrer" @click.stop>Xで開く</a>
	</section>
	<section v-else-if="viewData.state === 'unavailable'" :class="$style.unavailable" role="status">
		<p>{{ retryBlocked || viewData.retryable === false ? '閲覧権限を確認できないため、プレビューを表示できません。' : retryTransportFailed ? (retryRateLimited ? '取得上限に達しました。時間をおいて再試行してください。' : '一時的な通信エラーが発生しました。') : errorMessage(viewData.error) }}</p>
		<p v-if="retryWaitMs > 0">次回再試行可能: {{ formatRetryAt(retryDeadline) }}</p>
		<p v-else-if="canRetryPreview">再試行できます。</p>
		<div :class="$style.unavailableActions">
			<button v-if="canRetryPreview" type="button" class="_button" :class="$style.action" :disabled="retryLoading || retryWaitMs > 0" @click.stop="retryPreview">
				{{ retryLoading ? '再取得中…' : '再試行' }}
			</button>
			<a class="_button" :class="$style.action" :href="sourceHref" target="_blank" rel="nofollow noopener noreferrer" @click.stop>Xで開く</a>
		</div>
	</section>
	<section v-else :class="$style.unavailable" role="status">
		<p>プレビューを表示できません。</p>
		<a :href="sourceHref" target="_blank" rel="nofollow noopener noreferrer" @click.stop>Xで開く</a>
	</section>
</article>
</template>

<script lang="ts" setup>
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { MkA, MkTime, MkUrl } from '@/plugin-api.js';
import type { PluginMediaItem, PreviewContext, PreviewInput } from '@/plugin-api.js';
import FxMediaGrid from './FxMediaGrid.vue';
import FxVideoPreview from './FxVideoPreview.vue';
import { apiSessionEpoch, fetchPluginResource, fetchPublicMedia, openPluginMedia, parsePreparedPublicMedia, truncatePostText, videoPlaybackMode } from './api.js';
import { loadPreview } from './preview-loader.js';
import type { PreviewResponse, PublicTextEntity, PublicVideo } from './api.js';
import { retainResource } from './media-cache.js';
import type { ResourceLease } from './media-cache.js';

const props = defineProps<{ data: PreviewResponse; input: PreviewInput; context: PreviewContext }>();
const retryResponse = ref<PreviewResponse | null>(null);
const viewData = computed(() => retryResponse.value ?? props.data);
const sourceHref = computed(() => props.input.kind === 'url' ? props.input.href : viewData.value.post?.url ?? '#');
const retryLoading = ref(false);
const retryTransportFailed = ref(false);
const retryRateLimited = ref(false);
const retryBlocked = ref(false);
const mediaRevision = ref(0);
const retryClock = ref(Date.now());
const retryBackoffUntil = ref(0);
let retryController: AbortController | null = null;
let retryGeneration = 0;
let retryClockTimer: number | null = null;
const retryDeadline = computed(() => Math.max(Date.parse(viewData.value.retryAt ?? '') || 0, retryBackoffUntil.value));
const retryWaitMs = computed(() => Math.max(0, retryDeadline.value - retryClock.value));
const retrySourceKey = computed(() => `${props.input.kind}:${props.input.kind === 'url' ? props.input.href : ''}:${props.context.source?.kind ?? ''}:${props.context.source?.kind === 'note' ? props.context.source.noteId : ''}`);
const retryOwnerKey = computed(() => `${apiSessionEpoch.value}:${retrySourceKey.value}`);
const canRetryPreview = computed(() => (viewData.value.state === 'stale' || viewData.value.state === 'unavailable') && viewData.value.retryable !== false &&
	props.context.source?.kind === 'note' && props.context.revealed && props.context.active && props.context.inViewport && !retryBlocked.value);
const expanded = ref(false);
type LoadedMedia = { src: string; blob: Blob };
type MediaLoadFailure = 'retryable' | 'blocked' | 'budget' | 'refresh';
type MediaFetchResult = { media: LoadedMedia | null; failure: MediaLoadFailure | null };
type IdentityBundle = {
	avatar: LoadedMedia | null;
	quoteAvatar: LoadedMedia | null;
	failure: MediaLoadFailure | null;
	objectUrls: string[];
};
type AttachmentBundle = {
	images: Array<LoadedMedia | null>;
	videoPosters: Array<LoadedMedia | null>;
	quoteImages: Array<LoadedMedia | null>;
	quoteVideoPosters: Array<LoadedMedia | null>;
	failure: MediaLoadFailure | null;
	objectUrls: string[];
};
let identityGeneration = 0;
let attachmentGeneration = 0;
let identityLease: ResourceLease<IdentityBundle> | null = null;
let attachmentLease: ResourceLease<AttachmentBundle> | null = null;
let identityKey = '';
let attachmentKey = '';
let identityFailure: MediaLoadFailure | null = null;
let attachmentFailure: MediaLoadFailure | null = null;
let mediaLoadGeneration = 0;
const avatarSrc = ref<string | null>(null);
const imageSources = ref<Array<string | null>>([]);
const imageBlobs = ref<Array<Blob | null>>([]);
type PhotoState = 'loading' | 'blocked' | 'budget' | 'error' | 'refresh' | undefined;
const photoStates = ref<{ main: PhotoState[]; quote: PhotoState[] }>({ main: [], quote: [] });
const photoViewerLoading = ref<{ main: boolean; quote: boolean }>({ main: false, quote: false });
const photoViewerError = ref<{ main: string | null; quote: string | null }>({ main: null, quote: null });
const photoObjectUrls = new Set<string>();
const photoControllers = new Map<string, AbortController>();
const photoPromises = new Map<string, Promise<void>>();
let photoQueue: Promise<void> = Promise.resolve();
let photoBytes = 0;
let photoGeneration = 0;
let photoViewerController: AbortController | null = null;
let photoViewerGeneration = 0;
const MAX_PHOTO_BYTES = 64 * 1024 * 1024;
const MAX_IMAGE_BYTES = 50 * 1024 * 1024;
const imagesRevealed = ref(false);
const videoPosterSources = ref<Array<string | null>>([]);
const quoteAvatarSrc = ref<string | null>(null);
const quoteImageSources = ref<Array<string | null>>([]);
const quoteImageBlobs = ref<Array<Blob | null>>([]);
const quoteImagesRevealed = ref(false);
const quoteVideoPosterSources = ref<Array<string | null>>([]);
const mediaLoadFailed = ref(false);
const mediaLoadBlocked = ref(false);
const photoHasRetryableFailure = computed(() => [...photoStates.value.main, ...photoStates.value.quote].some(state => state === 'error' || state === 'refresh'));
const canRetryMedia = computed(() => (mediaLoadFailed.value || photoHasRetryableFailure.value) && !mediaLoadBlocked.value && !retryBlocked.value &&
	(viewData.value.state === 'ready' || viewData.value.state === 'stale') && viewData.value.post != null &&
	props.context.source?.kind === 'note' && props.context.revealed && props.context.active && props.context.inViewport);
function hasSharedPhotoBudget(): boolean {
	const post = viewData.value.post;
	return post != null && (post.images.length > 4 || (post.quote?.images.length ?? 0) > 4);
}
function photoGridReady(target: PhotoTarget): boolean {
	const data = photoTargetData(target);
	if (data == null || data.images.length === 0 || !hasSharedPhotoBudget()) return true;
	const blobs = photoBlobRef(target).value;
	const visibleCount = Math.min(data.images.length, 6);
	return data.images.slice(0, visibleCount).every((_, index) => blobs[index] != null || ['blocked', 'budget', 'error', 'refresh'].includes(photoStates.value[target][index] ?? ''));
}
function photoGridStatus(target: PhotoTarget): string | null {
	const data = photoTargetData(target);
	if (!hasSharedPhotoBudget() || data == null || data.images.length === 0) return null;
	const states = photoStates.value[target].slice(0, data.images.length);
	if (states.includes('blocked')) return '一部の画像を表示する権限を確認できません。';
	if (states.includes('budget')) return '写真全体がメモリ上限を超えるため、一部の画像とギャラリーを表示できません。';
	if (states.includes('refresh')) return '画像情報を再取得してください。';
	if (states.includes('error')) return '一部の画像を読み込めませんでした。';
	return null;
}
const mainPhotoGridStatus = computed(() => photoGridStatus('main'));
const quotePhotoGridStatus = computed(() => photoGridStatus('quote'));
const mainPhotoGridRetryable = computed(() => photoStates.value.main.some(state => state === 'error' || state === 'refresh'));
const quotePhotoGridRetryable = computed(() => photoStates.value.quote.some(state => state === 'error' || state === 'refresh'));
const renderedImages = computed(() => {
	if (!photoGridReady('main')) return [];
	const images = viewData.value.post?.images ?? [];
	const visible = images.slice(0, hasSharedPhotoBudget() ? 6 : images.length);
	return visible.flatMap((image, index) => {
		const src = imageSources.value[index];
		return (hasSharedPhotoBudget() && images.length > 4) || src != null ? [{ index, src: src ?? null, alt: image.alt ?? '', width: image.width, height: image.height }] : [];
	});
});
const renderedQuoteImages = computed(() => {
	if (!photoGridReady('quote')) return [];
	const images = viewData.value.post?.quote?.images ?? [];
	const visible = images.slice(0, hasSharedPhotoBudget() ? 6 : images.length);
	return visible.flatMap((image, index) => {
		const src = quoteImageSources.value[index];
		return (hasSharedPhotoBudget() && images.length > 4) || src != null ? [{ index, src: src ?? null, alt: image.alt ?? '', width: image.width, height: image.height }] : [];
	});
});
const mainVideo = computed<PublicVideo | null>(() => viewData.value.post?.video ?? viewData.value.post?.videos?.[0] ?? null);
const quoteVideo = computed<PublicVideo | null>(() => viewData.value.post?.quote?.video ?? viewData.value.post?.quote?.videos?.[0] ?? null);
const mainVideos = computed<PublicVideo[]>(() => withPrimaryToken(viewData.value.post?.videos, viewData.value.post?.video));
const quoteVideos = computed<PublicVideo[]>(() => withPrimaryToken(viewData.value.post?.quote?.videos, viewData.value.post?.quote?.video));
const showAttachmentMedia = computed(() => props.context.detail || props.context.manual === true || props.context.preferenceValue === 'media');
const textSummary = computed(() => truncatePostText(viewData.value.post?.text ?? ''));
const displayedText = computed(() => expanded.value ? viewData.value.post?.text ?? '' : textSummary.value.text);
const displayedTextSegments = computed(() => richTextSegments(displayedText.value, viewData.value.post?.textEntities ?? []));
const quoteTextSegments = computed(() => richTextSegments(viewData.value.post?.quote?.text ?? '', viewData.value.post?.quote?.textEntities ?? []));
const moreExpandQueryKey = 'fxPreviewExpand';
const noteHref = computed(() => {
	const source = props.context.source;
	if (source?.kind !== 'note') return sourceHref.value;
	const notePath = `/notes/${encodeURIComponent(source.noteId)}`;
	const postId = viewData.value.post?.id;
	if (!postId) return notePath;
	const query = new URLSearchParams({ [moreExpandQueryKey]: `${source.noteId}:${postId}` });
	return `${notePath}?${query.toString()}`;
});
const postCreatedAt = computed(() => validDate(viewData.value.post?.createdAt));
const quoteCreatedAt = computed(() => validDate(viewData.value.post?.quote?.createdAt));
const mediaSummary = computed(() => formatMediaSummary((viewData.value.post?.images ?? []).length, videoCount(viewData.value.post?.videos?.length ?? 0, viewData.value.post?.hasVideo === true)));
const quoteMediaSummary = computed(() => formatMediaSummary((viewData.value.post?.quote?.images ?? []).length, videoCount(viewData.value.post?.quote?.videos?.length ?? 0, viewData.value.post?.quote?.hasVideo === true)));
const authorProfileUrl = computed(() => viewData.value.post?.author.profileUrl ?? null);
const quoteAuthorProfileUrl = computed(() => viewData.value.post?.quote?.author.profileUrl ?? null);
const repostCount = computed(() => Math.min(Number.MAX_SAFE_INTEGER, Math.max(0, viewData.value.post?.reposts ?? 0) + Math.max(0, viewData.value.post?.quotes ?? 0)));

const countFormatter = new Intl.NumberFormat();
function ownerKey(): string {
	return `${apiSessionEpoch.value}:${props.context.source?.noteId ?? ''}:${viewData.value.post?.id ?? ''}:${mediaRevision.value}`;
}
const currentOwnerKey = computed(ownerKey);
function validDate(value?: string): string | null {
	if (value == null || !Number.isFinite(Date.parse(value))) return null;
	return value;
}
function errorMessage(error?: PreviewResponse['error']): string {
	switch (error) {
		case 'temporary': return '一時的な取得エラーです。';
		case 'private': return 'この投稿は非公開として確認されました。';
		case 'not-found': return '投稿が見つからないか、アカウントが停止されている可能性があります。';
		case 'invalid-response': return 'プレビューを表示できませんでした。';
		case 'unavailable': return 'Xから投稿情報を取得できませんでした。非公開・削除などの状態は確認できていません。';
		default: return '投稿情報を取得できませんでした。';
	}
}
function formatRetryAt(timestamp?: string | number): string {
	const date = typeof timestamp === 'number' ? new Date(timestamp) : new Date(timestamp ?? '');
	return Number.isFinite(date.getTime()) ? new Intl.DateTimeFormat(undefined, { dateStyle: 'short', timeStyle: 'short' }).format(date) : '時刻不明';
}

function abortRetry(clearResult: boolean): void {
	retryGeneration++;
	retryController?.abort();
	retryController = null;
	retryLoading.value = false;
	if (clearResult) {
		retryResponse.value = null;
		retryTransportFailed.value = false;
		retryRateLimited.value = false;
		retryBlocked.value = false;
		retryBackoffUntil.value = 0;
	}
}

function failureCode(error: unknown): { code: string; status: number | null } {
	if (typeof error !== 'object' || error === null) return { code: '', status: null };
	const value = error as { code?: unknown; status?: unknown; statusCode?: unknown };
	const statusValue = value.status ?? value.statusCode;
	return {
		code: typeof value.code === 'string' ? value.code : '',
		status: typeof statusValue === 'number' ? statusValue : null,
	};
}

function isAccessFailure(error: unknown): boolean {
	const { code, status } = failureCode(error);
	return ['LOGIN_REQUIRED', 'PREVIEW_FORBIDDEN', 'POLICY_UNAVAILABLE', 'NOTE_UNAVAILABLE', 'LINK_NOT_IN_NOTE', 'INVALID_REQUEST'].includes(code) ||
		status === 401 || status === 403 || status === 404;
}

async function retryPreview(): Promise<void> {
	if ((!canRetryPreview.value && !canRetryMedia.value) || retryWaitMs.value > 0 || retryLoading.value || props.context.source?.kind !== 'note') return;
	const owner = retryOwnerKey.value;
	const generation = ++retryGeneration;
	const controller = new AbortController();
	retryController = controller;
	retryLoading.value = true;
	retryTransportFailed.value = false;
	retryRateLimited.value = false;
	try {
		const response = await loadPreview(props.input, props.context, controller.signal);
		if (controller.signal.aborted || generation !== retryGeneration || owner !== retryOwnerKey.value) return;
		acceptRefreshedPreview(response);
		retryBlocked.value = response.retryable === false;
		if (retryBlocked.value) clearMedia();
		retryBackoffUntil.value = 0;
	} catch (error) {
		if (controller.signal.aborted || generation !== retryGeneration || owner !== retryOwnerKey.value) return;
		if (isAccessFailure(error)) {
			retryResponse.value = { state: 'unavailable', postId: viewData.value.postId, error: 'unavailable' };
			retryBlocked.value = true;
			retryBackoffUntil.value = 0;
			clearMedia();
		} else {
			const { code, status } = failureCode(error);
			retryTransportFailed.value = true;
			retryRateLimited.value = code === 'RATE_LIMITED' || status === 429;
			retryBackoffUntil.value = Date.now() + (retryRateLimited.value ? 60_000 : 5000);
		}
	} finally {
		if (generation === retryGeneration) {
			retryController = null;
			retryLoading.value = false;
		}
	}
}

function updateRetryClock(deadline: number): void {
	if (retryClockTimer != null) window.clearTimeout(retryClockTimer);
	retryClockTimer = null;
	retryClock.value = Date.now();
	if (deadline > retryClock.value) {
		retryClockTimer = window.setTimeout(() => {
			updateRetryClock(deadline);
		}, deadline - retryClock.value);
	}
}

watch(retryDeadline, updateRetryClock, { immediate: true });

watch(retrySourceKey, () => {
	abortRetry(true);
	retryResponse.value = { state: 'pending' };
});
watch(apiSessionEpoch, () => {
	const hadRetryResult = retryResponse.value != null;
	abortRetry(true);
	if (hadRetryResult) retryResponse.value = { state: 'pending' };
});
watch(() => props.data, () => {
	if (retryResponse.value?.state === 'pending' && !retryLoading.value) retryResponse.value = null;
});
watch(() => [props.context.active, props.context.revealed, props.context.inViewport], values => {
	if (values.some(value => value !== true)) abortRetry(false);
});

function formatCount(value: number): string {
	return countFormatter.format(Math.max(0, value));
}

function formatMediaSummary(imageCount: number, videoCount: number): string {
	return [imageCount > 0 ? `画像${imageCount}件` : '', videoCount > 0 ? `動画${videoCount}件` : ''].filter(Boolean).join('・');
}

function videoCount(arrayCount: number, hasVideo: boolean): number {
	return Math.max(arrayCount, hasVideo ? 1 : 0);
}

function withPrimaryToken(videos?: PublicVideo[], primary?: PublicVideo): PublicVideo[] {
	const values = videos?.length ? videos : primary == null ? [] : [primary];
	return values.map(video => primary?.embedToken != null ? { ...video, embedToken: primary.embedToken, embedExpiresAt: primary.embedExpiresAt } : video);
}

function acceptRefreshedPreview(response: PreviewResponse): void {
	retryResponse.value = response;
	mediaRevision.value++;
}

type VideoRefreshRequest = {
	target: 'main' | 'quote';
	video: PublicVideo;
	videoIndex?: number;
	contentPostId: string;
	signal: AbortSignal;
};

function sameVideoMetadata(oldVideo: PublicVideo, nextVideo: PublicVideo): boolean {
	if (oldVideo.provider === 'youtube') return nextVideo.provider === 'youtube' && oldVideo.providerVideoId != null && oldVideo.providerVideoId === nextVideo.providerVideoId;
	return oldVideo.provider === nextVideo.provider && oldVideo.providerVideoId === nextVideo.providerVideoId &&
		oldVideo.provider === 'x' && oldVideo.mediaIdentity != null && oldVideo.mediaIdentity === nextVideo.mediaIdentity &&
		(oldVideo.durationMs == null || nextVideo.durationMs === oldVideo.durationMs) &&
		(oldVideo.playbackAvailable == null || nextVideo.playbackAvailable === oldVideo.playbackAvailable);
}

async function refreshPlaybackCapability(request: VideoRefreshRequest): Promise<{ video: PublicVideo; videoIndex: number } | null> {
	if (request.signal.aborted || props.context.source?.kind !== 'note' || !props.context.revealed || !props.context.active || !props.context.inViewport) return null;
	const owner = retryOwnerKey.value;
	const generation = ++retryGeneration;
	retryController?.abort();
	const controller = new AbortController();
	const abortFromPlayback = (): void => controller.abort();
	request.signal.addEventListener('abort', abortFromPlayback, { once: true });
	if (request.signal.aborted) controller.abort();
	retryController = controller;
	try {
		const response = await loadPreview(props.input, props.context, controller.signal);
		if (controller.signal.aborted || generation !== retryGeneration || owner !== retryOwnerKey.value) return null;
		retryBlocked.value = response.retryable === false;
		retryTransportFailed.value = response.state === 'unavailable' && response.retryable === true;
		retryRateLimited.value = response.error === 'temporary' && Date.parse(response.retryAt ?? '') - Date.now() >= 55_000;
		retryBackoffUntil.value = 0;
		if (response.state !== 'ready' && response.state !== 'stale' || response.post == null) {
			acceptRefreshedPreview(response);
			if (retryBlocked.value) clearMedia();
			return null;
		}
		const targetPost = request.target === 'main' ? response.post : response.post.quote;
		if (targetPost?.id !== request.contentPostId) {
			acceptRefreshedPreview(response);
			return null;
		}
		if (videoPlaybackMode.value === 'officialEmbed' && request.video.provider === 'x') {
			const refreshedPrimaryVideo = targetPost.video;
			const match = refreshedPrimaryVideo?.provider === 'x' && refreshedPrimaryVideo.embedToken != null
				? { video: refreshedPrimaryVideo, videoIndex: 0 }
				: null;
			acceptRefreshedPreview(response);
			return match;
		}
		const refreshedVideos = request.target === 'main'
			? withPrimaryToken(response.post.videos, response.post.video)
			: withPrimaryToken(targetPost.videos, targetPost.video);
		const candidates = refreshedVideos.flatMap((video, index) => sameVideoMetadata(request.video, video) ? [{ video, index }] : []);
		if (request.video.provider === 'youtube' && request.video.providerVideoId != null) {
			const match = candidates.length === 1 && candidates[0].video.embedToken != null
				? { video: candidates[0].video, videoIndex: candidates[0].index }
				: null;
			acceptRefreshedPreview(response);
			return match;
		}
		const match = request.video.provider === 'x' && request.video.mediaIdentity != null && candidates.length === 1 && candidates[0].video.embedToken != null
			? { video: candidates[0].video, videoIndex: candidates[0].index }
			: null;
		acceptRefreshedPreview(response);
		return match;
	} catch (error) {
		if (controller.signal.aborted || generation !== retryGeneration || owner !== retryOwnerKey.value) return null;
		const failure = failureCode(error);
		const failureResponse: PreviewResponse = isAccessFailure(error)
			? { state: 'unavailable', postId: viewData.value.postId, error: 'unavailable', retryable: false }
			: { state: 'unavailable', postId: viewData.value.postId, error: 'temporary', retryable: true, retryAt: new Date(Date.now() + (failure.status === 429 ? 60_000 : 5000)).toISOString() };
		acceptRefreshedPreview(failureResponse);
		retryBlocked.value = failureResponse.retryable === false;
		retryTransportFailed.value = !retryBlocked.value;
		retryRateLimited.value = failure.status === 429;
		if (retryBlocked.value) clearMedia();
		return null;
	} finally {
		request.signal.removeEventListener('abort', abortFromPlayback);
		if (generation === retryGeneration) retryController = null;
	}
}

type TextSegment = { key: string; kind?: PublicTextEntity['kind']; text: string; href?: string };
function richTextSegments(text: string, entities: PublicTextEntity[]): TextSegment[] {
	const segments: TextSegment[] = [];
	let cursor = 0;
	for (const entity of entities) {
		if (entity.start < cursor || entity.end > text.length || entity.end <= entity.start) continue;
		if (entity.start > cursor) segments.push({ key: `text:${cursor}`, text: text.slice(cursor, entity.start) });
		if (entity.kind === 'hashtag' && entity.tag != null) {
			segments.push({ key: `tag:${entity.start}`, kind: 'hashtag', text: entity.display, href: `/tags/${encodeURIComponent(entity.tag)}` });
		} else if (entity.href != null) {
			segments.push({ key: `${entity.kind}:${entity.start}`, kind: entity.kind, text: entity.display, href: entity.href });
		} else {
			segments.push({ key: `text:${entity.start}`, text: text.slice(entity.start, entity.end) });
		}
		cursor = entity.end;
	}
	if (cursor < text.length) segments.push({ key: `text:${cursor}`, text: text.slice(cursor) });
	return segments.length > 0 ? segments : [{ key: 'text:0', text }];
}

function clearIdentity(): void {
	identityGeneration++;
	identityLease?.release();
	identityLease = null;
	identityKey = '';
	identityFailure = null;
	avatarSrc.value = null;
	quoteAvatarSrc.value = null;
}

function clearAttachments(): void {
	attachmentGeneration++;
	attachmentLease?.release();
	attachmentLease = null;
	attachmentKey = '';
	attachmentFailure = null;
	imageSources.value = [];
	imageBlobs.value = [];
	videoPosterSources.value = [];
	quoteImageSources.value = [];
	quoteImageBlobs.value = [];
	quoteVideoPosterSources.value = [];
	clearPhotoResources();
}

function clearPhotoResources(): void {
	photoGeneration++;
	photoViewerGeneration++;
	photoViewerController?.abort();
	photoViewerController = null;
	for (const controller of photoControllers.values()) controller.abort();
	photoControllers.clear();
	photoPromises.clear();
	for (const objectUrl of photoObjectUrls) URL.revokeObjectURL(objectUrl);
	photoObjectUrls.clear();
	photoBytes = 0;
	photoQueue = Promise.resolve();
	photoStates.value = { main: [], quote: [] };
	photoViewerLoading.value = { main: false, quote: false };
	photoViewerError.value = { main: null, quote: null };
}

function clearMedia(): void {
	mediaLoadGeneration++;
	clearIdentity();
	clearAttachments();
	mediaLoadFailed.value = false;
	mediaLoadBlocked.value = false;
}

function emptyMediaResult(): MediaFetchResult {
	return { media: null, failure: null };
}

function failedMediaResult(failure: MediaLoadFailure): MediaFetchResult {
	return { media: null, failure };
}

function combineMediaFailures(...failures: Array<MediaLoadFailure | null>): MediaLoadFailure | null {
	if (failures.includes('blocked')) return 'blocked';
	if (failures.includes('refresh')) return 'refresh';
	return failures.includes('retryable') ? 'retryable' : null;
}

function expectedMediaUrl(kind: string, index: number): URL | null {
	const post = viewData.value.post;
	const source = props.context.source;
	if (post == null || source?.kind !== 'note' || !Number.isSafeInteger(index) || index < 0) return null;
	return new URL(`/api/plugin/fxtwitter-preview/media/${encodeURIComponent(post.id)}/${kind}/${index}?noteId=${encodeURIComponent(source.noteId)}`, window.location.origin);
}

async function fetchMedia(path: string, signal: AbortSignal, kind: string, index: number, maxBytes = MAX_IMAGE_BYTES): Promise<MediaFetchResult> {
	const expected = expectedMediaUrl(kind, index);
	let url: URL;
	try { url = new URL(path, window.location.origin); } catch { return emptyMediaResult(); }
	if (expected == null || url.origin !== expected.origin || url.pathname !== expected.pathname || url.hash !== '' ||
		url.username !== '' || url.password !== '' || url.searchParams.size !== 1 ||
		url.searchParams.getAll('noteId').length !== 1 || url.searchParams.get('noteId') !== props.context.source?.noteId) return emptyMediaResult();
	const noteId = url.searchParams.get('noteId');
	const postId = viewData.value.post?.id;
	if (noteId == null || postId == null) return emptyMediaResult();
	for (let attempt = 0; attempt < 2; attempt++) {
		if (signal.aborted || expectedMediaUrl(kind, index)?.pathname !== expected.pathname || expectedMediaUrl(kind, index)?.search !== expected.search) return emptyMediaResult();
		let prepare: Response;
		try {
			prepare = await fetchPluginResource('/api/plugin/fxtwitter-preview/media/prepare', {
				method: 'POST',
				cache: 'no-store',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ noteId, postId, kind, index }),
				signal,
			});
		} catch {
			return signal.aborted ? emptyMediaResult() : failedMediaResult('retryable');
		}
		if (signal.aborted) return emptyMediaResult();
		if (prepare.status === 409 && (kind === 'poster' || kind === 'quote-poster')) {
			let errorCode: unknown;
			try { errorCode = (await prepare.json() as { error?: { code?: unknown } }).error?.code; } catch { return failedMediaResult('retryable'); }
			if (errorCode !== 'PRIVATE_MEDIA_ONLY') return failedMediaResult('retryable');
			return await fetchPrivatePoster(expected, signal, kind, index, maxBytes);
		}
		if (prepare.status === 401 || prepare.status === 403) return failedMediaResult('blocked');
		if (!prepare.ok) return failedMediaResult(prepare.status === 404 && (kind === 'image' || kind === 'quote-image') ? 'refresh' : 'retryable');
		let metadata: unknown;
		try { metadata = await prepare.json(); } catch { return failedMediaResult('retryable'); }
		const prepared = parsePreparedPublicMedia(metadata, 'image', MAX_IMAGE_BYTES);
		if (prepared == null) return failedMediaResult('retryable');
		if (prepared.size > maxBytes) return failedMediaResult('budget');
		let response: Response;
		try { response = await fetchPublicMedia(prepared.url, signal); } catch {
			return signal.aborted ? emptyMediaResult() : failedMediaResult('retryable');
		}
		if (signal.aborted) return emptyMediaResult();
		if (response.status === 401 || response.status === 403) return failedMediaResult('blocked');
		if (response.status === 404 && attempt === 0) continue;
		if (!response.ok) return failedMediaResult(response.status === 404 && (kind === 'image' || kind === 'quote-image') ? 'refresh' : 'retryable');
		const contentType = response.headers.get('Content-Type')?.split(';', 1)[0]?.trim().toLowerCase();
		if (contentType !== prepared.contentType) return failedMediaResult('retryable');
		const contentLength = response.headers.get('Content-Length');
		if (contentLength != null) {
			if (!/^\d+$/.test(contentLength) || Number(contentLength) !== prepared.size) return failedMediaResult('retryable');
		}
		let blob: Blob;
		try { blob = await response.blob(); } catch { return signal.aborted ? emptyMediaResult() : failedMediaResult('retryable'); }
		const blobType = blob.type.split(';', 1)[0]?.trim().toLowerCase();
		if (signal.aborted) return emptyMediaResult();
		if (blob.size > MAX_IMAGE_BYTES || blob.size !== prepared.size || blobType !== prepared.contentType) return failedMediaResult('retryable');
		if (blob.size > maxBytes) return failedMediaResult('budget');
		try { return { media: { src: URL.createObjectURL(blob), blob }, failure: null }; } catch { return failedMediaResult('retryable'); }
	}
	return failedMediaResult(kind === 'image' || kind === 'quote-image' ? 'refresh' : 'retryable');
}

async function fetchPrivatePoster(expected: URL, signal: AbortSignal, kind: string, index: number, maxBytes: number): Promise<MediaFetchResult> {
	let response: Response;
	try {
		response = await fetchPluginResource(`${expected.pathname}${expected.search}`, { method: 'GET', cache: 'no-store', signal });
	} catch {
		return signal.aborted ? emptyMediaResult() : failedMediaResult('retryable');
	}
		if (signal.aborted || expectedMediaUrl(kind, index)?.pathname !== expected.pathname || expectedMediaUrl(kind, index)?.search !== expected.search) return emptyMediaResult();
	if (response.status === 401 || response.status === 403) return failedMediaResult('blocked');
	if (!response.ok) return failedMediaResult('retryable');
	const contentType = response.headers.get('Content-Type')?.split(';', 1)[0]?.trim().toLowerCase();
	if (!['image/jpeg', 'image/png', 'image/gif', 'image/webp'].includes(contentType ?? '')) return failedMediaResult('retryable');
	const contentLength = response.headers.get('Content-Length');
	if (contentLength != null && (!/^\d+$/.test(contentLength) || Number(contentLength) > maxBytes)) return failedMediaResult('budget');
	let blob: Blob;
	try { blob = await response.blob(); } catch { return signal.aborted ? emptyMediaResult() : failedMediaResult('retryable'); }
	if (signal.aborted || expectedMediaUrl(kind, index)?.pathname !== expected.pathname || expectedMediaUrl(kind, index)?.search !== expected.search) return emptyMediaResult();
	const blobType = blob.type.split(';', 1)[0]?.trim().toLowerCase();
	if (blob.size > MAX_IMAGE_BYTES || blob.size > maxBytes) return failedMediaResult('budget');
	if (blob.size === 0 || blobType !== contentType || (contentLength != null && blob.size !== Number(contentLength))) return failedMediaResult('retryable');
	try { return { media: { src: URL.createObjectURL(blob), blob }, failure: null }; } catch { return failedMediaResult('retryable'); }
}

async function loadIdentity(): Promise<MediaLoadFailure | null> {
	const post = viewData.value.post;
	const source = props.context.source;
	if (post == null || source?.kind !== 'note') return null;
	const nextKey = `${ownerKey()}:identity`;
	if (identityLease != null && identityKey === nextKey) {
		const lease = identityLease;
		const current = identityGeneration;
			const bundle = await lease.promise;
		if (current !== identityGeneration || identityLease !== lease) return null;
		identityFailure = bundle.failure;
		avatarSrc.value = bundle.avatar?.src ?? null;
		quoteAvatarSrc.value = bundle.quoteAvatar?.src ?? null;
		return identityFailure;
	}
	clearIdentity();
	const current = identityGeneration;
	const quote = post.quote;
	const lease = retainResource<IdentityBundle>(nextKey, async signal => {
		const loadOne = async (path: string | undefined, kind: string): Promise<MediaFetchResult> => path == null ? emptyMediaResult() : await fetchMedia(path, signal, kind, 0);
		const [avatarResult, quoteAvatarResult] = await Promise.all([
			loadOne(post.author.avatarUrl, 'avatar'),
			loadOne(quote?.author.avatarUrl, 'quote-avatar'),
		]);
		const avatar = avatarResult.media;
		const quoteAvatar = quoteAvatarResult.media;
		const objectUrls = [avatar, quoteAvatar].flatMap(value => value == null ? [] : [value.src]);
		return { avatar, quoteAvatar, failure: combineMediaFailures(avatarResult.failure, quoteAvatarResult.failure), objectUrls };
	}, bundle => {
		for (const objectUrl of bundle.objectUrls) URL.revokeObjectURL(objectUrl);
	});
	identityLease = lease;
	identityKey = nextKey;
	const bundle = await lease.promise;
	if (current !== identityGeneration || identityLease !== lease) return null;
	identityFailure = bundle.failure;
	avatarSrc.value = bundle.avatar?.src ?? null;
	quoteAvatarSrc.value = bundle.quoteAvatar?.src ?? null;
	return identityFailure;
}

async function loadAttachments(): Promise<MediaLoadFailure | null> {
	const post = viewData.value.post;
	const source = props.context.source;
	if (post == null || source?.kind !== 'note' || !showAttachmentMedia.value) {
		clearAttachments();
		return null;
	}
	const quote = post.quote;
	const sharedPhotoBudget = hasSharedPhotoBudget();
	const nextKey = `${ownerKey()}:attachments`;
	if (attachmentLease != null && attachmentKey === nextKey) {
		const lease = attachmentLease;
		const current = attachmentGeneration;
		const bundle = await lease.promise;
		if (current !== attachmentGeneration || attachmentLease !== lease) return null;
		attachmentFailure = bundle.failure;
		if (!sharedPhotoBudget && post.images.length <= 4) {
			imageSources.value = bundle.images.map(value => value?.src ?? null);
			imageBlobs.value = bundle.images.map(value => value?.blob ?? null);
		}
		videoPosterSources.value = bundle.videoPosters.map(value => value?.src ?? null);
		if (!sharedPhotoBudget && (quote?.images.length ?? 0) <= 4) {
			quoteImageSources.value = bundle.quoteImages.map(value => value?.src ?? null);
			quoteImageBlobs.value = bundle.quoteImages.map(value => value?.blob ?? null);
		}
		quoteVideoPosterSources.value = bundle.quoteVideoPosters.map(value => value?.src ?? null);
		return attachmentFailure;
	}
	clearAttachments();
	const current = attachmentGeneration;
	const postVideos = post.videos?.length ? post.videos : post.video == null ? [] : [post.video];
	const quoteVideos = quote?.videos?.length ? quote.videos : quote?.video == null ? [] : [quote.video];
	const lease = retainResource<AttachmentBundle>(nextKey, async signal => {
		const [images, videoPosters, quoteImages, quoteVideoPosters] = await Promise.all([
			sharedPhotoBudget ? Promise.resolve(post.images.map(() => emptyMediaResult())) : Promise.all(post.images.map((image, index) => fetchMedia(image.url, signal, 'image', index))),
			Promise.all(postVideos.map((video, index) => video.posterUrl == null ? emptyMediaResult() : fetchMedia(video.posterUrl, signal, 'poster', index))),
			quote == null ? Promise.resolve([]) : sharedPhotoBudget ? Promise.resolve(quote.images.map(() => emptyMediaResult())) : Promise.all(quote.images.map((image, index) => fetchMedia(image.url, signal, 'quote-image', index))),
			Promise.all(quoteVideos.map((video, index) => video.posterUrl == null ? emptyMediaResult() : fetchMedia(video.posterUrl, signal, 'quote-poster', index))),
		]);
		const results = [...images, ...videoPosters, ...quoteImages, ...quoteVideoPosters];
		const objectUrls = results.flatMap(result => result.media == null ? [] : [result.media.src]);
		return {
			images: images.map(result => result.media),
			videoPosters: videoPosters.map(result => result.media),
			quoteImages: quoteImages.map(result => result.media),
			quoteVideoPosters: quoteVideoPosters.map(result => result.media),
			failure: combineMediaFailures(...results.map(result => result.failure)),
			objectUrls,
		};
	}, bundle => {
		for (const objectUrl of bundle.objectUrls) URL.revokeObjectURL(objectUrl);
	});
	attachmentLease = lease;
	attachmentKey = nextKey;
	const bundle = await lease.promise;
	if (current !== attachmentGeneration || attachmentLease !== lease) return null;
	attachmentFailure = bundle.failure;
	if (!sharedPhotoBudget && post.images.length <= 4) {
		imageSources.value = bundle.images.map(value => value?.src ?? null);
		imageBlobs.value = bundle.images.map(value => value?.blob ?? null);
	}
	videoPosterSources.value = bundle.videoPosters.map(value => value?.src ?? null);
	if (!sharedPhotoBudget && (quote?.images.length ?? 0) <= 4) {
		quoteImageSources.value = bundle.quoteImages.map(value => value?.src ?? null);
		quoteImageBlobs.value = bundle.quoteImages.map(value => value?.blob ?? null);
	}
	quoteVideoPosterSources.value = bundle.quoteVideoPosters.map(value => value?.src ?? null);
	return attachmentFailure;
}

type PhotoTarget = 'main' | 'quote';

function photoTargetData(target: PhotoTarget): { id: string; images: Array<{ url: string; width?: number; height?: number; alt?: string }>; kind: string } | null {
	const post = viewData.value.post;
	if (post == null) return null;
	if (target === 'main') return { id: post.id, images: post.images, kind: 'image' };
	return post.quote == null ? null : { id: post.quote.id, images: post.quote.images, kind: 'quote-image' };
}

function photoSourceRef(target: PhotoTarget) { return target === 'main' ? imageSources : quoteImageSources; }
function photoBlobRef(target: PhotoTarget) { return target === 'main' ? imageBlobs : quoteImageBlobs; }

function setPhotoState(target: PhotoTarget, index: number, state: PhotoState): void {
	const next = { ...photoStates.value, [target]: [...photoStates.value[target]] };
	while (next[target].length <= index) next[target].push(undefined);
	next[target][index] = state;
	photoStates.value = next;
}

function canLoadPhotos(): boolean {
	return canOpenMedia() && showAttachmentMedia.value;
}

function cancelPhotoViewer(): void {
	photoViewerGeneration++;
	photoViewerController?.abort();
	photoViewerController = null;
	photoViewerLoading.value = { main: false, quote: false };
}

function loadPhoto(target: PhotoTarget, index: number, signal?: AbortSignal): Promise<void> {
	const data = photoTargetData(target);
	if (!canLoadPhotos() || !hasSharedPhotoBudget() || data == null || !Number.isSafeInteger(index) || index < 0 || index >= data.images.length) return Promise.resolve();
	if (photoBlobRef(target).value[index] != null) return Promise.resolve();
	if (photoStates.value[target][index] === 'blocked' || photoStates.value[target][index] === 'budget' || photoStates.value[target][index] === 'refresh') return Promise.resolve();
	const key = `${ownerKey()}:${target}:${index}`;
	const existing = photoPromises.get(key);
	if (existing != null && !photoControllers.get(key)?.signal.aborted) {
		const controller = photoControllers.get(key);
		if (signal != null && controller != null) {
			const abort = (): void => controller.abort();
			if (signal.aborted) controller.abort();
			else signal.addEventListener('abort', abort, { once: true });
			return existing.finally(() => signal.removeEventListener('abort', abort));
		}
		return existing;
	}
	const generation = photoGeneration;
	const owner = ownerKey();
	const controller = new AbortController();
	photoControllers.set(key, controller);
	const abortFromCaller = (): void => controller.abort();
	if (signal?.aborted) controller.abort();
	else signal?.addEventListener('abort', abortFromCaller, { once: true });
	setPhotoState(target, index, 'loading');
	const work = photoQueue.then(async () => {
		if (controller.signal.aborted || generation !== photoGeneration || owner !== ownerKey() || !canLoadPhotos()) return;
		const remaining = Math.max(0, MAX_PHOTO_BYTES - photoBytes);
		const result = await fetchMedia(data.images[index].url, controller.signal, data.kind, index, remaining);
		if (controller.signal.aborted || generation !== photoGeneration || owner !== ownerKey() || !canLoadPhotos()) {
			if (result.media != null) URL.revokeObjectURL(result.media.src);
			return;
		}
		if (result.media == null) {
			setPhotoState(target, index, result.failure === 'blocked' ? 'blocked' : result.failure === 'budget' ? 'budget' : result.failure === 'refresh' ? 'refresh' : 'error');
			return;
		}
		const sources = photoSourceRef(target);
		const blobs = photoBlobRef(target);
		const nextSources = [...sources.value];
		const nextBlobs = [...blobs.value];
		while (nextSources.length < data.images.length) nextSources.push(null);
		while (nextBlobs.length < data.images.length) nextBlobs.push(null);
		nextSources[index] = result.media.src;
		nextBlobs[index] = result.media.blob;
		sources.value = nextSources;
		blobs.value = nextBlobs;
		photoObjectUrls.add(result.media.src);
		photoBytes += result.media.blob.size;
		setPhotoState(target, index, undefined);
	});
	photoQueue = work.catch(() => undefined);
	photoPromises.set(key, work);
	return work.finally(() => {
		signal?.removeEventListener('abort', abortFromCaller);
		const ownsEntry = photoPromises.get(key) === work;
		if (photoControllers.get(key) === controller) photoControllers.delete(key);
		if (ownsEntry) photoPromises.delete(key);
		if (ownsEntry && generation === photoGeneration && photoBlobRef(target).value[index] == null && photoStates.value[target][index] === 'loading') setPhotoState(target, index, undefined);
	});
}

function loadPhotoWindow(target: PhotoTarget): void {
	const data = photoTargetData(target);
	if (data == null || data.images.length === 0 || !hasSharedPhotoBudget()) return;
	if (!canLoadPhotos() || photoViewerLoading.value[target]) return;
	const desired = data.images.slice(0, data.images.length <= 4 ? data.images.length : 6).map((_, index) => index);
	for (const index of desired) {
		if (photoStates.value[target][index] !== 'error') void loadPhoto(target, index);
	}
}

function retryPhotoGrid(target: PhotoTarget): void {
	const data = photoTargetData(target);
	if (data == null || !hasSharedPhotoBudget()) return;
	if (photoStates.value[target].includes('refresh')) {
		if (canRetryMedia.value && retryWaitMs.value === 0) void retryPreview();
		return;
	}
	for (let index = 0; index < Math.min(data.images.length, 6); index++) {
		if (photoStates.value[target][index] === 'error') void loadPhoto(target, index);
	}
}

async function openPhotoGallery(target: PhotoTarget, initialIndex: number): Promise<void> {
	const data = photoTargetData(target);
	if (!canLoadPhotos() || data == null || !Number.isSafeInteger(initialIndex) || initialIndex < 0 || initialIndex >= data.images.length) return;
	cancelPhotoViewer();
	const controller = new AbortController();
	photoViewerController = controller;
	const generation = ++photoViewerGeneration;
	const owner = ownerKey();
	photoViewerLoading.value = { ...photoViewerLoading.value, [target]: true };
	photoViewerError.value = { ...photoViewerError.value, [target]: null };
	try {
		for (let index = 0; index < data.images.length; index++) {
			if (photoBlobRef(target).value[index] != null) continue;
			await loadPhoto(target, index, controller.signal);
			if (controller.signal.aborted || generation !== photoViewerGeneration || owner !== ownerKey()) return;
			if (photoBlobRef(target).value[index] == null) {
				const state = photoStates.value[target][index];
				photoViewerError.value = { ...photoViewerError.value, [target]: state === 'blocked' ? '画像を表示する権限を確認できません。' : state === 'budget' ? '写真全体がメモリ上限を超えるため、ギャラリーを開けません。' : state === 'refresh' ? '画像情報を再取得してからギャラリーを開いてください。' : '写真をすべて読み込めませんでした。再試行してください。' };
				return;
			}
		}
		if (controller.signal.aborted || generation !== photoViewerGeneration || owner !== ownerKey() || !canOpenMedia()) return;
		const blobs = photoBlobRef(target).value;
		if (blobs.length < data.images.length || blobs.slice(0, data.images.length).some(blob => blob == null)) {
			photoViewerError.value = { ...photoViewerError.value, [target]: '写真をすべて読み込めませんでした。再試行してください。' };
			return;
		}
		const initiallyRevealed = target === 'main' ? imagesRevealed.value : quoteImagesRevealed.value;
		const items = data.images.map((image, index): PluginMediaItem => ({
			id: `${data.id}:${index}`, type: 'image', blob: blobs[index]!, alt: image.alt, width: image.width, height: image.height,
			filename: `x-${data.id}-${index + 1}`, isSensitive: target === 'main' ? viewData.value.post?.isSensitive === true : viewData.value.post?.quote?.isSensitive === true,
			initiallyRevealed,
		}));
		await openPluginMedia(items, `${data.id}:${initialIndex}`);
	} catch {
		if (!controller.signal.aborted && generation === photoViewerGeneration && owner === ownerKey()) photoViewerError.value = { ...photoViewerError.value, [target]: 'ギャラリーを開けませんでした。再試行してください。' };
	} finally {
		if (generation === photoViewerGeneration) {
			photoViewerLoading.value = { ...photoViewerLoading.value, [target]: false };
			if (photoViewerController === controller) photoViewerController = null;
		}
	}
}

async function loadMedia(): Promise<void> {
	const post = viewData.value.post;
	const source = props.context.source;
	if (!props.context.revealed || (viewData.value.state !== 'ready' && viewData.value.state !== 'stale') || post == null || source?.kind !== 'note') {
		clearMedia();
		return;
	}
	if (!showAttachmentMedia.value) clearAttachments();
	if (!props.context.active || !props.context.inViewport) {
		cancelPhotoViewer();
		return;
	}
	const owner = ownerKey();
	const generation = ++mediaLoadGeneration;
	const results = await Promise.allSettled([loadIdentity(), loadAttachments()]);
	if (generation !== mediaLoadGeneration || owner !== ownerKey()) return;
	const targets: PhotoTarget[] = ['main', 'quote'];
	if (hasSharedPhotoBudget()) {
		for (const target of targets) {
			if ((photoTargetData(target)?.images.length ?? 0) <= 4) loadPhotoWindow(target);
		}
		for (const target of targets) {
			if ((photoTargetData(target)?.images.length ?? 0) > 4) loadPhotoWindow(target);
		}
	} else {
		loadPhotoWindow('main');
		loadPhotoWindow('quote');
	}
	const failures = results.flatMap(result => result.status === 'fulfilled' ? [result.value] : ['retryable' as const]);
	const failure = combineMediaFailures(...failures);
	mediaLoadBlocked.value = failure === 'blocked';
	mediaLoadFailed.value = failure != null;
}

async function openImage(index: number): Promise<void> {
	const post = viewData.value.post;
	if (!canOpenMedia() || post == null || !Number.isSafeInteger(index)) return;
	if (hasSharedPhotoBudget()) {
		await openPhotoGallery('main', index);
		return;
	}
	if (imageBlobs.value[index] == null) return;
	const items = (post.images ?? []).flatMap<PluginMediaItem>((image, imageIndex) => {
		const blob = imageBlobs.value[imageIndex];
		return blob == null ? [] : [{
			id: `${post.id}:${imageIndex}`,
			type: 'image',
			blob,
			alt: image.alt,
			width: image.width,
			height: image.height,
			filename: `x-${post.id}-${imageIndex + 1}`,
			isSensitive: post.isSensitive === true,
			initiallyRevealed: imagesRevealed.value,
		}];
	});
	await openPluginMedia(items, `${post.id}:${index}`);
}

async function openQuoteImage(index: number): Promise<void> {
	const quote = viewData.value.post?.quote;
	if (!canOpenMedia() || quote == null || !Number.isSafeInteger(index)) return;
	if (hasSharedPhotoBudget()) {
		await openPhotoGallery('quote', index);
		return;
	}
	if (quoteImageBlobs.value[index] == null) return;
	const items = quote.images.flatMap<PluginMediaItem>((image, imageIndex) => {
		const blob = quoteImageBlobs.value[imageIndex];
		return blob == null ? [] : [{
			id: `${quote.id}:${imageIndex}`,
			type: 'image',
			blob,
			alt: image.alt,
			width: image.width,
			height: image.height,
			filename: `x-${quote.id}-${imageIndex + 1}`,
			isSensitive: quote.isSensitive === true,
			initiallyRevealed: quoteImagesRevealed.value,
		}];
	});
	await openPluginMedia(items, `${quote.id}:${index}`);
}

function canOpenMedia(): boolean {
	return props.context.source?.kind === 'note' && props.context.revealed && props.context.active && props.context.inViewport &&
		(viewData.value.state === 'ready' || viewData.value.state === 'stale');
}

function consumeMoreExpandRequest(): boolean {
	const source = props.context.source;
	const postId = viewData.value.post?.id;
	if (!props.context.detail || !props.context.revealed || source?.kind !== 'note' || postId == null) return false;

	const currentURL = new URL(window.location.href);
	const expectedPath = `/notes/${encodeURIComponent(source.noteId)}`;
	const expectedMarker = `${source.noteId}:${postId}`;
	if (currentURL.pathname !== expectedPath || currentURL.searchParams.get(moreExpandQueryKey) !== expectedMarker) return false;

	currentURL.searchParams.delete(moreExpandQueryKey);
	window.history.replaceState(window.history.state, '', `${currentURL.pathname}${currentURL.search}${currentURL.hash}`);
	return true;
}

watch(
	() => [currentOwnerKey.value, viewData.value, props.context.revealed, props.context.active, props.context.inViewport, showAttachmentMedia.value] as const,
	(values, oldValues) => {
		if (oldValues != null && values[0] !== oldValues[0]) clearMedia();
		void loadMedia();
	},
);
watch([
	() => viewData.value.post?.id,
	() => props.context.detail,
	() => props.context.revealed,
	() => props.context.source?.kind,
	() => props.context.source?.kind === 'note' ? props.context.source.noteId : null,
], () => {
	expanded.value = consumeMoreExpandRequest();
}, { immediate: true });
onMounted(() => {
	void loadMedia();
});
onBeforeUnmount(() => {
	clearMedia();
	abortRetry(false);
	if (retryClockTimer != null) window.clearTimeout(retryClockTimer);
});
</script>

<style lang="scss" module>
.card { border: 1px solid var(--MI_THEME-divider); border-left: 4px solid var(--MI_THEME-fg); border-radius: var(--MI-radius); overflow: hidden; background: var(--MI_THEME-panel); }
.header { display: flex; align-items: center; gap: 10px; padding: 14px 14px 8px; }
.avatarLink { display: block; flex: 0 0 auto; border-radius: 50%; }
.avatar { display: block; width: 40px; height: 40px; border-radius: 50%; object-fit: cover; }
.identity { display: flex; min-width: 0; flex: 1; flex-direction: column; line-height: 1.25; }
.identity span { color: var(--MI_THEME-fgTransparentWeak); overflow: hidden; text-overflow: ellipsis; }
.identityLink { display: flex; min-width: 0; flex-direction: column; color: inherit; text-decoration: none; }
.postTime { display: block; margin: 8px 14px 12px; color: var(--MI_THEME-fgTransparentWeak); font-size: 0.82em; }
.quote .postTime { margin: 8px 10px 10px; }
.text { margin: 0; padding: 8px 14px 14px; white-space: pre-wrap; overflow-wrap: anywhere; line-height: 1.55; }
.mediaSummary { margin: -8px 14px 14px; color: var(--MI_THEME-fgTransparentWeak); font-size: 0.9em; }
.quote { display: block; margin: 14px; border: 1px solid var(--MI_THEME-divider); border-radius: var(--MI-radius); overflow: hidden; color: inherit; text-decoration: none; }
.quoteHeader { display: flex; align-items: center; gap: 8px; padding: 10px 10px 4px; }
.quoteAvatar { width: 28px; height: 28px; border-radius: 50%; object-fit: cover; }
.quoteText { margin: 0; padding: 8px 10px 10px; white-space: pre-wrap; overflow-wrap: anywhere; line-height: 1.45; }
.quoteMediaSummary { margin: -4px 10px 10px; color: var(--MI_THEME-fgTransparentWeak); font-size: 0.85em; }
.mediaError { margin: 10px 14px; color: var(--MI_THEME-warn); font-size: 0.85em; }
.footer { display: flex; flex-wrap: wrap; gap: 16px; padding: 10px 14px; border-top: 1px solid var(--MI_THEME-divider); color: var(--MI_THEME-fgTransparentWeak); font-size: 0.85em; }
.footer span { display: inline-flex; align-items: center; gap: 5px; }
.legacyFooter { display: none; }
.more { display: block; width: max-content; margin: -8px 14px 12px; color: var(--MI_THEME-accent); text-decoration: none; }
.stale, .unavailable { color: var(--MI_THEME-warn); font-size: 0.85em; }
.stale { margin: -4px 14px 12px; }
.unavailable { padding: 12px 14px; }
.stale p, .unavailable p { margin: 4px 0; }
.stale button, .unavailable button { margin: 4px 8px 4px 0; color: var(--MI_THEME-accent); }
.unavailableActions { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 10px; }
.action { display: inline-flex; align-items: center; justify-content: center; min-height: 36px; padding: 7px 12px; border: 1px solid var(--MI_THEME-divider); border-radius: var(--MI-radius); color: var(--MI_THEME-fg); background: var(--MI_THEME-buttonBg); text-decoration: none; }
.action:disabled { opacity: 0.5; }
.pending { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; padding: 14px; color: var(--MI_THEME-fgTransparentWeak); }
.pending a, .unavailable a { color: var(--MI_THEME-accent); }
.dots { display: inline-flex; align-items: center; gap: 3px; }
.dots i { width: 4px; height: 4px; border-radius: 50%; background: currentColor; animation: loadingDot 1s ease-in-out infinite; }
.dots i:nth-child(2) { animation-delay: 0.15s; }
.dots i:nth-child(3) { animation-delay: 0.3s; }
@keyframes loadingDot { 0%, 60%, 100% { opacity: 0.3; transform: translateY(0); } 30% { opacity: 1; transform: translateY(-3px); } }
@media (prefers-reduced-motion: reduce) { .dots i { animation: none; } }
.videoGrid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 2px; background: var(--MI_THEME-divider); }
.videoGrid[data-video-count="1"] { grid-template-columns: minmax(0, 1fr); }
.videoGrid[data-video-count="2"] { aspect-ratio: 16 / 9; }
</style>
