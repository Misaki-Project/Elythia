<!--
SPDX-FileCopyrightText: Misaki Project
SPDX-License-Identifier: AGPL-3.0-only
-->

<template>
<MkFolder v-if="config" :defaultOpen="ranges.length > 0">
	<template #icon><i class="ti ti-trending-up"></i></template>
	<template #label>{{ i18n.ts._roleLevel.policyRanges }}</template>
	<template #suffix>{{ ranges.length }}</template>
	<div class="_gaps_s">
		<MkInfo>{{ i18n.ts._roleLevel.perPolicyRangesDescription }}</MkInfo>
		<div v-for="(range, index) in ranges" :key="rangeIdentity(range)" class="_panel _gaps_s" :class="$style.range">
			<div :class="$style.header">
				<b>{{ i18n.tsx._roleLevel.range({ n: index + 1 }) }}</b>
				<MkButton v-if="!readonly" danger iconOnly :aria-label="i18n.ts.delete" @click="removeRange(index)"><i class="ti ti-trash"></i></MkButton>
			</div>
			<div :class="$style.rangeHeader">
				<MkInput
					:modelValue="stageToLevel(range.start)"
					type="number"
					:min="stageToLevel(1)"
					:max="stageToLevel(range.end - 1)"
					:readonly="readonly || index === 0"
					@update:modelValue="value => updateStart(index, levelToStage(Number(value)))"
				>
					<template #label>{{ i18n.ts._roleLevel.startPolicyLevel }}</template>
				</MkInput>
				<MkInput
					:modelValue="stageToLevel(range.end - 1)"
					type="number"
					:min="stageToLevel(range.start)"
					:max="stageToLevel(maxStage)"
					:readonly="readonly || index === ranges.length - 1"
					@update:modelValue="value => updateEnd(index, levelToStage(Number(value)))"
				>
					<template #label>{{ i18n.ts._roleLevel.endPolicyLevel }}</template>
				</MkInput>
				<MkSelect :modelValue="range.type" :items="rangeTypes" :readonly="readonly" @update:modelValue="value => updateType(range, value)">
					<template #label>{{ i18n.ts._roleLevel.rangeType }}</template>
				</MkSelect>
			</div>

			<MkInfo v-if="range.type === 'base'">{{ i18n.ts._roleLevel.noPolicyChangeDescription }}</MkInfo>
			<template v-else-if="range.type === 'const'">
				<MkSwitch v-if="policyKind === 'boolean'" :modelValue="Boolean(range.value)" :disabled="readonly" @update:modelValue="value => range.value = value">
					<template #label>{{ i18n.ts._roleLevel.constantValue }}</template>
				</MkSwitch>
				<MkInput v-else-if="policyKind === 'number'" :modelValue="Number(range.value)" type="number" :min="isGenshinRefresh ? 1 : undefined" :max="isGenshinRefresh ? 1440 : undefined" :step="isGenshinRefresh ? 1 : undefined" :readonly="readonly" @update:modelValue="value => updateConstant(range, value)">
					<template #label>{{ i18n.ts._roleLevel.constantValue }}</template>
				</MkInput>
				<MkSelect v-else-if="policyKind === 'enum'" :modelValue="String(range.value)" :items="enumItems" :readonly="readonly" @update:modelValue="value => range.value = value">
					<template #label>{{ i18n.ts._roleLevel.constantValue }}</template>
				</MkSelect>
				<div v-else-if="policyKey === 'optOutNotificationTypes'" class="_gaps_s">
					<MkSwitch
						v-for="type in optOutNotificationTypes"
						:key="type"
						:modelValue="stringSetIncludes(range, type)"
						:disabled="readonly"
						@update:modelValue="value => toggleStringSetValue(range, type, value)"
					>
						{{ notificationTypeLabel(type) }}
					</MkSwitch>
				</div>
				<MkTextarea v-else :modelValue="stringSetValue(range)" :readonly="readonly" @update:modelValue="value => updateStringSet(range, value)">
					<template #label>{{ i18n.ts._roleLevel.constantValue }}</template>
				</MkTextarea>
			</template>
			<template v-else-if="range.type === 'multiplier'">
				<div :class="$style.valueColumns">
					<MkInput :modelValue="range.base" type="number" :readonly="readonly" @update:modelValue="value => updateMultiplier(range, 'base', value)"><template #label>{{ i18n.ts._roleLevel.multiplierBase }}</template></MkInput>
					<MkInput :modelValue="range.additional" type="number" :readonly="readonly" @update:modelValue="value => updateMultiplier(range, 'additional', value)"><template #label>{{ i18n.ts._roleLevel.multiplierAdditional }}</template></MkInput>
				</div>
				<MkFolder defaultOpen>
					<template #icon><i class="ti ti-calculator"></i></template>
					<template #label>{{ i18n.ts._roleLevel.impactInformation }}</template>
					<div class="_gaps_s">
						<div><b>{{ i18n.ts._roleLevel.formula }}</b> {{ policyFormula(range) }}</div>
						<div :class="$style.simulation">
							<template v-for="(row, rowIndex) in policyPreview(range)" :key="row.level">
								<div v-if="rowIndex === 3 && range.end - range.start > 6" :class="$style.ellipsis">…</div>
								<div>{{ i18n.tsx._roleLevel.policySimulation({ level: row.level, value: row.value }) }}</div>
							</template>
						</div>
					</div>
				</MkFolder>
			</template>
		</div>
		<MkInfo v-if="isGenshinRefresh">{{ i18n.ts._mkgoRolePolicy.genshinRefreshRange_caption }}</MkInfo>
		<MkButton v-if="!readonly" :disabled="!canAddRange" @click="addRange"><i class="ti ti-plus"></i> {{ i18n.ts._roleLevel.addPolicyRange }}</MkButton>
	</div>
</MkFolder>
</template>

<script setup lang="ts">
import { computed, watch } from 'vue';
import MkButton from '@/components/MkButton.vue';
import MkFolder from '@/components/MkFolder.vue';
import MkInfo from '@/components/MkInfo.vue';
import MkInput from '@/components/MkInput.vue';
import MkSelect from '@/components/MkSelect.vue';
import MkSwitch from '@/components/MkSwitch.vue';
import MkTextarea from '@/components/MkTextarea.vue';
import { i18n } from '@/i18n.js';
import { instance } from '@/instance.js';
import { isGenshinRefreshInterval, validGenshinRefreshMultiplier, normalizeGenshinRefreshMultiplier } from '@/utility/genshin-refresh-policy.js';
import { moveRangeEnd, moveRangeStart, previewOffsets } from './role-level-editor-utils.js';
import type { EditablePolicyRange } from './role-level-editor-utils.js';
import type { RoleLevelConfig, RoleLevelRangeType } from '@/utility/role-level-api.js';

const props = defineProps<{
	config?: RoleLevelConfig;
	policyKey: string;
	readonly?: boolean;
}>();

const numericPolicyKeys = new Set([
	'antennaLimit', 'avatarDecorationLimit', 'chunkedUploadMaxConcurrentSessions',
	'chunkedUploadMaxPendingMb', 'clipLimit', 'driveCapacityMb', 'emojiApplicationMaxPending',
	'emojiApplicationMaxPerDay', 'emojiApplicationMaxPerMonth', 'emojiApplicationMaxPerWeek',
	'genshinRefreshIntervalMinutes',
	'inviteExpirationTime', 'inviteLimit', 'inviteLimitCycle', 'maxFileSizeMb', 'mentionLimit',
	'noteDraftLimit', 'noteEachClipsLimit', 'pinLimit', 'rateLimitFactor', 'scheduledNoteLimit',
	'userEachUserListsLimit', 'userListLimit', 'webhookLimit', 'wordMuteLimit',
]);
const stringSetPolicyKeys = new Set(['optOutNotificationTypes', 'uploadableFileTypes']);
const optOutNotificationTypes = ['abuseReport', 'emojiApplicationReceived', 'signupApplicationReceived'] as const;

const maxStage = computed(() => 1 + (props.config?.experienceCurve.reduce((sum, curve) => sum + Math.max(0, Math.trunc(Number(curve.levelUps))), 0) ?? 0));
const ranges = computed(() => (props.config?.policyRanges ?? [])
	.filter((range): range is EditablePolicyRange => range.key === props.policyKey)
	.sort((a, b) => a.start - b.start));
const policyKind = computed<'boolean' | 'number' | 'enum' | 'stringSet'>(() => {
	if (numericPolicyKeys.has(props.policyKey)) return 'number';
	if (stringSetPolicyKeys.has(props.policyKey)) return 'stringSet';
	if (props.policyKey === 'chatAvailability') return 'enum';
	return 'boolean';
});
const isGenshinRefresh = computed(() => props.policyKey === 'genshinRefreshIntervalMinutes');
const rangeTypes = computed(() => [
	{ label: i18n.ts._roleLevel.noPolicyChange, value: 'base' },
	{ label: i18n.ts._roleLevel.constant, value: 'const' },
	...(policyKind.value === 'number' ? [{ label: i18n.ts._roleLevel.multiplier, value: 'multiplier' }] : []),
]);
const enumItems = computed(() => [
	{ label: i18n.ts.enabled, value: 'available' },
	{ label: i18n.ts.readonly, value: 'readonly' },
	{ label: i18n.ts.disabled, value: 'unavailable' },
]);
const canAddRange = computed(() => ranges.value.length === 0 || ranges.value.some(range => range.end - range.start >= 2));
const rangeIds = new WeakMap<EditablePolicyRange, number>();
let nextRangeId = 0;

ensureTiling();
watch(maxStage, ensureTiling);

function defaultValue(): unknown {
	return (instance.policies as unknown as Record<string, unknown>)[props.policyKey] ?? (isGenshinRefresh.value ? 10 : null);
}

function updateConstant(range: EditablePolicyRange, value: unknown): void {
	if (isGenshinRefresh.value && !isGenshinRefreshInterval(value)) return;
	range.value = Number(value);
}

function updateMultiplier(range: EditablePolicyRange, key: 'base' | 'additional', value: number): void {
	const base = key === 'base' ? value : range.base;
	const additional = key === 'additional' ? value : range.additional;
	if (isGenshinRefresh.value && !validGenshinRefreshMultiplier(base, additional, range.start, range.end)) return;
	range[key] = value;
}

function makeRange(type: RoleLevelRangeType, start: number, end: number): EditablePolicyRange {
	const value = defaultValue();
	return {
		type,
		key: props.policyKey,
		start,
		end,
		value,
		base: typeof value === 'number' ? value : 0,
		additional: 0,
	};
}

function ensureTiling(): void {
	if (!props.config || ranges.value.length === 0) return;
	const finalEnd = maxStage.value + 1;
	const normalized: EditablePolicyRange[] = [];
	let cursor = 1;
	for (const range of ranges.value) {
		const start = Math.max(1, Math.min(finalEnd - 1, Math.trunc(Number(range.start))));
		const end = Math.max(start + 1, Math.min(finalEnd, Math.trunc(Number(range.end))));
		if (start > cursor) normalized.push(makeRange('base', cursor, start));
		const actualStart = Math.max(cursor, start);
		if (end > actualStart) {
			range.start = actualStart;
			range.end = end;
			range.base ??= 0;
			range.additional ??= 0;
			normalized.push(range);
			cursor = end;
		}
		if (cursor >= finalEnd) break;
	}
	if (cursor < finalEnd) normalized.push(makeRange('base', cursor, finalEnd));
	const others = props.config.policyRanges.filter(range => range.key !== props.policyKey);
	props.config.policyRanges.splice(0, props.config.policyRanges.length, ...others, ...normalized);
	normalizeRefreshRanges();
}

function normalizeRefreshRanges(): void {
	if (!isGenshinRefresh.value) return;
	for (const range of ranges.value) {
		if (range.type === 'multiplier') normalizeGenshinRefreshMultiplier(range);
	}
}

function rangeIdentity(range: EditablePolicyRange): number {
	const current = rangeIds.get(range);
	if (current != null) return current;
	const id = nextRangeId++;
	rangeIds.set(range, id);
	return id;
}

function addRange(): void {
	if (!props.config) return;
	if (ranges.value.length === 0) {
		props.config.policyRanges.push(makeRange('const', 1, maxStage.value + 1));
		return;
	}
	const index = ranges.value.findLastIndex(range => range.end - range.start >= 2);
	if (index < 0) return;
	const range = ranges.value[index];
	const oldEnd = range.end;
	range.end = oldEnd - 1;
	props.config.policyRanges.push(makeRange('base', oldEnd - 1, oldEnd));
}

function removeRange(index: number): void {
	if (!props.config) return;
	const range = ranges.value[index];
	if (!range) return;
	if (ranges.value.length > 1) {
		if (index > 0) ranges.value[index - 1].end = range.end;
		else ranges.value[1].start = range.start;
	}
	const configIndex = props.config.policyRanges.indexOf(range);
	if (configIndex >= 0) props.config.policyRanges.splice(configIndex, 1);
	normalizeRefreshRanges();
}

function updateStart(index: number, value: number): void {
	moveRangeStart(ranges.value, index, value);
	normalizeRefreshRanges();
}

function updateEnd(index: number, value: number): void {
	moveRangeEnd(ranges.value, index, value);
	normalizeRefreshRanges();
}

function stageToLevel(stage: number): number {
	return Number(props.config?.baseLevel ?? 1) + stage - 1;
}

function levelToStage(level: number): number {
	return level - Number(props.config?.baseLevel ?? 1) + 1;
}

function updateType(range: EditablePolicyRange, type: unknown): void {
	range.type = type as RoleLevelRangeType;
	if (range.type === 'const' && range.value == null) range.value = defaultValue();
}

function stringSetValue(range: EditablePolicyRange): string {
	return Array.isArray(range.value) ? range.value.join('\n') : '';
}

function updateStringSet(range: EditablePolicyRange, value: string): void {
	range.value = value.split('\n').filter(line => line.trim() !== '');
}

function stringSetIncludes(range: EditablePolicyRange, value: string): boolean {
	return Array.isArray(range.value) && range.value.includes(value);
}

function toggleStringSetValue(range: EditablePolicyRange, value: string, enabled: boolean): void {
	const current = Array.isArray(range.value) ? range.value.filter((item): item is string => typeof item === 'string') : [];
	range.value = enabled
		? (current.includes(value) ? current : [...current, value])
		: current.filter(item => item !== value);
}

function notificationTypeLabel(type: typeof optOutNotificationTypes[number]): string {
	if (type === 'abuseReport') return i18n.ts._mkgoNotification.abuseReport;
	if (type === 'emojiApplicationReceived') return i18n.ts._mkgoNotification.emojiApplicationReceivedLabel;
	return i18n.ts._mkgoNotification.signupApplicationReceived;
}

function policyFormula(range: EditablePolicyRange): string {
	return `${range.base} + ${range.additional} × n`;
}

function policyPreview(range: EditablePolicyRange): Array<{ level: number; value: string }> {
	return previewOffsets(range.end - range.start).map(offset => ({
		level: Number(props.config?.baseLevel ?? 0) + range.start + offset - 1,
		value: formatNumber(Math.floor(Number(range.base) + Number(range.additional) * offset)),
	}));
}

function formatNumber(value: number): string {
	return Number.isFinite(value) ? value.toLocaleString() : String(value);
}
</script>

<style lang="scss" module>
.range {
	padding: 16px;
}

.header {
	display: flex;
	align-items: center;
	justify-content: space-between;
	gap: 12px;
}

.rangeHeader {
	display: grid;
	grid-template-columns: repeat(3, minmax(0, 1fr));
	gap: 12px;
}

.valueColumns {
	display: grid;
	grid-template-columns: repeat(2, minmax(0, 1fr));
	gap: 12px;
}

.simulation {
	display: grid;
	gap: 4px;
	font-variant-numeric: tabular-nums;
}

.ellipsis {
	opacity: 0.7;
}

@media (max-width: 500px) {
	.rangeHeader,
	.valueColumns {
		grid-template-columns: 1fr;
	}
}
</style>
