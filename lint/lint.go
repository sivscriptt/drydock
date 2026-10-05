// Package lint checks every expression and transition condition in a
// workflow without running it.
package lint

import (
	"fmt"
	"sort"
	"strings"

	"github.com/sivscriptt/drydock/expr"
	"github.com/sivscriptt/drydock/workflow"
)

// Finding is one problem in one place.
type Finding struct {
	Node    string // node index
	Where   string // "output id_expression" or "condition to n20"
	Code    string
	Fails   bool // the live engine fails the instance, rather than giving a wrong answer
	Message string
}

func (f Finding) String() string {
	kind := "wrong"
	if f.Fails {
		kind = "FAILS"
	}
	return fmt.Sprintf("%-6s %-6s %-34s %-30s %s", f.Node, kind, f.Where, f.Code, f.Message)
}

// Stats counts what was checked.
type Stats struct {
	Expressions, Conditions int
}

// Workflow checks every Expression V2 output and every transition condition.
func Workflow(w *workflow.Workflow) ([]Finding, Stats) {
	var out []Finding
	var st Stats
	for _, n := range w.Nodes {
		if n.Control != workflow.CtrlExpressionV2 {
			continue
		}
		exprs, _ := n.Params["expressions"].([]any)
		keys := map[string]bool{}
		for _, e := range exprs {
			m, _ := e.(map[string]any)
			key, _ := m["key"].(string)
			src, _ := m["expression"].(string)
			st.Expressions++
			where := "output " + key
			for _, h := range expr.Hazards(src) {
				f := Finding{n.Index, where, h.Code, h.Fails, h.Message}
				if h.Code == "guard-does-not-protect" {
					lookup := strings.SplitN(h.Ref, ".", 2)[0]
					if gatedOnCount(w, n, lookup) {
						continue // the node only runs when the lookup found a row
					}
					f.Message = fmt.Sprintf("$.{%s} is read inside ifCondition, and some route reaches %s without a transition that checks %s.count. References are pasted in before parsing, so on that route an empty lookup fails the instance. Gate the node on the count.", h.Ref, n.Index, lookup)
				}
				out = append(out, f)
			}
			// Reading a sibling output in the same node. One defined earlier
			// may resolve; the docs call it unverified. One defined later
			// (or the output itself) cannot have a value yet.
			for _, r := range expr.Refs(src) {
				prefix := n.Index + ".output."
				if !strings.HasPrefix(r, prefix) {
					continue
				}
				sib := strings.SplitN(strings.TrimPrefix(r, prefix), ".", 2)[0]
				if keys[sib] {
					out = append(out, Finding{n.Index, where, "same-node-earlier", false,
						fmt.Sprintf("reads $.{%s}, an earlier output of the same node. The docs say this may resolve but is unverified on the live engine; a separate node is safer.", r)})
				} else {
					out = append(out, Finding{n.Index, where, "same-node-forward", true,
						fmt.Sprintf("reads $.{%s}, which this node has not produced yet at this point. It stays unresolved and the instance fails.", r)})
				}
			}
			keys[key] = true
			if ok, err := parsesWithPlaceholders(src); !ok {
				out = append(out, Finding{n.Index, where, "syntax", true, err.Error()})
			}
		}
	}
	out = append(out, staleSlugs(w)...)
	for _, t := range w.Transitions {
		if t.Condition == "" {
			continue
		}
		st.Conditions++
		if _, err := expr.Parse(t.Condition, expr.ConditionMode); err != nil {
			out = append(out, Finding{t.From.Index, "condition to " + t.To.Index, "syntax", true, err.Error()})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Fails && !out[j].Fails })
	return out, st
}

// gatedOnCount reports whether every route from the start to target passes
// a transition that is false when the lookup node found nothing. If so, the
// target cannot run after an empty lookup, and reading its rows is safe.
func gatedOnCount(w *workflow.Workflow, target *workflow.Node, lookup string) bool {
	empty := expr.MapContext{lookup: map[string]any{"count": 0, "output": []any{}}}
	gate := func(t *workflow.Transition) bool {
		if !strings.Contains(t.Condition, "$."+lookup+".count") {
			return false
		}
		return !expr.EvalCondition(t.Condition, empty, expr.Live()).Pass
	}
	// Walk the graph without crossing gating transitions. If the target
	// is still reachable, some route skips the gate.
	if target.Loop != nil {
		target = target.Loop
	}
	seen := map[*workflow.Node]bool{}
	stack := []*workflow.Node{w.Start}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n == nil || seen[n] {
			continue
		}
		if n == target {
			return false
		}
		seen[n] = true
		for _, t := range n.Out {
			if !gate(t) {
				stack = append(stack, t.To)
			}
		}
	}
	return true
}

// parsesWithPlaceholders checks the expression's own syntax by pasting a
// harmless value for every reference, so only the author's text is judged.
func parsesWithPlaceholders(src string) (bool, error) {
	text, _ := expr.Substitute(src, anything{}, expr.Live(), expr.KeepLiteral)
	if _, err := expr.Parse(text, expr.ExpressionMode); err != nil {
		// A whole-string template is fine even if it would not parse.
		if r := expr.EvalExpression(src, "string", anything{}, expr.Live()); r.Err == nil {
			return true, nil
		}
		return false, err
	}
	return true, nil
}

// anything resolves every reference to 1, a value that pastes bare and
// keeps arithmetic and comparisons parseable.
type anything struct{}

func (anything) Lookup(string) (expr.Value, bool) { return expr.Num(1), true }

// staleSlugs finds references to a form a task does not submit, typically
// left behind when the task's form moved to a new version
// ($.n5.form-v2.field when n5 now submits form-v3). Such a reference never
// has a value: a condition on it never matches, an Expression V2 output
// reading it bare fails.
func staleSlugs(w *workflow.Workflow) []Finding {
	var out []Finding
	seen := map[string]bool{}
	check := func(where, at, src string) {
		for _, ref := range expr.Refs(src) {
			p := strings.Split(ref, ".")
			if len(p) < 3 {
				continue
			}
			t := w.NodeByIndex(p[0])
			if t == nil {
				continue
			}
			slugs := t.FormSlugs()
			if len(slugs) == 0 {
				continue
			}
			found := false
			for _, s := range slugs {
				if s == p[1] {
					found = true
				}
			}
			key := at + "|" + where + "|" + p[0] + "." + p[1]
			if found || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, Finding{at, where, "stale-form-slug", false,
				fmt.Sprintf("reads %s.%s, but %s submits %s. This reference never has a value.", p[0], p[1], p[0], strings.Join(slugs, ", "))})
		}
	}
	for _, t := range w.Transitions {
		check("condition to "+t.To.Index, t.From.Index, t.Condition)
	}
	for _, n := range w.Nodes {
		exprs, _ := n.Params["expressions"].([]any)
		for _, e := range exprs {
			m, _ := e.(map[string]any)
			k, _ := m["key"].(string)
			src, _ := m["expression"].(string)
			check("output "+k, n.Index, src)
		}
	}
	return out
}
