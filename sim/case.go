// Package sim runs a workflow against a case file: the applicant's
// submission and every outside answer the platform would normally supply.
package sim

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Case is everything outside the workflow that decides how a run goes. Keys
// are node indexes ("n12"); loop children use "n5/lm1".
//
//	name: approved first-time applicant
//	submission:
//	  attributes: {island: Male, boats: [{name: Blue Fin}]}
//	  meta: {user_identifier: A123456}
//	tasks:
//	  n2: {state: Completed}
//	  n4: {state: Approved, form: {approval-form: {remarks: ok}}}
//	lookups:
//	  n3: []                    # DataHub select: the rows it finds
//	  n5/lm1: [[{id: 1}], []]   # inside a loop: one result per iteration
//	payments: {n17: paid}
//	external: {n7: {next_state: Accepted}}
type Case struct {
	Name        string     `yaml:"name"`
	Description string     `yaml:"description"`
	Submission  Submission `yaml:"submission"`
	// Variables sets workflow variables directly, after the trigger map.
	// Replay uses it to start from a recorded context.
	Variables map[string]any            `yaml:"variables"`
	Tasks     map[string]TaskAnswers    `yaml:"tasks"`
	Lookups   map[string]any            `yaml:"lookups"`
	Payments  map[string]string         `yaml:"payments"`
	External  map[string]map[string]any `yaml:"external"`
}

// Submission is the form submission that starts the workflow. Trigger map
// entries like $.attributes.island and $.meta.user_identifier read from it.
type Submission struct {
	Attributes map[string]any `yaml:"attributes"`
	Meta       map[string]any `yaml:"meta"`
}

// TaskAnswer is what the officer did with a task: the task state, plus the
// fields of any form attached to it, keyed by form slug.
type TaskAnswer struct {
	State   string                    `yaml:"state"`
	Remarks string                    `yaml:"remarks"`
	Form    map[string]map[string]any `yaml:"form"`
}

// TaskAnswers is one answer per visit. A task can come round again after a
// send-back, so a case may list answers in order:
//
//	n5: [{state: Returned}, {state: Completed}]
//
// A single answer (not a list) applies to every visit.
type TaskAnswers []TaskAnswer

func (t *TaskAnswers) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.SequenceNode {
		var list []TaskAnswer
		if err := n.Decode(&list); err != nil {
			return err
		}
		*t = list
		return nil
	}
	var one TaskAnswer
	if err := n.Decode(&one); err != nil {
		return err
	}
	*t = TaskAnswers{one}
	return nil
}

// answer returns the answer for a task's visit (0-based). Past the end of
// the list, the last answer repeats, unless strict is set.
func (c *Case) answer(key string, visit int, strict bool) (TaskAnswer, bool) {
	list := c.Tasks[key]
	switch {
	case visit < len(list):
		return list[visit], list[visit].State != ""
	case len(list) > 0 && !strict:
		last := list[len(list)-1]
		return last, last.State != ""
	}
	return TaskAnswer{}, false
}

// SetTask records the answer for a task's first visit.
func (c *Case) SetTask(key string, a TaskAnswer) {
	if c.Tasks == nil {
		c.Tasks = map[string]TaskAnswers{}
	}
	list := c.Tasks[key]
	if len(list) == 0 {
		list = TaskAnswers{a}
	} else {
		list[0] = a
	}
	c.Tasks[key] = list
}

// Task returns the first-visit answer.
func (c *Case) Task(key string) TaskAnswer {
	if list := c.Tasks[key]; len(list) > 0 {
		return list[0]
	}
	return TaskAnswer{}
}

// LoadCase reads a case file (YAML, or JSON, which YAML also accepts).
func LoadCase(path string) (*Case, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Case
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true) // a typo in a key should not silently do nothing
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	c.normalise()
	if c.Name == "" {
		c.Name = strings.TrimSuffix(path[strings.LastIndexAny(path, "/\\")+1:], ".yaml")
	}
	return &c, nil
}

// normalise turns YAML's map[string]any-with-interface-keys into plain JSON
// shapes, so values look the same as in a real instance context.
func (c *Case) normalise() {
	c.Submission.Attributes = normMap(c.Submission.Attributes)
	c.Submission.Meta = normMap(c.Submission.Meta)
	if c.Variables != nil {
		c.Variables = normMap(c.Variables)
	}
	for k, v := range c.Lookups {
		c.Lookups[k] = norm(v)
	}
	for k, list := range c.Tasks {
		for i := range list {
			for slug, f := range list[i].Form {
				list[i].Form[slug] = normMap(f)
			}
		}
		c.Tasks[k] = list
	}
}

func normMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = norm(v)
	}
	return out
}

func norm(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return normMap(t)
	case map[any]any:
		m := make(map[string]any, len(t))
		for k, e := range t {
			m[fmt.Sprint(k)] = norm(e)
		}
		return m
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = norm(e)
		}
		return out
	case int:
		return float64(t)
	case int64:
		return float64(t)
	}
	return v
}

// keys lists map keys in order, for stable messages.
func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
