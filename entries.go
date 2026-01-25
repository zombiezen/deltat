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
	"cmp"
	"context"
	"encoding/csv"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	jsonv2 "github.com/go-json-experiment/json"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"zombiezen.com/go/gregorian"
	"zombiezen.com/go/log"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
	"zombiezen.com/go/xcontext"
)

func newTimesheetCommand(g *globalConfig) *cobra.Command {
	c := &cobra.Command{
		Use:           "timesheet [flags] [START_DATE [END_DATE]]",
		Short:         "Show a daily breakdown",
		Args:          cobra.MaximumNArgs(2),
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	opts := &timesheetOptions{globalConfig: g}
	c.Flags().BoolVarP(&opts.all, "all", "a", false, "show all entries")
	c.Flags().BoolVar(&opts.showTotals, "totals", true, "show total times (plain format only)")
	c.Flags().StringVar(&opts.format, "format", "plain", "output `format` (plain, csv, or json)")
	c.RegisterFlagCompletionFunc("format", cobra.FixedCompletions(
		[]cobra.Completion{
			"plain",
			"csv",
			"json",
		},
		cobra.ShellCompDirectiveDefault,
	))
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if opts.format != "plain" && opts.format != "csv" && opts.format != "json" {
			return fmt.Errorf("invalid format %q", opts.format)
		}

		if opts.all {
			if len(args) != 0 {
				return fmt.Errorf("cannot pass dates with --all")
			}
		} else {
			switch len(args) {
			case 0:
				now := time.Now()
				today := gregorian.NewDate(now.Year(), now.Month(), now.Day())
				opts.startDate, opts.endDate = today, today
			case 1:
				var err error
				opts.startDate, err = gregorian.ParseDate(args[0])
				if err != nil {
					return err
				}
				opts.endDate = opts.startDate
			default:
				var err error
				opts.startDate, err = gregorian.ParseDate(args[0])
				if err != nil {
					return err
				}
				opts.endDate, err = gregorian.ParseDate(args[1])
				if err != nil {
					return err
				}
			}
		}

		return runTimesheet(cmd.Context(), opts)
	}
	return c
}

type timesheetOptions struct {
	*globalConfig

	all       bool
	startDate gregorian.Date
	endDate   gregorian.Date

	format     string
	showTotals bool
}

func runTimesheet(ctx context.Context, opts *timesheetOptions) error {
	type timesheetTotal struct {
		description string
		duration    time.Duration
	}
	now := time.Now().UTC()

	db, err := opts.open(ctx)
	if err != nil {
		return err
	}
	defer closeConn(ctx, db)

	minTime := time.Date(opts.startDate.Year(), opts.startDate.Month(), opts.startDate.Day(), 0, 0, 0, 0, time.Local)
	maxTime := time.Date(opts.endDate.Year(), opts.endDate.Month(), opts.endDate.Day()+1, 0, 0, 0, 0, time.Local)

	var w *csv.Writer
	if opts.format == "csv" {
		w = csv.NewWriter(os.Stdout)
		w.Write([]string{"ID", "Start Time", "End Time", "Task ID", "Description"})
	}
	totals := make(map[uuid.UUID]timesheetTotal)
	var lastDateHeader gregorian.Date
	args := map[string]any{
		":now": now.UTC().Format(time.RFC3339),
	}
	if opts.all {
		args[":min_time"] = nil
		args[":max_time"] = nil
	} else {
		args[":min_time"] = minTime.UTC().Format(time.RFC3339)
		args[":max_time"] = maxTime.UTC().Format(time.RFC3339)
	}
	var labelsBuf []byte
	err = sqlitex.ExecuteTransientFS(db, sqlFiles(), "entries/list.sql", &sqlitex.ExecOptions{
		Named: args,
		ResultFunc: func(stmt *sqlite.Stmt) error {
			entryID, err := uuid.Parse(stmt.GetText("uuid"))
			if err != nil {
				return fmt.Errorf("uuid: %v", err)
			}
			startTime, err := time.Parse(timestampLayout, stmt.GetText("start_time"))
			if err != nil {
				return fmt.Errorf("start_time: %v", err)
			}
			var endTime time.Time
			if s := stmt.GetText("end_time"); s != "" {
				var err error
				endTime, err = time.Parse(timestampLayout, s)
				if err != nil {
					return fmt.Errorf("end_time: %v", err)
				}
			}
			taskID, err := uuid.Parse(stmt.GetText("task.uuid"))
			if err != nil {
				return fmt.Errorf("task.uuid: %v", err)
			}
			taskDescription := stmt.GetText("task.description")
			taskLabels, err := labelsFromDatabase(stmt, "task.labels", &labelsBuf)
			if err != nil {
				return err
			}

			switch opts.format {
			case "plain":
				startDate := localDateFromTime(startTime)

				var headerFormat string
				switch {
				case lastDateHeader.IsZero():
					headerFormat = "# %v\n\n"
				case !lastDateHeader.Equal(startDate):
					headerFormat = "\n# %v\n\n"
				}
				if headerFormat != "" {
					fmt.Printf(headerFormat, startDate)
					lastDateHeader = startDate
				}

				switch {
				case endTime.IsZero():
					fmt.Printf(
						"- %7s – present: %s\n",
						startTime.Local().Format(time.Kitchen),
						plainTaskDescription(taskDescription, false),
					)
				case !startDate.Equal(localDateFromTime(endTime)):
					fmt.Printf(
						"- %7s – %s: %s\n",
						startTime.Local().Format(time.Kitchen),
						endTime.Local().Format("2006-01-02T15:04"),
						plainTaskDescription(taskDescription, false),
					)
				default:
					fmt.Printf(
						"- %7s – %7s: %s\n",
						startTime.Local().Format(time.Kitchen),
						endTime.Local().Format(time.Kitchen),
						plainTaskDescription(taskDescription, false),
					)
				}

				t := totals[taskID]
				t.description = taskDescription
				startTimeForDuration := startTime
				if !opts.all && startTime.Before(minTime) {
					startTimeForDuration = minTime
				}
				endTimeForDuration := endTime
				if endTime.IsZero() {
					endTimeForDuration = now
				} else if !opts.all && endTime.After(maxTime) {
					endTimeForDuration = maxTime
				}
				t.duration += endTimeForDuration.Sub(startTimeForDuration)
				totals[taskID] = t
			case "csv":
				row := []string{
					entryID.String(),
					startTime.UTC().Format(time.RFC3339),
					endTime.UTC().Format(time.RFC3339),
					taskID.String(),
					taskDescription,
				}
				if err := w.Write(row); err != nil {
					return err
				}
			case "json":
				var obj struct {
					ID        uuid.UUID  `json:"id"`
					StartTime time.Time  `json:"start_time,format:RFC3339"`
					EndTime   *time.Time `json:"end_time,format:RFC3339"`
					Task      task       `json:"task"`
				}
				obj.ID = taskID
				obj.StartTime = startTime.UTC()
				if !endTime.IsZero() {
					obj.EndTime = new(time.Time)
					*obj.EndTime = endTime.UTC()
				}
				obj.Task.ID = taskID
				obj.Task.Description = taskDescription
				obj.Task.Labels = taskLabels

				line, err := jsonv2.Marshal(obj, jsonv2.WithMarshalers(jsonv2.MarshalToFunc(marshalUUIDTo)))
				if err != nil {
					return fmt.Errorf("entry %v: %v", entryID, err)
				}
				line = append(line, '\n')
				if _, err := os.Stdout.Write(line); err != nil {
					return err
				}
			default:
				return fmt.Errorf("unhandled format %s", opts.format)
			}
			return nil
		},
	})
	if err != nil {
		return err
	}
	if w != nil {
		w.Flush()
		if err := w.Error(); err != nil {
			return err
		}
	}

	if opts.format == "plain" && opts.showTotals && len(totals) > 0 {
		totalList := slices.AppendSeq(make([]timesheetTotal, 0, len(totals)), maps.Values(totals))
		slices.SortFunc(totalList, func(a, b timesheetTotal) int {
			return -cmp.Compare(a.duration, b.duration)
		})
		fmt.Print("\n# Totals\n\n")
		const (
			taskColumnWidth = 56
			timeColumnWidth = 7
		)
		fmt.Printf("| %-*s | %-*s |\n", taskColumnWidth, "Task", timeColumnWidth, "Time")
		fmt.Printf(
			"| :%s | %s: |\n",
			strings.Repeat("-", taskColumnWidth-1),
			strings.Repeat("-", timeColumnWidth-1),
		)
		for _, t := range totalList {
			fmt.Printf(
				"| %-*s | %-*s |\n",
				taskColumnWidth, plainTaskDescription(t.description, false),
				timeColumnWidth, formatDuration(t.duration),
			)
		}
	}

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
	taskDescription := opts.newTaskOptions.description
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
			return fmt.Errorf("already tracking %s (use deltat stop)", plainTaskDescription(activeTask, true))
		}

		if taskID == (uuid.UUID{}) {
			taskID, err = newTask(db, &opts.newTaskOptions)
			if err != nil {
				return err
			}
		} else {
			task, err := fetchTask(db, taskID)
			if err != nil {
				return err
			}
			taskDescription = task.Description
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

	fmt.Printf("“%s” started at %s\n", taskDescription, startedAt.Local().Format(time.Kitchen))
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
			description := plainTaskDescription(stmt.GetText("description"), false)
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

	c := exec.CommandContext(ctx, "fzf", "--delimiter="+string(columnSeparator), "--read0", "--with-nth=2..", "--accept-nth=1")
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
			tasksToStop = append(tasksToStop, plainTaskDescription(stmt.GetText("description"), true))
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
