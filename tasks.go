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
	"encoding/csv"
	"fmt"
	"iter"
	"os"
	"slices"
	"strings"
	"time"

	jsonv2 "github.com/go-json-experiment/json"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func newTaskCommand(g *globalConfig) *cobra.Command {
	c := &cobra.Command{
		Use:           "task",
		Short:         "Manage types of activities",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	c.AddCommand(
		newTaskListCommand(g),
		newTaskNewCommand(g),
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
	c.RunE = func(cmd *cobra.Command, args []string) error {
		return runTaskList(cmd.Context(), g)
	}
	return c
}

func runTaskList(ctx context.Context, g *globalConfig) error {
	db, err := g.open(ctx)
	if err != nil {
		return err
	}
	defer closeConn(ctx, db)

	w := csv.NewWriter(os.Stdout)
	w.Write([]string{"ID", "Description", "Labels"})
	var labelsBuf []byte
	err = sqlitex.ExecuteTransientFS(db, sqlFiles(), "tasks/list.sql", &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			labelsLen := stmt.GetLen("labels")
			labelsBuf = slices.Grow(labelsBuf[:0], labelsLen)
			labelsBuf = labelsBuf[:labelsLen]
			stmt.GetBytes("labels", labelsBuf[:labelsLen])
			var labels []string
			if err := jsonv2.Unmarshal(labelsBuf, &labels); err != nil {
				return fmt.Errorf("labels: %v", err)
			}

			row := []string{
				stmt.GetText("uuid"),
				stmt.GetText("description"),
				strings.Join(labels, ","),
			}
			if err := w.Write(row); err != nil {
				return err
			}
			return nil
		},
	})
	if err != nil {
		return err
	}

	w.Flush()
	if err := w.Error(); err != nil {
		return err
	}

	return nil
}

func newTaskNewCommand(g *globalConfig) *cobra.Command {
	c := &cobra.Command{
		Use:           "new",
		Short:         "Add a new activity type",
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

func taskDescriptionFromArgs(args []string) string {
	return strings.TrimSpace(strings.Join(args, " "))
}

func safeTaskDescription(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(unnamed task)"
	}
	return s
}
