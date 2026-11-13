<!--
SPDX-FileCopyrightText: syuilo and misskey-project
SPDX-License-Identifier: AGPL-3.0-only
-->

<template>
<div :class="$style.page">
	<h2>FxTwitter preview 管理</h2>

	<section :class="$style.section">
		<h3>外部取得レート制限</h3>
		<p>キャッシュにない投稿を Fx から取得する回数です。全体上限は、すべてのユーザーとサーバープロセスを合算した安全上限です。</p>
		<MkLoading v-if="settingsLoading"/>
		<p v-else-if="!canManageSettings" :class="$style.muted">この設定は管理者だけが変更できます。</p>
		<div v-else :class="$style.form">
			<p :class="$style.muted">全体上限だけをここで変更できます。旧標準・ロール別上限は読み取り専用で表示します。必要な利用者別上限はネイティブロールの FxTwitter ポリシーへ移行してください。</p>
			<div :class="$style.limitGrid">
				<MkInput v-model="globalPerHour" type="number" :min="1" :max="100000"><template #label>サーバー全体・1時間</template></MkInput>
				<div :class="$style.muted">旧標準ユーザー: {{ legacySettings?.authenticatedPerHour ?? '—' }}/時、{{ legacySettings?.authenticatedPerDay ?? '—' }}/日</div>
			</div>
			<div v-if="legacySettings?.roleLimits.length" :class="$style.legacyRoles">
				<h4>旧ロール別上限（読み取り専用）</h4>
				<ul><li v-for="limit in legacySettings.roleLimits" :key="limit.roleId">{{ roles.find(role => role.id === limit.roleId)?.name ?? limit.roleId }}: {{ limit.perHour }}/時、{{ limit.perDay }}/日</li></ul>
			</div>
			<p v-if="settingsError" :class="$style.error">{{ settingsError }}</p>
			<p v-if="settingsSaved" :class="$style.saved">保存しました。</p>
			<form @submit.prevent="saveSettings"><MkButton primary type="submit" :disabled="settingsSaving || !validSettings">全体上限を保存</MkButton></form>
		</div>
	</section>

	<section :class="$style.section">
		<h3>監査本文スナップショット</h3>
		<p>公開プレビューとは分離された本文スナップショットを、投稿IDを指定して確認します。</p>
		<MkInput v-model="postId" pattern="[0-9]{2,20}" placeholder="X 投稿ID"><template #label>投稿ID</template></MkInput>
		<MkButton :disabled="!validPostId || loading" @click="load">スナップショットを表示</MkButton>
		<p v-if="error" :class="$style.error">{{ error }}</p>
		<MkLoading v-else-if="loading"/>
		<div v-else-if="items.length" :class="$style.timeline">
			<article v-for="item in items" :key="item.id" :class="$style.snapshot"><time>{{ item.observedAt }}</time><pre>{{ item.body }}</pre><small>{{ item.contentHash }}</small></article>
		</div>
		<p v-else-if="loaded">スナップショットはありません。</p>
		<div :class="$style.deleteBox">
			<h4>個別削除</h4>
			<p>削除対象を選び、理由と確認欄に <code>DELETE</code> を入力してください。</p>
			<select v-model="target"><option value="public">公開キャッシュ</option><option value="audit">監査スナップショット</option><option value="both">両方</option></select>
			<MkInput v-model="reason"><template #label>理由</template></MkInput>
			<MkInput v-model="confirmation"><template #label>確認</template></MkInput>
			<MkButton danger :disabled="!canDelete" @click="remove">削除する</MkButton>
		</div>
	</section>
</div>
</template>

<script lang="ts" setup>
import { computed, onMounted, ref } from 'vue';
import { MkButton, MkInput, MkLoading } from '@/plugin-api.js';
import { api, nativeApi } from './api.js';
import type { AuditSnapshot, GlobalRateLimit, RateLimitSettings } from './api.js';

type AdminRole = { id: string; name: string };

const postId = ref('');
const target = ref<'public' | 'audit' | 'both'>('audit');
const reason = ref('');
const confirmation = ref('');
const items = ref<AuditSnapshot[]>([]);
const loading = ref(false);
const loaded = ref(false);
const error = ref('');
const validPostId = computed(() => /^[0-9]{2,20}$/.test(postId.value));
const canDelete = computed(() => validPostId.value && reason.value.trim() !== '' && confirmation.value === 'DELETE' && !loading.value);

const settingsLoading = ref(true);
const settingsSaving = ref(false);
const canManageSettings = ref(true);
const settingsError = ref('');
const settingsSaved = ref(false);
const revision = ref(0);
const globalPerHour = ref(300);
const legacySettings = ref<RateLimitSettings | null>(null);
const roles = ref<AdminRole[]>([]);

function integer(value: number, min: number, max: number): number | null {
	return Number.isSafeInteger(value) && value >= min && value <= max ? value : null;
}

const validSettings = computed(() => {
	return integer(globalPerHour.value, 1, 100000) != null;
});

async function loadSettings(): Promise<void> {
	settingsLoading.value = true;
	settingsError.value = '';
	try {
		const status = await api<{ canManageRateLimits: boolean }>('admin/status');
		if (!status.canManageRateLimits) {
			canManageSettings.value = false;
			return;
		}
		const [settings, legacy, adminRoles] = await Promise.all([api<GlobalRateLimit>('admin/global-rate-limit'), api<RateLimitSettings>('admin/rate-limit-settings'), nativeApi<AdminRole[]>('admin/roles/list')]);
		revision.value = settings.revision;
		globalPerHour.value = settings.globalPerHour;
		legacySettings.value = legacy;
		roles.value = adminRoles.map(role => ({ id: role.id, name: role.name }));
		for (const limit of legacy.roleLimits) if (!roles.value.some(role => role.id === limit.roleId)) roles.value.push({ id: limit.roleId, name: `${limit.roleId}（削除済みロール）` });
	} catch (err) { settingsError.value = (err as { message?: string })?.message ?? '設定を取得できませんでした。'; }
	finally { settingsLoading.value = false; }
}

async function saveSettings(): Promise<void> {
	if (!validSettings.value) return;
	settingsSaving.value = true; settingsError.value = ''; settingsSaved.value = false;
	try {
		const result = await api<GlobalRateLimit>('admin/global-rate-limit/update', { revision: revision.value, globalPerHour: globalPerHour.value });
		revision.value = result.revision;
		globalPerHour.value = result.globalPerHour;
		settingsSaved.value = true;
	} catch (err) { settingsError.value = (err as { message?: string })?.message ?? '設定を保存できませんでした。再読み込みして確認してください。'; }
	finally { settingsSaving.value = false; }
}

async function load(): Promise<void> {
	loading.value = true; error.value = '';
	try { items.value = (await api<{ items: AuditSnapshot[] }>('admin/audit/list', { postId: postId.value })).items; loaded.value = true; }
	catch (err) { error.value = (err as { message?: string })?.message ?? '取得できませんでした。'; }
	finally { loading.value = false; }
}

async function remove(): Promise<void> {
	if (!canDelete.value) return;
	loading.value = true; error.value = '';
	try { await api('admin/audit/delete', { target: target.value, postId: postId.value, reason: reason.value.trim(), confirm: true }); confirmation.value = ''; await load(); }
	catch (err) { error.value = (err as { message?: string })?.message ?? '削除できませんでした。'; loading.value = false; }
}

onMounted(loadSettings);
</script>

<style lang="scss" module>
.page { max-width: 800px; margin: 0 auto; padding: 24px; display: grid; gap: 24px; }
.section { display: grid; gap: 16px; padding: 20px; background: var(--MI_THEME-panel); border-radius: var(--MI-radius); }
.section h3, .section h4, .section p { margin: 0; }
.form { display: grid; gap: 16px; }
.limitGrid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 12px; }
.legacyRoles ul { margin: 8px 0 0; padding-left: 20px; }
.timeline { display: grid; gap: 10px; border-left: 3px solid var(--MI_THEME-accent); padding-left: 16px; }
.snapshot { border-bottom: 1px solid var(--MI_THEME-divider); padding-bottom: 12px; }
.snapshot pre { white-space: pre-wrap; overflow-wrap: anywhere; font: inherit; }
.snapshot small, .snapshot time, .muted { color: var(--MI_THEME-fgTransparentWeak); }
.deleteBox { display: grid; gap: 12px; margin-top: 4px; padding-top: 18px; border-top: 1px solid var(--MI_THEME-divider); }
.deleteBox select { padding: 10px; color: var(--MI_THEME-fg); background: var(--MI_THEME-panel); border: 1px solid var(--MI_THEME-divider); border-radius: var(--MI-radius); }
.error { color: var(--MI_THEME-error); }
.saved { color: var(--MI_THEME-success); }
@media (max-width: 700px) { .limitGrid { grid-template-columns: 1fr; } }
</style>
