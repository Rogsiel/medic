# Changelog

## 0.2.0

Rewritten in Go. medic is now a single static binary (Linux, macOS, Windows; amd64, arm64), with a Docker image,
a GitHub Action and an install script. The Python package `medic-cli` is discontinued.

Breaking: the config format is new (YAML, `version: 1`); the TOML config and most CLI flags are gone.

- YAML config with strict validation: errors carry line/column and "did you mean" hints, all reported at once.
- Typed values with units (`5s`, `512MiB`, `85%`, `30d`) and uniform `{warn, fail}` thresholds under `expect:`.
- `depends_on` replaces stages: a failed dependency skips only its dependents.
- Retries per check (`retry: {until: 60s}` / `{attempts: 3}`) and `--wait` for wait-for-ready pipelines.
- `${VAR}` interpolation and `secrets:` masking in every output.
- `allow_failure`, `fail_on`, `--only` / `--skip` by id or `tag:NAME`.
- New `file` check; `tcp` can require a closed port; `dns` can query a given server; `system` split into
  `cpu`, `memory`, `swap`, `disk` and `load`. `exec` takes `command:` (no shell) or `shell:`, and can report its own
  JSON result.
- Commands: `run`, `validate`, `list`, `check` (one-liner), `init`, `explain`, `schema`.
- Outputs: `-o json|junit|md|text=PATH` (repeatable), GitHub Actions annotations and step summary.
- Published JSON Schemas for the config, the JSON report and exec check output.

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
