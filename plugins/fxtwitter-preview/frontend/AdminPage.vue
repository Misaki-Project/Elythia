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
		<form v-else :class="$style.form" @submit.prevent="saveSettings">
			<div :class="$style.limitGrid">
				<MkInput v-model="authenticatedPerHour" type="number" :min="1" :max="10000"><template #label>標準ユーザー・1時間</template></MkInput>
				<MkInput v-model="authenticatedPerDay" type="number" :min="1" :max="100000"><template #label>標準ユーザー・1日</template></MkInput>
				<MkInput v-model="globalPerHour" type="number" :min="1" :max="100000"><template #label>サーバー全体・1時間</template></MkInput>
			</div>
			<div :class="$style.rolesHeader">
				<div>
					<h4>ロール別上限</h4>
					<p :class="$style.muted">複数ロールがある場合は、1時間・1日ごとに最大値が適用されます。未指定のロールは標準値です。</p>
				</div>
				<MkButton type="button" :disabled="availableRoles.length === 0" @click="addRoleLimit">ロールを追加</MkButton>
			</div>
			<div v-for="(limit, index) in roleLimits" :key="limit.roleId" :class="$style.roleRow">
				<MkSelect v-model="limit.roleId" :items="selectableRoles(limit.roleId).map(role => ({ value: role.id, label: role.name }))"><template #label>ロール</template></MkSelect>
				<MkInput v-model="limit.perHour" type="number" :min="1" :max="10000"><template #label>1時間</template></MkInput>
				<MkInput v-model="limit.perDay" type="number" :min="1" :max="100000"><template #label>1日</template></MkInput>
				<MkButton type="button" danger @click="roleLimits.splice(index, 1)">削除</MkButton>
			</div>
			<p v-if="settingsError" :class="$style.error">{{ settingsError }}</p>
			<p v-if="settingsSaved" :class="$style.saved">保存しました。</p>
			<MkButton primary type="submit" :disabled="settingsSaving || !validSettings">レート制限を保存</MkButton>
		</form>
	</section>

	<section :class="$style.section">
		<h3>監査本文スナップショット（正規化済みテキスト）</h3>
		<p>本文形式は normalized_text_v1 です。URLの展開・文字参照の復号・メディアURLの除去を行ったテキストを保存します。再取得間隔は保存期限ではありません。監査の保持期間はサーバー設定に従い、既定の0は無期限です。公開キャッシュの代替表示には使用しません。</p>
		<p>公開プレビューとは分離された本文スナップショットを、投稿IDを指定して確認します。</p>
		<MkInput v-model="postId" pattern="[0-9]{2,20}" placeholder="X 投稿ID"><template #label>投稿ID</template></MkInput>
		<MkButton :disabled="!validPostId || loading" @click="load">スナップショットを表示</MkButton>
		<p v-if="error" :class="$style.error">{{ error }}</p>
		<MkLoading v-else-if="loading"/>
		<div v-else-if="items.length" :class="$style.timeline">
			<article v-for="item in items" :key="item.id" :class="$style.snapshot"><time>{{ item.observedAt }}</time><small>{{ item.bodyFormat === 'normalized_text_v1' ? '正規化済みテキスト (normalized_text_v1)' : '不明な本文形式' }}</small><pre v-if="item.bodyFormat === 'normalized_text_v1'">{{ item.body }}</pre><small>{{ item.contentHash }}</small></article>
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
import { api, nativeApi } from './api.js';
import type { AuditSnapshot, RateLimitSettings, RoleRateLimit } from './api.js';
import { MkButton, MkInput, MkLoading, MkSelect } from '@/plugin-api.js';

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
const authenticatedPerHour = ref(100);
const authenticatedPerDay = ref(600);
const globalPerHour = ref(300);
const roleLimits = ref<Array<{ roleId: string; perHour: number; perDay: number }>>([]);
const roles = ref<AdminRole[]>([]);

function integer(value: number, min: number, max: number): number | null {
	return Number.isSafeInteger(value) && value >= min && value <= max ? value : null;
}

const validSettings = computed(() => {
	if (integer(authenticatedPerHour.value, 1, 10000) == null || integer(authenticatedPerDay.value, 1, 100000) == null || integer(globalPerHour.value, 1, 100000) == null) return false;
	const ids = new Set<string>();
	for (const role of roleLimits.value) {
		if (!/^[A-Za-z0-9_-]{1,64}$/.test(role.roleId) || ids.has(role.roleId) || integer(role.perHour, 1, 10000) == null || integer(role.perDay, 1, 100000) == null) return false;
		ids.add(role.roleId);
	}
	return true;
});

const availableRoles = computed(() => roles.value.filter(role => !roleLimits.value.some(limit => limit.roleId === role.id)));

function selectableRoles(current: string): AdminRole[] {
	return roles.value.filter(role => role.id === current || !roleLimits.value.some(limit => limit.roleId === role.id));
}

function applySettings(value: RateLimitSettings): void {
	revision.value = value.revision;
	authenticatedPerHour.value = value.authenticatedPerHour;
	authenticatedPerDay.value = value.authenticatedPerDay;
	globalPerHour.value = value.globalPerHour;
	roleLimits.value = value.roleLimits.map(limit => ({ roleId: limit.roleId, perHour: limit.perHour, perDay: limit.perDay }));
}

function errorMessage(failure: unknown, fallback: string): string {
	return typeof failure === 'object' && failure !== null && 'message' in failure && typeof failure.message === 'string' ? failure.message : fallback;
}

async function loadSettings(): Promise<void> {
	settingsLoading.value = true;
	settingsError.value = '';
	try {
		const status = await api<{ canManageRateLimits: boolean }>('admin/status');
		if (!status.canManageRateLimits) {
			canManageSettings.value = false;
			return;
		}
		const [settings, adminRoles] = await Promise.all([api<RateLimitSettings>('admin/rate-limit-settings'), nativeApi<AdminRole[]>('admin/roles/list')]);
		applySettings(settings);
		roles.value = adminRoles.map(role => ({ id: role.id, name: role.name }));
		for (const limit of settings.roleLimits) if (!roles.value.some(role => role.id === limit.roleId)) roles.value.push({ id: limit.roleId, name: `${limit.roleId}（削除済みロール）` });
	} catch (err) { settingsError.value = errorMessage(err, '設定を取得できませんでした。'); } finally { settingsLoading.value = false; }
}

function addRoleLimit(): void {
	const role = availableRoles.value.at(0);
	if (role != null) roleLimits.value.push({ roleId: role.id, perHour: authenticatedPerHour.value, perDay: authenticatedPerDay.value });
}

async function saveSettings(): Promise<void> {
	if (!validSettings.value) return;
	settingsSaving.value = true; settingsError.value = ''; settingsSaved.value = false;
	try {
		const payload: Omit<RateLimitSettings, 'updatedAt' | 'updatedBy'> = {
			revision: revision.value,
			authenticatedPerHour: authenticatedPerHour.value, authenticatedPerDay: authenticatedPerDay.value, globalPerHour: globalPerHour.value,
			roleLimits: roleLimits.value.map<RoleRateLimit>(limit => ({ roleId: limit.roleId, perHour: limit.perHour, perDay: limit.perDay })),
		};
		applySettings(await api<RateLimitSettings>('admin/rate-limit-settings/update', { ...payload }));
		settingsSaved.value = true;
	} catch (err) { settingsError.value = errorMessage(err, '設定を保存できませんでした。再読み込みして確認してください。'); } finally { settingsSaving.value = false; }
}

async function load(): Promise<void> {
	loading.value = true; error.value = '';
	try { items.value = (await api<{ items: AuditSnapshot[] }>('admin/audit/list', { postId: postId.value })).items; loaded.value = true; } catch (err) { error.value = errorMessage(err, '取得できませんでした。'); } finally { loading.value = false; }
}

async function remove(): Promise<void> {
	if (!canDelete.value) return;
	loading.value = true; error.value = '';
	try { await api('admin/audit/delete', { target: target.value, postId: postId.value, reason: reason.value.trim(), confirm: true }); confirmation.value = ''; await load(); } catch (err) { error.value = errorMessage(err, '削除できませんでした。'); loading.value = false; }
}

onMounted(loadSettings);
</script>

<style lang="scss" module>
.page { max-width: 800px; margin: 0 auto; padding: 24px; display: grid; gap: 24px; }
.section { display: grid; gap: 16px; padding: 20px; background: var(--MI_THEME-panel); border-radius: var(--MI-radius); }
.section h3, .section h4, .section p { margin: 0; }
.form { display: grid; gap: 16px; }
.limitGrid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 12px; }
.rolesHeader { display: flex; gap: 16px; align-items: end; justify-content: space-between; }
.roleRow { display: grid; grid-template-columns: minmax(180px, 2fr) minmax(100px, 1fr) minmax(100px, 1fr) auto; gap: 10px; align-items: end; }
.timeline { display: grid; gap: 10px; border-left: 3px solid var(--MI_THEME-accent); padding-left: 16px; }
.snapshot { border-bottom: 1px solid var(--MI_THEME-divider); padding-bottom: 12px; }
.snapshot pre { white-space: pre-wrap; overflow-wrap: anywhere; font: inherit; }
.snapshot small, .snapshot time, .muted { color: var(--MI_THEME-fgTransparentWeak); }
.deleteBox { display: grid; gap: 12px; margin-top: 4px; padding-top: 18px; border-top: 1px solid var(--MI_THEME-divider); }
.deleteBox select { padding: 10px; color: var(--MI_THEME-fg); background: var(--MI_THEME-panel); border: 1px solid var(--MI_THEME-divider); border-radius: var(--MI-radius); }
.error { color: var(--MI_THEME-error); }
.saved { color: var(--MI_THEME-success); }
@media (max-width: 700px) { .limitGrid, .roleRow { grid-template-columns: 1fr; } .rolesHeader { align-items: stretch; flex-direction: column; } }
</style>
