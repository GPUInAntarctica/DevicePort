# Contributing / 贡献指南

Thanks for your interest in DevicePort!

## Development setup

- Go **1.26+** (the module uses a `tool` directive for `gomobile`).
- For Android: Android SDK (platform 35, build-tools 36), NDK 27, JDK 17+, `gomobile`.
- For desktop: Node.js 20+ and npm.
- For Windows testing: Wine (Linux) or a real Windows host with [Wintun](https://www.wintun.net/).

```bash
git clone https://github.com/GPUInAntarctica/DevicePort.git
cd DevicePort
make test
make build
```

## Code style

- Run `make fmt` and `make vet` before opening a PR.
- Keep the core packages dependency-free (standard library only) where practical.
- Add tests for protocol/crypto changes; the E2E test in `internal/node/e2e_test.go`
  is the reference for direct + relay behavior.

## Commits & PRs

- Use clear, imperative commit messages (e.g. `fix: handle relay timeout`).
- Describe testing steps in the PR. If you changed the wire format, update
  `docs/PROTOCOL.md` and bump the protocol version.
- UI strings must be added to **both** languages in `internal/api/web/index.html`
  (`I18N.en` and `I18N.zh`) and, for native Android strings, in both
  `res/values/strings.xml` and `res/values-zh/strings.xml`.

## Security

Please do not open public issues for security vulnerabilities. Report them privately
to the maintainers. This prototype has **not** been audited; do not use it to protect
sensitive traffic without a professional review.

## 中文

欢迎贡献！开发环境需要 Go 1.26+；Android 构建需要 SDK/NDK 和 gomobile；桌面端需要 Node.js。
提交前请运行 `make fmt`、`make vet` 和 `make test`。修改协议时请同步更新 `docs/PROTOCOL.md`。
添加界面文案时请同时更新中英文。请勿在公开 issue 中报告安全漏洞。
