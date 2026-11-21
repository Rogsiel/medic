// Package checks implements medic's built-in check types. Each file registers
// one or more types in init(); the struct tags are the config reference.
package checks

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Rogsiel/medic/internal/check"
	"github.com/Rogsiel/medic/internal/units"
)

// grade accumulates the worst status and the message that caused it.
type grade struct {
	status check.Status
	msgs   []string
}

func newGrade() *grade { return &grade{status: check.Passed} }

// add records a sub-result; msg is only kept for non-passing statuses.
func (g *grade) add(s check.Status, msg string) {
	if s != check.Passed && msg != "" {
		g.msgs = append(g.msgs, msg)
	}
	g.status = check.Worst(g.status, s)
}

func (g *grade) message(ok string) string {
	if len(g.msgs) == 0 {
		return ok
	}
	return strings.Join(g.msgs, "; ")
}

func result(s check.Status, msg string, details map[string]any) check.Result {
	return check.Result{Status: s, Message: msg, Details: details}
}

func failed(msg string, details map[string]any, remediation string) check.Result {
	return check.Result{Status: check.Failed, Message: msg, Details: details, Remediation: remediation}
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000.0 }

func dur(d time.Duration) string { return units.FormatDuration(d) }

// splitHostPort parses host:port, with defPort used when the port is omitted (0 = required).
func splitHostPort(target string, defPort int) (string, int, error) {
	target = strings.TrimSpace(target)
	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		if defPort == 0 {
			return "", 0, fmt.Errorf("invalid target %q: expected host:port", target)
		}
		h := target
		if strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]") {
			h = h[1 : len(h)-1]
		} else if strings.Contains(h, ":") && net.ParseIP(h) == nil {
			return "", 0, fmt.Errorf("invalid target %q: expected host or host:port", target)
		}
		if h == "" {
			return "", 0, fmt.Errorf("missing host in %q", target)
		}
		return h, defPort, nil
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("invalid port %q in %q (1-65535)", portStr, target)
	}
	if host == "" {
		return "", 0, fmt.Errorf("missing host in %q", target)
	}
	return host, port, nil
}

func joinHostPort(host string, port int) string { return net.JoinHostPort(host, strconv.Itoa(port)) }

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

func firstLine(s string, n int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > n {
		s = s[:n] + "…"
	}
	return s
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
