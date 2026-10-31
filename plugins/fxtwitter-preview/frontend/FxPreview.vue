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
		<MkTime v-if="postCreatedAt" :class="$style.postTime" :time="postCreatedAt" mode="detail"/>
		<a :class="$style.xMark" :href="data.post.url" target="_blank" rel="nofollow noopener noreferrer" aria-label="Xで開く">𝕏</a>
	</header>
	<p :class="$style.text"><template v-for="segment in displayedTextSegments" :key="segment.key"><MkUrl v-if="segment.kind === 'url' && segment.href" :url="segment.href" :aria-label="segment.text" @click.stop/><MkA v-else-if="segment.kind === 'hashtag' && segment.href" class="_link" :to="segment.href" @click.stop>{{ segment.text }}</MkA><a v-else-if="segment.kind === 'mention' && segment.href" class="_link" :href="segment.href" target="_blank" rel="nofollow noopener noreferrer" @click.stop>{{ segment.text }}</a><span v-else>{{ segment.text }}</span></template><span v-if="textSummary.truncated && !expanded">…</span></p>
	<a v-if="textSummary.truncated && !context.detail" :class="$style.more" :href="noteHref">もっと見る</a>
	<button v-else-if="textSummary.truncated && !expanded" type="button" class="_button" :class="$style.more" @click="expanded = true">もっと見る</button>
	<p v-if="data.stale" :class="$style.stale">一時的に取得できないため、前回取得した内容を表示しています。</p>
	<p v-if="!showAttachmentMedia && mediaSummary" :class="$style.mediaSummary">{{ mediaSummary }}</p>
	<FxMediaGrid v-if="showAttachmentMedia && renderedImages.length" :key="`images:${data.post.id}`" v-model:revealed="imagesRevealed" :images="renderedImages" :isSensitive="data.post.isSensitive" @open="openImage"/>
	<FxVideoPreview
		v-else-if="showAttachmentMedia && data.post.video"
		:key="`video:${data.post.id}`"
		:video="data.post.video"
		:posterSrc="posterSrc"
		:cachePostId="data.post.id"
		:contentPostId="data.post.id"
		target="main"
		:context="context"
		:isSensitive="data.post.isSensitive"
	/>
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
			<MkTime v-if="quoteCreatedAt" :class="$style.postTime" :time="quoteCreatedAt" mode="detail"/>
			<a :class="$style.quoteSource" :href="data.post.quote.url" target="_blank" rel="nofollow noopener noreferrer" aria-label="引用をXで開く" @click.stop>𝕏</a>
		</header>
		<p :class="$style.quoteText"><template v-for="segment in quoteTextSegments" :key="segment.key"><MkUrl v-if="segment.kind === 'url' && segment.href" :url="segment.href" :aria-label="segment.text" @click.stop/><MkA v-else-if="segment.kind === 'hashtag' && segment.href" class="_link" :to="segment.href" @click.stop>{{ segment.text }}</MkA><a v-else-if="segment.kind === 'mention' && segment.href" class="_link" :href="segment.href" target="_blank" rel="nofollow noopener noreferrer" @click.stop>{{ segment.text }}</a><span v-else>{{ segment.text }}</span></template></p>
		<p v-if="!showAttachmentMedia && quoteMediaSummary" :class="$style.quoteMediaSummary">引用: {{ quoteMediaSummary }}</p>
		<FxMediaGrid v-if="showAttachmentMedia && renderedQuoteImages.length" :key="`quote-images:${data.post.quote.id}`" v-model:revealed="quoteImagesRevealed" :images="renderedQuoteImages" :isSensitive="data.post.quote.isSensitive" @open="openQuoteImage"/>
		<FxVideoPreview
			v-else-if="showAttachmentMedia && data.post.quote.video"
			:key="`quote-video:${data.post.quote.id}`"
			:video="data.post.quote.video"
			:posterSrc="quotePosterSrc"
			:cachePostId="data.post.id"
			:contentPostId="data.post.quote.id"
			target="quote"
			:context="context"
			:isSensitive="data.post.quote.isSensitive"
		/>
	</section>
	<p v-if="mediaLoadFailed" :class="$style.mediaError">メディアを読み込めませんでした。</p>
	<footer :class="$style.footer">
		<span aria-label="返信"><i class="ti ti-arrow-back-up"></i> {{ formatCount(data.post.replies) }}</span>
		<span aria-label="リポストと引用"><i class="ti ti-repeat"></i> {{ formatCount(repostCount) }}</span>
		<span aria-label="いいね"><i class="ti ti-heart"></i> {{ formatCount(data.post.likes) }}</span>
	</footer>
	<footer :class="$style.legacyFooter" aria-hidden="true">
		<span>返信 {{ data.post.replies }}</span><span>リポストと引用 {{ repostCount }}</span><span>いいね {{ data.post.likes }}</span>
	</footer>
</article>
<div v-else-if="data.state === 'pending'" :class="$style.pending">Xの投稿を取得しています… <a :href="sourceHref" target="_blank" rel="nofollow noopener noreferrer">Xで開く</a></div>
<div v-else-if="data.error === 'invalid-response'" :class="$style.source">プレビューを表示できませんでした。 <a :href="sourceHref" target="_blank" rel="nofollow noopener noreferrer">Xで開く</a></div>
<div v-else :class="$style.source"><p v-if="data.state === 'unavailable'" :data-fx-unavailable-reason="data.unavailableReason ?? 'unknown'">{{ unavailableMessage(data.unavailableReason) }}</p><p v-else-if="data.state === 'disabled'">プレビューは無効です。</p><a :href="sourceHref" target="_blank" rel="nofollow noopener noreferrer">Xで投稿を開く</a></div>
</template>

<script lang="ts" setup>
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import FxMediaGrid from './FxMediaGrid.vue';
import FxVideoPreview from './FxVideoPreview.vue';
import { apiSession, fetchPluginResource, openPluginMedia, truncatePostText, unavailableMessage } from './api.js';
import { retainResource } from './media-cache.js';
import type { PreviewResponse, PublicTextEntity } from './api.js';
import type { PluginMediaItem, PreviewContext, PreviewInput } from '@/plugin-api.js';
import type { ResourceLease } from './media-cache.js';
import { MkA, MkTime, MkUrl } from '@/plugin-api.js';

const props = defineProps<{ data: PreviewResponse; input: PreviewInput; context: PreviewContext }>();
const sourceHref = computed(() => props.input.kind === 'url' ? props.input.href : props.data.post?.url ?? '#');
const expanded = ref(false);
type LoadedMedia = { src: string; blob: Blob };
type IdentityBundle = {
	avatar: LoadedMedia | null;
	quoteAvatar: LoadedMedia | null;
	objectUrls: string[];
	failed: boolean;
};
type AttachmentBundle = {
	images: Array<LoadedMedia | null>;
	poster: LoadedMedia | null;
	quoteImages: Array<LoadedMedia | null>;
	quotePoster: LoadedMedia | null;
	objectUrls: string[];
	failed: boolean;
};
let identityGeneration = 0;
let attachmentGeneration = 0;
let identityLease: ResourceLease<IdentityBundle> | null = null;
let attachmentLease: ResourceLease<AttachmentBundle> | null = null;
let identityKey = '';
let attachmentKey = '';
const avatarSrc = ref<string | null>(null);
const imageSources = ref<Array<string | null>>([]);
const imageBlobs = ref<Array<Blob | null>>([]);
const imagesRevealed = ref(false);
const posterSrc = ref<string | null>(null);
const quoteAvatarSrc = ref<string | null>(null);
const quoteImageSources = ref<Array<string | null>>([]);
const quoteImageBlobs = ref<Array<Blob | null>>([]);
const quoteImagesRevealed = ref(false);
const quotePosterSrc = ref<string | null>(null);
const identityLoadFailed = ref(false);
const attachmentLoadFailed = ref(false);
const mediaLoadFailed = computed(() => [identityLoadFailed.value, attachmentLoadFailed.value].includes(true));
const renderedImages = computed(() => (props.data.post?.images ?? []).flatMap((image, index) => {
	const src = imageSources.value[index];
	return src == null ? [] : [{ index, src, alt: image.alt ?? '', width: image.width, height: image.height }];
}));
const renderedQuoteImages = computed(() => (props.data.post?.quote?.images ?? []).flatMap((image, index) => {
	const src = quoteImageSources.value[index];
	return src == null ? [] : [{ index, src, alt: image.alt ?? '', width: image.width, height: image.height }];
}));
const showAttachmentMedia = computed(() => props.context.detail);
const textSummary = computed(() => truncatePostText(props.data.post?.text ?? ''));
const displayedText = computed(() => expanded.value ? props.data.post?.text ?? '' : textSummary.value.text);
const displayedTextSegments = computed(() => richTextSegments(displayedText.value, props.data.post?.textEntities ?? []));
const quoteTextSegments = computed(() => richTextSegments(props.data.post?.quote?.text ?? '', props.data.post?.quote?.textEntities ?? []));
const noteHref = computed(() => props.context.source?.kind === 'note' ? `/notes/${encodeURIComponent(props.context.source.noteId)}` : sourceHref.value);
const postCreatedAt = computed(() => validDate(props.data.post?.createdAt));
const quoteCreatedAt = computed(() => validDate(props.data.post?.quote?.createdAt));
const mediaSummary = computed(() => formatMediaSummary((props.data.post?.images ?? []).length, props.data.post?.hasVideo === true));
const quoteMediaSummary = computed(() => formatMediaSummary((props.data.post?.quote?.images ?? []).length, props.data.post?.quote?.hasVideo === true));
const authorProfileUrl = computed(() => props.data.post?.author.profileUrl ?? null);
const quoteAuthorProfileUrl = computed(() => props.data.post?.quote?.author.profileUrl ?? null);
const repostCount = computed(() => Math.min(Number.MAX_SAFE_INTEGER, Math.max(0, props.data.post?.reposts ?? 0) + Math.max(0, props.data.post?.quotes ?? 0)));

const countFormatter = new Intl.NumberFormat();

function validDate(value?: string): string | null {
	if (value == null || !Number.isFinite(Date.parse(value))) return null;
	return value;
}

function formatCount(value: number): string {
	return countFormatter.format(Math.max(0, value));
}

function formatMediaSummary(imageCount: number, hasVideo: boolean): string {
	return [imageCount > 0 ? `画像${imageCount}件` : '', hasVideo ? '動画1件' : ''].filter(Boolean).join('・');
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
}

function clearAttachments(): void {
	attachmentGeneration++;
	attachmentLease?.release();
	attachmentLease = null;
	attachmentKey = '';
	imageSources.value = [];
	imageBlobs.value = [];
	posterSrc.value = null;
	quoteImageSources.value = [];
	quoteImageBlobs.value = [];
	quotePosterSrc.value = null;
	attachmentLoadFailed.value = false;
}

function clearMedia(): void {
	clearIdentity();
	clearAttachments();
}

async function fetchMedia(path: string, signal: AbortSignal): Promise<LoadedMedia | null> {
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

async function loadIdentity(): Promise<void> {
	const post = props.data.post;
	const source = props.context.source;
	if (post == null || source?.kind !== 'note') return;
	const nextKey = `${apiSession.value}:${source.noteId}:${post.id}:identity`;
	if (identityLease != null && identityKey === nextKey) return;
	clearIdentity();
	const current = identityGeneration;
	const quote = post.quote;
	const lease = retainResource<IdentityBundle>(nextKey, async signal => {
		let failed = false;
		const loadOne = async (path?: string): Promise<LoadedMedia | null> => {
			if (path == null) return null;
			const value = await fetchMedia(path, signal).catch(() => null);
			if (value == null && !signal.aborted) failed = true;
			return value;
		};
		const [avatar, quoteAvatar] = await Promise.all([
			loadOne(post.author.avatarUrl),
			loadOne(quote?.author.avatarUrl),
		]);
		const objectUrls = [avatar, quoteAvatar].flatMap(value => value == null ? [] : [value.src]);
		return { avatar, quoteAvatar, objectUrls, failed };
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
	} catch {
		if (current === identityGeneration && identityLease === lease) identityLoadFailed.value = true;
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
		const loadOne = async (path?: string): Promise<LoadedMedia | null> => {
			if (path == null) return null;
			const value = await fetchMedia(path, signal).catch(() => null);
			if (value == null && !signal.aborted) failed = true;
			return value;
		};
		const [images, poster, quoteImages, quotePoster] = await Promise.all([
			Promise.all(post.images.map(image => loadOne(image.url))),
			loadOne(post.video?.posterUrl),
			Promise.all(quote?.images.map(image => loadOne(image.url)) ?? []),
			loadOne(quote?.video?.posterUrl),
		]);
		const objectUrls = [...images, poster, ...quoteImages, quotePoster].flatMap(value => value == null ? [] : [value.src]);
		return { images, poster, quoteImages, quotePoster, objectUrls, failed };
	}, bundle => {
		for (const objectUrl of bundle.objectUrls) URL.revokeObjectURL(objectUrl);
	});
	attachmentLease = lease;
	attachmentKey = nextKey;
	try {
		const bundle = await lease.promise;
		if (current !== attachmentGeneration || attachmentLease !== lease) return;
		imageSources.value = bundle.images.map(value => value?.src ?? null);
		imageBlobs.value = bundle.images.map(value => value?.blob ?? null);
		posterSrc.value = bundle.poster?.src ?? null;
		quoteImageSources.value = bundle.quoteImages.map(value => value?.src ?? null);
		quoteImageBlobs.value = bundle.quoteImages.map(value => value?.blob ?? null);
		quotePosterSrc.value = bundle.quotePoster?.src ?? null;
		attachmentLoadFailed.value = bundle.failed;
	} catch {
		if (current === attachmentGeneration && attachmentLease === lease) attachmentLoadFailed.value = true;
	}
}

async function loadMedia(): Promise<void> {
	const post = props.data.post;
	const source = props.context.source;
	if (!props.context.detail || !props.context.revealed || !props.context.active || !props.context.inViewport || (props.data.state !== 'ready' && props.data.state !== 'stale') || post == null || source?.kind !== 'note') {
		clearMedia();
		return;
	}
	await Promise.all([loadIdentity(), loadAttachments()]);
}

async function openImage(index: number): Promise<void> {
	const post = props.data.post;
	if (post == null || !props.context.detail || !props.context.active || !props.context.revealed || !props.context.inViewport || props.context.source == null) return;
	const items = post.images.flatMap<PluginMediaItem>((image, imageIndex) => {
		const blob = imageBlobs.value[imageIndex];
		return blob == null ? [] : [{
			id: `${post.id}:${imageIndex}`,
			type: 'image',
			blob,
			width: image.width,
			height: image.height,
			filename: `x-${post.id}-${imageIndex + 1}`,
			alt: image.alt,
			isSensitive: post.isSensitive === true,
			initiallyRevealed: imagesRevealed.value,
		}];
	});
	await openPluginMedia(items, `${post.id}:${index}`);
}

async function openQuoteImage(index: number): Promise<void> {
	const quote = props.data.post?.quote;
	if (quote == null || !props.context.detail || !props.context.active || !props.context.revealed || !props.context.inViewport || props.context.source == null) return;
	const items = quote.images.flatMap<PluginMediaItem>((image, imageIndex) => {
		const blob = quoteImageBlobs.value[imageIndex];
		return blob == null ? [] : [{
			id: `${quote.id}:${imageIndex}`,
			type: 'image',
			blob,
			width: image.width,
			height: image.height,
			filename: `x-${quote.id}-${imageIndex + 1}`,
			alt: image.alt,
			isSensitive: quote.isSensitive === true,
			initiallyRevealed: quoteImagesRevealed.value,
		}];
	});
	await openPluginMedia(items, `${quote.id}:${index}`);
}

watch(() => [props.data, props.context.source?.noteId, props.context.revealed, props.context.active, props.context.inViewport, showAttachmentMedia.value], () => { void loadMedia(); });
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
