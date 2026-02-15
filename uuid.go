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
	"crypto/rand"
	"errors"
	"time"

	"github.com/go-json-experiment/json/jsontext"
	"github.com/google/uuid"
)

func marshalUUIDTo(enc *jsontext.Encoder, u uuid.UUID) error {
	return enc.WriteToken(jsontext.String(u.String()))
}

func parseUUIDs(slice []string) (uuid.UUIDs, error) {
	result := make(uuid.UUIDs, len(slice))
	var resultError error
	for i, s := range slice {
		var err error
		result[i], err = uuid.Parse(s)
		resultError = errors.Join(resultError, err)
	}
	return result, resultError
}

func newUUIDV7(t time.Time, prev uuid.UUID) uuid.UUID {
	millis := t.UnixMilli()

	if prev.Variant() == uuid.RFC4122 && prev.Version() == 7 {
		if prevMillis := readUUIDV7Timestamp(prev[:]); prevMillis >= millis {
			seq := uint16(prev[6]&0x0f)<<8 | uint16(prev[7])
			if seq < 0xfff {
				seq++
				var u uuid.UUID
				copy(u[:], prev[:6])
				u[6] = 0x70 | (byte(seq>>8) & 0x0f) // version 7 | 4 bits of seq
				u[7] = byte(seq)
				rand.Read(u[8:])            // fill random suffix
				u[8] = (u[8] & 0x3f) | 0x80 // variant 0b10
				return u
			}
			millis = prevMillis + 1
		}
	}

	var u uuid.UUID
	fillUUIDV7Timestamp(u[:6], millis)
	rand.Read(u[6:])
	u[6] = (u[6] & 0x0f) | 0x70 // version 7
	u[8] = (u[8] & 0x3f) | 0x80 // variant 0b10

	return u
}

func readUUIDV7Timestamp(u []byte) int64 {
	_ = u[5] // bounds check
	return int64(u[0])<<40 |
		int64(u[1])<<32 |
		int64(u[2])<<24 |
		int64(u[3])<<16 |
		int64(u[4])<<8 |
		int64(u[5])
}

func fillUUIDV7Timestamp(u []byte, ms int64) {
	_ = u[5] // bounds check
	u[0] = byte(ms >> 40)
	u[1] = byte(ms >> 32)
	u[2] = byte(ms >> 24)
	u[3] = byte(ms >> 16)
	u[4] = byte(ms >> 8)
	u[5] = byte(ms)
}
