# cc-connect-qhn

This package is an unofficial personal fork of `cc-connect`.

It is published only for personal practice, experimentation, and self-use. It is not an official upstream distribution and should not be treated as a supported replacement for the original project.

Fork base: upstream `cc-connect` release `v1.3.3-beta.2`.

Local fork import reference in this repository: commit `c099ce699e44d74a9f2018244375a4ff410cd7eb`.

## Install

```bash
npm install -g @qinghuangniao/cc-connect-qhn
```

## Usage

```bash
# Create config
cc-connect-qhn --version

# Edit config.toml, then run
cc-connect-qhn
cc-connect-qhn -config /path/to/config.toml
```

## Documentation

Fork repository: https://github.com/janostudio/cc-connect-qhn

Original upstream project: https://github.com/chenhg5/cc-connect

## Publish Flow

Running `npm publish` in this directory now triggers `prepublishOnly`, which will:

1. build missing release archives into `../dist`
2. use `gh` to create or update GitHub release `v<package-version>`
3. upload required archives and `checksums.txt` before publishing to npm

Prerequisites for publish:

- `gh auth login`
- Go build environment available locally
- release repository push permission on `janostudio/cc-connect-qhn`
