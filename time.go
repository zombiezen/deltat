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
	"time"

	"zombiezen.com/go/gregorian"
)

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
