/*
 * SPDX-FileCopyrightText: Elythia-Network
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import { describe, expect, test } from 'vitest';
import { isAvailable } from './host.js';

type Api = Parameters<typeof isAvailable>[0];

describe('delivery-targets isAvailable', () => {
	test('true only when the backend route answers available: true', async () => {
		const calls: string[] = [];
		const api = (async (endpoint: string) => {
			calls.push(endpoint);
			return { available: true };
		}) as Api;
		expect(await isAvailable(api)).toBe(true);
		expect(calls).toEqual(['plugin/delivery-targets/available']);
	});

	test('false when the route is missing (backend disabled)', async () => {
		const api = (async () => {
			throw new Error('404');
		}) as Api;
		expect(await isAvailable(api)).toBe(false);
	});

	test('false when the answer is not available: true', async () => {
		const api = (async () => ({ available: 'yes' })) as Api;
		expect(await isAvailable(api)).toBe(false);
	});

	test('false when the backend does not answer in time', async () => {
		const api = ((_e: string, _p?: Record<string, unknown>, options?: { signal?: AbortSignal }) => new Promise((_resolve, reject) => {
			options?.signal?.addEventListener('abort', () => reject(new Error('aborted')));
		})) as Api;
		expect(await isAvailable(api, 20)).toBe(false);
	});
});
