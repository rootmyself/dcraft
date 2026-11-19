package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"

	"github.com/rootmyself/dcraft/internal/app"
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/ui/cli"
	"github.com/rootmyself/dcraft/internal/ui/tui"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) int {
	if versionRequested(args) {
		printVersion(stdout)
		return 0
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	interactive := isInteractive(stdin, stdout)
	application, err := app.New(app.Options{
		Getenv: getenv,
	})
	if err != nil {
		fmt.Fprintf(stderr, "오류: dcraft를 초기화하지 못했습니다: %v\n", err)
		return 1
	}

	root := cli.New(cli.Options{
		Engine:      application.Engine,
		Registry:    application.Registry,
		Interactive: interactive,
		Stdout:      stdout,
		Stderr:      stderr,
		Home: func(ctx context.Context, target core.TargetRequest) error {
			return tui.Run(ctx, tui.Options{
				Engine:   application.Engine,
				Registry: application.Registry,
				Target:   target,
			})
		},
	})
	root.SetArgs(args)
	if err := root.ExecuteContext(ctx); err != nil {
		var exitErr *cli.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.Code
		}
		fmt.Fprintf(stderr, "오류: %v\n도움말: dcraft --help\n", err)
		return 2
	}
	return 0
}

func isInteractive(stdin io.Reader, stdout io.Writer) bool {
	in, ok := stdin.(*os.File)
	if !ok {
		return false
	}
	out, ok := stdout.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(in.Fd())) && term.IsTerminal(int(out.Fd()))
}
