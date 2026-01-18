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
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"time"

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
		newLabelsCommand(g),
		newTasksCommand(g),
		newShellCommand(g),
		newStartCommand(g),
		newStopCommand(g),
		newStatusCommand(g),
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

func newStartCommand(g *globalConfig) *cobra.Command {
	c := &cobra.Command{
		Use:                   "start [flags] [DESCRIPTION]",
		Short:                 "Start a new entry",
		Args:                  cobra.ArbitraryArgs,
		DisableFlagsInUseLine: true,
		SilenceErrors:         true,
		SilenceUsage:          true,
	}
	opts := &startOptions{globalConfig: g}
	c.Flags().StringSliceVar(&opts.newTaskOptions.labels, "label", nil, "comma-separated `labels` for new task")
	c.Flags().BoolVarP(&opts.detach, "detach", "d", false, "start task without occupying terminal")
	c.Flags().BoolVarP(&opts.continueInteractive, "continue", "c", false, "continue a previous task (using fzf to select)")
	c.Flags().StringVar(&opts.continueID, "continue-task", "", "`ID` of a previous task to continue")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		opts.newTaskOptions.description = taskDescriptionFromArgs(args)
		var err error
		opts.newTaskOptions.labels, err = cleanLabels(opts.newTaskOptions.labels)
		if err != nil {
			return err
		}
		return runStart(cmd.Context(), opts)
	}
	return c
}

type startOptions struct {
	*globalConfig
	newTaskOptions      newTaskOptions
	detach              bool
	continueID          string
	continueInteractive bool
}

func runStart(ctx context.Context, opts *startOptions) error {
	startedAt := time.Now().UTC()

	isContinue := opts.continueID != "" || opts.continueInteractive
	hasTaskArguments := opts.newTaskOptions.description != "" ||
		len(opts.newTaskOptions.labels) > 0
	if isContinue && hasTaskArguments {
		return fmt.Errorf("do not pass task options when continuing")
	}

	db, err := opts.open(ctx)
	if err != nil {
		return err
	}
	defer closeConn(ctx, db)

	var taskID uuid.UUID
	switch {
	case opts.continueInteractive:
		var err error
		taskID, err = selectTask(ctx, db)
		if err != nil {
			return err
		}
		// Don't count the time interactively selecting the task.
		startedAt = time.Now().UTC()
	case opts.continueID != "":
		var err error
		taskID, err = uuid.Parse(opts.continueID)
		if err != nil {
			return err
		}
	}

	var entryID uuid.UUID
	err = func() (err error) {
		endFn, err := sqlitex.ImmediateTransaction(db)
		if err != nil {
			return err
		}
		defer endFn(&err)

		var activeTask string
		var hasActive bool
		err = sqlitex.ExecuteTransientFS(db, sqlFiles(), "tasks/list_active.sql", &sqlitex.ExecOptions{
			Named: map[string]any{":limit": 1},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				activeTask = stmt.GetText("description")
				hasActive = true
				return nil
			},
		})
		if err != nil {
			return err
		}
		if hasActive {
			return fmt.Errorf("already tracking %s (use deltat stop)", safeTaskDescription(activeTask))
		}

		if taskID == (uuid.UUID{}) {
			taskID, err = newTask(db, &opts.newTaskOptions)
			if err != nil {
				return err
			}
		}

		err = sqlitex.ExecuteTransientFS(db, sqlFiles(), "entries/insert.sql", &sqlitex.ExecOptions{
			Named: map[string]any{
				":task_uuid":  taskID.String(),
				":started_at": startedAt.Format(time.RFC3339),
			},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				var err error
				entryID, err = uuid.Parse(stmt.GetText("uuid"))
				return err
			},
		})
		if err != nil {
			return err
		}

		return nil
	}()
	if err != nil {
		return err
	}

	fmt.Printf("Started at %s\n", startedAt.Local().Format(time.Kitchen))
	if opts.detach {
		return nil
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case now := <-ticker.C:
			isEnded := true // If no rows found, then assume ended.
			err := sqlitex.ExecuteTransientFS(db, sqlFiles(), "entries/get.sql", &sqlitex.ExecOptions{
				Named: map[string]any{
					":uuid": entryID.String(),
				},
				ResultFunc: func(stmt *sqlite.Stmt) error {
					isEnded = stmt.ColumnType(stmt.ColumnIndex("end_time")) != sqlite.TypeNull
					t, err := time.Parse(timestampLayout, stmt.GetText("start_time"))
					if err != nil {
						return fmt.Errorf("start_time: %v", err)
					}
					startedAt = t
					return nil
				},
			})
			if err != nil {
				log.Warnf(ctx, "Read entry: %v", err)
			}
			if isEnded {
				// Another process ended or removed the entry.
				fmt.Printf("\nEnded at %s\n", now.UTC().Format(time.Kitchen))
				return nil
			}

			fmt.Printf("\r%s elapsed", formatDuration(now.Sub(startedAt)))
		case <-ctx.Done():
			now := time.Now()

			ctx, cancel := xcontext.KeepAlive(ctx, 10*time.Second)
			defer cancel()
			db.SetInterrupt(ctx.Done())

			err := func() (err error) {
				endFn, err := sqlitex.ImmediateTransaction(db)
				if err != nil {
					return err
				}
				defer endFn(&err)

				return sqlitex.ExecuteTransientFS(db, sqlFiles(), "entries/stop.sql", &sqlitex.ExecOptions{
					Named: map[string]any{
						":uuid": entryID.String(),
						":now":  now.UTC().Format(time.RFC3339),
					},
				})
			}()
			if err != nil {
				return err
			}

			fmt.Printf("\nEnded at %s\n", now.Format(time.Kitchen))
			return nil
		}
	}
}

func selectTask(ctx context.Context, db *sqlite.Conn) (uuid.UUID, error) {
	const (
		columnSeparator = byte(0x1f) // unit separator in ASCII
		recordSeparator = byte(0)
	)
	r := strings.NewReplacer(string(columnSeparator), "", string(recordSeparator), "")

	tasksInput := new(bytes.Buffer)
	err := sqlitex.ExecuteTransientFS(db, sqlFiles(), "tasks/list.sql", &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			id := stmt.GetText("uuid")
			description := safeTaskDescription(stmt.GetText("description"))
			tasksInput.WriteString(id)
			tasksInput.WriteByte(columnSeparator)
			tasksInput.WriteString(r.Replace(description))
			tasksInput.WriteByte(recordSeparator)
			return nil
		},
	})
	if err != nil {
		return uuid.UUID{}, err
	}

	c := exec.CommandContext(ctx, "fzf", "--delimiter=\x1f", "--read0", "--with-nth=2..", "--accept-nth=1")
	c.Stdin = tasksInput
	output, err := c.Output()
	if err != nil {
		return uuid.UUID{}, err
	}
	return uuid.ParseBytes(bytes.TrimSuffix(output, []byte("\n")))
}

func newStopCommand(g *globalConfig) *cobra.Command {
	c := &cobra.Command{
		Use:           "stop",
		Short:         "Stop the currently tracked task",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		return runStop(cmd.Context(), g)
	}
	return c
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
			description := safeTaskDescription(stmt.GetText("description"))
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

func runStop(ctx context.Context, g *globalConfig) (err error) {
	now := time.Now().UTC()

	db, err := g.open(ctx)
	if err != nil {
		return err
	}
	defer closeConn(ctx, db)
	endFn, err := sqlitex.ImmediateTransaction(db)
	if err != nil {
		return err
	}
	defer endFn(&err)

	var tasksToStop []string
	err = sqlitex.ExecuteTransientFS(db, sqlFiles(), "tasks/list_active.sql", &sqlitex.ExecOptions{
		Named: map[string]any{":limit": nil},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			tasksToStop = append(tasksToStop, safeTaskDescription(stmt.GetText("description")))
			return nil
		},
	})
	if err != nil {
		return err
	}
	if len(tasksToStop) == 0 {
		fmt.Println("No running tasks.")
		return nil
	}

	err = sqlitex.ExecuteTransientFS(db, sqlFiles(), "entries/stop_all.sql", &sqlitex.ExecOptions{
		Named: map[string]any{":now": now.Format(time.RFC3339)},
	})
	if err != nil {
		return err
	}
	fmt.Println("Stopped", strings.Join(tasksToStop, ", "))

	return nil
}

func formatDuration(d time.Duration) string {
	totalSeconds := int64(d / time.Second)
	seconds := totalSeconds % 60
	minutes := (totalSeconds / 60) % 60
	hours := totalSeconds / (60 * 60)
	return fmt.Sprintf("%d:%02d:%02d", hours, minutes, seconds)
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
			Output: log.New(os.Stderr, "deltat: ", log.StdFlags, nil),
		})
	})
}
