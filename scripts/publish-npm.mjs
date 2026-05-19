#!/usr/bin/env node
// publish-npm.mjs [--dry-run] [--tag <dist-tag>]
//
// Publishes every package in /npm/ to the public npm registry. The 6 platform
// sub-packages are published first (so the main package can resolve them as
// optionalDependencies), then @techreloaded/archetipo.
//
// Requires NPM_TOKEN to be set (or `npm login` to have been run interactively).

import { spawnSync } from "node:child_process";
import fs from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(__dirname, "..");
const npmDir = path.join(repoRoot, "npm");

const args = new Set(process.argv.slice(2));
const dryRun = args.has("--dry-run");
let distTag = "latest";
const tagIdx = process.argv.indexOf("--tag");
if (tagIdx !== -1 && process.argv[tagIdx + 1]) distTag = process.argv[tagIdx + 1];

async function pkgDirs() {
  const entries = await fs.readdir(npmDir, { withFileTypes: true });
  return entries
    .filter((e) => e.isDirectory() && e.name.startsWith("archetipo"))
    .map((e) => path.join(npmDir, e.name));
}

function publish(dir) {
  const argv = ["publish", "--access", "public", "--tag", distTag];
  if (dryRun) argv.push("--dry-run");
  const r = spawnSync("npm", argv, { cwd: dir, stdio: "inherit" });
  if (r.status !== 0) {
    console.error(`✗ npm publish failed for ${path.basename(dir)} (exit ${r.status})`);
    process.exit(r.status ?? 1);
  }
}

const dirs = await pkgDirs();
// platform packages first, main last
const platformDirs = dirs.filter((d) => path.basename(d) !== "archetipo");
const mainDir = dirs.find((d) => path.basename(d) === "archetipo");
if (!mainDir) {
  console.error("✗ npm/archetipo/ not found");
  process.exit(2);
}

for (const dir of platformDirs) publish(dir);
publish(mainDir);

console.log(`\n${dryRun ? "[dry-run] " : ""}Published ${dirs.length} package(s) with tag '${distTag}'.`);
