// Package runner executes checks as a dependency graph with per-attempt
// timeouts, retries and bounded concurrency.
package runner

import (
	"context"
	"fmt"
	"math/rand/v2"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Rogsiel/medic/internal/check"
	"github.com/Rogsiel/medic/internal/units"
)

// Grace is how long the runner waits past an attempt's timeout for a check
// to return before it gives up on it.
const Grace = 2 * time.Second

// DefaultRetryInterval is used when retry.interval is not set.
const DefaultRetryInterval = time.Second

// Result is the final outcome of one check.
type Result struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Type        string         `json:"type"`
	Status      check.Status   `json:"status"`
	Message     string         `json:"message"`
	Target      string         `json:"target"`
	Attempts    int            `json:"attempts"`
	Duration    time.Duration  `json:"-"`
	DependsOn   []string       `json:"depends_on"`
	Tags        []string       `json:"tags"`
	Details     map[string]any `json:"details"`
	Remediation string         `json:"remediation,omitempty"`
}

// Report is the outcome of a run.
type Report struct {
	StartedAt time.Time
	Duration  time.Duration
	Results   []Result // in config order
}

// Count returns the number of results with status s.
func (r *Report) Count(s check.Status) int {
	n := 0
	for _, x := range r.Results {
		if x.Status == s {
			n++
		}
	}
	return n
}

// ExitCode maps the report to medic's exit code under a fail_on policy.
func (r *Report) ExitCode(failOn string) int {
	switch {
	case r.Count(check.Error) > 0:
		return 3
	case r.Count(check.Failed) > 0:
		return 1
	case failOn == "warning" && r.Count(check.Warning) > 0:
		return 1
	}
	return 0
}

// EventKind identifies a runner event.
type EventKind int

const (
	Started EventKind = iota
	Retrying
	Finished
)

// Event is emitted while the run progresses (serialised; handlers need no locking).
type Event struct {
	Kind    EventKind
	ID      string
	Attempt int
	Last    check.Result // for Retrying: the failed attempt
	Result  *Result      // for Finished
}

// Options configure a run.
type Options struct {
	Concurrency int
	// Wait is the default retry deadline for checks without their own retry.
	Wait time.Duration
	// NoRetry disables retries (watch mode).
	NoRetry bool
	// Skip lists ids to report as skipped (with their dependents).
	Skip    map[string]bool
	Mask    *Masker
	OnEvent func(Event)
}

type node struct {
	spec *check.Spec
	done chan struct{}
	res  Result
}

// Run executes specs and returns the report. Specs must already be selected
// and their dependency graph validated.
func Run(ctx context.Context, specs []*check.Spec, opts Options) *Report {
	if opts.Concurrency < 1 {
		opts.Concurrency = 1
	}
	start := time.Now()
	nodes := make(map[string]*node, len(specs))
	for _, s := range specs {
		nodes[s.ID] = &node{spec: s, done: make(chan struct{})}
	}
	sem := make(chan struct{}, opts.Concurrency)
	var emitMu sync.Mutex
	emit := func(e Event) {
		if opts.OnEvent != nil {
			emitMu.Lock()
			defer emitMu.Unlock()
			opts.OnEvent(e)
		}
	}

	var wg sync.WaitGroup
	for _, s := range specs {
		n := nodes[s.ID]
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer close(n.done)
			n.res = execute(ctx, n.spec, nodes, sem, opts, emit)
			opts.Mask.Result(&n.res)
			emit(Event{Kind: Finished, ID: n.spec.ID, Result: &n.res})
		}()
	}
	wg.Wait()

	rep := &Report{StartedAt: start, Duration: time.Since(start)}
	for _, s := range specs {
		rep.Results = append(rep.Results, nodes[s.ID].res)
	}
	return rep
}

func base(s *check.Spec) Result {
	deps := s.DependsOn
	if deps == nil {
		deps = []string{}
	}
	tags := s.Tags
	if tags == nil {
		tags = []string{}
	}
	return Result{ID: s.ID, Name: s.DisplayName(), Type: s.Type, Target: s.Checker.Target(), DependsOn: deps, Tags: tags, Details: map[string]any{}}
}

func execute(ctx context.Context, s *check.Spec, nodes map[string]*node, sem chan struct{}, opts Options, emit func(Event)) Result {
	res := base(s)
	if opts.Skip[s.ID] {
		res.Status, res.Message = check.Skipped, "skipped by --skip"
		return res
	}
	// Wait for dependencies.
	var blocked []string
	for _, dep := range s.DependsOn {
		dn := nodes[dep]
		select {
		case <-dn.done:
		case <-ctx.Done():
			res.Status, res.Message = check.Skipped, "run cancelled"
			return res
		}
		if !dn.res.Status.Healthy() {
			blocked = append(blocked, fmt.Sprintf("%s %s", dep, dn.res.Status))
		}
	}
	if len(blocked) > 0 {
		res.Status, res.Message = check.Skipped, "dependency "+strings.Join(blocked, ", ")
		return res
	}

	// Retry policy.
	maxAttempts, deadline := 1, time.Time{}
	interval := DefaultRetryInterval
	if !opts.NoRetry {
		switch {
		case s.Retry != nil && s.Retry.Until != nil:
			maxAttempts, deadline = 0, time.Now().Add(s.Retry.Until.D)
		case s.Retry != nil:
			maxAttempts = s.Retry.Attempts
		case opts.Wait > 0:
			maxAttempts, deadline = 0, time.Now().Add(opts.Wait)
		}
		if s.Retry != nil && s.Retry.Interval.D > 0 {
			interval = s.Retry.Interval.D
		}
	}

	timeout := 5 * time.Second
	if s.Timeout != nil {
		timeout = s.Timeout.D
	}
	started := time.Now()
	var last check.Result
	for attempt := 1; ; attempt++ {
		sem <- struct{}{}
		if attempt == 1 {
			emit(Event{Kind: Started, ID: s.ID, Attempt: 1})
		}
		last = attempt1(ctx, s.Checker, timeout)
		<-sem
		res.Attempts = attempt

		if last.Status != check.Failed || ctx.Err() != nil {
			break
		}
		if maxAttempts > 0 && attempt >= maxAttempts {
			break
		}
		wait := interval + time.Duration(rand.Int64N(int64(interval)/10+1))
		if maxAttempts == 0 && time.Now().Add(wait).After(deadline) {
			break
		}
		emit(Event{Kind: Retrying, ID: s.ID, Attempt: attempt, Last: last})
		select {
		case <-time.After(wait):
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
	}
	res.Duration = time.Since(started)
	res.Status, res.Message, res.Remediation = last.Status, last.Message, last.Remediation
	for k, v := range last.Details {
		res.Details[k] = v
	}
	if res.Attempts > 1 {
		res.Message += fmt.Sprintf(" (after %d attempts over %s)", res.Attempts, units.FormatDuration(res.Duration))
	}
	if res.Status == check.Failed && s.AllowFailure {
		res.Status = check.Warning
		res.Message = "allowed failure: " + res.Message
		res.Details["allowed_failure"] = true
	}
	return res
}

// attempt1 runs one attempt with a timeout, converting panics and hangs into results.
func attempt1(ctx context.Context, c check.Checker, timeout time.Duration) check.Result {
	actx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ch := make(chan check.Result, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				ch <- check.Result{
					Status:      check.Error,
					Message:     fmt.Sprintf("internal error: %v", p),
					Details:     map[string]any{"stack": string(debug.Stack())},
					Remediation: "This is a bug in medic; please report it with the details above.",
				}
			}
		}()
		ch <- c.Run(actx)
	}()
	select {
	case r := <-ch:
		if r.Status == "" {
			r.Status = check.Error
			r.Message = "internal error: check returned no status"
		}
		if r.Status == check.Failed && actx.Err() == context.DeadlineExceeded && ctx.Err() == nil &&
			!strings.Contains(r.Message, "timed out") {
			r.Message = fmt.Sprintf("timed out after %s: %s", units.FormatDuration(timeout), r.Message)
		}
		return r
	case <-time.After(timeout + Grace):
		return check.Result{Status: check.Failed, Message: fmt.Sprintf("timed out after %s", units.FormatDuration(timeout)),
			Remediation: "The target did not respond in time; raise 'timeout' if it is legitimately slow."}
	}
}

// Select resolves --only/--skip. Entries are check ids or tag:NAME.
// --only keeps the matched checks plus everything they depend on;
// --skip marks matched checks as skipped (dependents are skipped in turn).
func Select(specs []*check.Spec, only, skip []string) ([]*check.Spec, map[string]bool, error) {
	byID := map[string]*check.Spec{}
	for _, s := range specs {
		byID[s.ID] = s
	}
	resolve := func(flag string, sel []string) (map[string]bool, error) {
		out := map[string]bool{}
		for _, item := range sel {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			if tag, ok := strings.CutPrefix(item, "tag:"); ok {
				found := false
				for _, s := range specs {
					for _, t := range s.Tags {
						if t == tag {
							out[s.ID], found = true, true
						}
					}
				}
				if !found {
					return nil, fmt.Errorf("%s: no check has tag %q", flag, tag)
				}
				continue
			}
			if byID[item] == nil {
				ids := make([]string, 0, len(byID))
				for id := range byID {
					ids = append(ids, id)
				}
				sort.Strings(ids)
				return nil, fmt.Errorf("%s: unknown check id %q (known: %s; use tag:NAME for tags)", flag, item, strings.Join(ids, ", "))
			}
			out[item] = true
		}
		return out, nil
	}
	skipSet, err := resolve("--skip", skip)
	if err != nil {
		return nil, nil, err
	}
	if len(only) == 0 {
		return specs, skipSet, nil
	}
	onlySet, err := resolve("--only", only)
	if err != nil {
		return nil, nil, err
	}
	keep := map[string]bool{}
	var walk func(id string)
	walk = func(id string) {
		if keep[id] {
			return
		}
		keep[id] = true
		for _, d := range byID[id].DependsOn {
			walk(d)
		}
	}
	for id := range onlySet {
		walk(id)
	}
	var out []*check.Spec
	for _, s := range specs {
		if keep[s.ID] {
			out = append(out, s)
		}
	}
	return out, skipSet, nil
}
