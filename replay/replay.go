// Package replay checks the simulator against a real recorded instance: it
// rebuilds the case from the instance's context, runs it, and compares the
// route and every substituted expression with what the engine recorded.
package replay

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/sivscriptt/drydock/expr"
	"github.com/sivscriptt/drydock/sim"
	"github.com/sivscriptt/drydock/workflow"
)

// Leg is one node execution recorded by the engine.
type Leg struct {
	ID        int64  `json:"id"`
	NodeID    int64  `json:"workflow_node_id"`
	State     int    `json:"state_id"` // 8 = passed
	CreatedAt string `json:"created_at"`
	Data      any    `json:"leg_data"`
}

// Recording is an instance's context snapshot and its legs.
type Recording struct {
	Context map[string]any
	Legs    []Leg
}

// Load reads a context file (context_data, wrapped or bare) and a legs file
// (a list, or {data: [...]}).
func Load(contextPath, legsPath string) (*Recording, error) {
	var rec Recording
	var raw map[string]any
	if err := readJSON(contextPath, &raw); err != nil {
		return nil, err
	}
	rec.Context = raw
	if cd, ok := raw["context_data"]; ok {
		switch t := cd.(type) {
		case map[string]any:
			rec.Context = t
		case string:
			var m map[string]any
			if err := json.Unmarshal([]byte(t), &m); err != nil {
				return nil, fmt.Errorf("%s: context_data: %w", contextPath, err)
			}
			rec.Context = m
		}
	}
	var legs json.RawMessage
	if err := readJSON(legsPath, &legs); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(legs, &rec.Legs); err != nil {
		var wrapped struct {
			Data []Leg `json:"data"`
		}
		if err2 := json.Unmarshal(legs, &wrapped); err2 != nil {
			return nil, fmt.Errorf("%s: %w", legsPath, err)
		}
		rec.Legs = wrapped.Data
	}
	sort.SliceStable(rec.Legs, func(i, j int) bool {
		if rec.Legs[i].CreatedAt != rec.Legs[j].CreatedAt {
			return rec.Legs[i].CreatedAt < rec.Legs[j].CreatedAt
		}
		return rec.Legs[i].ID < rec.Legs[j].ID
	})
	return &rec, nil
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// Mismatch is one place the simulator disagreed with the engine.
type Mismatch struct {
	Node    string `json:"node"`
	Key     string `json:"key,omitempty"`
	Engine  string `json:"engine"`
	Drydock string `json:"drydock"`
}

// Check counts one kind of comparison.
type Check struct {
	Checked    int        `json:"checked"`
	Matched    int        `json:"matched"`
	Mismatches []Mismatch `json:"mismatches,omitempty"`
}

func (c *Check) add(ok bool, m Mismatch) {
	c.Checked++
	if ok {
		c.Matched++
	} else {
		c.Mismatches = append(c.Mismatches, m)
	}
}

// Report is the outcome of a replay.
type Report struct {
	Workflow string `json:"workflow"`
	Version  string `json:"version"`

	Recorded  []string `json:"recorded_route"`
	Simulated []string `json:"simulated_route"`
	// RouteMatches is true when the simulated route equals the recorded one.
	// Diverge is the first position where they differ, or -1.
	RouteMatches bool `json:"route_matches"`
	Diverge      int  `json:"diverge"`

	Substitution Check `json:"substitution"` // expression text after substitution
	Outputs      Check `json:"outputs"`      // expression values vs stored outputs

	Status   sim.Status `json:"status"`
	Reason   string     `json:"reason"`
	Inferred []string   `json:"inferred"` // answers worked out from the route, not the context
	Run      *sim.Run   `json:"-"`

	runtime map[string]recovered
}

// Replay rebuilds the case from the recording, runs it, and compares.
func Replay(w *workflow.Workflow, rec *Recording, opt sim.Options) *Report {
	rep := &Report{Workflow: w.Name, Version: w.Version, Diverge: -1}
	for _, l := range rec.Legs {
		if n := w.Node(l.NodeID); n != nil && n.Loop == nil {
			rep.Recorded = append(rep.Recorded, n.Index)
		}
	}

	c := rep.buildCase(w, rec)
	// The clock is the time of the first recorded leg, so date functions
	// give the values the engine computed.
	if len(rec.Legs) > 0 {
		if t, err := time.Parse(time.RFC3339Nano, rec.Legs[0].CreatedAt); err == nil {
			opt.Policy.Now = func() time.Time { return t }
		}
	}
	// Simulate; where the run leaves the recorded route at a branch, the
	// condition on the transition the engine really took says what the
	// answers were. Apply that and run again, a bounded number of times.
	var run *sim.Run
	for attempt := 0; attempt < 20; attempt++ {
		run = sim.Simulate(w, c, opt)
		rep.Simulated = rep.Simulated[:0]
		for _, s := range run.Steps {
			rep.Simulated = append(rep.Simulated, s.Node)
		}
		if !rep.correctFromRoute(w, c) {
			break
		}
	}
	rep.Run, rep.Status, rep.Reason = run, run.Status, run.Reason

	rep.RouteMatches = len(rep.Recorded) == len(rep.Simulated)
	for i := 0; i < max(len(rep.Recorded), len(rep.Simulated)); i++ {
		if i >= len(rep.Recorded) || i >= len(rep.Simulated) || rep.Recorded[i] != rep.Simulated[i] {
			rep.RouteMatches = false
			rep.Diverge = i
			break
		}
	}

	rep.compareExpressions(w, rec, run)
	return rep
}

// buildCase turns the recorded context into a case: variables and meta as
// they were, task outcomes and lookup rows from node outputs. Where the
// context snapshot is older than the legs, the missing answers are worked
// out from the route the instance actually took.
func (rep *Report) buildCase(w *workflow.Workflow, rec *Recording) *sim.Case {
	c := &sim.Case{
		Name:      "replay",
		Variables: map[string]any{},
		Tasks:     map[string]sim.TaskAnswers{},
		Lookups:   map[string]any{},
		Payments:  map[string]string{},
		External:  map[string]map[string]any{},
	}
	meta, _ := rec.Context["meta"].(map[string]any)
	c.Submission.Meta = meta
	// Everything in the context that is not meta or a node's output is a
	// workflow variable. (Some exports carry no variable list, so the
	// context is the better source.)
	for k, v := range rec.Context {
		if k == "meta" || nodeKey.MatchString(k) {
			continue
		}
		c.Variables[k] = v
	}
	if ref, ok := meta["reference"].(string); ok {
		c.Variables["reference"] = ref
	}

	for _, n := range w.Nodes {
		out, ok := rec.Context[n.Index].(map[string]any)
		if !ok {
			continue
		}
		switch n.Control {
		case workflow.CtrlAssignTask:
			a := sim.TaskAnswer{Form: map[string]map[string]any{}}
			a.State, _ = out["state"].(string)
			a.Remarks, _ = out["remarks"].(string)
			for k, v := range out {
				if m, ok := v.(map[string]any); ok && k != "metadata" {
					a.Form[k] = m
				}
			}
			if a.State != "" {
				c.SetTask(n.Index, a)
			}
		case workflow.CtrlDataHub:
			if rows, ok := out["output"].([]any); ok {
				c.Lookups[n.Index] = rows
			}
		case workflow.CtrlPaymentStatus:
			if s, ok := out["invoice_status"].(string); ok {
				c.Payments[n.Index] = s
			}
		case workflow.CtrlExternalState:
			c.External[n.Index] = out
		}
	}

	// Runtime values from leg_data win over the snapshot: they are what the
	// engine actually used. Only inputs are taken (variables, task form
	// fields, lookup columns); values expressions compute are left for the
	// simulator to work out, so comparing them is not circular.
	rep.runtime = recoverValues(w, rec)
	rep.applyRecovered(w, c, rep.runtime)

	// Fill gaps from the route: if the instance went from a task to node X,
	// the condition on that transition says what the task's answer was.
	for i := 0; i+1 < len(rep.Recorded); i++ {
		n := w.NodeByIndex(rep.Recorded[i])
		next := rep.Recorded[i+1]
		if n == nil {
			continue
		}
		var cond string
		for _, t := range n.Out {
			if t.To.Index == next {
				cond = t.Condition
			}
		}
		switch n.Control {
		case workflow.CtrlAssignTask:
			if _, known := c.Tasks[n.Index]; known {
				continue
			}
			a := answerFromCondition(n.Index, cond)
			if a.State == "" {
				a.State = "Completed"
			}
			c.SetTask(n.Index, a)
			rep.Inferred = append(rep.Inferred, fmt.Sprintf("%s: officer answer %q, from the route to %s", n.Index, a.State, next))
		case workflow.CtrlDataHub:
			if _, known := c.Lookups[n.Index]; known {
				continue
			}
			if op, _ := n.Params["operation"].(string); !strings.EqualFold(op, "select") {
				continue
			}
			rows := rowsFromCondition(n.Index, cond)
			c.Lookups[n.Index] = rows
			rep.Inferred = append(rep.Inferred, fmt.Sprintf("%s: lookup found %d row(s), from the route to %s", n.Index, len(rows), next))
		}
	}
	return c
}

// correctFromRoute finds the first step where the simulation left the
// recorded route and, if the engine's next node was reached by a condition
// on task answers or a lookup count, sets those answers. It reports whether
// it changed anything.
func (rep *Report) correctFromRoute(w *workflow.Workflow, c *sim.Case) bool {
	for i := 0; i+1 < len(rep.Recorded); i++ {
		if i >= len(rep.Simulated) || rep.Simulated[i] != rep.Recorded[i] {
			return false // diverged before this point for another reason
		}
		if i+1 < len(rep.Simulated) && rep.Simulated[i+1] == rep.Recorded[i+1] {
			continue
		}
		n := w.NodeByIndex(rep.Recorded[i])
		if n == nil {
			return false
		}
		var cond string
		for _, t := range n.Out {
			if t.To.Index == rep.Recorded[i+1] {
				cond = t.Condition
			}
		}
		changed := false
		for _, tm := range regexp.MustCompile(`\$\.(n\d+)\.`).FindAllStringSubmatch(cond, -1) {
			src := w.NodeByIndex(tm[1])
			if src == nil {
				continue
			}
			switch src.Control {
			case workflow.CtrlAssignTask:
				want := answerFromCondition(src.Index, cond)
				a := c.Task(src.Index)
				if a.Form == nil {
					a.Form = map[string]map[string]any{}
				}
				if want.State != "" && a.State != want.State {
					rep.Inferred = append(rep.Inferred, fmt.Sprintf("%s: task state was %q, not %q as the snapshot says, from the route %s → %s", src.Index, want.State, a.State, n.Index, rep.Recorded[i+1]))
					a.State, changed = want.State, true
				}
				for slug, fields := range want.Form {
					if a.Form[slug] == nil {
						a.Form[slug] = map[string]any{}
					}
					for f, v := range fields {
						if a.Form[slug][f] != v {
							a.Form[slug][f], changed = v, true
							rep.Inferred = append(rep.Inferred, fmt.Sprintf("%s: %s.%s was %q, from the route %s → %s", src.Index, slug, f, v, n.Index, rep.Recorded[i+1]))
						}
					}
				}
				c.SetTask(src.Index, a)
			case workflow.CtrlDataHub:
				rows := rowsFromCondition(src.Index, cond)
				if old, _ := c.Lookups[src.Index].([]any); len(old) != len(rows) {
					c.Lookups[src.Index], changed = rows, true
					rep.Inferred = append(rep.Inferred, fmt.Sprintf("%s: lookup found %d row(s), from the route %s → %s", src.Index, len(rows), n.Index, rep.Recorded[i+1]))
				}
			}
		}
		return changed
	}
	return false
}

func (rep *Report) applyRecovered(w *workflow.Workflow, c *sim.Case, vals map[string]recovered) {
	changed := 0
	for _, ref := range sortedRecovered(vals) {
		v := vals[ref]
		parts := strings.Split(ref, ".")
		root := w.NodeByIndex(parts[0])
		switch {
		case root == nil && len(parts) == 1:
			if old, had := c.Variables[ref]; !had || !sameValue(old, v.value) {
				changed++
			}
			c.Variables[ref] = v.value
		case root != nil && root.Control == workflow.CtrlAssignTask && len(parts) == 3:
			a := c.Task(parts[0])
			if a.Form == nil {
				a.Form = map[string]map[string]any{}
			}
			if a.Form[parts[1]] == nil {
				a.Form[parts[1]] = map[string]any{}
			}
			a.Form[parts[1]][parts[2]] = v.value
			c.SetTask(parts[0], a)
			changed++
		}
	}
	if changed > 0 {
		rep.Inferred = append(rep.Inferred, fmt.Sprintf("%d input value(s) taken from leg_data, where the engine recorded what it actually used", changed))
	}
}

func sortedRecovered(m map[string]recovered) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

var nodeKey = regexp.MustCompile(`^n\d+$`)

var (
	stateEq = `\$\.%s\.state\s*==\s*"([^"]*)"`
	fieldEq = `\$\.%s\.([A-Za-z0-9_\-]+)\.([A-Za-z0-9_\-]+)\s*==\s*"([^"]*)"`
	countIs = `\$\.%s\.count\s*(==|!=|>|>=)\s*"?(\d+)"?`
)

func answerFromCondition(idx, cond string) sim.TaskAnswer {
	a := sim.TaskAnswer{Form: map[string]map[string]any{}}
	if m := regexp.MustCompile(fmt.Sprintf(stateEq, regexp.QuoteMeta(idx))).FindStringSubmatch(cond); m != nil {
		a.State = m[1]
	}
	for _, m := range regexp.MustCompile(fmt.Sprintf(fieldEq, regexp.QuoteMeta(idx))).FindAllStringSubmatch(cond, -1) {
		if a.Form[m[1]] == nil {
			a.Form[m[1]] = map[string]any{}
		}
		a.Form[m[1]][m[2]] = m[3]
	}
	return a
}

func rowsFromCondition(idx, cond string) []any {
	m := regexp.MustCompile(fmt.Sprintf(countIs, regexp.QuoteMeta(idx))).FindStringSubmatch(cond)
	if m == nil {
		return []any{}
	}
	empty := (m[1] == "==" && m[2] == "0") || (m[1] == "!=" && m[2] != "0")
	if empty {
		return []any{}
	}
	return []any{map[string]any{}}
}

// compareExpressions checks each recorded Expression V2 leg: the text after
// substitution must match leg_data exactly, and the values must match the
// outputs stored in the context.
func (rep *Report) compareExpressions(w *workflow.Workflow, rec *Recording, run *sim.Run) {
	simByNode := map[string]sim.Step{}
	for _, s := range run.Steps {
		if _, seen := simByNode[s.Node]; !seen {
			simByNode[s.Node] = s
		}
	}
	for _, l := range rec.Legs {
		n := w.Node(l.NodeID)
		if n == nil || n.Control != workflow.CtrlExpressionV2 {
			continue
		}
		st, ran := simByNode[n.Index]
		data, _ := l.Data.(map[string]any)
		recorded, _ := data["expressions"].([]any)
		simExprs := map[string]sim.ExprTrace{}
		for _, e := range st.Exprs {
			simExprs[e.Key] = e
		}
		for _, e := range recorded {
			m, _ := e.(map[string]any)
			key, _ := m["key"].(string)
			text, _ := m["expression"].(string)
			got, ok := simExprs[key]
			if !ran || !ok {
				rep.Substitution.add(false, Mismatch{n.Index, key, text, "(not evaluated: node did not run in the simulation)"})
				continue
			}
			rep.Substitution.add(got.Substituted == text, Mismatch{n.Index, key, text, got.Substituted})
		}

	}

	// Outputs: compare each value the simulator computed with the value the
	// engine pasted wherever a later node used it. That is the engine's real
	// output for this run; the context snapshot can predate it.
	for _, s := range run.Steps {
		for _, e := range s.Exprs {
			if e.Error != "" {
				continue
			}
			v, ok := rep.runtime[s.Node+".output."+e.Key]
			if !ok {
				continue
			}
			rep.Outputs.add(sameValue(v.value, e.Value), Mismatch{s.Node, e.Key, fmt.Sprint(v.value), fmt.Sprint(e.Value)})
		}
	}
}

// sameValue compares a stored output with a simulated one: numbers by value
// (6 and 6.0 are equal), everything else by its text.
func sameValue(a, b any) bool {
	va, vb := expr.FromAny(a), expr.FromAny(b)
	if va.Kind == expr.Number && vb.Kind == expr.Number {
		return va.Num == vb.Num
	}
	return va.Text() == vb.Text()
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
