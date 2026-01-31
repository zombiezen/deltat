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
	"testing"
)

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
