// Package docs generates the JSON Schemas, reference docs and the init
// template from the check type definitions.
package docs

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/Rogsiel/medic/internal/check"
	"github.com/Rogsiel/medic/internal/config"
	"github.com/Rogsiel/medic/internal/spec"
	"github.com/Rogsiel/medic/internal/units"
)

// SchemaBaseURL is where the published schemas live.
const SchemaBaseURL = "https://raw.githubusercontent.com/Rogsiel/medic/main/schema/"

// ConfigSchema returns the JSON Schema for medic.yaml.
func ConfigSchema() map[string]any {
	g := spec.NewSchemaGen()
	s := g.For(reflect.TypeOf(config.File{}))
	props := s["properties"].(map[string]any)
	props["version"] = map[string]any{"const": config.Version, "description": "Config format version. Must be 1."}
	s["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	s["$id"] = SchemaBaseURL + "medic.schema.json"
	s["title"] = "medic config"
	s["$defs"] = g.Defs
	return s
}

// ReportSchema returns the JSON Schema of `--output json`.
func ReportSchema() map[string]any {
	str := map[string]any{"type": "string"}
	integer := map[string]any{"type": "integer"}
	strList := map[string]any{"type": "array", "items": str}
	status := map[string]any{"enum": []string{"passed", "warning", "failed", "skipped", "error"}}
	counts := map[string]any{}
	for _, k := range []string{"total", "passed", "warning", "failed", "skipped", "error"} {
		counts[k] = integer
	}
	checkProps := map[string]any{
		"id": str, "name": str, "type": str, "status": status, "message": str, "target": str,
		"attempts": integer, "duration": str, "depends_on": strList, "tags": strList,
		"details": map[string]any{"type": "object"}, "remediation": str,
	}
	return map[string]any{
		"$schema":  "https://json-schema.org/draft/2020-12/schema",
		"$id":      SchemaBaseURL + "report.schema.json",
		"title":    "medic report",
		"type":     "object",
		"required": []string{"schema_version", "medic_version", "started_at", "duration", "fail_on", "exit_code", "summary", "checks"},
		"properties": map[string]any{
			"schema_version": map[string]any{"const": 1},
			"medic_version":  str,
			"started_at":     map[string]any{"type": "string", "format": "date-time"},
			"duration":       str,
			"fail_on":        map[string]any{"enum": []string{"failure", "warning"}},
			"exit_code":      map[string]any{"enum": []int{0, 1, 3}, "description": "0 healthy, 1 unhealthy, 3 internal error."},
			"summary":        map[string]any{"type": "object", "properties": counts, "required": keys(counts)},
			"checks": map[string]any{"type": "array", "items": map[string]any{
				"type": "object", "properties": checkProps,
				"required": []string{"id", "name", "type", "status", "message", "target", "attempts", "duration", "depends_on", "tags", "details"},
			}},
		},
	}
}

// ExecOutputSchema returns the schema of what an `output: json` exec check prints.
func ExecOutputSchema() map[string]any {
	return map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"$id":                  SchemaBaseURL + "exec-output.schema.json",
		"title":                "medic exec check result",
		"description":          "Printed as one JSON object on stdout by a command run with `output: json`.",
		"type":                 "object",
		"required":             []string{"status"},
		"additionalProperties": false,
		"properties": map[string]any{
			"status":  map[string]any{"enum": []string{"passed", "warning", "failed"}},
			"message": map[string]any{"type": "string", "description": "One-line human summary."},
			"details": map[string]any{"type": "object", "description": "Extra data, shown with -v and in JSON output."},
		},
	}
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for _, k := range []string{"total", "passed", "warning", "failed", "skipped", "error"} {
		if _, ok := m[k]; ok {
			out = append(out, k)
		}
	}
	return out
}

// MarshalSchema renders a schema as stable, indented JSON.
func MarshalSchema(s map[string]any) []byte {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		panic(err)
	}
	return append(b, '\n')
}

func table(rows []spec.FieldDoc) string {
	var b strings.Builder
	b.WriteString("| Key | Type | Default | Description |\n|---|---|---|---|\n")
	for _, r := range rows {
		key := "`" + r.Key + "`"
		if r.Required {
			key += " **(required)**"
		}
		def := ""
		if r.Default != "" {
			def = "`" + r.Default + "`"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", key, r.Type, def, strings.ReplaceAll(r.Doc, "|", "\\|"))
	}
	return b.String()
}

// TypeMarkdown is the reference page of one check type.
func TypeMarkdown(t *check.Type) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# `%s` check\n\n%s\n\n", t.Name, t.Summary)
	b.WriteString("```yaml\nchecks:\n  example:\n    " + strings.ReplaceAll(t.Example, "\n", "\n    ") + "\n```\n\n")
	b.WriteString("## Keys\n\n")
	b.WriteString(table(spec.Docs(reflect.ValueOf(t.New()).Elem(), "")))
	b.WriteString("\nEvery check also accepts the [common keys](../config.md#common-check-keys).\n")
	if t.Positional != "" {
		fmt.Fprintf(&b, "\nOne-liner: `medic check %s <%s> [--set key=value ...]`\n", t.Name, t.Positional)
	} else {
		fmt.Fprintf(&b, "\nOne-liner: `medic check %s [--set key=value ...]`\n", t.Name)
	}
	return b.String()
}

// ConfigMarkdown is the reference page for the top-level config.
func ConfigMarkdown() string {
	var b strings.Builder
	b.WriteString("# Config reference\n\n")
	b.WriteString("<!-- Generated by `medic gen-docs`. Do not edit. -->\n\n")
	b.WriteString("medic reads `medic.yaml` (or `medic.yml`) from the working directory, `-c FILE`, or `-c -` for stdin.\n")
	b.WriteString("Add this first line for editor completion and validation:\n\n")
	b.WriteString("```yaml\n# yaml-language-server: $schema=" + SchemaBaseURL + "medic.schema.json\n```\n\n")
	b.WriteString("## Top-level keys\n\n")
	var f config.File
	b.WriteString(table(spec.Docs(reflect.ValueOf(&f).Elem(), "")))
	b.WriteString("\n## Common check keys\n\nEvery entry under `checks:` is keyed by its id (`[a-z0-9][a-z0-9_-]*`) and accepts:\n\n")
	var c check.Common
	b.WriteString(table(spec.Docs(reflect.ValueOf(&c).Elem(), "")))
	b.WriteString("\n## Check types\n\n")
	for _, t := range check.Types() {
		fmt.Fprintf(&b, "- [`%s`](checks/%s.md): %s\n", t.Name, t.Name, t.Summary)
	}
	b.WriteString("\n## Value types\n\nQuantities are strings with a unit; bare numbers are rejected so nothing is ambiguous.\n\n")
	b.WriteString("| Type | Format | Examples |\n|---|---|---|\n")
	for _, d := range units.Defs {
		fmt.Fprintf(&b, "| %s | %s | `%s` |\n", d.Kind, d.Description, strings.Join(d.Examples, "`, `"))
	}
	b.WriteString("| {warn, fail} X | A threshold: `{warn: X, fail: X}`, at least one bound. Each key says whether higher or lower values are worse. | `{warn: 80%, fail: 95%}` |\n")
	b.WriteString(`
## Environment variables and secrets

` + "`${VAR}`" + ` is replaced in any string value. An unset variable is an error; use ` + "`${VAR:-default}`" + ` for a fallback
and ` + "`$${`" + ` for a literal ` + "`${`" + `. Variables listed under ` + "`secrets:`" + ` are masked as ` + "`***`" + ` in every output.

## Ordering and retries

All checks run in parallel (up to ` + "`concurrency`" + `). ` + "`depends_on`" + ` makes a check wait until its dependencies
pass or warn; if one fails or is skipped, the dependent is skipped. ` + "`timeout`" + ` bounds one attempt; ` + "`retry`" + `
repeats a failing check (` + "`{until: 60s}`" + ` or ` + "`{attempts: 3}`" + `). ` + "`--wait 2m`" + ` applies ` + "`retry.until`" + ` to every
check without its own retry.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Healthy (warnings allowed unless ` + "`fail_on: warning`" + `) |
| 1 | A check failed (or warned with ` + "`fail_on: warning`" + `) |
| 2 | Config or usage error |
| 3 | Internal error in medic |
`)
	return b.String()
}

// InitTemplate is the starter config written by `medic init`.
func InitTemplate() string {
	var b strings.Builder
	b.WriteString("# yaml-language-server: $schema=" + SchemaBaseURL + "medic.schema.json\n")
	b.WriteString("# medic config. Reference: https://github.com/Rogsiel/medic/blob/main/docs/config.md\n")
	b.WriteString("version: 1\n\ndefaults:\n  timeout: 5s\n\nchecks:\n")
	b.WriteString("  # Local system\n")
	for _, item := range []struct{ id, typ string }{{"cpu", "cpu"}, {"memory", "memory"}, {"swap", "swap"}, {"disk-root", "disk"}, {"load", "load"}} {
		t, _ := check.Lookup(item.typ)
		fmt.Fprintf(&b, "  %s:\n    %s\n", item.id, strings.ReplaceAll(t.Example, "\n", "\n    "))
	}
	b.WriteString("\n  # Services: uncomment and edit. depends_on orders checks; retry waits for slow starters.\n")
	for _, typ := range []string{"tcp", "http", "tls", "dns", "exec"} {
		t, _ := check.Lookup(typ)
		fmt.Fprintf(&b, "  # %s-example:\n  #   %s\n", typ, strings.ReplaceAll(t.Example, "\n", "\n  #   "))
	}
	return b.String()
}
