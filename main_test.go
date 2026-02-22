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
	"io"
	"os"
	"slices"
	"sync/atomic"
	"testing"

	"zombiezen.com/go/log"
	"zombiezen.com/go/log/testlog"
)

func TestLookupEnv(t *testing.T) {
	tests := []struct {
		environ []string
		key     string
		want    string
		wantOK  bool
	}{
		{
			environ: nil,
			key:     "FOO",
			want:    "",
			wantOK:  false,
		},
		{
			environ: []string{"BAZ=quux"},
			key:     "FOO",
			want:    "",
			wantOK:  false,
		},
		{
			environ: []string{"FOO=bar"},
			key:     "FOO",
			want:    "bar",
			wantOK:  true,
		},
		{
			environ: []string{"FOO="},
			key:     "FOO",
			want:    "",
			wantOK:  true,
		},
		{
			environ: []string{"FOO=bar", "FOO=baz"},
			key:     "FOO",
			want:    "baz",
			wantOK:  true,
		},
	}

	for _, test := range tests {
		env := &processEnvironment{environ: test.environ}
		got, ok := env.lookupEnv(test.key)
		if got != test.want || ok != test.wantOK {
			t.Errorf("(&processEnvironment{environ: %q}).lookupEnv(%q) = %q, %t; want %q; %t",
				test.environ, test.key, got, ok, test.want, test.wantOK)
		}
	}
}

func TestJoinSeq(t *testing.T) {
	tests := []struct {
		words       []string
		sep         string
		conjunction string
		want        string
	}{
		{
			words:       []string{},
			sep:         ", ",
			conjunction: "and",
			want:        "",
		},
		{
			words:       []string{"foo"},
			sep:         ", ",
			conjunction: "and",
			want:        "foo",
		},
		{
			words:       []string{"foo", "bar"},
			sep:         ", ",
			conjunction: "and",
			want:        "foo and bar",
		},
		{
			words:       []string{"foo", "bar", "baz"},
			sep:         ", ",
			conjunction: "and",
			want:        "foo, bar, and baz",
		},

		{
			words: []string{"foo"},
			sep:   ", ",
			want:  "foo",
		},
		{
			words: []string{"foo", "bar"},
			sep:   ", ",
			want:  "foo, bar",
		},
		{
			words: []string{"foo", "bar", "baz"},
			sep:   ", ",
			want:  "foo, bar, baz",
		},
		{
			words: []string{"foo", "bar", "baz"},
			want:  "foobarbaz",
		},
		{
			words:       []string{"foo", "bar", "baz"},
			conjunction: "or",
			want:        "foobaror baz",
		},
	}

	for _, test := range tests {
		got := joinSeq(slices.Values(test.words), test.sep, test.conjunction)
		if got != test.want {
			t.Errorf("joinSeq(slices.Values(%#v), %q, %q) = %q; want %q", test.words, test.sep, test.conjunction, got, test.want)
		}
	}
}

type logStateKey struct{}

type logState struct {
	output    io.Writer
	hideDebug atomic.Bool
}

type testLogger struct{}

func (testLogger) Log(ctx context.Context, e log.Entry) {
	if state, _ := ctx.Value(logStateKey{}).(*logState); state != nil {
		newLogger(state.output, !state.hideDebug.Load()).Log(ctx, e)
	} else {
		testlog.Logger{}.Log(ctx, e)
	}
}

func (testLogger) LogEnabled(e log.Entry) bool {
	return true
}

func TestMain(m *testing.M) {
	log.SetDefault(testLogger{})
	os.Exit(m.Run())
}
