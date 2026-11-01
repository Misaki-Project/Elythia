/*
 * SPDX-FileCopyrightText: syuilo and misskey-project
 * SPDX-License-Identifier: AGPL-3.0-only
 */
import frontendConfig from '../../../frontend/packages/frontend/eslint.config.js';

const remapPattern = pattern => Array.isArray(pattern) ? pattern.map(remapPattern) : typeof pattern === 'string' ? pattern.replace(/^src\//, '') : pattern;

// Apply the host's existing rules, including its typed Vue rules, to this
// independent module. The host config otherwise scopes those rules to src/.
// ESLint's flat-config loader requires the configuration's default export.
// eslint-disable-next-line import/no-default-export
export default frontendConfig.map(config => ({
	...config,
	...(config.files == null ? {} : { files: config.files.map(remapPattern) }),
	...(config.languageOptions?.parserOptions?.project == null ? {} : {
		languageOptions: {
			...config.languageOptions,
			parserOptions: {
				...config.languageOptions.parserOptions,
				project: ['./tsconfig.json'],
				tsconfigRootDir: import.meta.dirname,
			},
		},
	}),
}));
