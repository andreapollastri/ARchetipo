#!/usr/bin/env node
// build-npm.mjs <version>
//
// Synchronizes the /npm/ workspace from the GoReleaser output (cli/dist/) and
// the source skills/runtime assets, then aligns every package.json to the
// given version.
//
// Layout expected after this script runs:
//   npm/archetipo/skills/<skill>/...
//   npm/archetipo/runtime/{config.yaml,shared-runtime.md}
//   npm/archetipo-<os>-<cpu>/bin/archetipo[.exe]

import fs from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(__dirname, "..");
const distDir = path.join(repoRoot, "cli", "dist");
const npmDir = path.join(repoRoot, "npm");
const skillsSrc = path.join(repoRoot, "skills");
const runtimeSrc = path.join(repoRoot, ".archetipo");

const version = process.argv[2];
if (!version) {
  console.error("usage: build-npm.mjs <version>");
  process.exit(2);
}
const normalizedVersion = version.replace(/^v/, "");

// goreleaser dir prefix -> [npm platform, npm cpu, binary basename]
const platformMap = {
  archetipo_darwin_amd64:  ["darwin", "x64",   "archetipo"],
  archetipo_darwin_arm64:  ["darwin", "arm64", "archetipo"],
  archetipo_linux_amd64:   ["linux",  "x64",   "archetipo"],
  archetipo_linux_arm64:   ["linux",  "arm64", "archetipo"],
  archetipo_windows_amd64: ["win32",  "x64",   "archetipo.exe"],
  archetipo_windows_arm64: ["win32",  "arm64", "archetipo.exe"],
};

async function exists(p) {
  try { await fs.access(p); return true; } catch { return false; }
}

async function emptyDir(dir) {
  await fs.rm(dir, { recursive: true, force: true });
  await fs.mkdir(dir, { recursive: true });
}

async function copyDir(src, dst) {
  await fs.mkdir(dst, { recursive: true });
  const entries = await fs.readdir(src, { withFileTypes: true });
  for (const e of entries) {
    const s = path.join(src, e.name);
    const d = path.join(dst, e.name);
    if (e.isDirectory()) await copyDir(s, d);
    else if (e.isFile()) await fs.copyFile(s, d);
  }
}

async function findGoreleaserDir(prefix) {
  const entries = await fs.readdir(distDir);
  // accept e.g. archetipo_darwin_arm64_v8.0, archetipo_linux_amd64_v1
  const match = entries.find((e) => e === prefix || e.startsWith(prefix + "_"));
  return match ? path.join(distDir, match) : null;
}

async function syncBinaries() {
  for (const [prefix, [platform, cpu, bin]] of Object.entries(platformMap)) {
    const src = await findGoreleaserDir(prefix);
    if (!src) {
      console.warn(`! skip ${platform}-${cpu}: goreleaser dir ${prefix}* not found`);
      continue;
    }
    const srcBin = path.join(src, bin);
    if (!(await exists(srcBin))) {
      console.warn(`! skip ${platform}-${cpu}: binary missing at ${srcBin}`);
      continue;
    }
    const dstDir = path.join(npmDir, `archetipo-${platform}-${cpu}`, "bin");
    await emptyDir(dstDir);
    const dstBin = path.join(dstDir, bin);
    await fs.copyFile(srcBin, dstBin);
    await fs.chmod(dstBin, 0o755);
    console.log(`✓ archetipo-${platform}-${cpu}/bin/${bin}`);
  }
}

async function syncAssets() {
  const skillsDst = path.join(npmDir, "archetipo", "skills");
  await emptyDir(skillsDst);
  await copyDir(skillsSrc, skillsDst);
  console.log("✓ archetipo/skills/");

  const runtimeDst = path.join(npmDir, "archetipo", "runtime");
  await emptyDir(runtimeDst);
  for (const name of ["config.yaml", "shared-runtime.md"]) {
    const src = path.join(runtimeSrc, name);
    if (await exists(src)) {
      await fs.copyFile(src, path.join(runtimeDst, name));
    }
  }
  console.log("✓ archetipo/runtime/");
}

async function bumpVersions() {
  const dirs = (await fs.readdir(npmDir, { withFileTypes: true }))
    .filter((e) => e.isDirectory() && e.name.startsWith("archetipo"))
    .map((e) => e.name);

  const mainPkgPath = path.join(npmDir, "archetipo", "package.json");
  const mainPkg = JSON.parse(await fs.readFile(mainPkgPath, "utf8"));
  mainPkg.version = normalizedVersion;
  mainPkg.optionalDependencies = mainPkg.optionalDependencies || {};
  for (const [, [platform, cpu]] of Object.entries(platformMap)) {
    mainPkg.optionalDependencies[`@techreloaded/archetipo-${platform}-${cpu}`] =
      normalizedVersion;
  }
  await fs.writeFile(mainPkgPath, JSON.stringify(mainPkg, null, 2) + "\n");
  console.log(`✓ archetipo@${normalizedVersion}`);

  for (const name of dirs) {
    if (name === "archetipo") continue;
    const pkgPath = path.join(npmDir, name, "package.json");
    const pkg = JSON.parse(await fs.readFile(pkgPath, "utf8"));
    pkg.version = normalizedVersion;
    await fs.writeFile(pkgPath, JSON.stringify(pkg, null, 2) + "\n");
    console.log(`✓ ${name}@${normalizedVersion}`);
  }
}

await syncBinaries();
await syncAssets();
await bumpVersions();
console.log(`\nReady to publish: cd npm/ && node ../scripts/publish-npm.mjs`);
