package exec

import (
	"fmt"
	"strings"
)

// splitCommand turns the TUI's one-line command input into an argv without
// invoking a shell. It supports whitespace separation, single and double
// quotes and backslash escapes; it performs no variable, glob or operator
// expansion. The CLI never uses this: there the user supplies the exact argv
// after `--`.
func splitCommand(input string) ([]string, error) {
	var args []string
	var current strings.Builder
	inSingle, inDouble := false, false
	hasToken := false
	escaped := false
	for _, r := range input {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
			hasToken = true
		case r == '\\' && !inSingle:
			escaped = true
			hasToken = true
		case r == '\'' && !inDouble:
			inSingle = !inSingle
			hasToken = true
		case r == '"' && !inSingle:
			inDouble = !inDouble
			hasToken = true
		case (r == ' ' || r == '\t' || r == '\n') && !inSingle && !inDouble:
			if hasToken {
				args = append(args, current.String())
				current.Reset()
				hasToken = false
			}
		default:
			current.WriteRune(r)
			hasToken = true
		}
	}
	if escaped {
		return nil, fmt.Errorf("명령이 역슬래시로 끝납니다")
	}
	if inSingle || inDouble {
		return nil, fmt.Errorf("명령의 따옴표가 닫히지 않았습니다")
	}
	if hasToken {
		args = append(args, current.String())
	}
	if len(args) == 0 {
		return nil, fmt.Errorf("명령이 비어 있습니다")
	}
	return args, nil
}
