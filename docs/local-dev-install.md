# 本地开发版安装与替换说明

本文专门说明一个容易混淆的问题：`heron-connect` 虽然核心实现是 Go 二进制，但全局安装场景里常常通过 npm 暴露命令入口。因此，本地开发版覆盖时不能只盯着 PATH 里的那个 `heron-connect` 路径。

## 背景

如果你是通过下面这种方式安装的：

```bash
npm install -g heron-connect
```

那么 PATH 里的 `heron-connect` 往往不是最终可执行二进制，而是 npm 放出来的入口脚本。以当前机器为例：

```bash
/Users/jahweijiang/.nvm/versions/node/v24.11.0/bin/heron-connect
```

这个路径通常是一个软链接，最后会跳到全局 npm 包目录中的 `run.js`，再由 `run.js` 去执行真正的 Go 二进制：

```text
~/.nvm/versions/node/<version>/lib/node_modules/heron-connect/run.js
~/.nvm/versions/node/<version>/lib/node_modules/heron-connect/bin/heron-connect
```

所以从执行链路上看：

1. shell 调用 PATH 里的 `heron-connect`
2. 这个入口跳到 npm 包内的 `run.js`
3. `run.js` 再调用 `bin/heron-connect`
4. `bin/heron-connect` 才是真正的 Go 程序

## 为什么不能只覆盖二进制

单独替换 `bin/heron-connect` 在很多场景下仍然不稳。原因是 npm 包里的 `run.js` 会读取同目录 `package.json` 的版本号，并和实际二进制版本做比较。

如果你从当前仓库直接构建，默认 Go 二进制版本可能是 git 描述值，比如：

- `c099ce6-dirty`
- `dev`

而 npm 全局包里的版本可能是发布版本，比如：

- `1.3.3-beta.2`

这时 `run.js` 可能把你本地编译的程序认成“旧版本”或“不匹配版本”，然后尝试重新下载安装官方 release，把你刚替换的开发版又覆盖回去。

## 现在的做法

仓库里已经增加了 `make build-local` 目标，位置见 [Makefile](/Users/jahweijiang/Documents/agent-qhn/projects/heron-connect/Makefile)。

执行：

```bash
make build-local
```

它会做这些事：

1. 先构建前端资源 `web/`
2. 用 npm 包版本号重新编译本地 Go 二进制，避免版本比较误判
3. 覆盖全局 npm 包目录中的包装层文件：`package.json`、`run.js`、`install.js`、`README.md`
4. 覆盖真正执行的二进制：`$(npm root -g)/heron-connect/bin/heron-connect`

换句话说，它替换的不是 PATH 里的软链接本身，而是软链接最终指向的 npm 包内容。

## 适用场景

### 场景 1：你通过 npm 全局安装

这是 `make build-local` 的主要目标场景。直接执行：

```bash
make build-local
heron-connect --version
```

### 场景 2：你是手动把二进制装进 PATH

如果 `which heron-connect` 输出的是这种路径：

- `/usr/local/bin/heron-connect`
- `/opt/homebrew/bin/heron-connect`

并且它不是 npm 包装层，那就不需要 `build-local` 这套逻辑，直接覆盖即可：

```bash
sudo install -m 755 ./heron-connect "$(which heron-connect)"
```

### 场景 3：你只想临时测试当前仓库构建

不改全局安装，直接把当前目录放到 PATH 前面：

```bash
export PATH="$PWD:$PATH"
./heron-connect --version
heron-connect --version
```

这种方式只适合临时调试。

## 如何确认当前命令走的是哪条链路

先看入口：

```bash
which heron-connect
ls -l "$(which heron-connect)"
```

如果你看到它是一个指向 `../lib/node_modules/heron-connect/run.js` 的软链接，那么说明当前就是 npm 包装层模式。

还可以直接看全局 npm 包目录：

```bash
npm root -g
ls -l "$(npm root -g)/heron-connect"
ls -l "$(npm root -g)/heron-connect/bin"
```

## 替换后建议验证

至少做两步：

1. 执行 `heron-connect --version`，确认版本号已经变成当前仓库希望暴露的版本
2. 执行一次最小启动验证，例如 `heron-connect -config ./config.toml` 或你自己的日常启动命令

如果你是通过守护进程或系统服务启动，还需要重启对应服务，否则旧进程不会自动切到新二进制。

## 补充说明

npm 在这里负责的是分发、安装和命令入口管理；真正的业务实现和运行时行为仍然由 Go 二进制承担。也就是说：

- npm 不是主要实现语言
- Node.js 不是核心运行时
- `heron-connect` 真正执行的仍然是 Go 编译出来的二进制文件
