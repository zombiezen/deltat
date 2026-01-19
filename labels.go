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
	"fmt"
	"iter"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func newLabelCommand(g *globalConfig) *cobra.Command {
	c := &cobra.Command{
		Use:           "label",
		Short:         "Manage labels",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	c.AddCommand(
		newLabelNewCommand(g),
		newLabelListCommand(g),
	)
	return c
}

func newLabelListCommand(g *globalConfig) *cobra.Command {
	c := &cobra.Command{
		Use:           "list",
		Short:         "List all labels",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		return runLabelList(cmd.Context(), g)
	}
	return c
}

func runLabelList(ctx context.Context, g *globalConfig) error {
	db, err := g.open(ctx)
	if err != nil {
		return err
	}
	defer closeConn(ctx, db)

	err = sqlitex.ExecuteTransientFS(db, sqlFiles(), "labels/list.sql", &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			_, err := fmt.Println(stmt.GetText("name"))
			return err
		},
	})
	if err != nil {
		return err
	}

	return nil
}

func newLabelNewCommand(g *globalConfig) *cobra.Command {
	c := &cobra.Command{
		Use:           "new [flags] LABEL [...]",
		Short:         "Add new labels",
		Args:          cobra.MinimumNArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		return runLabelNew(cmd.Context(), g, args)
	}
	return c
}

func runLabelNew(ctx context.Context, g *globalConfig, labels []string) (err error) {
	labels, err = cleanLabels(labels)
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

	if err := upsertLabels(db, slices.Values(labels)); err != nil {
		return err
	}

	return nil
}

// cleanLabels validates the given set of labels.
func cleanLabels(labels []string) ([]string, error) {
	modified := false
	for i, label := range labels {
		newLabel := strings.TrimSpace(label)
		if newLabel == "" {
			return labels, fmt.Errorf("empty label")
		}
		if strings.Contains(newLabel, ",") {
			return labels, fmt.Errorf("label %s: cannot contain commas", newLabel)
		}
		if newLabel != label {
			if !modified {
				labels = slices.Clone(labels)
				modified = true
			}
			labels[i] = label
		}
	}
	return labels, nil
}

func upsertLabels(db *sqlite.Conn, labels iter.Seq[string]) (err error) {
	defer sqlitex.Save(db)(&err)

	stmt, err := sqlitex.PrepareTransientFS(db, sqlFiles(), "labels/upsert.sql")
	if err != nil {
		return err
	}
	defer stmt.Finalize()
	for label := range labels {
		stmt.SetText(":name", label)
		if _, err := stmt.Step(); err != nil {
			return fmt.Errorf("insert label %q: %v", label, err)
		}
		if err := stmt.Reset(); err != nil {
			return fmt.Errorf("insert label %q: %v", label, err)
		}
	}
	return nil
}
