package db

import (
	// modernc.org/sqlite is a pure-Go sqlite implementation, so the panel stays a
	// single CGO-free binary that cross-compiles to any target.
	_ "modernc.org/sqlite"

	// The postgres driver is registered so DB_DRIVER=postgres works on the same
	// binary. It costs a few hundred kilobytes of code but keeps the escape hatch
	// free for anyone who needs it.
	_ "github.com/jackc/pgx/v5/stdlib"
)
