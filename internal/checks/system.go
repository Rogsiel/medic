package checks

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"

	"github.com/Rogsiel/medic/internal/check"
	"github.com/Rogsiel/medic/internal/units"
)

func init() {
	check.Register(&check.Type{
		Name:    "cpu",
		Summary: "Sample CPU usage on this machine.",
		New: func() check.Checker {
			return &CPU{Sample: units.Dur(time.Second), Expect: CPUExpect{Usage: check.TH[units.Percent]("80%", "95%")}}
		},
		Example: `type: cpu
expect:
  usage: { warn: 80%, fail: 95% }`,
	})
	check.Register(&check.Type{
		Name:    "memory",
		Summary: "Check RAM usage on this machine.",
		New: func() check.Checker {
			return &Memory{Expect: MemoryExpect{Used: check.TH[units.Percent]("85%", "95%")}}
		},
		Example: `type: memory
expect:
  used: { warn: 85%, fail: 95% }`,
	})
	check.Register(&check.Type{
		Name:    "swap",
		Summary: "Check swap usage on this machine (passes when no swap is configured).",
		New: func() check.Checker {
			return &Swap{Expect: SwapExpect{Used: check.TH[units.Percent]("50%", "80%")}}
		},
		Example: `type: swap
expect:
  used: { warn: 50%, fail: 80% }`,
	})
	check.Register(&check.Type{
		Name:    "disk",
		Summary: "Check usage and free space of the filesystem holding a path.",
		New: func() check.Checker {
			return &Disk{Expect: DiskExpect{Used: check.TH[units.Percent]("85%", "92%")}}
		},
		Positional: "path",
		Example: `type: disk
path: /
expect:
  used: { warn: 85%, fail: 92% }
  free: { warn: 10GiB, fail: 2GiB }`,
	})
	check.Register(&check.Type{
		Name:    "load",
		Summary: "Check the load average relative to the number of CPUs (not supported on Windows).",
		New: func() check.Checker {
			return &Load{Window: "5m", Expect: LoadExpect{PerCPU: check.TH[units.Percent]("100%", "200%")}}
		},
		Example: `type: load
window: 5m
expect:
  per_cpu: { warn: 100%, fail: 200% }`,
	})
}

// ---- cpu ----

// CPU samples CPU usage.
type CPU struct {
	Sample units.Duration `yaml:"sample" doc:"How long to measure CPU usage."`
	Expect CPUExpect      `yaml:"expect" doc:"What counts as healthy."`
}

// CPUExpect are CPU expectations.
type CPUExpect struct {
	Usage check.PercentTH `yaml:"usage" threshold:"above" doc:"Average usage across all CPUs."`
}

func (c *CPU) Target() string { return "localhost" }

func (c *CPU) Run(ctx context.Context) check.Result {
	pct, err := cpu.PercentWithContext(ctx, c.Sample.D, false)
	if err != nil || len(pct) == 0 {
		return failed(fmt.Sprintf("cannot read CPU usage: %v", err), nil, "")
	}
	v := pct[0]
	details := map[string]any{"usage_percent": round1(v), "cpus": runtime.NumCPU()}
	st := c.Expect.Usage.Above(v)
	r := result(st, fmt.Sprintf("%.1f%% used over %s", v, c.Sample), details)
	if st != check.Passed {
		r.Remediation = "Find the busy processes (top, ps -eo pcpu,cmd --sort=-pcpu)."
	}
	return r
}

// ---- memory ----

// Memory checks RAM.
type Memory struct {
	Expect MemoryExpect `yaml:"expect" doc:"What counts as healthy."`
}

// MemoryExpect are memory expectations.
type MemoryExpect struct {
	Used      check.PercentTH `yaml:"used" threshold:"above" doc:"Share of RAM in use."`
	Available check.SizeTH    `yaml:"available" threshold:"below" doc:"RAM available for new work."`
}

func (c *Memory) Target() string { return "localhost" }

func (c *Memory) Run(ctx context.Context) check.Result {
	vm, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return failed("cannot read memory stats: "+err.Error(), nil, "")
	}
	details := map[string]any{"used_percent": round1(vm.UsedPercent), "total": units.FormatBytes(float64(vm.Total)), "available": units.FormatBytes(float64(vm.Available))}
	g := newGrade()
	g.add(c.Expect.Used.Above(vm.UsedPercent), fmt.Sprintf("%.1f%% used (limit %s)", vm.UsedPercent, c.Expect.Used))
	g.add(c.Expect.Available.Below(float64(vm.Available)), fmt.Sprintf("only %s available (limit %s)", units.FormatBytes(float64(vm.Available)), c.Expect.Available))
	r := result(g.status, g.message(fmt.Sprintf("%.1f%% used, %s available of %s", vm.UsedPercent,
		units.FormatBytes(float64(vm.Available)), units.FormatBytes(float64(vm.Total)))), details)
	if g.status != check.Passed {
		r.Remediation = "Find memory-hungry processes (ps -eo rss,cmd --sort=-rss)."
	}
	return r
}

// ---- swap ----

// Swap checks swap usage.
type Swap struct {
	Expect SwapExpect `yaml:"expect" doc:"What counts as healthy."`
}

// SwapExpect are swap expectations.
type SwapExpect struct {
	Used check.PercentTH `yaml:"used" threshold:"above" doc:"Share of swap in use."`
}

func (c *Swap) Target() string { return "localhost" }

func (c *Swap) Run(ctx context.Context) check.Result {
	sw, err := mem.SwapMemoryWithContext(ctx)
	if err != nil {
		return failed("cannot read swap stats: "+err.Error(), nil, "")
	}
	if sw.Total == 0 {
		return result(check.Passed, "no swap configured", map[string]any{"total": "0B"})
	}
	details := map[string]any{"used_percent": round1(sw.UsedPercent), "total": units.FormatBytes(float64(sw.Total))}
	st := c.Expect.Used.Above(sw.UsedPercent)
	return result(st, fmt.Sprintf("%.1f%% of %s used", sw.UsedPercent, units.FormatBytes(float64(sw.Total))), details)
}

// ---- disk ----

// Disk checks a filesystem.
type Disk struct {
	Path   string     `yaml:"path" required:"true" doc:"Any path on the filesystem to check, e.g. / or /var/lib/docker."`
	Expect DiskExpect `yaml:"expect" doc:"What counts as healthy."`
}

// DiskExpect are disk expectations.
type DiskExpect struct {
	Used       check.PercentTH `yaml:"used" threshold:"above" doc:"Share of space in use."`
	Free       check.SizeTH    `yaml:"free" threshold:"below" doc:"Free space."`
	InodesUsed check.PercentTH `yaml:"inodes_used" threshold:"above" doc:"Share of inodes in use."`
}

func (c *Disk) Target() string { return c.Path }

func (c *Disk) Run(ctx context.Context) check.Result {
	u, err := disk.UsageWithContext(ctx, c.Path)
	if err != nil {
		return failed("cannot read disk usage: "+err.Error(), nil, "Check that the path exists.")
	}
	details := map[string]any{
		"used_percent": round1(u.UsedPercent), "free": units.FormatBytes(float64(u.Free)),
		"total": units.FormatBytes(float64(u.Total)), "fstype": u.Fstype, "inodes_used_percent": round1(u.InodesUsedPercent),
	}
	g := newGrade()
	g.add(c.Expect.Used.Above(u.UsedPercent), fmt.Sprintf("%.1f%% used (limit %s)", u.UsedPercent, c.Expect.Used))
	g.add(c.Expect.Free.Below(float64(u.Free)), fmt.Sprintf("only %s free (limit %s)", units.FormatBytes(float64(u.Free)), c.Expect.Free))
	if u.InodesTotal > 0 {
		g.add(c.Expect.InodesUsed.Above(u.InodesUsedPercent), fmt.Sprintf("%.1f%% inodes used (limit %s)", u.InodesUsedPercent, c.Expect.InodesUsed))
	}
	r := result(g.status, g.message(fmt.Sprintf("%.1f%% used, %s free of %s", u.UsedPercent,
		units.FormatBytes(float64(u.Free)), units.FormatBytes(float64(u.Total)))), details)
	if g.status != check.Passed {
		r.Remediation = "Free space: old logs, caches, container images (du -xh " + c.Path + " | sort -h | tail)."
	}
	return r
}

// ---- load ----

// Load checks the load average.
type Load struct {
	Window string     `yaml:"window" enum:"1m,5m,15m" doc:"Which load average to use."`
	Expect LoadExpect `yaml:"expect" doc:"What counts as healthy."`
}

// LoadExpect are load expectations.
type LoadExpect struct {
	PerCPU check.PercentTH `yaml:"per_cpu" threshold:"above" doc:"Load average divided by the number of CPUs (100% = one runnable task per CPU)."`
}

func (c *Load) Target() string { return "localhost" }

func (c *Load) Run(ctx context.Context) check.Result {
	if runtime.GOOS == "windows" {
		return result(check.Skipped, "load average is not available on Windows", nil)
	}
	avg, err := load.AvgWithContext(ctx)
	if err != nil {
		return failed("cannot read load average: "+err.Error(), nil, "")
	}
	v := map[string]float64{"1m": avg.Load1, "5m": avg.Load5, "15m": avg.Load15}[c.Window]
	n := runtime.NumCPU()
	per := v / float64(n) * 100
	details := map[string]any{"load1": avg.Load1, "load5": avg.Load5, "load15": avg.Load15, "cpus": n, "per_cpu_percent": round1(per)}
	st := c.Expect.PerCPU.Above(per)
	return result(st, fmt.Sprintf("load %.2f (%s) on %d CPUs = %.0f%% per CPU", v, c.Window, n, per), details)
}

func round1(v float64) float64 { return float64(int(v*10+0.5)) / 10 }
