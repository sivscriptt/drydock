package expr

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var fixed = func() time.Time { return time.Date(2026, 10, 4, 9, 30, 0, 0, time.UTC) }

func live() Policy    { p := Live(); p.Now = fixed; return p }
func sandbox() Policy { p := Sandbox(); p.Now = fixed; return p }

func run(t *testing.T, src string, ctx Context, p Policy) Result {
	t.Helper()
	return EvalExpression(src, "", ctx, p)
}

// Every example from the Expression V2 function reference.
func TestDocumentedExamples(t *testing.T) {
	tests := []struct {
		src  string
		want string
	}{
		{"7 + 3", "10"},
		{"4.5 + 2.5", "7"},
		{"10 - 4", "6"},
		{"6 * 7", "42"},
		{"10 / 4", "2.5"},
		{"5 == 5", "true"},
		{"'Tradenet' == 'TradeNet'", "false"},
		{"5 != 3", "true"},
		{"3 < 5", "true"},
		{"5 > 3", "true"},
		{"5 <= 5", "true"},
		{"6 >= 7", "false"},
		{"true && false", "false"},
		{"true || false", "true"},
		{"ifCondition(5 > 3, 'Yes', 'No')", "Yes"},
		{"ifCondition('apple' == 'banana', 100, 200)", "200"},
		{"charAt('Tradenet', 0)", "T"},
		// The reference says 'e', but indexes start at 0 (charAt(…, 0) is
		// 'T'), so position 5 is 'n'. The doc example is wrong.
		{"charAt('Tradenet', 5)", "n"},
		{"compareIgnoreCase('Apple', 'apple')", "0"},
		{"endsWith('Tradenet', 'net')", "true"},
		{"equals('a', 'a')", "true"},
		{"equalsIgnoreCase('Male', 'MALE')", "true"},
		{"indexOf('hello world', 'o', 5)", "7"},
		{"indexOf('banana', 'na', 2)", "2"},
		{"lastIndexOf('banana', 'na', 5)", "4"},
		{"length('Tradenet')", "8"},
		{"replace('a-b-c', '-', '/')", "a/b/c"},
		{"startsWith('Tradenet', 'Trade', 0)", "true"},
		{"substring('Tradenet', 0, 5)", "Trade"},
		{"toLower('ABC')", "abc"},
		{"toUpper('abc')", "ABC"},
		{"trim('  x  ')", "x"},
		{"properCase('hello big WORLD')", "Hello Big World"},
		{"absolute(-4)", "4"},
		{"ceil(4.1)", "5"},
		{"maximum(3, 9)", "9"},
		{"minimum(3, 9)", "3"},
		{"roundNumber(2.71828, 2)", "2.72"},
		{"roundNumber(3.14159, 0)", "3"},
		{"numberFormat(2.71828, 2)", "2.72"},
		{"numberFormat(3.1, 2)", "3.10"},
		{"numberFormat(5, 2)", "5.00"},
		{"numberFormat(1999.123123, 2)", "1999.12"},
		{"addDays('2023-01-01', 10)", "2023-01-11"},
		{"addDays('2023-01-01T12:00:00Z', -5)", "2022-12-27T12:00:00Z"},
		{"addMonths('2023-01-31', 2)", "2023-03-31"},
		{"addMonths('2023-01-31T10:30:00Z', -1)", "2022-12-31T10:30:00Z"},
		{"addMonths('2023-01-31', 1)", "2023-02-28"},
		{"addYears('2023-01-01', 2)", "2025-01-01"},
		{"addYears('2023-01-01T12:00:00Z', -1)", "2022-01-01T12:00:00Z"},
		{"createDate(2023, 1, 15)", "2023-01-15"},
		{"createDateTime(2023, 1, 15, 10, 30, 0, 'UTC')", "2023-01-15T10:30:00Z"},
		{"createDateTime(2022, 12, 31, 23, 59, 59, 'Asia/Male')", "2022-12-31T23:59:59+05:00"},
		{"getDay('2023-01-02T12:00:00Z')", "2"},
		{"getMonth('2023-01-01')", "0"},
		{"getMonth('2023-02-15T12:00:00Z')", "1"},
		{"getYear('2022-12-31T23:59:59Z')", "2022"},
		{"addHours('2023-01-01T12:00:00Z', 5)", "2023-01-01T17:00:00Z"},
		{"addHours('2023-01-01T12:00:00Z', -3)", "2023-01-01T09:00:00Z"},
		{"hoursDifference('2023-01-01T12:00:00Z', '2023-01-02T12:00:00Z')", "24"},
		{"daysDifference('2023-01-01', '2023-01-10')", "9"},
		{"daysDifference('2023-01-01T12:00:00Z', '2023-01-02T12:00:00Z')", "1"},
		{"formatDate('2023-08-25', 'DD/MM/YY')", "25/08/23"},
		{"formatDate('2023-08-25', 'DDth Month, YY')", "25th August, 23"},
		{"formatDate('2023-08-25', 'Mon DD, YY')", "Aug 25, 23"},
		{"formatDate('2023-08-01', 'DDth')", "1st"},
		{"currentDate('Asia/Male')", "2026-10-04"},
		{"currentDateTime('Asia/Male')", "2026-10-04T14:30:00+05:00"},
		{"formatDate(currentDate('Asia/Male'), 'DD/MM/YY')", "04/10/26"},
		{"-(2 + 3) * 2", "-10"},
		{"!(1 > 2)", "true"},
	}
	for _, tt := range tests {
		r := run(t, tt.src, nil, live())
		if r.Err != nil {
			t.Errorf("%s: %v", tt.src, r.Err)
			continue
		}
		if got := r.Value.Text(); got != tt.want {
			t.Errorf("%s = %q, want %q", tt.src, got, tt.want)
		}
	}
}

// The behaviours that make the live engine different from a normal
// evaluator. Each one is from a real incident in the pitfalls checklist.
func TestEngineQuirks(t *testing.T) {
	ctx := MapContext{
		"answer":    "Yes",
		"amount":    "",
		"price":     12.5,
		"applicant": "Ali",
		"nullish":   nil,
		"n3":        map[string]any{"count": 0, "output": []any{}},
		"x":         "2026-01-01",
		"ref":       "2026-",
	}

	t.Run("quoted ref compared with plain literal is always false", func(t *testing.T) {
		if r := run(t, `'$.{answer}' == 'Yes'`, ctx, live()); r.Err != nil || r.Value.Bool {
			t.Errorf("live: %v %v (substituted %s)", r.Value, r.Err, r.Substituted)
		}
		if r := run(t, `'$.{answer}' == '"Yes"'`, ctx, live()); r.Err != nil || !r.Value.Bool {
			t.Errorf(`live with '"Yes"': %v %v`, r.Value, r.Err)
		}
		// The sandbox quotes with single quotes, so it gets this wrong in
		// the other direction and hides the bug.
		if r := run(t, `'$.{answer}' == 'Yes'`, ctx, sandbox()); r.Err == nil && r.Value.Bool {
			t.Errorf("sandbox should not say true either way: %v", r.Value)
		}
	})

	t.Run("ref nested in a double-quoted literal fails live, passes sandbox", func(t *testing.T) {
		src := `replace($.{x}, "$.{ref}", "")`
		r := run(t, src, ctx, live())
		var se *SyntaxError
		if !errors.As(r.Err, &se) {
			t.Errorf("live should fail to parse, got %v (substituted %s)", r.Err, r.Substituted)
		}
		if r := run(t, src, ctx, sandbox()); r.Err == nil {
			t.Logf("sandbox: %v (substituted %s)", r.Value, r.Substituted)
		}
	})

	t.Run("a guard does not protect a ref", func(t *testing.T) {
		src := `ifCondition($.{n3.count} > 0, $.{n3.output.0.name}, '')`
		r := run(t, src, ctx, live())
		var se *SyntaxError
		if !errors.As(r.Err, &se) || !strings.Contains(r.Err.Error(), "unresolved") {
			t.Errorf("want unresolved-reference failure, got %v", r.Err)
		}
	})

	t.Run("documented null-safe pattern works on an empty string", func(t *testing.T) {
		r := EvalExpression(`ifCondition(($.{amount} == ''), 0, $.{amount})`, "numeric", ctx, live())
		if r.Err != nil || r.Value.Num != 0 {
			t.Errorf("%v %v (substituted %s)", r.Value, r.Err, r.Substituted)
		}
	})

	t.Run("a null pastes as an empty string (recorded instance)", func(t *testing.T) {
		// dob2 was null on the instance and this produced '' without failing.
		src := `ifCondition( $.{nullish} == '', '', getYear(currentDate("Indian/Maldives")) - getYear( $.{nullish} ) )`
		r := EvalExpression(src, "string", ctx, live())
		if r.Err != nil || r.Value.Str != "" {
			t.Errorf("%v %v (substituted %q)", r.Value, r.Err, r.Substituted)
		}
	})

	t.Run("numbers paste bare", func(t *testing.T) {
		r := EvalExpression(`$.{price} * 2`, "numeric", ctx, live())
		if r.Err != nil || r.Value.Num != 25 || r.Substituted != "12.5 * 2" {
			t.Errorf("%v %v %q", r.Value, r.Err, r.Substituted)
		}
	})

	t.Run("whole-string template fills in plain text", func(t *testing.T) {
		r := EvalExpression(`"Name: $.{applicant} | Ref: $.{answer}"`, "string", ctx, live())
		if r.Err != nil || r.Value.Str != "Name: Ali | Ref: Yes" {
			t.Errorf("%v %v", r.Value, r.Err)
		}
		// A missing ref shows its placeholder on Expression V2.
		r = EvalExpression(`"Name: $.{nobody}"`, "string", ctx, live())
		if r.Value.Str != "Name: $.{nobody}" {
			t.Errorf("missing in template: %q", r.Value.Str)
		}
	})

	t.Run("an empty date does not fail (recorded run)", func(t *testing.T) {
		for src, want := range map[string]string{
			`daysDifference("", currentDate("Indian/Maldives"))`: "0",
			`getYear("")`:                "0",
			`formatDate("", 'DD/MM/YY')`: "",
			`addDays("", 3)`:             "",
		} {
			if r := run(t, src, ctx, live()); r.Err != nil || r.Value.Text() != want {
				t.Errorf("%s = %v %v", src, r.Value, r.Err)
			}
		}
		if r := run(t, `daysDifference("not a date", "2026-01-01")`, ctx, live()); r.Err == nil {
			t.Error("a non-empty bad date should still fail")
		}
	})

	t.Run("plus does not join text", func(t *testing.T) {
		if r := run(t, `'a' + 'b'`, ctx, live()); r.Err == nil || !strings.Contains(r.Err.Error(), "does not join text") {
			t.Errorf("%v", r.Err)
		}
	})

	t.Run("expression == is strict about types", func(t *testing.T) {
		if r := run(t, `5 == '5'`, ctx, live()); r.Err != nil || r.Value.Bool {
			t.Errorf("5 == '5' should be false in expressions: %v", r.Value)
		}
	})

	t.Run("only the chosen ifCondition branch is evaluated", func(t *testing.T) {
		if r := run(t, `ifCondition(true, 1, 1 / 0)`, ctx, live()); r.Err != nil || r.Value.Num != 1 {
			t.Errorf("%v %v", r.Value, r.Err)
		}
	})

	t.Run("return types", func(t *testing.T) {
		if r := EvalExpression(`'abc'`, "numeric", ctx, live()); r.Err == nil {
			t.Error("text into numeric should fail")
		}
		if r := EvalExpression(`'42'`, "numeric", ctx, live()); r.Err != nil || r.Value.Num != 42 {
			t.Errorf("'42' numeric: %v %v", r.Value, r.Err)
		}
		// A string return type keeps a computed number as a number.
		if r := EvalExpression(`2 + 2`, "string", ctx, live()); r.Value.Kind != Number || r.Value.Num != 4 {
			t.Errorf("numeric into string: %v", r.Value)
		}
	})
}

func TestConditions(t *testing.T) {
	ctx := MapContext{
		"n13":    map[string]any{"state": "Completed"},
		"n19":    map[string]any{"state": "Completed", "approval-form-v2": map[string]any{"approvalStatus": "Approved"}},
		"n3":     map[string]any{"count": 0},
		"n4":     map[string]any{"count": 2},
		"gender": "Female",
	}
	tests := []struct {
		src  string
		want bool
	}{
		{"", true},
		{`$.n13.state == "Completed"`, true},
		{`$.n13.state == "Cancelled"`, false},
		{`$.n19.state == "Completed" AND $.n19.approval-form-v2.approvalStatus != "Not Approved"`, true},
		{`$.n19.state == "Cancelled" OR $.n19.approval-form-v2.approvalStatus == "Not Approved"`, false},
		// Counts are stored as numbers but written as quoted strings.
		{`$.n3.count == "0"`, true},
		{`$.n3.count != "0"`, false},
		{`$.n4.count != "0"`, true},
		{`$.n4.count > 1`, true},
		{`$.gender == "Female" && $.n13.state == "Completed"`, true},
		{`($.gender == "Male") or ($.n3.count == "0")`, true},
	}
	for _, tt := range tests {
		r := EvalCondition(tt.src, ctx, live())
		if r.Err != nil || r.Pass != tt.want {
			t.Errorf("%s = %v (%v), want %v", tt.src, r.Pass, r.Err, tt.want)
		}
	}

	// A reference nobody produced: the condition is false, and reported.
	r := EvalCondition(`$.n99.state == "Approved"`, ctx, live())
	if r.Pass || len(r.Unresolved) != 1 || r.Unresolved[0] != "n99.state" {
		t.Errorf("unresolved: %+v", r)
	}
	if r := EvalCondition(`$.n13.state ==`, ctx, live()); r.Err == nil {
		t.Error("broken condition should error")
	}
}

func TestHazards(t *testing.T) {
	codes := func(src string) []string {
		var out []string
		for _, h := range Hazards(src) {
			out = append(out, h.Code)
		}
		return out
	}
	tests := []struct {
		src  string
		want []string
	}{
		{`replace($.{x}, "$.{ref}", "")`, []string{"nested-quoted-ref"}},
		{`'$.{flag}' == 'Yes'`, []string{"quoted-compare-always-false"}},
		{`'$.{flag}' == '"Yes"'`, nil},
		{`ifCondition($.{n3.count} > 0, $.{n3.output.0.name}, '')`, []string{"guard-does-not-protect"}},
		{`'Total: ' + $.{amount}`, []string{"plus-joins-text"}},
		{`$.{x} == null`, []string{"null-compare"}},
		{`"Name: $.{n3.output.0.name}"`, nil}, // whole-string template is the documented pattern
		{`ifCondition( $.{applicantCheck} == 'Self', $.{applicantID}, $.{otherID} )`, nil},
		// Quoted, so a missing row is inert text and cannot crash.
		{`ifCondition( '$.{n9.output.0.lm2.output.0.x}' == '"Yes"', 'a', 'b')`, nil},
	}
	for _, tt := range tests {
		got := codes(tt.src)
		if strings.Join(got, ",") != strings.Join(tt.want, ",") {
			t.Errorf("%s: hazards %v, want %v", tt.src, got, tt.want)
		}
	}
}

func TestRefs(t *testing.T) {
	got := Refs(`ifCondition($.{a} == '', $.{n3.output.0.x}, $.{a}) && $.n5.state == "Done"`)
	want := []string{"a", "n3.output.0.x", "n5.state"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("refs = %v", got)
	}
}

func TestSyntaxErrors(t *testing.T) {
	for _, src := range []string{"", "(1 + 2", "1 +", "foo", "'unterminated", "1 2", "ifCondition(1, 2)", "nosuch(1)", "#"} {
		r := run(t, src, nil, live())
		if r.Err == nil {
			t.Errorf("%q should fail, got %v", src, r.Value)
		}
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{`ifCondition($.{a} == '', 0, $.{a})`, `$.n3.count != "0" AND $.x == 'y'`, `'a' == "b"`, `-(1+2)*3/4`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		// Must never panic, whatever the input.
		Parse(s, ExpressionMode)
		Parse(s, ConditionMode)
		Hazards(s)
		EvalExpression(s, "string", MapContext{"a": "x"}, live())
	})
}
