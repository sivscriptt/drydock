// Package explore finds every distinct way a case can go through a
// workflow. It runs the simulator in explore mode: each time a run needs an
// answer it does not have (an officer's decision, a lookup result, a form
// field a condition reads), it branches once per option, in parallel.
package explore

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sivscriptt/drydock/sim"
	"github.com/sivscriptt/drydock/workflow"
)

// Mode is how much of the decision space to explore.
type Mode string

const (
	// All tries every combination of answers that can matter.
	All Mode = "all"
	// Each tries every answer to every decision at least once, across all
	// runs: a run branches only into options no earlier run has tried for
	// that decision, and always carries on with one. Grows with the number
	// of distinct answers, not with their combinations.
	Each Mode = "each"
	// Auto tries All within MaxRuns and falls back to Each if it runs out.
	Auto Mode = "auto"
)

type Config struct {
	Mode         Mode
	MaxRuns      int           // budget for All in Auto mode
	Workers      int           // parallel simulations; default: number of CPUs
	MaxScenarios int           // stop after this many finished scenarios
	MaxDecisions int           // longest chain of decisions in one scenario
	MaxVisits    int           // times one task may come round in a scenario
	Timeout      time.Duration // whole exploration
	// Exhaustive branches on every answer, including ones that cannot change
	// the route or fail the instance.
	Exhaustive bool
	Sim        sim.Options
}

func DefaultConfig() Config {
	return Config{Mode: Auto, MaxRuns: 6000, Workers: runtime.NumCPU(), MaxScenarios: 5000, MaxDecisions: 500, MaxVisits: 3,
		Timeout: 60 * time.Second, Sim: sim.DefaultOptions()}
}

// Scenario is one distinct way through the workflow.
type Scenario struct {
	ID        string     `json:"id"`
	Decisions []string   `json:"decisions"` // the answers that lead here, in order
	Status    sim.Status `json:"status"`
	At        string     `json:"at,omitempty"`
	Reason    string     `json:"reason"`
	FlowState string     `json:"flow_state,omitempty"`
	Route     []string   `json:"route"`
	Pruned    string     `json:"pruned,omitempty"` // why exploration stopped early here
	Case      *sim.Case  `json:"case"`
	run       *sim.Run
}

// Result is a whole exploration.
type Result struct {
	Workflow  string        `json:"workflow"`
	Version   string        `json:"version"`
	Scenarios []*Scenario   `json:"scenarios"`
	Findings  []Finding     `json:"findings"`
	Coverage  Coverage      `json:"coverage"`
	Runs      int64         `json:"runs"`
	Duplicate int64         `json:"duplicates_skipped"`
	Truncated string        `json:"truncated,omitempty"`
	Mode      Mode          `json:"mode"`
	Fallback  string        `json:"fallback,omitempty"` // why Auto switched to Each
	Elapsed   time.Duration `json:"elapsed"`
	Workers   int           `json:"workers"`
}

type job struct {
	c         *sim.Case
	decisions []string
	choice    string // decision+option this job tried, for Each mode
	first     bool   // the first option of its decision
}

// Run explores from a base case, which fixes the submission and any
// answers that should not vary.
func Run(ctx context.Context, w *workflow.Workflow, base *sim.Case, cfg Config) *Result {
	if cfg.Mode == "" {
		cfg.Mode = Auto
	}
	if cfg.Mode != Auto {
		return run(ctx, w, base, cfg, 0)
	}
	start := time.Now()
	all := cfg
	all.Mode = All
	res := run(ctx, w, base, all, cfg.MaxRuns)
	if res.Truncated == "" {
		return res
	}
	each := cfg
	each.Mode = Each
	if cfg.Timeout > 0 {
		each.Timeout = cfg.Timeout - time.Since(start)
	}
	why := fmt.Sprintf("every combination needs more than %d runs (%s); showing each-choice coverage instead: every answer tried at least once", cfg.MaxRuns, res.Truncated)
	res = run(ctx, w, base, each, 0)
	res.Fallback = why
	res.Elapsed = time.Since(start)
	return res
}

// run explores in one mode. maxRuns > 0 stops it after that many runs.
func run(ctx context.Context, w *workflow.Workflow, base *sim.Case, cfg Config, maxRuns int) *Result {
	if cfg.Workers <= 0 {
		cfg.Workers = runtime.NumCPU()
	}
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}
	cfg.Sim.Explore = true
	cfg.Sim.AllDecisions = cfg.Exhaustive
	start := time.Now()
	res := &Result{Workflow: w.Name, Version: w.Version, Workers: cfg.Workers, Mode: cfg.Mode}

	var (
		finished  []*Scenario
		truncated string
		runs      int64
		dupes     int64
		seen      = map[string]bool{} // case fingerprints already queued
		tried     = map[string]bool{} // Each mode: decision+option already explored
	)
	if base == nil {
		base = &sim.Case{}
	}

	// Level by level: every case at one depth runs in parallel on a fixed
	// pool of workers, then the results are combined in a fixed order at a
	// barrier. Whatever order the workers finish in, the next level is the
	// same, so the report is too. A timeout stops between levels or mid-level
	// (workers check the context before each run).
	frontier := []job{{c: base.Apply(sim.Decision{}, sim.Option{})}}
	for len(frontier) > 0 {
		if ctx.Err() != nil {
			truncated = "time limit reached (" + cfg.Timeout.String() + ")"
			break
		}
		results := make([]stepResult, len(frontier))
		var next atomic.Int64
		var wg sync.WaitGroup
		for range min(cfg.Workers, len(frontier)) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					i := int(next.Add(1) - 1)
					if i >= len(frontier) || ctx.Err() != nil {
						return
					}
					results[i] = step(w, frontier[i], cfg)
				}
			}()
		}
		wg.Wait()
		runs += int64(len(frontier))

		// Combine in frontier order (already sorted), so the next level and
		// the report never depend on which worker finished first.
		var nextLevel []job
		for _, r := range results {
			if r.scenario != nil {
				finished = append(finished, r.scenario)
				continue
			}
			for _, ch := range r.children {
				if cfg.Mode == Each {
					// Carry on with the first option; branch into another
					// only if no run has tried it for this decision yet.
					if !ch.first && tried[ch.choice] {
						continue
					}
					tried[ch.choice] = true
				}
				fp := ch.c.Fingerprint()
				if seen[fp] {
					dupes++
					continue
				}
				seen[fp] = true
				nextLevel = append(nextLevel, ch)
			}
		}
		if maxRuns > 0 && runs > int64(maxRuns) {
			truncated = fmt.Sprintf("run budget reached (%d)", maxRuns)
			break
		}
		if cfg.MaxScenarios > 0 && len(finished) >= cfg.MaxScenarios {
			truncated = fmt.Sprintf("scenario limit reached (%d)", cfg.MaxScenarios)
			break
		}
		sort.Slice(nextLevel, func(a, b int) bool { return less(nextLevel[a].decisions, nextLevel[b].decisions) })
		frontier = nextLevel
	}

	// Workers finish in any order; sort so the same workflow always gives
	// the same report.
	sort.Slice(finished, func(i, j int) bool {
		return strings.Join(finished[i].Decisions, "\x00") < strings.Join(finished[j].Decisions, "\x00")
	})
	for i, sc := range finished {
		sc.ID = fmt.Sprintf("S%03d", i+1)
	}
	res.Scenarios = finished
	res.Findings = findings(finished)
	res.Coverage = cover(w, finished)
	res.Runs, res.Duplicate = runs, dupes
	res.Truncated = truncated
	res.Elapsed = time.Since(start)
	return res
}

// step runs one case. It returns either the branches to try next or, if
// the run ended (or cannot be explored further), a finished scenario.
type stepResult struct {
	children []job
	scenario *Scenario
}

func step(w *workflow.Workflow, j job, cfg Config) stepResult {
	run := sim.Simulate(w, j.c, cfg.Sim)
	d := run.Pending
	if d == nil || run.Status == sim.Failed || run.Status == sim.Stuck || run.Status == sim.Runaway {
		// Failed or stuck routes end the scenario even if another route
		// is waiting: the instance is already broken.
		return stepResult{scenario: scenario(j, run, "")}
	}
	if len(j.decisions) >= cfg.MaxDecisions {
		return stepResult{scenario: scenario(j, run, fmt.Sprintf("stopped after %d decisions", cfg.MaxDecisions))}
	}
	if d.Kind == sim.TaskState && d.Visit >= cfg.MaxVisits {
		return stepResult{scenario: scenario(j, run, fmt.Sprintf("%s came round %d times; a send-back cycle, not explored further", d.Key, d.Visit))}
	}
	children := make([]job, 0, len(d.Options))
	decKey := fmt.Sprintf("%s|%s|%d|%s|", d.Kind, d.Key, d.Visit, d.Field)
	for i, o := range d.Options {
		decisions := j.decisions
		if len(d.Options) > 1 {
			// Only real choices are worth listing in the scenario.
			decisions = append(append([]string(nil), j.decisions...), d.Label(o))
		}
		children = append(children, job{c: j.c.Apply(*d, o), decisions: decisions, choice: decKey + o.Label, first: i == 0})
	}
	return stepResult{children: children}
}

func less(a, b []string) bool { return strings.Join(a, "\x00") < strings.Join(b, "\x00") }

func scenario(j job, run *sim.Run, pruned string) *Scenario {
	sc := &Scenario{Decisions: j.decisions, Status: run.Status, At: run.At, Reason: run.Reason,
		FlowState: run.FlowState, Pruned: pruned, Case: j.c, run: run}
	if sc.Decisions == nil {
		sc.Decisions = []string{}
	}
	for _, s := range run.Steps {
		sc.Route = append(sc.Route, s.Node)
	}
	return sc
}

// Coverage is what the scenarios exercised, as a whole.
type Coverage struct {
	Nodes      int      `json:"nodes"`       // reachable nodes
	NodesHit   int      `json:"nodes_hit"`   // reached by some scenario
	Edges      int      `json:"edges"`       // transitions out of reachable nodes
	EdgesTaken int      `json:"edges_taken"` // taken by some scenario
	Unreached  []string `json:"unreached"`   // reachable in the graph, but no scenario got there
	Untaken    []string `json:"untaken"`     // transitions no scenario took
}

func cover(w *workflow.Workflow, scs []*Scenario) Coverage {
	hit := map[string]bool{}
	taken := map[string]bool{}
	for _, sc := range scs {
		for _, s := range sc.run.Steps {
			hit[s.Node] = true
			for _, c := range s.Children {
				hit[c.Node] = true
			}
			for _, e := range s.Edges {
				if e.Taken {
					taken[s.Node+"→"+e.To] = true
				}
			}
		}
	}
	var c Coverage
	reach := w.Reachable()
	for _, n := range w.Nodes {
		if !reach[n] {
			continue
		}
		key := n.Index
		if n.Loop != nil {
			key = n.Loop.Index + "/" + n.Index
		}
		c.Nodes++
		if hit[key] {
			c.NodesHit++
		} else {
			c.Unreached = append(c.Unreached, key)
		}
		for _, t := range n.Out {
			c.Edges++
			k := n.Index + "→" + t.To.Index
			if taken[k] {
				c.EdgesTaken++
			} else {
				cond := t.Condition
				if cond == "" {
					cond = "always"
				}
				c.Untaken = append(c.Untaken, fmt.Sprintf("%s → %s  when %s", n.Index, t.To.Index, cond))
			}
		}
	}
	return c
}

// Finding is one distinct problem, however many scenarios hit it.
type Finding struct {
	Status    sim.Status `json:"status"`
	At        string     `json:"at"`
	Reason    string     `json:"reason"`
	Scenarios int        `json:"scenarios"`
	// Example is the scenario with the fewest decisions that hits it: the
	// shortest way to reproduce.
	Example *Scenario `json:"example"`
}

// findings groups failed and stuck scenarios by where and why.
func findings(scs []*Scenario) []Finding {
	byKey := map[string]*Finding{}
	var order []string
	for _, sc := range scs {
		if sc.Status != sim.Failed && sc.Status != sim.Stuck && sc.Status != sim.Runaway {
			continue
		}
		reason := strings.SplitN(sc.Reason, "\n", 2)[0]
		key := string(sc.Status) + "|" + sc.At + "|" + reason
		f, ok := byKey[key]
		if !ok {
			f = &Finding{Status: sc.Status, At: sc.At, Reason: reason}
			byKey[key] = f
			order = append(order, key)
		}
		f.Scenarios++
		if f.Example == nil || len(sc.Decisions) < len(f.Example.Decisions) {
			f.Example = sc
		}
	}
	out := make([]Finding, 0, len(order))
	for _, k := range order {
		out = append(out, *byKey[k])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Scenarios > out[j].Scenarios })
	return out
}

// Steps returns the scenario's simulated steps.
func (s *Scenario) Steps() []sim.Step { return s.run.Steps }
