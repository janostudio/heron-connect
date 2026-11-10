# heron-connect

A bridge service that connects local AI coding agents to messaging platforms, so you can talk to your AI coding assistant directly from Feishu, Telegram, Discord, Slack, and more.

## Install

```bash
npm install -g @qinghuangniao/heron-connect
```

## Usage

```bash
# Check version
heron-connect --version

# Create config
heron-connect

# Edit config.toml, then run
heron-connect --config /path/to/config.toml
```

## Documentation

Repository: https://github.com/janostudio/heron-connect

Full documentation: see the repository README and `docs/` directory.

## Distribution

The binary ships in per-platform npm packages. The main package only contains the
wrapper scripts; `optionalDependencies` point at `@qinghuangniao/heron-connect-<platform>`
(same package name, pre-release version like `1.4.0-linux-x64`), and npm installs only
the package matching the current `os`/`cpu`. Installing therefore goes through the npm
registry (mirrors work) instead of downloading from GitHub Releases.

## Publish Flow

Run from the repository root:

```bash
node npm/publish.js            # build missing artifacts, publish platform packages, then the main package
node npm/publish.js --dry-run  # pack and verify without publishing
```

`release-assets.js ensure` still uploads archives to the GitHub release for manual
downloads, but that release is no longer required for `npm install` to work.

Prerequisites:

- `npm whoami` succeeds (token in `npm/.npmrc` or the user config)
- `gh auth login` (only when syncing GitHub release archives)
- Go build environment available locally
