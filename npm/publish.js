#!/usr/bin/env node
"use strict";

// 发布 npm 包：先发各平台二进制包，再发主包。
//
// 主包的 optionalDependencies 指向平台包（npm: 别名），顺序反了会有一段时间
// 让用户装到"主包已就位、平台包还没发布"的版本，那样只能回退 GitHub 下载。
//
// 用法：
//   node npm/publish.js              # 构建缺失产物 → 发平台包 → 发主包
//   node npm/publish.js --dry-run    # 只验证打包结果，不真正发布
//   node npm/publish.js --skip-build # 跳过 release-assets.js build（产物已就绪）

const { execFileSync } = require("child_process");
const fs = require("fs");
const path = require("path");

const NPM_DIR = __dirname;
const REPO_ROOT = path.resolve(NPM_DIR, "..");
const DIST_DIR = path.join(REPO_ROOT, "dist");
const OUT_DIR = path.join(REPO_ROOT, "npm-dist");
const NPMRC = path.join(NPM_DIR, ".npmrc");

function run(cmd, args, opts = {}) {
  execFileSync(cmd, args, { stdio: "inherit", cwd: opts.cwd || REPO_ROOT });
}

function publishArgs(extra) {
  const args = ["publish", "--access", "public", ...extra];
  // 本地发布时 token 通常写在 npm/.npmrc；产物目录不在 npm/ 下，显式指给 npm。
  if (fs.existsSync(NPMRC)) args.push("--userconfig", NPMRC);
  return args;
}

function main() {
  const argv = process.argv.slice(2);
  const dryRun = argv.includes("--dry-run");
  const skipBuild = argv.includes("--skip-build");

  const version = JSON.parse(fs.readFileSync(path.join(NPM_DIR, "package.json"), "utf8")).version;
  console.log(`[publish] version ${version}${dryRun ? " (dry run)" : ""}`);

  if (!skipBuild) {
    run("node", [path.join(NPM_DIR, "release-assets.js"), "build"]);
  }
  run("node", [
    path.join(NPM_DIR, "make-platform-packages.js"),
    "--dist",
    DIST_DIR,
    "--out",
    OUT_DIR,
    "--version",
    version,
  ]);

  const extra = dryRun ? ["--dry-run"] : [];
  const platformDirs = fs
    .readdirSync(OUT_DIR)
    .filter((name) => name.startsWith("platform-"))
    .sort();

  for (const name of platformDirs) {
    const key = name.replace(/^platform-/, "");
    console.log(`[publish] platform package ${key}`);
    run("npm", publishArgs([...extra, "--tag", `platform-${version}-${key}`]), { cwd: path.join(OUT_DIR, name) });
  }

  console.log("[publish] main package");
  run("npm", publishArgs(extra), { cwd: path.join(OUT_DIR, "main") });
  console.log("[publish] done");
}

main();
