<!--
SPDX-FileCopyrightText: syuilo and misskey-project
SPDX-License-Identifier: AGPL-3.0-only
-->

<template>
<article :class="$style.card" role="status" aria-label="Xの投稿を取得しています" aria-busy="true">
	<div :class="$style.message"><span>Xの投稿を取得しています</span><span :class="$style.dots" aria-hidden="true"><i></i><i></i><i></i></span></div>
	<a v-if="input?.kind === 'url'" :class="$style.openLink" :href="input.href" target="_blank" rel="nofollow noopener noreferrer">Xで開く</a>
</article>
</template>

<script lang="ts" setup>
import type { PreviewInput } from '@/plugin-api.js';

defineProps<{ input?: PreviewInput }>();
</script>

<style lang="scss" module>
.card {
	display: flex;
	flex-wrap: wrap;
	align-items: center;
	gap: 8px 12px;
	padding: 14px;
	border: 1px solid var(--MI_THEME-divider);
	border-left: 4px solid var(--MI_THEME-fg);
	border-radius: var(--MI-radius);
	background: var(--MI_THEME-panel);
	color: var(--MI_THEME-fgTransparentWeak);
}

.message { display: inline-flex; align-items: center; gap: 5px; }
.openLink { color: var(--MI_THEME-accent); }
.dots { display: inline-flex; align-items: center; gap: 3px; }
.dots i {
	width: 4px;
	height: 4px;
	border-radius: 50%;
	background: currentColor;
	animation: loadingDot 1s ease-in-out infinite;
}
.dots i:nth-child(2) { animation-delay: 0.15s; }
.dots i:nth-child(3) { animation-delay: 0.3s; }
@keyframes loadingDot { 0%, 60%, 100% { opacity: 0.3; transform: translateY(0); } 30% { opacity: 1; transform: translateY(-3px); } }

@media (prefers-reduced-motion: reduce) {
	.dots i { animation: none; }
}
</style>
