package checks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/Rogsiel/medic/internal/check"
)

func init() {
	check.Register(&check.Type{
		Name:       "exec",
		Summary:    "Run a command and judge it by exit code and output, or let it report its own result as JSON.",
		New:        func() check.Checker { return &Exec{Output: "text"} },
		Positional: "shell",
		Example: `type: exec
command: [pg_isready, -h, db.internal]
expect:
  exit: 0`,
	})
}

const outputTail = 2000

// Exec runs a command.
type Exec struct {
	Command []string          `yaml:"command" doc:"Program and arguments, run directly without a shell, e.g. [pg_isready, -h, db]. Set exactly one of command or shell."`
	Shell   string            `yaml:"shell" doc:"Command line run by sh -c (cmd /c on Windows), for pipes and redirects. Set exactly one of command or shell."`
	Env     map[string]string `yaml:"env" doc:"Extra environment variables for the command."`
	Cwd     string            `yaml:"cwd" doc:"Working directory."`
	Output  string            `yaml:"output" enum:"text,json" doc:"text: judge by exit code and expect. json: the command prints {\"status\": \"passed|warning|failed\", \"message\": ..., \"details\": {...}} on stdout."`
	Expect  ExecExpect        `yaml:"expect" doc:"What counts as healthy (output: text only)."`
}

// ExecExpect are exec expectations.
type ExecExpect struct {
	Exit           int    `yaml:"exit" doc:"Required exit code."`
	StdoutContains string `yaml:"stdout_contains" doc:"Text stdout must contain."`
}

// PluginResult is the JSON a command prints with output: json.
type PluginResult struct {
	Status  string         `json:"status"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func (c *Exec) Target() string {
	if c.Shell != "" {
		return c.Shell
	}
	return strings.Join(c.Command, " ")
}

func (c *Exec) Validate() error {
	switch {
	case len(c.Command) == 0 && c.Shell == "":
		return fmt.Errorf("set 'command' (a list, run without a shell) or 'shell' (a string, run by sh -c)")
	case len(c.Command) > 0 && c.Shell != "":
		return fmt.Errorf("set only one of 'command' or 'shell'")
	case len(c.Command) > 0 && c.Command[0] == "":
		return fmt.Errorf("command: the program name is empty")
	case c.Output == "json" && (c.Expect.Exit != 0 || c.Expect.StdoutContains != ""):
		return fmt.Errorf("expect does not apply with output: json (the command reports its own status)")
	}
	return nil
}

func (c *Exec) Run(ctx context.Context) check.Result {
	var cmd *exec.Cmd
	switch {
	case c.Shell != "" && runtime.GOOS == "windows":
		cmd = exec.CommandContext(ctx, "cmd", "/c", c.Shell)
	case c.Shell != "":
		cmd = exec.CommandContext(ctx, "sh", "-c", c.Shell)
	default:
		cmd = exec.CommandContext(ctx, c.Command[0], c.Command[1:]...)
	}
	cmd.Dir = c.Cwd
	cmd.Env = os.Environ()
	for _, k := range sortedKeys(c.Env) {
		cmd.Env = append(cmd.Env, k+"="+c.Env[k])
	}
	setProcessGroup(cmd)
	cmd.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start)
	out, errOut := stdout.String(), stderr.String()
	details := map[string]any{"elapsed_ms": ms(elapsed), "stdout": tail(out, outputTail), "stderr": tail(errOut, outputTail)}

	if ctx.Err() != nil {
		return failed("command timed out after "+dur(elapsed), details, "The command did not finish within the check timeout.")
	}
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			return failed("could not run command: "+err.Error(), details, "Check the program exists and is on PATH.")
		}
		code = ee.ExitCode()
	}
	details["exit_code"] = code

	if c.Output == "json" {
		return c.pluginResult(out, code, details)
	}
	if code != c.Expect.Exit {
		msg := fmt.Sprintf("exit code %d (expected %d)", code, c.Expect.Exit)
		if l := lastLine(errOut, out); l != "" {
			msg += ": " + l
		}
		return failed(msg, details, "")
	}
	if c.Expect.StdoutContains != "" && !strings.Contains(out, c.Expect.StdoutContains) {
		return failed(fmt.Sprintf("stdout does not contain %q", c.Expect.StdoutContains), details, "")
	}
	return result(check.Passed, fmt.Sprintf("exit code %d in %s", code, dur(elapsed)), details)
}

func (c *Exec) pluginResult(out string, code int, details map[string]any) check.Result {
	var pr PluginResult
	dec := json.NewDecoder(strings.NewReader(strings.TrimSpace(out)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&pr); err != nil {
		if code != 0 {
			return failed(fmt.Sprintf("exit code %d and no valid JSON result", code), details, "")
		}
		return failed("invalid JSON result: "+err.Error(), details, "Print one JSON object: {\"status\": \"passed|warning|failed\", \"message\": \"...\"}.")
	}
	st := check.Status(pr.Status)
	if st != check.Passed && st != check.Warning && st != check.Failed {
		return failed(fmt.Sprintf("invalid status %q in JSON result (passed, warning or failed)", pr.Status), details, "")
	}
	delete(details, "stdout")
	for k, v := range pr.Details {
		details[k] = v
	}
	msg := pr.Message
	if msg == "" {
		msg = string(st)
	}
	return result(st, msg, details)
}

func lastLine(texts ...string) string {
	for _, t := range texts {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		lines := strings.Split(t, "\n")
		return firstLine(lines[len(lines)-1], 200)
	}
	return ""
}
