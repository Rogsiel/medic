// Package cli wires medic's commands.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Rogsiel/medic/internal/check"
	_ "github.com/Rogsiel/medic/internal/checks" // register check types
	"github.com/Rogsiel/medic/internal/config"
	"github.com/Rogsiel/medic/internal/docs"
	"github.com/Rogsiel/medic/internal/output"
	"github.com/Rogsiel/medic/internal/runner"
	"github.com/Rogsiel/medic/internal/spec"
	"github.com/Rogsiel/medic/internal/units"
)

// Exit codes.
const (
	ExitOK        = 0
	ExitUnhealthy = 1
	ExitUsage     = 2
	ExitInternal  = 3
)

// exitError carries an exit code out of a command.
type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }

func usageErr(format string, args ...any) error {
	return &exitError{code: ExitUsage, msg: fmt.Sprintf(format, args...)}
}

// durationFlag is a pflag.Value for unit-suffixed durations.
type durationFlag struct{ d *units.Duration }

func (f durationFlag) String() string {
	if f.d == nil || f.d.D == 0 {
		return ""
	}
	return f.d.String()
}
func (f durationFlag) Set(s string) error { return f.d.Set(s) }
func (f durationFlag) Type() string       { return "duration" }

type runFlags struct {
	only, skip []string
	wait       units.Duration
	watch      units.Duration
	failOn     string
	outputs    []string
	ci         string
	jobs       int
	quiet      bool
	verbose    bool
	noColor    bool
}

func (rf *runFlags) register(cmd *cobra.Command, selection bool) {
	f := cmd.Flags()
	if selection {
		f.StringSliceVar(&rf.only, "only", nil, "run only these check ids or tag:NAME (plus their dependencies)")
		f.StringSliceVar(&rf.skip, "skip", nil, "skip these check ids or tag:NAME (dependents are skipped too)")
		f.Var(durationFlag{&rf.watch}, "watch", "re-run every interval (e.g. 10s) until Ctrl-C; retries are disabled")
	}
	f.Var(durationFlag{&rf.wait}, "wait", "retry failing checks for up to this long (e.g. 2m) unless they set their own retry")
	f.StringVar(&rf.failOn, "fail-on", "", "what fails the run: failure or warning (overrides fail_on)")
	f.StringArrayVarP(&rf.outputs, "output", "o", nil, "write a report: FORMAT=PATH, PATH '-' = stdout; formats: "+strings.Join(output.Formats, ", ")+" (repeatable)")
	f.StringVar(&rf.ci, "ci", "auto", "CI integration (GitHub annotations + step summary): auto or off")
	f.IntVarP(&rf.jobs, "jobs", "j", 0, "max checks running at once (overrides concurrency)")
	f.BoolVarP(&rf.quiet, "quiet", "q", false, "no human-readable output (exit code and --output only)")
	f.BoolVarP(&rf.verbose, "verbose", "v", false, "show details for every check and each retry")
	f.BoolVar(&rf.noColor, "no-color", false, "disable colour (also NO_COLOR=1)")
}

// Execute runs the CLI and returns the process exit code.
func Execute(version string, args []string, stdin io.Reader, stdout, stderr *os.File) int {
	root := newRoot(version, stdin, stdout, stderr)
	root.SetArgs(args)
	err := root.Execute()
	if err == nil {
		return ExitOK
	}
	var ee *exitError
	if errors.As(err, &ee) {
		if ee.msg != "" {
			fmt.Fprintln(stderr, "medic: "+ee.msg)
		}
		return ee.code
	}
	fmt.Fprintln(stderr, "medic: "+err.Error())
	return ExitUsage
}

func newRoot(version string, stdin io.Reader, stdout, stderr *os.File) *cobra.Command {
	var configPath string
	rf := &runFlags{}
	root := &cobra.Command{
		Use:   "medic",
		Short: "Health checks for pipelines: gate deploys, smoke-test them, wait for dependencies.",
		Long: `medic runs the health checks declared in medic.yaml and exits 0 (healthy),
1 (unhealthy), 2 (config/usage error) or 3 (internal error).

Start with 'medic init', check the file with 'medic validate', then run 'medic'.`,
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runConfig(cmd.Context(), version, configPath, rf, stdin, stdout, stderr)
		},
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageErr("%v", err) })
	root.PersistentFlags().StringVarP(&configPath, "config", "c", "", "config file (default: medic.yaml or medic.yml; '-' = stdin)")
	rf.register(root, true)

	runCmd := &cobra.Command{
		Use:   "run",
		Short: "Run the checks in the config (the default command)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runConfig(cmd.Context(), version, configPath, rf, stdin, stdout, stderr)
		},
	}
	rf.register(runCmd, true)

	root.AddCommand(runCmd,
		newValidate(&configPath, stdin, stdout),
		newList(&configPath, stdin, stdout),
		newSchema(stdout),
		newInit(stdout),
		newExplain(stdout),
		newCheck(version, stdout, stderr),
		newGenDocs(),
	)
	return root
}

// ---- config loading ---------------------------------------------------------

func readConfig(path string, stdin io.Reader) (string, []byte, error) {
	if path == "" {
		p, err := config.Discover(".")
		if err != nil {
			return "", nil, usageErr("%v", err)
		}
		path = p
	}
	var src []byte
	var err error
	if path == "-" {
		src, err = io.ReadAll(stdin)
		path = "<stdin>"
	} else {
		src, err = os.ReadFile(path)
	}
	if err != nil {
		return "", nil, usageErr("cannot read config: %v", err)
	}
	return path, src, nil
}

func loadConfig(path string, stdin io.Reader) (*config.Config, error) {
	name, src, err := readConfig(path, stdin)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(name, src, config.Options{})
	if err != nil {
		return nil, configErr(name, err)
	}
	return cfg, nil
}

func configErr(name string, err error) error {
	es, ok := spec.AsErrors(err)
	if !ok {
		return usageErr("%s: %v", name, err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "invalid config %s (%d error", name, len(es))
	if len(es) != 1 {
		b.WriteString("s")
	}
	b.WriteString("):")
	for _, e := range es {
		b.WriteString("\n  " + name)
		if e.Pos.Line > 0 {
			fmt.Fprintf(&b, ":%d:%d", e.Pos.Line, e.Pos.Col)
		}
		b.WriteString(": ")
		if e.Path != "" {
			b.WriteString(e.Path + ": ")
		}
		b.WriteString(e.Msg)
	}
	return &exitError{code: ExitUsage, msg: b.String()}
}

// ---- run --------------------------------------------------------------------

type sink struct {
	format string
	path   string
}

func parseOutputs(specs []string) ([]sink, bool, error) {
	var out []sink
	stdoutUsed := false
	for _, s := range specs {
		f, p, ok := strings.Cut(s, "=")
		if !ok || p == "" {
			return nil, false, usageErr("--output %q: expected FORMAT=PATH (e.g. junit=medic.xml or json=-)", s)
		}
		valid := false
		for _, x := range output.Formats {
			valid = valid || x == f
		}
		if !valid {
			return nil, false, usageErr("--output %q: unknown format %q (use %s)", s, f, strings.Join(output.Formats, ", "))
		}
		if p == "-" {
			if stdoutUsed {
				return nil, false, usageErr("--output: only one report can go to stdout")
			}
			stdoutUsed = true
		}
		out = append(out, sink{f, p})
	}
	return out, stdoutUsed, nil
}

type session struct {
	version string
	cfg     *config.Config
	specs   []*check.Spec
	skip    map[string]bool
	rf      *runFlags
	sinks   []sink
	human   *os.File // nil when quiet
	stdout  *os.File
	ci      output.CI
	meta    output.Meta
	color   bool
}

func newSession(version string, cfg *config.Config, rf *runFlags, stdout, stderr *os.File) (*session, error) {
	specs, skip, err := runner.Select(cfg.Checks, rf.only, rf.skip)
	if err != nil {
		return nil, usageErr("%v", err)
	}
	failOn := cfg.FailOn
	if rf.failOn != "" {
		if rf.failOn != "failure" && rf.failOn != "warning" {
			return nil, usageErr("--fail-on must be failure or warning")
		}
		failOn = rf.failOn
	}
	if rf.ci != "auto" && rf.ci != "off" {
		return nil, usageErr("--ci must be auto or off")
	}
	if rf.jobs < 0 {
		return nil, usageErr("--jobs must be at least 1")
	}
	sinks, stdoutUsed, err := parseOutputs(rf.outputs)
	if err != nil {
		return nil, err
	}
	s := &session{version: version, cfg: cfg, specs: specs, skip: skip, rf: rf, sinks: sinks, stdout: stdout,
		meta: output.Meta{Version: version, FailOn: failOn}}
	if !rf.quiet {
		s.human = stdout
		if stdoutUsed {
			s.human = stderr
		}
		s.color = !rf.noColor && output.UseColor(s.human)
	}
	if rf.ci == "auto" {
		s.ci = output.DetectCI()
	}
	return s, nil
}

func (s *session) options(mask *runner.Masker, noRetry bool) runner.Options {
	conc := s.cfg.Concurrency
	if s.rf.jobs > 0 {
		conc = s.rf.jobs
	}
	waiting := map[string]bool{}
	return runner.Options{
		Concurrency: conc,
		Wait:        s.rf.wait.D,
		NoRetry:     noRetry,
		Skip:        s.skip,
		Mask:        mask,
		OnEvent: func(e runner.Event) {
			if s.human == nil || e.Kind != runner.Retrying {
				return
			}
			if s.rf.verbose || !waiting[e.ID] {
				waiting[e.ID] = true
				fmt.Fprintf(s.human, "… %s: attempt %d %s: %s; retrying\n", e.ID, e.Attempt, e.Last.Status, mask.String(e.Last.Message))
			}
		},
	}
}

func (s *session) emit(rep *runner.Report) error {
	if s.human != nil {
		output.Human(s.human, rep, s.meta, output.HumanOptions{Color: s.color, Verbose: s.rf.verbose})
	}
	for _, k := range s.sinks {
		if err := writeSink(k, rep, s.meta, s.stdout); err != nil {
			return &exitError{code: ExitInternal, msg: err.Error()}
		}
	}
	if s.ci == output.GitHub {
		w := s.stdout
		if s.human != nil && s.human != s.stdout {
			w = s.human
		}
		output.GitHubAnnotations(w, rep)
		if err := output.GitHubSummary(rep, s.meta); err != nil && s.human != nil {
			fmt.Fprintf(s.human, "medic: cannot write step summary: %v\n", err)
		}
	}
	return nil
}

func writeSink(k sink, rep *runner.Report, meta output.Meta, stdout *os.File) error {
	if k.path == "-" {
		return output.Write(stdout, k.format, rep, meta)
	}
	if dir := filepath.Dir(k.path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("--output %s: %v", k.path, err)
		}
	}
	tmp := k.path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("--output %s: %v", k.path, err)
	}
	if err := output.Write(f, k.format, rep, meta); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, k.path)
}

func signalContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
}

func runConfig(ctx context.Context, version, path string, rf *runFlags, stdin io.Reader, stdout, stderr *os.File) error {
	cfg, err := loadConfig(path, stdin)
	if err != nil {
		return err
	}
	return runSession(ctx, version, cfg, rf, stdout, stderr)
}

func runSession(ctx context.Context, version string, cfg *config.Config, rf *runFlags, stdout, stderr *os.File) error {
	s, err := newSession(version, cfg, rf, stdout, stderr)
	if err != nil {
		return err
	}
	ctx, cancel := signalContext(ctx)
	defer cancel()
	mask := runner.NewMasker(cfg.SecretValues)

	if rf.watch.D > 0 {
		return s.watch(ctx, mask)
	}
	rep := runner.Run(ctx, s.specs, s.options(mask, false))
	if err := s.emit(rep); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return &exitError{code: ExitUnhealthy, msg: "interrupted"}
	}
	if code := rep.ExitCode(s.meta.FailOn); code != 0 {
		return &exitError{code: code}
	}
	return nil
}

func (s *session) watch(ctx context.Context, mask *runner.Masker) error {
	prev := map[string]check.Status{}
	code := 0
	interactive := s.human != nil && s.human == s.stdout && s.color
	for {
		rep := runner.Run(ctx, s.specs, s.options(mask, true))
		if ctx.Err() != nil {
			break
		}
		code = rep.ExitCode(s.meta.FailOn)
		if interactive {
			fmt.Fprint(s.human, "\x1b[H\x1b[2J")
		}
		if err := s.emit(rep); err != nil {
			return err
		}
		for _, r := range rep.Results {
			if old, ok := prev[r.ID]; ok && old != r.Status {
				fmt.Fprintf(os.Stderr, "[%s] %s: %s -> %s (%s)\n", time.Now().Format("15:04:05"), r.ID, old, r.Status, r.Message)
			}
			prev[r.ID] = r.Status
		}
		if s.human != nil {
			fmt.Fprintf(s.human, "watching every %s; Ctrl-C to stop\n", s.rf.watch)
		}
		select {
		case <-time.After(s.rf.watch.D):
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
	}
	if code != 0 {
		return &exitError{code: code}
	}
	return nil
}

// ---- validate / list --------------------------------------------------------

func newValidate(configPath *string, stdin io.Reader, stdout *os.File) *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Check the config without running anything",
		Long:  "Parse and validate the config exactly like a run would (types, units, env vars, dependencies), without executing checks.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if format != "text" && format != "json" {
				return usageErr("--format must be text or json")
			}
			name, src, err := readConfig(*configPath, stdin)
			if err != nil {
				return err
			}
			cfg, lerr := config.Load(name, src, config.Options{})
			if format == "json" {
				type res struct {
					Valid  bool         `json:"valid"`
					File   string       `json:"file"`
					Checks int          `json:"checks"`
					Errors []spec.Error `json:"errors"`
				}
				r := res{Valid: lerr == nil, File: name, Errors: []spec.Error{}}
				if lerr != nil {
					es, ok := spec.AsErrors(lerr)
					if !ok {
						es = spec.Errors{{Msg: lerr.Error()}}
					}
					for _, e := range es {
						e.Line, e.Col = e.Pos.Line, e.Pos.Col
						r.Errors = append(r.Errors, e)
					}
				} else {
					r.Checks = len(cfg.Checks)
				}
				enc := json.NewEncoder(stdout)
				enc.SetIndent("", "  ")
				_ = enc.Encode(r)
				if lerr != nil {
					return &exitError{code: ExitUsage}
				}
				return nil
			}
			if lerr != nil {
				return configErr(name, lerr)
			}
			fmt.Fprintf(stdout, "%s: valid (%d checks)\n", name, len(cfg.Checks))
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "text", "text or json")
	return cmd
}

func newList(configPath *string, stdin io.Reader, stdout *os.File) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the checks in the config",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(*configPath, stdin)
			if err != nil {
				return err
			}
			mask := runner.NewMasker(cfg.SecretValues)
			type item struct {
				ID        string   `json:"id"`
				Type      string   `json:"type"`
				Target    string   `json:"target"`
				DependsOn []string `json:"depends_on"`
				Tags      []string `json:"tags"`
			}
			var items []item
			for _, s := range cfg.Checks {
				items = append(items, item{s.ID, s.Type, mask.String(s.Checker.Target()), nonNil(s.DependsOn), nonNil(s.Tags)})
			}
			if asJSON {
				enc := json.NewEncoder(stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(items)
			}
			w := 2
			for _, it := range items {
				w = max(w, len(it.ID))
			}
			for _, it := range items {
				extra := ""
				if len(it.DependsOn) > 0 {
					extra += "  needs: " + strings.Join(it.DependsOn, ",")
				}
				if len(it.Tags) > 0 {
					extra += "  tags: " + strings.Join(it.Tags, ",")
				}
				fmt.Fprintf(stdout, "%-*s  %-6s  %s%s\n", w, it.ID, it.Type, it.Target, extra)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ---- schema / init / explain / gen-docs -------------------------------------

func newSchema(stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:       "schema [config|report|exec-output]",
		Short:     "Print a JSON Schema (config by default)",
		Long:      "Print the JSON Schema of the config file, the JSON report (--output json), or the result an 'output: json' exec check prints.",
		Args:      cobra.MatchAll(cobra.MaximumNArgs(1), cobra.OnlyValidArgs),
		ValidArgs: []string{"config", "report", "exec-output"},
		RunE: func(cmd *cobra.Command, args []string) error {
			which := "config"
			if len(args) == 1 {
				which = args[0]
			}
			s := map[string]func() map[string]any{"config": docs.ConfigSchema, "report": docs.ReportSchema, "exec-output": docs.ExecOutputSchema}[which]
			_, err := stdout.Write(docs.MarshalSchema(s()))
			return err
		},
	}
}

func newInit(stdout io.Writer) *cobra.Command {
	var force bool
	return withFlags(&cobra.Command{
		Use:   "init [FILE]",
		Short: "Write a starter medic.yaml ('-' prints it)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "medic.yaml"
			if len(args) == 1 {
				path = args[0]
			}
			tmpl := docs.InitTemplate()
			if path == "-" {
				_, err := io.WriteString(stdout, tmpl)
				return err
			}
			if _, err := os.Stat(path); err == nil && !force {
				return usageErr("%s already exists (use --force to overwrite)", path)
			}
			if err := os.WriteFile(path, []byte(tmpl), 0o644); err != nil {
				return usageErr("%v", err)
			}
			fmt.Fprintf(stdout, "wrote %s; edit it, then run: medic validate && medic\n", path)
			return nil
		},
	}, func(c *cobra.Command) { c.Flags().BoolVarP(&force, "force", "f", false, "overwrite an existing file") })
}

func withFlags(c *cobra.Command, f func(*cobra.Command)) *cobra.Command {
	f(c)
	return c
}

func newExplain(stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:       "explain [TYPE]",
		Short:     "Show the reference for a check type (or list all types)",
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: check.Names(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				fmt.Fprint(stdout, docs.ConfigMarkdown())
				return nil
			}
			t, ok := check.Lookup(args[0])
			if !ok {
				msg := fmt.Sprintf("unknown check type %q", args[0])
				if s := spec.Suggest(args[0], check.Names()); s != "" {
					msg += fmt.Sprintf(" (did you mean %q?)", s)
				}
				return usageErr("%s; available: %s", msg, strings.Join(check.Names(), ", "))
			}
			fmt.Fprint(stdout, docs.TypeMarkdown(t))
			return nil
		},
	}
}

func newGenDocs() *cobra.Command {
	return &cobra.Command{
		Use:    "gen-docs DIR",
		Short:  "Regenerate docs/ and schema/ (development)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := args[0]
			files := map[string][]byte{
				"docs/config.md":                 []byte(docs.ConfigMarkdown()),
				"schema/medic.schema.json":       docs.MarshalSchema(docs.ConfigSchema()),
				"schema/report.schema.json":      docs.MarshalSchema(docs.ReportSchema()),
				"schema/exec-output.schema.json": docs.MarshalSchema(docs.ExecOutputSchema()),
				"docs/medic.example.yaml":        []byte(docs.InitTemplate()),
			}
			for _, t := range check.Types() {
				files["docs/checks/"+t.Name+".md"] = []byte("<!-- Generated by `medic gen-docs`. Do not edit. -->\n\n" + docs.TypeMarkdown(t))
			}
			names := make([]string, 0, len(files))
			for n := range files {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				p := filepath.Join(root, n)
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					return err
				}
				if err := os.WriteFile(p, files[n], 0o644); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

// ---- check one-liner ---------------------------------------------------------

func newCheck(version string, stdout, stderr *os.File) *cobra.Command {
	rf := &runFlags{}
	var sets []string
	cmd := &cobra.Command{
		Use:   "check TYPE [VALUE] [--set key=value ...]",
		Short: "Run a single check without a config file",
		Long: `Run one check from the command line. VALUE fills the type's main key
(url for http, target for tcp, ...). --set takes the same keys as the config:

  medic check http https://api/health --set expect.status=[200] --wait 60s
  medic check tcp db:5432 --wait 2m
  medic check exec 'pg_isready -h db' --set timeout=10s
  medic check disk / --set expect.used='{warn: 80%, fail: 90%}'`,
		Args:      cobra.RangeArgs(1, 2),
		ValidArgs: check.Names(),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := oneLinerConfig(args, sets)
			if err != nil {
				return err
			}
			return runSession(cmd.Context(), version, cfg, rf, stdout, stderr)
		},
	}
	cmd.Flags().StringArrayVar(&sets, "set", nil, "set a config key, e.g. expect.status=[200,204] or timeout=10s (repeatable)")
	rf.register(cmd, false)
	return cmd
}

func oneLinerConfig(args, sets []string) (*config.Config, error) {
	typ, ok := check.Lookup(args[0])
	if !ok {
		msg := fmt.Sprintf("unknown check type %q", args[0])
		if s := spec.Suggest(args[0], check.Names()); s != "" {
			msg += fmt.Sprintf(" (did you mean %q?)", s)
		}
		return nil, usageErr("%s; available: %s", msg, strings.Join(check.Names(), ", "))
	}
	node := &spec.Node{Kind: spec.Map}
	node.Set("type", &spec.Node{Kind: spec.Scalar, Value: typ.Name})
	if len(args) == 2 {
		if typ.Positional == "" {
			return nil, usageErr("check type %s takes no VALUE; use --set", typ.Name)
		}
		node.Set(typ.Positional, &spec.Node{Kind: spec.Scalar, Value: args[1], Quoted: true})
	}
	for _, s := range sets {
		k, v, ok := strings.Cut(s, "=")
		if !ok || k == "" {
			return nil, usageErr("--set %q: expected key=value", s)
		}
		val, err := spec.ParseValue(v)
		if err != nil {
			return nil, usageErr("--set %s: %v", k, err)
		}
		setPath(node, strings.Split(k, "."), val)
	}
	d := &spec.Decoder{Interp: func(s string) (string, error) { return config.Interpolate(s, os.LookupEnv) }}
	s := config.DecodeCheck(d, typ.Name, node, typ.Name)
	if err := d.Err(); err != nil {
		es, _ := spec.AsErrors(err)
		var lines []string
		for _, e := range es {
			e.Pos = spec.Pos{}
			lines = append(lines, e.Error())
		}
		return nil, usageErr("%s", strings.Join(lines, "\n  "))
	}
	if s.Timeout == nil {
		t := units.Dur(5 * time.Second)
		s.Timeout = &t
	}
	if len(s.DependsOn) > 0 {
		return nil, usageErr("depends_on is not available for a single check")
	}
	return &config.Config{File: config.File{Version: 1, Concurrency: 1, FailOn: "failure", Checks: config.Checks{s}}}, nil
}

func setPath(n *spec.Node, path []string, val *spec.Node) {
	if len(path) == 1 {
		n.Set(path[0], val)
		return
	}
	child := n.Get(path[0])
	if child == nil || child.Kind != spec.Map {
		child = &spec.Node{Kind: spec.Map}
		n.Set(path[0], child)
	}
	setPath(child, path[1:], val)
}
