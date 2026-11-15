<!--
SPDX-FileCopyrightText: syuilo and misskey-project
SPDX-License-Identifier: AGPL-3.0-only
-->

<template>
<div :class="$style.card" aria-label="X preview loading" role="status" data-fx-preview-loading>
	<span :class="$style.dots" aria-hidden="true"><span :class="$style.dot"></span><span :class="$style.dot"></span><span :class="$style.dot"></span></span>
</div>
</template>

<style lang="scss" module>
.card {
	display: flex;
	align-items: center;
	justify-content: center;
	min-height: 56px;
	padding: 14px;
	border: 1px solid var(--MI_THEME-divider);
	border-left: 4px solid var(--MI_THEME-fg);
	border-radius: var(--MI-radius);
	background: var(--MI_THEME-panel);
}

.dots {
	display: inline-flex;
	gap: 6px;
}

.dot {
	width: 8px;
	height: 8px;
	border-radius: 50%;
	background: var(--MI_THEME-fg);
	opacity: 0.25;
	animation: blink 1.2s ease-in-out infinite;

	&:nth-child(2) { animation-delay: 0.2s; }
	&:nth-child(3) { animation-delay: 0.4s; }
}

@keyframes blink {
	0%, 80%, 100% { opacity: 0.25; transform: translateY(0); }
	40% { opacity: 0.8; transform: translateY(-3px); }
}

@media (prefers-reduced-motion: reduce) {
	.dot { animation: none; opacity: 0.5; }
}
</style>
