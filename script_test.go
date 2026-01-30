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
	"io"
	"iter"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/spf13/pflag"
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

	teeUsage := script.CmdUsage{
		Summary: "write standard output to a file",
		Args:    "file [...]",
		Detail:  []string{"The command writes the stdout buffer to the files."},
	}
	engine.Cmds["tee"] = script.Command(teeUsage, func(state *script.State, args ...string) (script.WaitFunc, error) {
		for _, arg := range args {
			if err := os.WriteFile(state.Path(arg), []byte(state.Stdout()), 0o666); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})

	engine.Cmds["cut"] = script.Command(script.CmdUsage{
		Summary: "cut out selected portions of each line of a file",
		Args:    "-f list [-d delim] [file [...]]",
	}, cutCmd)

	engine.Cmds["read"] = script.Command(script.CmdUsage{
		Summary: "read stdout or a file into variables",
		Args:    "[--file path] name [...]",
		Detail: []string{
			"Read one line from standard output or the file given by --file,",
			"split it into words, then assign the first word to the first name,",
			"the second word to the second name, and so on.",
			"If there are more words than names,",
			"the remaining words and their delimiters are assigned to the last name.",
		},
	}, readCmd)

	engine.Cmds["tail"] = script.Command(script.CmdUsage{
		Summary: "display the last part of a file",
		Args:    "[-n number] [file [...]]",
	}, tailCmd)

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

func cutCmd(state *script.State, args ...string) (script.WaitFunc, error) {
	f := pflag.NewFlagSet("cut", pflag.ContinueOnError)
	fieldsString := f.StringP("fields", "f", "", "output field `list`")
	delimiter := f.StringP("delimiter", "d", "\t", "use `delim` as the field delimiter character")
	f.SetOutput(io.Discard)
	if err := f.Parse(args); err != nil {
		return nil, err
	}
	args = f.Args()

	if *fieldsString == "" {
		return nil, new(script.UsageError)
	}
	fields := strings.FieldsFunc(*fieldsString, func(c rune) bool {
		return c == ',' || c == ' ' || c == '\t'
	})

	var input string
	if len(args) == 0 {
		input = state.Stdout()
	} else {
		var err error
		input, err = catFiles(func(yield func(string) bool) {
			for _, arg := range args {
				if !yield(state.Path(arg)) {
					return
				}
			}
		})
		if err != nil {
			return nil, err
		}
	}

	lines := splitLines(input)
	output := new(strings.Builder)
	for _, line := range lines {
		row := strings.Split(line, *delimiter)
		first := true
		for _, f := range fields {
			i, err := strconv.Atoi(f)
			if err != nil {
				return nil, err
			}
			var s string
			if 1 <= i && i <= len(row) {
				s = row[i-1]
			}
			if first {
				first = false
			} else {
				output.WriteString(*delimiter)
			}
			output.WriteString(s)
		}
		output.WriteByte('\n')
	}
	return func(state *script.State) (stdout string, stderr string, err error) {
		return output.String(), "", nil
	}, nil
}

func tailCmd(state *script.State, args ...string) (script.WaitFunc, error) {
	f := pflag.NewFlagSet("tail", pflag.ContinueOnError)
	linesArg := f.StringP("lines", "n", "-10", "location is `number` lines")
	f.SetOutput(io.Discard)
	if err := f.Parse(args); err != nil {
		return nil, err
	}
	args = f.Args()

	startLine, err := strconv.Atoi(*linesArg)
	if err != nil {
		return nil, new(script.UsageError)
	}
	if !strings.HasPrefix(*linesArg, "+") && !strings.HasPrefix(*linesArg, "-") {
		startLine = -startLine
	}

	var input string
	if len(args) == 0 {
		input = state.Stdout()
	} else {
		var err error
		input, err = catFiles(func(yield func(string) bool) {
			for _, arg := range args {
				if !yield(state.Path(arg)) {
					return
				}
			}
		})
		if err != nil {
			return nil, err
		}
	}

	lines := splitLines(input)
	if startLine < 0 {
		startLine = len(lines) + startLine + 1
	}
	startLine = max(startLine, 1)
	if startLine-1 < len(lines) {
		lines = lines[startLine-1:]
	} else {
		lines = nil
	}
	output := strings.Join(lines, "")

	return func(state *script.State) (stdout string, stderr string, err error) {
		return output, "", nil
	}, nil
}

func readCmd(state *script.State, args ...string) (script.WaitFunc, error) {
	f := pflag.NewFlagSet("read", pflag.ContinueOnError)
	path := f.String("file", "", "`path` to file to read")
	f.SetOutput(io.Discard)
	if err := f.Parse(args); err != nil {
		return nil, err
	}
	args = f.Args()

	if len(args) < 1 {
		return nil, new(script.UsageError)
	}
	var input string
	if *path == "" {
		input = state.Stdout()
	} else {
		var err error
		input, err = catFiles(func(yield func(string) bool) {
			yield(*path)
		})
		if err != nil {
			return nil, err
		}
	}
	input, _, _ = strings.Cut(input, "\n")

	for _, varName := range args[:len(args)-1] {
		input = strings.TrimLeftFunc(input, unicode.IsSpace)
		i := strings.IndexFunc(input, unicode.IsSpace)
		if i < 0 {
			i = len(input)
		}
		state.Setenv(varName, input[:i])
		input = input[i:]
		// Trim whitespace before final variable.
		input = strings.TrimLeftFunc(input, unicode.IsSpace)
	}

	state.Setenv(args[len(args)-1], input)
	return nil, nil
}

func catFiles(paths iter.Seq[string]) (string, error) {
	sb := new(strings.Builder)
	for path := range paths {
		f, err := os.Open(path)
		if err != nil {
			return sb.String(), err
		}
		_, err = io.Copy(sb, f)
		f.Close()
		if err != nil {
			return sb.String(), err
		}
	}
	return sb.String(), nil
}

func splitLines(s string) []string {
	lines := strings.SplitAfter(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

const exeModeVar = "DELTAT_SCRIPTTEST"

func TestMain(m *testing.M) {
	if os.Getenv(exeModeVar) == "1" {
		main()
	} else {
		m.Run()
	}
}
