package secretref

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/execx"
)

// The AWS CLI is the access path. It already owns the operator's credential
// chain (environment, shared config/credentials, SSO, instance role), so
// dcraft never reads, stores or passes credential material. The narrow execx
// boundary keeps the CLI fake-able in tests.
const awsCLIName = "aws"

var awsErrorCodeRe = regexp.MustCompile(`\(([A-Za-z0-9_.-]{1,64})\)`)

// awsFetcher resolves AWS references for one operation. The cache is scoped to
// the caller: a secret id (+ region/profile) is fetched at most once per
// operation, never across operations.
type awsFetcher struct {
	runner execx.Runner
	cache  map[string][]byte
}

func newAWSFetcher(runner execx.Runner) *awsFetcher {
	if runner == nil {
		runner = execx.NewOSRunner()
	}
	return &awsFetcher{runner: runner, cache: map[string][]byte{}}
}

// fetch returns the selected value for one reference. It never returns a value
// with an error, and errors never contain the secret string or raw CLI output.
func (f *awsFetcher) fetch(ctx context.Context, ref Reference) ([]byte, error) {
	secretString, err := f.secretString(ctx, ref)
	if err != nil {
		return nil, err
	}
	value, err := selectAWSValue(secretString, ref.Field)
	if err != nil {
		return nil, err
	}
	if len(value) == 0 || strings.TrimSpace(string(value)) == "" {
		return nil, awsValueError(msgAWSValueEmpty, "SecretString or selected field is empty", []string{
			msgAWSValueActionOne, msgAWSValueActionTwo,
		})
	}
	if bytes.IndexByte(value, 0) >= 0 {
		return nil, awsValueError(msgAWSValueInvalid, "value contains a NUL byte", []string{
			msgAWSValueActionTwo,
		})
	}
	return value, nil
}

// secretString returns the raw SecretString bytes. --output json preserves the
// exact string: the CLI's text mode appends a newline and prints "None" for an
// absent binary-only value, which is ambiguous with a literal "None".
func (f *awsFetcher) secretString(ctx context.Context, ref Reference) ([]byte, error) {
	key := strings.Join([]string{strings.TrimSpace(ref.Ref), strings.TrimSpace(ref.Region), strings.TrimSpace(ref.Profile)}, "\x00")
	if cached, ok := f.cache[key]; ok {
		return cached, nil
	}
	if _, err := f.runner.LookPath(awsCLIName); err != nil {
		return nil, awsCLIError(err.Error())
	}
	args := []string{
		"secretsmanager", "get-secret-value",
		"--secret-id", strings.TrimSpace(ref.Ref),
		"--query", "SecretString",
		"--output", "json",
		"--no-cli-pager",
	}
	if region := strings.TrimSpace(ref.Region); region != "" {
		args = append(args, "--region", region)
	}
	if profile := strings.TrimSpace(ref.Profile); profile != "" {
		args = append(args, "--profile", profile)
	}
	res, err := f.runner.Run(ctx, execx.Command{Name: awsCLIName, Args: args})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil {
		return nil, awsCLIError(err.Error())
	}
	if !res.Succeeded() {
		return nil, classifyAWSFailure(res)
	}

	var decoded any
	if err := json.Unmarshal(bytes.TrimSpace(res.Stdout), &decoded); err != nil {
		return nil, awsValueError(msgAWSValueInvalid, "aws cli returned unparseable SecretString JSON", []string{
			msgAWSValueActionOne, msgAWSValueActionTwo,
		})
	}
	text, ok := decoded.(string)
	if !ok {
		// null means the secret has no SecretString (binary-only or absent).
		return nil, awsValueError(msgAWSValueAbsent, "SecretString is null or not a string", []string{
			msgAWSValueActionOne, msgAWSValueActionTwo,
		})
	}
	value := []byte(text)
	f.cache[key] = value
	return value, nil
}

// selectAWSValue walks a dot path into a JSON SecretString. Numeric segments
// index arrays. Only one scalar can be delivered; object/array leaves are
// refused instead of serialized implicitly.
func selectAWSValue(secretString []byte, field string) ([]byte, error) {
	field = strings.TrimSpace(field)
	if field == "" {
		return secretString, nil
	}
	dec := json.NewDecoder(bytes.NewReader(secretString))
	dec.UseNumber()
	var root any
	if err := dec.Decode(&root); err != nil {
		return nil, awsFieldError(field, "SecretString is not valid JSON")
	}
	current := root
	for _, segment := range strings.Split(field, ".") {
		switch node := current.(type) {
		case map[string]any:
			next, ok := node[segment]
			if !ok {
				return nil, awsFieldError(field, "path segment not found")
			}
			current = next
		case []any:
			index, err := strconv.Atoi(segment)
			if err != nil || index < 0 || index >= len(node) {
				return nil, awsFieldError(field, "array index is not valid")
			}
			current = node[index]
		default:
			return nil, awsFieldError(field, "path continues through a scalar value")
		}
	}
	switch value := current.(type) {
	case string:
		return []byte(value), nil
	case json.Number:
		return []byte(value.String()), nil
	case bool:
		return []byte(strconv.FormatBool(value)), nil
	case nil:
		return nil, awsValueError(msgAWSValueAbsent, "selected field is null", []string{
			msgAWSValueActionOne, msgAWSValueActionTwo,
		})
	default:
		return nil, awsFieldError(field, "selected field is an object or array")
	}
}

// classifyAWSFailure maps the verified AWS CLI conventions to stable
// categories. It uses only the exit status and the error code; raw stderr can
// contain an account identity and is never echoed.
func classifyAWSFailure(res execx.Result) *core.Error {
	text := strings.ToLower(string(res.Stderr))
	detail := fmt.Sprintf("aws cli exit=%d", res.ExitCode)
	switch res.ExitCode {
	case 253:
		// 253 means configuration/credentials prevented the command.
		if strings.Contains(text, "unable to locate credentials") ||
			strings.Contains(text, "could not be found") ||
			strings.Contains(text, "no credentials") {
			return awsCredentialsError(detail)
		}
		if strings.Contains(text, "specify a region") || strings.Contains(text, "no region") {
			return awsRegionError(detail)
		}
		return awsCredentialsError(detail)
	case 254:
		code := awsErrorCode(res.Stderr)
		if code != "" {
			detail += " error=" + code
		}
		switch code {
		case "ResourceNotFoundException":
			return awsNotFoundError(detail)
		case "AccessDeniedException", "UnauthorizedException", "KMSAccessDeniedException", "DecryptionFailure":
			return awsAccessDeniedError(detail)
		default:
			return awsGenericError(detail)
		}
	case 255:
		return awsNetworkError(detail)
	default:
		return awsGenericError(detail)
	}
}

func awsErrorCode(stderr []byte) string {
	match := awsErrorCodeRe.FindSubmatch(stderr)
	if len(match) < 2 {
		return ""
	}
	return string(match[1])
}

func awsCLIError(detail string) *core.Error {
	return awsError(core.CategorySecretAWSCLIMissing, msgAWSCLIMissing, detail, []string{
		msgAWSCLIActionOne, msgAWSCLIActionTwo,
	})
}

func awsCredentialsError(detail string) *core.Error {
	return awsError(core.CategorySecretAWSCredentialsMissing, msgAWSCredentialsMissing, detail, []string{
		msgAWSCredentialsActionOne, msgAWSCredentialsActionTwo,
	})
}

func awsRegionError(detail string) *core.Error {
	return awsError(core.CategorySecretAWSRegionMissing, msgAWSRegionMissing, detail, []string{
		msgAWSRegionActionOne, msgAWSRegionActionTwo,
	})
}

func awsAccessDeniedError(detail string) *core.Error {
	return awsError(core.CategorySecretAWSAccessDenied, msgAWSAccessDenied, detail, []string{
		msgAWSAccessDeniedActionOne, msgAWSAccessDeniedActionTwo,
	})
}

func awsNotFoundError(detail string) *core.Error {
	return awsError(core.CategorySecretAWSSecretNotFound, msgAWSSecretNotFound, detail, []string{
		msgAWSSecretNotFoundActionOne, msgAWSSecretNotFoundActionTwo,
	})
}

func awsNetworkError(detail string) *core.Error {
	return awsError(core.CategorySecretAWSNetwork, msgAWSNetwork, detail, []string{
		msgAWSNetworkActionOne, msgAWSNetworkActionTwo,
	})
}

func awsValueError(message, detail string, actions []string) *core.Error {
	return awsError(core.CategorySecretAWSValueInvalid, message, detail, actions)
}

func awsFieldError(field, detail string) *core.Error {
	return awsError(
		core.CategorySecretAWSFieldInvalid,
		fmt.Sprintf(msgAWSFieldInvalid, field),
		detail,
		[]string{msgAWSFieldActionOne, msgAWSFieldActionTwo},
	)
}

func awsGenericError(detail string) *core.Error {
	return awsError(core.CategorySecretAWSFailed, msgAWSFailed, detail, []string{
		msgAWSFailedActionOne, msgAWSFailedActionTwo,
	})
}

func awsError(category core.Category, message, detail string, actions []string) *core.Error {
	return core.Fail(core.Diagnostic{
		Category:    category,
		Severity:    core.SeverityError,
		Message:     message,
		Detail:      detail,
		NextActions: actions,
	})
}
