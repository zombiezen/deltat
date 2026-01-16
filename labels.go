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
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func newLabelsCommand(g *globalConfig) *cobra.Command {
	c := &cobra.Command{
		Use:           "labels",
		Short:         "Manage labels",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	c.AddCommand(
		newLabelAddCommand(g),
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

func newLabelAddCommand(g *globalConfig) *cobra.Command {
	c := &cobra.Command{
		Use:           "add [flags] LABEL [...]",
		Short:         "Add new labels",
		Args:          cobra.MinimumNArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		return runLabelAdd(cmd.Context(), g, args)
	}
	return c
}

func runLabelAdd(ctx context.Context, g *globalConfig, labels []string) (err error) {
	labels = slices.Clone(labels)
	for i, label := range labels {
		label = strings.TrimSpace(label)
		if label == "" {
			return fmt.Errorf("empty label")
		}
		if strings.Contains(label, ",") {
			return fmt.Errorf("label %s: cannot contain commas", label)
		}
		labels[i] = label
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

	stmt, err := sqlitex.PrepareTransientFS(db, sqlFiles(), "labels/upsert.sql")
	if err != nil {
		return err
	}
	defer stmt.Finalize()
	for _, label := range labels {
		stmt.SetText(":name", label)
		if _, err := stmt.Step(); err != nil {
			return err
		}
		if err := stmt.Reset(); err != nil {
			return err
		}
	}

	return nil
}
