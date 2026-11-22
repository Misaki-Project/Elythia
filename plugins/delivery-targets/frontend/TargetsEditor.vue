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

	<MkFolder :defaultOpen="true" data-testid="delivery-targets-follower-hosts">
		<template #label>{{ messages.followerHosts }}</template>
		<div class="_gaps_s">
			<div :class="$style.caption">{{ messages.followerHostsCaption }}</div>
			<MkLoading v-if="followers === undefined"/>
			<div v-else-if="followers === null" :class="$style.caption">{{ messages.followerHostsFailed }}</div>
			<div v-else-if="followers.hosts.length === 0" :class="$style.caption">{{ messages.followerHostsEmpty }}</div>
			<template v-else>
				<MkSwitch v-for="f in followers.hosts" :key="f.host" :modelValue="option.hosts.includes(f.host)" data-testid="delivery-targets-follower-host" @update:modelValue="v => toggle(f.host, v)">
					{{ f.host }}
					<template #caption>{{ messages.followerCount(f.count) }}</template>
				</MkSwitch>
				<div v-if="followers.truncated" :class="$style.caption">{{ messages.followerHostsTruncated }}</div>
			</template>
		</div>
	</MkFolder>
</div>
</template>

<script lang="ts" setup>
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import { MkButton, MkFolder, MkInput, MkLoading, MkSwitch } from '@/plugin-api.js';
import { messages } from './messages.js';
import { addHosts, removeHost, setHost, toOption } from './option.js';
import type { DeliveryTargetsOption } from './option.js';
import { followerHosts, suggestHosts } from './host.js';
import type { FollowerHosts } from './host.js';

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

function toggle(host: string, selected: boolean) {
	update(setHost(option.value, host, selected));
}

// フォロワーのいるサーバーの一覧。undefined は読み込み中、null は読めなかった。
const followers = ref<FollowerHosts | null | undefined>(undefined);
const followersController = new AbortController();
followerHosts(followersController.signal).then(v => {
	followers.value = v;
});

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
	followersController.abort();
});
</script>

<style lang="scss" module>
.caption {
	font-size: 0.85em;
	opacity: 0.7;
}
</style>
