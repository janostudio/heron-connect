# Publish

这个项目的 npm 包发布名是 `@qinghuangniao/cc-connect-qhn`，命令名是 `cc-connect-qhn`。

这是个人 fork，仅用于个人练手、自用和实验。发布时除了 npm，还要保证 GitHub release 上已经有对应版本的二进制资源，因为 npm 安装阶段会自动下载对应平台的二进制包。

## 发布前准备

先确认这些前提都满足：

- 已登录 npm：`npm whoami`
- 已登录 GitHub CLI：`gh auth status`
- 本地可以正常执行 Go 构建
- 当前版本号已经在 [npm/package.json](/Users/jahweijiang/Documents/agent-qhn/projects/cc-connect-qhn/npm/package.json) 更新
- 如有必要，已更新 [CHANGELOG.md](/Users/jahweijiang/Documents/agent-qhn/projects/cc-connect-qhn/CHANGELOG.md)

## 标准发布流程

进入 npm 包目录：

```bash
cd /Users/jahweijiang/Documents/agent-qhn/projects/cc-connect-qhn/npm
```

建议先做一次本地打包检查：

```bash
npm pack --dry-run
```

然后直接发布：

```bash
npm publish
```

现在 `npm publish` 会自动触发 `prepublishOnly`，做这些事情：

1. 检查 `../dist` 里是否存在当前版本需要的发布归档
2. 如果缺失，就自动执行发布构建
3. 检查 GitHub release `v<version>` 是否存在，不存在就创建
4. 把二进制归档和 `checksums.txt` 上传到 GitHub release
5. 最后再发布 npm 包

也就是说，正常情况下你不需要再手动单独执行 GitHub release 上传。

## 手动分步命令

如果你想分步执行，可以用：

```bash
cd /Users/jahweijiang/Documents/agent-qhn/projects/cc-connect-qhn/npm
node release-assets.js build
node release-assets.js ensure
npm publish
```

说明：

- `node release-assets.js build`：只构建本地 `dist/` 归档
- `node release-assets.js ensure`：构建缺失资源，并同步到 GitHub release
- `npm publish`：发布 npm 包，且会再次做一次 release 资源校验

## 发布后验证

先确认 npm 包版本：

```bash
npm view @qinghuangniao/cc-connect-qhn version
```

确认 GitHub release 资源：

```bash
gh release view v1.3.5 --repo janostudio/cc-connect-qhn --json tagName,assets,url
```

也可以直接验证某个平台资源是否可下载，例如 Linux amd64：

```bash
curl -I -L https://github.com/janostudio/cc-connect-qhn/releases/download/v1.3.5/cc-connect-v1.3.5-linux-amd64.tar.gz
```

最后找一台干净环境机器验证安装：

```bash
npm install -g @qinghuangniao/cc-connect-qhn
cc-connect-qhn --version
```

## 常见问题

### 1. `npm publish` 时报 `EOTP`

这是 npm 账户开启了 2FA 写操作保护。需要使用认证器里当前有效的 6 位验证码，或者按 npm 当前账户策略使用浏览器确认。

### 2. npm 发布成功，但安装时报 GitHub release 404

这说明 npm 包已经发了，但对应版本的 GitHub release 二进制没有上传完整。现在这个仓库已经把这一步并进 `prepublishOnly`，后续应优先使用当前标准流程发布。

### 3. 为什么不能只发 npm，不发 GitHub release

因为这个 npm 包本身只是包装层，安装时会执行 [npm/install.js](/Users/jahweijiang/Documents/agent-qhn/projects/cc-connect-qhn/npm/install.js)，再去 GitHub release 下载对应平台的真实二进制。

如果 release 资源缺失，安装一定失败。
