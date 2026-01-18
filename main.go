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
	"os"
	"os/signal"
	"sync"
	"time"

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
		newShellCommand(g),
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
