/*
 * SPDX-FileCopyrightText: Elythia-Network
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import { describe, expect, test } from 'vitest';
import { addHosts, emptyOption, isSet, MAX_HOSTS, normalizeHost, removeHost, setHost, toOption } from './option.js';

describe('delivery-targets option', () => {
	test('isSet sends only lists with servers, in either mode', () => {
		expect(isSet(null)).toBe(false);
		expect(isSet(undefined)).toBe(false);
		expect(isSet('x')).toBe(false);
		expect(isSet({ mode: 'include', hosts: [] })).toBe(false);
		expect(isSet({ mode: 'include', hosts: ['a.example'] })).toBe(true);
		expect(isSet({ mode: 'exclude', hosts: [] })).toBe(false);
		expect(isSet({ mode: 'exclude', hosts: ['a.example'] })).toBe(true);
	});

	test('toOption falls back to an empty include list', () => {
		expect(toOption(null)).toEqual(emptyOption());
		expect(toOption({ mode: 'bogus', hosts: ['a.example', 1] })).toEqual({ mode: 'include', hosts: ['a.example'] });
		expect(toOption({ mode: 'exclude', hosts: 'a.example' })).toEqual({ mode: 'exclude', hosts: [] });
	});

	test('normalizeHost takes the host of a URL and drops decorations', () => {
		expect(normalizeHost(' Remote.Example. ')).toBe('remote.example');
		expect(normalizeHost('@remote.example')).toBe('remote.example');
		expect(normalizeHost('https://remote.example:8443/@user')).toBe('remote.example:8443');
		// 既定のポートは落とす (バックエンドの NormalizeHost と同じ形にする)。
		expect(normalizeHost('http://remote.example:80/@user')).toBe('remote.example');
		expect(normalizeHost('https://remote.example:443/@user')).toBe('remote.example');
		expect(normalizeHost('http://remote.example:443/')).toBe('remote.example:443');
		expect(normalizeHost('')).toBeNull();
		expect(normalizeHost('a b')).toBeNull();
		expect(normalizeHost('a/b')).toBeNull();
		expect(normalizeHost('http://[bad')).toBeNull();
	});

	test('addHosts splits, normalizes, deduplicates and caps the list', () => {
		const added = addHosts(emptyOption(), 'a.example, B.example\nhttps://a.example/x、c.example');
		expect(added.hosts).toEqual(['a.example', 'b.example', 'c.example']);
		const many = Array.from({ length: MAX_HOSTS + 5 }, (_, i) => `h${i}.example`).join(' ');
		expect(addHosts(emptyOption(), many).hosts).toHaveLength(MAX_HOSTS);
		expect(removeHost(added, 'b.example').hosts).toEqual(['a.example', 'c.example']);
	});

	test('setHost toggles one host in the same form as typing it', () => {
		const on = setHost(emptyOption(), 'A.Example', true);
		expect(on.hosts).toEqual(addHosts(emptyOption(), 'A.Example').hosts);
		expect(setHost(on, 'a.example', true).hosts).toEqual(['a.example']);
		expect(setHost(on, 'A.Example', false).hosts).toEqual([]);
		expect(setHost(on, 'a b', false)).toBe(on);
	});
});
