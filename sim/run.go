package sim

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/sivscriptt/drydock/expr"
	"github.com/sivscriptt/drydock/workflow"
)

// Status is how a run ended.
type Status string

const (
	Completed Status = "completed" // every route reached an end
	Failed    Status = "failed"    // the engine would fail the instance
	Waiting   Status = "waiting"   // the case does not say what happens at a node
	Stuck     Status = "stuck"     // a node finished and no transition matched
	Rejected  Status = "rejected"  // the submission never became an instance
	Runaway   Status = "runaway"   // hit the step limit; probably a cycle
)

// Fork is what happens when more than one transition out of a node matches.
// The platform documentation says both; replay will settle it.
type Fork int

const (
	FirstMatch Fork = iota // take the first matching transition
	AllMatches             // start a parallel route for each
)

type Options struct {
	Policy   expr.Policy
	Fork     Fork
	MaxSteps int
	// Explore turns every unknown outside answer into a Decision on the run
	// instead of a guess or a plain stop, so the path explorer can branch.
	Explore bool
	// AllDecisions branches even on answers that cannot change the route
	// or make the instance fail. Off by default: those get one
	// representative answer.
	AllDecisions bool
}

func DefaultOptions() Options { return Options{Policy: expr.Live(), MaxSteps: 2000} }

// Run is the result of one simulated case.
type Run struct {
	Workflow  string
	Version   string
	Case      string
	Status    Status
	Reason    string // why it stopped, in plain words
	At        string // node it stopped at, if it did not complete
	FlowState string // the instance's last public status
	Steps     []Step
	Notes     []string // assumptions the simulator made
	// Pending is the decision an explored run stopped on.
	Pending *Decision
	Context map[string]any
}

// Step is one node running on one route.
type Step struct {
	Seq       int         `json:"seq"`
	Leg       int         `json:"leg"`
	Node      string      `json:"node"`
	Name      string      `json:"name"`
	Control   string      `json:"control"`
	Milestone string      `json:"milestone,omitempty"`
	Event     string      `json:"event"` // ran, waiting, failed, stuck, ended
	Detail    string      `json:"detail,omitempty"`
	Output    any         `json:"output,omitempty"`
	Subs      []expr.Sub  `json:"-"`
	Edges     []Edge      `json:"edges,omitempty"`
	Children  []Step      `json:"children,omitempty"` // loop iterations
	Exprs     []ExprTrace `json:"expressions,omitempty"`
}

// ExprTrace is one Expression V2 output: the text the parser saw, and the
// value it produced.
type ExprTrace struct {
	Key         string `json:"key"`
	Substituted string `json:"substituted"`
	Value       any    `json:"value"`
	Error       string `json:"error,omitempty"`
}

// Edge is a transition that was considered after a node ran.
type Edge struct {
	To          string   `json:"to"`
	Condition   string   `json:"condition,omitempty"`
	Pass        bool     `json:"pass"`
	Taken       bool     `json:"taken"`
	Unresolved  []string `json:"unresolved,omitempty"`
	Error       string   `json:"error,omitempty"`
	StateChange string   `json:"state_change,omitempty"`
}

type leg struct {
	id   int
	node *workflow.Node
}

type runner struct {
	w      *workflow.Workflow
	c      *Case
	opt    Options
	s      *scope
	run    *Run
	seq    int
	legs   int
	stops  []stop // waiting / stuck / failed legs
	visits map[string]int
	cols   map[string][]string
}

type stop struct {
	status Status
	node   string
	reason string
}

// Simulate runs a case through a workflow.
func Simulate(w *workflow.Workflow, c *Case, opt Options) *Run {
	if opt.MaxSteps == 0 {
		opt.MaxSteps = 2000
	}
	r := &runner{w: w, c: c, opt: opt, s: &scope{data: map[string]any{}}, visits: map[string]int{},
		run: &Run{Workflow: w.Name, Version: w.Version, Case: c.Name}}
	r.run.Context = r.s.data

	if reason := r.trigger(); reason != "" {
		r.run.Status, r.run.Reason = Rejected, reason
		return r.run
	}

	queue := []leg{{id: 1, node: w.Start}}
	r.legs = 1
	steps := 0
	for len(queue) > 0 {
		l := queue[0]
		queue = queue[1:]
		for l.node != nil {
			if steps++; steps > opt.MaxSteps {
				r.run.Status, r.run.At = Runaway, l.node.Index
				r.run.Reason = fmt.Sprintf("stopped after %d steps; the case seems to go round a cycle (last at %s)", opt.MaxSteps, l.node.Index)
				return r.run
			}
			next, forks := r.step(l)
			for _, f := range forks {
				r.legs++
				queue = append(queue, leg{id: r.legs, node: f})
			}
			l.node = next
		}
	}
	r.finish()
	return r.run
}

// trigger fills workflow variables from the submission through the trigger
// map, and applies the type check the platform does before an instance
// exists: a number sent to a string variable fails the handshake, and no
// instance is created at all.
func (r *runner) trigger() string {
	src := map[string]any{
		"attributes": r.c.Submission.Attributes,
		"meta":       r.c.Submission.Meta,
	}
	r.s.set("meta", r.c.Submission.Meta)
	ref, _ := r.c.Submission.Meta["reference"].(string)
	if ref == "" {
		ref = "SIM.TEST.0001"
	}
	r.s.set("reference", ref)

	// Every declared variable exists from the start, null until something
	// sets it. (Recorded instances hold all of them; unset ones are null.)
	for name := range r.w.Variables {
		r.s.set(name, nil)
	}
	for _, name := range keys(r.w.Trigger) {
		path := strings.TrimPrefix(r.w.Trigger[name], "$.")
		v, ok := (&scope{data: src}).Lookup(path)
		vt, declared := r.w.Variables[name]
		if !ok {
			if declared && !vt.Nullable && vt.Base != "" && vt.Base != "file" {
				r.note("submission has no %s for required variable %s; it starts null", r.w.Trigger[name], name)
			}
			continue
		}
		if declared && vt.Base == "string" && v.Kind == expr.Number {
			return fmt.Sprintf("Workflow handshake fail: variable %q is a string but the submission sends the number %s. No instance is created, so nothing shows in the instance list.", name, v.Text())
		}
		r.s.set(name, toAny(v))
	}
	for _, name := range keys(r.c.Variables) {
		r.s.set(name, r.c.Variables[name])
	}
	return ""
}

func (r *runner) note(format string, a ...any) {
	r.run.Notes = append(r.run.Notes, fmt.Sprintf(format, a...))
}

func (r *runner) record(st Step) {
	r.seq++
	st.Seq = r.seq
	r.run.Steps = append(r.run.Steps, st)
}

func (r *runner) milestone(n *workflow.Node) string {
	if n.Milestone == nil {
		return ""
	}
	for _, m := range r.w.Milestones {
		if m.ID == *n.Milestone {
			return m.Name
		}
	}
	return ""
}

// step runs one node and picks where the route goes next. It returns the
// next node (nil when this route ends) and any parallel routes to start.
func (r *runner) step(l leg) (*workflow.Node, []*workflow.Node) {
	n := l.node
	st := Step{Leg: l.id, Node: n.Index, Name: n.Name, Control: n.Control.String(), Milestone: r.milestone(n)}

	out, ev := r.exec(n, &st)
	st.Event = ev.event
	st.Detail = ev.detail
	if out != nil {
		st.Output = out
		r.s.set(n.Index, out)
	}
	if ev.stop != "" {
		r.stops = append(r.stops, stop{ev.stop, n.Index, ev.detail})
		r.record(st)
		return nil, nil
	}
	if n.Control.Terminal() {
		st.Event = "ended"
		r.record(st)
		return nil, nil
	}

	if d := r.fieldDecision(n); d != nil {
		st.Event = "waiting"
		st.Detail = fmt.Sprintf("the case does not give %s, which a transition checks", d.Field)
		r.decide(d)
		r.record(st)
		return nil, nil
	}

	var passing []int
	for i, t := range n.Out {
		res := expr.EvalCondition(t.Condition, r.s, r.opt.Policy)
		e := Edge{To: t.To.Index, Condition: t.Condition, Pass: res.Pass, Unresolved: res.Unresolved}
		if res.Err != nil {
			e.Error = res.Err.Error()
		}
		if t.StateChange != nil {
			e.StateChange = r.flowLabel(*t.StateChange)
		}
		st.Edges = append(st.Edges, e)
		if res.Pass {
			passing = append(passing, i)
		}
	}
	if len(passing) == 0 {
		st.Event = "stuck"
		reason := r.whyStuck(n, st.Edges)
		st.Detail = reason
		r.stops = append(r.stops, stop{Stuck, n.Index, reason})
		r.record(st)
		return nil, nil
	}

	take := passing[:1]
	if r.opt.Fork == AllMatches {
		take = passing
	} else if len(passing) > 1 {
		r.note("%s: %d transitions matched; took the first (to %s). Whether the engine forks here is unsettled.", n.Index, len(passing), n.Out[passing[0]].To.Index)
	}
	var next *workflow.Node
	var forks []*workflow.Node
	for k, i := range take {
		t := n.Out[i]
		st.Edges[i].Taken = true
		if t.StateChange != nil {
			r.run.FlowState = r.flowLabel(*t.StateChange)
		}
		if k == 0 {
			next = t.To
		} else {
			forks = append(forks, t.To)
		}
	}
	r.record(st)
	return next, forks
}

// decide stops this route on a decision (explore mode).
func (r *runner) decide(d *Decision) {
	if r.run.Pending == nil {
		r.run.Pending = d
	}
	r.stops = append(r.stops, stop{Waiting, d.Node, fmt.Sprintf("needs a decision: %s %s", d.Key, d.Kind)})
}

// fieldDecision finds a task form field that a reference in src reads but
// the case does not answer, for a task that has already run. Only in
// explore mode, and only for references in refs.
func (r *runner) fieldRef(refs []string, at string) *Decision {
	if !r.opt.Explore {
		return nil
	}
	for _, ref := range refs {
		parts := strings.Split(ref, ".")
		if d := r.variableRef(ref, parts, at); d != nil {
			return d
		}
		if len(parts) < 3 {
			continue
		}
		t := r.w.NodeByIndex(parts[0])
		if t == nil || t.Control != workflow.CtrlAssignTask {
			continue
		}
		if _, ran := r.s.data[t.Index]; !ran {
			continue
		}
		if _, ok := r.s.Lookup(ref); ok {
			continue
		}
		if slugs := t.FormSlugs(); len(slugs) > 0 && !contains(slugs, parts[1]) {
			continue // a form this task never submits: always missing (lint reports it)
		}
		field := strings.Join(parts[1:], ".")
		vals := compared(r.w, ref)
		blankChecked := false
		var nonBlank []string
		for _, v := range vals {
			if v == "" {
				blankChecked = true
			} else {
				nonBlank = append(nonBlank, v)
			}
		}
		var opts []Option
		switch {
		case usedInArithmetic(r.w, ref):
			// A number on the real form: the numbers the workflow compares
			// it with, or 1, plus blank if the workflow guards for blank.
			for _, v := range nonBlank {
				if _, err := strconv.ParseFloat(v, 64); err == nil {
					opts = append(opts, Option{Label: v, Value: v})
				}
			}
			if len(opts) == 0 {
				opts = []Option{{Label: "1", Value: "1"}}
			}
			if blankChecked {
				opts = append(opts, Option{Label: "(left blank)", Value: ""})
			}
		case len(nonBlank) > 0:
			opts = valuesOrDefault(vals)
			opts = append(opts, Option{Label: Other, Value: Other})
		case blankChecked:
			opts = []Option{{Label: "(left blank)", Value: ""}, {Label: "(filled in)", Value: "sample"}}
		default:
			opts = []Option{{Label: "(left blank)", Value: ""}}
		}
		for i := range opts {
			if opts[i].Label == "" {
				opts[i].Label = "(left blank)"
			}
		}
		if !r.opt.AllDecisions && !fieldMatters(r.w, ref) {
			opts = opts[:1] // cannot change the route: one representative answer
		}
		return &Decision{Kind: TaskField, Key: t.Index, Visit: max(0, r.visits[t.Index]-1), Field: field, Node: at, Options: opts}
	}
	return nil
}

// variableRef: a submitted value the base case leaves out (null in the
// context) that a condition or expression uses. Same options as a form
// field; one representative value if it cannot change the route.
func (r *runner) variableRef(ref string, parts []string, at string) *Decision {
	if len(parts) != 1 {
		return nil
	}
	if _, declared := r.w.Variables[ref]; !declared {
		return nil
	}
	if _, given := r.c.Variables[ref]; given || r.submitted(ref) {
		return nil
	}
	if v, ok := r.s.Lookup(ref); ok && !v.IsNull() {
		return nil
	}
	var opts []Option
	vals := compared(r.w, ref)
	var nonBlank []string
	blankChecked := false
	for _, v := range vals {
		if v != "" {
			nonBlank = append(nonBlank, v)
		} else {
			blankChecked = true
		}
	}
	switch {
	case usedInArithmetic(r.w, ref):
		opts = []Option{{Label: "1", Value: 1.0}}
		for _, v := range nonBlank {
			if f, err := strconv.ParseFloat(v, 64); err == nil && v != "1" {
				opts = append(opts, Option{Label: v, Value: f})
			}
		}
	case len(nonBlank) > 0:
		opts = valuesOrDefault(nonBlank)
		opts = append(opts, Option{Label: Other, Value: Other})
	case blankChecked:
		// Only checked for blank ($.loanName0 != ""): whether it was
		// filled in is the decision.
		opts = []Option{{Label: "(left blank)", Value: ""}, {Label: "(filled in)", Value: "sample"}}
	default:
		return nil // only displayed or stored; null is a fine stand-in
	}
	if !r.opt.AllDecisions && !fieldMatters(r.w, ref) {
		opts = opts[:1]
	}
	return &Decision{Kind: Variable, Key: ref, Visit: -1, Node: at, Options: opts}
}

func (r *runner) fieldDecision(n *workflow.Node) *Decision {
	var refs []string
	for _, t := range n.Out {
		refs = append(refs, expr.Refs(t.Condition)...)
	}
	return r.fieldRef(refs, n.Index)
}

func (r *runner) flowLabel(id int64) string {
	if fs, ok := r.w.FlowStates[id]; ok {
		if fs.PublicLabel != "" {
			return fs.PublicLabel
		}
		return fs.Label
	}
	return fmt.Sprintf("flow state %d", id)
}

// whyStuck explains, in plain words, why no transition matched.
func (r *runner) whyStuck(n *workflow.Node, edges []Edge) string {
	if len(edges) == 0 {
		return fmt.Sprintf("%s has no way out, so the case stops here without reaching an end", n.Index)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s finished but none of its %d transitions matched:", n.Index, len(edges))
	for _, e := range edges {
		fmt.Fprintf(&b, "\n  to %s when %s", e.To, e.Condition)
		vals := r.describeRefs(e.Condition)
		if vals != "" {
			fmt.Fprintf(&b, "  (%s)", vals)
		}
		if e.Error != "" {
			fmt.Fprintf(&b, "  [condition error: %s]", e.Error)
		}
	}
	return b.String()
}

func (r *runner) describeRefs(cond string) string {
	var parts []string
	for _, ref := range expr.Refs(cond) {
		if v, ok := r.s.Lookup(ref); ok {
			parts = append(parts, fmt.Sprintf("%s is %s", ref, v))
		} else {
			parts = append(parts, ref+" has no value")
		}
	}
	return strings.Join(parts, ", ")
}

func (r *runner) finish() {
	run := r.run
	if len(r.stops) == 0 {
		run.Status = Completed
		run.Reason = "every route reached an end"
		return
	}
	// The worst stop decides the status.
	rank := map[Status]int{Failed: 3, Stuck: 2, Waiting: 1}
	worst := r.stops[0]
	for _, s := range r.stops[1:] {
		if rank[s.status] > rank[worst.status] {
			worst = s
		}
	}
	run.Status, run.At, run.Reason = worst.status, worst.node, worst.reason
}

func toAny(v expr.Value) any {
	switch v.Kind {
	case expr.Null:
		return nil
	case expr.Number:
		return v.Num
	case expr.String:
		return v.Str
	case expr.Bool:
		return v.Bool
	case expr.List:
		out := make([]any, len(v.List))
		for i, e := range v.List {
			out[i] = toAny(e)
		}
		return out
	}
	out := map[string]any{}
	for k, e := range v.Map {
		out[k] = toAny(e)
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
