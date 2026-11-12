/*
 * SPDX-FileCopyrightText: syuilo and misskey-project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import { describe, expect, test, vi } from 'vitest';
import { cleanTrackingParameters } from '../../../../../plugins/tracking-parameter-cleaner/frontend/cleaner';
import trackingParameterCleaner from '../../../../../plugins/tracking-parameter-cleaner/frontend/index';
import type { PluginHost } from '@/plugin-api.js';

vi.mock('@/plugin-api.js', () => ({
	definePlugin: (plugin: unknown) => plugin,
}));

describe('tracking parameter cleaner', () => {
	test.each([
		[
			'X and Twitter tracking keys are removed without rewriting useful values',
			'https://x.com/alice/status/123?s=20&video_id=abc&t=4&ref_src=twsrc%5Etfw&start=8&ref_url=https%3A%2F%2Ft.co#reply',
			'https://x.com/alice/status/123?video_id=abc&start=8#reply',
		],
		[
			'YouTube sharing ID is removed while playback parameters remain',
			'https://www.youtube.com/watch?v=abc123&si=shareToken&list=PL123&index=2&t=90s',
			'https://www.youtube.com/watch?v=abc123&list=PL123&index=2&t=90s',
		],
		[
			'Instagram share identifiers and Facebook click ID are scoped to those sites',
			'https://www.instagram.com/p/abc/?igsh=one&igshid=two&img_index=3&utm_source=friend',
			'https://www.instagram.com/p/abc/?img_index=3',
		],
		[
			'Facebook removes only fbclid and approved UTM keys',
			'https://www.facebook.com/watch/?v=123&fbclid=click&ref=bookmark&utm_campaign=spring',
			'https://www.facebook.com/watch/?v=123&ref=bookmark',
		],
		[
			'Threads, TikTok, and Reddit use their small documented key sets',
			'https://threads.com/@a/post/1?igshid=x&xmt=A; https://www.tiktok.com/@a/video/1?_t=1&_r=2&lang=en; https://www.reddit.com/r/a/comments/1?share_id=x&sort=top',
			'https://threads.com/@a/post/1?xmt=A; https://www.tiktok.com/@a/video/1?lang=en; https://www.reddit.com/r/a/comments/1?sort=top',
		],
		[
			'UTM keys are removed only on supported social sites',
			'https://x.com/a?utm_source=s&utm_medium=m&utm_campaign=c&utm_term=t&utm_content=body&utm_id=42&feature=share',
			'https://x.com/a?feature=share',
		],
	])('%s', (_name, input, expected) => {
		expect(cleanTrackingParameters(input)).toBe(expected);
	});

	test('preserves unknown hosts, deceptive suffixes, unsupported schemes, and non-default ports', () => {
		const input = [
			'https://example.com/?utm_source=x&fbclid=y',
			'https://x.com.evil.test/a?s=1',
			'https://x.com:8443/a?s=1',
			'javascript:https://x.com/a?s=1',
		].join('\n');
		expect(cleanTrackingParameters(input)).toBe(input);
	});

	test('supports HTTP URLs and leaves signed URLs untouched', () => {
		expect(cleanTrackingParameters('http://x.com/alice/status/1?s=1')).toBe('http://x.com/alice/status/1');
		const signed = 'https://youtube.com/watch?v=abc&si=share&X-Amz-Signature=secret';
		expect(cleanTrackingParameters(signed)).toBe(signed);
		expect(cleanTrackingParameters('https://youtube.com/watch?si=share&hmac=secret')).toBe('https://youtube.com/watch?si=share&hmac=secret');
	});

	test('restricts short X/Twitter keys to shared status paths', () => {
		const input = 'https://x.com/search?s=query&t=sort&ref_src=search&utm_source=sidebar';
		expect(cleanTrackingParameters(input)).toBe('https://x.com/search?s=query&t=sort&ref_src=search');
	});

	test('leaves fragments and redirect or OAuth routes opaque', () => {
		const cases = [
			'https://youtu.be/abc#?si=fragment-value',
			'https://x.com/i/redirect?s=1&url=https%3A%2F%2Fexample.com',
			'https://x.com/%72edirect?s=1',
			'https://youtube.com/oauth?si=share&state=opaque',
			'https://threads.com/oauth?igshid=share&redirect_uri=https%3A%2F%2Fapp.example',
		];
		for (const value of cases) expect(cleanTrackingParameters(value)).toBe(value);
	});

	test('preserves the surrounding Japanese prose, punctuation, emoji, Markdown and line endings', () => {
		const input = '見てね🙂 https://x.com/alice/status/1?s=1、次もどうぞ。 (https://x.com/alice/status/3?s=5).\r\n[xの投稿](https://x.com/bob/status/2?t=2 "説明")';
		const expected = '見てね🙂 https://x.com/alice/status/1、次もどうぞ。 (https://x.com/alice/status/3).\r\n[xの投稿](https://x.com/bob/status/2 "説明")';
		expect(cleanTrackingParameters(input)).toBe(expected);
		expect(cleanTrackingParameters('$[tada ここ https://x.com/m/status/4?s=1 どうぞ]')).toBe('$[tada ここ https://x.com/m/status/4 どうぞ]');
	});

	test.each([
		['bold', '**https://x.com/a/status/123?s=20**', '**https://x.com/a/status/123**'],
		['italic', '*https://x.com/a/status/123?s=20*', '*https://x.com/a/status/123*'],
		['strikethrough', '~~https://youtu.be/abcdefghijk?si=tracking~~', '~~https://youtu.be/abcdefghijk~~'],
		['nested formatting', '***~~https://youtu.be/abcdefghijk?si=tracking~~***', '***~~https://youtu.be/abcdefghijk~~***'],
		['percent-encoded marker text', '**https://x.com/a/status/123?s=20&literal=%2A%2A**', '**https://x.com/a/status/123?literal=%2A%2A**'],
	])('preserves closing MFM delimiters for %s URLs', (_name, input, expected) => {
		expect(cleanTrackingParameters(input)).toBe(expected);
	});

	test('leaves unmatched or escaped MFM marker candidates unchanged', () => {
		const unmatched = '**https://x.com/a/status/123?s=20~~';
		const escaped = '\\**https://x.com/a/status/123?s=20**';
		expect(cleanTrackingParameters(unmatched)).toBe(unmatched);
		expect(cleanTrackingParameters(escaped)).toBe(escaped);
	});

	test('leaves URLs inside surrounding MFM formatting unchanged when the opener is ambiguous', () => {
		const bold = '**see https://x.com/a/status/123?s=20**';
		const strikethrough = '~~watch https://youtu.be/abcdefghijk?si=tracking~~';
		expect(cleanTrackingParameters(bold)).toBe(bold);
		expect(cleanTrackingParameters(strikethrough)).toBe(strikethrough);
	});

	test('leaves inline and fenced code examples untouched', () => {
		const input = '`https://x.com/a/status/1?s=1`\n```txt\nhttps://youtu.be/abc?si=demo\n```\nhttps://x.com/b/status/2?s=2';
		const expected = '`https://x.com/a/status/1?s=1`\n```txt\nhttps://youtu.be/abc?si=demo\n```\nhttps://x.com/b/status/2';
		expect(cleanTrackingParameters(input)).toBe(expected);
	});

	test('keeps fenced code intact when an unmatched inline code span precedes it', () => {
		const input = '`unclosed inline\n```txt\n`https://x.com/a/status/1?s=1\n```\nhttps://x.com/b/status/2?s=2';
		const expected = '`unclosed inline\n```txt\n`https://x.com/a/status/1?s=1\n```\nhttps://x.com/b/status/2';
		expect(cleanTrackingParameters(input)).toBe(expected);
	});

	test('preserves raw query encoding, key order, duplicates not targeted, and fragments', () => {
		const input = 'https://youtu.be/abc?x=%2f&si=one&x=%2F&feature=shared#t=12';
		expect(cleanTrackingParameters(input)).toBe('https://youtu.be/abc?x=%2f&x=%2F&feature=shared#t=12');
		expect(cleanTrackingParameters('https://x.com/a?utm%5Fsource=test&v=1')).toBe('https://x.com/a?v=1');
	});

	test('trims a long run of unmatched URL closers while keeping balanced closers', () => {
		const url = 'https://youtu.be/abc(foo)?si=share';
		const extraClosers = ')'.repeat(8000);
		expect(cleanTrackingParameters(url + extraClosers)).toBe('https://youtu.be/abc(foo)' + extraClosers);
		expect(cleanTrackingParameters('https://youtu.be/abc(foo)?si=share))')).toBe('https://youtu.be/abc(foo)))');
	});

	test('preserves suffix matching after an unmatched longer inline-code run', () => {
		const inCode = 'https://x.com/a/status/1?s=1```';
		const afterCode = 'https://x.com/b/status/2?s=2';
		expect(cleanTrackingParameters('prefix ````' + inCode + ' ' + afterCode))
			.toBe('prefix ' + '`' + '```' + inCode + ' https://x.com/b/status/2');
	});

	test('keeps fence lookalikes with embedded JavaScript line terminators unfenced', () => {
		const input = '```\u2028intro\nhttps://x.com/a/status/1?s=1';
		expect(cleanTrackingParameters(input))
			.toBe('```\u2028intro\nhttps://x.com/a/status/1');
	});

	test('handles long unmatched backtick runs and still cleans following URLs', () => {
		const unmatched = '`'.repeat(8000);
		expect(cleanTrackingParameters('prefix ' + unmatched + ' https://x.com/a/status/1?s=1'))
			.toBe('prefix ' + unmatched + ' https://x.com/a/status/1');
	});

	test('registers the transform as a default plugin behavior', () => {
		const registered: Array<{ id: string; transform: (text: string) => string; options?: { enabledPreferenceKey?: string; notification?: () => string } }> = [];
		const registerPasteTransform: PluginHost['postForm']['registerPasteTransform'] = (id, transform, options) => {
			registered.push({ id, transform, options });
		};
		trackingParameterCleaner.setup({
			postForm: { registerPasteTransform },
		} as unknown as PluginHost);

		expect(registered).toHaveLength(1);
		expect(registered[0].id).toBe('clean-links');
		expect(registered[0].options).toEqual({
			enabledPreferenceKey: 'trackingParameterCleanerEnabled',
			notification: expect.any(Function),
		});
		expect(registered[0].options?.notification?.()).toBe('Tracking parameters removed');
		expect(registered[0].transform('https://x.com/a/status/1?s=1')).toBe('https://x.com/a/status/1');
	});
});
