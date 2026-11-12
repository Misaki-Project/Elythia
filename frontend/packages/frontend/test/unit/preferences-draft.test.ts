/*
 * SPDX-FileCopyrightText: Elythia-Network
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import { nextTick } from 'vue';
import { describe, expect, test } from 'vitest';
import { PreferencesManager } from '@/preferences/manager.js';
import type { StorageProvider } from '@/preferences/manager.js';

describe('X timeline preview preference', () => {
	test('persists and reloads a non-default server-dependent mode', async () => {
		const savedTimelineValues: string[] = [];
		let storedProfile: string | null = null;
		const storage: StorageProvider = {
			load: () => storedProfile == null ? null : JSON.parse(storedProfile),
			save: ({ profile }) => {
				storedProfile = JSON.stringify(profile);
				savedTimelineValues.push(profile.preferences.xTimelinePreviewMode[0][1]);
			},
			cloudGetBulk: async () => ({}),
			cloudGet: async () => null,
			cloudSet: async () => undefined,
		};
		const manager = new PreferencesManager(storage, null);
		await manager.cloudReady;

		const timelineMode = manager.model('xTimelinePreviewMode');
		for (const mode of ['ogp', 'text'] as const) {
			timelineMode.value = mode;
			await nextTick();
		}

		expect(savedTimelineValues.slice(-2)).toEqual(['ogp', 'text']);
		expect(manager.s.xTimelinePreviewMode).toBe('text');

		const reloadedManager = new PreferencesManager(storage, null);
		await reloadedManager.cloudReady;
		expect(reloadedManager.s.xTimelinePreviewMode).toBe('text');
	});
});
