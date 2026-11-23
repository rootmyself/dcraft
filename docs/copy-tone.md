# Product copy tone

dcraft's product copy is short and fragment-style. The reader scans it in a
terminal; a formal sentence ending costs a line and adds nothing.

## Scope

Applies to:

- TUI labels, hints, prompts, notices, confirmation and failure text
- CLI help, usage, flag descriptions, errors and next actions
- JSON `message` and `next_actions` fields
- Printed guidance from generated scripts (`say`, `die`, and equivalent)
- Descriptors shown in both surfaces: title, summary, usage

Does not apply to:

- `docs/` and `README` explanatory prose
- Go comments
- Generated script header comments, which document the artifact like a README
- Keycap labels, flag names, machine values, paths, code and command examples
- Test fixtures that quote the old wording on purpose

## Rules

1. End with a noun, a verb stem, a value, or a label. Never end with `~합니다`,
   `~입니다`, `~습니다`, `~하세요`, `~으세요`, `~십시오`, `~까요`, or `~나요`.
2. State the fact or the action first. Drop greetings, encouragement and filler.
3. Keep a failure useful: state the cause, then the next action. Short is not a
   reason to hide either one.
4. Use `예:` and ` · ` for examples. List inputs add `한 줄에 하나`.
5. Keep labels to one or two words. Keep commands, flags, paths and machine
   values exactly as they are.
6. Dry tone only. No jokes, exclamation marks, or vague reassurance.

## Before and after

| Before | After |
| --- | --- |
| 데이터를 백업합니다. | 데이터 백업 |
| 스택 상태를 확인하세요. | 스택 상태 확인 |
| 승인이 필요합니다. | 승인 필요 |
| 결과를 표시할 수 없습니다. | 결과 표시 불가 |
| 다시 실행하지 않았습니다. | 다시 실행 안 함 |
| 다음 작업을 실행할까요? | 작업 실행 확인 |
| Docker Desktop을 시작하세요. | Docker Desktop 시작 |
| 저장 폴더의 권한과 여유 공간을 확인하세요. | 저장 폴더 권한·여유 공간 확인 |
| 화면을 닫으면 일부 변경이 남을 수 있습니다. | 화면을 닫으면 일부 변경이 남을 수 있음 |

Connective clauses stay prose: `…한 뒤 다시 실행`, `…하면 중단`, `…이므로 확인`.
Only the final ending changes.

## Guard

`internal/copyguard/tone_test.go` scans string literals in production Go files
under `internal/` and `cmd/`. It ignores comments, tests, `testdata`, and shell
comment lines inside generated-script literals.

`toneAllowlist` in that test names the only exceptions: a string that quotes
another tool's output or reproduces an exact command/example. Add an entry only
with the reason next to it.
