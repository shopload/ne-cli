// Package cmd wires the ne-cli command tree.
package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/signal"
	"time"

	"github.com/spf13/cobra"

	"github.com/shopload/ne-cli/internal/client"
	"github.com/shopload/ne-cli/internal/output"
)

// Exit codes.
const (
	exitOK      = 0
	exitAPI     = 1 // the API answered result=error
	exitUsage   = 2 // bad flags, failed validation, confirmation required
	exitAuth    = 3 // not logged in, or re-login required
	exitNetwork = 4 // transport failure or unexpected HTTP response
)

var opts struct {
	format     string
	profile    string
	dryRun     bool
	yes        bool
	wait       bool
	noValidate bool
	timeout    time.Duration
}

// cliError is rendered on stderr as {"ok":false,"error":{...}}.
type cliError struct {
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
	Message any    `json:"message"`
	Hint    string `json:"hint,omitempty"`
	Retry   string `json:"retryable,omitempty"`
	Details any    `json:"details,omitempty"`
	exit    int
}

func (e *cliError) Error() string { return fmt.Sprint(e.Message) }

func usageErr(format string, a ...any) *cliError {
	return &cliError{Type: "usage", Message: fmt.Sprintf(format, a...), exit: exitUsage}
}

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "ne-cli",
		Short: "Next Engine API command line tool for humans and AI agents",
		Long: `ne-cli calls the Next Engine (ネクストエンジン) API.

Three layers:
  shortcuts     ne-cli receiveorder +list --status 20       (task-level helpers, prefixed with +)
  API commands  ne-cli receiveorder base search --fields …   (one per endpoint, all 131)
  raw API       ne-cli api /api_v1_master_goods/search -p …  (any path)

Discover before calling:
  ne-cli schema                       list every endpoint
  ne-cli schema master.goods.search   parameters, fields and types of one endpoint

Output is JSON on stdout ({"ok":true,…}); errors are JSON on stderr ({"ok":false,"error":{…}})
with a non-zero exit code (1 API error, 2 usage, 3 auth, 4 network).`,
		Version:       client.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if !output.ValidFormat(opts.format) {
				return usageErr("unknown --format %q (use one of %v)", opts.format, output.Formats)
			}
			return nil
		},
	}
	pf := root.PersistentFlags()
	pf.StringVar(&opts.format, "format", "json", "output format: json|pretty|table|ndjson|csv")
	pf.StringVar(&opts.profile, "profile", "", "config profile (default: $NE_PROFILE or \"default\")")
	pf.BoolVar(&opts.dryRun, "dry-run", false, "print the request that would be sent, without sending it")
	pf.BoolVar(&opts.yes, "yes", false, "confirm a high-risk write (shipped, bundle, divide, bulk updates)")
	pf.BoolVar(&opts.wait, "wait", false, "send wait_flag=1: wait instead of failing with 003002 when Next Engine is busy")
	pf.BoolVar(&opts.noValidate, "no-validate", false, "skip client-side parameter and field validation")
	pf.DurationVar(&opts.timeout, "timeout", 5*time.Minute, "HTTP timeout per request")
	root.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		return &cliError{Type: "usage", Message: err.Error(), Hint: "run: " + c.CommandPath() + " --help", exit: exitUsage}
	})

	root.AddGroup(
		&cobra.Group{ID: "core", Title: "Core commands:"},
		&cobra.Group{ID: "service", Title: "API services:"},
	)
	root.AddCommand(newAuthCmd(), newConfigCmd(), newAPICmd(), newSchemaCmd())
	addServiceCommands(root)
	return root
}

// Execute runs the CLI and returns the process exit code.
func Execute() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	root := newRoot()
	root.SetArgs(args)
	root.SetIn(stdin)
	root.SetOut(stdout)
	root.SetErr(stderr)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return exitOK
	}
	ce := asCLIError(err)
	enc := json.NewEncoder(stderr)
	enc.SetEscapeHTML(false)
	if opts.format == "pretty" || opts.format == "table" {
		enc.SetIndent("", "  ")
	}
	_ = enc.Encode(map[string]any{"ok": false, "error": ce})
	return ce.exit
}

func asCLIError(err error) *cliError {
	var ce *cliError
	if errors.As(err, &ce) {
		return ce
	}
	var he *client.HTTPError
	if errors.As(err, &he) {
		return &cliError{Type: "http", Message: he.Error(), exit: exitNetwork}
	}
	var ne net.Error
	var ue *url.Error
	if errors.As(err, &ne) || errors.As(err, &ue) || errors.Is(err, context.DeadlineExceeded) {
		return &cliError{Type: "network", Message: err.Error(), Retry: "yes", exit: exitNetwork}
	}
	if errors.Is(err, context.Canceled) {
		return &cliError{Type: "cancelled", Message: "interrupted", exit: exitUsage}
	}
	// cobra's own errors (unknown command, wrong arg count, …)
	return &cliError{Type: "usage", Message: err.Error(), Hint: "run: ne-cli --help", exit: exitUsage}
}

// emit writes a success envelope to stdout.
func emit(cmd *cobra.Command, v map[string]any, columns []string) error {
	v["ok"] = true
	if v["dry_run"] == true {
		columns = nil // the body is a request description, not rows
	}
	return output.Write(cmd.OutOrStdout(), opts.format, v, columns)
}
