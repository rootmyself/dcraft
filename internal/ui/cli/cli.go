// Package cli assembles Cobra commands from the feature registry. It contains
// no feature-specific branches: every command, flag, approval and presenter
// comes from the registered descriptors.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/ui/view"
)

// HomeFunc opens the interactive TUI for the parsed target request.
type HomeFunc func(ctx context.Context, target core.TargetRequest) error

// Options are the shared CLI dependencies.
type Options struct {
	Engine      *core.Engine
	Registry    *core.Registry
	Interactive bool
	Stdout      io.Writer
	Stderr      io.Writer
	Home        HomeFunc
}

// ExitError carries a stable process exit status without an extra message.
type ExitError struct {
	Code int
}

func (e *ExitError) Error() string { return fmt.Sprintf("exit %d", e.Code) }

type targetFlags struct {
	json        bool
	path        string
	files       []string
	envFiles    []string
	profiles    []string
	projectName string
	environment string
	approvals   []string
}

// New builds the root command and every registered feature command.
func New(opts Options) *cobra.Command {
	if opts.Stdout == nil {
		opts.Stdout = io.Discard
	}
	if opts.Stderr == nil {
		opts.Stderr = io.Discard
	}
	tf := &targetFlags{}

	root := &cobra.Command{
		Use:           "dcraft",
		Short:         msgRootShort,
		Long:          msgRootLong,
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHome(cmd, opts, tf)
		},
	}
	root.SetOut(opts.Stdout)
	root.SetErr(opts.Stderr)
	root.SetHelpTemplate(helpTemplate)
	root.SetUsageTemplate(helpTemplate)
	root.CompletionOptions.DisableDefaultCmd = true
	root.InitDefaultHelpFlag()
	if f := root.Flags().Lookup("help"); f != nil {
		f.Usage = msgFlagHelp
	}

	pf := root.PersistentFlags()
	pf.BoolVar(&tf.json, "json", false, msgFlagJSON)
	pf.StringVar(&tf.path, "path", "", msgFlagPath)
	pf.StringArrayVarP(&tf.files, "file", "f", nil, msgFlagFile)
	pf.StringArrayVar(&tf.envFiles, "env-file", nil, msgFlagEnvFile)
	pf.StringArrayVar(&tf.profiles, "profile", nil, msgFlagProfile)
	pf.StringVarP(&tf.projectName, "project-name", "p", "", msgFlagProjectName)
	pf.StringVar(&tf.environment, "env", "", msgFlagEnvironment)
	pf.StringArrayVar(&tf.approvals, "approve", nil, msgFlagApprove)

	if opts.Registry != nil {
		for _, feature := range opts.Registry.All() {
			root.AddCommand(newFeatureCommand(opts, tf, feature))
		}
	}

	help := &cobra.Command{
		Use:   "help [명령]",
		Short: msgHelpShort,
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			target := root
			if len(args) > 0 {
				found, _, err := root.Find(args)
				if err != nil || found == nil {
					fmt.Fprintf(opts.Stderr, "오류: 알 수 없는 명령입니다: %s\n", strings.Join(args, " "))
					return &ExitError{Code: 2}
				}
				target = found
			}
			return target.Help()
		},
	}
	root.SetHelpCommand(help)

	return root
}

func newFeatureCommand(opts Options, tf *targetFlags, feature core.Feature) *cobra.Command {
	d := feature.Descriptor()
	use := d.Name
	argsFn := cobra.NoArgs
	if d.AcceptsArgs {
		use += " [args...]"
		argsFn = cobra.ArbitraryArgs
	}
	cmd := &cobra.Command{
		Use:           use,
		Short:         d.Summary,
		Long:          d.Usage,
		Args:          argsFn,
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	cmd.SetOut(opts.Stdout)
	cmd.SetErr(opts.Stderr)
	cmd.SetHelpTemplate(helpTemplate)
	cmd.SetUsageTemplate(helpTemplate)
	if d.AcceptsArgs {
		// Features that take positional arguments (for example exec) must not
		// treat the user's command as dcraft flags. Options come before args.
		cmd.Flags().SetInterspersed(false)
	}
	cmd.InitDefaultHelpFlag()
	if f := cmd.Flags().Lookup("help"); f != nil {
		f.Usage = msgFlagHelp
	}

	for _, fl := range d.Flags {
		switch fl.Kind {
		case core.FlagBool:
			def, _ := strconv.ParseBool(fl.Default)
			cmd.Flags().BoolP(fl.Name, fl.Shorthand, def, fl.Usage)
		default:
			cmd.Flags().StringP(fl.Name, fl.Shorthand, fl.Default, fl.Usage)
		}
	}
	if d.SupportsServices {
		cmd.Flags().StringArrayP("service", "s", nil, msgFlagService)
	}

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		approvals, err := parseApprovals(tf.approvals)
		if err != nil {
			fmt.Fprintf(opts.Stderr, "오류: %v\n도움말: dcraft --help\n", err)
			return &ExitError{Code: 2}
		}
		options := map[string]string{}
		for _, fl := range d.Flags {
			switch fl.Kind {
			case core.FlagBool:
				v, _ := cmd.Flags().GetBool(fl.Name)
				options[fl.Name] = strconv.FormatBool(v)
			default:
				v, _ := cmd.Flags().GetString(fl.Name)
				options[fl.Name] = v
			}
		}
		var services []string
		if d.SupportsServices {
			services, _ = cmd.Flags().GetStringArray("service")
		}
		req := core.Request{
			Feature:     d.Name,
			Target:      tf.targetRequest(),
			Services:    services,
			Args:        append([]string{}, args...),
			Options:     options,
			Approvals:   approvals,
			Interactive: opts.Interactive && !tf.json,
		}
		if opts.Engine == nil {
			fmt.Fprint(opts.Stderr, "오류: 작업 엔진이 준비되지 않았습니다.\n")
			return &ExitError{Code: 1}
		}
		res := opts.Engine.Run(cmd.Context(), req, nil)
		if err := renderResult(opts.Stdout, opts.Stderr, feature, res, tf.json); err != nil {
			return &ExitError{Code: 1}
		}
		if code := res.ExitCode(); code != 0 {
			return &ExitError{Code: code}
		}
		return nil
	}
	return cmd
}

func runHome(cmd *cobra.Command, opts Options, tf *targetFlags) error {
	if tf.json {
		fmt.Fprint(opts.Stderr, msgJSONNeedsCommand)
		return &ExitError{Code: 2}
	}
	if !opts.Interactive {
		printNonInteractiveUsage(opts.Stderr, opts.Registry)
		return &ExitError{Code: 2}
	}
	if opts.Home == nil {
		printNonInteractiveUsage(opts.Stderr, opts.Registry)
		return &ExitError{Code: 2}
	}
	if err := opts.Home(cmd.Context(), tf.targetRequest()); err != nil {
		var ee *ExitError
		if errors.As(err, &ee) {
			return ee
		}
		fmt.Fprintf(opts.Stderr, "오류: %v\n", err)
		return &ExitError{Code: 1}
	}
	return nil
}

func printNonInteractiveUsage(w io.Writer, registry *core.Registry) {
	fmt.Fprint(w, msgNonInteractive)
	if registry == nil {
		fmt.Fprint(w, msgHelpHint)
		return
	}
	features := registry.All()
	if len(features) > 0 {
		width := 0
		for _, f := range features {
			if n := len(f.Descriptor().Name); n > width {
				width = n
			}
		}
		for _, f := range features {
			d := f.Descriptor()
			fmt.Fprintf(w, "  dcraft %-*s  %s\n", width, d.Name, d.Summary)
		}
	}
	fmt.Fprint(w, msgNonInteractiveJSON)
	fmt.Fprint(w, msgHelpHint)
}

func (tf *targetFlags) targetRequest() core.TargetRequest {
	return core.TargetRequest{
		Path:        tf.path,
		Files:       append([]string{}, tf.files...),
		EnvFiles:    append([]string{}, tf.envFiles...),
		Profiles:    append([]string{}, tf.profiles...),
		ProjectName: tf.projectName,
		Environment: tf.environment,
	}
}

func parseApprovals(values []string) ([]core.ApprovalKind, error) {
	out := make([]core.ApprovalKind, 0, len(values))
	for _, raw := range values {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		var kind core.ApprovalKind
		switch v {
		case "install":
			kind = core.ApprovalInstall
		case "script_trust":
			kind = core.ApprovalScriptTrust
		case "previous_version":
			kind = core.ApprovalPreviousGood
		case "data_restore":
			kind = core.ApprovalDataRestore
		case "file_overwrite":
			kind = core.ApprovalFileOverwrite
		default:
			return nil, fmt.Errorf("알 수 없는 승인 종류입니다: %s (install, script_trust, previous_version, data_restore, file_overwrite)", v)
		}
		out = append(out, kind)
	}
	return out, nil
}

func renderResult(stdout, stderr io.Writer, feature core.Feature, res core.Result, jsonMode bool) error {
	if jsonMode {
		// Marshal before writing so a feature payload that cannot be encoded
		// never leaves partial JSON on stdout.
		data, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "오류: 결과를 JSON으로 출력하지 못했습니다: %v\n", err)
			return err
		}
		data = append(data, '\n')
		if _, err := stdout.Write(data); err != nil {
			fmt.Fprintf(stderr, "오류: 결과를 출력하지 못했습니다: %v\n", err)
			return err
		}
		return nil
	}
	if res.Outcome == core.OutcomeSuccess {
		if presenter, ok := feature.(core.Presenter); ok {
			fmt.Fprintln(stdout, presenter.Present(res, 0))
		} else {
			fmt.Fprintln(stdout, view.SuccessFallback(res))
		}
		if warnings := view.Warnings(res.Diagnostics); len(warnings) > 0 {
			fmt.Fprintln(stderr)
			fmt.Fprintln(stderr, view.Diagnostics(warnings))
		}
		return nil
	}
	fmt.Fprintln(stderr, view.Failure(res))
	return nil
}
