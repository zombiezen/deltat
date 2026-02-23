// Copyright 2026 Roxy Light
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"iter"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"
	"zombiezen.com/go/log"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/shell"
	"zombiezen.com/go/sqlite/sqlitemigration"
	"zombiezen.com/go/sqlite/sqlitex"
	"zombiezen.com/go/xcontext"
)

// processEnvironment holds the dependencies from the OS environment used in this program.
type processEnvironment struct {
	// executablePath is the absolute path to the running program.
	executablePath string
	// workDirectory can override the current work directory if non-empty.
	workDirectory string
	// environ is a list of environment variables in the form "KEY=VALUE".
	environ []string
	// location is the local timezone. Must not be nil.
	location *time.Location
	runStart time.Time

	stdin           io.Reader
	isStdinTerminal bool
	stdout          io.Writer
	stderr          io.Writer

	initLogging func(showDebug bool)
}

// path returns the operating system path
// for the path relative to env.workDirectory.
func (env *processEnvironment) path(name string) string {
	if env == nil || env.workDirectory == "" || filepath.IsAbs(name) {
		return name
	}
	return filepath.Join(env.workDirectory, name)
}

func (env *processEnvironment) lookupEnv(key string) (string, bool) {
	if env == nil || strings.Contains(key, "=") {
		return "", false
	}
	for _, kv := range slices.Backward(env.environ) {
		if len(kv) >= len(key)+1 && kv[:len(key)] == key && kv[len(key)] == '=' {
			return kv[len(key)+1:], true
		}
	}
	return "", false
}

func (env *processEnvironment) getenv(key string) string {
	value, _ := env.lookupEnv(key)
	return value
}

type globalConfig struct {
	processEnvironment

	dbPath string
	quiet  bool
}

func (g *globalConfig) open(ctx context.Context) (*sqlite.Conn, error) {
	if g.dbPath == "" {
		return nil, fmt.Errorf("must set DELTAT_DB or pass --db flag")
	}
	conn, err := sqlite.OpenConn(g.path(g.dbPath), sqlite.OpenReadWrite, sqlite.OpenCreate)
	if err != nil {
		return nil, err
	}
	conn.SetInterrupt(ctx.Done())
	if err := prepareConn(conn); err != nil {
		conn.Close()
		return nil, err
	}
	if err := sqlitemigration.Migrate(ctx, conn, schema()); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

//go:embed docs/deltat.txt
var rootCommandHelp string

var versionString string

func main() {
	env := &processEnvironment{
		runStart: time.Now(),
		environ:  os.Environ(),
		location: time.Local,

		stdin:           os.Stdin,
		isStdinTerminal: term.IsTerminal(int(os.Stdin.Fd())),
		stdout:          os.Stdout,
		stderr:          os.Stderr,

		initLogging: initLogging,
	}

	var err error
	env.executablePath, err = os.Executable()
	if err != nil {
		initLogging(false)
		log.Errorf(context.Background(), "%v", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), sigterm...)
	err = run(ctx, env, os.Args[1:])
	cancel()
	if err != nil {
		initLogging(false)
		log.Errorf(context.Background(), "%v", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, env *processEnvironment, args []string) error {
	rootCommand := &cobra.Command{
		Use:           "deltat",
		Short:         "time tracker",
		Long:          rootCommandHelp,
		SilenceErrors: true,
		SilenceUsage:  true,
		Version:       versionString,
	}

	g := &globalConfig{processEnvironment: *env}

	showDebug := rootCommand.PersistentFlags().Bool("debug", false, "show debugging output")
	rootCommand.PersistentFlags().BoolVar(&g.quiet, "quiet", false, "display less output")
	rootCommand.PersistentFlags().StringVar(&g.dbPath, "db", env.getenv("DELTAT_DB"), "`path` to database")
	rootCommand.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if g.initLogging != nil {
			g.initLogging(*showDebug)
		}
		return nil
	}

	rootCommand.AddGroup(&cobra.Group{
		ID:    "basic",
		Title: "Everyday Commands:",
	})
	rootCommand.AddCommand(
		newEntryCommand(g),
		newGenerateUUIDCommand(g),
		newLabelCommand(g),
		newPomodoroSettingsCommand(g),
		newShellCommand(g),
		newStartCommand(g),
		newStatusCommand(g),
		newStopCommand(g),
		newTaskCommand(g),
		newTimesheetCommand(g),
	)

	rootCommand.SetArgs(args)
	return rootCommand.ExecuteContext(ctx)
}

func closeConn(ctx context.Context, conn *sqlite.Conn) {
	ctx, cancel := xcontext.KeepAlive(ctx, 10*time.Second)
	defer cancel()
	conn.SetInterrupt(ctx.Done())
	if err := sqlitex.ExecuteTransient(conn, `PRAGMA optimize;`, nil); err != nil {
		log.Warnf(ctx, "Database optimization failed: %v", err)
	}
	if err := conn.Close(); err != nil {
		log.Errorf(ctx, "Closing database connection: %v", err)
	}
}

func newShellCommand(g *globalConfig) *cobra.Command {
	c := &cobra.Command{
		Use:           "shell",
		Short:         "SQLite shell",
		Args:          noArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		Hidden:        true,
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		return runShell(cmd.Context(), g)
	}
	return c
}

func runShell(ctx context.Context, g *globalConfig) error {
	db, err := g.open(ctx)
	if err != nil {
		return err
	}
	defer closeConn(ctx, db)

	shell.Run(db)
	return nil
}

func newStatusCommand(g *globalConfig) *cobra.Command {
	c := &cobra.Command{
		GroupID:       "basic",
		Use:           "status",
		Short:         "Show currently running task",
		Args:          noArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		return runStatus(cmd.Context(), g)
	}
	return c
}

func runStatus(ctx context.Context, g *globalConfig) error {
	db, err := g.open(ctx)
	if err != nil {
		return err
	}
	defer closeConn(ctx, db)

	hasAny := false
	err = sqlitex.ExecuteTransientFS(db, sqlFiles(), "tasks/list_active.sql", &sqlitex.ExecOptions{
		Named: map[string]any{
			":now":   timeToSQLArg(g.runStart),
			":limit": nil,
		},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			hasAny = true
			description := plainTaskDescription(stmt.GetText("description"), true)
			startTime, err := time.Parse(timestampLayout, stmt.GetText("start_time"))
			if err != nil {
				return fmt.Errorf("start_time: %v", err)
			}
			fmt.Fprintf(
				g.stdout,
				"%s running since %s (%s elapsed)\n",
				description,
				startTime.In(g.location).Format(time.Stamp),
				formatDuration(g.runStart.Sub(startTime)),
			)
			return nil
		},
	})
	if err != nil {
		return err
	}

	if !hasAny && !g.quiet {
		fmt.Fprintln(g.stdout, "Nothing running.")
	}
	return nil
}

// outputFormat is an enumeration of output formats for the CLI.
type outputFormat string

// Known output formats.
// Can be listed with [knownOutputFormats].
const (
	plainOutputFormat outputFormat = "plain"
	csvOutputFormat   outputFormat = "csv"
	jsonOutputFormat  outputFormat = "json"
)

// knownOutputFormats is an [iter.Seq] over all the known [outputFormat] values.
func knownOutputFormats(yield func(outputFormat) bool) {
	if !yield(plainOutputFormat) {
		return
	}
	if !yield(csvOutputFormat) {
		return
	}
	if !yield(jsonOutputFormat) {
		return
	}
}

func registerOutputFormatFlagVar(c *cobra.Command, p *outputFormat) {
	options := joinSeq(func(yield func(string) bool) {
		knownOutputFormats(func(f outputFormat) bool {
			return yield(string(f))
		})
	}, ", ", "or")

	*p = plainOutputFormat // set default
	const name = "format"
	c.Flags().Var(p, name, "output `format` ("+options+")")

	completions := slices.Collect(func(yield func(cobra.Completion) bool) {
		knownOutputFormats(func(f outputFormat) bool {
			return yield(cobra.Completion(f))
		})
	})
	c.RegisterFlagCompletionFunc(name, cobra.FixedCompletions(completions, cobra.ShellCompDirectiveDefault))
}

func (f outputFormat) isKnown() bool {
	for known := range knownOutputFormats {
		if f == known {
			return true
		}
	}
	return false
}

func (f outputFormat) String() string {
	return string(f)
}

func (f *outputFormat) Set(s string) error {
	newValue := outputFormat(s)
	if !newValue.isKnown() {
		return fmt.Errorf("unknown format %q", s)
	}
	*f = newValue
	return nil
}

func (f outputFormat) Type() string {
	return "string"
}

func joinSeq(words iter.Seq[string], sep, conjunction string) string {
	sb := new(strings.Builder)
	next, stop := iter.Pull(words)
	defer stop()

	prev, ok := next()
	if !ok {
		return ""
	}

	n := 1
	for {
		next, ok := next()
		if !ok {
			break
		}
		if n > 1 {
			sb.WriteString(sep)
		}
		sb.WriteString(prev)
		prev = next

		if n < 3 {
			n++
		}
	}
	if n > 1 {
		switch {
		case conjunction == "" || n > 2:
			sb.WriteString(sep)
		case conjunction != "" && n == 2:
			sb.WriteByte(' ')
		}
		sb.WriteString(conjunction)
		if conjunction != "" {
			sb.WriteByte(' ')
		}
	}
	sb.WriteString(prev)
	return sb.String()
}

func noArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("`%s` does not accept args, received %d", cmd.CommandPath(), len(args))
	}
	return nil
}

var initLogOnce sync.Once

func initLogging(showDebug bool) {
	initLogOnce.Do(func() {
		log.SetDefault(newLogger(os.Stderr, showDebug))
	})
}

func newLogger(out io.Writer, showDebug bool) log.Logger {
	minLogLevel := log.Info
	if showDebug {
		minLogLevel = log.Debug
	}
	return &log.LevelFilter{
		Min:    minLogLevel,
		Output: log.New(out, "deltat: ", 0, nil),
	}
}
