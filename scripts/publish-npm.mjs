#!/usr/bin/env node
// publish-npm.mjs [--dry-run] [--tag <dist-tag>]
//
// Publishes every package in /npm/ to the public npm registry. The 6 platform
// sub-packages are published first (so the main package can resolve them as
// optionalDependencies), then @techreloaded/archetipo.
//
// The npm dist-tag is derived from the main package version: a prerelease such
// as 2.3.2-beta.1 publishes under the prerelease identifier ("beta"), while a
// plain 2.3.2 publishes under "latest". This keeps prereleases off the default
// `npm install -g @techreloaded/archetipo` channel (use @beta to opt in). Pass
// --tag <dist-tag> to override the derived value.
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

// Map a semver version to an npm dist-tag: prerelease identifier or "latest".
//   2.3.2-beta.1 -> "beta"   2.3.2-rc.2 -> "rc"   2.3.2 -> "latest"
function distTagFromVersion(v) {
  const pre = String(v).replace(/^v/, "").split("-")[1]; // "beta.1" | undefined
  return pre ? pre.split(".")[0] : "latest";
}

let distTag;
const tagIdx = process.argv.indexOf("--tag");
if (tagIdx !== -1 && process.argv[tagIdx + 1]) {
  distTag = process.argv[tagIdx + 1]; // explicit override
} else {
  const mainPkg = JSON.parse(
    await fs.readFile(path.join(npmDir, "archetipo", "package.json"), "utf8"),
  );
  distTag = distTagFromVersion(mainPkg.version);
}

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
