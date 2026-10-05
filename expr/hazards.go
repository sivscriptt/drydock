package expr

import (
	"fmt"
	"regexp"
	"strings"
)

// Hazard is a pattern in an expression's source that is known to break, or
// to quietly give the wrong answer, in the live engine.
type Hazard struct {
	Code    string
	Ref     string // the reference involved, if any
	Message string
	Fails   bool // true if it fails the instance, false if it is silently wrong
}

var rowRef = regexp.MustCompile(`^n\d+\.output\.\d+\.`)

// Hazards checks an Expression V2 source before substitution. Every rule
// here comes from a real failure written up in the workflow-expressions
// pitfalls checklist.
func Hazards(src string) []Hazard {
	var out []Hazard
	toks, err := lex(src)
	if err != nil {
		return []Hazard{{Code: "syntax", Message: err.Error(), Fails: true}}
	}
	whole := len(toks) == 2 && toks[0].kind == tStr // the expression is one string

	for i, t := range toks {
		switch {
		case t.kind == tStr && strings.Contains(t.text, "$.{") && t.quote == '"' && !whole:
			// A ref inside a double-quoted literal: the engine pastes
			// "value" inside "...", the quotes collide and the parse fails.
			// One whole-expression string is the documented exception.
			for _, r := range Refs(t.text) {
				out = append(out, Hazard{"nested-quoted-ref", r,
					fmt.Sprintf("$.{%s} sits inside a double-quoted string; the engine pastes the value in double quotes and the parse fails. Pass the reference unquoted.", r), true})
			}
		case t.kind == tStr && strings.Contains(t.text, "$.{") && t.quote == '\'':
			// '$.{ref}' == 'Yes' compares '"Yes"' with 'Yes': always false.
			if other, ok := comparedLiteral(toks, i); ok && !strings.HasPrefix(other, `"`) {
				for _, r := range Refs(t.text) {
					out = append(out, Hazard{"quoted-compare-always-false", r,
						fmt.Sprintf("'$.{%s}' arrives as '\"value\"', so comparing it with '%s' is never true. Compare with '\"%s\"' instead.", r, other, other), false})
				}
			}
		case t.kind == tOp && t.text == "+":
			if i > 0 && toks[i-1].kind == tStr || i+1 < len(toks) && toks[i+1].kind == tStr {
				out = append(out, Hazard{"plus-joins-text", "",
					"+ next to text: Expression V2 does not join strings with +. Put the references inside one double-quoted string.", true})
			}
		case t.kind == tIdent && strings.EqualFold(t.text, "null") && i > 0 && toks[i-1].kind == tOp && (toks[i-1].text == "==" || toks[i-1].text == "!="):
			out = append(out, Hazard{"null-compare", "", "comparing with null; blanks arrive as '', so guard with == '' instead.", false})
		}
	}

	// A lookup row read inside ifCondition: the guard does not help, because
	// every reference is pasted in before parsing. If the lookup found
	// nothing, the literal $.{...} reaches the parser and the instance fails.
	//
	// Only bare references count. A reference inside a quoted literal stays
	// harmless text when it is missing, which is why quoted checks never
	// crash (they can still be wrong; see quoted-compare-always-false).
	if strings.Contains(src, "ifCondition") {
		for _, t := range toks {
			if t.kind != tUnresolved {
				continue
			}
			r := strings.TrimSpace(t.text[3 : len(t.text)-1])
			if rowRef.MatchString(r) {
				out = append(out, Hazard{"guard-does-not-protect", r,
					fmt.Sprintf("$.{%s} is a lookup row read inside ifCondition. The guard cannot protect it: references are pasted in before parsing, so an empty lookup fails the instance. Gate the node on the lookup count instead.", r), true})
			}
		}
	}
	return out
}

// comparedLiteral finds the string on the other side of == or != from the
// token at i.
func comparedLiteral(toks []token, i int) (string, bool) {
	if i+2 < len(toks) && toks[i+1].kind == tOp && (toks[i+1].text == "==" || toks[i+1].text == "!=") && toks[i+2].kind == tStr {
		return toks[i+2].text, true
	}
	if i >= 2 && toks[i-1].kind == tOp && (toks[i-1].text == "==" || toks[i-1].text == "!=") && toks[i-2].kind == tStr {
		return toks[i-2].text, true
	}
	return "", false
}
