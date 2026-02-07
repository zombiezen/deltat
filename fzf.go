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
	"errors"
	"fmt"
	"iter"
	"os"
	"os/exec"
	"strings"

	"zombiezen.com/go/log"
)

type fzfOptions struct {
	// template is the field index expression or template
	// for transforming each item for display.
	// If empty, the item is displayed as-is.
	template string
	// outputTemplate is the field index expression or template
	// for transforming each accepted item.
	// If empty, accepted item(s) are returned as-is.
	outputTemplate string
	// searchScope is the field index expression
	// for limiting search scope
	// on the displayed template.
	searchScope string
	// delimiter is a regular expression for template, outputTemplate, and searchScope
	// field index expressions.
	// If empty, then fields are separated by whitespace.
	delimiter string

	bind string

	multi        bool
	initialQuery string
	select1      bool
}

func (opts *fzfOptions) clone() *fzfOptions {
	opts2 := new(fzfOptions)
	if opts != nil {
		*opts2 = *opts
	}
	return opts2
}

func fzf(ctx context.Context, items iter.Seq[string], opts *fzfOptions) ([]string, error) {
	fzfPath := os.Getenv("DELTAT_FZF")
	if fzfPath == "" {
		fzfPath = "fzf"
	}
	c := exec.CommandContext(ctx,
		fzfPath,
		"--read0",
		"--print0",
		"--no-sort",
	)
	if opts != nil {
		if opts.delimiter != "" {
			c.Args = append(c.Args, "--delimiter="+opts.delimiter)
		}
		if opts.template != "" {
			c.Args = append(c.Args, "--with-nth="+opts.template)
		}
		if opts.outputTemplate != "" {
			c.Args = append(c.Args, "--accept-nth="+opts.outputTemplate)
		}
		if opts.searchScope != "" {
			c.Args = append(c.Args, "--nth="+opts.searchScope)
		}
		if opts.initialQuery != "" {
			c.Args = append(c.Args, "--query="+opts.initialQuery)
		}
		if opts.bind != "" {
			c.Args = append(c.Args, "--bind="+opts.bind)
		}
	}
	if opts != nil && opts.multi {
		c.Args = append(c.Args, "--multi")
	} else {
		c.Args = append(c.Args, "--no-multi")
	}
	if opts != nil && opts.select1 {
		c.Args = append(c.Args, "--select-1")
	} else {
		c.Args = append(c.Args, "--no-select-1")
	}

	stdin, err := c.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("fzf: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer func() {
			stdin.Close()
			close(done)
		}()

		var buf []byte
		for item := range items {
			if strings.Contains(item, "\x00") {
				log.Warnf(ctx, "fzf: invalid item %q", item)
				continue
			}
			buf = buf[:0]
			buf = append(buf, item...)
			buf = append(buf, 0)
			if _, err := stdin.Write(buf); err != nil {
				return
			}
		}
	}()

	output, err := c.Output()
	if err != nil {
		exitCode := -1
		hasStderr := false
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			hasStderr = len(exitError.Stderr) > 0
			exitCode = exitError.ExitCode()
		}
		switch {
		case exitCode == 1 && !hasStderr:
			// Exit code 1 is a common enough exit code that wrappers
			// or other things may return.
			// Only use this error message if there was no additional stderr output.
			err = errFZFNoMatch
		case exitCode == 130:
			err = errFZFCanceled
		default:
			err = fmt.Errorf("fzf: %v", err)
		}
	}
	<-done

	if len(output) == 0 {
		return nil, err
	}
	output = bytes.TrimSuffix(output, []byte{0})
	return strings.Split(string(output), "\x00"), err
}

var (
	errFZFNoMatch  = errors.New("fzf: no match")
	errFZFCanceled = errors.New("fzf: user canceled")
)

// writeFZFActionWithArgument writes "action(...)" to sb with an acceptable delimiter.
// writeFZFActionWithArgument returns an error if arg cannot be escaped properly.
// See the ["ACTION ARGUMENT" section] of `fzf --man` for more details.
//
// ["ACTION ARGUMENT" section]: https://manpages.debian.org/trixie/fzf/fzf.1.en.html#ACTION_ARGUMENT
func writeFZFActionWithArgument(sb *strings.Builder, action, arg string) error {
	const delims = "()[]{}<>~~!!@@##$$%%&&**;;//||"
	for i := 0; i+1 < len(delims); i += 2 {
		startChar := delims[i]
		endChar := delims[i+1]
		if strings.IndexByte(arg, endChar) < 0 {
			sb.WriteString(action)
			sb.WriteByte(startChar)
			sb.WriteString(arg)
			sb.WriteByte(endChar)
			return nil
		}
	}
	return fmt.Errorf("fzf: %s action argument %q is not safe", action, arg)
}
