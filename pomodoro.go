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
	"time"

	"github.com/spf13/cobra"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

type pomodoroConfiguration struct {
	duration      time.Duration
	breakDuration time.Duration
}

func defaultPomodoroConfiguration() *pomodoroConfiguration {
	return &pomodoroConfiguration{
		duration:      25 * time.Minute,
		breakDuration: 5 * time.Minute,
	}
}

func readPomodoroConfiguration(db *sqlite.Conn) (*pomodoroConfiguration, error) {
	cfg := defaultPomodoroConfiguration()
	err := sqlitex.ExecuteTransientFS(db, sqlFiles(), "pomodoro/get.sql", &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			cfg.duration = time.Duration(stmt.GetInt64("duration_minutes")) * time.Minute
			cfg.breakDuration = time.Duration(stmt.GetInt64("break_duration_minutes")) * time.Minute
			return nil
		},
	})
	if err != nil {
		return nil, fmt.Errorf("read pomodoro configuration: %v", err)
	}
	return cfg, nil
}

func writePomodoroConfiguration(db *sqlite.Conn, cfg *pomodoroConfiguration) (err error) {
	defer sqlitex.Save(db)(&err)

	err = sqlitex.ExecuteTransientFS(db, sqlFiles(), "pomodoro/insert.sql", nil)
	if err != nil && sqlite.ErrCode(err).ToPrimary() != sqlite.ResultConstraint {
		// Ignoring constraint failures, since that should mean the row already exists.
		return fmt.Errorf("write pomodoro configuration: %v", err)
	}

	err = sqlitex.ExecuteScriptFS(db, sqlFiles(), "pomodoro/set.sql", &sqlitex.ExecOptions{
		Named: map[string]any{
			":duration_minutes":       cfg.duration / time.Minute,
			":break_duration_minutes": cfg.breakDuration / time.Minute,
		},
	})
	if err != nil {
		return fmt.Errorf("write pomodoro configuration: %v", err)
	}

	if n := db.Changes(); n != 1 {
		return fmt.Errorf("write pomodoro configuration: %d rows affected by write (corrupt database?)", n)
	}

	return nil
}

func newPomodoroSettingsCommand(g *globalConfig) *cobra.Command {
	c := &cobra.Command{
		Use:           "pomodoro-settings",
		Short:         "Display or change default Pomodoro timings",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	opts := new(editPomodoroSettingsOptions)
	c.Flags().IntVar(&opts.durationMinutes, "duration", 0, "number of `minutes` for a Pomodoro to last")
	c.Flags().IntVar(&opts.breakMinutes, "break-duration", 0, "number of `minutes` for a Pomodoro break to last")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		opts.hasDurationMinutes = cmd.Flags().Changed("duration")
		opts.hasBreakMinutes = cmd.Flags().Changed("break-duration")
		if opts.isEmpty() {
			return runPomodoroSettingsShow(cmd.Context(), g)
		} else {
			return runPomodoroSettingsEdit(cmd.Context(), g, opts)
		}
	}
	return c
}

func runPomodoroSettingsShow(ctx context.Context, g *globalConfig) error {
	db, err := g.open(ctx)
	if err != nil {
		return err
	}
	defer closeConn(ctx, db)

	cfg, err := readPomodoroConfiguration(db)
	if err != nil {
		return err
	}
	fmt.Printf("%d minutes followed by %d-minute break\n", cfg.duration/time.Minute, cfg.breakDuration/time.Minute)
	return nil
}

type editPomodoroSettingsOptions struct {
	durationMinutes    int
	hasDurationMinutes bool
	breakMinutes       int
	hasBreakMinutes    bool
}

func (opts *editPomodoroSettingsOptions) isEmpty() bool {
	return opts == nil || (!opts.hasDurationMinutes && !opts.hasBreakMinutes)
}

func (opts *editPomodoroSettingsOptions) apply(cfg *pomodoroConfiguration) {
	if opts.hasDurationMinutes {
		cfg.duration = time.Duration(opts.durationMinutes) * time.Minute
	}
	if opts.hasBreakMinutes {
		cfg.breakDuration = time.Duration(opts.breakMinutes) * time.Minute
	}
}

func runPomodoroSettingsEdit(ctx context.Context, g *globalConfig, opts *editPomodoroSettingsOptions) error {
	if opts.hasDurationMinutes && opts.durationMinutes <= 0 {
		return fmt.Errorf("duration must be positive")
	}
	if opts.hasBreakMinutes && opts.breakMinutes < 0 {
		return fmt.Errorf("break duration must be non-negative")
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

	cfg, err := readPomodoroConfiguration(db)
	if err != nil {
		return err
	}
	opts.apply(cfg)
	if err := writePomodoroConfiguration(db, cfg); err != nil {
		return err
	}

	return nil
}
