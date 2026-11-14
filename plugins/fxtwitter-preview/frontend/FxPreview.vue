<!--
SPDX-FileCopyrightText: syuilo and misskey-project
SPDX-License-Identifier: AGPL-3.0-only
-->

<template>
<article v-if="(data.state === 'ready' || data.state === 'stale') && data.post" :class="$style.card">
	<header :class="$style.header">
		<a v-if="avatarSrc && authorProfileUrl" :class="$style.avatarLink" :href="authorProfileUrl" target="_blank" rel="nofollow noopener noreferrer" :aria-label="`${data.post.author.name}のXプロフィール`" @click.stop>
			<img :class="$style.avatar" :src="avatarSrc" alt="" loading="lazy">
		</a>
		<img v-else-if="avatarSrc" :class="$style.avatar" :src="avatarSrc" alt="" loading="lazy">
		<div :class="$style.identity">
			<a v-if="authorProfileUrl" :class="$style.identityLink" :href="authorProfileUrl" target="_blank" rel="nofollow noopener noreferrer" @click.stop>
				<strong>{{ data.post.author.name }}</strong>
				<span>@{{ data.post.author.screenName }}</span>
			</a>
			<template v-else><strong>{{ data.post.author.name }}</strong><span>@{{ data.post.author.screenName }}</span></template>
		</div>
	</header>
	<p :class="$style.text"><template v-for="segment in displayedTextSegments" :key="segment.key"><MkUrl v-if="segment.kind === 'url' && segment.href" :url="segment.href" :aria-label="segment.text" @click.stop/><MkA v-else-if="segment.kind === 'hashtag' && segment.href" class="_link" :to="segment.href" @click.stop>{{ segment.text }}</MkA><a v-else-if="segment.kind === 'mention' && segment.href" class="_link" :href="segment.href" target="_blank" rel="nofollow noopener noreferrer" @click.stop>{{ segment.text }}</a><span v-else>{{ segment.text }}</span></template><span v-if="textSummary.truncated && !expanded">…</span></p>
	<a v-if="textSummary.truncated && !context.detail" :class="$style.more" :href="noteHref ?? undefined">もっと見る</a>
	<button v-else-if="textSummary.truncated && !expanded" type="button" class="_button" :class="$style.more" @click="expanded = true">もっと見る</button>
	<p v-if="data.stale" :class="$style.stale">一時的に取得できないため、前回取得した内容を表示しています。</p>
	<p v-if="!isTextMode && !showAttachmentMedia && mediaSummary" :class="$style.mediaSummary">{{ mediaSummary }}</p>
	<FxMediaGrid v-if="showAttachmentMedia && renderedImages.length" :key="`images:${data.post.id}`" v-model:revealed="imagesRevealed" :images="renderedImages" :isSensitive="data.post.isSensitive" @open="openImage"/>
	<FxVideoPreview
		v-for="(video, index) in mainVideos"
		:key="`video:${data.post.id}:${index}`"
		:video="video"
		:index="index"
		:cachePostId="data.post.id"
		:contentPostId="data.post.id"
		target="main"
		:context="context"
		:isSensitive="data.post.isSensitive"
	/>
	<MkTime v-if="postCreatedAt" data-fx-post-time="main" :class="$style.postTime" :time="postCreatedAt" mode="detail"/>
	<p v-if="data.post.quoteUnavailableReason" :data-fx-quote-unavailable="data.post.quoteUnavailableReason">引用: {{ unavailableMessage(data.post.quoteUnavailableReason) }}</p>
	<section
		v-if="data.post.quote"
		:class="$style.quote"
	>
		<header :class="$style.quoteHeader">
			<a v-if="quoteAvatarSrc && quoteAuthorProfileUrl" :href="quoteAuthorProfileUrl" target="_blank" rel="nofollow noopener noreferrer" :aria-label="`${data.post.quote.author.name}のXプロフィール`" @click.stop><img :class="$style.quoteAvatar" :src="quoteAvatarSrc" alt="" loading="lazy"></a>
			<img v-else-if="quoteAvatarSrc" :class="$style.quoteAvatar" :src="quoteAvatarSrc" alt="" loading="lazy">
			<div :class="$style.identity">
				<a v-if="quoteAuthorProfileUrl" :class="$style.identityLink" :href="quoteAuthorProfileUrl" target="_blank" rel="nofollow noopener noreferrer" @click.stop><strong>{{ data.post.quote.author.name }}</strong><span>@{{ data.post.quote.author.screenName }}</span></a>
				<template v-else><strong>{{ data.post.quote.author.name }}</strong><span>@{{ data.post.quote.author.screenName }}</span></template>
			</div>
		</header>
		<p :class="$style.quoteText"><template v-for="segment in quoteTextSegments" :key="segment.key"><MkUrl v-if="segment.kind === 'url' && segment.href" :url="segment.href" :aria-label="segment.text" @click.stop/><MkA v-else-if="segment.kind === 'hashtag' && segment.href" class="_link" :to="segment.href" @click.stop>{{ segment.text }}</MkA><a v-else-if="segment.kind === 'mention' && segment.href" class="_link" :href="segment.href" target="_blank" rel="nofollow noopener noreferrer" @click.stop>{{ segment.text }}</a><span v-else>{{ segment.text }}</span></template></p>
		<p v-if="!isTextMode && !showAttachmentMedia && quoteMediaSummary" :class="$style.quoteMediaSummary">引用: {{ quoteMediaSummary }}</p>
		<FxMediaGrid v-if="showAttachmentMedia && renderedQuoteImages.length" :key="`quote-images:${data.post.quote.id}`" v-model:revealed="quoteImagesRevealed" :images="renderedQuoteImages" :isSensitive="data.post.quote.isSensitive" @open="openQuoteImage"/>
		<FxVideoPreview
			v-for="(video, index) in quoteVideos"
			:key="`quote-video:${data.post.quote.id}:${index}`"
			:video="video"
			:index="index"
			:cachePostId="data.post.id"
			:contentPostId="data.post.quote.id"
			target="quote"
			:context="context"
			:isSensitive="data.post.quote.isSensitive"
		/>
		<MkTime v-if="quoteCreatedAt" data-fx-post-time="quote" :class="$style.postTime" :time="quoteCreatedAt" mode="detail"/>
	</section>
	<div v-if="mediaLoadFailed" :class="$style.mediaError" role="status">
		<span>メディアを読み込めませんでした。通信または容量上限を確認して再試行してください。</span>
		<button v-if="attachmentLoadFailed && photoRetryable" type="button" class="_button" @click="retryVisiblePhotos">写真を再試行</button>
		<a v-for="link in mediaErrorLinks" :key="link.href" :href="link.href" target="_blank" rel="nofollow noopener noreferrer" @click.stop>{{ link.label }}</a>
	</div>
	<footer :class="$style.footer">
		<span aria-label="返信"><i class="ti ti-arrow-back-up"></i> {{ formatCount(data.post.replies) }}</span>
		<span aria-label="リポストと引用"><i class="ti ti-repeat"></i> {{ formatCount(repostCount) }}</span>
		<span aria-label="いいね"><i class="ti ti-heart"></i> {{ formatCount(data.post.likes) }}</span>
	</footer>
	<footer :class="$style.legacyFooter" aria-hidden="true">
		<span>返信 {{ data.post.replies }}</span><span>リポストと引用 {{ repostCount }}</span><span>いいね {{ data.post.likes }}</span>
	</footer>
</article>
<div v-else-if="data.state === 'pending'" :class="$style.pending">Xの投稿を取得しています…</div>
<div v-else-if="data.error === 'invalid-response'" :class="$style.source">プレビューを表示できませんでした。</div>
<div v-else :class="$style.source"><p v-if="data.state === 'unavailable'" :data-fx-unavailable-reason="data.unavailableReason ?? 'unknown'">{{ unavailableMessage(data.unavailableReason) }}</p><p v-else-if="data.state === 'disabled'">プレビューは無効です。</p><p v-else>プレビューを表示できません。</p></div>
</template>

<script lang="ts" setup>
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import FxMediaGrid from './FxMediaGrid.vue';
import FxVideoPreview from './FxVideoPreview.vue';
import { api, apiSession, fetchPluginResource, openPluginMedia, truncatePostText, unavailableMessage } from './api.js';
import { retainResource } from './media-cache.js';
import { loadPreview } from './preview-loader.js';
import type { PreviewResponse, PublicPost, PublicTextEntity, PublicVideo } from './api.js';
import type { PluginMediaItem, PreviewContext, PreviewInput } from '@/plugin-api.js';
import type { ResourceLease } from './media-cache.js';
import { MkA, MkTime, MkUrl } from '@/plugin-api.js';
import { loadPhotoIndexesSequentially, orderPhotoPreviews } from './photo-gallery.js';
import { fetchPreparedPublicBlob } from './public-media.js';
import type { PreparedPublicMedia } from './public-media.js';

const props = defineProps<{ data: PreviewResponse; input: PreviewInput; context: PreviewContext }>();
const expanded = ref(false);
type LoadedMedia = { src: string; blob: Blob };
type IdentityBundle = {
	avatar: LoadedMedia | null;
	quoteAvatar: LoadedMedia | null;
	mainFailed: boolean;
	quoteFailed: boolean;
	objectUrls: string[];
	failed: boolean;
};
type AttachmentBundle = {
	images: Array<LoadedMedia | null>;
	imageStates: Array<'loading' | 'error' | 'forbidden' | 'limit' | undefined>;
	quoteImages: Array<LoadedMedia | null>;
	quoteImageStates: Array<'loading' | 'error' | 'forbidden' | 'limit' | undefined>;
	objectUrls: string[];
	failed: boolean;
	bytes: number;
};
let identityGeneration = 0;
let attachmentGeneration = 0;
let identityLease: ResourceLease<IdentityBundle> | null = null;
let attachmentLease: ResourceLease<AttachmentBundle> | null = null;
let attachmentBundle: AttachmentBundle | null = null;
let viewerController: AbortController | null = null;
let identityKey = '';
let attachmentKey = '';
const avatarSrc = ref<string | null>(null);
const imageSources = ref<Array<string | null>>([]);
const imageBlobs = ref<Array<Blob | null>>([]);
const imageStates = ref<Array<'loading' | 'error' | 'forbidden' | 'limit' | undefined>>([]);
const imagesRevealed = ref(false);
const quoteAvatarSrc = ref<string | null>(null);
const quoteImageSources = ref<Array<string | null>>([]);
const quoteImageBlobs = ref<Array<Blob | null>>([]);
const quoteImageStates = ref<Array<'loading' | 'error' | 'forbidden' | 'limit' | undefined>>([]);
const quoteImagesRevealed = ref(false);
const identityLoadFailed = ref(false);
const identityMainFailed = ref(false);
const identityQuoteFailed = ref(false);
const attachmentLoadFailed = ref(false);
const photoRetryBlocked = ref(false);
const mediaLoadFailed = computed(() => [identityLoadFailed.value, attachmentLoadFailed.value].includes(true));
const photoRetryable = computed(() => !photoRetryBlocked.value && [...imageStates.value, ...quoteImageStates.value].includes('error'));
const renderedImages = computed(() => (props.data.post?.images ?? []).map((image, index) => ({ index, src: imageSources.value[index] ?? null, alt: image.alt ?? '', width: image.width, height: image.height, state: imageStates.value[index] ?? 'loading' as const })));
const renderedQuoteImages = computed(() => (props.data.post?.quote?.images ?? []).map((image, index) => ({ index, src: quoteImageSources.value[index] ?? null, alt: image.alt ?? '', width: image.width, height: image.height, state: quoteImageStates.value[index] ?? 'loading' as const })));
const previewMode = computed(() => {
	const value = props.context.preferenceValue;
	return value === 'text' || value === 'ogp' || value === 'media' ? value : 'media';
});
const isTextMode = computed(() => previewMode.value === 'text');
const showAttachmentMedia = computed(() => props.context.detail && previewMode.value === 'media');
const textSummary = computed(() => truncatePostText(props.data.post?.text ?? ''));
const displayedText = computed(() => expanded.value ? props.data.post?.text ?? '' : textSummary.value.text);
const displayedTextSegments = computed(() => richTextSegments(displayedText.value, props.data.post?.textEntities ?? []));
const quoteTextSegments = computed(() => richTextSegments(props.data.post?.quote?.text ?? '', props.data.post?.quote?.textEntities ?? []));
const noteHref = computed(() => props.context.source?.kind === 'note' ? `/notes/${encodeURIComponent(props.context.source.noteId)}` : null);
const postCreatedAt = computed(() => validDate(props.data.post?.createdAt));
const quoteCreatedAt = computed(() => validDate(props.data.post?.quote?.createdAt));
const mainVideos = computed(() => !showAttachmentMedia.value ? [] : props.data.post?.videos?.length ? props.data.post.videos : props.data.post?.video ? [props.data.post.video] : []);
const quoteVideos = computed(() => !showAttachmentMedia.value ? [] : props.data.post?.quote?.videos?.length ? props.data.post.quote.videos : props.data.post?.quote?.video ? [props.data.post.quote.video] : []);
const mediaSummary = computed(() => formatMediaSummary((props.data.post?.images ?? []).length, props.data.post?.videos?.length ?? (props.data.post?.video ? 1 : 0)));
const quoteMediaSummary = computed(() => formatMediaSummary((props.data.post?.quote?.images ?? []).length, props.data.post?.quote?.videos?.length ?? (props.data.post?.quote?.video ? 1 : 0)));
const authorProfileUrl = computed(() => props.data.post?.author.profileUrl ?? null);
const quoteAuthorProfileUrl = computed(() => props.data.post?.quote?.author.profileUrl ?? null);

function xStatusUrl(id?: string): string | null {
	return id != null && /^[0-9]{2,20}$/.test(id) ? `https://x.com/i/status/${id}` : null;
}

const mediaErrorLinks = computed(() => {
	const post = props.data.post;
	if (post == null) return [];
	const mainFailed = identityMainFailed.value || imageStates.value.some(state => state === 'error' || state === 'forbidden' || state === 'limit');
	const quoteFailed = identityQuoteFailed.value || quoteImageStates.value.some(state => state === 'error' || state === 'forbidden' || state === 'limit');
	const links = [
		mainFailed ? { href: xStatusUrl(post.id), label: 'Xで投稿を開く' } : null,
		quoteFailed ? { href: xStatusUrl(post.quote?.id), label: 'Xで引用投稿を開く' } : null,
	].filter((link): link is { href: string; label: string } => link?.href != null);
	return links.filter((link, index) => links.findIndex(candidate => candidate.href === link.href) === index);
});
const repostCount = computed(() => Math.min(Number.MAX_SAFE_INTEGER, Math.max(0, props.data.post?.reposts ?? 0) + Math.max(0, props.data.post?.quotes ?? 0)));

const countFormatter = new Intl.NumberFormat();

function validDate(value?: string): string | null {
	if (value == null || !Number.isFinite(Date.parse(value))) return null;
	return value;
}

function formatCount(value: number): string {
	return countFormatter.format(Math.max(0, value));
}

function formatMediaSummary(imageCount: number, videoCount: number): string {
	return [imageCount > 0 ? `画像${imageCount}件` : '', videoCount > 0 ? `動画${videoCount}件` : ''].filter(Boolean).join('・');
}

function sameImageDescriptors(left: PublicPost['images'], right: PublicPost['images']): boolean {
	return left.length === right.length && left.every((image, index) => image.url === right[index]?.url && image.width === right[index]?.width && image.height === right[index]?.height);
}

function samePhotoOwnership(left: NonNullable<PreviewResponse['post']>, right: NonNullable<PreviewResponse['post']>): boolean {
	if (left.id !== right.id || (left.isSensitive === true) !== (right.isSensitive === true) || !sameImageDescriptors(left.images, right.images)) return false;
	if ((left.quote == null) !== (right.quote == null)) return false;
	if (left.quote == null || right.quote == null) return left.quote == null && right.quote == null;
	return left.quote.id === right.quote.id && (left.quote.isSensitive === true) === (right.quote.isSensitive === true) && sameImageDescriptors(left.quote.images, right.quote.images);
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
	avatarSrc.value = null;
	quoteAvatarSrc.value = null;
	identityLoadFailed.value = false;
	identityMainFailed.value = false;
	identityQuoteFailed.value = false;
}

function clearAttachments(): void {
	attachmentGeneration++;
	photoRetryBlocked.value = false;
	viewerController?.abort();
	viewerController = null;
	attachmentBundle = null;
	attachmentLease?.release();
	attachmentLease = null;
	attachmentKey = '';
	imageSources.value = [];
	imageBlobs.value = [];
	imageStates.value = [];
	quoteImageSources.value = [];
	quoteImageBlobs.value = [];
	quoteImageStates.value = [];
	attachmentLoadFailed.value = false;
}

function clearMedia(): void {
	clearIdentity();
	clearAttachments();
}

async function fetchPrivateMedia(path: string, signal: AbortSignal): Promise<LoadedMedia | null> {
	if (signal.aborted || !path.startsWith('/') || path.startsWith('//')) return null;
	const url = new URL(path, window.location.origin);
	const match = /^\/api\/plugin\/fxtwitter-preview\/media\/([0-9]{2,20})\/(?:avatar|image|poster|quote-avatar|quote-image|quote-poster)\/(?:0|[1-9][0-9]*)$/.exec(url.pathname);
	if (url.origin !== window.location.origin || url.hash !== '' || match?.[1] !== props.data.post?.id || url.searchParams.size !== 1 || url.searchParams.get('noteId') !== props.context.source?.noteId) return null;
	const response = await fetchPluginResource(`${url.pathname}${url.search}`, {
		method: 'GET',
		cache: 'no-store',
		signal,
	});
	const mime = response.headers.get('Content-Type')?.split(';')[0].trim().toLowerCase();
	// Cancellation can occur during the awaited host fetch, beyond TypeScript's synchronous narrowing.
	// eslint-disable-next-line @typescript-eslint/no-unnecessary-condition
	if (signal.aborted || !response.ok || !['image/jpeg', 'image/png', 'image/gif', 'image/webp'].includes(mime ?? '')) return null;
	const blob = await response.blob();
	// Cached Blob reads can also settle after abort; covered by the late-blob mutation.
	// eslint-disable-next-line @typescript-eslint/no-unnecessary-condition
	if (signal.aborted || blob.type !== mime) return null;
	return { src: URL.createObjectURL(blob), blob };
}

async function fetchPreparedImage(path: string, kind: 'image' | 'quote-image', index: number, signal: AbortSignal, maxBytes: number): Promise<LoadedMedia> {
	const post = props.data.post;
	const noteId = props.context.source?.noteId;
	if (post == null || noteId == null || !path.startsWith('/') || path.startsWith('//')) throw new Error('invalid source');
	if (signal.aborted) throw new DOMException('aborted', 'AbortError');
	const url = new URL(path, window.location.origin);
	const expectedPath = `/api/plugin/fxtwitter-preview/media/${post.id}/${kind}/${index}`;
	if (url.origin !== window.location.origin || url.hash !== '' || url.pathname !== expectedPath || url.searchParams.size !== 1 || url.searchParams.get('noteId') !== noteId) throw new Error('invalid media route');
	const prepared = await api<PreparedPublicMedia>('media/prepare', { noteId, postId: post.id, kind, index }, { signal });
	if (signal.aborted) throw new DOMException('aborted', 'AbortError');
	const blob = await fetchPreparedPublicBlob(prepared, { expectedType: 'image', maxBytes, signal });
	return { src: URL.createObjectURL(blob), blob };
}

async function loadIdentity(): Promise<void> {
	const post = props.data.post;
	const source = props.context.source;
	if (isTextMode.value) {
		clearIdentity();
		return;
	}
	if (post == null || source?.kind !== 'note') return;
	const nextKey = `${apiSession.value}:${source.noteId}:${post.id}:identity`;
	if (identityLease != null && identityKey === nextKey) return;
	clearIdentity();
	const current = identityGeneration;
	const quote = post.quote;
	const lease = retainResource<IdentityBundle>(nextKey, async signal => {
		const loadOne = async (path?: string): Promise<{ media: LoadedMedia | null; failed: boolean }> => {
			if (path == null) return { media: null, failed: false };
			const value = await fetchPrivateMedia(path, signal).catch(() => null);
			return { media: value, failed: value == null && !signal.aborted };
		};
		const [mainIdentity, quoteIdentity] = await Promise.all([
			loadOne(post.author.avatarUrl),
			loadOne(quote?.author.avatarUrl),
		]);
		const avatar = mainIdentity.media;
		const quoteAvatar = quoteIdentity.media;
		const objectUrls = [avatar, quoteAvatar].flatMap(value => value == null ? [] : [value.src]);
		return { avatar, quoteAvatar, mainFailed: mainIdentity.failed, quoteFailed: quoteIdentity.failed, objectUrls, failed: mainIdentity.failed || quoteIdentity.failed };
	}, bundle => {
		for (const objectUrl of bundle.objectUrls) URL.revokeObjectURL(objectUrl);
	});
	identityLease = lease;
	identityKey = nextKey;
	try {
		const bundle = await lease.promise;
		if (current !== identityGeneration || identityLease !== lease) return;
		avatarSrc.value = bundle.avatar?.src ?? null;
		quoteAvatarSrc.value = bundle.quoteAvatar?.src ?? null;
		identityLoadFailed.value = bundle.failed;
		identityMainFailed.value = bundle.mainFailed;
		identityQuoteFailed.value = bundle.quoteFailed;
	} catch {
		if (current === identityGeneration && identityLease === lease) {
			identityLoadFailed.value = true;
			identityMainFailed.value = post.author.avatarUrl != null;
			identityQuoteFailed.value = quote?.author.avatarUrl != null;
		}
	}
}

async function loadAttachments(): Promise<void> {
	const post = props.data.post;
	const source = props.context.source;
	if (post == null || source?.kind !== 'note' || !showAttachmentMedia.value) {
		clearAttachments();
		return;
	}
	const nextKey = `${apiSession.value}:${source.noteId}:${post.id}:attachments`;
	if (attachmentLease != null && attachmentKey === nextKey) return;
	clearAttachments();
	const current = attachmentGeneration;
	const quote = post.quote;
	const lease = retainResource<AttachmentBundle>(nextKey, async signal => {
		let failed = false;
		let bytes = 0;
		let images: Array<LoadedMedia | null> = Array(post.images.length).fill(null);
		let quoteImages: Array<LoadedMedia | null> = Array(quote?.images.length ?? 0).fill(null);
		let mainStates: AttachmentBundle['imageStates'] = Array(post.images.length).fill('loading');
		let quoteStates: AttachmentBundle['quoteImageStates'] = Array(quote?.images.length ?? 0).fill('loading');
		const targets = orderPhotoPreviews(post.images.length, quote?.images.length ?? 0);
		for (const target of targets) {
			const descriptors = target === 'main' ? post.images : quote?.images ?? [];
			const indexes = descriptors.map((_, index) => index).slice(0, descriptors.length > 4 ? 6 : descriptors.length);
			const loaded = await loadPhotoIndexesSequentially(
				descriptors,
				indexes,
				target === 'main' ? images : quoteImages,
				bytes,
				64 * 1024 * 1024,
				(path, index, remaining) => fetchPreparedImage(path, target === 'main' ? 'image' : 'quote-image', index, signal, remaining),
				false,
				signal,
			);
			bytes = loaded.bytes;
			failed ||= loaded.failures.length > 0;
			const failureByIndex = new Map(loaded.failures.map(failure => [failure.index, failure.reason]));
			const states = loaded.items.map((item, index) => item != null ? undefined : failureByIndex.get(index) === 'budget' ? 'limit' as const : failureByIndex.get(index) === 'forbidden' ? 'forbidden' as const : failureByIndex.has(index) ? 'error' as const : 'loading' as const);
			if (target === 'main') {
				images = loaded.items;
				mainStates = states;
			} else {
				quoteImages = loaded.items;
				quoteStates = states;
			}
			if (signal.aborted) break;
		}
		// Poster fetching is delegated to the individual video controls so it
		// shares the same prepare/public-byte validation path as video playback.
		const objectUrls = [...images, ...quoteImages].flatMap(value => value == null ? [] : [value.src]);
		return { images, imageStates: mainStates, quoteImages, quoteImageStates: quoteStates, objectUrls, failed, bytes };
	}, bundle => {
		for (const objectUrl of bundle.objectUrls) URL.revokeObjectURL(objectUrl);
	});
	attachmentLease = lease;
	attachmentKey = nextKey;
	try {
		const bundle = await lease.promise;
		if (current !== attachmentGeneration || attachmentLease !== lease) return;
		attachmentBundle = bundle;
		imageSources.value = bundle.images.map(value => value?.src ?? null);
		imageBlobs.value = bundle.images.map(value => value?.blob ?? null);
		imageStates.value = bundle.imageStates;
		quoteImageSources.value = bundle.quoteImages.map(value => value?.src ?? null);
		quoteImageBlobs.value = bundle.quoteImages.map(value => value?.blob ?? null);
		quoteImageStates.value = bundle.quoteImageStates;
		attachmentLoadFailed.value = bundle.failed;
	} catch {
		if (current === attachmentGeneration && attachmentLease === lease) attachmentLoadFailed.value = true;
	}
}

async function loadMedia(): Promise<void> {
	const post = props.data.post;
	const source = props.context.source;
	if (!props.context.detail || !props.context.revealed || !props.context.active || (props.data.state !== 'ready' && props.data.state !== 'stale') || post == null || source?.kind !== 'note') {
		clearMedia();
		return;
	}
	if (!props.context.inViewport) return;
	await Promise.all([loadIdentity(), loadAttachments()]);
}

async function retryVisiblePhotos(): Promise<void> {
	const initialPost = props.data.post;
	const bundle = attachmentBundle;
	if (initialPost == null || bundle == null || props.context.source?.kind !== 'note' || !props.context.detail || !props.context.active || !props.context.revealed || !props.context.inViewport) return;
	viewerController?.abort();
	const controller = new AbortController();
	viewerController = controller;
	const generation = attachmentGeneration;
	let post = initialPost;
	try {
		const refreshed = await loadPreview(props.input, props.context, controller.signal);
		if (controller.signal.aborted || generation !== attachmentGeneration || attachmentBundle !== bundle) return;
		if ((refreshed.state !== 'ready' && refreshed.state !== 'stale') || refreshed.post == null || !samePhotoOwnership(initialPost, refreshed.post)) {
			photoRetryBlocked.value = true;
			attachmentLoadFailed.value = true;
			return;
		}
		post = refreshed.post;
	} catch {
		if (!controller.signal.aborted && generation === attachmentGeneration) attachmentLoadFailed.value = true;
		return;
	}
	const quote = post.quote;
	let bytes = [...imageBlobs.value, ...quoteImageBlobs.value].reduce((sum, blob) => sum + (blob?.size ?? 0), 0);
	let failed = false;
	for (const target of orderPhotoPreviews(post.images.length, quote?.images.length ?? 0)) {
		const descriptors = target === 'main' ? post.images : quote?.images ?? [];
		const states = target === 'main' ? imageStates.value : quoteImageStates.value;
		const existing = target === 'main' ? bundle.images : bundle.quoteImages;
		const indexes = descriptors.map((_, index) => index).slice(0, descriptors.length > 4 ? 6 : descriptors.length).filter(index => existing[index] == null && states[index] !== 'loading' && states[index] !== 'forbidden');
		const result = await loadPhotoIndexesSequentially(descriptors, indexes, existing, bytes, 64 * 1024 * 1024, (path, index, remaining) => fetchPreparedImage(path, target === 'main' ? 'image' : 'quote-image', index, controller.signal, remaining), false, controller.signal);
		if (controller.signal.aborted || generation !== attachmentGeneration || attachmentBundle !== bundle) break;
		bytes = result.bytes;
		failed ||= result.failures.length > 0;
		const priorUrls = new Set(existing.flatMap(item => item == null ? [] : [item.src]));
		for (const item of result.items) if (item != null && !priorUrls.has(item.src)) bundle.objectUrls.push(item.src);
		if (target === 'main') {
			bundle.images = result.items;
			imageSources.value = result.items.map(item => item?.src ?? null);
			imageBlobs.value = result.items.map(item => item?.blob ?? null);
			const failures = new Map(result.failures.map(failure => [failure.index, failure.reason]));
			imageStates.value = result.items.map((item, index) => item != null ? undefined : failures.get(index) === 'budget' ? 'limit' : failures.get(index) === 'forbidden' ? 'forbidden' : failures.has(index) ? 'error' : imageStates.value[index] ?? 'loading');
			bundle.imageStates = imageStates.value;
		} else {
			bundle.quoteImages = result.items;
			quoteImageSources.value = result.items.map(item => item?.src ?? null);
			quoteImageBlobs.value = result.items.map(item => item?.blob ?? null);
			const failures = new Map(result.failures.map(failure => [failure.index, failure.reason]));
			quoteImageStates.value = result.items.map((item, index) => item != null ? undefined : failures.get(index) === 'budget' ? 'limit' : failures.get(index) === 'forbidden' ? 'forbidden' : failures.has(index) ? 'error' : quoteImageStates.value[index] ?? 'loading');
			bundle.quoteImageStates = quoteImageStates.value;
		}
	}
	if (viewerController === controller) viewerController = null;
	if (generation === attachmentGeneration && !controller.signal.aborted) {
		bundle.bytes = bytes;
		bundle.failed = failed;
		attachmentLoadFailed.value = failed;
	}
}

async function openGallery(target: 'main' | 'quote', selectedIndex: number): Promise<void> {
	const post = props.data.post;
	const quote = post?.quote;
	const isQuote = target === 'quote';
	const content = isQuote ? quote : post;
	const descriptors = content?.images ?? [];
	if (post == null || content == null || selectedIndex < 0 || selectedIndex >= descriptors.length || !props.context.detail || !props.context.active || !props.context.revealed || !props.context.inViewport || props.context.source?.kind !== 'note') return;
	const generation = attachmentGeneration;
	const noteId = props.context.source.noteId;
	const controller = new AbortController();
	viewerController?.abort();
	viewerController = controller;
	let loaded = isQuote ? [...quoteImageBlobs.value.map((blob, index) => blob == null ? null : { blob, src: quoteImageSources.value[index]! })] : [...imageBlobs.value.map((blob, index) => blob == null ? null : { blob, src: imageSources.value[index]! })];
	const bytesLoaded = [...imageBlobs.value, ...quoteImageBlobs.value].reduce((sum, blob) => sum + (blob?.size ?? 0), 0);
	const result = await loadPhotoIndexesSequentially(
		descriptors,
		descriptors.map((_, index) => index),
		loaded,
		bytesLoaded,
		64 * 1024 * 1024,
		(path, index, remaining) => fetchPreparedImage(path, isQuote ? 'quote-image' : 'image', index, controller.signal, remaining),
		true,
		controller.signal,
	).catch(() => null);
	if (viewerController === controller) viewerController = null;
	if (result == null || controller.signal.aborted || generation !== attachmentGeneration || props.context.source?.noteId !== noteId || props.data.post?.id !== post.id || !props.context.detail || !props.context.active || !props.context.revealed || !props.context.inViewport) {
		if (result != null) {
			const retained = new Set([...imageSources.value, ...quoteImageSources.value].filter((value): value is string => value != null));
			for (const item of result.items) if (item != null && !retained.has(item.src)) URL.revokeObjectURL(item.src);
		}
		return;
	}
	const states = result.items.map((item, index) => item != null ? undefined : result.failures.some(failure => failure.index === index && failure.reason === 'budget') ? 'limit' as const : result.failures.some(failure => failure.index === index && failure.reason === 'forbidden') ? 'forbidden' as const : 'error' as const);
	if (result.failures.length > 0 || result.items.some(item => item == null)) {
		const retained = new Set([...imageSources.value, ...quoteImageSources.value].filter((value): value is string => value != null));
		for (const item of result.items) if (item != null && !retained.has(item.src)) URL.revokeObjectURL(item.src);
		attachmentLoadFailed.value = true;
		if (isQuote) quoteImageStates.value = states;
		else imageStates.value = states;
		return;
	}
	if (attachmentBundle == null) {
		for (const item of result.items) if (item != null) URL.revokeObjectURL(item.src);
		return;
	}
	const complete = result.items as LoadedMedia[];
	const prior = isQuote ? attachmentBundle.quoteImages : attachmentBundle.images;
	const priorURLs = new Set(prior.flatMap(item => item == null ? [] : [item.src]));
	for (const item of complete) if (!priorURLs.has(item.src)) attachmentBundle.objectUrls.push(item.src);
	if (isQuote) {
		attachmentBundle.quoteImages = complete;
		quoteImageSources.value = complete.map(item => item.src);
		quoteImageBlobs.value = complete.map(item => item.blob);
		quoteImageStates.value = states;
	} else {
		attachmentBundle.images = complete;
		imageSources.value = complete.map(item => item.src);
		imageBlobs.value = complete.map(item => item.blob);
		imageStates.value = states;
	}
	attachmentBundle.bytes = result.bytes;
	const sensitive = content.isSensitive === true;
	const initiallyRevealed = isQuote ? quoteImagesRevealed.value : imagesRevealed.value;
	const mediaItems: PluginMediaItem[] = descriptors.map((image, imageIndex) => ({
		id: `${content.id}:${imageIndex}`,
		type: 'image',
		blob: complete[imageIndex].blob,
		width: image.width,
		height: image.height,
		filename: `x-${content.id}-${imageIndex + 1}`,
		alt: image.alt,
		isSensitive: sensitive,
		initiallyRevealed,
	}));
	await openPluginMedia(mediaItems, `${content.id}:${selectedIndex}`);
}

async function openImage(index: number): Promise<void> { await openGallery('main', index); }

async function openQuoteImage(index: number): Promise<void> { await openGallery('quote', index); }

watch(() => [props.data, props.context.source?.noteId, props.context.revealed, props.context.active, props.context.inViewport, showAttachmentMedia.value, isTextMode.value], () => { void loadMedia(); });
watch(() => props.data, () => { photoRetryBlocked.value = false; });
watch(() => props.context.inViewport, visible => { if (!visible) viewerController?.abort(); });
watch(apiSession, () => { clearMedia(); });
watch(() => props.data.post?.id, () => { expanded.value = false; });
onMounted(() => {
	void loadMedia();
});
onBeforeUnmount(() => { clearMedia(); });
</script>

<style lang="scss" module>
.card { border: 1px solid var(--MI_THEME-divider); border-left: 4px solid var(--MI_THEME-fg); border-radius: var(--MI-radius); overflow: hidden; background: var(--MI_THEME-panel); }
.header { display: flex; align-items: center; gap: 10px; padding: 14px 14px 8px; }
.avatarLink { display: block; flex: 0 0 auto; border-radius: 50%; }
.avatar { display: block; width: 40px; height: 40px; border-radius: 50%; object-fit: cover; }
.identity { display: flex; min-width: 0; flex: 1; flex-direction: column; line-height: 1.25; }
.identity span { color: var(--MI_THEME-fgTransparentWeak); overflow: hidden; text-overflow: ellipsis; }
.identityLink { display: flex; min-width: 0; flex-direction: column; color: inherit; text-decoration: none; }
.postTime { flex: 0 0 auto; color: var(--MI_THEME-fgTransparentWeak); font-size: 0.82em; }
.xMark { display: grid; width: 44px; height: 44px; place-items: center; border-radius: 50%; color: var(--MI_THEME-fg); text-decoration: none; font-size: 1.8em; font-weight: 700; }
.text { margin: 0; padding: 8px 14px 14px; white-space: pre-wrap; overflow-wrap: anywhere; line-height: 1.55; }
.mediaSummary { margin: -8px 14px 14px; color: var(--MI_THEME-fgTransparentWeak); font-size: 0.9em; }
.quote { display: block; margin: 14px; border: 1px solid var(--MI_THEME-divider); border-radius: var(--MI-radius); overflow: hidden; color: inherit; text-decoration: none; }
.quoteHeader { display: flex; align-items: center; gap: 8px; padding: 10px 10px 4px; }
.quoteAvatar { width: 28px; height: 28px; border-radius: 50%; object-fit: cover; }
.quoteSource { flex: 0 0 auto; color: inherit; text-decoration: none; font-size: 1.15em; }
.quoteText { margin: 0; padding: 8px 10px 10px; white-space: pre-wrap; overflow-wrap: anywhere; line-height: 1.45; }
.quoteMediaSummary { margin: -4px 10px 10px; color: var(--MI_THEME-fgTransparentWeak); font-size: 0.85em; }
.mediaError { margin: 10px 14px; color: var(--MI_THEME-warn); font-size: 0.85em; }
.footer { display: flex; flex-wrap: wrap; gap: 16px; padding: 10px 14px; border-top: 1px solid var(--MI_THEME-divider); color: var(--MI_THEME-fgTransparentWeak); font-size: 0.85em; }
.footer span { display: inline-flex; align-items: center; gap: 5px; }
.legacyFooter { display: none; }
.more { display: block; width: max-content; margin: -8px 14px 12px; color: var(--MI_THEME-accent); text-decoration: none; }
.stale { margin: -4px 14px 12px; color: var(--MI_THEME-warn); font-size: 0.85em; }
.pending, .source { display: block; padding: 12px 14px; border: 1px solid var(--MI_THEME-divider); border-radius: var(--MI-radius); }
.pending { color: var(--MI_THEME-fgTransparentWeak); }
</style>
