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
	_ "embed"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"maps"
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

type entry struct {
	ID         uuid.UUID  `json:"id"`
	StartTime  time.Time  `json:"start_time,format:RFC3339"`
	RawEndTime *time.Time `json:"end_time,format:RFC3339"`
	Task       *task      `json:"task,omitzero"`

	ScheduledEndTime time.Time `json:"-"`
}

func (e *entry) EndTime() time.Time {
	if e.RawEndTime == nil {
		return time.Time{}
	}
	return *e.RawEndTime
}

func (e *entry) isActive() bool {
	return e.EndTime().IsZero()
}

func newEntryCommand(g *globalConfig) *cobra.Command {
	c := &cobra.Command{
		Use:           "entry",
		Aliases:       []string{"e"},
		Short:         "Manage time entries",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	c.AddCommand(
		newEntryDeleteCommand(g),
		newEntryEditCommand(g),
		newEntryImportCommand(g),
		newEntryNewCommand(g),
		newEntrySelectCommand(g),
	)
	return c
}

func newTimesheetCommand(g *globalConfig) *cobra.Command {
	c := &cobra.Command{
		GroupID:       "basic",
		Use:           "timesheet [flags] [START_DATE [END_DATE]]",
		Short:         "Show a daily breakdown",
		Args:          cobra.MaximumNArgs(2),
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	opts := &timesheetOptions{globalConfig: g}
	c.Flags().BoolVarP(&opts.all, "all", "a", false, "show all entries")
	c.Flags().BoolVar(&opts.showTotals, "totals", true, "show total times (plain format only)")
	registerOutputFormatFlagVar(c, &opts.format)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if opts.all {
			if len(args) != 0 {
				return fmt.Errorf("cannot pass dates with --all")
			}
		} else {
			switch len(args) {
			case 0:
				today := dateFromTime(g.runStart.In(g.location))
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

	format     outputFormat
	showTotals bool
}

func runTimesheet(ctx context.Context, opts *timesheetOptions) error {
	type timesheetTotal struct {
		description string
		duration    time.Duration
	}

	db, err := opts.open(ctx)
	if err != nil {
		return err
	}
	defer closeConn(ctx, db)

	minTime := time.Date(opts.startDate.Year(), opts.startDate.Month(), opts.startDate.Day(), 0, 0, 0, 0, opts.location)
	maxTime := time.Date(opts.endDate.Year(), opts.endDate.Month(), opts.endDate.Day()+1, 0, 0, 0, 0, opts.location)

	var w *csv.Writer
	if opts.format == csvOutputFormat {
		w = csv.NewWriter(opts.stdout)
		w.Write(entryCSVHeaderRow())
	}
	totals := make(map[uuid.UUID]timesheetTotal)
	totalsByLabel := make(map[string]time.Duration)
	var lastDateHeader gregorian.Date
	args := map[string]any{
		":now": timeToSQLArg(opts.runStart),
	}
	if opts.all {
		args[":min_time"] = nil
		args[":max_time"] = nil
	} else {
		args[":min_time"] = timeToSQLArg(minTime)
		args[":max_time"] = timeToSQLArg(maxTime)
	}
	var labelsBuf []byte
	err = sqlitex.ExecuteTransientFS(db, sqlFiles(), "entries/list.sql", &sqlitex.ExecOptions{
		Named: args,
		ResultFunc: func(stmt *sqlite.Stmt) error {
			e := new(entry)
			var err error
			e.ID, err = uuid.Parse(stmt.GetText("uuid"))
			if err != nil {
				return fmt.Errorf("uuid: %v", err)
			}
			if err := fillEntryFromDatabase(e, stmt); err != nil {
				return err
			}

			e.Task = new(task)
			e.Task.ID, err = uuid.Parse(stmt.GetText("task.uuid"))
			if err != nil {
				return fmt.Errorf("task.uuid: %v", err)
			}
			e.Task.Description = stmt.GetText("task.description")
			e.Task.Labels, err = labelsFromDatabase(stmt, "task.labels", &labelsBuf)
			if err != nil {
				return err
			}

			switch opts.format {
			case plainOutputFormat:
				startDate := dateFromTime(e.StartTime.In(opts.location))

				var headerFormat string
				switch {
				case lastDateHeader.IsZero():
					headerFormat = "# %v\n\n"
				case !lastDateHeader.Equal(startDate):
					headerFormat = "\n# %v\n\n"
				}
				if headerFormat != "" {
					fmt.Fprintf(opts.stdout, headerFormat, startDate)
					lastDateHeader = startDate
				}

				switch {
				case e.isActive():
					fmt.Fprintf(
						opts.stdout,
						"- %7s – present: %s\n",
						e.StartTime.In(opts.location).Format(time.Kitchen),
						plainTaskDescription(e.Task.Description, false),
					)
				case !startDate.Equal(dateFromTime(e.EndTime().In(opts.location))):
					fmt.Fprintf(
						opts.stdout,
						"- %7s – %s: %s\n",
						e.StartTime.In(opts.location).Format(time.Kitchen),
						e.EndTime().In(opts.location).Format("2006-01-02T15:04"),
						plainTaskDescription(e.Task.Description, false),
					)
				default:
					fmt.Fprintf(
						opts.stdout,
						"- %7s – %7s: %s\n",
						e.StartTime.In(opts.location).Format(time.Kitchen),
						e.EndTime().In(opts.location).Format(time.Kitchen),
						plainTaskDescription(e.Task.Description, false),
					)
				}

				t := totals[e.Task.ID]
				t.description = e.Task.Description
				startTimeForDuration := e.StartTime
				if !opts.all && e.StartTime.Before(minTime) {
					startTimeForDuration = minTime
				}
				endTimeForDuration := e.EndTime()
				if e.EndTime().IsZero() {
					endTimeForDuration = opts.runStart
				} else if !opts.all && e.EndTime().After(maxTime) {
					endTimeForDuration = maxTime
				}
				taskDuration := endTimeForDuration.Sub(startTimeForDuration)
				t.duration += taskDuration
				totals[e.Task.ID] = t
				for _, label := range e.Task.Labels {
					totalsByLabel[label] += taskDuration
				}
			case csvOutputFormat:
				if err := w.Write(entryToCSV(e)); err != nil {
					return err
				}
			case jsonOutputFormat:
				e.StartTime = e.StartTime.UTC()
				if e.RawEndTime != nil {
					*e.RawEndTime = e.RawEndTime.UTC()
				}

				line, err := jsonv2.Marshal(e, jsonv2.WithMarshalers(jsonv2.MarshalToFunc(marshalUUIDTo)))
				if err != nil {
					return fmt.Errorf("entry %v: %v", e.ID, err)
				}
				line = append(line, '\n')
				if _, err := opts.stdout.Write(line); err != nil {
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

	if opts.format == plainOutputFormat && opts.showTotals && len(totals) > 0 {
		totalList := slices.AppendSeq(make([]timesheetTotal, 0, len(totals)), maps.Values(totals))
		slices.SortFunc(totalList, func(a, b timesheetTotal) int {
			return -cmp.Compare(a.duration, b.duration)
		})
		totalByLabelList := make([]timesheetTotal, 0, len(totals))
		for label, duration := range totalsByLabel {
			totalByLabelList = append(totalByLabelList, timesheetTotal{
				description: label,
				duration:    duration,
			})
		}
		slices.SortFunc(totalByLabelList, func(a, b timesheetTotal) int {
			return -cmp.Compare(a.duration, b.duration)
		})

		fmt.Fprint(opts.stdout, "\n# Totals\n\n")
		const (
			taskColumnWidth = 56
			timeColumnWidth = 7
		)
		fmt.Fprintf(opts.stdout, "| %-*s | %-*s |\n", taskColumnWidth, "Task", timeColumnWidth, "Time")
		fmt.Fprintf(
			opts.stdout,
			"| :%s | %s: |\n",
			strings.Repeat("-", taskColumnWidth-1),
			strings.Repeat("-", timeColumnWidth-1),
		)
		for _, t := range totalList {
			fmt.Fprintf(
				opts.stdout,
				"| %-*s | %-*s |\n",
				taskColumnWidth, plainTaskDescription(t.description, false),
				timeColumnWidth, formatDuration(t.duration),
			)
		}

		if len(totalByLabelList) > 0 {
			fmt.Fprint(opts.stdout, "\nBy label:\n\n")
			const labelColumnWidth = 32
			fmt.Fprintf(opts.stdout, "| %-*s | %-*s |\n", labelColumnWidth, "Label", timeColumnWidth, "Time")
			fmt.Fprintf(
				opts.stdout,
				"| :%s | %s: |\n",
				strings.Repeat("-", labelColumnWidth-1),
				strings.Repeat("-", timeColumnWidth-1),
			)
			for _, t := range totalByLabelList {
				fmt.Fprintf(
					opts.stdout,
					"| %-*s | %-*s |\n",
					labelColumnWidth, t.description,
					timeColumnWidth, formatDuration(t.duration),
				)
			}
		}
	}

	return nil
}

func entryCSVHeaderRow() []string {
	return []string{"ID", "Start Time", "End Time", "Task ID", "Description"}
}

func entryToCSV(e *entry) []string {
	var endTimeColumn string
	if et := e.EndTime(); !et.IsZero() {
		endTimeColumn = et.UTC().Format(time.RFC3339)
	}
	return []string{
		e.ID.String(),
		e.StartTime.UTC().Format(time.RFC3339),
		endTimeColumn,
		e.Task.ID.String(),
		e.Task.Description,
	}
}

//go:embed docs/start.txt
var startCommandHelp string

func newStartCommand(g *globalConfig) *cobra.Command {
	c := &cobra.Command{
		GroupID:               "basic",
		Use:                   "start [flags] [DESCRIPTION]",
		Short:                 "Start a new entry",
		Long:                  startCommandHelp,
		Args:                  cobra.ArbitraryArgs,
		DisableFlagsInUseLine: true,
		SilenceErrors:         true,
		SilenceUsage:          true,
	}
	opts := &startOptions{globalConfig: g}
	c.Flags().StringSliceVar(&opts.newTaskOptions.labels, "label", nil, "comma-separated `labels` for new task")
	c.Flags().BoolVarP(&opts.detach, "detach", "d", false, "start task without occupying terminal")
	uuidFlagVarP(c.Flags(), &opts.continueID, "continue", "c", "`ID` of a previous task to continue")
	c.Flags().StringVarP(&opts.startTimeOverride, "start", "s", "", "`time` to use for the entry's start")
	c.Flags().StringVarP(&opts.endTime, "end", "e", "", "scheduled end `time` for task (can be a duration like \"1h5m\")")
	c.Flags().BoolVarP(&opts.pomodoro, "pomodoro", "p", false, "run a timed session")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 && opts.continueID == uuid.Nil && opts.newTaskOptions.isEmpty() {
			opts.continueInteractive = true
		} else {
			opts.newTaskOptions.description = taskDescriptionFromArgs(args)
		}
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
	startTimeOverride   string
	endTime             string
	detach              bool
	continueID          uuid.UUID
	continueInteractive bool
	pomodoro            bool
}

func runStart(ctx context.Context, opts *startOptions) error {
	startedAt := opts.runStart
	entryStartTime := startedAt
	if opts.startTimeOverride != "" {
		var err error
		entryStartTime, err = parseTime(startedAt, opts.startTimeOverride, false)
		if err != nil {
			return fmt.Errorf("start time: %v", err)
		}
	}

	isContinue := opts.continueID != uuid.Nil || opts.continueInteractive
	if isContinue && !opts.newTaskOptions.isEmpty() {
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
		taskIDs, err := selectTask(ctx, db, &selectTaskOptions{
			deltatExecutable: opts.executablePath,
			databasePath:     opts.dbPath,
			fzfOptions: fzfOptions{
				env: &opts.processEnvironment,
			},
		})
		if err != nil {
			return err
		}
		if len(taskIDs) == 0 {
			return fmt.Errorf("fzf: no results")
		}
		taskID = taskIDs[0]

		startedAt = time.Now()
		if opts.startTimeOverride == "" {
			// Don't count the time interactively selecting the task.
			entryStartTime = startedAt
		}
	case opts.continueID != uuid.Nil:
		taskID = opts.continueID
	}

	// Parse end time once entryStartTime settles.
	var scheduledEndTime time.Time
	if opts.endTime != "" {
		if d, err := time.ParseDuration(opts.endTime); err == nil {
			scheduledEndTime = entryStartTime.Add(d)
		} else {
			var err error
			scheduledEndTime, err = parseTime(startedAt, opts.endTime, false)
			if err != nil {
				return fmt.Errorf("parse end time: %v", err)
			}
			if !scheduledEndTime.After(entryStartTime) {
				return fmt.Errorf("end time (%s) is before start (%s)",
					scheduledEndTime.Format(time.RFC3339),
					entryStartTime.Format(time.RFC3339),
				)
			}
		}
	}

	var entryID uuid.UUID
	var breakEndTime time.Time
	err = func() (err error) {
		endFn, err := sqlitex.ImmediateTransaction(db)
		if err != nil {
			return err
		}
		defer endFn(&err)

		if err := endScheduledEntries(db, startedAt); err != nil {
			return err
		}

		if opts.pomodoro {
			cfg, err := readPomodoroConfiguration(db)
			if err != nil {
				return err
			}
			if scheduledEndTime.IsZero() {
				scheduledEndTime = entryStartTime.Add(cfg.duration)
			}
			if cfg.breakDuration > 0 {
				breakEndTime = scheduledEndTime.Add(cfg.breakDuration)
			}
		}

		var activeTask string
		var hasActive bool
		err = sqlitex.ExecuteTransientFS(db, sqlFiles(), "tasks/list_active.sql", &sqlitex.ExecOptions{
			Named: map[string]any{
				":now":   timeToSQLArg(startedAt),
				":limit": 1,
			},
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

		var prevID uuid.UUID
		if taskID == uuid.Nil {
			taskID = newUUIDV7(startedAt, uuid.Nil)
			prevID = taskID
			if err := insertTask(db, startedAt, opts.newTaskOptions.toTask(taskID)); err != nil {
				return err
			}
		} else {
			task, err := fetchTask(db, taskID)
			if err != nil {
				return err
			}
			taskDescription = task.Description
		}

		entryID = newUUIDV7(startedAt, prevID)
		err = insertEntry(db, &entry{
			ID:               entryID,
			StartTime:        entryStartTime,
			ScheduledEndTime: scheduledEndTime,
			Task:             &task{ID: taskID},
		})
		if err != nil {
			return err
		}

		return nil
	}()
	if err != nil {
		return err
	}

	if !opts.quiet {
		outputLine := make([]byte, 0, uuidStringLength+1)
		outputLine = appendUUIDText(outputLine, entryID)
		outputLine = append(outputLine, '\n')
		if _, err := opts.stdout.Write(outputLine); err != nil {
			return err
		}
	}

	initialMessage := new(bytes.Buffer)
	initialMessage.WriteString(plainTaskDescription(taskDescription, true))
	initialMessage.WriteString(" started at ")
	initialMessage.WriteString(startedAt.In(opts.location).Format(time.Kitchen))
	if !scheduledEndTime.IsZero() {
		initialMessage.WriteString("; will end at ")
		initialMessage.WriteString(scheduledEndTime.In(opts.location).Format(time.Kitchen))
	}
	if !breakEndTime.IsZero() {
		initialMessage.WriteString(". Break ends at ")
		initialMessage.WriteString(breakEndTime.In(opts.location).Format(time.Kitchen))
	}
	initialMessage.WriteString(".")
	log.Infof(ctx, "%s", initialMessage.Bytes())
	if opts.detach {
		return nil
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case now := <-ticker.C:
			newStartTime, newEndTime, newScheduledEndTime, err := pollEnd(db, entryID, now, scheduledEndTime)
			if err != nil {
				io.WriteString(opts.stderr, "\n")
				log.Warnf(ctx, "Read entry: %v", err)
			}
			if !newStartTime.IsZero() {
				startedAt = newStartTime
			}
			if !newEndTime.IsZero() {
				// Another process ended or removed the entry.
				io.WriteString(opts.stderr, "\n")
				log.Infof(ctx, "Ended at %s", newEndTime.In(opts.location).Format(time.Kitchen))
				return nil
			}
			scheduledEndTime = newScheduledEndTime

			if scheduledEndTime.IsZero() {
				fmt.Fprintf(opts.stderr, "\r%s elapsed", formatDuration(now.Sub(startedAt)))
			} else {
				// Round everything so that the formatted durations add up to the user-specified duration.
				// Satisfies my constant need to add both numbers and have it be a whole number. 😅
				fmt.Fprintf(opts.stderr,
					"\r%s elapsed (%s remaining)",
					formatDuration(now.Sub(startedAt.Round(time.Second)).Round(time.Second)),
					formatDuration(scheduledEndTime.Round(time.Second).Sub(now).Round(time.Second)),
				)
			}
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
						":now":  timeToSQLArg(now),
					},
				})
			}()
			if err != nil {
				return err
			}

			io.WriteString(opts.stderr, "\n")
			log.Infof(ctx, "Ended at %s", now.Format(time.Kitchen))
			return nil
		}
	}
}

// pollEnd updates the timing information about an entry in a new database transaction.
// If the entry has a scheduled end time,
// then pollEnd will handle writing the end time as appropriate.
// pollEnd generally tries to use read transactions where possible
// to avoid locking out other processes.
func pollEnd(db *sqlite.Conn, entryID uuid.UUID, now, scheduledEndTime time.Time) (startTime, endTime, newScheduledEndTime time.Time, err error) {
	if scheduledEndTime.IsZero() || now.Before(scheduledEndTime) {
		rollback, err := readonlySavepoint(db)
		if err != nil {
			return time.Time{}, time.Time{}, scheduledEndTime, err
		}
		defer rollback()
	} else {
		var endFn func(*error)
		endFn, err = sqlitex.ExclusiveTransaction(db)
		if err != nil {
			return time.Time{}, time.Time{}, scheduledEndTime, err
		}
		defer endFn(&err)

		if err := endScheduledEntries(db, now); err != nil {
			return time.Time{}, time.Time{}, scheduledEndTime, err
		}
	}

	e, err := fetchEntry(db, entryID, now)
	if isEntryNotFound(err) {
		// If no rows found, then assume ended.
		return time.Time{}, now, scheduledEndTime, nil
	}
	if err != nil {
		return time.Time{}, time.Time{}, scheduledEndTime, err
	}
	return e.StartTime, e.EndTime(), e.ScheduledEndTime, nil
}

func newEntryNewCommand(g *globalConfig) *cobra.Command {
	c := &cobra.Command{
		Use:           "new [flags] START END",
		Short:         "Create a new time entry",
		Args:          cobra.ExactArgs(2),
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	opts := &newEntryOptions{globalConfig: g}
	c.Flags().StringVar(&opts.newTaskOptions.description, "description", "", "description of new task")
	c.Flags().StringSliceVar(&opts.newTaskOptions.labels, "label", nil, "comma-separated `labels` for new task")
	uuidFlagVar(c.Flags(), &opts.taskID, "task", "`ID` of a previous task to continue")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		opts.startTime = args[0]
		opts.endTime = args[1]

		var err error
		opts.newTaskOptions.labels, err = cleanLabels(opts.newTaskOptions.labels)
		if err != nil {
			return err
		}

		if opts.taskID != uuid.Nil && !opts.newTaskOptions.isEmpty() {
			return fmt.Errorf("do not pass task options when using --task")
		}

		return runEntryNew(cmd.Context(), opts)
	}
	return c
}

type newEntryOptions struct {
	*globalConfig

	startTime string
	endTime   string

	taskID         uuid.UUID
	newTaskOptions newTaskOptions
}

func runEntryNew(ctx context.Context, opts *newEntryOptions) error {
	startTime, err := parseTime(opts.runStart, opts.startTime, false)
	if err != nil {
		return fmt.Errorf("start time: %v", err)
	}
	endTime, err := parseTime(opts.runStart, opts.endTime, false)
	if err != nil {
		return fmt.Errorf("end time: %v", err)
	}
	if startTime.After(endTime) {
		return fmt.Errorf("start time is after end time")
	}

	db, err := opts.open(ctx)
	if err != nil {
		return err
	}
	defer closeConn(ctx, db)
	endFn, err := sqlitex.ImmediateTransaction(db)
	if err != nil {
		return err
	}
	defer endFn(&err)

	var prevID uuid.UUID
	taskID := opts.taskID
	if taskID == uuid.Nil {
		taskID = newUUIDV7(opts.runStart, uuid.Nil)
		prevID = taskID
		if err := insertTask(db, opts.runStart, opts.newTaskOptions.toTask(taskID)); err != nil {
			return err
		}
	}
	e := &entry{
		ID:         newUUIDV7(opts.runStart, prevID),
		StartTime:  startTime,
		RawEndTime: &endTime,
		Task:       &task{ID: taskID},
	}
	if err := insertEntry(db, e); err != nil {
		return err
	}
	if !opts.quiet {
		outputLine := make([]byte, 0, uuidStringLength+1)
		outputLine = appendUUIDText(outputLine, e.ID)
		outputLine = append(outputLine, '\n')
		if _, err := opts.stdout.Write(outputLine); err != nil {
			return err
		}
	}

	return nil
}

func insertEntry(db *sqlite.Conn, e *entry) error {
	args := map[string]any{
		":uuid":               e.ID.String(),
		":task_uuid":          e.Task.ID.String(),
		":started_at":         timeToSQLArg(e.StartTime),
		":ended_at":           timeToSQLArg(e.EndTime()),
		":scheduled_end_time": timeToSQLArg(e.ScheduledEndTime),
	}
	err := sqlitex.ExecuteTransientFS(db, sqlFiles(), "entries/insert.sql", &sqlitex.ExecOptions{
		Named: args,
	})
	if err != nil {
		return fmt.Errorf("create entry: %w", err)
	}
	return nil
}

func newEntryEditCommand(g *globalConfig) *cobra.Command {
	c := &cobra.Command{
		Use:           "edit [flags] ID",
		Short:         "Change details about an entry",
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	opts := new(editEntryOptions)
	c.Flags().StringVarP(&opts.startTime, "start", "s", "", "start `time` of the entry")
	c.Flags().StringVarP(&opts.endTime, "end", "e", "", "end `time` of the entry")
	uuidFlagVar(c.Flags(), &opts.taskID, "task", "`ID` of the task to associate with the entry")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		var err error
		opts.entryID, err = uuid.Parse(args[0])
		if err != nil {
			return err
		}
		return runEntryEdit(cmd.Context(), g, opts)
	}
	return c
}

type editEntryOptions struct {
	entryID uuid.UUID

	startTime string
	endTime   string
	taskID    uuid.UUID
}

func runEntryEdit(ctx context.Context, g *globalConfig, opts *editEntryOptions) error {
	startTime, err := parseTimeOrEmpty(g.runStart, opts.startTime, false)
	if err != nil {
		return fmt.Errorf("start time: %v", err)
	}
	endTime, err := parseTimeOrEmpty(g.runStart, opts.endTime, false)
	if err != nil {
		return fmt.Errorf("end time: %v", err)
	}

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

	if _, err := fetchEntry(db, opts.entryID, g.runStart); err != nil {
		return err
	}

	if err := updateEntryTimes(db, opts.entryID, startTime, opts.startTime != "", endTime, opts.endTime != ""); err != nil {
		return err
	}

	if opts.taskID != uuid.Nil {
		if err := updateEntryTaskID(db, opts.entryID, opts.taskID); err != nil {
			return err
		}
	}

	return nil
}

func updateEntryTimes(db *sqlite.Conn, entryID uuid.UUID, startTime time.Time, changeStart bool, endTime time.Time, changeEnd bool) (err error) {
	defer sqlitex.Save(db)(&err)

	switch {
	case changeStart && !changeEnd:
		err := sqlitex.ExecuteTransientFS(db, sqlFiles(), "entries/set_start_time.sql", &sqlitex.ExecOptions{
			Named: map[string]any{
				":uuid": entryID.String(),
				":time": timeToSQLArg(startTime),
			},
		})
		if err != nil {
			return fmt.Errorf("set start time: %v", err)
		}
	case !changeStart && changeEnd:
		err := sqlitex.ExecuteTransientFS(db, sqlFiles(), "entries/set_end_time.sql", &sqlitex.ExecOptions{
			Named: map[string]any{
				":uuid": entryID.String(),
				":time": timeToSQLArg(endTime),
			},
		})
		if err != nil {
			return fmt.Errorf("set end time: %v", err)
		}
	case changeStart && changeEnd:
		setEndStmt, err := sqlitex.PrepareTransientFS(db, sqlFiles(), "entries/set_end_time.sql")
		if err != nil {
			return err
		}
		defer setEndStmt.Finalize()

		// Clear the end time first so we don't violate the CHECK constraints.
		setEndStmt.SetText(":uuid", entryID.String())
		setEndStmt.SetNull(":time")
		if _, err := setEndStmt.Step(); err != nil {
			return fmt.Errorf("set end time: %v", err)
		}
		if err := setEndStmt.Reset(); err != nil {
			return fmt.Errorf("set end time: %v", err)
		}

		err = sqlitex.ExecuteTransientFS(db, sqlFiles(), "entries/set_start_time.sql", &sqlitex.ExecOptions{
			Named: map[string]any{
				":uuid": entryID.String(),
				":time": timeToSQLArg(startTime),
			},
		})
		if err != nil {
			return fmt.Errorf("set start time: %v", err)
		}

		if !endTime.IsZero() {
			setEndStmt.SetText(":time", timeToSQLArg(endTime).(string))
			if _, err := setEndStmt.Step(); err != nil {
				return fmt.Errorf("set end time: %v", err)
			}
			if err := setEndStmt.Reset(); err != nil {
				return fmt.Errorf("set end time: %v", err)
			}
		}
	}

	return nil
}

func updateEntryTaskID(db *sqlite.Conn, entryID, taskID uuid.UUID) error {
	err := sqlitex.ExecuteTransientFS(db, sqlFiles(), "entries/set_task.sql", &sqlitex.ExecOptions{
		Named: map[string]any{
			":uuid":      entryID.String(),
			":task_uuid": taskID.String(),
		},
	})
	if err != nil {
		return fmt.Errorf("set task: %v", err)
	}
	return nil
}

//go:embed docs/entry-select.txt
var entrySelectCommandHelp string

func newEntrySelectCommand(g *globalConfig) *cobra.Command {
	c := &cobra.Command{
		Use:           "select",
		Aliases:       []string{"sel"},
		Short:         "Run fzf on the entries",
		Long:          entrySelectCommandHelp,
		Args:          cobra.ArbitraryArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	multi := c.Flags().BoolP("multi", "m", false, "enable multi-select")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		return runEntrySelect(cmd.Context(), g, *multi, taskDescriptionFromArgs(args))
	}
	return c
}

func runEntrySelect(ctx context.Context, g *globalConfig, multi bool, query string) error {
	db, err := g.open(ctx)
	if err != nil {
		return err
	}
	defer closeConn(ctx, db)

	ids, err := selectEntry(ctx, db, g.runStart.In(g.location), &fzfOptions{
		env:          &g.processEnvironment,
		multi:        multi,
		initialQuery: query,
		select1:      query != "",
	})
	if err != nil {
		return err
	}
	for _, id := range ids {
		fmt.Fprintln(g.stdout, id)
	}

	return nil
}

func selectEntry(ctx context.Context, db *sqlite.Conn, now time.Time, opts *fzfOptions) (uuid.UUIDs, error) {
	location := now.Location()

	opts = opts.clone()
	opts.template = "2.."
	opts.outputTemplate = "1"
	opts.delimiter = "\n"
	opts.border = "rounded"
	opts.borderLabel = "Entries"

	var queryError error
	output, err := fzf(ctx, func(yield func(string) bool) {
		var labelsBuf []byte
		queryError = sqlitex.ExecuteTransientFS(db, sqlFiles(), "entries/list_recent.sql", &sqlitex.ExecOptions{
			Named: map[string]any{
				":now":   timeToSQLArg(now),
				":limit": -1,
			},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				e := new(entry)
				var err error
				e.ID, err = uuid.Parse(stmt.GetText("uuid"))
				if err != nil {
					return fmt.Errorf("uuid: %v", err)
				}
				if err := fillEntryFromDatabase(e, stmt); err != nil {
					return err
				}

				e.Task = new(task)
				e.Task.ID, err = uuid.Parse(stmt.GetText("task.uuid"))
				if err != nil {
					return fmt.Errorf("task.uuid: %v", err)
				}
				e.Task.Description = stmt.GetText("task.description")
				e.Task.Labels, err = labelsFromDatabase(stmt, "task.labels", &labelsBuf)
				if err != nil {
					return err
				}

				var s string
				startDate := dateFromTime(e.StartTime.In(location))
				switch {
				case e.isActive():
					s = fmt.Sprintf(
						"%v\n%s\n%s – present\n",
						e.ID,
						plainTaskDescription(e.Task.Description, false),
						e.StartTime.In(location).Format("2006-01-02T15:04"),
					)
				case !startDate.Equal(dateFromTime(e.EndTime().In(location))):
					s = fmt.Sprintf(
						"%v\n%s\n%s – %s",
						e.ID,
						plainTaskDescription(e.Task.Description, false),
						e.StartTime.In(location).Format("2006-01-02T15:04"),
						e.EndTime().In(location).Format("2006-01-02T15:04"),
					)
				default:
					s = fmt.Sprintf(
						"%v\n%s\n%v %7s – %7s",
						e.ID,
						plainTaskDescription(e.Task.Description, false),
						startDate,
						e.StartTime.In(location).Format(time.Kitchen),
						e.EndTime().In(location).Format(time.Kitchen),
					)
				}
				if !yield(s) {
					return fmt.Errorf("iteration stopped")
				}

				return nil
			},
		})
	}, opts)
	if err != nil {
		return nil, err
	}
	if queryError != nil {
		return nil, err
	}
	return parseUUIDs(output)
}

func newEntryDeleteCommand(g *globalConfig) *cobra.Command {
	c := &cobra.Command{
		Use:           "delete [flags] ID [...]",
		Short:         "Delete one or more entries",
		Args:          cobra.MinimumNArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		ids, err := parseUUIDs(args)
		if err != nil {
			return err
		}
		return runEntryDelete(cmd.Context(), g, ids)
	}
	return c
}

func runEntryDelete(ctx context.Context, g *globalConfig, ids []uuid.UUID) error {
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

	stmt, err := sqlitex.PrepareTransientFS(db, sqlFiles(), "entries/delete.sql")
	if err != nil {
		return err
	}
	defer stmt.Finalize()

	for _, id := range ids {
		stmt.SetText(":uuid", id.String())
		if _, err := stmt.Step(); err != nil {
			return fmt.Errorf("delete entry %v: %v", id, err)
		}
		if err := stmt.Reset(); err != nil {
			return fmt.Errorf("delete entry %v: %v", id, err)
		}
	}

	return nil
}

func newStopCommand(g *globalConfig) *cobra.Command {
	c := &cobra.Command{
		GroupID:       "basic",
		Use:           "stop",
		Short:         "Stop the currently tracked task",
		Args:          noArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		return runStop(cmd.Context(), g)
	}
	return c
}

func runStop(ctx context.Context, g *globalConfig) (err error) {
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

	if err := endScheduledEntries(db, g.runStart); err != nil {
		return err
	}

	var tasksToStop []string
	err = sqlitex.ExecuteTransientFS(db, sqlFiles(), "tasks/list_active.sql", &sqlitex.ExecOptions{
		Named: map[string]any{
			":now":   timeToSQLArg(g.runStart),
			":limit": nil,
		},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			tasksToStop = append(tasksToStop, plainTaskDescription(stmt.GetText("description"), true))
			return nil
		},
	})
	if err != nil {
		return err
	}
	if len(tasksToStop) == 0 {
		if !g.quiet {
			fmt.Fprintln(g.stdout, "No running tasks.")
		}
		return nil
	}

	err = sqlitex.ExecuteTransientFS(db, sqlFiles(), "entries/stop_all.sql", &sqlitex.ExecOptions{
		Named: map[string]any{":now": timeToSQLArg(g.runStart)},
	})
	if err != nil {
		return err
	}
	if !g.quiet {
		fmt.Fprintln(g.stdout, "Stopped", strings.Join(tasksToStop, ", "))
	}

	return nil
}

func fetchEntry(db *sqlite.Conn, entryID uuid.UUID, now time.Time) (*entry, error) {
	var result *entry
	err := sqlitex.ExecuteTransientFS(db, sqlFiles(), "entries/get.sql", &sqlitex.ExecOptions{
		Named: map[string]any{
			":now":  timeToSQLArg(now),
			":uuid": entryID.String(),
		},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			result = &entry{ID: entryID}
			return fillEntryFromDatabase(result, stmt)
		},
	})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, &entryNotFoundError{id: entryID}
	}
	return result, nil
}

func fillEntryFromDatabase(e *entry, stmt *sqlite.Stmt) error {
	var err error
	e.StartTime, err = time.Parse(timestampLayout, stmt.GetText("start_time"))
	if err != nil {
		return fmt.Errorf("start_time: %v", err)
	}
	if i := stmt.ColumnIndex("end_time"); stmt.ColumnType(i) != sqlite.TypeNull {
		t, err := time.Parse(timestampLayout, stmt.ColumnText(i))
		if err != nil {
			return fmt.Errorf("end_time: %v", err)
		}
		e.RawEndTime = new(t)
	}
	if i := stmt.ColumnIndex("scheduled_end_time"); stmt.ColumnType(i) != sqlite.TypeNull {
		var err error
		e.ScheduledEndTime, err = time.Parse(timestampLayout, stmt.ColumnText(i))
		if err != nil {
			return fmt.Errorf("end_time: %v", err)
		}
	}
	return nil
}

type entryNotFoundError struct {
	id uuid.UUID
}

func (e *entryNotFoundError) Error() string {
	return fmt.Sprintf("no entry with ID %v", e.id)
}

func isEntryNotFound(err error) bool {
	_, ok := errors.AsType[*entryNotFoundError](err)
	return ok
}

// endScheduledEntries sets the end time of any entries
// that have a scheduled end time before the given time
// to their scheduled end time.
func endScheduledEntries(db *sqlite.Conn, now time.Time) error {
	err := sqlitex.ExecuteTransientFS(db, sqlFiles(), "entries/end_scheduled.sql", &sqlitex.ExecOptions{
		Named: map[string]any{
			":now": timeToSQLArg(now),
		},
	})
	if err != nil {
		return fmt.Errorf("mark end times for scheduled entries: %v", err)
	}
	return nil
}
