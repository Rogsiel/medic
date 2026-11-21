package checks

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/Rogsiel/medic/internal/check"
)

func init() {
	check.Register(&check.Type{
		Name:       "dns",
		Summary:    "Resolve a hostname, optionally via a specific DNS server, and check the addresses.",
		New:        func() check.Checker { return &DNS{} },
		Positional: "host",
		Example: `type: dns
host: api.example.com
server: 1.1.1.1
expect:
  addresses: [203.0.113.10]`,
	})
}

// DNS checks name resolution.
type DNS struct {
	Host   string    `yaml:"host" required:"true" doc:"Hostname to resolve."`
	Server string    `yaml:"server" doc:"DNS server to query, ip or ip:port (default: the system resolver)."`
	Expect DNSExpect `yaml:"expect" doc:"What counts as healthy."`
}

// DNSExpect are DNS expectations.
type DNSExpect struct {
	Addresses []string         `yaml:"addresses" doc:"Addresses that must all be in the answer."`
	Latency   check.DurationTH `yaml:"latency" threshold:"above" doc:"Resolution time limits."`
}

func (c *DNS) Target() string {
	if c.Server != "" {
		return c.Host + " @" + c.Server
	}
	return c.Host
}

func (c *DNS) Validate() error {
	for _, a := range c.Expect.Addresses {
		if net.ParseIP(a) == nil {
			return fmt.Errorf("expect.addresses: %q is not an IP address", a)
		}
	}
	if c.Server != "" {
		if _, _, err := splitHostPort(c.Server, 53); err != nil {
			return fmt.Errorf("server: %v", err)
		}
	}
	return nil
}

func (c *DNS) Run(ctx context.Context) check.Result {
	r := net.DefaultResolver
	if c.Server != "" {
		host, port, _ := splitHostPort(c.Server, 53)
		addr := joinHostPort(host, port)
		r = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		}}
	}
	start := time.Now()
	addrs, err := r.LookupHost(ctx, c.Host)
	elapsed := time.Since(start)
	details := map[string]any{"elapsed_ms": ms(elapsed)}
	if err != nil {
		msg := err.Error()
		var de *net.DNSError
		if errors.As(err, &de) {
			switch {
			case de.IsNotFound:
				msg = "no such host"
			case de.IsTimeout:
				msg = "timed out"
			default:
				msg = de.Err
			}
		}
		return failed("resolution failed: "+msg, details, "Check the DNS record and the resolver (/etc/resolv.conf or 'server').")
	}
	sort.Strings(addrs)
	details["addresses"] = addrs
	var missing []string
	for _, want := range c.Expect.Addresses {
		found := false
		for _, a := range addrs {
			if net.ParseIP(a).Equal(net.ParseIP(want)) {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, want)
		}
	}
	if len(missing) > 0 {
		return failed("missing expected address(es): "+strings.Join(missing, ", ")+" (got "+strings.Join(addrs, ", ")+")",
			details, "Update the DNS record or wait for propagation.")
	}
	g := newGrade()
	g.add(c.Expect.Latency.Above(elapsed.Seconds()), fmt.Sprintf("slow resolution: %s (limit %s)", dur(elapsed), c.Expect.Latency))
	return result(g.status, g.message("resolved to "+strings.Join(addrs, ", ")), details)
}
