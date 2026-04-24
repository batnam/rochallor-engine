// Package migrations embeds all SQL migration files so they can be used
// programmatically in tests and at startup without needing to resolve
// a filesystem path at runtime.
package migrations

import "embed"

// FS is the embedded filesystem containing all *.sql migration files.
//
//go:embed *.sql
var FS embed.FS
