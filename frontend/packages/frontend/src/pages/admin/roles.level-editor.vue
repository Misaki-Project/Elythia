<!--
SPDX-FileCopyrightText: Misaki Project
SPDX-License-Identifier: AGPL-3.0-only
-->

<template>
<div class="_gaps_m">
	<MkInfo>{{ i18n.ts._roleLevel.editorDescription }}</MkInfo>

	<MkInput v-model="config.baseLevel" type="number" :min="0" :readonly="readonly">
		<template #label>{{ i18n.ts._roleLevel.baseLevel }}</template>
	</MkInput>

	<MkFolder defaultOpen>
		<template #label>{{ i18n.ts._roleLevel.experienceCurve }}</template>
		<div class="_gaps_m">
			<div v-for="(curve, index) in config.experienceCurve" :key="index" class="_panel _gaps_s" :class="$style.item">
				<div :class="$style.itemHeader">
					<b>{{ i18n.tsx._roleLevel.segment({ n: index + 1 }) }}</b>
					<MkButton v-if="!readonly && config.experienceCurve.length > 1" danger iconOnly :aria-label="i18n.ts.delete" @click="removeCurve(index)"><i class="ti ti-trash"></i></MkButton>
				</div>
				<div :class="$style.segmentHeader">
					<MkSelect v-model="curve.type" :items="curveTypes" :readonly="readonly">
						<template #label>{{ i18n.ts._roleLevel.calculation }}</template>
					</MkSelect>
					<MkInput v-model="curve.levelUps" type="number" :min="1" :readonly="readonly"><template #label>{{ i18n.ts._roleLevel.levelUps }}</template></MkInput>
				</div>
				<div :class="[$style.curveValues, $style[`curveValues_${curve.type}`]]">
					<MkInput v-model="curve.base" type="number" :min="0" :readonly="readonly"><template #label>{{ i18n.ts._roleLevel.baseExperience }}</template></MkInput>
					<MkInput v-if="curve.type !== 'const'" v-model="curve.additional" type="number" :readonly="readonly"><template #label>{{ i18n.ts._roleLevel.additionalExperience }}</template></MkInput>
					<MkInput v-if="curve.type === 'exponential'" v-model="curve.exponential" type="number" :min="0" :step="0.001" :readonly="readonly"><template #label>{{ i18n.ts._roleLevel.exponential }}</template></MkInput>
				</div>
				<MkFolder defaultOpen>
					<template #icon><i class="ti ti-calculator"></i></template>
					<template #label>{{ i18n.ts._roleLevel.impactInformation }}</template>
					<div class="_gaps_s">
						<div><b>{{ i18n.ts._roleLevel.formula }}</b> {{ curveFormula(curve) }}</div>
						<div :class="$style.simulation">
							<template v-for="(row, rowIndex) in curvePreview(index)" :key="row.level">
								<div v-if="rowIndex === 3 && curvePreviewLength(index) > 6" :class="$style.ellipsis">…</div>
								<div v-if="row.maximum">{{ i18n.tsx._roleLevel.experienceSimulationMax({ level: row.level, from: row.from }) }}</div>
								<div v-else>{{ i18n.tsx._roleLevel.experienceSimulation({ level: row.level, cost: row.cost, from: row.from, to: row.to }) }}</div>
							</template>
						</div>
					</div>
				</MkFolder>
			</div>
			<MkButton v-if="!readonly" @click="addCurve"><i class="ti ti-plus"></i> {{ i18n.ts._roleLevel.addSegment }}</MkButton>
		</div>
	</MkFolder>
</div>
</template>

<script lang="ts" setup>
import { computed } from 'vue';
import { curveCost, curveTotal, previewOffsets } from './role-level-editor-utils.js';
import MkButton from '@/components/MkButton.vue';
import MkFolder from '@/components/MkFolder.vue';
import MkInfo from '@/components/MkInfo.vue';
import MkInput from '@/components/MkInput.vue';
import MkSelect from '@/components/MkSelect.vue';
import { i18n } from '@/i18n.js';
import type { RoleLevelConfig, RoleLevelCurve } from '@/utility/role-level-api.js';

const props = defineProps<{ modelValue: RoleLevelConfig; readonly?: boolean }>();
const config = computed(() => props.modelValue);

const curveTypes = [
	{ label: i18n.ts._roleLevel.constant, value: 'const' },
	{ label: i18n.ts._roleLevel.linear, value: 'linear' },
	{ label: i18n.ts._roleLevel.exponential, value: 'exponential' },
];

function addCurve(): void {
	config.value.experienceCurve.push({ type: 'const', levelUps: 1, base: 100, additional: 0, exponential: 1 });
}

function removeCurve(index: number): void {
	config.value.experienceCurve.splice(index, 1);
}

function curveFormula(curve: RoleLevelCurve): string {
	if (curve.type === 'const') return `${curve.base}`;
	if (curve.type === 'linear') return `${curve.base} + ${curve.additional} × n`;
	return `${curve.base} + ${curve.additional} × ${curve.exponential}ⁿ`;
}

function curvePreviewLength(index: number): number {
	const curve = config.value.experienceCurve[index];
	return Math.max(0, Math.trunc(Number(curve.levelUps))) + (index === config.value.experienceCurve.length - 1 ? 1 : 0);
}

function curvePreview(index: number): Array<{ level: number; maximum: boolean; cost: string; from: string; to: string }> {
	const curve = config.value.experienceCurve[index];
	const levelUpsBefore = config.value.experienceCurve.slice(0, index).reduce((sum, item) => sum + Math.max(0, Math.trunc(Number(item.levelUps))), 0);
	const experienceBefore = config.value.experienceCurve.slice(0, index).reduce((sum, item) => sum + curveTotal(item), 0);
	const length = curvePreviewLength(index);
	return previewOffsets(length).map(offset => {
		const level = Number(config.value.baseLevel) + levelUpsBefore + offset;
		const maximum = index === config.value.experienceCurve.length - 1 && offset === Math.trunc(Number(curve.levelUps));
		const threshold = experienceBefore + segmentThreshold(curve, offset);
		if (maximum) {
			return { level, maximum, cost: '', from: formatNumber(Math.ceil(threshold)), to: '' };
		}
		const cost = curveCost(curve, offset);
		return {
			level,
			maximum,
			cost: formatNumber(cost),
			from: formatNumber(Math.ceil(threshold)),
			to: formatNumber(Math.ceil(threshold + cost) - 1),
		};
	});
}

function segmentThreshold(curve: RoleLevelCurve, count: number): number {
	return curveTotal({ ...curve, levelUps: count });
}

function formatNumber(value: number): string {
	if (!Number.isFinite(value)) return String(value);
	return Number.isInteger(value) ? value.toLocaleString() : value.toLocaleString(undefined, { maximumFractionDigits: 6 });
}
</script>

<style lang="scss" module>
.item {
	padding: 16px;
}

.itemHeader {
	display: flex;
	align-items: center;
	justify-content: space-between;
	gap: 12px;
}

.segmentHeader {
	display: grid;
	grid-template-columns: repeat(2, minmax(0, 1fr));
	gap: 12px;
}

.curveValues {
	display: grid;
	gap: 12px;
}

.curveValues_const {
	grid-template-columns: minmax(0, 1fr);
}

.curveValues_linear {
	grid-template-columns: repeat(2, minmax(0, 1fr));
}

.curveValues_exponential {
	grid-template-columns: repeat(3, minmax(0, 1fr));
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
	.segmentHeader,
	.curveValues {
		grid-template-columns: 1fr;
	}
}
</style>
