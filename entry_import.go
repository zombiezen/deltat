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
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"golang.org/x/term"
	"zombiezen.com/go/log"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

//go:embed docs/entry-import.txt
var entryImportCommandHelp string

func newEntryImportCommand(g *globalConfig) *cobra.Command {
	c := &cobra.Command{
		Use:           "import [flags] [FILE]",
		Short:         "Import a CSV file of time entries",
		Long:          entryImportCommandHelp,
		Args:          cobra.MaximumNArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		inputFile := os.Stdin
		inputFileName := "<stdin>"
		if len(args) > 0 {
			var err error
			inputFile, err = os.Open(args[0])
			if err != nil {
				return err
			}
			defer inputFile.Close()
			inputFileName = filepath.Base(args[0])
		} else if term.IsTerminal(int(os.Stdin.Fd())) {
			log.Infof(ctx, "Reading from stdin...")
		}

		return runEntryImportCSV(ctx, g, inputFileName, inputFile)
	}
	return c
}

func runEntryImportCSV(ctx context.Context, g *globalConfig, inputFileName string, input io.Reader) (err error) {
	now := getNow()

	r := csv.NewReader(input)
	r.ReuseRecord = true

	firstRow, err := readCSVRow(ctx, r)
	if err != nil {
		return err
	}
	idColumn := -1
	startTimeColumn := -1
	endTimeColumn := -1
	taskIDColumn := -1
	taskDescriptionColumn := -1
	err = mapCSVColumnHeaders([]csvColumnHeader{
		{&idColumn, []string{"ID"}},
		{&startTimeColumn, []string{"Start Time", "Start", "start_time"}},
		{&endTimeColumn, []string{"End Time", "End", "end_time"}},
		{&taskIDColumn, []string{"Task ID"}},
		{&taskDescriptionColumn, []string{"Description", "Task Description"}},
	}, firstRow)
	if err != nil {
		return err
	}
	if startTimeColumn == -1 || endTimeColumn == -1 {
		return fmt.Errorf("%s: must have Start Time and End Time columns", inputFileName)
	}
	if taskIDColumn == -1 && taskDescriptionColumn == -1 {
		return fmt.Errorf("%s: must have either a Task ID or Description column", inputFileName)
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
	defer func() {
		// Because this operation is largely idempotent,
		// we want to commit as much as we can,
		// even if the overall import fails.
		var endError error
		endFn(&endError)
		if err == nil {
			err = endError
		} else if endError != nil {
			log.Errorf(ctx, "Commit transaction: %v", endError)
		}
	}()

	taskMap := make(map[string]uuid.UUID)
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

		e := &entry{Task: new(task)}
		if 0 <= idColumn && idColumn < len(row) {
			var err error
			e.ID, err = uuid.Parse(row[idColumn])
			if err != nil {
				line, col := r.FieldPos(idColumn)
				resultError = errors.Join(resultError, fmt.Errorf("%s:%d:%d: %v", inputFileName, line, col, err))
				continue
			}
		}
		e.StartTime, err = parseTime(now, row[startTimeColumn], true)
		if err != nil {
			line, col := r.FieldPos(startTimeColumn)
			resultError = errors.Join(resultError, fmt.Errorf("%s:%d:%d: %v", inputFileName, line, col, err))
			continue
		}
		if s := row[endTimeColumn]; s != "" {
			endTime, err := parseTime(now, row[endTimeColumn], true)
			if err != nil {
				line, col := r.FieldPos(endTimeColumn)
				resultError = errors.Join(resultError, fmt.Errorf("%s:%d:%d: %v", inputFileName, line, col, err))
				continue
			}
			e.RawEndTime = &endTime
		}

		if 0 <= taskIDColumn && taskIDColumn < len(row) {
			s := row[taskIDColumn]
			if s != "" {
				var err error
				e.Task.ID, err = uuid.Parse(s)
				if err != nil {
					line, col := r.FieldPos(taskIDColumn)
					resultError = errors.Join(resultError, fmt.Errorf("%s:%d:%d: %v", inputFileName, line, col, err))
					continue
				}
			} else if taskDescriptionColumn < 0 {
				line, col := r.FieldPos(taskIDColumn)
				resultError = errors.Join(resultError, fmt.Errorf("%s:%d:%d: missing task ID", inputFileName, line, col))
				continue
			}
		}

		if 0 <= taskDescriptionColumn && taskDescriptionColumn < len(row) {
			e.Task.Description = row[taskDescriptionColumn]
			if e.Task.Description != "" && e.Task.ID == uuid.Nil {
				e.Task.ID = taskMap[e.Task.Description]
			}
		}

		newTask := e.Task.ID == uuid.Nil
		if newTask {
			e.Task.ID = newUUIDV7(now, prevID)
			prevID = e.Task.ID
		}
		if err := insertTask(db, now, e.Task); err != nil && (newTask || sqlite.ErrCode(err) != sqlite.ResultConstraintPrimaryKey) {
			line, _ := r.FieldPos(0)
			resultError = errors.Join(resultError, fmt.Errorf("%s:%d: %v", inputFileName, line, err))
			continue
		}
		if newTask && e.Task.Description != "" {
			taskMap[e.Task.Description] = e.Task.ID
		}

		newEntry := e.ID == uuid.Nil
		if newEntry {
			e.ID = newUUIDV7(now, prevID)
			prevID = e.ID
		}
		if err := insertEntry(db, e); err != nil {
			if newEntry || sqlite.ErrCode(err) != sqlite.ResultConstraintPrimaryKey {
				line, _ := r.FieldPos(0)
				resultError = errors.Join(resultError, fmt.Errorf("%s:%d: %v", inputFileName, line, err))
				continue
			}

			if err := updateEntryTimes(db, e.ID, e.StartTime, true, e.EndTime(), true); err != nil {
				line, _ := r.FieldPos(0)
				resultError = errors.Join(resultError, fmt.Errorf("%s:%d: %v", inputFileName, line, err))
				// Update as many fields as possible, don't skip others.
			}
			if err := updateEntryTaskID(db, e.ID, e.Task.ID); err != nil {
				line, _ := r.FieldPos(0)
				resultError = errors.Join(resultError, fmt.Errorf("%s:%d: %v", inputFileName, line, err))
				// Update as many fields as possible, don't skip others.
			}
		}
	}
}

type csvColumnHeader struct {
	index *int
	names []string
}

func mapCSVColumnHeaders(headers []csvColumnHeader, row []string) error {
	for _, h := range headers {
		*h.index = -1
	}

	var resultError error
forEachColumn:
	for i, col := range row {
		col = strings.TrimSpace(col)
		for _, h := range headers {
			for _, name := range h.names {
				if strings.EqualFold(col, name) {
					if *h.index >= 0 {
						resultError = errors.Join(resultError, fmt.Errorf("multiple %s columns", h.names[0]))
					} else {
						*h.index = i
					}
					continue forEachColumn
				}
			}
		}
	}

	return resultError
}

func readCSVRow(ctx context.Context, r *csv.Reader) ([]string, error) {
	var row []string
	var err error
	done := make(chan struct{}, 1)
	go func() {
		row, err = r.Read()
		done <- struct{}{}
	}()
	select {
	case <-done:
		return row, err
	case <-ctx.Done():
		// We leak a goroutine in this case, but in our program,
		// we exit out quickly and avoid using the reader.
		return nil, ctx.Err()
	}
}
