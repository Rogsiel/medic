# Changelog

## 0.1.1

- Distribution is now named `medic-cli` (the `medic` name on PyPI is taken); the command is still `medic`.
- `medic --version` reads the installed package version.
- Add README screenshot, AUR `PKGBUILD` (`packaging/`), and a PyPI publish step in the release workflow.

## 0.1.0

Initial release.

- Checks: system (CPU, memory, swap, disk, uptime), ping, TCP, HTTP(S), TLS certificates, SSH, DNS, and arbitrary commands (`--exec`).
- TOML config (`--config`) with `stage`s: a failing stage skips all later stages.
- Per-check timeouts enforced by the runner; hung checks are reported as failed.
- `--fail-on warning`, `--stop-on-failure`, `--watch`.
- Output formats: rich, text, compact, JSON, JUnit XML.
- Shell completion for bash, zsh, fish (`--install-completion`).
