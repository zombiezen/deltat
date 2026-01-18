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
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sync"

	"github.com/google/uuid"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/ext/refunc"
	"zombiezen.com/go/sqlite/sqlitemigration"
	"zombiezen.com/go/sqlite/sqlitex"
)

const timestampLayout = "2006-01-02T15:04:05"

//go:embed sql/*/*.sql
var rawSQLFiles embed.FS

var sqlFiles = sync.OnceValue(func() fs.FS {
	sub, err := fs.Sub(rawSQLFiles, "sql")
	if err != nil {
		panic(err)
	}
	return sub
})

var schema = sync.OnceValue(func() sqlitemigration.Schema {
	result := sqlitemigration.Schema{
		AppID: 0x2d2646b4,
	}
	files := sqlFiles()
	for i := 1; ; i++ {
		migration, err := fs.ReadFile(files, fmt.Sprintf("schema/%02d.sql", i))
		if errors.Is(err, fs.ErrNotExist) {
			break
		}
		if err != nil {
			panic(err)
		}
		result.Migrations = append(result.Migrations, string(migration))
	}
	return result
})

func prepareConn(conn *sqlite.Conn) error {
	if err := sqlitex.ExecuteTransient(conn, "PRAGMA auto_vacuum = full;", nil); err != nil {
		return err
	}
	if err := sqlitex.ExecuteTransient(conn, "PRAGMA foreign_keys = on;", nil); err != nil {
		return err
	}

	if err := refunc.Register(conn); err != nil {
		return err
	}

	// uuid(TEXT) -> BLOB | NULL
	// Parse UUID, returning NULL if it does not represent a valid UUID.
	err := conn.CreateFunction("uuid", &sqlite.FunctionImpl{
		NArgs:         1,
		Deterministic: true,
		AllowIndirect: true,
		Scalar: func(ctx sqlite.Context, args []sqlite.Value) (sqlite.Value, error) {
			u, err := uuid.Parse(args[0].Text())
			if err != nil {
				return sqlite.Value{}, nil
			}
			return sqlite.BlobValue(u[:]), nil
		},
	})
	if err != nil {
		return err
	}
	// uuid7() -> BLOB
	// Generate a new version 7 UUID.
	err = conn.CreateFunction("uuid7", &sqlite.FunctionImpl{
		NArgs:         0,
		Deterministic: false,
		Scalar: func(ctx sqlite.Context, args []sqlite.Value) (sqlite.Value, error) {
			u, err := uuid.NewV7()
			if err != nil {
				return sqlite.Value{}, nil
			}
			return sqlite.BlobValue(u[:]), nil
		},
	})
	if err != nil {
		return err
	}
	// uuidhex(any) -> TEXT | NULL
	// Format UUID in canonical dash-separated lower hex format.
	// If argument is not a BLOB, it is converted to TEXT and parsing is attempted.
	// If parsing fails or the argument is a BLOB with a length other than 16,
	// then uuidhex returns NULL.
	err = conn.CreateFunction("uuidhex", &sqlite.FunctionImpl{
		NArgs:         1,
		Deterministic: true,
		AllowIndirect: true,
		Scalar: func(ctx sqlite.Context, args []sqlite.Value) (sqlite.Value, error) {
			var u uuid.UUID
			switch args[0].Type() {
			case sqlite.TypeBlob:
				b := args[0].Blob()
				if len(b) != len(u) {
					return sqlite.Value{}, nil
				}
				copy(u[:], b)
			default:
				var err error
				u, err = uuid.Parse(args[0].Text())
				if err != nil {
					return sqlite.Value{}, nil
				}
			}
			return sqlite.TextValue(u.String()), nil
		},
	})
	if err != nil {
		return err
	}

	return nil
}
