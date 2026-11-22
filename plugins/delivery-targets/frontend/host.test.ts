/*
 * SPDX-FileCopyrightText: Elythia-Network
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import { describe, expect, test } from 'vitest';
import { followerHosts, initApi, isAvailable, suggestHosts } from './host.js';

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

describe('delivery-targets suggestHosts', () => {
	test('asks only for servers that respond and are neither suspended nor blocked', async () => {
		const seen: Record<string, unknown>[] = [];
		initApi((async (endpoint: string, params?: Record<string, unknown>) => {
			expect(endpoint).toBe('federation/instances');
			seen.push(params ?? {});
			return [{ host: 'a.example' }];
		}) as Api);
		expect(await suggestHosts(' a ')).toEqual(['a.example']);
		expect(seen[0]).toMatchObject({ host: 'a', notResponding: false, suspended: false, blocked: false });
	});

	test('returns nothing for an empty query or a failed call', async () => {
		initApi((async () => {
			throw new Error('boom');
		}) as Api);
		expect(await suggestHosts('  ')).toEqual([]);
		expect(await suggestHosts('a')).toEqual([]);
	});
});

describe('delivery-targets followerHosts', () => {
	test('reads the list from the backend and drops malformed rows', async () => {
		const calls: string[] = [];
		initApi((async (endpoint: string) => {
			calls.push(endpoint);
			return { hosts: [{ host: 'a.example', count: 2 }, { host: 1, count: 1 }, null], truncated: true };
		}) as Api);
		expect(await followerHosts()).toEqual({ hosts: [{ host: 'a.example', count: 2 }], truncated: true });
		expect(calls).toEqual(['plugin/delivery-targets/follower-hosts']);
	});

	test('null when the backend does not answer with a list', async () => {
		initApi((async () => ({ hosts: 'x' })) as Api);
		expect(await followerHosts()).toBeNull();
		initApi((async () => null) as Api);
		expect(await followerHosts()).toBeNull();
		initApi((async () => {
			throw new Error('404');
		}) as Api);
		expect(await followerHosts()).toBeNull();
	});

	test('truncated is false unless the backend says so', async () => {
		initApi((async () => ({ hosts: [] })) as Api);
		expect(await followerHosts()).toEqual({ hosts: [], truncated: false });
	});
});
