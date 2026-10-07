<!--
SPDX-FileCopyrightText: syuilo and misskey-project
SPDX-License-Identifier: AGPL-3.0-only
-->

<template>
<PageWithHeader v-model:tab="tab" :actions="headerActions" :tabs="headerTabs" :swipable="true">
	<div v-if="tab === 'featured'">
		<XFeatured/>
	</div>
	<div v-else-if="tab === 'users'">
		<XUsers/>
	</div>
	<div v-else-if="tab === 'roles'">
		<XRoles/>
	</div>
	<component :is="pluginPage.component" v-else-if="pluginPage" :embedded="true"/>
</PageWithHeader>
</template>

<script lang="ts" setup>
import { computed, ref } from 'vue';
import XFeatured from './explore.featured.vue';
import XUsers from './explore.users.vue';
import XRoles from './explore.roles.vue';
import { definePage } from '@/page.js';
import { i18n } from '@/i18n.js';
import { collectExplorePages } from '@/plugin-api.js';
import { serverPlugins } from '@/server-plugins.generated.js';

const props = withDefaults(defineProps<{
	initialTab?: string;
}>(), {
	initialTab: 'featured',
});

const tab = ref(props.initialTab);
const pluginPages = collectExplorePages(serverPlugins);
const pluginPage = computed(() => pluginPages.find(page => tab.value === page.fullPath));

const headerActions = computed(() => []);

const headerTabs = computed(() => [{
	key: 'featured',
	icon: 'ti ti-bolt',
	title: i18n.ts.featured,
}, {
	key: 'users',
	icon: 'ti ti-users',
	title: i18n.ts.users,
}, {
	key: 'roles',
	icon: 'ti ti-badges',
	title: i18n.ts.roles,
}, ...pluginPages.map(page => ({
	key: page.fullPath,
	icon: page.navIcon ?? 'ti ti-puzzle',
	title: page.navTitle!,
}))]);

definePage(() => ({
	title: i18n.ts.explore,
	icon: 'ti ti-hash',
}));
</script>
