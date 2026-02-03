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
	"bytes"
	"context"
	"errors"
	"fmt"
	"iter"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-json-experiment/json/jsontext"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"zombiezen.com/go/log"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/shell"
	"zombiezen.com/go/sqlite/sqlitemigration"
	"zombiezen.com/go/sqlite/sqlitex"
	"zombiezen.com/go/xcontext"
)

type globalConfig struct {
	dbPath string
}

func (g *globalConfig) open(ctx context.Context) (*sqlite.Conn, error) {
	if g.dbPath == "" {
		return nil, fmt.Errorf("DELTAT_DB not set")
	}
	conn, err := sqlite.OpenConn(g.dbPath, sqlite.OpenReadWrite, sqlite.OpenCreate)
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

func main() {
	rootCommand := &cobra.Command{
		Use:           "deltat",
		Short:         "time tracker",
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	g := new(globalConfig)
	showDebug := rootCommand.PersistentFlags().Bool("debug", false, "show debugging output")
	rootCommand.PersistentFlags().StringVar(&g.dbPath, "db", os.Getenv("DELTAT_DB"), "`path` to database")
	rootCommand.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		initLogging(*showDebug)
		return nil
	}

	rootCommand.AddCommand(
		newEntryCommand(g),
		newLabelCommand(g),
		newShellCommand(g),
		newStartCommand(g),
		newStatusCommand(g),
		newStopCommand(g),
		newTaskCommand(g),
		newTimesheetCommand(g),
	)

	ctx, cancel := signal.NotifyContext(context.Background(), sigterm...)
	err := rootCommand.ExecuteContext(ctx)
	cancel()
	if err != nil {
		initLogging(*showDebug)
		log.Errorf(context.Background(), "%v", err)
		os.Exit(1)
	}
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
		Args:          cobra.NoArgs,
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
		Use:           "status",
		Short:         "Show currently running task",
		Args:          cobra.NoArgs,
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

	now := time.Now().UTC()
	hasAny := false
	err = sqlitex.ExecuteTransientFS(db, sqlFiles(), "tasks/list_active.sql", &sqlitex.ExecOptions{
		Named: map[string]any{":limit": nil},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			hasAny = true
			description := plainTaskDescription(stmt.GetText("description"), true)
			startTime, err := time.Parse(timestampLayout, stmt.GetText("start_time"))
			if err != nil {
				return fmt.Errorf("start_time: %v", err)
			}
			fmt.Printf("%s running since %s (%s elapsed)\n", description, startTime.Local().Format(time.Stamp), formatDuration(now.Sub(startTime)))
			return nil
		},
	})
	if err != nil {
		return err
	}

	if !hasAny {
		fmt.Println("Nothing running.")
	}
	return nil
}

type fzfOptions struct {
	template     string
	multi        bool
	initialQuery string
	select1      bool
}

func (opts *fzfOptions) clone() *fzfOptions {
	opts2 := new(fzfOptions)
	if opts != nil {
		*opts2 = *opts
	}
	return opts2
}

func (opts *fzfOptions) templateFlagValue() string {
	if opts == nil || opts.template == "" {
		return "{2}"
	}
	return opts.template
}

func fzf(ctx context.Context, items iter.Seq2[string, string], opts *fzfOptions) ([]string, error) {
	const (
		columnSeparator = byte(0x1f) // unit separator in ASCII
		recordSeparator = byte(0)
	)

	fzfPath := os.Getenv("DELTAT_FZF")
	if fzfPath == "" {
		fzfPath = "fzf"
	}
	c := exec.CommandContext(ctx,
		fzfPath,
		"--delimiter="+string(columnSeparator),
		"--read0",
		"--accept-nth=1",
		"--with-nth="+opts.templateFlagValue(),
		"--no-sort",
	)
	if opts != nil && opts.multi {
		c.Args = append(c.Args, "--multi")
	} else {
		c.Args = append(c.Args, "--no-multi")
	}
	if opts != nil && opts.select1 {
		c.Args = append(c.Args, "--select-1")
	} else {
		c.Args = append(c.Args, "--no-select-1")
	}
	if opts != nil && opts.initialQuery != "" {
		c.Args = append(c.Args, "--query="+opts.initialQuery)
	}

	stdin, err := c.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("fzf: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer func() {
			stdin.Close()
			close(done)
		}()

		var buf []byte
		r := strings.NewReplacer(string(columnSeparator), "", string(recordSeparator), "")
		for id, data := range items {
			if strings.ContainsAny(id, string(columnSeparator)+string(recordSeparator)) {
				log.Warnf(ctx, "fzf: invalid id %q", id)
				continue
			}
			buf = buf[:0]
			buf = append(buf, id...)
			buf = append(buf, columnSeparator)
			buf = append(buf, r.Replace(data)...)
			buf = append(buf, recordSeparator)
			if _, err := stdin.Write(buf); err != nil {
				return
			}
		}
	}()

	output, err := c.Output()
	if err != nil {
		exitCode := -1
		hasStderr := false
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			hasStderr = len(exitError.Stderr) > 0
			exitCode = exitError.ExitCode()
		}
		switch {
		case exitCode == 1 && !hasStderr:
			// Exit code 1 is a common enough exit code that wrappers
			// or other things may return.
			// Only use this error message if there was no additional stderr output.
			err = errFZFNoMatch
		case exitCode == 130:
			err = errFZFCanceled
		default:
			err = fmt.Errorf("fzf: %v", err)
		}
	}
	<-done

	if len(output) == 0 {
		return nil, err
	}
	output = bytes.TrimSuffix(output, []byte("\n"))
	return strings.Split(string(output), "\n"), err
}

var (
	errFZFNoMatch  = errors.New("fzf: no match")
	errFZFCanceled = errors.New("fzf: user canceled")
)

func marshalUUIDTo(enc *jsontext.Encoder, u uuid.UUID) error {
	return enc.WriteToken(jsontext.String(u.String()))
}

func parseUUIDs(slice []string) (uuid.UUIDs, error) {
	result := make(uuid.UUIDs, len(slice))
	var resultError error
	for i, s := range slice {
		var err error
		result[i], err = uuid.Parse(s)
		resultError = errors.Join(resultError, err)
	}
	return result, resultError
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

var initLogOnce sync.Once

func initLogging(showDebug bool) {
	initLogOnce.Do(func() {
		minLogLevel := log.Info
		if showDebug {
			minLogLevel = log.Debug
		}
		log.SetDefault(&log.LevelFilter{
			Min:    minLogLevel,
			Output: log.New(os.Stderr, "deltat: ", 0, nil),
		})
	})
}
