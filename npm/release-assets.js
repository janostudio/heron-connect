#!/usr/bin/env node

"use strict";

const { execFileSync } = require("child_process");
const fs = require("fs");
const path = require("path");

const PACKAGE = require("./package.json");

const REPO = "janostudio/cc-connect-qhn";
const APP = "cc-connect-qhn";
const VERSION = `v${PACKAGE.version}`;
const ROOT_DIR = path.resolve(__dirname, "..");
const DIST_DIR = path.join(ROOT_DIR, "dist");

const PLATFORMS = [
  ["linux", "amd64"],
  ["linux", "arm64"],
  ["darwin", "amd64"],
  ["darwin", "arm64"],
  ["windows", "amd64"],
  ["windows", "arm64"],
];

function run(bin, args, opts = {}) {
  return execFileSync(bin, args, {
    cwd: ROOT_DIR,
    stdio: ["ignore", "pipe", "pipe"],
    encoding: "utf8",
    ...opts,
  }).trim();
}

function archiveName(goos, goarch) {
  const base = `${APP}-${VERSION}-${goos}-${goarch}`;
  return goos === "windows" ? `${base}.zip` : `${base}.tar.gz`;
}

function expectedArchives() {
  return PLATFORMS.map(([goos, goarch]) => archiveName(goos, goarch));
}

function expectedDistFiles() {
  return [...expectedArchives(), "checksums.txt"];
}

function missingFiles(dir, names) {
  return names.filter((name) => !fs.existsSync(path.join(dir, name)));
}

function buildReleaseAssets() {
  console.log(`[release-assets] Building release archives for ${VERSION}`);
  execFileSync("make", ["release-all", `VERSION=${VERSION}`], {
    cwd: ROOT_DIR,
    stdio: "inherit",
  });
}

function ensureLocalAssets() {
  const missing = missingFiles(DIST_DIR, expectedDistFiles());
  if (missing.length === 0) {
    console.log(`[release-assets] Local release assets already exist in ${DIST_DIR}`);
    return;
  }

  console.log(`[release-assets] Missing local release assets:`);
  for (const name of missing) console.log(`  - ${name}`);
  buildReleaseAssets();

  const stillMissing = missingFiles(DIST_DIR, expectedDistFiles());
  if (stillMissing.length > 0) {
    throw new Error(
      `Local release assets are still missing after build: ${stillMissing.join(", ")}`
    );
  }
}

function hasGh() {
  try {
    run("gh", ["--version"]);
    return true;
  } catch {
    return false;
  }
}

function hasGhAuth() {
  try {
    execFileSync("gh", ["auth", "status"], {
      cwd: ROOT_DIR,
      stdio: "ignore",
    });
    return true;
  } catch {
    return false;
  }
}

function releaseInfo() {
  try {
    return JSON.parse(run("gh", ["release", "view", VERSION, "--repo", REPO, "--json", "tagName,assets"]));
  } catch {
    return null;
  }
}

function ensureReleaseExists() {
  if (releaseInfo()) {
    console.log(`[release-assets] GitHub release ${VERSION} already exists`);
    return;
  }

  console.log(`[release-assets] Creating GitHub release ${VERSION}`);
  const notes = [
    `Release assets for npm package ${PACKAGE.name}@${PACKAGE.version}.`,
    "",
    "This is an unofficial personal fork build.",
  ].join("\n");

  execFileSync(
    "gh",
    ["release", "create", VERSION, "--repo", REPO, "--title", VERSION, "--notes", notes],
    { cwd: ROOT_DIR, stdio: "inherit" }
  );
}

function uploadReleaseAssets() {
  const files = expectedDistFiles().map((name) => path.join(DIST_DIR, name));
  console.log(`[release-assets] Uploading release assets to ${REPO}@${VERSION}`);
  execFileSync("gh", ["release", "upload", VERSION, "--repo", REPO, "--clobber", ...files], {
    cwd: ROOT_DIR,
    stdio: "inherit",
  });
}

function ensureRemoteAssets() {
  if (!hasGh()) {
    throw new Error(
      "GitHub CLI `gh` is not installed. Install it or manually upload dist/ archives before npm publish."
    );
  }
  if (!hasGhAuth()) {
    throw new Error(
      "GitHub CLI is not authenticated. Run `gh auth login` before npm publish so release assets can be synced."
    );
  }

  ensureReleaseExists();
  uploadReleaseAssets();

  const info = releaseInfo();
  const assetNames = new Set((info && info.assets ? info.assets : []).map((asset) => asset.name));
  const missingRemote = expectedDistFiles().filter((name) => !assetNames.has(name));
  if (missingRemote.length > 0) {
    throw new Error(`Remote release is missing assets after upload: ${missingRemote.join(", ")}`);
  }

  console.log(`[release-assets] GitHub release ${VERSION} is ready for npm publish`);
}

function printExpectedArchives() {
  for (const name of expectedArchives()) {
    console.log(name);
  }
}

function main() {
  const command = process.argv[2] || "ensure";

  if (command === "list") {
    printExpectedArchives();
    return;
  }

  if (command === "build") {
    ensureLocalAssets();
    return;
  }

  if (command === "ensure") {
    ensureLocalAssets();
    ensureRemoteAssets();
    return;
  }

  console.error("Usage: node release-assets.js [list|build|ensure]");
  process.exit(1);
}

main();
