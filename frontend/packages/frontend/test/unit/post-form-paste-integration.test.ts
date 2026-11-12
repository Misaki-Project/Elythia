/*
 * SPDX-FileCopyrightText: syuilo and misskey-project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

import { afterEach, describe, expect, test, vi } from 'vitest';
import { cleanup, render } from '@testing-library/vue';
import { nextTick } from 'vue';
import MkPostForm from '@/components/MkPostForm.vue';
import { _resetPasteTransformsForTest, launchServerPlugins } from '@/plugin-api.js';
import { preferState } from '../setup.unit.js';

const testState = vi.hoisted(() => ({
	confirm: vi.fn(),
	addFiles: vi.fn(),
	transform: vi.fn(),
	toast: vi.fn(),
}));

vi.mock('@/i.js', async () => {
	const { reactive } = await import('vue');
	const user = reactive({ id: 'paste-test-user', avatarUrl: '', federationStatus: 'on', policies: new Proxy({}, { get: () => false }) });
	return { $i: user, ensureSignin: () => user, notesCount: 0, incNotesCount: vi.fn(), iAmModerator: false, iAmAdmin: false };
});

vi.mock('@/store.js', async () => {
	const { ref } = await import('vue');
	const models = new Map<string, unknown>();
	return {
		store: {
			r: { tips: { value: { postForm: true } } },
			s: { showPreview: false, localOnly: false, visibility: 'public', reactionAcceptance: 'nonSensitiveOnly' },
			model(key: string) {
				if (!models.has(key)) models.set(key, ref(key === 'postFormWithHashtags' ? false : ''));
				return models.get(key);
			},
			set: vi.fn(),
		},
	};
});

vi.mock('@/composables/use-uploader.js', async () => {
	const { ref } = await import('vue');
	return {
		useUploader: () => ({
			items: ref([]),
			uploading: ref(false),
			readyForUpload: ref(true),
			allItemsUploaded: ref(true),
			events: { on: vi.fn() },
			addFiles: testState.addFiles,
			removeItem: vi.fn(),
			dispose: vi.fn(),
			abortAll: vi.fn(),
			reset: vi.fn(),
		}),
	};
});

vi.mock('@/plugin.js', () => ({ getPluginHandlers: () => [] }));
vi.mock('@/os.js', () => ({ confirm: testState.confirm, toast: testState.toast }));
vi.mock('@/utility/autocomplete.js', () => ({ Autocomplete: class { detach() {} } }));
vi.mock('@/local-storage.js', () => ({ miLocalStorage: {
	getItem: () => null,
	setItem: vi.fn(),
	removeItem: vi.fn(),
	getItemAsJson: () => undefined,
	setItemAsJson: vi.fn(),
} }));
vi.mock('@/utility/misskey-api.js', () => ({ misskeyApi: vi.fn(async () => ({})) }));

async function flush(): Promise<void> {
	await Promise.resolve();
	await nextTick();
	await Promise.resolve();
}

function makePasteEvent(text: string, items: DataTransferItem[] = []): ClipboardEvent {
	const event = new Event('paste', { bubbles: true, cancelable: true }) as ClipboardEvent;
	Object.defineProperty(event, 'clipboardData', {
		value: { items, getData: (type: string) => type === 'text' ? text : '' },
	});
	return event;
}

async function mountPostForm(initialText = '') {
	return render(MkPostForm, {
		props: { initialText, instant: true, autofocus: false, mock: false },
		global: {
			stubs: { MkEllipsis: true, MkAcct: true, MkTime: true, I18n: true, MkTip: true },
			directives: { 'click-anime': () => {}, tooltip: () => {} },
		},
	});
}

afterEach(() => {
	cleanup();
	_resetPasteTransformsForTest();
	testState.confirm.mockReset();
	testState.addFiles.mockReset();
	testState.transform.mockReset();
	testState.toast.mockReset();
	delete preferState.trackingParameterCleanerEnabled;
	vi.restoreAllMocks();
	if (typeof document.execCommand !== 'function') {
		Object.defineProperty(document, 'execCommand', { configurable: true, value: () => false });
	}
});

describe('MkPostForm paste transform integration', () => {
	test('transforms selected text through insertTextAtCursor and keeps native paste when unchanged', async () => {
		await launchServerPlugins([{
			name: 'cleaner',
			setup(host) {
				host.postForm.registerPasteTransform('tracking', text => text.replace('World', 'Galaxy'));
			},
		}]);
		Object.defineProperty(document, 'execCommand', { configurable: true, value: () => false });
		const view = await mountPostForm('Hello World');
		const textarea = view.getByTestId('post-form-text') as HTMLTextAreaElement;
		textarea.focus();
		textarea.setSelectionRange(6, 11);
		const transformed = makePasteEvent('World');
		textarea.dispatchEvent(transformed);
		await flush();
		expect(transformed.defaultPrevented).toBe(true);
		expect(textarea.value).toBe('Hello Galaxy');
		expect(textarea.selectionStart).toBe('Hello Galaxy'.length);

		_resetPasteTransformsForTest();
		await launchServerPlugins([{
			name: 'unchanged',
			setup(host) { host.postForm.registerPasteTransform('identity', text => text); },
		}]);
		const nativePaste = makePasteEvent('native');
		textarea.dispatchEvent(nativePaste);
		await flush();
		expect(nativePaste.defaultPrevented).toBe(false);
		expect(testState.toast).not.toHaveBeenCalled();
	});

	test('reads the current preference at paste time and notifies only when changed text is applied', async () => {
		const transform = vi.fn((text: string) => text.replace('utm_source=track', ''));
		await launchServerPlugins([{
			name: 'cleaner',
			setup(host) {
				host.postForm.registerPasteTransform('tracking', transform, {
					enabledPreferenceKey: 'trackingParameterCleanerEnabled',
					notification: () => 'Tracking parameters removed',
				});
			},
		}]);
		const view = await mountPostForm();
		const textarea = view.getByTestId('post-form-text');
		const input = 'https://example.com/?utm_source=track';
		preferState.trackingParameterCleanerEnabled = false;
		const disabled = makePasteEvent(input);
		textarea.dispatchEvent(disabled);
		await flush();
		expect(disabled.defaultPrevented).toBe(false);
		expect(transform).not.toHaveBeenCalled();
		expect(testState.toast).not.toHaveBeenCalled();

		preferState.trackingParameterCleanerEnabled = true;
		const enabled = makePasteEvent(input);
		textarea.dispatchEvent(enabled);
		await flush();
		expect(enabled.defaultPrevented).toBe(true);
		expect(textarea).toHaveProperty('value', 'https://example.com/?');
		expect(testState.toast).toHaveBeenCalledOnce();
		expect(testState.toast).toHaveBeenCalledWith('Tracking parameters removed');
	});

	test('continues inserting transformed text when the notification fails', async () => {
		await launchServerPlugins([{
			name: 'cleaner',
			setup(host) {
				host.postForm.registerPasteTransform('tracking', text => `${text}!`, {
					notification: () => 'Tracking parameters removed',
				});
			},
		}]);
		testState.toast.mockImplementationOnce(() => { throw new Error('toast unavailable'); });
		const view = await mountPostForm();
		const textarea = view.getByTestId('post-form-text');
		const event = makePasteEvent('paste');
		textarea.dispatchEvent(event);
		await flush();
		expect(event.defaultPrevented).toBe(true);
		expect(textarea).toHaveProperty('value', 'paste!');
		expect(testState.toast).toHaveBeenCalledOnce();
	});

	test('keeps file paste ahead of text transforms', async () => {
		await launchServerPlugins([{
			name: 'cleaner',
			setup(host) { host.postForm.registerPasteTransform('tracking', testState.transform); },
		}]);
		const view = await mountPostForm();
		const textarea = view.getByTestId('post-form-text');
		const file = new File(['image'], 'image.png', { type: 'image/png' });
		const item = { kind: 'file', getAsFile: () => file } as unknown as DataTransferItem;
		const event = makePasteEvent('ignored text', [item]);
		textarea.dispatchEvent(event);
		await flush();
		expect(event.defaultPrevented).toBe(true);
		expect(testState.transform).not.toHaveBeenCalled();
		expect(testState.addFiles).toHaveBeenCalledOnce();
	});

	test('does not lose text when a same-origin quote prefix cannot resolve a quote ID', async () => {
		testState.confirm.mockResolvedValue({ canceled: false });
		const view = await mountPostForm();
		const textarea = view.getByTestId('post-form-text') as HTMLTextAreaElement;
		const raw = `${window.location.origin}/notes/invalid\nchild`;
		const event = makePasteEvent(raw);
		textarea.dispatchEvent(event);
		await flush();
		expect(event.defaultPrevented).toBe(true);
		expect(textarea.value).toBe(raw);
	});

	test('keeps the normal quote accept path from inserting the source URL', async () => {
		testState.confirm.mockResolvedValue({ canceled: false });
		const view = await mountPostForm();
		const textarea = view.getByTestId('post-form-text') as HTMLTextAreaElement;
		const raw = `${window.location.origin}/notes/quoted-note`;
		const event = makePasteEvent(raw);
		textarea.dispatchEvent(event);
		await flush();
		expect(event.defaultPrevented).toBe(true);
		expect(textarea.value).toBe('');
	});

	test('inserts quote text at the active selection when quote confirmation is canceled', async () => {
		testState.confirm.mockResolvedValue({ canceled: true });
		Object.defineProperty(document, 'execCommand', { configurable: true, value: () => false });
		const view = await mountPostForm('abcDEFxyz');
		const textarea = view.getByTestId('post-form-text') as HTMLTextAreaElement;
		textarea.focus();
		textarea.setSelectionRange(3, 6);
		const raw = `${window.location.origin}/notes/quoted-note`;
		const event = makePasteEvent(raw);
		textarea.dispatchEvent(event);
		await flush();
		expect(event.defaultPrevented).toBe(true);
		expect(textarea.value).toBe(`abc${raw}xyz`);
		expect(textarea.selectionStart).toBe(3 + raw.length);
	});

	test('notifies only when a transformed quote URL is inserted, not when its quote is accepted', async () => {
		await launchServerPlugins([{
			name: 'cleaner',
			setup(host) {
				host.postForm.registerPasteTransform('tracking', text => text.replace('?utm_source=track', ''), {
					enabledPreferenceKey: 'trackingParameterCleanerEnabled',
					notification: () => 'Tracking parameters removed',
				});
			},
		}]);
		const raw = `${window.location.origin}/notes/quoted-note?utm_source=track`;
		testState.confirm.mockResolvedValue({ canceled: true });
		const canceledView = await mountPostForm();
		const canceledTextarea = canceledView.getByTestId('post-form-text') as HTMLTextAreaElement;
		canceledTextarea.dispatchEvent(makePasteEvent(raw));
		await flush();
		expect(canceledTextarea.value).toBe(`${window.location.origin}/notes/quoted-note`);
		expect(testState.toast).toHaveBeenCalledWith('Tracking parameters removed');

		cleanup();
		_resetPasteTransformsForTest();
		testState.toast.mockReset();
		testState.confirm.mockReset().mockResolvedValue({ canceled: false });
		await launchServerPlugins([{
			name: 'cleaner',
			setup(host) {
				host.postForm.registerPasteTransform('tracking', text => text.replace('?utm_source=track', ''), {
					enabledPreferenceKey: 'trackingParameterCleanerEnabled',
					notification: () => 'Tracking parameters removed',
				});
			},
		}]);
		const acceptedView = await mountPostForm();
		const acceptedTextarea = acceptedView.getByTestId('post-form-text') as HTMLTextAreaElement;
		acceptedTextarea.dispatchEvent(makePasteEvent(raw));
		await flush();
		expect(acceptedTextarea.value).toBe('');
		expect(testState.toast).not.toHaveBeenCalled();
	});

	test('sends transformed long text to the existing attach-as-file path', async () => {
		testState.confirm.mockResolvedValue({ canceled: false });
		await launchServerPlugins([{
			name: 'cleaner',
			setup(host) {
				host.postForm.registerPasteTransform('tracking', text => `${text} cleaned`, {
					enabledPreferenceKey: 'trackingParameterCleanerEnabled',
					notification: () => 'Tracking parameters removed',
				});
			},
		}]);
		const view = await mountPostForm();
		const textarea = view.getByTestId('post-form-text');
		const raw = 'x'.repeat(1001);
		const event = makePasteEvent(raw);
		textarea.dispatchEvent(event);
		await flush();
		expect(event.defaultPrevented).toBe(true);
		expect(testState.confirm).toHaveBeenCalledOnce();
		expect(testState.addFiles).toHaveBeenCalledOnce();
		const attached = testState.addFiles.mock.calls[0][0][0] as File;
		expect(await attached.text()).toBe(`${raw} cleaned`);
		expect(testState.toast).toHaveBeenCalledWith('Tracking parameters removed');
	});

	test('inserts transformed long text at the active selection when attach is canceled', async () => {
		testState.confirm.mockResolvedValue({ canceled: true });
		await launchServerPlugins([{
			name: 'cleaner',
			setup(host) {
				host.postForm.registerPasteTransform('tracking', text => `${text} cleaned`, {
					enabledPreferenceKey: 'trackingParameterCleanerEnabled',
					notification: () => 'Tracking parameters removed',
				});
			},
		}]);
		Object.defineProperty(document, 'execCommand', { configurable: true, value: () => false });
		const view = await mountPostForm('abcDEFxyz');
		const textarea = view.getByTestId('post-form-text') as HTMLTextAreaElement;
		textarea.focus();
		textarea.setSelectionRange(3, 6);
		const raw = 'x'.repeat(1001);
		const event = makePasteEvent(raw);
		textarea.dispatchEvent(event);
		await flush();
		const inserted = `${raw} cleaned`;
		expect(event.defaultPrevented).toBe(true);
		expect(textarea.value).toBe(`abc${inserted}xyz`);
		expect(textarea.selectionStart).toBe(3 + inserted.length);
		expect(testState.addFiles).not.toHaveBeenCalled();
		expect(testState.toast).toHaveBeenCalledWith('Tracking parameters removed');
	});

	test('does not invoke transforms for readonly paste events', async () => {
		await launchServerPlugins([{
			name: 'cleaner',
			setup(host) { host.postForm.registerPasteTransform('tracking', testState.transform); },
		}]);
		const view = await mountPostForm();
		const textarea = view.getByTestId('post-form-text') as HTMLTextAreaElement;
		textarea.readOnly = true;
		const event = makePasteEvent('keep native');
		textarea.dispatchEvent(event);
		await flush();
		expect(event.defaultPrevented).toBe(false);
		expect(testState.transform).not.toHaveBeenCalled();
	});
});
