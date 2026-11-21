# 🩺 medic

[![CI](https://github.com/Rogsiel/medic/actions/workflows/ci.yml/badge.svg)](https://github.com/Rogsiel/medic/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Health checks for pipelines, in one static binary. Declare what "healthy" means in `medic.yaml`, then use it to
**gate a deploy**, **smoke-test** one, or **wait for dependencies** to come up. medic reports problems; it does not
fix them.

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/Rogsiel/medic/main/schema/medic.schema.json
version: 1
secrets: [API_TOKEN]

checks:
  db:
    type: tcp
    target: ${DB_HOST}:5432
    retry: { until: 60s, interval: 2s }   # wait for it to come up

  api:
    type: http
    url: ${API_URL}/health
    headers: { Authorization: "Bearer ${API_TOKEN}" }
    depends_on: [db]                       # starts once db passes
    expect:
      status: [200]
      json: { "$.status": up }
      latency: { warn: 300ms, fail: 2s }

  cert:
    type: tls
    target: api.example.com
    expect:
      days_left: { warn: 30d, fail: 7d }
    allow_failure: true                    # report, never block
```

```console
$ medic
✔ db    connected in 3ms (3ms)
✔ api   200 in 41ms (41ms)
⚠ cert  expires in 21 days (2026-12-12) (85ms)

healthy: 2 passed, 1 warning in 129ms
```

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/Rogsiel/medic/main/install.sh | sh   # Linux, macOS
go install github.com/Rogsiel/medic/cmd/medic@latest
docker run --rm -v "$PWD:/work" ghcr.io/rogsiel/medic
yay -S medic-bin                                                                   # Arch (AUR)
```

Or download a binary from [releases](https://github.com/Rogsiel/medic/releases). Shell completion:
`medic completion bash|zsh|fish|powershell`.

## Usage

```bash
medic init                  # write a commented starter medic.yaml
medic validate              # check the config without running anything
medic                       # run it (same as: medic run)
medic list                  # ids, types, targets, dependencies, tags
medic explain http          # reference for a check type
medic schema                # JSON Schema for medic.yaml (also: report, exec-output)

medic --wait 2m                         # retry failing checks for up to 2 minutes
medic --only api                        # api plus everything it depends on
medic --skip tag:slow                   # skip by id or tag (dependents are skipped too)
medic -o junit=medic.xml -o json=-      # reports to files or stdout (human output moves to stderr)
medic --watch 10s                       # re-run every 10s until Ctrl-C

medic check tcp db:5432 --wait 60s      # one check, no config file
medic check http https://x/health --set expect.status=[200,204]
```

## Check types

| Type | Verifies |
|---|---|
| [`http`](docs/checks/http.md) | Status, body text, JSON fields, latency, redirects, TLS verification |
| [`tcp`](docs/checks/tcp.md) | Port open (or closed), connect latency, banner |
| [`tls`](docs/checks/tls.md) | Certificate chain and days until expiry |
| [`dns`](docs/checks/dns.md) | Resolution (optionally via a given server) and expected addresses |
| [`ssh`](docs/checks/ssh.md) | SSH banner, optionally key authentication |
| [`ping`](docs/checks/ping.md) | ICMP packet loss and round-trip time |
| [`exec`](docs/checks/exec.md) | Any command, by exit code/output, or reporting its own JSON result |
| [`file`](docs/checks/file.md) | Exists (or not), mode, age, contents |
| [`cpu`](docs/checks/cpu.md), [`memory`](docs/checks/memory.md), [`swap`](docs/checks/swap.md), [`disk`](docs/checks/disk.md), [`load`](docs/checks/load.md) | Local resources |

Full reference: [docs/config.md](docs/config.md).

## The config, in short

- **Strict.** Unknown keys, wrong types, unknown check types and bad references are errors with line, column and a
  "did you mean" hint. Every error is reported at once. `medic validate --format json` gives them to tools and LLMs.
- **Typed units.** Durations (`500ms`, `2m`), sizes (`512MiB`), percentages (`85%`) and days (`30d`) always carry a
  unit; `timeout: 5` is rejected. Thresholds are always `{warn: X, fail: Y}`.
- **One way to write things.** Checks are a map keyed by id with an explicit `type`; what to probe sits at the top,
  what counts as healthy goes under `expect:`. No anchors, merge keys or profiles.
- **Ordering by dependency.** Everything runs in parallel; `depends_on` makes a check wait, and skips it if a
  dependency fails.
- **Environment, not variants.** `${VAR}` / `${VAR:-default}` fill in per-environment values; unset variables are
  errors. Values of variables listed in `secrets:` are masked in all output.
- **Custom checks.** An `exec` check with `output: json` lets any script report
  `{"status": "passed|warning|failed", "message": "...", "details": {...}}` ([schema](schema/exec-output.schema.json)).

## In CI

Exit codes: `0` healthy, `1` unhealthy (or warnings with `--fail-on warning`), `2` config/usage error, `3` internal
error.

On GitHub Actions medic adds error/warning annotations and a step summary automatically (`--ci off` to disable):

```yaml
- uses: Rogsiel/medic@v0.2.0
  with:
    args: --wait 2m -o junit=medic.xml
```

GitLab CI:

```yaml
smoke-test:
  image: ghcr.io/rogsiel/medic:0.2.0
  script: [medic --wait 2m -o junit=medic.xml]
  artifacts:
    when: always
    reports: { junit: medic.xml }
```

The JSON report (`-o json=FILE`) is versioned and has a [schema](schema/report.schema.json).

## Development

```bash
go test ./...
go run ./cmd/medic gen-docs .   # regenerate docs/ and schema/ after changing a check type
```

Check types live in `internal/checks/`. Their struct tags are the single source for config parsing, validation,
the JSON Schema, the reference docs and `medic explain`.

## License

MIT
