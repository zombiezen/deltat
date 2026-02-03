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
	"encoding/csv"
	"errors"
	"fmt"
	"iter"
	"os"
	"slices"
	"strings"
	"time"

	jsonv2 "github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

type task struct {
	ID          uuid.UUID `json:"id"`
	Description string    `json:"description"`
	Labels      []string  `json:"labels"`

	EntryCount int      `json:"-"`
	Entries    []*entry `json:"entries,omitzero"`
}

func newTaskCommand(g *globalConfig) *cobra.Command {
	c := &cobra.Command{
		Use:           "task",
		Short:         "Manage types of entries",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	c.AddCommand(
		newTaskDeleteCommand(g),
		newTaskEditCommand(g),
		newTaskListCommand(g),
		newTaskNewCommand(g),
		newTaskSelectCommand(g),
		newTaskShowCommand(g),
	)
	return c
}

func newTaskListCommand(g *globalConfig) *cobra.Command {
	c := &cobra.Command{
		Use:           "list",
		Short:         "List tasks",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	var format outputFormat
	registerOutputFormatFlagVar(c, &format)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		return runTaskList(cmd.Context(), g, format)
	}
	return c
}

func runTaskList(ctx context.Context, g *globalConfig, format outputFormat) error {
	db, err := g.open(ctx)
	if err != nil {
		return err
	}
	defer closeConn(ctx, db)

	var w *csv.Writer
	if format == csvOutputFormat {
		w = csv.NewWriter(os.Stdout)
		w.Write([]string{"ID", "Description", "Labels"})
	}
	var writeError error
	err = listTasks(db, func(t *task) bool {
		switch format {
		case plainOutputFormat:
			_, writeError = fmt.Println(plainTaskDescription(t.Description, false))
		case csvOutputFormat:
			row := []string{
				t.ID.String(),
				t.Description,
				strings.Join(t.Labels, ","),
			}
			writeError = w.Write(row)
		case jsonOutputFormat:
			line, err := jsonv2.Marshal(t, jsonv2.WithMarshalers(jsonv2.MarshalToFunc(marshalUUIDTo)))
			if err != nil {
				writeError = fmt.Errorf("task %v: %v", t.ID, err)
				return false
			}
			line = append(line, '\n')
			_, writeError = os.Stdout.Write(line)
		default:
			writeError = fmt.Errorf("unhandled format %s", format)
		}
		return writeError == nil
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

	return nil
}

func newTaskNewCommand(g *globalConfig) *cobra.Command {
	c := &cobra.Command{
		Use:           "new",
		Short:         "Add a new entry type",
		Args:          cobra.MinimumNArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	opts := new(newTaskOptions)
	c.Flags().StringSliceVar(&opts.labels, "label", nil, "comma-separated task `label`s")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		opts.description = taskDescriptionFromArgs(args)
		var err error
		opts.labels, err = cleanLabels(opts.labels)
		if err != nil {
			return err
		}

		return runTaskNew(cmd.Context(), g, opts)
	}
	return c
}

func runTaskNew(ctx context.Context, g *globalConfig, opts *newTaskOptions) (err error) {
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

	if _, err := newTask(db, opts); err != nil {
		return err
	}

	return nil
}

type newTaskOptions struct {
	description string
	labels      []string
}

func (opts *newTaskOptions) isEmpty() bool {
	return opts.description == "" && len(opts.labels) == 0
}

func newTask(db *sqlite.Conn, opts *newTaskOptions) (id uuid.UUID, err error) {
	id, err = uuid.NewV7()
	if err != nil {
		return uuid.UUID{}, err
	}

	defer sqlitex.Save(db)(&err)
	createdAt := time.Unix(id.Time().UnixTime()).UTC()
	err = sqlitex.ExecuteTransientFS(db, sqlFiles(), "tasks/insert.sql", &sqlitex.ExecOptions{
		Named: map[string]any{
			":uuid":        id.String(),
			":description": opts.description,
			":created_at":  createdAt.Format(time.RFC3339),
		},
	})
	if err != nil {
		return uuid.UUID{}, err
	}
	if len(opts.labels) > 0 {
		if err := addTaskLabels(db, id, slices.Values(opts.labels)); err != nil {
			return uuid.UUID{}, err
		}
	}
	return id, nil
}

func addTaskLabels(db *sqlite.Conn, taskID uuid.UUID, labels iter.Seq[string]) (err error) {
	defer sqlitex.Save(db)(&err)

	if err := upsertLabels(db, labels); err != nil {
		return err
	}

	stmt, err := sqlitex.PrepareTransientFS(db, sqlFiles(), "tasks/add_label.sql")
	if err != nil {
		return err
	}
	defer stmt.Finalize()
	for label := range labels {
		stmt.SetText(":task_uuid", taskID.String())
		stmt.SetText(":label", label)
		if _, err := stmt.Step(); err != nil {
			return fmt.Errorf("add label %q to task %v: %v", label, taskID, err)
		}
		if err := stmt.Reset(); err != nil {
			return fmt.Errorf("add label %q to task %v: %v", label, taskID, err)
		}
	}
	return nil
}

func newTaskShowCommand(g *globalConfig) *cobra.Command {
	c := &cobra.Command{
		Use:           "show",
		Short:         "Show information about a task",
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	var format outputFormat
	registerOutputFormatFlagVar(c, &format)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		return runTaskShow(cmd.Context(), g, args[0], format)
	}
	return c
}

func runTaskShow(ctx context.Context, g *globalConfig, taskIDString string, format outputFormat) (err error) {
	now := time.Now()
	taskID, err := uuid.Parse(taskIDString)
	if err != nil {
		return err
	}

	db, err := g.open(ctx)
	if err != nil {
		return err
	}
	defer closeConn(ctx, db)
	rollback, err := readonlySavepoint(db)
	if err != nil {
		return err
	}
	defer rollback()

	t, err := fetchTask(db, taskID)
	if err != nil {
		return err
	}
	err = sqlitex.ExecuteTransientFS(db, sqlFiles(), "entries/list_by_task.sql", &sqlitex.ExecOptions{
		Named: map[string]any{
			":task_uuid": taskID.String(),
		},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			e := &entry{Task: t}
			var err error
			e.ID, err = uuid.Parse(stmt.GetText("uuid"))
			if err != nil {
				return fmt.Errorf("uuid: %v", err)
			}
			if err := fillEntryFromDatabase(e, stmt); err != nil {
				return err
			}

			t.Entries = append(t.Entries, e)
			return nil
		},
	})
	if err != nil {
		return err
	}

	switch format {
	case plainOutputFormat:
		buf := new(bytes.Buffer)
		fmt.Fprintf(buf, "# %s\n\nID: %s\n\n", t.Description, t.ID)

		if len(t.Labels) > 0 {
			buf.WriteString("Labels:\n\n")
			for _, label := range t.Labels {
				fmt.Fprintf(buf, "- %s\n", label)
			}
			buf.WriteString("\n")
		}

		if len(t.Entries) == 0 {
			buf.WriteString("0 entries")
		} else {
			var total time.Duration
			for _, e := range t.Entries {
				endTime := e.EndTime()
				if endTime.IsZero() {
					endTime = now
				}
				total += max(endTime.Sub(e.StartTime), 0)
			}
			noun := "entries"
			if len(t.Entries) == 1 {
				noun = "entry"
			}
			fmt.Fprintf(buf, "%d %s, totaling %s\n\n", len(t.Entries), noun, formatDuration(total))

			buf.WriteString("| Date       | Start   | End     |\n")
			buf.WriteString("| :--------- | ------: | ------: |\n")
			for _, e := range t.Entries {
				startTime := e.StartTime.Local()
				startDate := localDateFromTime(startTime)
				endTime := e.EndTime().Local()
				var endTimeString string
				switch {
				case endTime.IsZero():
					endTimeString = "present"
				case !localDateFromTime(endTime).Equal(startDate):
					endTimeString = endTime.Format("2006-01-02T15:04")
				default:
					endTimeString = endTime.Format(time.Kitchen)
				}

				fmt.Fprintf(buf, "| %s | %7s | %7s |\n",
					startDate,
					startTime.Format(time.Kitchen),
					endTimeString,
				)
			}

			if _, err := buf.WriteTo(os.Stdout); err != nil {
				return err
			}
		}

	case csvOutputFormat:
		w := csv.NewWriter(os.Stdout)
		w.Write(entryCSVHeaderRow())
		for _, e := range t.Entries {
			e.Task = t
			w.Write(entryToCSV(e))
		}
		w.Flush()
		if err := w.Error(); err != nil {
			return err
		}
	case jsonOutputFormat:
		if len(t.Entries) == 0 {
			// Make sure we always serialize this field.
			t.Entries = make([]*entry, 0)
		}
		for _, e := range t.Entries {
			// Clear task, since the field will be the same on all of them.
			e.Task = nil
		}

		data, err := jsonv2.Marshal(t, jsontext.WithIndent("  "))
		if err != nil {
			return err
		}
		data = append(data, '\n')
		if _, err := os.Stdout.Write(data); err != nil {
			return err
		}
	}

	return nil
}

func newTaskEditCommand(g *globalConfig) *cobra.Command {
	c := &cobra.Command{
		Use:           "edit [flags] ID",
		Short:         "Change details about a task",
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	opts := new(editTaskOptions)
	c.Flags().StringVar(&opts.description, "description", "", "new description for task")
	c.Flags().StringSliceVar(&opts.labels, "label", nil, "comma-separated task `label`s")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		opts.taskID = args[0]
		opts.descriptionPresent = c.Flags().Changed("description")
		opts.labelsPresent = c.Flags().Changed("label")
		return runTaskEdit(cmd.Context(), g, opts)
	}
	return c
}

type editTaskOptions struct {
	taskID string

	newTaskOptions
	descriptionPresent bool
	labelsPresent      bool
}

func runTaskEdit(ctx context.Context, g *globalConfig, opts *editTaskOptions) error {
	taskID, err := uuid.Parse(opts.taskID)
	if err != nil {
		return err
	}
	labels, err := cleanLabels(opts.labels)
	if err != nil {
		return err
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

	if err := verifyTaskExists(db, taskID); err != nil {
		return err
	}

	if opts.descriptionPresent {
		err := sqlitex.ExecuteTransientFS(db, sqlFiles(), "tasks/set_description.sql", &sqlitex.ExecOptions{
			Named: map[string]any{
				":uuid":        taskID.String(),
				":description": opts.description,
			},
		})
		if err != nil {
			return fmt.Errorf("set description: %v", err)
		}
	}

	if opts.labelsPresent {
		err := sqlitex.ExecuteTransientFS(db, sqlFiles(), "tasks/clear_labels.sql", &sqlitex.ExecOptions{
			Named: map[string]any{
				":uuid": taskID.String(),
			},
		})
		if err != nil {
			return fmt.Errorf("set labels: %v", err)
		}
		if err := addTaskLabels(db, taskID, slices.Values(labels)); err != nil {
			return err
		}
	}

	return nil
}

func newTaskSelectCommand(g *globalConfig) *cobra.Command {
	c := &cobra.Command{
		Use:           "select",
		Short:         "Run fzf on the tasks",
		Args:          cobra.ArbitraryArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	multi := c.Flags().BoolP("multi", "m", false, "enable multi-select")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		return runTaskSelect(cmd.Context(), g, *multi, taskDescriptionFromArgs(args))
	}
	return c
}

func runTaskSelect(ctx context.Context, g *globalConfig, multi bool, query string) error {
	db, err := g.open(ctx)
	if err != nil {
		return err
	}
	defer closeConn(ctx, db)

	ids, err := selectTask(ctx, db, &fzfOptions{
		multi:        multi,
		initialQuery: query,
		select1:      query != "",
	})
	if err != nil {
		return err
	}
	for _, id := range ids {
		fmt.Println(id)
	}

	return nil
}

func selectTask(ctx context.Context, db *sqlite.Conn, opts *fzfOptions) (uuid.UUIDs, error) {
	opts = opts.clone()
	opts.template = "{2} ({1})"

	var rows [][2]string
	err := listTasks(db, func(t *task) bool {
		description := plainTaskDescription(t.Description, false)
		if t.EntryCount == 1 {
			description += "\n1 entry"
		} else {
			description = fmt.Sprintf("%s\n%d entries", t.Description, t.EntryCount)
		}
		rows = append(rows, [2]string{t.ID.String(), description})
		return true
	})
	if err != nil {
		return nil, err
	}

	output, err := fzf(ctx, func(yield func(string, string) bool) {
		for _, row := range rows {
			if !yield(row[0], row[1]) {
				return
			}
		}
	}, opts)
	if err != nil {
		return nil, err
	}
	return parseUUIDs(output)
}

func newTaskDeleteCommand(g *globalConfig) *cobra.Command {
	c := &cobra.Command{
		Use:           "delete [flags] ID [...]",
		Short:         "Delete one or more tasks",
		Args:          cobra.MinimumNArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	force := c.Flags().BoolP("force", "f", false, "delete task even if it has entries")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		return runTaskDelete(cmd.Context(), g, args, *force)
	}
	return c
}

func runTaskDelete(ctx context.Context, g *globalConfig, taskIDStrings []string, force bool) error {
	taskIDs := make(uuid.UUIDs, 0, len(taskIDStrings))
	for _, s := range taskIDStrings {
		id, err := uuid.Parse(s)
		if err != nil {
			return err
		}
		taskIDs = append(taskIDs, id)
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

	deleteEntriesStmt, err := sqlitex.PrepareTransientFS(db, sqlFiles(), "entries/delete_by_task.sql")
	if err != nil {
		return err
	}
	defer deleteEntriesStmt.Finalize()
	deleteTaskStmt, err := sqlitex.PrepareTransientFS(db, sqlFiles(), "tasks/delete.sql")
	if err != nil {
		return err
	}
	defer deleteTaskStmt.Finalize()

	for _, id := range taskIDs {
		if err := verifyTaskExists(db, id); err != nil {
			return err
		}
		if !force {
			if hasEntries, err := taskHasEntries(db, id); err != nil {
				return err
			} else if hasEntries {
				return fmt.Errorf("task %v has entries", id)
			}
		}

		deleteEntriesStmt.SetText(":uuid", id.String())
		if _, err := deleteEntriesStmt.Step(); err != nil {
			return fmt.Errorf("delete entries for task %v: %v", id, err)
		}
		if err := deleteEntriesStmt.Reset(); err != nil {
			return fmt.Errorf("delete entries for task %v: %v", id, err)
		}

		deleteTaskStmt.SetText(":uuid", id.String())
		if _, err := deleteTaskStmt.Step(); err != nil {
			return fmt.Errorf("delete task %v: %v", id, err)
		}
		if err := deleteTaskStmt.Reset(); err != nil {
			return fmt.Errorf("delete task %v: %v", id, err)
		}
	}

	return nil
}

func fetchTask(db *sqlite.Conn, taskID uuid.UUID) (*task, error) {
	var result *task
	err := sqlitex.ExecuteTransientFS(db, sqlFiles(), "tasks/get.sql", &sqlitex.ExecOptions{
		Named: map[string]any{":uuid": taskID.String()},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			result = &task{
				ID:          taskID,
				Description: stmt.GetText("description"),
				EntryCount:  int(stmt.GetInt64("entry_count")),
			}
			var labelsBuf []byte
			var err error
			result.Labels, err = labelsFromDatabase(stmt, "labels", &labelsBuf)
			if err != nil {
				return err
			}
			return nil
		},
	})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, &taskNotFoundError{id: taskID}
	}
	return result, nil
}

func listTasks(db *sqlite.Conn, yield func(*task) bool) error {
	errStop := errors.New("stop")
	var labelsBuf []byte
	err := sqlitex.ExecuteTransientFS(db, sqlFiles(), "tasks/list.sql", &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			t := &task{
				Description: stmt.GetText("description"),
				EntryCount:  int(stmt.GetInt64("entry_count")),
			}
			var err error
			t.ID, err = uuid.Parse(stmt.GetText("uuid"))
			if err != nil {
				return err
			}
			t.Labels, err = labelsFromDatabase(stmt, "labels", &labelsBuf)
			if err != nil {
				return fmt.Errorf("%v: %v", t.ID, err)
			}

			if !yield(t) {
				return errStop
			}

			return nil
		},
	})
	if errors.Is(err, errStop) {
		err = nil
	}
	return err
}

func verifyTaskExists(db *sqlite.Conn, taskID uuid.UUID) error {
	stmt, err := sqlitex.PrepareTransientFS(db, sqlFiles(), "tasks/exists.sql")
	if err != nil {
		return fmt.Errorf("check for task %v: %v", taskID, err)
	}
	defer stmt.Finalize()
	stmt.SetText(":uuid", taskID.String())
	exists, err := sqlitex.ResultBool(stmt)
	if err != nil {
		return fmt.Errorf("check for task %v: %v", taskID, err)
	}
	if !exists {
		return &taskNotFoundError{id: taskID}
	}
	return nil
}

// taskHasEntries reports whether the task with the given ID has entries.
func taskHasEntries(db *sqlite.Conn, taskID uuid.UUID) (bool, error) {
	stmt, err := sqlitex.PrepareTransientFS(db, sqlFiles(), "tasks/has_entries.sql")
	if err != nil {
		return false, fmt.Errorf("check for task %v entries: %v", taskID, err)
	}
	defer stmt.Finalize()
	stmt.SetText(":uuid", taskID.String())
	hasRows, err := sqlitex.ResultBool(stmt)
	if err != nil {
		err = fmt.Errorf("check for task %v entries: %v", taskID, err)
	}
	return hasRows, err
}

// labelsFromDatabase unmarshals the JSON labels column from a task row.
func labelsFromDatabase(stmt *sqlite.Stmt, columnName string, labelsBuf *[]byte) ([]string, error) {
	i := stmt.ColumnIndex(columnName)
	labelsLen := stmt.ColumnLen(i)
	*labelsBuf = slices.Grow((*labelsBuf)[:0], labelsLen)
	*labelsBuf = (*labelsBuf)[:labelsLen]
	stmt.ColumnBytes(i, (*labelsBuf)[:labelsLen])
	var labels []string
	if err := jsonv2.Unmarshal(*labelsBuf, &labels); err != nil {
		return nil, fmt.Errorf("labels: %v", err)
	}
	return labels, nil
}

type taskNotFoundError struct {
	id uuid.UUID
}

func (e *taskNotFoundError) Error() string {
	return fmt.Sprintf("no task with ID %v", e.id)
}

func isTaskNotFound(err error) bool {
	return errors.As(err, new(*taskNotFoundError))
}

func taskDescriptionFromArgs(args []string) string {
	return strings.TrimSpace(strings.Join(args, " "))
}

func plainTaskDescription(s string, quoted bool) string {
	if strings.TrimSpace(s) == "" {
		return "(unnamed task)"
	}
	if quoted {
		return "“" + s + "”"
	}
	return s
}
