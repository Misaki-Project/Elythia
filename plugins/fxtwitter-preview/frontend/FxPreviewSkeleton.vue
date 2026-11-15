<!--
SPDX-FileCopyrightText: syuilo and misskey-project
SPDX-License-Identifier: AGPL-3.0-only
-->

<template>
<div :class="$style.card" aria-label="X preview loading" role="status">
	<div :class="[$style.line, $style.identity]"></div>
	<div :class="[$style.line, $style.body]"></div>
	<div :class="[$style.line, $style.bodyShort]"></div>
</div>
</template>

<style lang="scss" module>
.card {
	display: grid;
	gap: 10px;
	padding: 14px;
	border: 1px solid var(--MI_THEME-divider);
	border-left: 4px solid var(--MI_THEME-fg);
	border-radius: var(--MI-radius);
	background: var(--MI_THEME-panel);
}

.line {
	height: 12px;
	border-radius: 999px;
	background: var(--MI_THEME-fgTransparentWeak);
	opacity: 0.32;
	animation: pulse 1.4s ease-in-out infinite;
}

.identity { width: min(42%, 180px); height: 16px; }
.body { width: 92%; }
.bodyShort { width: 68%; }

@keyframes pulse {
	0%, 100% { opacity: 0.22; }
	50% { opacity: 0.42; }
}

@media (prefers-reduced-motion: reduce) {
	.line { animation: none; }
}
</style>
