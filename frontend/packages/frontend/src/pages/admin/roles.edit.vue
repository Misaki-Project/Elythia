<!--
SPDX-FileCopyrightText: syuilo and misskey-project
SPDX-License-Identifier: AGPL-3.0-only
-->

<template>
<PageWithHeader :tabs="headerTabs">
	<div class="_spacer" style="--MI_SPACER-w: 600px; --MI_SPACER-min: 16px; --MI_SPACER-max: 32px;">
		<XEditor v-if="data" v-model="data"/>
	</div>
	<template #footer>
		<div :class="$style.footer">
			<div class="_spacer" style="--MI_SPACER-w: 600px; --MI_SPACER-min: 16px; --MI_SPACER-max: 16px;">
				<MkButton primary rounded @click="save"><i class="ti ti-check"></i> {{ i18n.ts.save }}</MkButton>
			</div>
		</div>
	</template>
</PageWithHeader>
</template>

<script lang="ts" setup>
import { computed, ref } from 'vue';
import * as Misskey from 'misskey-js';
import XEditor from './roles.editor.vue';
import { genId } from '@/utility/id.js';
import * as os from '@/os.js';
import { misskeyApi } from '@/utility/misskey-api.js';
import { i18n } from '@/i18n.js';
import { definePage } from '@/page.js';
import MkButton from '@/components/MkButton.vue';
import { rolesCache } from '@/cache.js';
import { useRouter } from '@/router.js';
import { defaultRoleLevelConfig, isLegacyLevelRole, roleLevelApi } from '@/utility/role-level-api.js';
import type { RoleLevelConfig } from '@/utility/role-level-api.js';

const router = useRouter();

const props = defineProps<{
	id?: string;
}>();

type RoleLike = Omit<Pick<Misskey.entities.Role, 'name' | 'description' | 'isAdministrator' | 'isModerator' | 'color' | 'iconUrl' | 'target' | 'isPublic' | 'isExplorable' | 'asBadge' | 'canEditMembersByModerator' | 'displayOrder' | 'preserveAssignmentOnMoveAccount'>, 'target'> & {
	target: Misskey.entities.Role['target'] | 'manualLevel';
	condFormula: any;
	policies: any;
	levelConfig: RoleLevelConfig;
};

const role = ref<Misskey.entities.Role | null>(null);
const data = ref<RoleLike | null>(null);
let hadLevelConfig = false;

if (props.id) {
	const loadedRole = await misskeyApi('admin/roles/show', {
		roleId: props.id,
	});
	role.value = loadedRole;

	const levelResponse = await roleLevelApi<{ role: RoleLevelConfig }>('plugin/role-level/admin/roles/show', {
		roleId: props.id,
	}).catch(() => null);
	hadLevelConfig = levelResponse != null;
	data.value = {
		...loadedRole,
		target: levelResponse != null || isLegacyLevelRole(loadedRole as { target: string }) ? 'manualLevel' : loadedRole.target,
		levelConfig: levelResponse?.role ?? defaultRoleLevelConfig(props.id),
	};
} else {
	data.value = {
		name: 'New Role',
		description: '',
		isAdministrator: false,
		isModerator: false,
		color: null,
		iconUrl: null,
		target: 'manual',
		condFormula: { id: genId(), type: 'isRemote' },
		isPublic: false,
		isExplorable: false,
		asBadge: false,
		canEditMembersByModerator: false,
		displayOrder: 0,
		preserveAssignmentOnMoveAccount: false,
		policies: {},
		levelConfig: defaultRoleLevelConfig(),
	};
}

function cleanLevelConfig(config: RoleLevelConfig, roleId: string) {
	return {
		roleId,
		baseLevel: Number(config.baseLevel),
		experienceCurve: config.experienceCurve.map(curve => ({
			type: curve.type,
			levelUps: Number(curve.levelUps),
			base: Number(curve.base),
			additional: Number(curve.additional ?? 0),
			...(curve.type === 'exponential' ? { exponential: Number(curve.exponential ?? 1) } : {}),
		})),
		policyRanges: config.policyRanges.map(range => ({
			type: range.type,
			start: Number(range.start),
			end: Number(range.end),
			...(range.key ? { key: range.key } : {}),
			...(range.type === 'const' ? { value: range.value } : {}),
			...(range.type === 'multiplier' ? { base: Number(range.base ?? 0), additional: Number(range.additional ?? 0) } : {}),
		})),
		revision: Number(config.revision),
	};
}

async function save() {
	if (data.value === null) return;
	rolesCache.delete();
	const isLevelRole = data.value.target === 'manualLevel';
	const { levelConfig, ...nativeData } = data.value;
	const nativeTarget: 'manual' | 'conditional' = isLevelRole
		? 'manual'
		: nativeData.target === 'conditional' ? 'conditional' : 'manual';
	const nativePayload = {
		...nativeData,
		target: nativeTarget,
	};
	if (role.value) {
		await os.apiWithDialog('admin/roles/update', {
			roleId: role.value.id,
			...nativePayload,
		});
		if (isLevelRole) {
			await roleLevelApi<{ role: RoleLevelConfig }>('plugin/role-level/admin/roles/update', cleanLevelConfig(levelConfig, role.value.id));
		} else if (hadLevelConfig) {
			await roleLevelApi('plugin/role-level/admin/roles/delete', { roleId: role.value.id, revision: levelConfig.revision });
		}
		router.push('/admin/roles/:id', {
			params: {
				id: role.value.id,
			},
		});
	} else {
		const created = await os.apiWithDialog('admin/roles/create', {
			...nativePayload,
		});
		if (isLevelRole) {
			await roleLevelApi<{ role: RoleLevelConfig }>('plugin/role-level/admin/roles/update', cleanLevelConfig(levelConfig, created.id));
		}
		router.push('/admin/roles/:id', {
			params: {
				id: created.id,
			},
		});
	}
}

const headerTabs = computed(() => []);

definePage(() => ({
	title: role.value ? `${i18n.ts._role.edit}: ${role.value.name}` : i18n.ts._role.new,
	icon: 'ti ti-badge',
}));
</script>

<style lang="scss" module>
.footer {
	-webkit-backdrop-filter: var(--MI-blur, blur(15px));
	backdrop-filter: var(--MI-blur, blur(15px));
}
</style>
