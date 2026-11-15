/*
 * SPDX-FileCopyrightText: syuilo and misskey-project
 * SPDX-License-Identifier: AGPL-3.0-only
 */

export type PreparedPublicMedia = {
	url: string;
	contentType: string;
	expiresAt: string;
	size: number;
};

export class PublicMediaForbiddenError extends Error {
	constructor() {
		super('public media access was denied');
		this.name = 'PublicMediaForbiddenError';
	}
}

export function isHttpForbidden(error: unknown): boolean {
	if (error instanceof PublicMediaForbiddenError) return true;
	if (typeof error !== 'object' || error == null) return false;
	const value = error as { status?: unknown; statusCode?: unknown; httpStatus?: unknown };
	return value.status === 403 || value.statusCode === 403 || value.httpStatus === 403;
}

const imageTypes = ['image/jpeg', 'image/png', 'image/gif', 'image/webp', 'image/avif'];

export async function readBoundedMediaBlob(response: Response, options: { maxBytes: number; expectedType: string; expectedSize?: number; signal: AbortSignal }): Promise<Blob> {
	const reader = response.body?.getReader();
	if (reader == null) throw new Error('missing media body');
	const chunks: ArrayBuffer[] = [];
	let size = 0;
	try {
		while (true) {
			if (options.signal.aborted) throw new DOMException('aborted', 'AbortError');
			const { done, value } = await reader.read();
			if (done) break;
			size += value.byteLength;
			if (!Number.isSafeInteger(size) || size > options.maxBytes || (options.expectedSize != null && size > options.expectedSize)) {
				await reader.cancel();
				throw new Error('media body exceeds limit');
			}
			// Fetch streams may use pooled backing stores. Copy only this bounded
			// view so neighboring bytes from the pool cannot enter the resulting Blob.
			const copy = new Uint8Array(value.byteLength);
			copy.set(value);
			chunks.push(copy.buffer);
		}
	} catch (error) {
		try { await reader.cancel(); } catch { /* stream already closed */ }
		throw error;
	}
	if (options.signal.aborted || size > options.maxBytes || (options.expectedSize != null && size !== options.expectedSize)) throw new Error('invalid media body size');
	return new Blob(chunks, { type: options.expectedType });
}

/** Fetch already-authorized public bytes without forwarding the host session. */
export async function fetchPreparedPublicBlob(
	prepared: PreparedPublicMedia,
	options: { expectedType: 'image' | 'video'; maxBytes: number; signal: AbortSignal; fetcher?: typeof fetch },
): Promise<Blob> {
	const type = prepared.contentType?.split(';')[0].trim().toLowerCase();
	const allowedTypes = options.expectedType === 'image' ? imageTypes : ['video/mp4'];
	if (!allowedTypes.includes(type) || !Number.isSafeInteger(prepared.size) || prepared.size < 0 || prepared.size > options.maxBytes) throw new Error('invalid prepared media');
	const url = new URL(prepared.url, window.location.origin);
	if (url.origin !== window.location.origin || url.protocol !== window.location.protocol || url.username !== '' || url.password !== '' || url.hash !== '' || url.search !== '' || !/^\/api\/plugin\/fxtwitter-preview\/public-media\/[a-f0-9]{32}\.(?:jpg|jpeg|png|gif|webp|avif|mp4)$/.test(url.pathname)) throw new Error('invalid public media URL');
	const extension = url.pathname.slice(url.pathname.lastIndexOf('.') + 1);
	const expectedExtensions: Record<string, string[]> = {
		'image/jpeg': ['jpg', 'jpeg'],
		'image/png': ['png'],
		'image/gif': ['gif'],
		'image/webp': ['webp'],
		'image/avif': ['avif'],
		'video/mp4': ['mp4'],
	};
	if (!expectedExtensions[type]?.includes(extension)) throw new Error('invalid public media extension');
	if (options.signal.aborted) throw new DOMException('aborted', 'AbortError');
	const fetcher = options.fetcher ?? ((input, init) => window.fetch(input, init));
	if (options.signal.aborted) throw new DOMException('aborted', 'AbortError');
	const response = await fetcher(url.href, {
		method: 'GET',
		credentials: 'omit',
		mode: 'same-origin',
		redirect: 'error',
		cache: 'default',
		signal: options.signal,
	});
	const responseType = response.headers.get('Content-Type')?.split(';')[0].trim().toLowerCase();
	if (response.status === 403) throw new PublicMediaForbiddenError();
	if (options.signal.aborted || !response.ok || responseType !== type) throw new Error('invalid public media response');
	const length = response.headers.get('Content-Length');
	if (length != null && (!/^\d+$/.test(length) || Number(length) !== prepared.size || Number(length) > options.maxBytes)) throw new Error('invalid public media size');
	const blob = await readBoundedMediaBlob(response, { maxBytes: options.maxBytes, expectedType: type, expectedSize: prepared.size, signal: options.signal });
	if (blob.type.split(';')[0].trim().toLowerCase() !== type) throw new Error('invalid public media bytes');
	return blob;
}
