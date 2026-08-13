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

## Publish Flow

Running `npm publish` in this directory triggers `prepublishOnly`, which:

1. builds missing release archives into `../dist`
2. uses `gh` to create or update the GitHub release `v<package-version>`
3. uploads required archives and `checksums.txt` before publishing to npm

Prerequisites:

- `gh auth login`
- Go build environment available locally
- release repository push permission on `janostudio/heron-connect`
