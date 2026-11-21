// Package config loads and validates medic.yaml.
package config

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Rogsiel/medic/internal/check"
	"github.com/Rogsiel/medic/internal/spec"
	"github.com/Rogsiel/medic/internal/units"
)

// Version is the only supported config format version.
const Version = 1

// DefaultFiles are looked up in the working directory when -c is not given.
var DefaultFiles = []string{"medic.yaml", "medic.yml"}

// IDPattern is the allowed shape of a check id.
const IDPattern = `^[a-z0-9][a-z0-9_-]*$`

var idRe = regexp.MustCompile(IDPattern)

// File is the top-level config document.
type File struct {
	Version     int      `yaml:"version" required:"true" doc:"Config format version. Must be 1."`
	Secrets     []string `yaml:"secrets" doc:"Names of environment variables whose values are masked (***) in all output (values shorter than 4 characters are not masked)."`
	Defaults    Defaults `yaml:"defaults" doc:"Settings applied to every check that does not set its own."`
	Concurrency int      `yaml:"concurrency" doc:"Maximum number of checks running at the same time (default 8)."`
	FailOn      string   `yaml:"fail_on" enum:"failure,warning" doc:"What fails the run: 'failure' (default) or 'warning' (warnings fail too)."`
	Checks      Checks   `yaml:"checks" required:"true" doc:"Checks to run, keyed by id."`
}

// Defaults are per-check settings applied when a check omits them.
type Defaults struct {
	Timeout units.Duration `yaml:"timeout" doc:"Time limit for one attempt of a check (default 5s)."`
	Retry   *check.Retry   `yaml:"retry" doc:"Retry policy for checks without their own (default: no retry)."`
}

// Validate implements spec.Validator.
func (f *File) Validate() error {
	var errs []string
	if f.Version != 0 && f.Version != Version {
		errs = append(errs, fmt.Sprintf("unsupported config version %d (this medic supports version %d)", f.Version, Version))
	}
	if f.Concurrency < 0 {
		errs = append(errs, "concurrency must be at least 1")
	}
	envName := regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	for _, s := range f.Secrets {
		if !envName.MatchString(s) {
			errs = append(errs, fmt.Sprintf("secrets: %q is not a valid environment variable name", s))
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// Checks is the ordered set of checks under `checks:`.
type Checks []*check.Spec

// DecodeNode implements spec.NodeDecoder.
func (c *Checks) DecodeNode(d *spec.Decoder, n *spec.Node, path string) {
	if n.Kind != spec.Map {
		d.Errorf(n.Pos, path, "expected a mapping of check id -> check, got a %s", n.Kind)
		return
	}
	if len(n.Pairs) == 0 {
		d.Errorf(n.Pos, path, "no checks defined")
		return
	}
	for _, p := range n.Pairs {
		cp := path + "." + p.Key
		if !idRe.MatchString(p.Key) {
			d.Errorf(p.KeyPos, cp, "invalid check id %q: use lowercase letters, digits, '-' and '_' (must start with a letter or digit)", p.Key)
		}
		if s := DecodeCheck(d, p.Key, p.Value, cp); s != nil {
			s.Pos = p.KeyPos
			*c = append(*c, s)
		}
	}
}

// DecodeCheck decodes one check body. Returns nil if the type is unusable.
func DecodeCheck(d *spec.Decoder, id string, n *spec.Node, path string) *check.Spec {
	if n.Kind != spec.Map {
		d.Errorf(n.Pos, path, "expected a mapping with at least 'type', got a %s", n.Kind)
		return nil
	}
	tn := n.Get("type")
	if tn == nil {
		d.Errorf(n.Pos, path, "missing required key \"type\" (one of: %s)", strings.Join(check.Names(), ", "))
		return nil
	}
	name, ok := d.Scalar(tn, path+".type")
	if !ok {
		return nil
	}
	typ, ok := check.Lookup(name)
	if !ok {
		msg := fmt.Sprintf("unknown check type %q", name)
		if s := spec.Suggest(name, check.Names()); s != "" {
			msg += fmt.Sprintf(" (did you mean %q?)", s)
		}
		d.Errorf(tn.Pos, path+".type", "%s; available: %s", msg, strings.Join(check.Names(), ", "))
		return nil
	}
	s := &check.Spec{ID: id, Checker: typ.New()}
	d.DecodeStruct(n, path, reflect.ValueOf(&s.Common).Elem(), reflect.ValueOf(s.Checker).Elem())
	return s
}

// JSONSchema implements spec.SchemaProvider.
func (Checks) JSONSchema(g *spec.SchemaGen) map[string]any {
	var branches []any
	for _, t := range check.Types() {
		props, req := g.Props(reflect.TypeOf(check.Common{}), reflect.TypeOf(t.New()).Elem())
		props["type"] = map[string]any{"const": t.Name, "description": t.Summary}
		branches = append(branches, map[string]any{
			"if":   map[string]any{"properties": map[string]any{"type": map[string]any{"const": t.Name}}, "required": []string{"type"}},
			"then": spec.Object(props, req),
		})
	}
	return map[string]any{
		"type":          "object",
		"minProperties": 1,
		"propertyNames": map[string]any{"pattern": IDPattern},
		"additionalProperties": map[string]any{
			"type":     "object",
			"required": []string{"type"},
			"properties": map[string]any{
				"type": map[string]any{"enum": check.Names(), "description": "Check type."},
			},
			"allOf": branches,
		},
	}
}

// Config is a loaded, validated configuration.
type Config struct {
	File
	Path         string
	SecretValues []string
}

// Options control loading.
type Options struct {
	// LookupEnv resolves ${VAR}; defaults to os.LookupEnv.
	LookupEnv func(string) (string, bool)
}

// Discover finds the default config file in dir.
func Discover(dir string) (string, error) {
	var found []string
	for _, name := range DefaultFiles {
		p := name
		if dir != "" && dir != "." {
			p = dir + string(os.PathSeparator) + name
		}
		if _, err := os.Stat(p); err == nil {
			found = append(found, p)
		}
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("no config found: create medic.yaml (medic init) or pass -c FILE")
	case 1:
		return found[0], nil
	}
	return "", fmt.Errorf("both %s exist; keep one or pass -c FILE", strings.Join(found, " and "))
}

// Load parses and validates a config document.
func Load(path string, src []byte, opts Options) (*Config, error) {
	lookup := opts.LookupEnv
	if lookup == nil {
		lookup = os.LookupEnv
	}
	root, err := spec.Parse(src)
	if err != nil {
		return nil, err
	}
	if root.Kind == spec.Null {
		return nil, spec.Error{Pos: spec.Pos{Line: 1, Col: 1}, Msg: "config is empty (start with: version: 1)"}
	}
	d := &spec.Decoder{Interp: func(s string) (string, error) { return Interpolate(s, lookup) }}
	cfg := &Config{Path: path}
	d.DecodeStruct(root, "", reflect.ValueOf(&cfg.File).Elem())
	if root.Kind == spec.Map && root.Get("version") != nil && root.Get("checks") != nil {
		checkGraph(d, cfg.Checks)
	}
	if err := d.Err(); err != nil {
		return nil, err
	}
	cfg.applyDefaults()
	for _, name := range cfg.Secrets {
		if v, ok := lookup(name); ok && v != "" {
			cfg.SecretValues = append(cfg.SecretValues, v)
		}
	}
	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Defaults.Timeout.D == 0 {
		c.Defaults.Timeout = units.Dur(5 * time.Second)
	}
	if c.Concurrency == 0 {
		c.Concurrency = 8
	}
	if c.FailOn == "" {
		c.FailOn = "failure"
	}
	for _, s := range c.Checks {
		if s.Timeout == nil {
			t := c.Defaults.Timeout
			s.Timeout = &t
		}
		if s.Retry == nil && c.Defaults.Retry != nil {
			r := *c.Defaults.Retry
			s.Retry = &r
		}
	}
}

// checkGraph validates depends_on references and rejects cycles.
func checkGraph(d *spec.Decoder, specs []*check.Spec) {
	byID := map[string]*check.Spec{}
	var ids []string
	for _, s := range specs {
		byID[s.ID] = s
		ids = append(ids, s.ID)
	}
	for _, s := range specs {
		seen := map[string]bool{}
		for _, dep := range s.DependsOn {
			path := "checks." + s.ID + ".depends_on"
			switch {
			case dep == s.ID:
				d.Errorf(s.Pos, path, "a check cannot depend on itself")
			case byID[dep] == nil:
				msg := fmt.Sprintf("unknown check id %q", dep)
				if sug := spec.Suggest(dep, ids); sug != "" {
					msg += fmt.Sprintf(" (did you mean %q?)", sug)
				}
				d.Errorf(s.Pos, path, "%s", msg)
			case seen[dep]:
				d.Errorf(s.Pos, path, "%q is listed twice", dep)
			}
			seen[dep] = true
		}
	}
	// Cycle detection (DFS, white/grey/black).
	state := map[string]int{}
	var stack []string
	reported := map[string]bool{}
	var visit func(id string)
	visit = func(id string) {
		state[id] = 1
		stack = append(stack, id)
		for _, dep := range byID[id].DependsOn {
			if byID[dep] == nil || dep == id {
				continue
			}
			switch state[dep] {
			case 0:
				visit(dep)
			case 1:
				i := indexOf(stack, dep)
				cycle := append(append([]string(nil), stack[i:]...), dep)
				key := canonical(cycle[:len(cycle)-1])
				if !reported[key] {
					reported[key] = true
					d.Errorf(byID[dep].Pos, "checks."+dep+".depends_on", "dependency cycle: %s", strings.Join(cycle, " -> "))
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[id] = 2
	}
	for _, id := range ids {
		if state[id] == 0 {
			visit(id)
		}
	}
}

func indexOf(xs []string, s string) int {
	for i, x := range xs {
		if x == s {
			return i
		}
	}
	return -1
}

func canonical(cycle []string) string {
	c := append([]string(nil), cycle...)
	sort.Strings(c)
	return strings.Join(c, ",")
}
