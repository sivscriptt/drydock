package expr

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // timezone names work on any machine
	"unicode"
)

type fn struct {
	min, max int
	call     func(ev *evaluator, args []Value) (Value, error)
}

var funcs map[string]fn

func init() {
	funcs = map[string]fn{
		// ifCondition is special-cased in call(): only the chosen branch is
		// evaluated. Both branches were already parsed, which is the
		// engine's real trap (see Hazards).
		"ifCondition": {3, 3, nil},

		// Strings. The names and behaviour follow Java's String API.
		"charAt": {2, 2, func(_ *evaluator, a []Value) (Value, error) {
			r, i := []rune(a[0].Text()), int(num(a[1]))
			if i < 0 || i >= len(r) {
				return Value{}, fmt.Errorf("charAt index %d out of range", i)
			}
			return Str(string(r[i])), nil
		}},
		"compareIgnoreCase": {2, 2, func(_ *evaluator, a []Value) (Value, error) {
			return Num(float64(strings.Compare(strings.ToLower(a[0].Text()), strings.ToLower(a[1].Text())))), nil
		}},
		"endsWith": {2, 2, func(_ *evaluator, a []Value) (Value, error) {
			return Boolean(strings.HasSuffix(a[0].Text(), a[1].Text())), nil
		}},
		"equals": {2, 2, func(_ *evaluator, a []Value) (Value, error) { return Boolean(a[0].Text() == a[1].Text()), nil }},
		"equalsIgnoreCase": {2, 2, func(_ *evaluator, a []Value) (Value, error) {
			return Boolean(strings.EqualFold(a[0].Text(), a[1].Text())), nil
		}},
		"indexOf": {2, 3, func(_ *evaluator, a []Value) (Value, error) {
			r, sub := []rune(a[0].Text()), a[1].Text()
			from := clampFrom(a, 2, len(r))
			i := strings.Index(string(r[from:]), sub)
			if i < 0 {
				return Num(-1), nil
			}
			return Num(float64(from + len([]rune(string(r[from:])[:i])))), nil
		}},
		"lastIndexOf": {2, 3, func(_ *evaluator, a []Value) (Value, error) {
			r, sub := []rune(a[0].Text()), a[1].Text()
			to := len(r)
			if len(a) > 2 {
				to = min(len(r), int(num(a[2]))+len([]rune(sub)))
			}
			if to < 0 {
				return Num(-1), nil
			}
			i := strings.LastIndex(string(r[:to]), sub)
			if i < 0 {
				return Num(-1), nil
			}
			return Num(float64(len([]rune(string(r[:to])[:i])))), nil
		}},
		"length": {1, 1, func(_ *evaluator, a []Value) (Value, error) { return Num(float64(len([]rune(a[0].Text())))), nil }},
		"replace": {3, 3, func(_ *evaluator, a []Value) (Value, error) {
			return Str(strings.ReplaceAll(a[0].Text(), a[1].Text(), a[2].Text())), nil
		}},
		"startsWith": {2, 3, func(_ *evaluator, a []Value) (Value, error) {
			r := []rune(a[0].Text())
			return Boolean(strings.HasPrefix(string(r[clampFrom(a, 2, len(r)):]), a[1].Text())), nil
		}},
		"substring": {2, 3, func(_ *evaluator, a []Value) (Value, error) {
			r := []rune(a[0].Text())
			from, to := int(num(a[1])), len(r)
			if len(a) > 2 {
				to = int(num(a[2]))
			}
			if from < 0 || to > len(r) || from > to {
				return Value{}, fmt.Errorf("substring(%d, %d) out of range for length %d", from, to, len(r))
			}
			return Str(string(r[from:to])), nil
		}},
		"toLower": {1, 1, func(_ *evaluator, a []Value) (Value, error) { return Str(strings.ToLower(a[0].Text())), nil }},
		"toUpper": {1, 1, func(_ *evaluator, a []Value) (Value, error) { return Str(strings.ToUpper(a[0].Text())), nil }},
		"trim":    {1, 1, func(_ *evaluator, a []Value) (Value, error) { return Str(strings.TrimSpace(a[0].Text())), nil }},
		"properCase": {1, 1, func(_ *evaluator, a []Value) (Value, error) {
			words := strings.Fields(strings.ToLower(a[0].Text()))
			for i, w := range words {
				r := []rune(w)
				r[0] = unicode.ToUpper(r[0])
				words[i] = string(r)
			}
			return Str(strings.Join(words, " ")), nil
		}},

		// Numbers.
		"absolute": {1, 1, numFn(math.Abs)},
		"ceil":     {1, 1, numFn(math.Ceil)},
		"maximum":  {2, 2, func(_ *evaluator, a []Value) (Value, error) { return num2(a, math.Max) }},
		"minimum":  {2, 2, func(_ *evaluator, a []Value) (Value, error) { return num2(a, math.Min) }},
		"roundNumber": {2, 2, func(_ *evaluator, a []Value) (Value, error) {
			x, ok := a[0].numeric()
			if !ok {
				return Value{}, fmt.Errorf("roundNumber needs a number, got %s", a[0])
			}
			p := math.Pow(10, math.Floor(num(a[1])))
			return Num(math.Round(x*p) / p), nil
		}},
		"numberFormat": {2, 2, func(_ *evaluator, a []Value) (Value, error) {
			x, ok := a[0].numeric()
			if !ok {
				return Value{}, fmt.Errorf("numberFormat needs a number, got %s", a[0])
			}
			return Str(strconv.FormatFloat(x, 'f', int(num(a[1])), 64)), nil
		}},

		// Dates. Inputs are "YYYY-MM-DD" or RFC 3339; outputs keep the shape
		// of the input.
		"addDays":   {2, 2, dateAdd(func(t time.Time, n int) time.Time { return t.AddDate(0, 0, n) })},
		"addMonths": {2, 2, dateAdd(addMonthsClamped)},
		"addYears":  {2, 2, dateAdd(func(t time.Time, n int) time.Time { return addMonthsClamped(t, 12*n) })},
		"addHours":  {2, 2, dateAdd(func(t time.Time, n int) time.Time { return t.Add(time.Duration(n) * time.Hour) })},
		"createDate": {3, 3, func(_ *evaluator, a []Value) (Value, error) {
			return Str(time.Date(int(num(a[0])), time.Month(num(a[1])), int(num(a[2])), 0, 0, 0, 0, time.UTC).Format(dateOnly)), nil
		}},
		"createDateTime": {6, 7, func(_ *evaluator, a []Value) (Value, error) {
			loc := time.UTC
			if len(a) == 7 {
				l, err := loadLocation(a[6].Text())
				if err != nil {
					return Value{}, fmt.Errorf("unknown timezone %q", a[6].Text())
				}
				loc = l
			}
			t := time.Date(int(num(a[0])), time.Month(num(a[1])), int(num(a[2])), int(num(a[3])), int(num(a[4])), int(num(a[5])), 0, loc)
			return Str(t.Format(time.RFC3339)), nil
		}},
		"getDay":   {1, 1, datePart(func(t time.Time) int { return t.Day() })},
		"getMonth": {1, 1, datePart(func(t time.Time) int { return int(t.Month()) - 1 })}, // zero-based, as documented
		"getYear":  {1, 1, datePart(func(t time.Time) int { return t.Year() })},
		"currentDate": {0, 1, func(ev *evaluator, a []Value) (Value, error) {
			t, err := nowIn(ev, a)
			return Str(t.Format(dateOnly)), err
		}},
		"currentDateTime": {0, 1, func(ev *evaluator, a []Value) (Value, error) {
			t, err := nowIn(ev, a)
			return Str(t.Format(time.RFC3339)), err
		}},
		"hoursDifference": {2, 2, func(_ *evaluator, a []Value) (Value, error) {
			if blankDate(a[0]) || blankDate(a[1]) {
				return Num(0), nil
			}
			x, y, err := twoDates(a)
			return Num(math.Trunc(y.Sub(x).Hours())), err
		}},
		"daysDifference": {2, 2, func(_ *evaluator, a []Value) (Value, error) {
			if blankDate(a[0]) || blankDate(a[1]) {
				return Num(0), nil
			}
			x, y, err := twoDates(a)
			return Num(math.Trunc(y.Sub(x).Hours() / 24)), err
		}},
		"formatDate": {2, 2, func(_ *evaluator, a []Value) (Value, error) {
			if blankDate(a[0]) {
				return Str(""), nil
			}
			t, _, err := parseDate(a[0].Text())
			if err != nil {
				return Value{}, err
			}
			return Str(formatDate(t, a[1].Text())), nil
		}},
	}
}

func (ev *evaluator) call(n callNode) (Value, error) {
	f, ok := funcs[n.name]
	if !ok {
		return Value{}, &EvalError{n.p, fmt.Sprintf("unknown function %s()", n.name)}
	}
	if len(n.args) < f.min || len(n.args) > f.max {
		return Value{}, &EvalError{n.p, fmt.Sprintf("%s() takes %d to %d arguments, got %d", n.name, f.min, f.max, len(n.args))}
	}
	if n.name == "ifCondition" {
		c, err := ev.eval(n.args[0])
		if err != nil {
			return c, err
		}
		if c.truthy() {
			return ev.eval(n.args[1])
		}
		return ev.eval(n.args[2])
	}
	args := make([]Value, len(n.args))
	for i, a := range n.args {
		v, err := ev.eval(a)
		if err != nil {
			return v, err
		}
		args[i] = v
	}
	v, err := f.call(ev, args)
	if err != nil {
		return v, &EvalError{n.p, n.name + ": " + err.Error()}
	}
	return v, nil
}

func num(v Value) float64 { f, _ := v.numeric(); return f }

func clampFrom(a []Value, i, n int) int {
	if len(a) <= i {
		return 0
	}
	return max(0, min(n, int(num(a[i]))))
}

func numFn(f func(float64) float64) func(*evaluator, []Value) (Value, error) {
	return func(_ *evaluator, a []Value) (Value, error) {
		x, ok := a[0].numeric()
		if !ok {
			return Value{}, fmt.Errorf("needs a number, got %s", a[0])
		}
		return Num(f(x)), nil
	}
}

func num2(a []Value, f func(float64, float64) float64) (Value, error) {
	x, ok1 := a[0].numeric()
	y, ok2 := a[1].numeric()
	if !ok1 || !ok2 {
		return Value{}, fmt.Errorf("needs numbers, got %s and %s", a[0], a[1])
	}
	return Num(f(x, y)), nil
}

const dateOnly = "2006-01-02"

// parseDate accepts a date or a date-time and reports whether it had a time.
func parseDate(s string) (time.Time, bool, error) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(dateOnly, s); err == nil {
		return t, false, nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true, nil
		}
	}
	return time.Time{}, false, fmt.Errorf("%q is not a date", s)
}

// blankDate: an empty date does not fail the instance. A recorded SPA run
// evaluated daysDifference("", currentDate(...)) and carried on. Empty dates
// give 0 from the difference and part functions and "" from the rest.
// (That it does not fail is recorded; the exact values are inferred.)
func blankDate(v Value) bool { return strings.TrimSpace(v.Text()) == "" }

func dateAdd(f func(time.Time, int) time.Time) func(*evaluator, []Value) (Value, error) {
	return func(_ *evaluator, a []Value) (Value, error) {
		if blankDate(a[0]) {
			return Str(""), nil
		}
		t, hasTime, err := parseDate(a[0].Text())
		if err != nil {
			return Value{}, err
		}
		out := f(t, int(num(a[1])))
		if hasTime {
			return Str(out.Format(time.RFC3339)), nil
		}
		return Str(out.Format(dateOnly)), nil
	}
}

// addMonthsClamped keeps the day inside the target month: 31 Jan + 1 month
// is 28 Feb, not 3 March. The docs' example (31 Jan + 2 = 31 Mar) holds.
func addMonthsClamped(t time.Time, n int) time.Time {
	y, m, d := t.Date()
	first := time.Date(y, m+time.Month(n), 1, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
	last := first.AddDate(0, 1, -1).Day()
	return first.AddDate(0, 0, min(d, last)-1)
}

func datePart(f func(time.Time) int) func(*evaluator, []Value) (Value, error) {
	return func(_ *evaluator, a []Value) (Value, error) {
		if blankDate(a[0]) {
			return Num(0), nil
		}
		t, _, err := parseDate(a[0].Text())
		if err != nil {
			return Value{}, err
		}
		return Num(float64(f(t))), nil
	}
}

func twoDates(a []Value) (time.Time, time.Time, error) {
	x, _, err := parseDate(a[0].Text())
	if err != nil {
		return x, x, err
	}
	y, _, err := parseDate(a[1].Text())
	return x, y, err
}

func nowIn(ev *evaluator, a []Value) (time.Time, error) {
	if len(a) == 0 || a[0].Text() == "" {
		return ev.now, nil
	}
	loc, err := loadLocation(a[0].Text())
	if err != nil {
		return time.Time{}, fmt.Errorf("unknown timezone %q", a[0].Text())
	}
	return ev.now.In(loc), nil
}

// formatDate supports the documented tokens: YYYY, YY, Month, Mon, MM,
// DDth, DD.
func formatDate(t time.Time, layout string) string {
	r := strings.NewReplacer(
		"YYYY", fmt.Sprintf("%04d", t.Year()),
		"YY", fmt.Sprintf("%02d", t.Year()%100),
		"Month", t.Month().String(),
		"Mon", t.Month().String()[:3],
		"MM", fmt.Sprintf("%02d", int(t.Month())),
		"DDth", fmt.Sprintf("%d%s", t.Day(), ordinal(t.Day())),
		"DD", fmt.Sprintf("%02d", t.Day()),
	)
	return r.Replace(layout)
}

func ordinal(d int) string {
	if d%100 >= 11 && d%100 <= 13 {
		return "th"
	}
	switch d % 10 {
	case 1:
		return "st"
	case 2:
		return "nd"
	case 3:
		return "rd"
	}
	return "th"
}

// loadLocation accepts IANA names plus "Asia/Male", which OneGov workflows
// use everywhere but which is not an IANA zone (the real one is
// Indian/Maldives).
func loadLocation(name string) (*time.Location, error) {
	if strings.EqualFold(strings.TrimSpace(name), "Asia/Male") {
		name = "Indian/Maldives"
	}
	return time.LoadLocation(name)
}
