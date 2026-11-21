// Package units defines the typed quantities used in medic configs.
//
// Every quantity is written as a string with an explicit unit. Bare numbers
// are rejected so a value can never be misread (is "5" seconds or millis?).
package units

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Quantity is a typed config value parsed from a unit-suffixed string.
type Quantity interface {
	// Set parses s into the receiver.
	Set(s string) error
	// String returns the canonical config spelling.
	String() string
	// Float is the value in the kind's base unit (seconds, bytes, percent, days).
	Float() float64
	// Kind names the quantity kind, e.g. "duration".
	Kind() string
}

// Def describes a quantity kind for schema and docs generation.
type Def struct {
	Kind        string
	Pattern     string
	Description string
	Examples    []string
}

var bareNumber = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)

func kindError(kind, s, example string) error {
	if bareNumber.MatchString(strings.TrimSpace(s)) {
		return fmt.Errorf("%s needs a unit, e.g. %s (got %q)", kind, example, s)
	}
	return fmt.Errorf("invalid %s %q (expected e.g. %s)", kind, s, example)
}

// ---- Duration ---------------------------------------------------------------

// Duration is a length of time such as "500ms", "5s", "2m", "1h30m".
type Duration struct {
	D   time.Duration
	raw string
}

var DurationDef = Def{
	Kind:        "duration",
	Pattern:     `^([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$`,
	Description: "A duration with a unit: ns, us, ms, s, m, h (e.g. 500ms, 5s, 2m, 1h30m).",
	Examples:    []string{"500ms", "5s", "2m"},
}

var durationRe = regexp.MustCompile(DurationDef.Pattern)

func (d *Duration) Set(s string) error {
	s = strings.TrimSpace(s)
	if !durationRe.MatchString(s) {
		return kindError("duration", s, "5s")
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return kindError("duration", s, "5s")
	}
	d.D, d.raw = v, s
	return nil
}

func (d Duration) String() string {
	if d.raw != "" {
		return d.raw
	}
	return FormatDuration(d.D)
}
func (d Duration) Float() float64 { return d.D.Seconds() }
func (Duration) Kind() string     { return "duration" }

// Dur builds a Duration from a time.Duration.
func Dur(v time.Duration) Duration { return Duration{D: v} }

// FormatDuration renders a duration compactly: 1.2s, 350ms, 2m0s -> 2m.
func FormatDuration(v time.Duration) string {
	switch {
	case v == 0:
		return "0s"
	case v < time.Millisecond:
		return v.Round(time.Microsecond).String()
	case v < time.Second:
		return strconv.FormatInt(v.Round(time.Millisecond).Milliseconds(), 10) + "ms"
	case v < time.Minute:
		return strconv.FormatFloat(v.Round(10*time.Millisecond).Seconds(), 'f', -1, 64) + "s"
	}
	s := v.Round(time.Second).String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// ---- Days -------------------------------------------------------------------

// Days is a whole number of days such as "30d".
type Days struct{ N int }

var DaysDef = Def{
	Kind:        "days",
	Pattern:     `^[0-9]+d$`,
	Description: "A whole number of days with a d suffix (e.g. 30d).",
	Examples:    []string{"7d", "30d"},
}

var daysRe = regexp.MustCompile(DaysDef.Pattern)

func (d *Days) Set(s string) error {
	s = strings.TrimSpace(s)
	if !daysRe.MatchString(s) {
		return kindError("days", s, "30d")
	}
	n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
	if err != nil {
		return kindError("days", s, "30d")
	}
	d.N = n
	return nil
}
func (d Days) String() string { return strconv.Itoa(d.N) + "d" }
func (d Days) Float() float64 { return float64(d.N) }
func (Days) Kind() string     { return "days" }
func DaysOf(n int) Days       { return Days{N: n} }

// ---- Percent ----------------------------------------------------------------

// Percent is a percentage such as "85%" or "99.5%".
type Percent struct{ V float64 }

var PercentDef = Def{
	Kind:        "percent",
	Pattern:     `^[0-9]+(\.[0-9]+)?%$`,
	Description: "A percentage with a % suffix (e.g. 85%, 99.5%).",
	Examples:    []string{"85%", "99.5%"},
}

var percentRe = regexp.MustCompile(PercentDef.Pattern)

func (p *Percent) Set(s string) error {
	s = strings.TrimSpace(s)
	if !percentRe.MatchString(s) {
		return kindError("percent", s, "85%")
	}
	v, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
	if err != nil {
		return kindError("percent", s, "85%")
	}
	p.V = v
	return nil
}
func (p Percent) String() string { return strconv.FormatFloat(p.V, 'f', -1, 64) + "%" }
func (p Percent) Float() float64 { return p.V }
func (Percent) Kind() string     { return "percent" }
func Pct(v float64) Percent      { return Percent{V: v} }

// ---- Size -------------------------------------------------------------------

// Size is an amount of bytes such as "512MiB" or "10GB".
type Size struct {
	Bytes float64
	raw   string
}

var SizeDef = Def{
	Kind:        "size",
	Pattern:     `^[0-9]+(\.[0-9]+)?(B|KB|MB|GB|TB|KiB|MiB|GiB|TiB)$`,
	Description: "A size with a unit: B, KB, MB, GB, TB (powers of 1000) or KiB, MiB, GiB, TiB (powers of 1024).",
	Examples:    []string{"512MiB", "10GB"},
}

var sizeRe = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)(B|KB|MB|GB|TB|KiB|MiB|GiB|TiB)$`)

var sizeMult = map[string]float64{
	"B": 1, "KB": 1e3, "MB": 1e6, "GB": 1e9, "TB": 1e12,
	"KiB": 1 << 10, "MiB": 1 << 20, "GiB": 1 << 30, "TiB": 1 << 40,
}

func (z *Size) Set(s string) error {
	s = strings.TrimSpace(s)
	m := sizeRe.FindStringSubmatch(s)
	if m == nil {
		return kindError("size", s, "512MiB")
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return kindError("size", s, "512MiB")
	}
	z.Bytes, z.raw = v*sizeMult[m[2]], s
	return nil
}

func (z Size) String() string {
	if z.raw != "" {
		return z.raw
	}
	return FormatBytes(z.Bytes)
}
func (z Size) Float() float64 { return z.Bytes }
func (Size) Kind() string     { return "size" }

// FormatBytes renders bytes with binary units.
func FormatBytes(b float64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	i := 0
	for b >= 1024 && i < len(units)-1 {
		b /= 1024
		i++
	}
	if i == 0 {
		return strconv.FormatFloat(b, 'f', 0, 64) + "B"
	}
	return strconv.FormatFloat(b, 'f', 1, 64) + units[i]
}

// Defs lists every quantity kind (for schema/docs).
var Defs = []Def{DurationDef, SizeDef, PercentDef, DaysDef}
