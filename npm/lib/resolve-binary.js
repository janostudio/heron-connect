"use strict";

// 解析当前平台可用的连接器二进制。
//
// 解析顺序：
//   1. 主包 bin/ 下的 heron-connect。本地构建（make build-local）直接把产物
//      覆盖在这里，优先保证开发时用的是未发布版本。
//   2. npm 平台包 @qinghuangniao/heron-connect-<platform>。发布时每个平台的
//      二进制打成独立包，安装时 npm 按平台包的 os/cpu 字段只拉当前平台那一份，
//      走 registry（国内可用镜像），不依赖 GitHub Releases 的连通性。
//   3. 都没有时返回 binaryPath=null，由调用方决定回退下载或报错。

const fs = require("fs");
const path = require("path");

const APP = "heron-connect";

const PLATFORM_KEYS = {
  "linux-x64": true,
  "linux-arm64": true,
  "darwin-x64": true,
  "darwin-arm64": true,
  "win32-x64": true,
  "win32-arm64": true,
};

const PLATFORM_PACKAGE_PREFIX = "@qinghuangniao/heron-connect-";

function currentPlatformKey() {
  return `${process.platform}-${process.arch}`;
}

function platformPackageName(platformKey) {
  return PLATFORM_PACKAGE_PREFIX + platformKey;
}

function binaryNameFor(platformKey) {
  return platformKey.startsWith("win32") ? `${APP}.exe` : APP;
}

// resolveBinary 返回 { supported, platformKey, binaryPath, source, packageName }。
// binaryPath 为 null 表示当前平台受支持但二进制还没就位。
function resolveBinary() {
  const platformKey = currentPlatformKey();
  if (!PLATFORM_KEYS[platformKey]) {
    return { supported: false, platformKey, binaryPath: null, source: null };
  }

  const binaryName = binaryNameFor(platformKey);
  const localPath = path.join(__dirname, "..", "bin", binaryName);
  if (fs.existsSync(localPath)) {
    return { supported: true, platformKey, binaryPath: localPath, source: "local" };
  }

  const packageName = platformPackageName(platformKey);
  try {
    const packageJsonPath = require.resolve(`${packageName}/package.json`);
    const candidate = path.join(path.dirname(packageJsonPath), "bin", binaryName);
    if (fs.existsSync(candidate)) {
      return { supported: true, platformKey, binaryPath: candidate, source: "platform-package", packageName };
    }
  } catch (err) {
    // 平台包没装：用了 --no-optional、registry 镜像还没同步到当前版本，
    // 或者不是通过 npm 安装的。交给调用方回退。
  }

  return { supported: true, platformKey, binaryPath: null, source: null };
}

module.exports = { resolveBinary, currentPlatformKey, platformPackageName, binaryNameFor };
