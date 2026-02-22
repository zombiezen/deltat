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
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	jsonv2 "github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"zombiezen.com/go/log"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

//go:embed docs/task-import.txt
var taskImportCommandHelp string

func newTaskImportCommand(g *globalConfig) *cobra.Command {
	c := &cobra.Command{
		Use:           "import [flags] [FILE]",
		Short:         "Import a file of tasks",
		Long:          taskImportCommandHelp,
		Args:          cobra.MaximumNArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	opts := &taskImportOptions{
		input:         g.stdin,
		inputFileName: "<stdin>",
	}
	c.Flags().BoolVarP(&opts.dryRun, "dry-run", "n", false, "preview changes to be made")

	formatOptions := joinSeq(func(yield func(string) bool) {
		knownOutputFormats(func(f outputFormat) bool {
			return yield(string(f))
		})
	}, ", ", "or")
	format := csvOutputFormat
	c.Flags().Var(&format, "format", "input `format` ("+formatOptions+")")
	formatCompletions := slices.Collect(func(yield func(cobra.Completion) bool) {
		knownOutputFormats(func(f outputFormat) bool {
			return yield(cobra.Completion(f))
		})
	})
	c.RegisterFlagCompletionFunc("format", cobra.FixedCompletions(formatCompletions, cobra.ShellCompDirectiveDefault))

	c.RunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if len(args) > 0 {
			opts.inputFileName = filepath.Base(args[0])

			f, err := os.Open(g.path(args[0]))
			if err != nil {
				return err
			}
			defer f.Close()
			opts.input = f
		} else if g.isStdinTerminal {
			log.Infof(ctx, "Reading from stdin...")
		}

		switch format {
		case plainOutputFormat:
			return runTaskImportPlain(cmd.Context(), g, opts)
		case csvOutputFormat:
			return runTaskImportCSV(cmd.Context(), g, opts)
		case jsonOutputFormat:
			return runTaskImportJSON(cmd.Context(), g, opts)
		default:
			return fmt.Errorf("unhandled format %q", format)
		}
	}
	return c
}

type taskImportOptions struct {
	input         io.Reader
	inputFileName string
	dryRun        bool
}

func runTaskImportPlain(ctx context.Context, g *globalConfig, opts *taskImportOptions) (err error) {
	s := bufio.NewScanner(opts.input)

	if opts.dryRun {
		var n int64
		for s.Scan() {
			if len(bytes.TrimSpace(s.Bytes())) > 0 {
				n++
			}
		}
		if n == 1 {
			fmt.Fprintf(g.stdout, "Creating %d tasks\n", n)
		} else if n > 1 {
			fmt.Fprintf(g.stdout, "Creating %d tasks\n", n)
		}
		return s.Err()
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

	var prevID uuid.UUID
	var resultError error
	for line := 1; s.Scan(); line++ {
		t := new(task)
		t.Description = string(bytes.TrimSpace(s.Bytes()))
		if t.Description == "" {
			continue
		}

		t.ID = newUUIDV7(g.runStart, prevID)
		prevID = t.ID
		if err := insertTask(db, g.runStart, t); err != nil {
			resultError = errors.Join(resultError, fmt.Errorf("%s:%d: %v", opts.inputFileName, line, err))
		}
	}

	resultError = errors.Join(resultError, s.Err())
	return resultError
}

func runTaskImportCSV(ctx context.Context, g *globalConfig, opts *taskImportOptions) (err error) {
	r := csv.NewReader(opts.input)
	r.ReuseRecord = true

	firstRow, err := readCSVRow(ctx, r)
	if err != nil {
		return err
	}
	idColumn := -1
	descriptionColumn := -1
	labelsColumn := -1
	err = mapCSVColumnHeaders([]csvColumnHeader{
		{&idColumn, []string{"ID"}},
		{&descriptionColumn, []string{"Description"}},
		{&labelsColumn, []string{"Labels"}},
	}, firstRow)
	if err != nil {
		return err
	}
	if descriptionColumn == -1 {
		return fmt.Errorf("%s: must have a Description column", opts.inputFileName)
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

	var prevID uuid.UUID
	var resultError error
	for {
		row, err := readCSVRow(ctx, r)
		if err != nil {
			if err == io.EOF {
				err = nil
			}
			resultError = errors.Join(resultError, err)
			return resultError
		}

		t := new(task)
		t.Description = strings.TrimSpace(row[descriptionColumn])

		newTask := true
		if 0 <= idColumn && idColumn < len(row) {
			s := row[idColumn]
			if s != "" {
				var err error
				t.ID, err = uuid.Parse(s)
				if err != nil {
					line, col := r.FieldPos(idColumn)
					resultError = errors.Join(resultError, fmt.Errorf("%s:%d:%d: %v", opts.inputFileName, line, col, err))
					continue
				}
				newTask = false
			}
		}
		if newTask {
			t.ID = newUUIDV7(g.runStart, prevID)
			prevID = t.ID
		}

		if 0 <= labelsColumn && labelsColumn < len(row) {
			if s := strings.TrimSpace(row[labelsColumn]); s != "" {
				var err error
				t.Labels, err = cleanLabels(strings.Split(s, ","))
				if err != nil {
					line, col := r.FieldPos(labelsColumn)
					resultError = errors.Join(resultError, fmt.Errorf("%s:%d:%d: %v", opts.inputFileName, line, col, err))
					continue
				}
			}
		}

		if opts.dryRun {
			line, _ := r.FieldPos(0)
			if newTask {
				fmt.Fprintf(g.stdout, "%s:%d: new task %s\n", opts.inputFileName, line, plainTaskDescription(t.Description, true))
			} else {
				switch err := verifyTaskExists(db, t.ID); {
				case err == nil:
					fmt.Fprintf(g.stdout, "%s:%d: update task %v with description %s\n", opts.inputFileName, line, t.ID, plainTaskDescription(t.Description, true))
				case isTaskNotFound(err):
					fmt.Fprintf(g.stdout, "%s:%d: new task %s\n", opts.inputFileName, line, plainTaskDescription(t.Description, true))
				default:
					resultError = errors.Join(resultError, fmt.Errorf("%s:%d: %v", opts.inputFileName, line, err))
				}
			}
			continue
		}

		if err := insertTask(db, g.runStart, t); err != nil {
			if newTask || sqlite.ErrCode(err) != sqlite.ResultConstraintPrimaryKey {
				line, _ := r.FieldPos(0)
				resultError = errors.Join(resultError, fmt.Errorf("%s:%d: %v", opts.inputFileName, line, err))
				continue
			}

			if err := setTaskDescription(db, t.ID, t.Description); err != nil {
				line, col := r.FieldPos(descriptionColumn)
				resultError = errors.Join(resultError, fmt.Errorf("%s:%d:%d: %v", opts.inputFileName, line, col, err))
				// Update as many fields as possible, don't skip others.
			}

			if 0 <= labelsColumn && labelsColumn < len(row) {
				if err := setTaskLabels(db, t.ID, slices.Values(t.Labels)); err != nil {
					line, col := r.FieldPos(labelsColumn)
					resultError = errors.Join(resultError, fmt.Errorf("%s:%d:%d: %v", opts.inputFileName, line, col, err))
					// Update as many fields as possible, don't skip others.
				}
			}
		}
	}
}

func runTaskImportJSON(ctx context.Context, g *globalConfig, opts *taskImportOptions) (err error) {
	d := jsontext.NewDecoder(opts.input)

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

	var prevID uuid.UUID
	var resultError error
	for {
		t := new(task)
		if err := jsonv2.UnmarshalDecode(d, t); err != nil {
			if err == io.EOF {
				err = nil
			}
			resultError = errors.Join(resultError, err)
			return resultError
		}

		newTask := t.ID == uuid.Nil
		if newTask {
			t.ID = newUUIDV7(g.runStart, prevID)
			prevID = t.ID
		}

		var err error
		t.Labels, err = cleanLabels(t.Labels)
		if err != nil {
			// TODO(someday): Add line number.
			resultError = errors.Join(resultError, fmt.Errorf("%s: %v", opts.inputFileName, err))
			continue
		}

		if opts.dryRun {
			// TODO(someday): Add line number.
			if newTask {
				fmt.Fprintf(g.stdout, "%s: new task %s\n", opts.inputFileName, plainTaskDescription(t.Description, true))
			} else {
				switch err := verifyTaskExists(db, t.ID); {
				case err == nil:
					fmt.Fprintf(g.stdout, "%s: update task %v with description %s\n", opts.inputFileName, t.ID, plainTaskDescription(t.Description, true))
				case isTaskNotFound(err):
					fmt.Fprintf(g.stdout, "%s: new task %s\n", opts.inputFileName, plainTaskDescription(t.Description, true))
				default:
					resultError = errors.Join(resultError, fmt.Errorf("%s: %v", opts.inputFileName, err))
				}
			}
			continue
		}

		if err := insertTask(db, g.runStart, t); err != nil {
			if newTask || sqlite.ErrCode(err) != sqlite.ResultConstraintPrimaryKey {
				// TODO(someday): Add line number.
				resultError = errors.Join(resultError, fmt.Errorf("%s: %v", opts.inputFileName, err))
				continue
			}

			if err := setTaskDescription(db, t.ID, t.Description); err != nil {
				// TODO(someday): Add line number.
				resultError = errors.Join(resultError, fmt.Errorf("%s: %v", opts.inputFileName, err))
				// Update as many fields as possible, don't skip others.
			}
			if err := setTaskLabels(db, t.ID, slices.Values(t.Labels)); err != nil {
				// TODO(someday): Add line number.
				resultError = errors.Join(resultError, fmt.Errorf("%s: %v", opts.inputFileName, err))
				// Update as many fields as possible, don't skip others.
			}
		}
	}
}
