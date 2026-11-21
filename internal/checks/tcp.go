package checks

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/Rogsiel/medic/internal/check"
)

func init() {
	check.Register(&check.Type{
		Name:       "tcp",
		Summary:    "Connect to a TCP port; optionally require it to be closed or to send a banner.",
		New:        func() check.Checker { return &TCP{Expect: TCPExpect{Open: true}} },
		Positional: "target",
		Example: `type: tcp
target: db.internal:5432
expect:
  latency: { warn: 200ms, fail: 1s }`,
	})
}

// TCP checks TCP reachability.
type TCP struct {
	Addr   string    `yaml:"target" required:"true" doc:"host:port to connect to."`
	Expect TCPExpect `yaml:"expect" doc:"What counts as healthy."`
}

// TCPExpect are TCP expectations.
type TCPExpect struct {
	Open           bool             `yaml:"open" doc:"true: the port must accept connections. false: the port must refuse or not answer (e.g. a firewalled admin port)."`
	BannerContains string           `yaml:"banner_contains" doc:"Text the server must send right after connecting (e.g. 'SSH-2.0', '220 ')."`
	Latency        check.DurationTH `yaml:"latency" threshold:"above" doc:"Connect time limits."`
}

func (c *TCP) Target() string { return c.Addr }

func (c *TCP) Validate() error {
	if c.Addr == "" {
		return nil
	}
	if _, _, err := splitHostPort(c.Addr, 0); err != nil {
		return err
	}
	if !c.Expect.Open && (c.Expect.BannerContains != "" || c.Expect.Latency.IsSet()) {
		return fmt.Errorf("expect.banner_contains and expect.latency cannot be used with expect.open: false")
	}
	return nil
}

func (c *TCP) Run(ctx context.Context) check.Result {
	var d net.Dialer
	start := time.Now()
	conn, err := d.DialContext(ctx, "tcp", c.Addr)
	elapsed := time.Since(start)
	details := map[string]any{"connect_ms": ms(elapsed)}
	if !c.Expect.Open {
		if err != nil {
			return result(check.Passed, "closed as expected", details)
		}
		_ = conn.Close()
		return failed("port is open but expected closed", details, "Close the port or firewall it off.")
	}
	if err != nil {
		return failed(fmt.Sprintf("connect failed: %s", netErr(err)), details,
			"Check that the service is running and listening, and that no firewall blocks the port.")
	}
	defer conn.Close()

	g := newGrade()
	g.add(c.Expect.Latency.Above(elapsed.Seconds()), fmt.Sprintf("slow connect: %s (limit %s)", dur(elapsed), c.Expect.Latency))
	msg := "connected in " + dur(elapsed)
	if c.Expect.BannerContains != "" {
		deadline := time.Now().Add(3 * time.Second)
		if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
			deadline = dl
		}
		_ = conn.SetReadDeadline(deadline)
		line, rerr := bufio.NewReader(conn).ReadString('\n')
		banner := strings.TrimSpace(line)
		details["banner"] = banner
		if banner == "" && rerr != nil {
			return failed("no banner received: "+netErr(rerr), details, "The service accepted the connection but sent nothing.")
		}
		if !strings.Contains(banner, c.Expect.BannerContains) {
			return failed(fmt.Sprintf("banner %q does not contain %q", firstLine(banner, 80), c.Expect.BannerContains), details, "")
		}
	}
	return result(g.status, g.message(msg), details)
}

// netErr shortens Go's verbose network errors.
func netErr(err error) string {
	s := err.Error()
	for _, marker := range []string{"connect: ", "dial tcp: ", "read: "} {
		if i := strings.LastIndex(s, marker); i >= 0 {
			s = s[i+len(marker):]
		}
	}
	if strings.Contains(s, "i/o timeout") || strings.Contains(s, "deadline exceeded") {
		return "timed out"
	}
	return s
}
