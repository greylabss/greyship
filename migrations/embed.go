// Package migrations embeds the SQL migrations so binaries and tests can apply them.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
