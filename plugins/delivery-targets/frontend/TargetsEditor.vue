<!--
SPDX-FileCopyrightText: Elythia-Network
SPDX-License-Identifier: AGPL-3.0-only
-->

<template>
<div class="_gaps_m">
	<div class="_buttons">
		<MkButton :primary="option.mode === 'include'" rounded small data-testid="delivery-targets-include" @click="setMode('include')">{{ messages.include }}</MkButton>
		<MkButton :primary="option.mode === 'exclude'" rounded small data-testid="delivery-targets-exclude" @click="setMode('exclude')">{{ messages.exclude }}</MkButton>
	</div>
	<div :class="$style.caption">{{ option.mode === 'include' ? messages.includeCaption : messages.excludeCaption }}</div>

	<MkInput v-model="draft" type="text" :placeholder="messages.hostPlaceholder" data-testid="delivery-targets-input" @enter="add()">
		<template #label>{{ messages.host }}</template>
	</MkInput>
	<div v-if="suggestions.length > 0" class="_buttons">
		<MkButton v-for="s in suggestions" :key="s" small rounded @click="addHost(s)">{{ s }}</MkButton>
	</div>
	<MkButton primary :disabled="draft.trim() === ''" data-testid="delivery-targets-add" @click="add()"><i class="ti ti-plus"></i> {{ messages.add }}</MkButton>

	<div v-if="option.hosts.length === 0" :class="$style.caption">{{ messages.empty }}</div>
	<div v-else class="_buttons">
		<MkButton v-for="h in option.hosts" :key="h" small rounded data-testid="delivery-targets-host" @click="remove(h)">{{ h }} <i class="ti ti-x"></i></MkButton>
	</div>
	<div :class="$style.caption">{{ messages.noteCaption }}</div>
</div>
</template>

<script lang="ts" setup>
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import { MkButton, MkInput } from '@/plugin-api.js';
import { messages } from './messages.js';
import { addHosts, removeHost, toOption } from './option.js';
import type { DeliveryTargetsOption } from './option.js';
import { suggestHosts } from './host.js';

const props = defineProps<{
	modelValue: unknown;
}>();

const emit = defineEmits<{
	(ev: 'update:modelValue', value: DeliveryTargetsOption): void;
}>();

const option = computed(() => toOption(props.modelValue));
const draft = ref('');
const suggestions = ref<string[]>([]);

function update(next: DeliveryTargetsOption) {
	emit('update:modelValue', next);
}

function setMode(mode: DeliveryTargetsOption['mode']) {
	update({ ...option.value, mode });
}

function add() {
	update(addHosts(option.value, draft.value));
	draft.value = '';
	suggestions.value = [];
}

function addHost(host: string) {
	update(addHosts(option.value, host));
	draft.value = '';
	suggestions.value = [];
}

function remove(host: string) {
	update(removeHost(option.value, host));
}

// 補完は入力が止まってから引く。打つたびに API を呼ばない。
let timer: number | null = null;
let controller: AbortController | null = null;
watch(draft, (value) => {
	if (timer != null) window.clearTimeout(timer);
	controller?.abort();
	if (value.trim() === '') {
		suggestions.value = [];
		return;
	}
	timer = window.setTimeout(async () => {
		controller = new AbortController();
		const hosts = await suggestHosts(value, controller.signal);
		suggestions.value = hosts.filter(h => !option.value.hosts.includes(h));
	}, 300);
});

onBeforeUnmount(() => {
	if (timer != null) window.clearTimeout(timer);
	controller?.abort();
});
</script>

<style lang="scss" module>
.caption {
	font-size: 0.85em;
	opacity: 0.7;
}
</style>
