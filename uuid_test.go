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

	"github.com/google/uuid"
)

func FuzzAppendUUIDText(f *testing.F) {
	f.Add(uuid.Nil[:])
	f.Add(uuid.NameSpaceDNS[:])
	f.Add(uuid.NameSpaceURL[:])
	f.Add(uuid.NameSpaceOID[:])
	f.Add(uuid.NameSpaceX500[:])
	f.Add(uuid.Max[:])

	f.Fuzz(func(t *testing.T, uuidBytes []byte) {
		var u uuid.UUID
		if len(uuidBytes) != len(u) {
			return
		}
		copy(u[:], uuidBytes)

		got := string(appendUUIDText(nil, u))
		want := u.String()
		if got != want {
			t.Errorf("string(appendUUIDText(nil, %v)) = %q; want %q", u, got, want)
		}
	})
}
