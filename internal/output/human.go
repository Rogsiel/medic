package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/Rogsiel/medic/internal/check"
	"github.com/Rogsiel/medic/internal/runner"
	"github.com/Rogsiel/medic/internal/units"
)

// HumanOptions control the human report.
type HumanOptions struct {
	Color   bool
	Verbose bool // show details for every check
}

// UseColor reports whether colour should be used on f.
func UseColor(f *os.File) bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	if os.Getenv("FORCE_COLOR") != "" || os.Getenv("GITHUB_ACTIONS") == "true" || os.Getenv("GITLAB_CI") == "true" {
		return true
	}
	return term.IsTerminal(int(f.Fd()))
}

type palette struct{ on bool }

func (p palette) wrap(code, s string) string {
	if !p.on {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (p palette) status(s check.Status) string {
	icon := map[check.Status]string{
		check.Passed: "✔", check.Warning: "⚠", check.Failed: "✖", check.Skipped: "⊘", check.Error: "‼",
	}[s]
	code := map[check.Status]string{
		check.Passed: "32", check.Warning: "33", check.Failed: "31", check.Skipped: "90", check.Error: "1;31",
	}[s]
	return p.wrap(code, icon)
}

func (p palette) dim(s string) string  { return p.wrap("90", s) }
func (p palette) bold(s string) string { return p.wrap("1", s) }

// Human writes the human-readable report.
func Human(w io.Writer, rep *runner.Report, meta Meta, opts HumanOptions) {
	p := palette{opts.Color}
	width := 0
	for _, r := range rep.Results {
		width = max(width, len(r.ID))
	}
	for _, r := range rep.Results {
		HumanLine(w, r, width, opts)
	}
	code := rep.ExitCode(meta.FailOn)
	verdict := p.wrap("1;32", "healthy")
	if code != 0 {
		verdict = p.wrap("1;31", "unhealthy")
	}
	fmt.Fprintf(w, "\n%s: %s %s\n", verdict, summaryLine(rep), p.dim("in "+units.FormatDuration(rep.Duration)))
}

// HumanLine writes one result (also used for streaming in watch/progress).
func HumanLine(w io.Writer, r runner.Result, width int, opts HumanOptions) {
	p := palette{opts.Color}
	id := r.ID + strings.Repeat(" ", max(0, width-len(r.ID)))
	label := p.bold(id)
	if r.Name != r.ID {
		label += " " + p.dim("("+r.Name+")")
	}
	timing := ""
	if r.Status != check.Skipped {
		timing = p.dim(" (" + units.FormatDuration(r.Duration) + ")")
	}
	fmt.Fprintf(w, "%s %s  %s%s\n", p.status(r.Status), label, r.Message, timing)
	indent := strings.Repeat(" ", width+4)
	bad := r.Status == check.Failed || r.Status == check.Error
	if bad || opts.Verbose {
		if r.Target != "" {
			fmt.Fprintf(w, "%s%s\n", indent, p.dim("target: "+r.Target))
		}
	}
	if bad && r.Remediation != "" {
		fmt.Fprintf(w, "%s%s %s\n", indent, p.wrap("36", "hint:"), r.Remediation)
	}
	if opts.Verbose || r.Status == check.Error {
		for _, k := range sortedDetailKeys(r.Details) {
			v := r.Details[k]
			s, ok := v.(string)
			if !ok {
				b, _ := json.Marshal(v)
				s = string(b)
			}
			if s == "" {
				continue
			}
			s = strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n"+indent+"  ")
			fmt.Fprintf(w, "%s%s %s\n", indent, p.dim(k+":"), s)
		}
	}
}
