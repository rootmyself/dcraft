package hooks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/secretref"
)

// Execute implements core.HookService. It re-hashes the script immediately
// before execution, so an approval is bound to the content that actually runs.
// The digest check narrows but cannot eliminate the time-of-check/time-of-use
// window (an in-place edit or symlink retarget after the final hash can still
// change what the interpreter reads). dcraft does not sandbox the script and
// does not undo its external effects.
func (s *Service) Execute(ctx context.Context, req core.HookRequest) (core.HookExecution, error) {
	if err := ctx.Err(); err != nil {
		return core.HookExecution{}, err
	}
	resolved, err := ResolvePath(req.Target.ProjectDir, req.Entry.Path)
	if err != nil {
		return core.HookExecution{}, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return core.HookExecution{}, invalid(fmt.Sprintf(msgScriptMissing, req.Entry.Path), resolved)
		}
		return core.HookExecution{}, invalid(msgScriptUnreadable, err.Error())
	}
	if info.IsDir() || !info.Mode().IsRegular() {
		return core.HookExecution{}, invalid(fmt.Sprintf(msgScriptNotRegular, req.Entry.Path), resolved)
	}
	if info.Mode().Perm()&0o111 == 0 {
		return core.HookExecution{}, nonExecutable(req.Entry.Path, resolved)
	}
	sha, err := contentSHA256(resolved)
	if err != nil {
		return core.HookExecution{}, invalid(msgScriptUnreadable, err.Error())
	}
	if sha != req.Entry.SHA256 {
		return core.HookExecution{}, core.Fail(core.Diagnostic{
			Category: core.CategoryHookContentChanged,
			Severity: core.SeverityError,
			Message:  fmt.Sprintf("승인 이후 스크립트 내용이 바뀌었습니다: %s (%s). 이전 승인을 재사용하지 않습니다.", req.Entry.Path, req.Entry.Stage),
			Detail:   fmt.Sprintf("approved_sha256=%s current_sha256=%s", req.Entry.SHA256, sha),
			NextActions: []string{
				"스크립트 내용을 확인한 뒤 다시 실행해 스크립트 신뢰를 승인하세요.",
				"dcraft는 이 스크립트를 실행하지 않았습니다.",
			},
		})
	}

	cmd := exec.Command(resolved)
	cmd.Dir = req.Target.ProjectDir
	cmd.Env = hookEnvironment(req, resolved)
	cmd.Stdin = nil
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	setProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		return core.HookExecution{}, startError(req.Entry.Path, err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var waitErr error
	select {
	case waitErr = <-done:
	case <-ctx.Done():
		_ = killProcessGroup(cmd.Process)
		waitErr = <-done
		text := s.redact(req.Target, output.String())
		if ctxErr := ctx.Err(); ctxErr != nil {
			return core.HookExecution{ExitCode: exitCode(waitErr), Output: text}, ctxErr
		}
		return core.HookExecution{ExitCode: exitCode(waitErr), Output: text}, waitErr
	}
	text := s.redact(req.Target, output.String())
	return core.HookExecution{ExitCode: exitCode(waitErr), Output: text}, nil
}

func hookEnvironment(req core.HookRequest, resolved string) []string {
	values := map[string]string{}
	for _, item := range os.Environ() {
		if key, value, ok := strings.Cut(item, "="); ok {
			values[key] = value
		}
	}
	values["DCRAFT_HOOK_STAGE"] = string(req.Entry.Stage)
	values["DCRAFT_HOOK_PATH"] = resolved
	values["DCRAFT_HOOK_SHA256"] = req.Entry.SHA256
	values["DCRAFT_ACTION"] = req.Action
	values["DCRAFT_OPERATION_ID"] = req.OperationID
	values["DCRAFT_PROJECT_DIR"] = req.Target.ProjectDir
	values["DCRAFT_PROJECT_NAME"] = req.Target.ProjectName
	values["DCRAFT_ENVIRONMENT"] = req.Target.Environment
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, key+"="+values[key])
	}
	return env
}

// redact removes values of environment-sourced runtime secret references that
// dcraft knows about. It cannot know values the script obtains or prints from
// other sources, and it makes no promise about them.
func (s *Service) redact(target core.Target, text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	values := s.knownSecretValues(target.ProjectDir)
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	for _, value := range values {
		if value == "" {
			continue
		}
		text = strings.ReplaceAll(text, value, msgRedacted)
	}
	return text
}

func (s *Service) knownSecretValues(projectDir string) []string {
	if s.getenv == nil {
		return nil
	}
	cat, err := secretref.Load(projectDir)
	if err != nil {
		return nil
	}
	var values []string
	for _, ref := range cat.References {
		if ref.Source != secretref.SourceEnv {
			continue
		}
		if value := s.getenv(ref.Ref); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func nonExecutable(path, resolved string) *core.Error {
	return core.Fail(core.Diagnostic{
		Category: core.CategoryHookInvalid,
		Severity: core.SeverityError,
		Message:  fmt.Sprintf(msgScriptNotExecutable, path),
		Detail:   resolved,
		NextActions: []string{
			"chmod +x로 실행 권한을 부여한 뒤 다시 실행하세요.",
			"dcraft는 인터프리터를 추측해 대신 실행하지 않습니다.",
		},
	})
}

func startError(path string, err error) *core.Error {
	return core.FailCause(err, core.Diagnostic{
		Category:    core.CategoryHookInvalid,
		Severity:    core.SeverityError,
		Message:     fmt.Sprintf("스크립트를 실행 형식으로 시작하지 못했습니다: %s — 실행 권한과 첫 줄 셔뱅을 확인하세요.", path),
		Detail:      err.Error(),
		NextActions: []string{"스크립트의 실행 권한과 첫 줄(셔뱅)을 확인하세요.", "dcraft는 인터프리터를 추측하지 않습니다."},
	})
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if code := exitErr.ExitCode(); code >= 0 {
			return code
		}
		return -1
	}
	return -1
}
