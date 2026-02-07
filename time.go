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
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"zombiezen.com/go/gregorian"
)

var testTime time.Time

func getNow() time.Time {
	if !testTime.IsZero() {
		return testTime.Local()
	}
	return time.Now()
}

// parseDate parses a wide variety of date formats into a date
// based on the current time and location.
// parseDate is intended to operate on human input,
// and so is fairly loose in its interpretation.
func parseDate(now time.Time, s string) (gregorian.Date, error) {
	p := &stringParser{s: s}
	p.skipSpace()

	if monthName := p.run(isLetter); monthName != "" {
		// Assume that this date is in the format "MonthName Day".
		p.skipSpace()
		dayString := p.run(isDigit)
		if dayString == "" {
			return gregorian.Date{}, fmt.Errorf("parse date %q: missing day", s)
		}
		m, ok := monthNames[strings.ToLower(monthName)]
		if !ok {
			return gregorian.Date{}, fmt.Errorf("parse date %q: invalid month %s", s, monthName)
		}
		n, err := strconv.Atoi(dayString)
		if err != nil {
			return gregorian.Date{}, fmt.Errorf("parse date %q: invalid day %s", s, dayString)
		}
		p.skipSpace()
		p.consume(',')
		p.skipSpace()
		y := now.Year()
		if yearString := p.run(isDigit); yearString != "" {
			p.skipSpace()
			var err error
			y, err = strconv.Atoi(yearString)
			if err != nil {
				return gregorian.Date{}, fmt.Errorf("parse date %q: invalid year %s", s, yearString)
			}
		}
		if p.s != "" {
			return gregorian.Date{}, fmt.Errorf("parse date %q: unexpected characters", s)
		}

		d, err := newStrictDate(y, m, n)
		if err != nil {
			return gregorian.Date{}, fmt.Errorf("parse date %q: %v", s, err)
		}
		return d, nil
	}

	if strings.Count(p.s, "/") == 1 {
		// Assume that this date is in the format "Month/Day".
		monthString := p.run(isDigit)
		if monthString == "" {
			return gregorian.Date{}, fmt.Errorf("parse date %q: invalid month", s)
		}
		m, err := strconv.Atoi(monthString)
		if err != nil || m < 1 || m > 12 {
			return gregorian.Date{}, fmt.Errorf("parse date %q: invalid month %s", s, monthString)
		}

		p.skipSpace()
		if !p.consume('/') {
			return gregorian.Date{}, fmt.Errorf("parse date %q: invalid month", s)
		}
		dayString := p.run(isDigit)
		if dayString == "" {
			return gregorian.Date{}, fmt.Errorf("parse date %q: invalid day", s)
		}
		n, err := strconv.Atoi(dayString)
		if err != nil {
			return gregorian.Date{}, fmt.Errorf("parse date %q: invalid day %s", s, dayString)
		}
		p.skipSpace()
		if p.s != "" {
			return gregorian.Date{}, fmt.Errorf("parse date %q: unexpected characters", s)
		}

		d, err := newStrictDate(now.Year(), time.Month(m), n)
		if err != nil {
			return gregorian.Date{}, fmt.Errorf("parse date %q: %v", s, err)
		}
		return d, nil
	}

	return gregorian.ParseDate(strings.TrimSpace(p.s))
}

func newStrictDate(year int, month time.Month, day int) (gregorian.Date, error) {
	d := gregorian.NewDate(year, month, day)
	if d.Day() != day {
		// Check for overflow or underflow.
		return gregorian.Date{}, fmt.Errorf("invalid day %d", day)
	}
	return d, nil
}

// parseTime parses a wide variety of time formats into a time
// based on the current time and location.
// parseTime is intended to operate on human input,
// and so is fairly loose in its interpretation.
func parseTime(now time.Time, s string) (time.Time, error) {
	trimmed := strings.TrimLeftFunc(s, unicode.IsSpace)
	var datePart, timePart string
	if len(trimmed) > 0 && isLetter(trimmed[0]) {
		// Assume that this time is in the format "MonthName Day [[,] Year] Time".
		p := &stringParser{s: trimmed}
		p.run(isLetter)
		p.skipSpace()
		p.run(isDigit)
		p.skipSpace()
		p.consume(',')
		p.skipSpace()

		// The next run of digits might be the year or the hour.
		// We do a token of lookahead to see whether there's a colon.
		save := p.s
		if x := p.run(isDigit); x != "" {
			p.skipSpace()
			if p.consume(':') {
				// These digits are part of the time.
				p.s = save
			}
		}

		i := len(trimmed) - len(p.s)
		datePart, timePart = trimmed[:i], trimmed[i:]
	} else if i := strings.IndexAny(trimmed, "Tt"); i >= 0 {
		datePart, timePart = trimmed[:i], trimmed[i+1:]
	} else {
		timePart = s
	}

	datePart = strings.TrimSpace(datePart)
	var d gregorian.Date
	if datePart == "" {
		d = gregorian.NewDate(now.Year(), now.Month(), now.Day())
	} else {
		var err error
		d, err = parseDate(now, datePart)
		if err != nil {
			return time.Time{}, err
		}
	}

	timePart = strings.TrimSpace(timePart)
	if timePart == "" {
		return time.Time{}, fmt.Errorf("parse time %q: empty", s)
	}
	timePart, ampm := trimAMPMSuffix(timePart)
	timeNums := strings.SplitN(timePart, ":", 3)
	if len(timeNums) < 2 {
		return time.Time{}, fmt.Errorf("parse time %q: missing HH:MM", s)
	}
	sn := 0
	if len(timeNums) == 3 {
		if strings.Contains(timeNums[2], ":") {
			return time.Time{}, fmt.Errorf("parse time %q: too many time parts", s)
		}
		n, err := strconv.ParseUint(strings.TrimSpace(timeNums[2]), 10, 0)
		if err != nil {
			return time.Time{}, fmt.Errorf("parse time %q: invalid seconds", s)
		}
		if n >= 60 {
			return time.Time{}, fmt.Errorf("parse time %q: seconds must be in range [0,59]", s)
		}
		sn = int(n)
	}

	h := strings.TrimSpace(timeNums[0])
	m := strings.TrimSpace(timeNums[1])
	hn, err := strconv.ParseUint(h, 10, 0)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse time %q: invalid hours", s)
	}
	switch {
	case ampm != 0 && !(1 <= hn || hn <= 12):
		return time.Time{}, fmt.Errorf("parse time %q: hours must be in range [1,12]", s)
	case ampm == 0 && hn >= 24:
		return time.Time{}, fmt.Errorf("parse time %q: hours must be in range [0,23]", s)
	case ampm < 0 && hn == 12:
		hn = 0
	case ampm > 0 && hn < 12:
		hn += 12
	}
	mn, err := strconv.ParseUint(m, 10, 0)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse time %q: invalid minutes", s)
	}
	if mn >= 60 {
		return time.Time{}, fmt.Errorf("parse time %q: minutes must be in range [0,59]", s)
	}

	return time.Date(d.Year(), d.Month(), d.Day(), int(hn), int(mn), sn, 0, now.Location()), nil
}

func trimAMPMSuffix(s string) (string, int) {
	if len(s) == 0 {
		return s, 0
	}
	n := 1
	if len(s) >= 2 {
		if c := s[len(s)-1]; c == 'm' || c == 'M' {
			n = 2
		}
	}
	switch s[len(s)-n] {
	case 'a', 'A':
		s = strings.TrimRightFunc(s[:len(s)-n], unicode.IsSpace)
		return s, -1
	case 'p', 'P':
		s = strings.TrimRightFunc(s[:len(s)-n], unicode.IsSpace)
		return s, 1
	}
	return s, 0
}

func formatDuration(d time.Duration) string {
	totalSeconds := int64(d / time.Second)
	seconds := totalSeconds % 60
	minutes := (totalSeconds / 60) % 60
	hours := totalSeconds / (60 * 60)
	return fmt.Sprintf("%d:%02d:%02d", hours, minutes, seconds)
}

func localDateFromTime(t time.Time) gregorian.Date {
	if t.IsZero() {
		return gregorian.Date{}
	}
	t = t.Local()
	return gregorian.NewDate(t.Year(), t.Month(), t.Day())
}

var monthNames = map[string]time.Month{
	"jan":       time.January,
	"january":   time.January,
	"feb":       time.February,
	"february":  time.February,
	"mar":       time.March,
	"march":     time.March,
	"apr":       time.April,
	"april":     time.April,
	"may":       time.May,
	"jun":       time.June,
	"june":      time.June,
	"jul":       time.July,
	"july":      time.July,
	"aug":       time.August,
	"august":    time.August,
	"sep":       time.September,
	"sept":      time.September,
	"september": time.September,
	"oct":       time.October,
	"october":   time.October,
	"nov":       time.November,
	"november":  time.November,
	"dec":       time.December,
	"december":  time.December,
}

type stringParser struct {
	s string
}

func (p *stringParser) run(f func(c byte) bool) string {
	for i, c := range []byte(p.s) {
		if !f(c) {
			result := p.s[:i]
			p.s = p.s[i:]
			return result
		}
	}
	result := p.s
	p.s = ""
	return result
}

func (p *stringParser) consume(c byte) bool {
	if len(p.s) == 0 || p.s[0] != c {
		return false
	}
	p.s = p.s[1:]
	return true
}

func (p *stringParser) skipSpace() {
	p.s = strings.TrimLeftFunc(p.s, unicode.IsSpace)
}

func isDigit(c byte) bool {
	return '0' <= c && c <= '9'
}

func isLetter(c byte) bool {
	return 'A' <= c && c <= 'Z' ||
		'a' <= c && c <= 'z'
}
