package checks

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Rogsiel/medic/internal/check"
)

func init() {
	check.Register(&check.Type{
		Name:       "file",
		Summary:    "Check that a file exists (or not), its permissions, age and contents.",
		New:        func() check.Checker { return &File{Expect: FileExpect{Exists: true}} },
		Positional: "path",
		Example: `type: file
path: /var/run/app/ready
expect:
  age: { warn: 5m, fail: 15m }`,
	})
}

const maxScan = 10 << 20

// File checks a file or directory.
type File struct {
	Path   string     `yaml:"path" required:"true" doc:"Path to the file or directory (~ is expanded)."`
	Expect FileExpect `yaml:"expect" doc:"What counts as healthy."`
}

// FileExpect are file expectations.
type FileExpect struct {
	Exists   bool             `yaml:"exists" doc:"true: the path must exist. false: it must not exist (e.g. a maintenance flag)."`
	Mode     string           `yaml:"mode" doc:"Exact permission bits in octal, e.g. \"0600\"."`
	Age      check.DurationTH `yaml:"age" threshold:"above" doc:"Time since last modification."`
	Contains string           `yaml:"contains" doc:"Text the file must contain (first 10MiB are searched)."`
}

var modeRe = regexp.MustCompile(`^0?[0-7]{3,4}$`)

func (c *File) Target() string { return c.Path }

func (c *File) Validate() error {
	if c.Expect.Mode != "" && !modeRe.MatchString(c.Expect.Mode) {
		return fmt.Errorf("expect.mode %q must be octal like \"0644\" (quote it so YAML keeps the leading zero)", c.Expect.Mode)
	}
	if !c.Expect.Exists && (c.Expect.Mode != "" || c.Expect.Age.IsSet() || c.Expect.Contains != "") {
		return fmt.Errorf("expect.mode, expect.age and expect.contains cannot be used with expect.exists: false")
	}
	return nil
}

func (c *File) Run(ctx context.Context) check.Result {
	p := expandHome(c.Path)
	info, err := os.Stat(p)
	if errors.Is(err, fs.ErrNotExist) {
		if !c.Expect.Exists {
			return result(check.Passed, "does not exist, as expected", nil)
		}
		return failed("does not exist", nil, "")
	}
	if err != nil {
		return failed("cannot stat: "+err.Error(), nil, "")
	}
	if !c.Expect.Exists {
		return failed("exists but should not", nil, "Remove it if it is a leftover (lock or maintenance file).")
	}
	age := time.Since(info.ModTime())
	mode := fmt.Sprintf("%04o", info.Mode().Perm())
	details := map[string]any{"size": info.Size(), "mode": mode, "modified": info.ModTime().UTC().Format(time.RFC3339), "dir": info.IsDir()}

	if c.Expect.Mode != "" {
		want, _ := strconv.ParseUint(c.Expect.Mode, 8, 32)
		if uint32(info.Mode().Perm()) != uint32(want) {
			return failed(fmt.Sprintf("mode is %s (expected %04o)", mode, want), details, fmt.Sprintf("chmod %04o %s", want, c.Path))
		}
	}
	if c.Expect.Contains != "" {
		if info.IsDir() {
			return failed("is a directory; expect.contains needs a file", details, "")
		}
		ok, err := fileContains(ctx, p, c.Expect.Contains)
		if err != nil {
			return failed("cannot read: "+err.Error(), details, "")
		}
		if !ok {
			return failed(fmt.Sprintf("does not contain %q", c.Expect.Contains), details, "")
		}
	}
	g := newGrade()
	g.add(c.Expect.Age.Above(age.Seconds()), fmt.Sprintf("last modified %s ago (limit %s)", dur(age), c.Expect.Age))
	kind := "file"
	if info.IsDir() {
		kind = "directory"
	}
	return result(g.status, g.message(fmt.Sprintf("%s exists, modified %s ago", kind, dur(age))), details)
}

func fileContains(ctx context.Context, path, needle string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 64<<10)
	var buf strings.Builder
	chunk := make([]byte, 64<<10)
	read := 0
	for read < maxScan {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		n, err := r.Read(chunk)
		buf.Write(chunk[:n])
		read += n
		if strings.Contains(buf.String(), needle) {
			return true, nil
		}
		// keep only a tail long enough to match across chunk borders
		if s := buf.String(); len(s) > len(needle) {
			keep := s[len(s)-len(needle):]
			buf.Reset()
			buf.WriteString(keep)
		}
		if err != nil {
			break
		}
	}
	return false, nil
}
