---
title: Installation
description: Homebrew, npm, PyPI, go install, and prebuilt binaries.
icon: download
weight: 200
---

## CLI

```sh
brew install agent-ecosystem/tap/agentminutes
```

```sh
npm install -g agentminutes
```

```sh
pip install agentminutes
```

```sh
go install github.com/agent-ecosystem/agentminutes/cmd/agentminutes@latest
```

The npm and pip packages wrap the same prebuilt Go binary (macOS, Linux,
and Windows on x64/arm64); nothing extra is downloaded at install time.
Prebuilt static binaries are also on the
[releases page](https://github.com/agent-ecosystem/agentminutes/releases).

## Library

```sh
go get github.com/agent-ecosystem/agentminutes
```

See [Go Library](/docs/library/) for the API.
