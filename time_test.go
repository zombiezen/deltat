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
	"testing"
	"time"

	"zombiezen.com/go/gregorian"
)

func TestParseDate(t *testing.T) {
	refLocation := time.FixedZone("America/Los_Angeles", -8*int(time.Hour/time.Second))
	refTime := time.Date(2026, time.January, 23, 8, 30, 0, 0, refLocation)

	tests := []struct {
		now  time.Time
		s    string
		want gregorian.Date
		err  bool
	}{
		{
			now: refTime,
			s:   "",
			err: true,
		},
		{
			now: refTime,
			s:   "  \t ",
			err: true,
		},
		{
			now:  refTime,
			s:    "Jan 22",
			want: gregorian.NewDate(2026, time.January, 22),
		},
		{
			now:  refTime,
			s:    "jan 22",
			want: gregorian.NewDate(2026, time.January, 22),
		},
		{
			now:  refTime,
			s:    "January 22",
			want: gregorian.NewDate(2026, time.January, 22),
		},
		{
			now:  refTime,
			s:    "  Jan 22  ",
			want: gregorian.NewDate(2026, time.January, 22),
		},
		{
			now:  refTime,
			s:    "Jan22",
			want: gregorian.NewDate(2026, time.January, 22),
		},
		{
			now:  refTime,
			s:    "August 22",
			want: gregorian.NewDate(2026, time.August, 22),
		},
		{
			now:  refTime,
			s:    "August 22, 2025",
			want: gregorian.NewDate(2025, time.August, 22),
		},
		{
			now:  refTime,
			s:    "2026-01-22",
			want: gregorian.NewDate(2026, time.January, 22),
		},
		{
			now:  refTime,
			s:    "1/23",
			want: gregorian.NewDate(2026, time.January, 23),
		},
		{
			now:  time.Date(2025, time.January, 23, 8, 30, 0, 0, refLocation),
			s:    "1/23",
			want: gregorian.NewDate(2025, time.January, 23),
		},
		{
			now:  refTime,
			s:    "1/23/2025",
			want: gregorian.NewDate(2025, time.January, 23),
		},
	}

	for _, test := range tests {
		got, err := parseDate(test.now, test.s)
		if !got.Equal(test.want) || (err != nil) != test.err {
			if test.err {
				t.Errorf("parseDate(%v, %q) = %v, %v; want _, <error>",
					test.now, test.s, got, err)
			} else {
				t.Errorf("parseDate(%v, %q) = %v, %v; want %v, <nil>",
					test.now, test.s, got, err, test.want)
			}
		}
	}
}

func TestParseTime(t *testing.T) {
	refLocation := time.FixedZone("America/Los_Angeles", -8*int(time.Hour/time.Second))
	refTime := time.Date(2026, time.January, 23, 8, 30, 0, 0, refLocation)

	tests := []struct {
		now  time.Time
		s    string
		want time.Time
		err  bool
	}{
		{
			now: refTime,
			s:   "",
			err: true,
		},
		{
			now: refTime,
			s:   "  \t ",
			err: true,
		},
		{
			now:  refTime,
			s:    "00:00:00",
			want: time.Date(2026, time.January, 23, 0, 0, 0, 0, refLocation),
		},
		{
			now:  refTime,
			s:    "  00:00:00  ",
			want: time.Date(2026, time.January, 23, 0, 0, 0, 0, refLocation),
		},
		{
			now:  refTime,
			s:    "23:59:59",
			want: time.Date(2026, time.January, 23, 23, 59, 59, 0, refLocation),
		},
		{
			now:  refTime,
			s:    "23:59",
			want: time.Date(2026, time.January, 23, 23, 59, 0, 0, refLocation),
		},
		{
			now:  refTime,
			s:    "7:04",
			want: time.Date(2026, time.January, 23, 7, 4, 0, 0, refLocation),
		},
		{
			now:  refTime,
			s:    "07:04",
			want: time.Date(2026, time.January, 23, 7, 4, 0, 0, refLocation),
		},
		{
			now:  refTime,
			s:    "7:04a",
			want: time.Date(2026, time.January, 23, 7, 4, 0, 0, refLocation),
		},
		{
			now:  refTime,
			s:    "7:04am",
			want: time.Date(2026, time.January, 23, 7, 4, 0, 0, refLocation),
		},
		{
			now:  refTime,
			s:    "7:04AM",
			want: time.Date(2026, time.January, 23, 7, 4, 0, 0, refLocation),
		},
		{
			now:  refTime,
			s:    "7:04 AM",
			want: time.Date(2026, time.January, 23, 7, 4, 0, 0, refLocation),
		},
		{
			now:  refTime,
			s:    "19:04",
			want: time.Date(2026, time.January, 23, 19, 4, 0, 0, refLocation),
		},
		{
			now:  refTime,
			s:    "7:04p",
			want: time.Date(2026, time.January, 23, 19, 4, 0, 0, refLocation),
		},
		{
			now:  refTime,
			s:    "7:04pm",
			want: time.Date(2026, time.January, 23, 19, 4, 0, 0, refLocation),
		},
		{
			now:  refTime,
			s:    "7:04PM",
			want: time.Date(2026, time.January, 23, 19, 4, 0, 0, refLocation),
		},
		{
			now:  refTime,
			s:    "7:04 PM",
			want: time.Date(2026, time.January, 23, 19, 4, 0, 0, refLocation),
		},
		{
			now:  refTime,
			s:    "07:04PM",
			want: time.Date(2026, time.January, 23, 19, 4, 0, 0, refLocation),
		},
		{
			now:  refTime,
			s:    "Jan 22 7:04 PM",
			want: time.Date(2026, time.January, 22, 19, 4, 0, 0, refLocation),
		},
		{
			now:  refTime,
			s:    "Jan22 7:04 PM",
			want: time.Date(2026, time.January, 22, 19, 4, 0, 0, refLocation),
		},
		{
			now:  refTime,
			s:    "Jan 22, 7:04 PM",
			want: time.Date(2026, time.January, 22, 19, 4, 0, 0, refLocation),
		},
		{
			now:  refTime,
			s:    "August 22 7:04 PM",
			want: time.Date(2026, time.August, 22, 19, 4, 0, 0, refLocation),
		},
		{
			now:  refTime,
			s:    "August 22 2026 7:04 PM",
			want: time.Date(2026, time.August, 22, 19, 4, 0, 0, refLocation),
		},
		{
			now:  refTime,
			s:    "August 22, 2026 7:04 PM",
			want: time.Date(2026, time.August, 22, 19, 4, 0, 0, refLocation),
		},
		{
			now:  refTime,
			s:    "2026-01-22T19:04",
			want: time.Date(2026, time.January, 22, 19, 4, 0, 0, refLocation),
		},
		{
			now:  refTime,
			s:    "2026-01-22T19:04:06",
			want: time.Date(2026, time.January, 22, 19, 4, 6, 0, refLocation),
		},
	}

	for _, test := range tests {
		got, err := parseTime(test.now, test.s)
		if !got.Equal(test.want) || (err != nil) != test.err {
			if test.err {
				t.Errorf("parseTime(%v, %q) = %v, %v; want _, <error>",
					test.now, test.s, got, err)
			} else {
				t.Errorf("parseTime(%v, %q) = %v, %v; want %v, <nil>",
					test.now, test.s, got, err, test.want)
			}
		}
	}
}
