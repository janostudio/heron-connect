#!/usr/bin/env node
"use strict";

// 生成 npm 分发产物：主包 + 各平台二进制包。
//
// 二进制随 npm 平台包发布，安装时 npm 按平台包的 os/cpu 只拉当前平台那一份，
// 走 registry（国内可用镜像）。主包的 postinstall 只在平台包缺失时回退下载，
// 不再是唯一获取途径。
//
// 产物结构：
//   <out>/main/                 主包：run.js + install.js + lib/
//   <out>/platform-<key>/       平台包：bin/heron-connect[.exe]，带 os/cpu 约束
//
// 用法：
//   node npm/make-platform-packages.js --dist dist --out npm-dist [--version 1.4.1]

const fs = require("fs");
const path = require("path");

const PACKAGE_NAME = "@qinghuangniao/heron-connect";
const APP = "heron-connect";
const REPO = "janostudio/heron-connect";
const NPM_DIR = __dirname;
const REPO_ROOT = path.resolve(NPM_DIR, "..");

const PLATFORMS = [
  { key: "linux-x64", os: "linux", cpu: "x64", goos: "linux", goarch: "amd64", ext: "" },
  { key: "linux-arm64", os: "linux", cpu: "arm64", goos: "linux", goarch: "arm64", ext: "" },
  { key: "darwin-x64", os: "darwin", cpu: "x64", goos: "darwin", goarch: "amd64", ext: "" },
  { key: "darwin-arm64", os: "darwin", cpu: "arm64", goos: "darwin", goarch: "arm64", ext: "" },
  { key: "win32-x64", os: "win32", cpu: "x64", goos: "windows", goarch: "amd64", ext: ".exe" },
  { key: "win32-arm64", os: "win32", cpu: "arm64", goos: "windows", goarch: "arm64", ext: ".exe" },
];

// 发布产物里只保留安装期需要的脚本；prepare-release / sync-release /
// prepublishOnly 是本地开发与手工发布用的，进了包只会误导使用者。
const RELEASE_SCRIPTS = { postinstall: "node install.js" };

function parseArgs(argv) {
  const args = { dist: path.join(REPO_ROOT, "dist"), out: path.join(REPO_ROOT, "npm-dist"), version: null };
  for (let i = 0; i < argv.length; i++) {
    const flag = argv[i];
    if (flag === "--dist") args.dist = path.resolve(argv[++i]);
    else if (flag === "--out") args.out = path.resolve(argv[++i]);
    else if (flag === "--version") args.version = argv[++i];
    else throw new Error(`Unknown argument: ${flag}`);
  }
  return args;
}

function writeJSON(file, value) {
  fs.writeFileSync(file, JSON.stringify(value, null, 2) + "\n");
}

function copyInto(source, target) {
  fs.mkdirSync(path.dirname(target), { recursive: true });
  fs.copyFileSync(source, target);
  fs.chmodSync(target, fs.statSync(source).mode & 0o777);
}

// release-all 产出的裸二进制以 `heron-connect-<version>-<goos>-<goarch>` 命名，
// 版本段带 v 前缀且可能是 git describe 的值，这里忽略版本段按平台后缀匹配。
function findBuiltBinary(distDir, platform) {
  const suffix = `-${platform.goos}-${platform.goarch}${platform.ext}`;
  const match = fs
    .readdirSync(distDir)
    .find((name) => name.startsWith(`${APP}-`) && name.endsWith(suffix) && !name.endsWith(".tar.gz") && !name.endsWith(".zip"));
  return match ? path.join(distDir, match) : null;
}

function buildPlatformPackage(outDir, version, platform, distDir) {
  const source = findBuiltBinary(distDir, platform);
  if (!source) {
    throw new Error(`Missing platform binary for ${platform.key} in ${distDir} (expected *-${platform.goos}-${platform.goarch}${platform.ext})`);
  }

  const pkgDir = path.join(outDir, `platform-${platform.key}`);
  const binaryPath = path.join(pkgDir, "bin", `${APP}${platform.ext}`);
  copyInto(source, binaryPath);
  fs.chmodSync(binaryPath, 0o755);

  writeJSON(path.join(pkgDir, "package.json"), {
    name: PACKAGE_NAME,
    version: `${version}-${platform.key}`,
    description: `heron-connect binary for ${platform.key}`,
    os: [platform.os],
    cpu: [platform.cpu],
    files: ["bin/"],
    license: "MIT",
    repository: { type: "git", url: `https://github.com/${REPO}` },
  });

  return pkgDir;
}

// buildMainPackage 复制主包模板并注入版本与实际存在的平台包依赖。
// optionalDependencies 用 npm: 别名指向同一包名的预发布版本，安装时落到
// node_modules/@qinghuangniao/heron-connect-<platform>。
function buildMainPackage(outDir, version) {
  const pkgDir = path.join(outDir, "main");
  fs.mkdirSync(pkgDir, { recursive: true });

  const pkg = JSON.parse(fs.readFileSync(path.join(NPM_DIR, "package.json"), "utf8"));
  pkg.version = version;
  pkg.scripts = { ...RELEASE_SCRIPTS };
  pkg.optionalDependencies = {};
  for (const platform of PLATFORMS) {
    pkg.optionalDependencies[`${PACKAGE_NAME}-${platform.key}`] = `npm:${PACKAGE_NAME}@${version}-${platform.key}`;
  }
  writeJSON(path.join(pkgDir, "package.json"), pkg);

  for (const file of ["install.js", "run.js", "release-assets.js", "README.md"]) {
    const source = path.join(NPM_DIR, file);
    if (fs.existsSync(source)) copyInto(source, path.join(pkgDir, file));
  }
  copyInto(path.join(NPM_DIR, "lib", "resolve-binary.js"), path.join(pkgDir, "lib", "resolve-binary.js"));

  return pkgDir;
}

function main() {
  const args = parseArgs(process.argv.slice(2));
  const templateVersion = JSON.parse(fs.readFileSync(path.join(NPM_DIR, "package.json"), "utf8")).version;
  const version = args.version || templateVersion;
  if (!version || version.includes("dev")) {
    throw new Error(`Refusing to build packages with placeholder version: ${version}`);
  }
  if (args.version && args.version !== templateVersion) {
    console.warn(`[make-platform-packages] --version ${args.version} differs from npm/package.json ${templateVersion}`);
  }

  fs.rmSync(args.out, { recursive: true, force: true });
  fs.mkdirSync(args.out, { recursive: true });

  const platformDirs = PLATFORMS.map((platform) => buildPlatformPackage(args.out, version, platform, args.dist));
  const mainDir = buildMainPackage(args.out, version);

  for (const dir of platformDirs) {
    console.log(`[make-platform-packages] ${path.basename(dir)}`);
  }
  console.log(`[make-platform-packages] main (${version}) -> ${mainDir}`);
}

main();
