# How the engine behaves, and how sure we are

Drydock models OneGov's Expression V2 and transition conditions from the platform's own documentation and from incidents written up after they happened. Some points the documentation states plainly. Others had to be inferred, because two documented facts only fit together one way. Every inferred point is a setting in `expr.Policy`, and milestone 4 (replaying recorded instances) will confirm or change it.

## Documented

| Behaviour | Source |
|---|---|
| References are pasted into the text before parsing, so `ifCondition` cannot protect a reference in either branch. | Pitfalls checklist, "Substitution happens before parsing". Confirmed by the a real workflow outage. |
| A missing reference stays as `$.{…}` on Expression V2 (the instance fails) and becomes blank on every other control. | SKILL.md core rule 5 |
| String values are pasted in double quotes on the live engine and single quotes in `test_workflow_expression`. | SKILL.md core rule 8 |
| `'$.{ref}' == 'Yes'` is always false; `== '"Yes"'` is the working form. | SKILL.md core rule 8, and a recorded incident |
| A reference inside a double-quoted literal used as an argument fails the parse. | SKILL.md core rule 8, and a recorded incident |
| `+` does not join text. | SKILL.md core rule 1 |
| `getMonth` is zero-based. | Function reference examples |
| `Asia/Male` is accepted as a timezone. It is not an IANA name, so Drydock maps it to `Indian/Maldives`. | snippets.md, function reference |

## Confirmed by recorded instances

| Behaviour | Evidence |
|---|---|
| A null value is pasted as `""`, not as nothing. | Recorded instance: `dob2` null, `ifCondition( $.{dob2} == '', '', … )` gave `''` and did not fail. |
| Every declared workflow variable is in the context from the start; unset ones are null. | SPA instance context: 109 of 109 variables present, 12 null. |
| Numeric variables hold what the form sent, usually strings (`iron: "10"`). | Same context. |
| Strings are pasted in double quotes, numbers bare, nulls as `""`: all 423 substitutions in a 26-leg run, compared character for character with the engine's `leg_data`. | `drydock replay` on a recorded run |
| A `string` return type does not turn a number into text. | Same run: `age0` (string) stored `6`; `rentReceived` (string) computed `0` and was pasted downstream as a bare `0`. |
| An empty date does not fail date functions. | Same run: `daysDifference( "" , currentDate(…) )` was evaluated in n11 and the instance continued. Drydock returns 0 from difference and part functions and `""` from the others; those values are inferred. |
| DataHub inserts and updates return only `{"state": "Success"}` (capital S), with no rows; selects return `count`, `output` and `"success"`. | Recorded contexts: n4 and n134 (writes) vs n125 and n126 (selects). |
| Only the chosen `ifCondition` branch is evaluated (the other is parsed, not run): `getYear('')` in the skipped branch did not fail. | Same instance. |

## Inferred (settings in `expr.Policy`)

| Setting | Default | Why |
|---|---|---|
| strings quoted when empty | always | The documented `ifCondition(($.{amount} == ''), 0, $.{amount})` only parses if an empty string arrives as `""`. |
| `Templates` | on | Core rule 1 recommends `"Name: $.{x}"` for text outputs. With quoted pasting that cannot parse, so a whole-expression string must be filled in as plain text. |
| `LooseEquality` | off | The function reference says `==` needs both sides to be the same type. |
| `ConditionLooseEquality` | on | Conditions compare `$.n3.count != "0"` while the context stores the number 0. Workflows written that way route correctly in production. |

## Open

- **Several matching transitions.** Does the engine run all of them as parallel branches, or only the first? The review skill says both in different places. One real workflow has a task with Approved and Rejected edges plus an unconditional edge to an expression node. The recorded run never had more than one match at a node, so it cannot say. Replaying the legs of any instance that passed such a node settles it: run `drydock replay` with and without `-fork all` and see which route matches.
- **A string-typed variable holding a number-like value** (a phone number, say): quoted or bare? Every number-like value in the recordings so far went in bare, but none was a string-typed variable, so the rule could be "by value" or "by declared type".
- **Number rendering.** Whether `4.5 + 2.5` comes out as `7` or `7.0` when pasted into text.
- **Escapes inside string literals.** Drydock treats backslash as an escape.
- **Empty string into a numeric return type.** One older recorded run (version unknown) stored `0` where a newer one, whose output is declared `string`, stored `''`. Drydock currently fails a numeric output given text.

## Documentation errors found

- `charAt('Tradenet', 5)` is listed as `'e'`. Counting from 0, as `charAt('Tradenet', 0) = 'T'` shows, position 5 is `'n'`.
