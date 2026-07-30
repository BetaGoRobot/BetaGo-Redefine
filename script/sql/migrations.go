package sqlmigrations

import "embed"

// Files is the canonical migration source embedded into the production binary.
// Keeping the SQL here preserves the same raw files for operators and tests.
//
//go:embed *.sql
var Files embed.FS
