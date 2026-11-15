/*
 * SPDX-FileCopyrightText: Elythia-Network
 * SPDX-License-Identifier: AGPL-3.0-only
 */
import { expect, test } from 'vitest';
import { cleanTrackingParameters } from './cleaner.js';

const status = 'https://x.com/example/status/1234567890123456789';

test.each([
	[`${status}?a=3Qsc5gKJv9RfdM1cI20`, status],
	[`${status}?a=3Qsc5gKJv9RfdM1cI20&s=20`, status],
	[`${status}?s=20&a=3Qsc5gKJv9RfdM1cI20`, status],
	[`${status}?a=xxxx&lang=ja`, `${status}?lang=ja`],
	['https://twitter.com/example/status/1234567890123456789?a=xxxx', 'https://twitter.com/example/status/1234567890123456789'],
	['https://x.com/i/status/1234567890123456789?a=xxxx', 'https://x.com/i/status/1234567890123456789'],
])('removes the X share parameter a from status links: %s', (input, expected) => {
	expect(cleanTrackingParameters(`見て ${input} です`)).toBe(`見て ${expected} です`);
});

test.each([
	'https://x.com/search?q=elythia&a=xxxx',
	'https://x.com/example?a=xxxx',
	'https://example.com/example/status/1234567890123456789?a=xxxx',
	'https://youtube.com/watch?v=abc&a=xxxx',
])('keeps a outside X status links: %s', input => {
	expect(cleanTrackingParameters(input)).toBe(input);
});
