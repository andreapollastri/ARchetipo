#!/usr/bin/env node
'use strict';

const path = require('node:path');
const fs = require('node:fs');
const { spawnSync } = require('node:child_process');

const platform = process.platform; // 'darwin' | 'linux' | 'win32'
const arch = process.arch;         // 'arm64' | 'x64'

const supported = new Set([
  'darwin-arm64', 'darwin-x64',
  'linux-arm64', 'linux-x64',
  'win32-arm64', 'win32-x64',
]);
const key = `${platform}-${arch}`;

if (!supported.has(key)) {
  process.stderr.write(`ARchetipo does not ship a prebuilt binary for ${key}.\n`);
  process.stderr.write(`Supported: ${[...supported].join(', ')}.\n`);
  process.exit(1);
}

const subPkg = `@techreloaded/archetipo-${platform}-${arch}`;
const binName = platform === 'win32' ? 'archetipo.exe' : 'archetipo';

let binPath;
try {
  const pkgJsonPath = require.resolve(`${subPkg}/package.json`);
  binPath = path.join(path.dirname(pkgJsonPath), 'bin', binName);
} catch (err) {
  process.stderr.write(
    `Cannot locate ${subPkg}. The optional dependency was not installed.\n` +
    `Reinstall with: npm i -g @techreloaded/archetipo\n`
  );
  process.exit(1);
}

if (!fs.existsSync(binPath)) {
  process.stderr.write(`Native binary missing at ${binPath}. Reinstall the CLI.\n`);
  process.exit(1);
}

const dataDir = path.resolve(__dirname, '..');
const env = Object.assign({}, process.env, { ARCHETIPO_DATA_DIR: dataDir });

const result = spawnSync(binPath, process.argv.slice(2), { stdio: 'inherit', env });

if (result.error) {
  process.stderr.write(`Failed to spawn archetipo: ${result.error.message}\n`);
  process.exit(1);
}
if (result.signal) {
  process.kill(process.pid, result.signal);
  return;
}
process.exit(result.status === null ? 1 : result.status);
