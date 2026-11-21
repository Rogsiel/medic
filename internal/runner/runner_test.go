package runner

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Rogsiel/medic/internal/check"
	"github.com/Rogsiel/medic/internal/units"
)

type fake struct {
	statuses  []check.Status // per attempt; last repeats
	calls     atomic.Int32
	sleep     time.Duration
	ignoreCtx bool
	panics    bool
	msg       string
}

func (f *fake) Target() string { return "fake" }
func (f *fake) Run(ctx context.Context) check.Result {
	n := int(f.calls.Add(1))
	if f.panics {
		panic("boom")
	}
	if f.sleep > 0 {
		if f.ignoreCtx {
			time.Sleep(f.sleep)
		} else {
			select {
			case <-time.After(f.sleep):
			case <-ctx.Done():
				return check.Result{Status: check.Failed, Message: "timed out"}
			}
		}
	}
	i := n - 1
	if i >= len(f.statuses) {
		i = len(f.statuses) - 1
	}
	return check.Result{Status: f.statuses[i], Message: f.msg + string(f.statuses[i])}
}

func sp(id string, f *fake, deps ...string) *check.Spec {
	to := units.Dur(time.Second)
	return &check.Spec{ID: id, Common: check.Common{Type: "fake", DependsOn: deps, Timeout: &to}, Checker: f}
}

func ok() *fake  { return &fake{statuses: []check.Status{check.Passed}} }
func bad() *fake { return &fake{statuses: []check.Status{check.Failed}} }
func byID(r *Report) map[string]Result {
	m := map[string]Result{}
	for _, x := range r.Results {
		m[x.ID] = x
	}
	return m
}

func TestDependencies(t *testing.T) {
	specs := []*check.Spec{sp("db", bad()), sp("api", ok(), "db"), sp("web", ok(), "api"), sp("other", ok())}
	rep := Run(context.Background(), specs, Options{Concurrency: 4})
	m := byID(rep)
	if m["db"].Status != check.Failed || m["api"].Status != check.Skipped || m["web"].Status != check.Skipped || m["other"].Status != check.Passed {
		t.Fatalf("statuses: %+v", m)
	}
	if m["api"].Message != "dependency db failed" || m["web"].Message != "dependency api skipped" {
		t.Errorf("messages: %q / %q", m["api"].Message, m["web"].Message)
	}
	if rep.ExitCode("failure") != 1 {
		t.Error("exit code")
	}
	if rep.Results[0].ID != "db" || rep.Results[3].ID != "other" {
		t.Error("config order not kept")
	}
}

func TestRetryUntilPass(t *testing.T) {
	f := &fake{statuses: []check.Status{check.Failed, check.Failed, check.Passed}}
	s := sp("db", f)
	iv := units.Dur(10 * time.Millisecond)
	s.Retry = &check.Retry{Attempts: 5, Interval: iv}
	rep := Run(context.Background(), []*check.Spec{s, sp("api", ok(), "db")}, Options{Concurrency: 2})
	m := byID(rep)
	if m["db"].Status != check.Passed || m["db"].Attempts != 3 || m["api"].Status != check.Passed {
		t.Fatalf("%+v", m)
	}
}

func TestRetryUntilDeadline(t *testing.T) {
	f := bad()
	s := sp("db", f)
	until := units.Dur(100 * time.Millisecond)
	s.Retry = &check.Retry{Until: &until, Interval: units.Dur(20 * time.Millisecond)}
	rep := Run(context.Background(), []*check.Spec{s}, Options{Concurrency: 1})
	r := rep.Results[0]
	if r.Status != check.Failed || r.Attempts < 3 || !strings.Contains(r.Message, "attempts") {
		t.Fatalf("%+v", r)
	}
}

func TestWaitFlagAndNoRetry(t *testing.T) {
	f := bad()
	rep := Run(context.Background(), []*check.Spec{sp("a", f)}, Options{Concurrency: 1, Wait: 50 * time.Millisecond, NoRetry: true})
	if rep.Results[0].Attempts != 1 {
		t.Fatal("NoRetry ignored")
	}
}

func TestAllowFailureAndExitCodes(t *testing.T) {
	s := sp("a", bad())
	s.AllowFailure = true
	rep := Run(context.Background(), []*check.Spec{s, sp("b", ok(), "a")}, Options{Concurrency: 1})
	m := byID(rep)
	if m["a"].Status != check.Warning || m["b"].Status != check.Passed {
		t.Fatalf("%+v", m)
	}
	if rep.ExitCode("failure") != 0 || rep.ExitCode("warning") != 1 {
		t.Error("exit codes")
	}
}

func TestTimeoutAndPanic(t *testing.T) {
	hang := &fake{statuses: []check.Status{check.Passed}, sleep: 5 * time.Second, ignoreCtx: true}
	s := sp("hang", hang)
	to := units.Dur(50 * time.Millisecond)
	s.Timeout = &to
	start := time.Now()
	rep := Run(context.Background(), []*check.Spec{s, sp("boom", &fake{panics: true, statuses: []check.Status{check.Passed}})}, Options{Concurrency: 2})
	if time.Since(start) > Grace+time.Second {
		t.Fatal("hung check blocked the run")
	}
	m := byID(rep)
	if m["hang"].Status != check.Failed || !strings.Contains(m["hang"].Message, "timed out") {
		t.Errorf("hang: %+v", m["hang"])
	}
	if m["boom"].Status != check.Error || rep.ExitCode("failure") != 3 {
		t.Errorf("panic: %+v", m["boom"])
	}
}

func TestSelect(t *testing.T) {
	specs := []*check.Spec{sp("db", ok()), sp("api", ok(), "db"), sp("web", ok()), sp("x", ok())}
	specs[2].Tags = []string{"prod"}
	got, _, err := Select(specs, []string{"api", "tag:prod"}, nil)
	if err != nil || len(got) != 3 || got[0].ID != "db" || got[2].ID != "web" {
		t.Fatalf("only: %v %v", got, err)
	}
	if _, _, err := Select(specs, []string{"nope"}, nil); err == nil {
		t.Error("unknown id accepted")
	}
	all, skip, _ := Select(specs, nil, []string{"db"})
	rep := Run(context.Background(), all, Options{Concurrency: 2, Skip: skip})
	m := byID(rep)
	if m["db"].Status != check.Skipped || m["api"].Message != "dependency db skipped" || rep.ExitCode("failure") != 0 {
		t.Fatalf("%+v", m)
	}
}

func TestMask(t *testing.T) {
	f := &fake{statuses: []check.Status{check.Failed}, msg: "token=hunter2 "}
	rep := Run(context.Background(), []*check.Spec{sp("a", f)}, Options{Concurrency: 1, Mask: NewMasker([]string{"hunter2"})})
	if strings.Contains(rep.Results[0].Message, "hunter2") {
		t.Fatal("secret leaked")
	}
}
