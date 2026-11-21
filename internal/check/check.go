// Package check defines the check contract, result model and type registry.
package check

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/Rogsiel/medic/internal/spec"
	"github.com/Rogsiel/medic/internal/units"
)

// Status is the outcome of a check.
type Status string

const (
	Passed  Status = "passed"
	Warning Status = "warning"
	Failed  Status = "failed"
	Skipped Status = "skipped"
	Error   Status = "error" // medic itself broke (bug, panic)
)

// Healthy reports whether dependents may proceed.
func (s Status) Healthy() bool { return s == Passed || s == Warning }

// Rank orders statuses from best to worst.
func (s Status) Rank() int {
	switch s {
	case Passed:
		return 0
	case Skipped:
		return 1
	case Warning:
		return 2
	case Failed:
		return 3
	}
	return 4
}

// Worst returns the worse of two statuses.
func Worst(a, b Status) Status {
	if b.Rank() > a.Rank() {
		return b
	}
	return a
}

// Result is what a single check attempt produces.
type Result struct {
	Status      Status
	Message     string
	Details     map[string]any
	Remediation string
}

// Checker is implemented by every check type's spec struct.
type Checker interface {
	// Run performs one attempt. It must honour ctx cancellation/deadline.
	Run(ctx context.Context) Result
	// Target is a short description of what is probed (host:port, URL, path).
	Target() string
}

// Type describes a registered check type.
type Type struct {
	Name    string
	Summary string
	// New returns a pointer to a spec struct populated with defaults.
	New func() Checker
	// Positional is the key filled by `medic check <type> <value>`.
	Positional string
	// Example is a YAML snippet of one check (the value under its id).
	Example string
}

var registry = map[string]*Type{}

// Register adds a check type. Called from init() in the checks package.
func Register(t *Type) {
	if _, dup := registry[t.Name]; dup {
		panic("check: duplicate type " + t.Name)
	}
	registry[t.Name] = t
}

// Lookup returns a registered type.
func Lookup(name string) (*Type, bool) {
	t, ok := registry[name]
	return t, ok
}

// Types returns all registered types sorted by name.
func Types() []*Type {
	out := make([]*Type, 0, len(registry))
	for _, t := range registry {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Names returns all registered type names, sorted.
func Names() []string {
	var out []string
	for _, t := range Types() {
		out = append(out, t.Name)
	}
	return out
}

// Retry controls re-attempts of a failing check.
type Retry struct {
	Until    *units.Duration `yaml:"until" doc:"Keep retrying until this much time has passed since the first attempt."`
	Attempts int             `yaml:"attempts" doc:"Maximum number of attempts in total (1 = no retry)."`
	Interval units.Duration  `yaml:"interval" doc:"Pause between attempts (a small random jitter is added)."`
}

// Validate implements spec.Validator.
func (r *Retry) Validate() error {
	if r.Until != nil && r.Attempts > 0 {
		return fmt.Errorf("retry: set either 'until' or 'attempts', not both")
	}
	if r.Until == nil && r.Attempts == 0 {
		return fmt.Errorf("retry: set 'until' (e.g. 60s) or 'attempts' (e.g. 3)")
	}
	if r.Attempts < 0 {
		return fmt.Errorf("retry.attempts must be at least 1")
	}
	return nil
}

// Common holds the keys every check accepts.
type Common struct {
	Type         string          `yaml:"type" required:"true" doc:"Check type."`
	Name         string          `yaml:"name" doc:"Display name (defaults to the check id)."`
	DependsOn    []string        `yaml:"depends_on" doc:"Ids of checks that must pass (or warn) before this one starts. If any of them fails or is skipped, this check is skipped."`
	Tags         []string        `yaml:"tags" doc:"Labels for selecting checks with --only/--skip tag:NAME."`
	Timeout      *units.Duration `yaml:"timeout" doc:"Time limit for a single attempt (default: defaults.timeout)."`
	Retry        *Retry          `yaml:"retry" doc:"Retry a failing check, e.g. {until: 60s, interval: 2s} or {attempts: 3, interval: 1s}."`
	AllowFailure bool            `yaml:"allow_failure" doc:"Report a failure as a warning so it never fails the run; dependents still run."`
}

// Spec is one decoded check from the config.
type Spec struct {
	ID string
	Common
	Checker Checker
	Pos     spec.Pos
}

// DisplayName returns name or id.
func (s *Spec) DisplayName() string {
	if s.Name != "" {
		return s.Name
	}
	return s.ID
}

// Threshold is a {warn, fail} pair of a quantity kind.
type Threshold[T any, PT interface {
	*T
	units.Quantity
}] struct {
	Warn *T `yaml:"warn" doc:"Report a warning past this value."`
	Fail *T `yaml:"fail" doc:"Report a failure past this value."`
}

// TH builds a threshold from quantity strings ("" = unset). Panics on bad input (code defaults only).
func TH[T any, PT interface {
	*T
	units.Quantity
}](warn, fail string) Threshold[T, PT] {
	var t Threshold[T, PT]
	if warn != "" {
		t.Warn = new(T)
		if err := PT(t.Warn).Set(warn); err != nil {
			panic(err)
		}
	}
	if fail != "" {
		t.Fail = new(T)
		if err := PT(t.Fail).Set(fail); err != nil {
			panic(err)
		}
	}
	return t
}

func (t Threshold[T, PT]) TypeName() string {
	var z T
	return "{warn, fail} " + PT(&z).Kind()
}

func (t Threshold[T, PT]) String() string {
	var parts []string
	if t.Warn != nil {
		parts = append(parts, "warn: "+PT(t.Warn).String())
	}
	if t.Fail != nil {
		parts = append(parts, "fail: "+PT(t.Fail).String())
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// IsSet reports whether any bound is configured.
func (t Threshold[T, PT]) IsSet() bool { return t.Warn != nil || t.Fail != nil }

// JSONSchema implements spec.SchemaProvider.
func (t Threshold[T, PT]) JSONSchema(g *spec.SchemaGen) map[string]any {
	q := g.For(reflect.TypeOf((*T)(nil)).Elem())
	return map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"warn": q, "fail": q},
		"additionalProperties": false,
		"minProperties":        1,
	}
}

// Validate implements spec.Validator.
func (t *Threshold[T, PT]) Validate() error {
	if !t.IsSet() {
		return fmt.Errorf("set at least one of 'warn' or 'fail'")
	}
	return nil
}

// CheckDirection implements spec.DirectionChecker.
func (t *Threshold[T, PT]) CheckDirection(dir string) error {
	if t.Warn == nil || t.Fail == nil {
		return nil
	}
	w, f := PT(t.Warn).Float(), PT(t.Fail).Float()
	if dir == "above" && w > f {
		return fmt.Errorf("warn (%s) must not be above fail (%s): higher values are worse", PT(t.Warn), PT(t.Fail))
	}
	if dir == "below" && w < f {
		return fmt.Errorf("warn (%s) must not be below fail (%s): lower values are worse", PT(t.Warn), PT(t.Fail))
	}
	return nil
}

// Above grades v where higher values are worse.
func (t Threshold[T, PT]) Above(v float64) Status {
	if t.Fail != nil && v > PT(t.Fail).Float() {
		return Failed
	}
	if t.Warn != nil && v > PT(t.Warn).Float() {
		return Warning
	}
	return Passed
}

// Below grades v where lower values are worse.
func (t Threshold[T, PT]) Below(v float64) Status {
	if t.Fail != nil && v < PT(t.Fail).Float() {
		return Failed
	}
	if t.Warn != nil && v < PT(t.Warn).Float() {
		return Warning
	}
	return Passed
}

// Threshold aliases for each quantity kind.
type (
	DurationTH = Threshold[units.Duration, *units.Duration]
	PercentTH  = Threshold[units.Percent, *units.Percent]
	SizeTH     = Threshold[units.Size, *units.Size]
	DaysTH     = Threshold[units.Days, *units.Days]
)
