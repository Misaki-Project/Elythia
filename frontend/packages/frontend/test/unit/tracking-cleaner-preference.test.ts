/*
 * SPDX-FileCopyrightText: Elythia-Network
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import { nextTick } from 'vue';
import { describe, expect, test } from 'vitest';
import { PreferencesManager } from '@/preferences/manager.js';
import type { StorageProvider } from '@/preferences/manager.js';

describe('tracking cleaner personal preference', () => {
	test('defaults on, persists off per account, and can be enabled again', async () => {
		let saved: string | null = null;
		const storage: StorageProvider = {
			load: () => saved == null ? null : JSON.parse(saved),
			save: ({ profile }) => { saved = JSON.stringify(profile); },
			cloudGetBulk: async () => ({}),
			cloudGet: async () => null,
			cloudSet: async () => undefined,
		};
		const first = new PreferencesManager(storage, { id: 'account-a' });
		await first.cloudReady;
		expect(first.s.trackingParameterCleanerEnabled).toBe(true);
		first.model('trackingParameterCleanerEnabled').value = false;
		await nextTick();
		expect(first.s.trackingParameterCleanerEnabled).toBe(false);

		const second = new PreferencesManager(storage, { id: 'account-b' });
		await second.cloudReady;
		expect(second.s.trackingParameterCleanerEnabled).toBe(true);

		const reloaded = new PreferencesManager(storage, { id: 'account-a' });
		await reloaded.cloudReady;
		expect(reloaded.s.trackingParameterCleanerEnabled).toBe(false);
		reloaded.model('trackingParameterCleanerEnabled').value = true;
		await nextTick();
		const enabledAgain = new PreferencesManager(storage, { id: 'account-a' });
		await enabledAgain.cloudReady;
		expect(enabledAgain.s.trackingParameterCleanerEnabled).toBe(true);
	});
});
