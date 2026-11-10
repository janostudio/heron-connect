# Publish

这个项目的 npm 包发布名是 `@qinghuangniao/heron-connect`，命令名是 `heron-connect`。

这是个人 fork，仅用于个人练手、自用和实验。

## 分发方式

二进制随 npm 平台包一起发布，安装时由 npm 按平台包的 `os`/`cpu` 字段只拉当前平台那一份：

- 主包 `@qinghuangniao/heron-connect`：只有 `run.js`、`install.js` 等包装脚本。
- 平台包：同一包名的预发布版本（`<version>-<platform>`，如 `1.4.0-linux-x64`），通过 `optionalDependencies` 的 `npm:` 别名安装到 `node_modules/@qinghuangniao/heron-connect-<platform>`。

这样安装走 npm registry（国内可用镜像），不需要访问 GitHub Releases。GitHub Release 里的归档仍然保留，供手动下载或非 npm 渠道使用，但不再是 npm 安装的前提。

## 发布前准备

先确认这些前提都满足：

- 已登录 npm：`npm whoami`
- 已登录 GitHub CLI：`gh auth status`（只有要同步 GitHub Release 归档时才需要）
- 本地可以正常执行 Go 构建与前端构建
- 当前版本号已经在 [npm/package.json](npm/package.json) 更新
- 如有必要，已更新 [CHANGELOG.md](CHANGELOG.md)

## 标准发布流程

```bash
make publish
```

它会按顺序做：

1. 跑 release-local 测试
2. `node npm/release-assets.js ensure`：构建各平台二进制，创建 `v<version>` 的 GitHub Release 并上传归档
3. `node npm/publish.js --skip-build`：生成 `npm-dist/`（主包 + 各平台包），**先发平台包，再发主包**

顺序不能反：主包的 `optionalDependencies` 指向平台包，先发主包会让装到该版本的用户找不到二进制。

只验证不发布：

```bash
node npm/publish.js --dry-run
```

## 手动分步命令

```bash
node npm/release-assets.js build     # 只构建本地 dist/ 二进制与归档
node npm/release-assets.js ensure    # 构建缺失资源并同步到 GitHub Release
node npm/publish.js --skip-build     # 生成 npm-dist/ 并发布（平台包 → 主包）
```

`npm/publish.js` 每个平台包用独立 dist-tag `platform-<version>-<platform>` 发布。**不要**用 `npm publish` 直接发 `npm/` 目录：模板里没有注入 `optionalDependencies` 和各平台版本，装出来的包只能回退 GitHub 下载。

## 发布后验证

```bash
npm view @qinghuangniao/heron-connect version
npm view @qinghuangniao/heron-connect@<version>-linux-x64 version   # 平台包应存在
```

再找一台干净环境机器验证安装，且确认装下来的二进制来自平台包（不走下载）：

```bash
npm install -g @qinghuangniao/heron-connect
heron-connect --version
ls "$(npm root -g)/@qinghuangniao/heron-connect/node_modules/@qinghuangniao"
```

## 常见问题

### 1. `npm publish` 时报 `EOTP`

这是 npm 账户开启了 2FA 写操作保护。需要使用认证器里当前有效的 6 位验证码，或者按 npm 当前账户策略使用浏览器确认。

### 2. 安装后提示 `Missing binary for <platform>`

说明平台包没有装到。常见原因：用了 `--no-optional`、registry 镜像还没同步到该版本，或发布时漏发了平台包。先确认 `npm view @qinghuangniao/heron-connect@<version>-<platform> version` 能查到。

### 3. GitHub Release 还要不要发

要发，但它不再是 npm 安装的前提。归档给手动下载和非 npm 渠道使用；平台包才是 npm 安装的主路径。
