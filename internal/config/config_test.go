package config

import (
	"strings"
	"testing"

	"github.com/Rogsiel/medic/internal/check"
	_ "github.com/Rogsiel/medic/internal/checks"
)

func env(m map[string]string) Options {
	return Options{LookupEnv: func(k string) (string, bool) { v, ok := m[k]; return v, ok }}
}

func load(t *testing.T, src string, e map[string]string) (*Config, error) {
	t.Helper()
	return Load("medic.yaml", []byte(src), env(e))
}

func mustErr(t *testing.T, src string, want ...string) {
	t.Helper()
	_, err := load(t, src, nil)
	if err == nil {
		t.Fatalf("expected error containing %q", want)
	}
	for _, w := range want {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error %q does not contain %q", err, w)
		}
	}
}

const good = `
version: 1
secrets: [TOKEN]
defaults:
  timeout: 3s
checks:
  db:
    type: tcp
    target: ${DB_HOST}:5432
    retry: { until: 60s, interval: 2s }
  api:
    type: http
    url: ${API_URL:-http://localhost:8080}/health
    headers: { Authorization: "Bearer ${TOKEN}" }
    depends_on: [db]
    expect:
      status: [200, 204]
      json: { "$.status": up }
      latency: { warn: 300ms, fail: 2s }
    allow_failure: true
    tags: [prod]
  root:
    type: disk
    path: /
`

func TestLoadGood(t *testing.T) {
	cfg, err := load(t, good, map[string]string{"DB_HOST": "db", "TOKEN": "s3cret"})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Checks) != 3 || cfg.Checks[0].ID != "db" || cfg.Checks[2].ID != "root" {
		t.Fatalf("order/ids wrong: %+v", cfg.Checks)
	}
	if got := cfg.Checks[0].Checker.Target(); got != "db:5432" {
		t.Errorf("interpolation: %q", got)
	}
	if got := cfg.Checks[1].Checker.Target(); got != "http://localhost:8080/health" {
		t.Errorf("default interpolation: %q", got)
	}
	if cfg.Checks[2].Timeout.D.Seconds() != 3 {
		t.Errorf("defaults.timeout not applied")
	}
	if len(cfg.SecretValues) != 1 || cfg.SecretValues[0] != "s3cret" {
		t.Errorf("secrets: %v", cfg.SecretValues)
	}
	if cfg.FailOn != "failure" || cfg.Concurrency != 8 {
		t.Errorf("defaults: %q %d", cfg.FailOn, cfg.Concurrency)
	}
	var _ check.Checker = cfg.Checks[0].Checker
}

func TestErrors(t *testing.T) {
	mustErr(t, `checks: {a: {type: cpu}}`, `missing required key "version"`)
	mustErr(t, "version: 2\nchecks: {a: {type: cpu}}", "unsupported config version 2")
	mustErr(t, "version: 1\nchecks: {a: {type: htp, url: x}}", `unknown check type "htp"`, `did you mean "http"`)
	mustErr(t, "version: 1\nchecks:\n  a:\n    type: http\n    url: http://x\n    expect_status: [200]\n",
		"line 6:5", `did you mean "expect.status"`)
	mustErr(t, "version: 1\nchecks: {a: {type: http, url: http://x, timout: 5s}}", `did you mean "timeout"`)
	mustErr(t, "version: 1\nchecks: {a: {type: cpu, timeout: 5}}", "duration needs a unit")
	mustErr(t, "version: 1\nchecks: {a: {type: tcp, target: x}}", "expected host:port")
	mustErr(t, "version: 1\nchecks: {a: {type: tcp, target: '${NOPE}:1'}}", "NOPE is not set")
	mustErr(t, "version: 1\nchecks: {a: {type: cpu, depends_on: [b]}}", `unknown check id "b"`)
	mustErr(t, "version: 1\nchecks: {a: {type: cpu, depends_on: [b]}, b: {type: cpu, depends_on: [a]}}", "dependency cycle: a -> b -> a")
	mustErr(t, "version: 1\nchecks: {Bad: {type: cpu}}", "invalid check id")
	mustErr(t, "version: 1\nchecks: {a: {type: exec}}", "set 'command'")
	mustErr(t, "version: 1\nchecks: {a: {type: exec, command: [x], shell: y}}", "only one of")
	mustErr(t, "version: 1\nchecks: {a: {type: http, url: http://x, follow_redirects: yes}}", "expected true or false")
	mustErr(t, "version: 1\nchecks: {a: {type: http, url: http://x, method: FETCH}}", "must be one of GET")
	mustErr(t, "version: 1\nchecks: {a: {type: disk, path: /, expect: {used: {warn: 95%, fail: 80%}}}}", "must not be above fail")
	mustErr(t, "version: 1\nchecks: {a: {type: cpu, retry: {until: 5s, attempts: 2}}}", "not both")
	mustErr(t, "version: 1\nchecks:\n  a: {type: cpu}\n  a: {type: cpu}\n", "already defined")
	mustErr(t, "version: 1\nx: &a 1\nchecks: {a: {type: cpu}}", "anchors")
	mustErr(t, "version: 1\nfail_on: warn\nchecks: {a: {type: cpu}}", "must be one of failure, warning")
	mustErr(t, "version: 1\nchecks: {}", "no checks defined")
	mustErr(t, "", "config is empty")
}

func TestMultipleErrorsReported(t *testing.T) {
	_, err := load(t, "version: 1\nchecks:\n  a: {type: cpu, foo: 1}\n  b: {type: cpu, bar: 1}\n", nil)
	if err == nil || strings.Count(err.Error(), "unknown key") != 2 {
		t.Fatalf("want both errors, got %v", err)
	}
}

func TestInterpolate(t *testing.T) {
	lk := func(k string) (string, bool) { return map[string]string{"A": "1", "E": ""}[k], k == "A" || k == "E" }
	cases := map[string]string{"x${A}y": "x1y", "${B:-d}": "d", "$${A}": "${A}", "${E}": "", "${E:-d}": "d", "$5": "$5"}
	for in, want := range cases {
		got, err := Interpolate(in, lk)
		if err != nil || got != want {
			t.Errorf("Interpolate(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"${B}", "${A", "${a b}"} {
		if _, err := Interpolate(bad, lk); err == nil {
			t.Errorf("Interpolate(%q): expected error", bad)
		}
	}
}

func TestExamplesValid(t *testing.T) {
	for _, typ := range check.Types() {
		src := "version: 1\nchecks:\n  ex:\n    " + strings.ReplaceAll(typ.Example, "\n", "\n    ") + "\n"
		if _, err := load(t, src, nil); err != nil {
			t.Errorf("example for %s is invalid: %v", typ.Name, err)
		}
	}
}
