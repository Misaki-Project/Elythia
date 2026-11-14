<!--
SPDX-FileCopyrightText: Elythia-Network
SPDX-License-Identifier: AGPL-3.0-only
-->

<template>
<div v-if="currentData.state === 'pending'" :class="$style.pending">
	<p role="status">{{ loading ? 'Xの投稿を再取得しています…' : '投稿の取得がまだ完了していません。時間をおいて再試行できます。' }}</p>
	<p v-if="failed" data-fx-preview-error role="status">取得に失敗しました。時間をおいて再試行してください。</p>
	<button type="button" data-fx-preview-retry :disabled="!eligible || loading || cooling" @click.stop="retry">{{ loading ? '取得中…' : '再試行' }}</button>
	<a :href="sourceHref" target="_blank" rel="nofollow noopener noreferrer">Xで投稿を開く</a>
</div>
<FxPreview v-else :data="currentData" :input="input" :context="context"/>
</template>

<script lang="ts" setup>
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue';
import FxPreview from './FxPreview.vue';
import { apiSession } from './api.js';
import { loadPreview, statusID } from './preview-loader.js';
import type { PreviewResponse } from './api.js';
import type { PreviewContext, PreviewInput } from '@/plugin-api.js';

const props = defineProps<{ data: PreviewResponse; input: PreviewInput; context: PreviewContext }>();
const currentData = shallowRef<PreviewResponse>({ state: 'pending' });
const loading = ref(false);
const failed = ref(false);
const cooling = ref(false);
const sourceHref = computed(() => props.input.kind === 'url' ? props.input.href : '#');
const ownershipKey = computed(() => [apiSession.value, sourceHref.value, props.context.source?.noteId ?? ''].join('\u0000'));
const eligible = computed(() => statusID(props.input) != null && props.context.source?.kind === 'note' && props.context.detail && props.context.revealed && props.context.active && props.context.inViewport);
let controller: AbortController | null = null;
let generation = 0;
let cooldownTimer: number | null = null;

function invalidate(): void {
	generation++;
	controller?.abort(); controller = null;
	loading.value = false;
}

function clearCooldown(): void {
	if (cooldownTimer != null) window.clearTimeout(cooldownTimer);
	cooldownTimer = null; cooling.value = false;
}

function cooldown(nextPollAt?: string): void {
	clearCooldown(); cooling.value = true;
	cooldownTimer = window.setTimeout(() => { cooldownTimer = null; cooling.value = false; }, Math.max(1_000, (Date.parse(nextPollAt ?? '') || 0) - Date.now()));
}

function ownsRequest(current: number, key: string, request: AbortController): boolean {
	return !request.signal.aborted && current === generation && key === ownershipKey.value && eligible.value;
}

async function retry(): Promise<void> {
	if (!eligible.value || loading.value || cooling.value || currentData.value.state !== 'pending') return;
	const current = ++generation;
	const key = ownershipKey.value;
	const request = new AbortController(); controller = request;
	loading.value = true; failed.value = false;
	try {
		const response = await loadPreview(props.input, props.context, request.signal);
		if (!ownsRequest(current, key, request)) return;
		currentData.value = response;
		if (response.state === 'pending') cooldown(response.nextPollAt);
	} catch {
		if (!ownsRequest(current, key, request)) return;
		currentData.value = { state: 'pending' };
		failed.value = true; cooldown();
	} finally {
		if (current === generation) { controller = null; loading.value = false; }
	}
}

// 親の応答が変わるか所有者が変わった時点で、手動取得した内容を引き継がない。
watch(() => [props.data, ownershipKey.value] as const, ([data], previous) => {
	invalidate(); clearCooldown(); failed.value = false;
	currentData.value = previous == null || data !== previous[0] ? data : { state: 'pending' };
	if (currentData.value.state === 'pending' && Date.parse(currentData.value.nextPollAt ?? '') > Date.now()) cooldown(currentData.value.nextPollAt);
}, { flush: 'sync', immediate: true });
watch(eligible, value => { if (!value) invalidate(); }, { flush: 'sync' });
onBeforeUnmount(() => { invalidate(); clearCooldown(); });
</script>

<style lang="scss" module>
.pending { padding: 12px 14px; border: 1px solid var(--MI_THEME-divider); border-radius: var(--MI-radius); color: var(--MI_THEME-fgTransparentWeak); }
.pending p { margin: 0 0 8px; }
.pending button { margin-right: 12px; }
</style>
