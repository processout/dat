package dat

import (
	"time"

	"github.com/processout/dat/common"
)

// Dialect is the active SQLDialect.
var Dialect SQLDialect

// SQLDialect represents a vendor specific SQL dialect.
type SQLDialect interface {
	// WriteStringLiteral writes a string literal.
	WriteStringLiteral(buf common.BufferWriter, value string)
	// WriteIdentifier writes a quoted identifer such as a column or table.
	WriteIdentifier(buf common.BufferWriter, column string)
	// WriteFormattedTime writes a time formatted for the database
	WriteFormattedTime(buf common.BufferWriter, t time.Time)
}
