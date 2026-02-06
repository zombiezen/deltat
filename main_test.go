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
	"slices"
	"strings"
	"testing"
)

func TestWriteFZFActionWithArgument(t *testing.T) {
	tests := []struct {
		action string
		arg    string
		want   string
	}{
		{
			action: "reload",
			arg:    "",
			want:   "reload()",
		},
		{
			action: "change-prompt",
			arg:    "NewPrompt> ",
			want:   "change-prompt(NewPrompt> )",
		},
		{
			action: "preview",
			arg:    "cat {}",
			want:   "preview(cat {})",
		},
		{
			action: "execute",
			arg:    "cat ()",
			want:   "execute[cat ()]",
		},
		{
			action: "execute",
			arg:    "cat () []",
			want:   "execute{cat () []}",
		},
		{
			action: "execute",
			arg:    "cat () [] {}",
			want:   "execute<cat () [] {}>",
		},
	}

	for _, test := range tests {
		sb := new(strings.Builder)
		if err := writeFZFActionWithArgument(sb, test.action, test.arg); err != nil {
			t.Errorf("writeFZFActionWithArgument(%q, %q): %v", test.action, test.arg, err)
			continue
		}
		got := sb.String()
		if got != test.want {
			t.Errorf("writeFZFActionWithArgument(%q, %q) wrote %q; want %q",
				test.action, test.arg, got, test.want)
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
