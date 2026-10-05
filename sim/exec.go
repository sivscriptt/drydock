package sim

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/sivscriptt/drydock/expr"
	"github.com/sivscriptt/drydock/workflow"
)

type outcome struct {
	event  string
	detail string
	stop   Status // set when this route stops here
}

func ran(detail string) outcome { return outcome{event: "ran", detail: detail} }

func (r *runner) exec(n *workflow.Node, st *Step) (any, outcome) {
	key := r.caseKey(n)
	switch n.Control {
	case workflow.CtrlNoAction:
		return map[string]any{}, ran("")
	case workflow.CtrlEnd, workflow.CtrlFormat:
		return nil, ran("")
	case workflow.CtrlAssignTask:
		return r.task(n, key, st)
	case workflow.CtrlExpressionV2:
		return r.expressions(n, st)
	case workflow.CtrlDataHub:
		return r.datahub(n, key, st)
	case workflow.CtrlLoop:
		return r.loop(n, st)
	case workflow.CtrlPaymentStatus:
		status, ok := r.c.Payments[key]
		if !ok && r.opt.Explore {
			r.run.Pending = firstDecision(r.run.Pending, &Decision{Kind: Payment, Key: key, Node: n.Index,
				Options: valuesOrDefault(compared(r.w, n.Index+".invoice_status"), "paid")})
		}
		if !ok {
			return nil, outcome{"waiting", fmt.Sprintf("the route pauses here until the invoice is paid, and the case does not say what happens (add payments: {%s: paid})", key), Waiting}
		}
		return map[string]any{"state": "success", "invoice_status": status}, ran("invoice " + status)
	case workflow.CtrlExternalState, workflow.CtrlRunQuery:
		ans, ok := r.c.External[key]
		if !ok && r.opt.Explore {
			r.run.Pending = firstDecision(r.run.Pending, &Decision{Kind: External, Key: key, Node: n.Index,
				Options: valuesOrDefault(compared(r.w, n.Index+".next_state"), "done")})
		}
		if !ok {
			return nil, outcome{"waiting", fmt.Sprintf("%s needs an answer from outside the workflow; add external: {%s: {...}} to the case", n.Control, key), Waiting}
		}
		out := map[string]any{"state": "success"}
		for k, v := range ans {
			out[k] = v
		}
		return out, ran("")
	case workflow.CtrlGenerateFile, workflow.CtrlGenerateFile2:
		r.fill(n, st)
		return map[string]any{"state": "success", "file": "drydock://generated/" + n.Index + ".pdf"}, ran("document generated")
	case workflow.CtrlDCN:
		r.fill(n, st)
		return map[string]any{"state": "success", "dcn_number": fmt.Sprintf("DCN-SIM-%s", strings.TrimPrefix(n.Index, "n"))}, ran("DCN issued")
	case workflow.CtrlInvoice:
		r.fill(n, st)
		return map[string]any{"state": "success", "invoice_number": "INV-SIM-" + strings.TrimPrefix(n.Index, "n")}, ran("invoice raised")
	case workflow.CtrlExpressionV1:
		r.note("%s is an Expression v1 node; Drydock does not evaluate v1 yet, so its outputs are empty", n.Index)
		return map[string]any{"state": "success", "output": map[string]any{}}, ran("not evaluated (v1)")
	}
	// Messages and records: SMS, email, notes, info, EDMS, Locker, G2G.
	return map[string]any{"state": "success"}, ran(r.fill(n, st))
}

// caseKey is how a case file names this node: "n12", or "n5/lm1" inside a loop.
func (r *runner) caseKey(n *workflow.Node) string {
	if n.Loop != nil {
		return n.Loop.Index + "/" + n.Index
	}
	return n.Index
}

// fill interpolates the node's parameters the way the platform does for
// every control except Expression V2: a missing reference becomes blank.
// It notes each blank, since that is text a citizen or officer will see.
func (r *runner) fill(n *workflow.Node, st *Step) string {
	filled, subs := interpolateDeep(n.Params, r.s)
	st.Subs = append(st.Subs, subs...)
	for _, s := range subs {
		if !s.Found {
			r.note("%s (%s): $.{%s} had no value, so it shows as a blank", n.Index, n.Control, s.Ref)
		}
	}
	_ = filled
	// The step detail shows the text as a citizen or officer would read it,
	// with every empty value marked, so a message full of gaps stands out.
	for _, k := range []string{"message", "body", "content", "text", "subject", "title", "description", "notes", "note"} {
		if tmpl, ok := n.Params[k].(string); ok && strings.TrimSpace(tmpl) != "" {
			return markBlanks(tmpl, r.s)
		}
	}
	return ""
}

// markBlanks fills a template like the platform does, but writes [blank]
// where a reference has no value or an empty one.
func markBlanks(tmpl string, ctx expr.Context) string {
	text, subs := expr.Interpolate(tmpl, ctx)
	if len(subs) == 0 {
		return text
	}
	var out strings.Builder
	rest := tmpl
	for _, sub := range subs {
		i := strings.Index(rest, "$.{")
		j := strings.IndexByte(rest[i:], '}')
		if i < 0 || j < 0 {
			break
		}
		out.WriteString(rest[:i])
		if v := sub.Value.Text(); sub.Found && strings.TrimSpace(v) != "" {
			out.WriteString(v)
		} else {
			out.WriteString("[blank]")
		}
		rest = rest[i+j+1:]
	}
	out.WriteString(rest)
	return out.String()
}

func interpolateDeep(v any, ctx expr.Context) (any, []expr.Sub) {
	switch t := v.(type) {
	case string:
		s, subs := expr.Interpolate(t, ctx)
		return s, subs
	case map[string]any:
		out := make(map[string]any, len(t))
		var subs []expr.Sub
		for _, k := range keys(t) {
			fv, s := interpolateDeep(t[k], ctx)
			out[k] = fv
			subs = append(subs, s...)
		}
		return out, subs
	case []any:
		out := make([]any, len(t))
		var subs []expr.Sub
		for i, e := range t {
			fv, s := interpolateDeep(e, ctx)
			out[i] = fv
			subs = append(subs, s...)
		}
		return out, subs
	}
	return v, nil
}

func (r *runner) task(n *workflow.Node, key string, st *Step) (any, outcome) {
	title, _ := n.Params["title"].(string)
	title, _ = expr.Interpolate(title, r.s)
	visit := r.visits[key]
	r.visits[key]++
	ans, ok := r.c.answer(key, visit, r.opt.Explore)
	if !ok {
		if r.opt.Explore {
			opts := taskStates(r.w, n.Index)
			r.run.Pending = firstDecision(r.run.Pending, &Decision{Kind: TaskState, Key: key, Visit: visit, Node: n.Index, Options: opts})
		}
		return nil, outcome{"waiting", fmt.Sprintf("the case does not say what the officer does with %q. %s", title, r.taskOptions(n, key)), Waiting}
	}
	now := r.opt.Policy.Now
	stamp := time.Now()
	if now != nil {
		stamp = now()
	}
	// Recorded contexts give every task output these timestamps, and
	// workflows read them ($.{n22.updated_at}).
	ts := stamp.Format("2006-01-02 15:04:05")
	out := map[string]any{"state": ans.State, "remarks": ans.Remarks, "task_uuid": "sim-task-" + n.Index,
		"created_at": ts, "updated_at": ts}
	for slug, fields := range ans.Form {
		out[slug] = fields
	}
	detail := fmt.Sprintf("%s → %s", title, ans.State)
	return out, outcome{event: "ran", detail: detail}
}

// taskOptions reads the node's outgoing conditions to tell the case author
// which answers this task's transitions look at.
func (r *runner) taskOptions(n *workflow.Node, key string) string {
	states := map[string]bool{}
	fields := map[string]map[string]bool{}
	stateRe := regexp.MustCompile(`\$\.` + regexp.QuoteMeta(n.Index) + `\.state\s*[!=]=\s*"([^"]*)"`)
	fieldRe := regexp.MustCompile(`\$\.` + regexp.QuoteMeta(n.Index) + `\.([A-Za-z0-9_\-]+)\.([A-Za-z0-9_\-]+)\s*[!=]=\s*"([^"]*)"`)
	for _, t := range n.Out {
		for _, m := range stateRe.FindAllStringSubmatch(t.Condition, -1) {
			states[m[1]] = true
		}
		for _, m := range fieldRe.FindAllStringSubmatch(t.Condition, -1) {
			f := m[1] + "." + m[2]
			if fields[f] == nil {
				fields[f] = map[string]bool{}
			}
			fields[f][m[3]] = true
		}
	}
	if len(states) == 0 && len(fields) == 0 {
		return fmt.Sprintf("Add tasks: {%s: {state: Completed}}.", key)
	}
	var parts []string
	if len(states) > 0 {
		parts = append(parts, "state is one of "+strings.Join(keys(states), ", "))
	}
	for _, f := range keys(fields) {
		parts = append(parts, fmt.Sprintf("form field %s is checked against %s", f, strings.Join(keys(fields[f]), ", ")))
	}
	return "Its transitions check: " + strings.Join(parts, "; ") + "."
}

func (r *runner) expressions(n *workflow.Node, st *Step) (any, outcome) {
	exprs, _ := n.Params["expressions"].([]any)
	outputs := map[string]any{}
	node := map[string]any{"state": "success", "output": outputs}
	// Earlier outputs are visible to later ones in the same node. The docs
	// call this unverified on the live engine; lint flags it.
	r.s.set(n.Index, node)
	var shown []string
	for _, e := range exprs {
		m, _ := e.(map[string]any)
		k, _ := m["key"].(string)
		src, _ := m["expression"].(string)
		rt, _ := m["returnType"].(string)
		if d := r.fieldRef(expr.Refs(src), n.Index); d != nil {
			delete(r.s.data, n.Index)
			r.run.Pending = firstDecision(r.run.Pending, d)
			return nil, outcome{"waiting", fmt.Sprintf("output %s reads %s.%s, which the case does not give", k, d.Key, d.Field), Waiting}
		}
		res := expr.EvalExpression(src, rt, r.s, r.opt.Policy)
		tr := ExprTrace{Key: k, Substituted: res.Substituted, Value: toAny(res.Value)}
		if res.Err != nil {
			tr.Error = res.Err.Error()
		}
		st.Exprs = append(st.Exprs, tr)
		if res.Err != nil {
			delete(r.s.data, n.Index)
			return nil, outcome{"failed", fmt.Sprintf("output %s failed: %v\n  the engine parsed: %s", k, res.Err, res.Substituted), Failed}
		}
		outputs[k] = toAny(res.Value)
		if len(shown) < 4 {
			shown = append(shown, fmt.Sprintf("%s = %s", k, res.Value))
		}
	}
	detail := strings.Join(shown, ", ")
	if len(exprs) > len(shown) {
		detail += fmt.Sprintf(" (+%d more)", len(exprs)-len(shown))
	}
	return node, ran(detail)
}

func (r *runner) datahub(n *workflow.Node, key string, st *Step) (any, outcome) {
	op, _ := n.Params["operation"].(string)
	table, _ := n.Params["table_name"].(string)
	if !strings.EqualFold(op, "select") {
		filled, subs := interpolateDeep(n.Params["rows"], r.s)
		st.Subs = append(st.Subs, subs...)
		for _, s := range subs {
			if !s.Found {
				r.note("%s: writes a blank into %s where $.{%s} had no value", n.Index, table, s.Ref)
			}
		}
		_ = filled
		// Recorded contexts show writes return only {"state": "Success"}
		// (capital S, unlike selects): no rows, no count.
		return map[string]any{"state": "Success"}, ran(fmt.Sprintf("%s %s", op, table))
	}

	rows, found := r.lookupRows(key)
	if !found && r.opt.Explore && !resultRead(r.w, n) {
		// Nothing reads this lookup's result, so it cannot change the
		// route or any value: no point branching on it.
		found = true
		rows = []any{}
	}
	if !found && r.opt.Explore && !r.opt.AllDecisions && !lookupMatters(r.w, n) {
		// Read, but only into stored values or messages: it cannot change
		// the route or fail the instance. Assume a row was found.
		found = true
		rows = []any{map[string]any{}}
	}
	if !found && r.opt.Explore {
		iter := -1
		if len(r.s.loops) > 0 {
			iter = r.s.loops[len(r.s.loops)-1].index
		}
		r.run.Pending = firstDecision(r.run.Pending, &Decision{Kind: Lookup, Key: key, Visit: iter, Node: n.Index, Options: []Option{
			{Label: "finds nothing", Value: []any{}},
			{Label: "finds a row", Value: []any{map[string]any{}}},
		}})
		return nil, outcome{"waiting", fmt.Sprintf("needs a decision: does the lookup on %s find a row?", table), Waiting}
	}
	if !found {
		r.note("%s: the case gives no rows for this lookup on %s, so it finds none. Add lookups: {%s: [...]} to choose.", key, table, key)
	}
	if lim, ok := n.Params["limit"].(float64); ok && lim > 0 && len(rows) > int(lim) {
		rows = rows[:int(lim)]
	}
	rows = r.completeRows(n, rows)
	return map[string]any{"state": "success", "count": float64(len(rows)), "output": rows},
		ran(fmt.Sprintf("select %s: %s", table, plural(len(rows), "row", "rows")))
}

// completeRows gives every row the columns the workflow reads from this
// lookup. A real row has all its table's columns, null when empty, so a
// case only needs to list the values that matter.
func (r *runner) completeRows(n *workflow.Node, rows []any) []any {
	cols := r.columns()[n.Index]
	if len(cols) == 0 {
		return rows
	}
	out := make([]any, len(rows))
	for i, row := range rows {
		m, ok := row.(map[string]any)
		if !ok {
			out[i] = row
			continue
		}
		full := make(map[string]any, len(m)+len(cols))
		for k, v := range m {
			full[k] = v
		}
		for _, c := range cols {
			if _, has := full[c]; !has {
				full[c] = nil
			}
		}
		out[i] = full
	}
	return out
}

// columns maps each node index to the columns the workflow reads from its
// rows ($.{n4.output.0.col}, $.{n4.output.currentIndex.col}).
func (r *runner) columns() map[string][]string {
	if r.cols != nil {
		return r.cols
	}
	seen := map[string]map[string]bool{}
	add := func(src string) {
		for _, ref := range expr.Refs(src) {
			p := strings.Split(ref, ".")
			if len(p) >= 4 && p[1] == "output" && (p[2] == "currentIndex" || isDigits(p[2])) {
				if seen[p[0]] == nil {
					seen[p[0]] = map[string]bool{}
				}
				seen[p[0]][p[3]] = true
			}
		}
	}
	for _, n := range r.w.Nodes {
		b, _ := json.Marshal(n.Params)
		add(string(b))
	}
	for _, t := range r.w.Transitions {
		add(t.Condition)
	}
	r.cols = map[string][]string{}
	for k, v := range seen {
		r.cols[k] = keys(v)
	}
	return r.cols
}

func isDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return s != ""
}

// lookupRows reads a lookup's rows from the case. Inside a loop the case
// may give one result per iteration as a list of lists.
func (r *runner) lookupRows(key string) ([]any, bool) {
	v, ok := r.c.Lookups[key]
	if !ok {
		return []any{}, false
	}
	rows, _ := v.([]any)
	if len(r.s.loops) > 0 && len(rows) > 0 {
		if _, perIter := rows[0].([]any); perIter {
			i := r.s.loops[len(r.s.loops)-1].index
			if i >= len(rows) || rows[i] == nil {
				return []any{}, false
			}
			inner, _ := rows[i].([]any)
			return inner, true
		}
	}
	if rows == nil {
		rows = []any{}
	}
	return rows, true
}

var braced = regexp.MustCompile(`^\s*\$\.\{([^}]+)\}\s*$`)

func (r *runner) loop(n *workflow.Node, st *Step) (any, outcome) {
	src, _ := n.Params["collection"].(string)
	m := braced.FindStringSubmatch(src)
	if m == nil {
		return nil, outcome{"failed", fmt.Sprintf("loop collection %q is not a single $.{...} reference", src), Failed}
	}
	path := strings.TrimSpace(m[1])
	v, found := r.s.Lookup(path)
	items := v.List
	fromTask := r.fromTask(path)

	if !found && fromTask && r.opt.Explore {
		// The officer fills this list in on the task form. Try it empty
		// (the platform never finishes a loop over an empty form list) and
		// with one row.
		parts := strings.SplitN(path, ".", 2)
		t := r.w.NodeByIndex(parts[0])
		r.run.Pending = firstDecision(r.run.Pending, &Decision{Kind: TaskField, Key: t.Index, Visit: max(0, r.visits[t.Index]-1),
			Field: parts[1], Node: n.Index, Options: []Option{
				{Label: "(no rows)", Value: []any{}},
				{Label: "(one row)", Value: []any{map[string]any{}}},
			}})
		return nil, outcome{"waiting", fmt.Sprintf("needs a decision: how many rows the officer enters in %s", parts[1]), Waiting}
	}
	if r.opt.Explore && !fromTask && (!found || v.IsNull()) && !strings.Contains(path, ".") {
		if _, given := r.c.Variables[path]; !given && !r.submitted(path) {
			// A submitted list the base case leaves out. An empty one just
			// means zero iterations (only form lists stall), so one item is
			// the case worth running: it covers the loop body.
			opts := []Option{{Label: "(one item)", Value: []any{map[string]any{}}}}
			if r.opt.AllDecisions {
				opts = append([]Option{{Label: "(empty list)", Value: []any{}}}, opts...)
			}
			r.run.Pending = firstDecision(r.run.Pending, &Decision{Kind: Variable, Key: path, Visit: -1, Node: n.Index, Options: opts})
			return nil, outcome{"waiting", fmt.Sprintf("needs a decision: how many items the applicant submits in %s", path), Waiting}
		}
	}
	switch {
	case (!found || v.IsNull() || len(items) == 0) && fromTask:
		// A form-task array that is empty or absent never releases the
		// loop: the platform waits forever. (node-recipes.md, Loop)
		return nil, outcome{"stuck", fmt.Sprintf("loop over $.{%s} has nothing to iterate, and that list comes from a task form. On the platform a loop over an empty form list never finishes and the instance stays open. Real only if the form lets the officer leave this list empty.", path), Stuck}
	case !found || v.IsNull():
		r.note("%s: loop collection $.{%s} has no value; it runs zero times", n.Index, path)
	case v.Kind != expr.List:
		return nil, outcome{"failed", fmt.Sprintf("loop collection $.{%s} is a %s, not a list", path, v.Kind), Failed}
	}

	frame := &loopFrame{node: n}
	r.s.loops = append(r.s.loops, frame)
	defer func() { r.s.loops = r.s.loops[:len(r.s.loops)-1] }()

	for i := range items {
		frame.index = i
		iter := map[string]any{}
		frame.out = append(frame.out, iter)
		for _, c := range n.Children {
			cs := Step{Leg: st.Leg, Node: n.Index + "/" + c.Index, Name: c.Name, Control: c.Control.String()}
			out, ev := r.exec(c, &cs)
			cs.Event, cs.Detail, cs.Output = ev.event, fmt.Sprintf("[item %d] %s", i, ev.detail), out
			r.seq++
			cs.Seq = r.seq
			st.Children = append(st.Children, cs)
			if ev.stop != "" {
				return nil, outcome{ev.event, fmt.Sprintf("in iteration %d, %s: %s", i, c.Index, ev.detail), ev.stop}
			}
			iter[c.Index] = out
		}
	}
	return map[string]any{"state": "success", "output": frame.out}, ran(fmt.Sprintf("ran %s over $.{%s}", plural(len(items), "time", "times"), path))
}

// fromTask reports whether a reference reads a task's form data.
func (r *runner) fromTask(path string) bool {
	root := strings.SplitN(path, ".", 2)[0]
	n := r.w.NodeByIndex(root)
	return n != nil && n.Control == workflow.CtrlAssignTask
}

func firstDecision(cur, d *Decision) *Decision {
	if cur != nil {
		return cur
	}
	return d
}

// submitted reports whether the base submission gave this variable through
// the trigger map.
func (r *runner) submitted(name string) bool {
	src, ok := r.w.Trigger[name]
	if !ok {
		return false
	}
	path := strings.TrimPrefix(src, "$.")
	_, found := (&scope{data: map[string]any{
		"attributes": r.c.Submission.Attributes,
		"meta":       r.c.Submission.Meta,
	}}).Lookup(path)
	return found
}

// plural writes "no rows", "1 row" or "3 rows".
func plural(n int, one, many string) string {
	switch n {
	case 0:
		return "no " + many
	case 1:
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
