package units

import (
	"strings"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	var d Duration
	if err := d.Set("1m30s"); err != nil || d.D != 90*time.Second {
		t.Fatalf("duration: %v %v", d.D, err)
	}
	for _, bad := range []string{"5", "5 s", "5sec", "", "-1s"} {
		if err := (&Duration{}).Set(bad); err == nil {
			t.Errorf("duration %q: expected error", bad)
		}
	}
	if err := (&Duration{}).Set("5"); !strings.Contains(err.Error(), "needs a unit") {
		t.Errorf("bare number hint missing: %v", err)
	}
	var p Percent
	if err := p.Set("99.5%"); err != nil || p.V != 99.5 {
		t.Fatalf("percent: %v %v", p.V, err)
	}
	if err := (&Percent{}).Set("85"); err == nil {
		t.Error("percent without % accepted")
	}
	var z Size
	if err := z.Set("2GiB"); err != nil || z.Bytes != 2<<30 {
		t.Fatalf("size: %v %v", z.Bytes, err)
	}
	if err := (&Size{}).Set("2gb"); err == nil {
		t.Error("lowercase unit accepted")
	}
	var dd Days
	if err := dd.Set("30d"); err != nil || dd.N != 30 {
		t.Fatalf("days: %v %v", dd.N, err)
	}
}

func TestFormatDuration(t *testing.T) {
	cases := map[time.Duration]string{
		350 * time.Millisecond:  "350ms",
		1234 * time.Millisecond: "1.23s",
		2 * time.Minute:         "2m",
		90 * time.Second:        "1m30s",
	}
	for in, want := range cases {
		if got := FormatDuration(in); got != want {
			t.Errorf("FormatDuration(%v) = %q, want %q", in, got, want)
		}
	}
}
