// Package output renders reports: human text, JSON, JUnit XML, Markdown and
// CI annotations.
package output

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/Rogsiel/medic/internal/check"
	"github.com/Rogsiel/medic/internal/runner"
	"github.com/Rogsiel/medic/internal/units"
)

// SchemaVersion is the version of the JSON report format.
const SchemaVersion = 1

// Meta is run information that is not part of the report itself.
type Meta struct {
	Version string
	FailOn  string
}

// Formats lists the machine formats accepted by --output.
var Formats = []string{"json", "junit", "md", "text"}

// Write renders rep in format to w.
func Write(w io.Writer, format string, rep *runner.Report, meta Meta) error {
	switch format {
	case "json":
		return JSON(w, rep, meta)
	case "junit":
		return JUnit(w, rep)
	case "md":
		return Markdown(w, rep, meta)
	case "text":
		Human(w, rep, meta, HumanOptions{})
		return nil
	}
	return fmt.Errorf("unknown output format %q (use %s)", format, strings.Join(Formats, ", "))
}

// ---- JSON -------------------------------------------------------------------

type jsonSummary struct {
	Total   int `json:"total"`
	Passed  int `json:"passed"`
	Warning int `json:"warning"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
	Error   int `json:"error"`
}

type jsonCheck struct {
	runner.Result
	Duration string `json:"duration"`
}

type jsonReport struct {
	SchemaVersion int         `json:"schema_version"`
	MedicVersion  string      `json:"medic_version"`
	StartedAt     string      `json:"started_at"`
	Duration      string      `json:"duration"`
	FailOn        string      `json:"fail_on"`
	ExitCode      int         `json:"exit_code"`
	Summary       jsonSummary `json:"summary"`
	Checks        []jsonCheck `json:"checks"`
}

func summary(rep *runner.Report) jsonSummary {
	return jsonSummary{
		Total: len(rep.Results), Passed: rep.Count(check.Passed), Warning: rep.Count(check.Warning),
		Failed: rep.Count(check.Failed), Skipped: rep.Count(check.Skipped), Error: rep.Count(check.Error),
	}
}

// JSON writes the machine-readable report.
func JSON(w io.Writer, rep *runner.Report, meta Meta) error {
	out := jsonReport{
		SchemaVersion: SchemaVersion,
		MedicVersion:  meta.Version,
		StartedAt:     rep.StartedAt.UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		Duration:      units.FormatDuration(rep.Duration),
		FailOn:        meta.FailOn,
		ExitCode:      rep.ExitCode(meta.FailOn),
		Summary:       summary(rep),
		Checks:        []jsonCheck{},
	}
	for _, r := range rep.Results {
		out.Checks = append(out.Checks, jsonCheck{Result: r, Duration: units.FormatDuration(r.Duration)})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// ---- JUnit ------------------------------------------------------------------

type junitSuite struct {
	XMLName   xml.Name    `xml:"testsuite"`
	Name      string      `xml:"name,attr"`
	Tests     int         `xml:"tests,attr"`
	Failures  int         `xml:"failures,attr"`
	Errors    int         `xml:"errors,attr"`
	Skipped   int         `xml:"skipped,attr"`
	Time      string      `xml:"time,attr"`
	Timestamp string      `xml:"timestamp,attr"`
	Cases     []junitCase `xml:"testcase"`
}

type junitCase struct {
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr"`
	Time      string        `xml:"time,attr"`
	Failure   *junitMessage `xml:"failure,omitempty"`
	Error     *junitMessage `xml:"error,omitempty"`
	Skipped   *junitMessage `xml:"skipped,omitempty"`
	SystemOut string        `xml:"system-out,omitempty"`
}

type junitMessage struct {
	Message string `xml:"message,attr"`
	Text    string `xml:",chardata"`
}

// JUnit writes a JUnit XML testsuite. Warnings pass with a system-out note.
func JUnit(w io.Writer, rep *runner.Report) error {
	s := junitSuite{
		Name: "medic", Tests: len(rep.Results), Failures: rep.Count(check.Failed), Errors: rep.Count(check.Error),
		Skipped: rep.Count(check.Skipped), Time: fmt.Sprintf("%.3f", rep.Duration.Seconds()),
		Timestamp: rep.StartedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	for _, r := range rep.Results {
		c := junitCase{Name: r.ID, Classname: "medic." + r.Type, Time: fmt.Sprintf("%.3f", r.Duration.Seconds())}
		body := r.Message
		if r.Target != "" {
			body += "\ntarget: " + r.Target
		}
		if r.Remediation != "" {
			body += "\nhint: " + r.Remediation
		}
		switch r.Status {
		case check.Failed:
			c.Failure = &junitMessage{Message: r.Message, Text: body}
		case check.Error:
			c.Error = &junitMessage{Message: r.Message, Text: body}
		case check.Skipped:
			c.Skipped = &junitMessage{Message: r.Message}
		case check.Warning:
			c.SystemOut = "WARNING: " + r.Message
		}
		s.Cases = append(s.Cases, c)
	}
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(s); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n")
	return err
}

// ---- Markdown ---------------------------------------------------------------

var mdIcon = map[check.Status]string{
	check.Passed: "✅", check.Warning: "⚠️", check.Failed: "❌", check.Skipped: "⏭️", check.Error: "💥",
}

// Markdown writes a summary table (used for the GitHub step summary).
func Markdown(w io.Writer, rep *runner.Report, meta Meta) error {
	code := rep.ExitCode(meta.FailOn)
	verdict := "✅ healthy"
	if code != 0 {
		verdict = "❌ unhealthy"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "### medic: %s\n\n%s · %s\n\n", verdict, summaryLine(rep), units.FormatDuration(rep.Duration))
	b.WriteString("| | Check | Type | Result | Time |\n|---|---|---|---|---|\n")
	for _, r := range rep.Results {
		msg := r.Message
		if r.Remediation != "" && (r.Status == check.Failed || r.Status == check.Error) {
			msg += "<br>💡 " + r.Remediation
		}
		took := "–"
		if r.Status != check.Skipped {
			took = units.FormatDuration(r.Duration)
		}
		fmt.Fprintf(&b, "| %s | `%s` | %s | %s | %s |\n", mdIcon[r.Status], r.ID, r.Type, mdEscape(msg), took)
	}
	_, err := io.WriteString(w, b.String()+"\n")
	return err
}

func mdEscape(s string) string {
	return strings.NewReplacer("|", "\\|", "\n", " ").Replace(s)
}

func summaryLine(rep *runner.Report) string {
	var parts []string
	for _, s := range []check.Status{check.Passed, check.Warning, check.Failed, check.Skipped, check.Error} {
		if n := rep.Count(s); n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, s))
		}
	}
	if len(parts) == 0 {
		return "no checks"
	}
	return strings.Join(parts, ", ")
}

// ---- CI ---------------------------------------------------------------------

// CI identifies a detected CI system.
type CI string

const (
	NoCI   CI = ""
	GitHub CI = "github"
	GitLab CI = "gitlab"
)

// DetectCI inspects the environment.
func DetectCI() CI {
	switch {
	case os.Getenv("GITHUB_ACTIONS") == "true":
		return GitHub
	case os.Getenv("GITLAB_CI") == "true":
		return GitLab
	}
	return NoCI
}

// GitHubAnnotations writes workflow commands for failures and warnings.
func GitHubAnnotations(w io.Writer, rep *runner.Report) {
	esc := strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A")
	escProp := strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A", ":", "%3A", ",", "%2C")
	for _, r := range rep.Results {
		level := ""
		switch r.Status {
		case check.Failed, check.Error:
			level = "error"
		case check.Warning:
			level = "warning"
		default:
			continue
		}
		msg := r.Message
		if r.Remediation != "" {
			msg += "\nhint: " + r.Remediation
		}
		fmt.Fprintf(w, "::%s title=%s::%s\n", level, escProp.Replace("medic "+r.ID), esc.Replace(msg))
	}
}

// GitHubSummary appends the Markdown summary to $GITHUB_STEP_SUMMARY if set.
func GitHubSummary(rep *runner.Report, meta Meta) error {
	path := os.Getenv("GITHUB_STEP_SUMMARY")
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	return Markdown(f, rep, meta)
}

// sortedDetailKeys returns map keys in a stable order.
func sortedDetailKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
