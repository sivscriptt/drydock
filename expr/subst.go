package expr

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Policy holds the engine behaviours that the documentation describes but
// that have not yet been confirmed against recorded instances. Each field
// names the source it comes from. Replay against real runs (milestone 4)
// will either confirm the defaults or change them.
type Policy struct {
	// Quote wraps string values pasted into Expression V2. The live engine
	// uses a double quote; test_workflow_expression uses a single quote,
	// which is why the sandbox hides nested-quote failures.
	// (workflow-expressions SKILL.md, core rule 8)
	Quote byte

	// QuoteNull: whether a null value is pasted as "" or as nothing.
	// Confirmed on a recorded Recorded instance: with dob2 null,
	// ifCondition( $.{dob2} == '', '', ... ) produced '' and did not
	// fail, which only parses if the null arrived as "". (The docs' "bare
	// empty substitution is a parse error" does not apply to nulls.)
	QuoteNull bool

	// Templates: an expression that is one double-quoted string with
	// references inside is filled in as plain text, not quoted values. Core
	// rule 1 recommends exactly that pattern for text outputs, and it could
	// not parse otherwise.
	Templates bool

	// LooseEquality for Expression V2 ==. The function reference says both
	// operands must be the same type, so 5 == '5' is false.
	LooseEquality bool

	// ConditionLooseEquality for transition conditions. Conditions compare
	// counts as $.n3.count != "0" while the context stores the number 0;
	// those workflows run correctly in production, so the engine must treat
	// them as equal.
	ConditionLooseEquality bool

	// Location for currentDate and friends. Maldives time unless set.
	Location *time.Location
	// Now is the clock. time.Now unless set; fix it in tests and fixtures.
	Now func() time.Time
}

// Live is the best current model of the production engine.
func Live() Policy {
	return Policy{Quote: '"', QuoteNull: true, ConditionLooseEquality: true, Templates: true}
}

// Sandbox models test_workflow_expression, which quotes with single quotes.
func Sandbox() Policy {
	p := Live()
	p.Quote = '\''
	return p
}

func (p Policy) clock() time.Time {
	loc := p.Location
	if loc == nil {
		loc = male
	}
	if p.Now != nil {
		return p.Now().In(loc)
	}
	return time.Now().In(loc)
}

var male = func() *time.Location {
	l, err := time.LoadLocation("Indian/Maldives")
	if err != nil {
		return time.FixedZone("MVT", 5*3600)
	}
	return l
}()

// Missing says what happens to a reference with no value.
type Missing int

const (
	// KeepLiteral leaves $.{path} in the text. This is Expression V2
	// (ctrl 22): the literal reaches the parser and the instance fails.
	KeepLiteral Missing = iota
	// Blank replaces it with nothing. Every other control does this
	// (replace_unmatched = ""), silently.
	Blank
)

var refPattern = regexp.MustCompile(`\$\.\{([^}]*)\}`)

// Sub records one reference replacement, for traces.
type Sub struct {
	Ref      string // path inside $.{...}
	Found    bool
	Value    Value
	Rendered string // what went into the text
}

// Substitute pastes reference values into src the way the engine does,
// before anything is parsed.
func Substitute(src string, ctx Context, p Policy, missing Missing) (string, []Sub) {
	return fill(src, ctx, func(v Value) string { return render(v, p) }, missing)
}

func fill(src string, ctx Context, show func(Value) string, missing Missing) (string, []Sub) {
	var subs []Sub
	out := refPattern.ReplaceAllStringFunc(src, func(m string) string {
		path := strings.TrimSpace(m[3 : len(m)-1])
		var v Value
		found := false
		if ctx != nil {
			v, found = ctx.Lookup(path)
		}
		s := Sub{Ref: path, Found: found, Value: v}
		switch {
		case !found && missing == KeepLiteral:
			s.Rendered = m
		case !found:
			s.Rendered = ""
		default:
			s.Rendered = show(v)
		}
		subs = append(subs, s)
		return s.Rendered
	})
	return out, subs
}

func render(v Value, p Policy) string {
	switch v.Kind {
	case Number, Bool:
		return v.Text()
	case Null:
		if p.QuoteNull {
			return string([]byte{p.Quote, p.Quote})
		}
		return ""
	}
	q := string(p.Quote)
	return q + v.Text() + q
}

// Interpolate fills references in plain text (task titles, SMS bodies,
// DataHub values): no quoting, and a missing value becomes blank, as on
// every control other than Expression V2.
func Interpolate(text string, ctx Context) (string, []Sub) {
	return fill(text, ctx, func(v Value) string { return v.Text() }, Blank)
}

// Result is one evaluated Expression V2 output.
type Result struct {
	Value       Value
	Substituted string // the text the parser actually saw
	Subs        []Sub
	Err         error
}

// EvalExpression runs one Expression V2 output: substitute, parse, evaluate,
// then convert to the declared return type.
func EvalExpression(src, returnType string, ctx Context, p Policy) Result {
	if p.Templates {
		if body, ok := templateBody(src); ok {
			// Plain text fill. Expression V2 still shows a missing
			// reference as its placeholder rather than blanking it.
			text, subs := fill(body, ctx, func(v Value) string { return v.Text() }, KeepLiteral)
			r := Result{Substituted: text, Subs: subs}
			r.Value, r.Err = convert(Str(text), returnType)
			return r
		}
	}
	text, subs := Substitute(src, ctx, p, KeepLiteral)
	r := Result{Substituted: text, Subs: subs}
	n, err := Parse(text, ExpressionMode)
	if err != nil {
		r.Err = err
		return r
	}
	ev := &evaluator{ctx: ctx, policy: p, now: p.clock()}
	v, err := ev.eval(n)
	if err != nil {
		r.Err = err
		return r
	}
	r.Value, r.Err = convert(v, returnType)
	return r
}

func convert(v Value, returnType string) (Value, error) {
	switch strings.ToLower(strings.TrimSpace(returnType)) {
	case "", "any":
		return v, nil
	case "numeric", "number", "integer", "decimal":
		if f, ok := v.numeric(); ok && v.Kind != Bool {
			return Num(f), nil
		}
		return v, fmt.Errorf("returnType numeric, but the expression gave %s %s", v.Kind, v)
	case "string", "text", "date":
		// A string return type does not turn a number into text: the engine
		// keeps what the expression computed. (Recorded run:
		// age0, declared string, stored 6; rentReceived,
		// declared string, was pasted downstream as a bare 0.)
		if v.Kind == Number || v.Kind == Bool {
			return v, nil
		}
		return Str(v.Text()), nil
	case "boolean", "bool":
		if v.Kind == Bool {
			return v, nil
		}
		return Boolean(v.truthy()), nil
	}
	return v, nil
}

// templateBody returns the inside of an expression that is one
// double-quoted string containing references.
func templateBody(src string) (string, bool) {
	s := strings.TrimSpace(src)
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' || !strings.Contains(s, "$.{") {
		return "", false
	}
	toks, err := lex(refPattern.ReplaceAllString(s, "x"))
	if err != nil || len(toks) != 2 || toks[0].kind != tStr {
		return "", false
	}
	return s[1 : len(s)-1], true
}

// ConditionResult is an evaluated transition condition.
type ConditionResult struct {
	Pass bool
	// Unresolved lists references in the condition that had no value. The
	// condition still evaluates (they count as blank), but a transition
	// that depends on a value nobody produced is worth a look.
	Unresolved []string
	Err        error
}

// EvalCondition evaluates a transition condition. An empty condition passes.
func EvalCondition(src string, ctx Context, p Policy) ConditionResult {
	if strings.TrimSpace(src) == "" {
		return ConditionResult{Pass: true}
	}
	n, err := Parse(src, ConditionMode)
	if err != nil {
		return ConditionResult{Err: err}
	}
	cp := p
	cp.LooseEquality = p.ConditionLooseEquality
	ev := &evaluator{ctx: ctx, policy: cp, now: p.clock()}
	v, err := ev.eval(n)
	return ConditionResult{Pass: err == nil && v.truthy(), Unresolved: ev.unresolved, Err: err}
}

// Refs lists the references in a source, braced ($.{x}) or bare ($.x),
// in order of appearance, without duplicates.
func Refs(src string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(r string) {
		if r != "" && !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	for _, m := range refPattern.FindAllStringSubmatch(src, -1) {
		add(strings.TrimSpace(m[1]))
	}
	if toks, err := lex(refPattern.ReplaceAllString(src, "0")); err == nil {
		for _, t := range toks {
			if t.kind == tRef {
				add(t.text)
			}
		}
	}
	return out
}
