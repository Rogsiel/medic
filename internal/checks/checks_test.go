package checks

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Rogsiel/medic/internal/check"
	"github.com/Rogsiel/medic/internal/units"
)

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	return c
}

func want(t *testing.T, r check.Result, st check.Status, sub string) {
	t.Helper()
	if r.Status != st || !strings.Contains(r.Message, sub) {
		t.Fatalf("got %s %q, want %s containing %q", r.Status, r.Message, st, sub)
	}
}

func TestTCP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = c.Write([]byte("220 hello\r\n"))
			_ = c.Close()
		}
	}()
	addr := ln.Addr().String()
	want(t, (&TCP{Addr: addr, Expect: TCPExpect{Open: true, BannerContains: "220"}}).Run(ctx(t)), check.Passed, "connected")
	want(t, (&TCP{Addr: addr, Expect: TCPExpect{Open: true, BannerContains: "SSH"}}).Run(ctx(t)), check.Failed, "banner")
	want(t, (&TCP{Addr: addr, Expect: TCPExpect{Open: false}}).Run(ctx(t)), check.Failed, "expected closed")
	_ = ln.Close()
	want(t, (&TCP{Addr: addr, Expect: TCPExpect{Open: true}}).Run(ctx(t)), check.Failed, "connect failed")
	want(t, (&TCP{Addr: addr, Expect: TCPExpect{Open: false}}).Run(ctx(t)), check.Passed, "closed")
}

func TestHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"up","n":3,"items":[{"ok":true}]}`))
		case "/redirect":
			http.Redirect(w, r, "/health", http.StatusFound)
		case "/auth":
			if r.Header.Get("Authorization") != "Bearer t" {
				w.WriteHeader(401)
			}
		default:
			w.WriteHeader(503)
		}
	}))
	defer srv.Close()
	mk := func(path string) *HTTP { return &HTTP{URL: srv.URL + path, Method: "GET", FollowRedirects: true} }

	c := mk("/health")
	c.Expect.JSON = map[string]string{"$.status": "up", "$.n": "3", "$.items[0].ok": "true"}
	c.Expect.BodyContains = "up"
	want(t, c.Run(ctx(t)), check.Passed, "200")
	c.Expect.JSON = map[string]string{"$.status": "down"}
	want(t, c.Run(ctx(t)), check.Failed, "expected \"down\"")
	want(t, mk("/down").Run(ctx(t)), check.Failed, "status 503 (expected 2xx)")
	r := mk("/redirect")
	want(t, r.Run(ctx(t)), check.Passed, "200")
	r.FollowRedirects = false
	want(t, r.Run(ctx(t)), check.Failed, "302")
	a := mk("/auth")
	a.Headers = map[string]string{"Authorization": "Bearer t"}
	want(t, a.Run(ctx(t)), check.Passed, "200")
	s := mk("/health")
	s.Expect.Latency = check.TH[units.Duration]("1ns", "")
	want(t, s.Run(ctx(t)), check.Warning, "slow response")
}

func TestTLSUntrusted(t *testing.T) {
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	defer srv.Close()
	c := &TLS{Addr: strings.TrimPrefix(srv.URL, "https://"), Expect: TLSExpect{DaysLeft: check.TH[units.Days]("30d", "7d")}}
	want(t, c.Run(ctx(t)), check.Failed, "")
}

func TestExec(t *testing.T) {
	want(t, (&Exec{Command: []string{"true"}, Output: "text"}).Run(ctx(t)), check.Passed, "exit code 0")
	want(t, (&Exec{Shell: "echo oops >&2; exit 3", Output: "text"}).Run(ctx(t)), check.Failed, "exit code 3 (expected 0): oops")
	want(t, (&Exec{Shell: "echo ready", Output: "text", Expect: ExecExpect{StdoutContains: "ready"}}).Run(ctx(t)), check.Passed, "")
	want(t, (&Exec{Shell: "echo $FOO", Env: map[string]string{"FOO": "bar"}, Output: "text", Expect: ExecExpect{StdoutContains: "bar"}}).Run(ctx(t)), check.Passed, "")
	want(t, (&Exec{Shell: `echo '{"status":"warning","message":"lag 5s","details":{"lag":5}}'`, Output: "json"}).Run(ctx(t)), check.Warning, "lag 5s")
	want(t, (&Exec{Shell: "echo nope; exit 1", Output: "json"}).Run(ctx(t)), check.Failed, "no valid JSON")
	want(t, (&Exec{Command: []string{"definitely-not-a-program-xyz"}, Output: "text"}).Run(ctx(t)), check.Failed, "could not run")
	short, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	want(t, (&Exec{Shell: "sleep 10 | cat", Output: "text"}).Run(short), check.Failed, "timed out")
	if time.Since(start) > 2*time.Second {
		t.Fatal("process tree not killed")
	}
}

func TestFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	_ = os.WriteFile(p, []byte("hello world"), 0o600)
	want(t, (&File{Path: p, Expect: FileExpect{Exists: true, Mode: "0600", Contains: "world"}}).Run(ctx(t)), check.Passed, "file exists")
	want(t, (&File{Path: p, Expect: FileExpect{Exists: true, Mode: "0644"}}).Run(ctx(t)), check.Failed, "mode is 0600")
	want(t, (&File{Path: p, Expect: FileExpect{Exists: false}}).Run(ctx(t)), check.Failed, "should not")
	want(t, (&File{Path: p + "x", Expect: FileExpect{Exists: false}}).Run(ctx(t)), check.Passed, "")
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(p, old, old)
	want(t, (&File{Path: p, Expect: FileExpect{Exists: true, Age: check.TH[units.Duration]("1m", "30m")}}).Run(ctx(t)), check.Failed, "last modified")
}

func TestLocalChecks(t *testing.T) {
	want(t, (&DNS{Host: "localhost"}).Run(ctx(t)), check.Passed, "resolved")
	d := &Disk{Path: "/", Expect: DiskExpect{Used: check.TH[units.Percent]("", "100%")}}
	want(t, d.Run(ctx(t)), check.Passed, "used")
	m := &Memory{Expect: MemoryExpect{Used: check.TH[units.Percent]("", "100%")}}
	want(t, m.Run(ctx(t)), check.Passed, "used")
}

func TestSplitHostPort(t *testing.T) {
	for in, defPort := range map[string]int{"a:1": 0, "a": 443, "[::1]:22": 0, "::1": 22, "[::1]": 22} {
		if _, _, err := splitHostPort(in, defPort); err != nil {
			t.Errorf("%q: %v", in, err)
		}
	}
	for in, defPort := range map[string]int{"a": 0, "a:0": 0, "a:x": 0, ":1": 0} {
		if _, _, err := splitHostPort(in, defPort); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}
