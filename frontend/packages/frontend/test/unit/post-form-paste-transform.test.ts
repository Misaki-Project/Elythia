/*
 * SPDX-FileCopyrightText: syuilo and misskey-project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import { afterEach, describe, expect, test, vi } from 'vitest';
import {
	_resetPasteTransformsForTest,
	launchServerPlugins,
	transformPastedText,
	type PluginDefinition,
} from '@/plugin-api.js';

afterEach(() => {
	_resetPasteTransformsForTest();
	vi.restoreAllMocks();
});

describe('post form paste transforms', () => {
	test('composes transforms in namespaced ID order, independent of plugin setup order', async () => {
		const plugin = (name: string, id: string, suffix: string): PluginDefinition => ({
			name,
			setup(host) {
				host.postForm.registerPasteTransform(id, text => text + suffix);
			},
		});
		await launchServerPlugins([
			plugin('zeta', 'first', 'Z'),
			plugin('alpha', 'later', 'L'),
			plugin('alpha', 'first', 'A'),
		]);

		expect(transformPastedText('paste').text).toBe('pasteALZ');
	});

	test('namespaces and validates IDs, and rejects duplicate registrations', async () => {
		let firstDuplicateError: unknown;
		let invalidIDError: unknown;
		let emptyIDError: unknown;
		await launchServerPlugins([
			{
				name: 'alpha',
				setup(host) {
					host.postForm.registerPasteTransform('clean', text => `${text}!`);
					try {
						host.postForm.registerPasteTransform('clean', text => text);
					} catch (err) {
						firstDuplicateError = err;
					}
					try {
						host.postForm.registerPasteTransform('invalid:id', text => text);
					} catch (err) {
						invalidIDError = err;
					}
					try {
						host.postForm.registerPasteTransform('', text => text);
					} catch (err) {
						emptyIDError = err;
					}
				},
			},
			{
				name: 'beta',
				setup(host) {
					host.postForm.registerPasteTransform('clean', text => `${text}?`);
				},
			},
		]);

		expect(firstDuplicateError).toBeInstanceOf(Error);
		expect((firstDuplicateError as Error).message).toContain('alpha:clean');
		expect(invalidIDError).toBeInstanceOf(Error);
		expect(emptyIDError).toBeInstanceOf(Error);
		expect(transformPastedText('paste').text).toBe('paste!?');
	});

	test('keeps the last valid value for thrown, non-string, and rejected async transforms', async () => {
		const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
		await launchServerPlugins([{
			name: 'bad',
			setup(host) {
				host.postForm.registerPasteTransform('throws', text => {
					throw new Error(`private clipboard value: ${text}`);
				});
				host.postForm.registerPasteTransform('number', (() => 42) as unknown as (text: string) => string);
				host.postForm.registerPasteTransform('async', ((text: string) => Promise.reject(new Error(text))) as unknown as (text: string) => string);
				host.postForm.registerPasteTransform('valid', text => `${text}!`);
			},
		}]);

		expect(transformPastedText('private clipboard value').text).toBe('private clipboard value!');
		await Promise.resolve();
		expect(warn).toHaveBeenCalled();
		expect(warn.mock.calls.flat().join(' ')).not.toContain('private clipboard value');
	});

	test('does nothing when no plugin registered a transform', () => {
		expect(transformPastedText('unchanged').text).toBe('unchanged');
	});

	test('honors the live preference and returns one localized message only for final changes', async () => {
		const transform = vi.fn((text: string) => text.replace('utm_source=track', ''));
		await launchServerPlugins([{
			name: 'cleaner',
			setup(host) {
				host.postForm.registerPasteTransform('tracking', transform, {
					enabledPreferenceKey: 'trackingParameterCleanerEnabled',
					notificationMessageKey: 'trackingParameterCleanerApplied',
				});
			},
		}]);
		const input = 'https://example.com/?utm_source=track';
		const runtime = {
			isPreferenceEnabled: (key: string) => key === 'trackingParameterCleanerEnabled' ? false : undefined,
			resolveMessage: (key: string) => key === 'trackingParameterCleanerApplied' ? 'Tracking parameters removed' : undefined,
		};

		const disabled = transformPastedText(input, runtime);
		expect(disabled).toEqual({ text: input });
		expect(transform).not.toHaveBeenCalled();

		const enabled = transformPastedText(input, {
			...runtime,
			isPreferenceEnabled: () => true,
		});
		expect(enabled).toEqual({ text: 'https://example.com/?', notification: 'Tracking parameters removed' });

		const unchanged = transformPastedText('https://example.com/', {
			...runtime,
			isPreferenceEnabled: () => true,
		});
		expect(unchanged).toEqual({ text: 'https://example.com/' });

		const missingPreferenceUsesDefault = transformPastedText(input, {
			...runtime,
			isPreferenceEnabled: () => undefined,
		});
		expect(missingPreferenceUsesDefault.notification).toBe('Tracking parameters removed');
	});

	test('preference and message resolver failures never interrupt the paste', async () => {
		await launchServerPlugins([{
			name: 'cleaner',
			setup(host) {
				host.postForm.registerPasteTransform('tracking', text => `${text}!`, {
					enabledPreferenceKey: 'trackingParameterCleanerEnabled',
					notificationMessageKey: 'trackingParameterCleanerApplied',
				});
			},
		}]);
		expect(transformPastedText('paste', {
			isPreferenceEnabled: () => { throw new Error('preference unavailable'); },
			resolveMessage: () => { throw new Error('message unavailable'); },
		})).toEqual({ text: 'paste!' });
	});
});
