/*
 * SPDX-FileCopyrightText: Elythia-Network
 * SPDX-License-Identifier: AGPL-3.0-only
 */

// /game-rankings is an Elythia-only route; keep this check out of upstream UI specs.
// The built-in frontend must render a useful empty state when optional ranking plugins
// are not installed.

import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';
import { type RootFixture, uiSigninAsRoot } from '../../../fixtures/ui_auth';
import { coversRoutes, gotoRoute } from '../../../fixtures/ui_route';

test.describe('UI: /game-rankings', () => {
	let root: RootFixture;

	test.beforeAll(() => {
		root = JSON.parse(readFileSync('.auth/root.json', 'utf-8'));
	});

	test('shows an empty state without optional ranking plugins', coversRoutes('/game-rankings'), async ({ page, baseURL }) => {
		await uiSigninAsRoot(page, baseURL, root);
		await gotoRoute(page, baseURL!, '/game-rankings');

		const rankings = page.locator('[data-testid="game-rankings"]');
		await expect(rankings).toBeVisible();
		await expect(rankings.locator('p')).toHaveText(/\S/);
	});
});
