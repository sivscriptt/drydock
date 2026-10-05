package sim

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/sivscriptt/drydock/expr"
	"github.com/sivscriptt/drydock/workflow"
)

// DecisionKind is what kind of outside answer a run is waiting for.
type DecisionKind string

const (
	TaskState DecisionKind = "task"     // what the officer did
	TaskField DecisionKind = "field"    // a form field the workflow reads
	Lookup    DecisionKind = "lookup"   // whether a DataHub select finds a row
	Payment   DecisionKind = "payment"  // invoice status
	External  DecisionKind = "external" // an outside system's answer
	Variable  DecisionKind = "variable" // a submitted value the base case leaves out
)

// Other is the option for "a value none of the workflow's checks expect".
// It finds branches that have no else.
const Other = "(other)"

// Decision is a point where an explored run needs an answer it does not
// have. Options are the answers worth trying.
type Decision struct {
	Kind    DecisionKind `json:"kind"`
	Key     string       `json:"key"`             // case key: "n5", "n9/lm1"
	Visit   int          `json:"visit"`           // task visit or loop iteration
	Field   string       `json:"field,omitempty"` // "form-slug.field"
	Node    string       `json:"node"`            // where the run stopped
	Options []Option     `json:"options"`
}

// Option is one answer to a decision.
type Option struct {
	Label string `json:"label"`
	Value any    `json:"value"`
}

// Label describes a chosen option in a scenario's list of decisions.
func (d Decision) Label(o Option) string {
	switch d.Kind {
	case TaskState:
		v := ""
		if d.Visit > 0 {
			v = fmt.Sprintf(" (visit %d)", d.Visit+1)
		}
		return fmt.Sprintf("%s%s: officer %s", d.Key, v, o.Label)
	case TaskField:
		return fmt.Sprintf("%s: %s = %s", d.Key, d.Field, o.Label)
	case Lookup:
		it := ""
		if d.Visit >= 0 {
			it = fmt.Sprintf(" (item %d)", d.Visit)
		}
		return fmt.Sprintf("%s%s: lookup %s", d.Key, it, o.Label)
	}
	if d.Kind == Variable {
		return fmt.Sprintf("submission %s: %s", d.Key, o.Label)
	}
	return fmt.Sprintf("%s: %s %s", d.Key, d.Kind, o.Label)
}

// Apply returns a copy of the case with the option chosen.
func (c *Case) Apply(d Decision, o Option) *Case {
	nc := c.clone()
	switch d.Kind {
	case TaskState:
		list := append(TaskAnswers(nil), nc.Tasks[d.Key]...)
		for len(list) <= d.Visit {
			list = append(list, TaskAnswer{})
		}
		list[d.Visit].State = fmt.Sprint(o.Value)
		nc.Tasks[d.Key] = list
	case TaskField:
		list := append(TaskAnswers(nil), nc.Tasks[d.Key]...)
		for len(list) <= d.Visit {
			list = append(list, TaskAnswer{})
		}
		slug, field := splitField(d.Field)
		a := list[d.Visit]
		form := map[string]map[string]any{}
		for k, v := range a.Form {
			form[k] = copyMap(v)
		}
		if form[slug] == nil {
			form[slug] = map[string]any{}
		}
		form[slug][field] = o.Value
		a.Form = form
		list[d.Visit] = a
		nc.Tasks[d.Key] = list
	case Lookup:
		if d.Visit < 0 {
			nc.Lookups[d.Key] = o.Value
			break
		}
		per, _ := nc.Lookups[d.Key].([]any)
		per = append([]any(nil), per...)
		for len(per) <= d.Visit {
			per = append(per, nil)
		}
		per[d.Visit] = o.Value
		nc.Lookups[d.Key] = per
	case Payment:
		nc.Payments[d.Key] = fmt.Sprint(o.Value)
	case External:
		nc.External[d.Key] = map[string]any{"next_state": o.Value}
	case Variable:
		if nc.Variables == nil {
			nc.Variables = map[string]any{}
		}
		nc.Variables[d.Key] = o.Value
	}
	return nc
}

func splitField(f string) (string, string) {
	for i := len(f) - 1; i >= 0; i-- {
		if f[i] == '.' {
			return f[:i], f[i+1:]
		}
	}
	return f, ""
}

func (c *Case) clone() *Case {
	b, _ := json.Marshal(c)
	var nc Case
	json.Unmarshal(b, &nc)
	nc.normalise()
	if nc.Tasks == nil {
		nc.Tasks = map[string]TaskAnswers{}
	}
	if nc.Lookups == nil {
		nc.Lookups = map[string]any{}
	}
	if nc.Payments == nil {
		nc.Payments = map[string]string{}
	}
	if nc.External == nil {
		nc.External = map[string]map[string]any{}
	}
	return &nc
}

// Fingerprint identifies a case's answers, for de-duplicating explored runs.
func (c *Case) Fingerprint() string {
	b, _ := json.Marshal(struct {
		T map[string]TaskAnswers
		L map[string]any
		P map[string]string
		E map[string]map[string]any
		V map[string]any
	}{c.Tasks, c.Lookups, c.Payments, c.External, c.Variables})
	return string(b)
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// compared lists the literal values the workflow compares a reference with,
// in conditions ($.n5.state == "Approved") and expressions
// ($.{n5.form.field} == 'Yes', '$.{x}' == '"Yes"'). Built once per workflow.
func compared(w *workflow.Workflow, ref string) []string {
	idx := comparisonIndex(w)
	return idx[ref]
}

var (
	indexes  sync.Map // *workflow.Workflow -> map[string][]string
	condCmp  = regexp.MustCompile(`\$\.([A-Za-z0-9_\-.]+)\s*(?:==|!=)\s*"([^"]*)"`)
	condCmpR = regexp.MustCompile(`"([^"]*)"\s*(?:==|!=)\s*\$\.([A-Za-z0-9_\-.]+)`)
	exprCmp  = regexp.MustCompile(`'?\$\.\{([^}]+)\}'?\s*(?:==|!=)\s*'"?([^'"]*)"?'`)
	exprCmpR = regexp.MustCompile(`'"?([^'"]*)"?'\s*(?:==|!=)\s*'?\$\.\{([^}]+)\}'?`)
	arithL   = regexp.MustCompile(`\$\.\{([^}]+)\}\s*[-+*/]`)
	arithR   = regexp.MustCompile(`[-+*/]\s*\$\.\{([^}]+)\}`)
	arithSet sync.Map // *workflow.Workflow -> map[string]bool
)

// usedInArithmetic reports whether an expression adds, subtracts,
// multiplies or divides with the reference: then it holds a number on a
// real form, and text options would only produce noise.
func usedInArithmetic(w *workflow.Workflow, ref string) bool {
	if v, ok := arithSet.Load(w); ok {
		return v.(map[string]bool)[ref]
	}
	set := map[string]bool{}
	for _, n := range w.Nodes {
		exprs, _ := n.Params["expressions"].([]any)
		for _, e := range exprs {
			m, _ := e.(map[string]any)
			src, _ := m["expression"].(string)
			// Anything read by an expression declared numeric is a number,
			// including guarded reads like ifCondition($.{x} == '', 0, $.{x}).
			if rt, _ := m["returnType"].(string); strings.EqualFold(rt, "numeric") {
				for _, ref := range expr.Refs(src) {
					set[ref] = true
				}
			}
			for _, re := range []*regexp.Regexp{arithL, arithR} {
				for _, mm := range re.FindAllStringSubmatch(src, -1) {
					set[strings.TrimSpace(mm[1])] = true
				}
			}
		}
	}
	actual, _ := arithSet.LoadOrStore(w, set)
	return actual.(map[string]bool)[ref]
}

func comparisonIndex(w *workflow.Workflow) map[string][]string {
	if v, ok := indexes.Load(w); ok {
		return v.(map[string][]string)
	}
	sets := map[string]map[string]bool{}
	add := func(ref, val string) {
		if sets[ref] == nil {
			sets[ref] = map[string]bool{}
		}
		sets[ref][val] = true
	}
	for _, t := range w.Transitions {
		for _, m := range condCmp.FindAllStringSubmatch(t.Condition, -1) {
			add(m[1], m[2])
		}
		for _, m := range condCmpR.FindAllStringSubmatch(t.Condition, -1) {
			add(m[2], m[1])
		}
	}
	for _, n := range w.Nodes {
		exprs, _ := n.Params["expressions"].([]any)
		for _, e := range exprs {
			m, _ := e.(map[string]any)
			src, _ := m["expression"].(string)
			for _, mm := range exprCmp.FindAllStringSubmatch(src, -1) {
				add(mm[1], mm[2])
			}
			for _, mm := range exprCmpR.FindAllStringSubmatch(src, -1) {
				add(mm[2], mm[1])
			}
		}
	}
	idx := map[string][]string{}
	for ref, vals := range sets {
		for v := range vals {
			idx[ref] = append(idx[ref], v)
		}
		sort.Strings(idx[ref])
	}
	actual, _ := indexes.LoadOrStore(w, idx)
	return actual.(map[string][]string)
}

func valuesOrDefault(vals []string, def ...string) []Option {
	if len(vals) == 0 {
		vals = def
	}
	out := make([]Option, 0, len(vals))
	for _, v := range vals {
		out = append(out, Option{Label: v, Value: v})
	}
	return out
}

var readIndexes sync.Map // *workflow.Workflow -> map[string]bool

// resultRead reports whether anything in the workflow reads a node's
// result: a condition, an expression, or another node's parameters. For a
// loop child the result is read through its loop
// ($.{n9.output.currentIndex.l1.x} or $.{n9.output.0.l1.x}).
func resultRead(w *workflow.Workflow, n *workflow.Node) bool {
	idx := readIndex(w)
	if n.Loop == nil {
		return idx[n.Index]
	}
	return idx[n.Loop.Index+"/"+n.Index]
}

func readIndex(w *workflow.Workflow) map[string]bool {
	if v, ok := readIndexes.Load(w); ok {
		return v.(map[string]bool)
	}
	idx := map[string]bool{}
	mark := func(src string) {
		for _, ref := range expr.Refs(src) {
			p := strings.Split(ref, ".")
			idx[p[0]] = true
			// n9.output.<i|currentIndex>.l1... reads loop child l1 of n9
			if len(p) >= 4 && p[1] == "output" {
				idx[p[0]+"/"+p[3]] = true
			}
		}
	}
	for _, t := range w.Transitions {
		mark(t.Condition)
	}
	for _, n := range w.Nodes {
		b, _ := json.Marshal(n.Params)
		mark(string(b))
	}
	actual, _ := readIndexes.LoadOrStore(w, idx)
	return actual.(map[string]bool)
}

// taskStates is what an officer can do with a task. The workflow may only
// compare one outcome ($.{n14.state} == 'Rejected'), but the platform
// offers the pair, so the partner is always tried too.
func taskStates(w *workflow.Workflow, idx string) []Option {
	seen := map[string]bool{}
	for _, v := range compared(w, idx+".state") {
		seen[v] = true
	}
	partners := map[string]string{"Approved": "Rejected", "Rejected": "Approved", "Completed": "Cancelled", "Cancelled": "Completed"}
	for v := range seen {
		if p, ok := partners[v]; ok {
			seen[p] = true
		}
	}
	if len(seen) == 0 {
		seen["Completed"] = true
	}
	vals := make([]string, 0, len(seen))
	for v := range seen {
		vals = append(vals, v)
	}
	sort.Strings(vals)
	return valuesOrDefault(vals)
}

// relevance says which outside answers can change anything that matters:
// the route a case takes, or whether the instance fails. Answers that only
// flow into stored values or messages are not branched on.
type relevance struct {
	refs    map[string]bool // references that feed conditions or loops, directly or via expressions
	roots   map[string]bool // node indexes whose results feed them
	bareRow map[string]bool // lookups whose rows an expression reads unquoted (fails if empty)
}

var relevances sync.Map // *workflow.Workflow -> *relevance

func relevant(w *workflow.Workflow) *relevance {
	if v, ok := relevances.Load(w); ok {
		return v.(*relevance)
	}
	rel := &relevance{refs: map[string]bool{}, roots: map[string]bool{}, bareRow: map[string]bool{}}

	// What each expression output reads.
	reads := map[string][]string{}
	for _, n := range w.Nodes {
		exprs, _ := n.Params["expressions"].([]any)
		for _, e := range exprs {
			m, _ := e.(map[string]any)
			k, _ := m["key"].(string)
			src, _ := m["expression"].(string)
			reads[n.Index+".output."+k] = expr.Refs(src)
			// Rows read bare (not inside quotes) fail the parse when the
			// lookup is empty.
			for _, h := range expr.Hazards(src) {
				// Both fail the parse depending on whether the lookup found
				// a row: bare reads when it did not, nested quoted reads
				// when it did.
				if h.Code == "guard-does-not-protect" || h.Code == "nested-quoted-ref" {
					rel.bareRow[strings.SplitN(h.Ref, ".", 2)[0]] = true
				}
			}
			for _, ref := range bareRefs(src) {
				p := strings.Split(ref, ".")
				if len(p) >= 3 && p[1] == "output" && isIndex(p[2]) {
					rel.bareRow[p[0]] = true
				}
			}
		}
	}

	// Start from what decides routes, and follow expressions backwards.
	var work []string
	for _, t := range w.Transitions {
		work = append(work, expr.Refs(t.Condition)...)
	}
	for _, n := range w.Nodes {
		if c, _ := n.Params["collection"].(string); c != "" {
			work = append(work, expr.Refs(c)...)
		}
	}
	for len(work) > 0 {
		ref := work[len(work)-1]
		work = work[:len(work)-1]
		if rel.refs[ref] {
			continue
		}
		rel.refs[ref] = true
		rel.roots[strings.SplitN(ref, ".", 2)[0]] = true
		for out, srcs := range reads {
			if out == ref || strings.HasPrefix(ref, out+".") {
				work = append(work, srcs...)
			}
		}
	}
	actual, _ := relevances.LoadOrStore(w, rel)
	return actual.(*relevance)
}

// bareRefs lists the $.{...} references in an expression that are not
// inside a quoted literal.
func bareRefs(src string) []string {
	var out []string
	inQuote := byte(0)
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case inQuote != 0 && c == inQuote:
			inQuote = 0
		case inQuote == 0 && (c == '\'' || c == '"'):
			inQuote = c
		case inQuote == 0 && strings.HasPrefix(src[i:], "$.{"):
			end := strings.IndexByte(src[i:], '}')
			if end > 0 {
				out = append(out, strings.TrimSpace(src[i+3:i+end]))
				i += end
			}
		}
	}
	return out
}

func isIndex(s string) bool {
	if s == "currentIndex" {
		return true
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return s != ""
}

// lookupMatters: a lookup is worth branching on if its count or rows feed a
// route, or an expression reads its rows unquoted.
func lookupMatters(w *workflow.Workflow, n *workflow.Node) bool {
	rel := relevant(w)
	idx := n.Index
	if n.Loop != nil {
		// A loop child's result is read through its loop.
		for ref := range rel.refs {
			p := strings.Split(ref, ".")
			if p[0] == n.Loop.Index && len(p) >= 4 && p[3] == n.Index {
				return true
			}
		}
		return false
	}
	return rel.roots[idx] || rel.bareRow[idx]
}

// fieldMatters: a form field is worth branching on if it feeds a route.
func fieldMatters(w *workflow.Workflow, ref string) bool { return relevant(w).refs[ref] }
