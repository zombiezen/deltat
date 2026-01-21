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
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"rsc.io/script"
	"rsc.io/script/scripttest"
)

func TestEntries(t *testing.T) {
	runScriptTests(t, "testdata/entries/*.txt")
}

func TestLabels(t *testing.T) {
	runScriptTests(t, "testdata/labels/*.txt")
}

func TestTasks(t *testing.T) {
	runScriptTests(t, "testdata/tasks/*.txt")
}

func runScriptTests(t *testing.T, pattern string) {
	engine := &script.Engine{
		Cmds:  scripttest.DefaultCmds(),
		Conds: scripttest.DefaultConds(),
	}
	exePath, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	engine.Cmds["deltat"] = script.Program(
		exePath,
		func(c *exec.Cmd) error { return killProcess(c.Process) },
		30*time.Second,
	)
	ctx := t.Context()
	scripttest.Test(t, ctx, engine, []string{exeModeVar + "=1"}, filepath.FromSlash(pattern))
}

const exeModeVar = "DELTAT_SCRIPTTEST"

func TestMain(m *testing.M) {
	if os.Getenv(exeModeVar) == "1" {
		main()
	} else {
		m.Run()
	}
}
