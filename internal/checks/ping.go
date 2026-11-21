package checks

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"time"

	probing "github.com/prometheus-community/pro-bing"

	"github.com/Rogsiel/medic/internal/check"
	"github.com/Rogsiel/medic/internal/units"
)

func init() {
	check.Register(&check.Type{
		Name:    "ping",
		Summary: "Send ICMP echo requests and check packet loss and round-trip time.",
		New: func() check.Checker {
			return &Ping{Count: 3, Expect: PingExpect{Loss: check.TH[units.Percent]("0%", "50%")}}
		},
		Positional: "host",
		Example: `type: ping
host: 10.0.0.1
count: 5
expect:
  loss: { warn: 0%, fail: 50% }
  rtt: { warn: 50ms, fail: 200ms }`,
	})
}

// Ping checks ICMP reachability.
type Ping struct {
	Host   string     `yaml:"host" required:"true" doc:"Host or IP to ping."`
	Count  int        `yaml:"count" doc:"Number of echo requests."`
	Expect PingExpect `yaml:"expect" doc:"What counts as healthy."`
}

// PingExpect are ping expectations.
type PingExpect struct {
	Loss check.PercentTH  `yaml:"loss" threshold:"above" doc:"Packet loss limits."`
	RTT  check.DurationTH `yaml:"rtt" threshold:"above" doc:"Average round-trip time limits."`
}

func (c *Ping) Target() string { return c.Host }

func (c *Ping) Validate() error {
	if c.Count < 1 || c.Count > 100 {
		return fmt.Errorf("count must be between 1 and 100")
	}
	return nil
}

type pingStats struct {
	sent, recv int
	loss       float64
	avg        time.Duration
	method     string
}

func (c *Ping) Run(ctx context.Context) check.Result {
	st, err := c.probe(ctx)
	if err != nil {
		st, err = c.systemPing(ctx)
	}
	if err != nil {
		return failed("ping failed: "+err.Error(), nil,
			"Unprivileged ICMP may be disabled (sysctl net.ipv4.ping_group_range) and no usable 'ping' binary was found.")
	}
	details := map[string]any{"sent": st.sent, "received": st.recv, "loss_percent": st.loss, "method": st.method}
	if st.recv == 0 {
		return failed(fmt.Sprintf("no replies (%d sent)", st.sent), details, "Check the host is up and ICMP is not blocked.")
	}
	details["rtt_avg_ms"] = ms(st.avg)
	g := newGrade()
	g.add(c.Expect.Loss.Above(st.loss), fmt.Sprintf("%.0f%% packet loss (limit %s)", st.loss, c.Expect.Loss))
	g.add(c.Expect.RTT.Above(st.avg.Seconds()), fmt.Sprintf("slow: avg %s (limit %s)", dur(st.avg), c.Expect.RTT))
	return result(g.status, g.message(fmt.Sprintf("%d/%d replies, avg %s", st.recv, st.sent, dur(st.avg))), details)
}

func (c *Ping) probe(ctx context.Context) (pingStats, error) {
	p, err := probing.NewPinger(c.Host)
	if err != nil {
		return pingStats{}, err
	}
	p.SetPrivileged(runtime.GOOS == "windows")
	p.Count = c.Count
	p.Interval = 200 * time.Millisecond
	if dl, ok := ctx.Deadline(); ok {
		p.Timeout = time.Until(dl)
	}
	if err := p.RunWithContext(ctx); err != nil {
		return pingStats{}, err
	}
	s := p.Statistics()
	return pingStats{sent: s.PacketsSent, recv: s.PacketsRecv, loss: s.PacketLoss, avg: s.AvgRtt, method: "icmp"}, nil
}

var (
	reLoss = regexp.MustCompile(`([0-9.]+)% (packet )?loss`)
	reSent = regexp.MustCompile(`(\d+) packets transmitted, (\d+) (packets )?received`)
	reWin  = regexp.MustCompile(`Sent = (\d+), Received = (\d+)`)
	reRTT  = regexp.MustCompile(`= [0-9.]+/([0-9.]+)/`)
	reWAvg = regexp.MustCompile(`Average = (\d+)ms`)
)

// systemPing falls back to the OS ping binary.
func (c *Ping) systemPing(ctx context.Context) (pingStats, error) {
	n := strconv.Itoa(c.Count)
	args := []string{"-c", n, "-i", "0.2", c.Host}
	if runtime.GOOS == "windows" {
		args = []string{"-n", n, c.Host}
	}
	out, err := exec.CommandContext(ctx, "ping", args...).CombinedOutput()
	text := string(out)
	st := pingStats{method: "system ping"}
	if m := reSent.FindStringSubmatch(text); m != nil {
		st.sent, _ = strconv.Atoi(m[1])
		st.recv, _ = strconv.Atoi(m[2])
	} else if m := reWin.FindStringSubmatch(text); m != nil {
		st.sent, _ = strconv.Atoi(m[1])
		st.recv, _ = strconv.Atoi(m[2])
	} else {
		if err != nil {
			return st, fmt.Errorf("%v: %s", err, firstLine(text, 120))
		}
		return st, fmt.Errorf("cannot parse ping output")
	}
	if m := reLoss.FindStringSubmatch(text); m != nil {
		st.loss, _ = strconv.ParseFloat(m[1], 64)
	}
	if m := reRTT.FindStringSubmatch(text); m != nil {
		f, _ := strconv.ParseFloat(m[1], 64)
		st.avg = time.Duration(f * float64(time.Millisecond))
	} else if m := reWAvg.FindStringSubmatch(text); m != nil {
		f, _ := strconv.Atoi(m[1])
		st.avg = time.Duration(f) * time.Millisecond
	}
	return st, nil
}
