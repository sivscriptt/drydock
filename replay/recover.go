package replay

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/sivscriptt/drydock/workflow"
)

var refRe = regexp.MustCompile(`\$\.\{([^}]+)\}`)

// recovered is a reference value read back out of leg_data.
type recovered struct {
	ref   string
	value any
	typed bool // from an expression, where quoting tells strings from numbers
}

// recoverValues reads the values the engine actually used out of the legs.
// leg_data holds each node's parameters after substitution, so lining a
// template up with its filled-in text shows what each reference was. The
// context snapshot can be older than the legs; this cannot.
func recoverValues(w *workflow.Workflow, rec *Recording) map[string]recovered {
	out := map[string]recovered{}
	ran := map[string]bool{} // nodes whose legs have already happened
	// A node's output can only be read from a leg that ran after the node;
	// before that, the reference was blank. Plain-text legs are also too
	// ambiguous for outputs ("$.{a} $.{b}" with a space inside a value), so
	// outputs come only from expression legs, where quoting marks the edges.
	usable := func(ref string, typed bool) bool {
		root := strings.SplitN(ref, ".", 2)[0]
		if w.NodeByIndex(root) == nil {
			return true // a variable, not a node output
		}
		return typed && ran[root]
	}
	for _, l := range rec.Legs {
		n := w.Node(l.NodeID)
		if n == nil {
			continue
		}
		if n.Control == workflow.CtrlExpressionV2 {
			tmpl := map[string]string{}
			exprs, _ := n.Params["expressions"].([]any)
			for _, e := range exprs {
				m, _ := e.(map[string]any)
				k, _ := m["key"].(string)
				tmpl[k], _ = m["expression"].(string)
			}
			data, _ := l.Data.(map[string]any)
			got, _ := data["expressions"].([]any)
			for _, e := range got {
				m, _ := e.(map[string]any)
				k, _ := m["key"].(string)
				text, _ := m["expression"].(string)
				for ref, rendered := range align(tmpl[k], text) {
					if v, ok := decodeRendered(rendered); ok && usable(ref, true) {
						out[ref] = recovered{ref, v, true}
					}
				}
			}
			ran[n.Index] = true
			continue
		}
		// Other controls fill in plain text: no quotes, so values are text.
		walkPair(n.Params, l.Data, func(tmpl, text string) {
			for ref, rendered := range align(tmpl, text) {
				if prev, ok := out[ref]; ok && prev.typed || !usable(ref, false) {
					continue
				}
				// Plain text has no quotes to show the type. Every typed
				// observation so far pasted number-like values bare, so a
				// number-like text value is taken as a number.
				var v any = rendered
				if f, err := strconv.ParseFloat(strings.TrimSpace(rendered), 64); err == nil {
					v = f
				}
				out[ref] = recovered{ref, v, false}
			}
		})
		ran[n.Index] = true
	}
	return out
}

// align matches a template against its filled-in text and returns what each
// reference became. It returns nothing if the two do not line up.
func align(tmpl, text string) map[string]string {
	if !strings.Contains(tmpl, "$.{") {
		return nil
	}
	refs := refRe.FindAllStringSubmatch(tmpl, -1)
	lits := refRe.Split(tmpl, -1)
	var pat strings.Builder
	pat.WriteString(`(?s)^`)
	for i, lit := range lits {
		pat.WriteString(regexp.QuoteMeta(lit))
		if i < len(refs) {
			pat.WriteString(`(.*?)`)
		}
	}
	pat.WriteString(`$`)
	re, err := regexp.Compile(pat.String())
	if err != nil {
		return nil
	}
	m := re.FindStringSubmatch(text)
	if m == nil {
		return nil
	}
	out := map[string]string{}
	for i, r := range refs {
		ref := strings.TrimSpace(r[1])
		if prev, seen := out[ref]; seen && prev != m[i+1] {
			continue // ambiguous alignment; keep the first
		}
		out[ref] = m[i+1]
	}
	return out
}

// decodeRendered reverses Expression V2 pasting: "x" is the string x, a
// bare number is a number, true/false are booleans.
func decodeRendered(s string) (any, bool) {
	s = strings.TrimSpace(s)
	switch {
	case len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' && !strings.Contains(s[1:len(s)-1], `"`):
		return s[1 : len(s)-1], true
	case s == "true" || s == "false":
		return s == "true", true
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f, true
	}
	return nil, false
}

// walkPair visits template strings and their filled-in counterparts.
func walkPair(tmpl, data any, fn func(t, d string)) {
	switch t := tmpl.(type) {
	case string:
		if d, ok := data.(string); ok {
			fn(t, d)
		}
	case map[string]any:
		d, _ := data.(map[string]any)
		for k, v := range t {
			walkPair(v, d[k], fn)
		}
	case []any:
		d, _ := data.([]any)
		for i, v := range t {
			if i < len(d) {
				walkPair(v, d[i], fn)
			}
		}
	}
}
