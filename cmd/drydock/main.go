// Command drydock loads and checks OneGov workflow exports.
//
//	drydock inspect <export.json | bundle-folder>
//	drydock lint    <export.json | bundle-folder> [-json]
//	drydock run     <export.json | bundle-folder> <case.yaml> [-json] [-fork all]
//	drydock replay  <export.json | bundle-folder> <context.json> <legs.json> [-fork all]
//	drydock paths   <export.json | bundle-folder> [base.yaml] [-mode auto|all|each] [-workers N] [-timeout 60s] [-json]
//	drydock view    <export.json | bundle-folder> [base.yaml] [-o view.html] [-mode auto|all|each]
//	drydock dot     <export.json | bundle-folder>   > workflow.dot
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/sivscriptt/drydock/explore"
	"github.com/sivscriptt/drydock/lint"
	"github.com/sivscriptt/drydock/replay"
	"github.com/sivscriptt/drydock/sim"
	"github.com/sivscriptt/drydock/viewer"
	"github.com/sivscriptt/drydock/workflow"
)

const usage = `usage:
  drydock inspect <export.json | bundle-folder>
  drydock lint    <export.json | bundle-folder> [-json]
  drydock run     <export.json | bundle-folder> <case.yaml> [-json] [-fork all]
  drydock replay  <export.json | bundle-folder> <context.json> <legs.json> [-fork all]
  drydock paths   <export.json | bundle-folder> [base.yaml] [-mode auto|all|each] [-workers N] [-timeout 60s] [-json]
  drydock view    <export.json | bundle-folder> [base.yaml] [-o view.html] [-mode auto|all|each]
  drydock dot     <export.json | bundle-folder>`

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "lint" {
		lintCmd(os.Args[2:])
		return
	}
	if len(os.Args) >= 2 && os.Args[1] == "view" {
		viewCmd(os.Args[2:])
		return
	}
	if len(os.Args) >= 2 && os.Args[1] == "paths" {
		pathsCmd(os.Args[2:])
		return
	}
	if len(os.Args) >= 2 && os.Args[1] == "replay" {
		replayCmd(os.Args[2:])
		return
	}
	if len(os.Args) >= 2 && os.Args[1] == "run" {
		runCase(os.Args[2:])
		return
	}
	if len(os.Args) != 3 {
		fail(usage)
	}
	w, problems, err := workflow.Load(os.Args[2])
	if err != nil && w == nil {
		fail(err.Error())
	}
	switch os.Args[1] {
	case "inspect":
		inspect(w, problems)
		if err != nil {
			os.Exit(1)
		}
	case "dot":
		if err != nil {
			fail(err.Error())
		}
		writeDot(w)
	default:
		fail(usage)
	}
}

func inspect(w *workflow.Workflow, problems []workflow.Problem) {
	fmt.Printf("%s  v%s\n", w.Name, w.Version)
	loops := 0
	for _, n := range w.Nodes {
		if n.Loop != nil {
			loops++
		}
	}
	fmt.Printf("%d nodes (%d inside loops), %d transitions, %d variables, %d trigger fields\n",
		len(w.Nodes), loops, len(w.Transitions), len(w.Variables), len(w.Trigger))
	if w.Start != nil {
		fmt.Printf("start: %s\n", w.Start)
	}

	var parts []string
	for _, c := range w.ControlCounts() {
		parts = append(parts, fmt.Sprintf("%s %d", c.Control, c.Count))
	}
	fmt.Printf("\nnodes by type: %s\n", strings.Join(parts, " · "))

	if len(w.Milestones) > 0 {
		var ms []string
		for _, m := range w.Milestones {
			ms = append(ms, m.Name)
		}
		fmt.Printf("milestones: %s\n", strings.Join(ms, " → "))
	}

	if len(problems) == 0 {
		fmt.Println("\nno problems found")
		return
	}
	sort.SliceStable(problems, func(i, j int) bool { return problems[i].Severity > problems[j].Severity })
	counts := map[workflow.Severity]int{}
	for _, p := range problems {
		counts[p.Severity]++
	}
	fmt.Printf("\n%d errors, %d warnings, %d notes\n", counts[workflow.Error], counts[workflow.Warning], counts[workflow.Info])
	for _, p := range problems {
		fmt.Println("  " + p.String())
	}
}

// writeDot prints the graph in Graphviz format: loops as clusters, tasks as
// boxes, ends as double circles, conditions on the edges.
func writeDot(w *workflow.Workflow) {
	fmt.Printf("digraph %q {\n  rankdir=TB;\n  node [fontname=\"Helvetica\", fontsize=10];\n  edge [fontname=\"Helvetica\", fontsize=8];\n", w.Name)
	shape := func(n *workflow.Node) string {
		switch n.Control {
		case workflow.CtrlAssignTask:
			return "box, style=\"rounded,filled\", fillcolor=\"#e7f5ff\""
		case workflow.CtrlEnd, workflow.CtrlFormat:
			return "doublecircle"
		case workflow.CtrlNoAction:
			return "circle"
		case workflow.CtrlExpressionV2, workflow.CtrlExpressionV1:
			return "diamond"
		case workflow.CtrlDataHub:
			return "cylinder"
		}
		return "box"
	}
	label := func(n *workflow.Node) string {
		name := n.Name
		if len(name) > 40 {
			name = name[:37] + "…"
		}
		return fmt.Sprintf("%s\\n%s", n.Index, strings.ReplaceAll(name, `"`, `'`))
	}
	id := func(n *workflow.Node) string { return fmt.Sprintf("N%d", n.ID) }

	for _, n := range w.Nodes {
		if n.Loop != nil {
			continue
		}
		if n.Control == workflow.CtrlLoop {
			fmt.Printf("  subgraph cluster_%d {\n    label=%q; style=dashed;\n", n.ID, "loop "+n.Index)
			fmt.Printf("    %s [label=\"%s\", shape=hexagon];\n", id(n), label(n))
			prev := n
			for _, c := range n.Children {
				fmt.Printf("    %s [label=\"%s\", shape=%s];\n", id(c), label(c), shape(c))
				fmt.Printf("    %s -> %s [style=dotted];\n", id(prev), id(c))
				prev = c
			}
			fmt.Println("  }")
			continue
		}
		fmt.Printf("  %s [label=\"%s\", shape=%s];\n", id(n), label(n), shape(n))
	}
	for _, t := range w.Transitions {
		cond := t.Condition
		if len(cond) > 60 {
			cond = cond[:57] + "…"
		}
		fmt.Printf("  %s -> %s [label=%q];\n", id(t.From), id(t.To), cond)
	}
	fmt.Println("}")
}

func runCase(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "print the run as JSON")
	fork := fs.String("fork", "first", "when several transitions match: first or all")
	// Allow flags after the positional arguments.
	var pos []string
	for len(args) > 0 {
		fs.Parse(args)
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
	if len(pos) != 2 {
		fail(usage)
	}
	w, _, err := workflow.Load(pos[0])
	if err != nil {
		fail(err.Error())
	}
	c, err := sim.LoadCase(pos[1])
	if err != nil {
		fail(err.Error())
	}
	opt := sim.DefaultOptions()
	if *fork == "all" {
		opt.Fork = sim.AllMatches
	}
	run := sim.Simulate(w, c, opt)
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(struct {
			*sim.Run
			Context any `json:"-"`
		}{Run: run})
	} else {
		printRun(run)
	}
	if run.Status != sim.Completed {
		os.Exit(3)
	}
}

func replayCmd(args []string) {
	fs := flag.NewFlagSet("replay", flag.ExitOnError)
	fork := fs.String("fork", "first", "when several transitions match: first or all")
	var pos []string
	for len(args) > 0 {
		fs.Parse(args)
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
	if len(pos) != 3 {
		fail(usage)
	}
	w, _, err := workflow.Load(pos[0])
	if err != nil {
		fail(err.Error())
	}
	rec, err := replay.Load(pos[1], pos[2])
	if err != nil {
		fail(err.Error())
	}
	opt := sim.DefaultOptions()
	if *fork == "all" {
		opt.Fork = sim.AllMatches
	}
	rep := replay.Replay(w, rec, opt)

	fmt.Printf("%s v%s: replaying %d recorded legs\n\n", rep.Workflow, rep.Version, len(rec.Legs))
	if rep.RouteMatches {
		fmt.Printf("route      MATCHES  %d steps: %s\n", len(rep.Recorded), strings.Join(rep.Recorded, " "))
	} else {
		fmt.Printf("route      DIFFERS  at step %d\n  engine:  %s\n  drydock: %s\n", rep.Diverge+1, strings.Join(rep.Recorded, " "), strings.Join(rep.Simulated, " "))
	}
	show := func(name string, c replay.Check) {
		fmt.Printf("%-10s %d/%d match\n", name, c.Matched, c.Checked)
		for i, m := range c.Mismatches {
			if i == 8 {
				fmt.Printf("  … and %d more\n", len(c.Mismatches)-8)
				break
			}
			fmt.Printf("  %s %s\n    engine:  %s\n    drydock: %s\n", m.Node, m.Key, clip(m.Engine), clip(m.Drydock))
		}
	}
	show("substitution", rep.Substitution)
	show("outputs", rep.Outputs)
	fmt.Printf("\nsimulation ended %s: %s\n", rep.Status, rep.Reason)
	for _, n := range rep.Run.Notes {
		if strings.Contains(n, "transitions matched") {
			fmt.Println("multiple matches: " + n)
		}
	}
	if len(rep.Inferred) > 0 {
		fmt.Println("\nworked out from the route (the context snapshot did not have them):")
		for _, s := range rep.Inferred {
			fmt.Println("  - " + s)
		}
	}
	if !rep.RouteMatches || len(rep.Substitution.Mismatches) > 0 || len(rep.Outputs.Mismatches) > 0 {
		os.Exit(4)
	}
}

func pathsCmd(args []string) {
	cfg := explore.DefaultConfig()
	fs := flag.NewFlagSet("paths", flag.ExitOnError)
	fs.IntVar(&cfg.Workers, "workers", cfg.Workers, "parallel simulations")
	fs.IntVar(&cfg.MaxScenarios, "max", cfg.MaxScenarios, "stop after this many scenarios")
	fs.DurationVar(&cfg.Timeout, "timeout", cfg.Timeout, "time limit")
	asJSON := fs.Bool("json", false, "print the result as JSON")
	mode := fs.String("mode", "auto", "all (every combination), each (every answer at least once) or auto")
	fs.BoolVar(&cfg.Exhaustive, "exhaustive", false, "branch on every answer, even ones that cannot change the route or fail the instance")
	var pos []string
	for len(args) > 0 {
		fs.Parse(args)
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
	if len(pos) < 1 || len(pos) > 2 {
		fail(usage)
	}
	w, _, err := workflow.Load(pos[0])
	if err != nil {
		fail(err.Error())
	}
	var base *sim.Case
	if len(pos) == 2 {
		if base, err = sim.LoadCase(pos[1]); err != nil {
			fail(err.Error())
		}
	}
	cfg.Mode = explore.Mode(*mode)
	res := explore.Run(context.Background(), w, base, cfg)
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(res)
		return
	}

	fmt.Printf("%s v%s: %d scenarios from %d runs on %d workers in %s\n",
		res.Workflow, res.Version, len(res.Scenarios), res.Runs, res.Workers, res.Elapsed.Round(time.Millisecond))
	if res.Fallback != "" {
		fmt.Printf("note: %s\n", res.Fallback)
	}
	if res.Truncated != "" {
		fmt.Printf("INCOMPLETE: %s. Coverage below is a lower bound.\n", res.Truncated)
	}
	if len(res.Findings) > 0 {
		fmt.Printf("\n%d distinct problems:\n", len(res.Findings))
		for i, f := range res.Findings {
			fmt.Printf("  %d. %s at %s, in %d scenario(s). Shortest: %s\n     %s\n", i+1, strings.ToUpper(string(f.Status)), f.At, f.Scenarios, f.Example.ID, clip(f.Reason))
		}
	}
	byStatus := map[sim.Status][]*explore.Scenario{}
	for _, sc := range res.Scenarios {
		byStatus[sc.Status] = append(byStatus[sc.Status], sc)
	}
	for _, st := range []sim.Status{sim.Failed, sim.Stuck, sim.Runaway, sim.Completed, sim.Waiting, sim.Rejected} {
		list := byStatus[st]
		if len(list) == 0 {
			continue
		}
		fmt.Printf("\n%s (%d)\n", strings.ToUpper(string(st)), len(list))
		for i, sc := range list {
			if i == 12 {
				fmt.Printf("  … and %d more\n", len(list)-12)
				break
			}
			end := sc.FlowState
			if st != sim.Completed {
				end = firstLine(sc.Reason)
			}
			if sc.Pruned != "" {
				end = sc.Pruned
			}
			fmt.Printf("  %s  %s\n        %s\n", sc.ID, clip(strings.Join(sc.Decisions, " · ")), clip(end))
		}
	}
	c := res.Coverage
	fmt.Printf("\ncoverage: %d/%d nodes, %d/%d transitions\n", c.NodesHit, c.Nodes, c.EdgesTaken, c.Edges)
	for i, u := range c.Untaken {
		if i == 10 {
			fmt.Printf("  … and %d more\n", len(c.Untaken)-10)
			break
		}
		fmt.Println("  never taken: " + clip(u))
	}
	if len(byStatus[sim.Failed])+len(byStatus[sim.Stuck]) > 0 {
		os.Exit(5)
	}
}

func lintCmd(args []string) {
	fs := flag.NewFlagSet("lint", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "print the findings as JSON")
	var pos []string
	for len(args) > 0 {
		fs.Parse(args)
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
	if len(pos) != 1 {
		fail(usage)
	}
	w, _, err := workflow.Load(pos[0])
	if err != nil {
		fail(err.Error())
	}
	findings, st := lint.Workflow(w)
	fails := 0
	for _, f := range findings {
		if f.Fails {
			fails++
		}
	}
	if *asJSON {
		if findings == nil {
			findings = []lint.Finding{}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(map[string]any{
			"workflow": w.Name, "version": w.Version,
			"expressions": st.Expressions, "conditions": st.Conditions,
			"fails": fails, "findings": findings,
		})
		return
	}
	fmt.Printf("%s v%s: checked %d expressions and %d conditions\n", w.Name, w.Version, st.Expressions, st.Conditions)
	fmt.Printf("%d findings, %d would fail an instance\n\n", len(findings), fails)
	for _, f := range findings {
		fmt.Println(f)
	}
	if fails > 0 {
		os.Exit(2)
	}
}

func viewCmd(args []string) {
	cfg := explore.DefaultConfig()
	fs := flag.NewFlagSet("view", flag.ExitOnError)
	out := fs.String("o", "drydock-view.html", "page to write")
	mode := fs.String("mode", "auto", "all, each or auto")
	fs.DurationVar(&cfg.Timeout, "timeout", cfg.Timeout, "time limit for exploring")
	var pos []string
	for len(args) > 0 {
		fs.Parse(args)
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
	if len(pos) < 1 || len(pos) > 2 {
		fail(usage)
	}
	w, _, err := workflow.Load(pos[0])
	if err != nil {
		fail(err.Error())
	}
	var base *sim.Case
	if len(pos) == 2 {
		if base, err = sim.LoadCase(pos[1]); err != nil {
			fail(err.Error())
		}
	}
	cfg.Mode = explore.Mode(*mode)
	res := explore.Run(context.Background(), w, base, cfg)
	f, err := os.Create(*out)
	if err != nil {
		fail(err.Error())
	}
	defer f.Close()
	if err := viewer.Render(f, viewer.Build(w, res)); err != nil {
		fail(err.Error())
	}
	fmt.Printf("wrote %s: %d scenarios, %d problems\n", *out, len(res.Scenarios), len(res.Findings))
}

func clip(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 150 {
		return s[:147] + "…"
	}
	return s
}

func firstLine(s string) string { return strings.SplitN(s, "\n", 2)[0] }

func printRun(run *sim.Run) {
	fmt.Printf("%s v%s · case: %s\n\n", run.Workflow, run.Version, run.Case)
	for _, st := range run.Steps {
		printStep(st, "")
	}
	fmt.Printf("\n%s", strings.ToUpper(string(run.Status)))
	if run.At != "" {
		fmt.Printf(" at %s", run.At)
	}
	fmt.Printf(": %s\n", run.Reason)
	if run.FlowState != "" {
		fmt.Printf("status shown to the applicant: %s\n", run.FlowState)
	}
	if len(run.Notes) > 0 {
		fmt.Println("\nassumptions and warnings:")
		for _, n := range run.Notes {
			fmt.Println("  - " + n)
		}
	}
}

func printStep(st sim.Step, indent string) {
	mark := map[string]string{"ran": "✓", "ended": "■", "waiting": "…", "failed": "✗", "stuck": "!"}[st.Event]
	line := fmt.Sprintf("%s%s %-8s %-15s %s", indent, mark, st.Node, st.Control, st.Name)
	if st.Milestone != "" {
		line += "  [" + st.Milestone + "]"
	}
	fmt.Println(line)
	if st.Detail != "" {
		for _, l := range strings.Split(st.Detail, "\n") {
			if len(l) > 160 {
				l = l[:157] + "…"
			}
			fmt.Printf("%s    %s\n", indent, l)
		}
	}
	for _, c := range st.Children {
		printStep(c, indent+"    ")
	}
	for _, e := range st.Edges {
		if e.Taken {
			cond := e.Condition
			if cond == "" {
				cond = "always"
			}
			extra := ""
			if e.StateChange != "" {
				extra = "  · status: " + e.StateChange
			}
			fmt.Printf("%s    → %s  (%s)%s\n", indent, e.To, cond, extra)
		}
	}
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}
