/*
 * SPDX-FileCopyrightText: syuilo and misskey-project
 * SPDX-License-Identifier: AGPL-3.0-only
 */
import { readFileSync, readdirSync } from 'node:fs';
import { createRequire } from 'node:module';
import { fileURLToPath, URL } from 'node:url';
import process from 'node:process';
import console from 'node:console';
import assert from 'node:assert/strict';

const root = import.meta.dirname;
const files = readdirSync(root).filter(file => /\.(?:ts|vue|mjs)$/.test(file)).sort();
assert(files.length > 0, 'No plugin frontend files found');
const mode = process.argv[2];
if (mode === 'spdx') {
	// The shared checker has a fixed frontend-only inventory and no explicit
	// path option. Inspect this module's complete source inventory instead.
	for (const file of files) {
		const source = readFileSync(new URL(file, import.meta.url), 'utf8');
		const header = file.endsWith('.vue') ? source.match(/^<!--([\s\S]*?)-->/)?.[1] : source.match(/^\/\*([\s\S]*?)\*\//)?.[1];
		assert(header?.includes('SPDX-FileCopyrightText:'), `${file}: copyright header missing`);
		assert(header.includes('SPDX-License-Identifier: AGPL-3.0-only'), `${file}: license header missing`);
		console.log(`SPDX ${file}`);
	}
	console.log(`Fx frontend SPDX: ${files.length} checked, 0 missing`);
} else if (mode === 'lint') {
	const require = createRequire(new URL('../../../frontend/packages/frontend/package.json', import.meta.url));
	const { ESLint } = require('eslint');
	const eslint = new ESLint({ cwd: root, fix: process.argv.includes('--fix'), overrideConfigFile: fileURLToPath(new URL('eslint.config.mjs', import.meta.url)) });
	const ignored = [];
	for (const file of files) {
		if (await eslint.isPathIgnored(file)) ignored.push(file);
		const config = await eslint.calculateConfigForFile(file);
		assert(config != null, `${file}: no ESLint configuration`);
		if (file.endsWith('.ts') || file.endsWith('.vue')) {
			assert(config.languageOptions.parserOptions.project != null, `${file}: typed rules missing`);
			if (file.endsWith('.vue')) assert(config.languageOptions.parserOptions.tsconfigRootDir === root, `${file}: wrong typed project`);
		}
	}
	assert.deepEqual(ignored, [], 'Ignored plugin source files');
	const results = await eslint.lintFiles(files);
	if (process.argv.includes('--fix')) await ESLint.outputFixes(results);
	assert.equal(results.length, files.length, 'Incomplete lint result inventory');
	console.log(await (await eslint.loadFormatter('stylish')).format(results));
	for (const result of results) console.log(`LINT ${result.filePath}`);
	const errors = results.reduce((sum, result) => sum + result.errorCount, 0);
	const warnings = results.reduce((sum, result) => sum + result.warningCount, 0);
	console.log(`Fx frontend ESLint: ${results.length} checked, ${ignored.length} ignored, ${errors} errors, ${warnings} warnings`);
	process.exitCode = results.some(result => result.errorCount > 0 || result.fatalErrorCount > 0) ? 1 : 0;
} else {
	throw new Error('Usage: node plugins/fxtwitter-preview/frontend/check-frontend.mjs lint|spdx');
}
