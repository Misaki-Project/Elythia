/*
 * SPDX-FileCopyrightText: Elythia-Network
 * SPDX-License-Identifier: AGPL-3.0-only
 */

// ロールポリシーの表: Fx preview と Genshin / HSR の native role policy。
// 各行は管理画面で編集し、API 保存値と再読み込み後の値まで確認する。

import { test } from '@playwright/test';
import { coversPolicies, type PolicyRow, policyTitle, runPolicyRow } from '../../../../fixtures/admin_table';
import { coversRoutes } from '../../../../fixtures/ui_route';

const FX_EFFECT_UNAVAILABLE = 'Fx preview plugin は標準 Playwright build で意図的に disabled のため、runtime endpoint が無い。この spec は role の編集・保存・再表示を確認し、capability effect は plugin を含む専用 build で確認する';
const GAME_EFFECT_UNAVAILABLE = 'この 2.2 source には Genshin / HSR の runtime policy consumer endpoint が無いため、policy を実際に消費する経路での効果を確認できない。編集・保存・再表示は確認する';

const ROWS: PolicyRow[] = [
  { key: 'canUseFxTwitterPreview', control: { kind: 'switch', on: false }, value: false, effect: { skip: FX_EFFECT_UNAVAILABLE } },
  { key: 'fxTwitterColdFetchesPerHour', control: { kind: 'number', value: 1 }, value: 1, effect: { skip: FX_EFFECT_UNAVAILABLE } },
  { key: 'fxTwitterColdFetchesPerDay', control: { kind: 'number', value: 1 }, value: 1, effect: { skip: FX_EFFECT_UNAVAILABLE } },
  { key: 'genshinRefreshIntervalMinutes', control: { kind: 'number', value: 15 }, value: 15, effect: { skip: GAME_EFFECT_UNAVAILABLE } },
  { key: 'genshinUidLimit', control: { kind: 'number', value: 2 }, value: 2, effect: { skip: GAME_EFFECT_UNAVAILABLE } },
  { key: 'hsrRefreshIntervalMinutes', control: { kind: 'number', value: 15 }, value: 15, effect: { skip: GAME_EFFECT_UNAVAILABLE } },
  { key: 'hsrUidLimit', control: { kind: 'number', value: 2 }, value: 2, effect: { skip: GAME_EFFECT_UNAVAILABLE } },
];

test.describe('admin: role policies (Fx preview, Genshin, HSR)', () => {
  test.setTimeout(60_000);
  for (const row of ROWS) {
    test(policyTitle(row), { annotation: [...coversRoutes('/admin/roles/:id/edit').annotation, ...coversPolicies(row.key).annotation] }, ({ page, request, baseURL }) => runPolicyRow({ page, request, baseURL }, row));
  }
});
