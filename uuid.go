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
	"errors"

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
