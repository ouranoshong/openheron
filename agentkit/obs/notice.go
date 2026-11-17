package obs

import (
	"io"
	"log/slog"
)

// NewNotice builds the logger for notices.
//
// ★ A notice is not a log record. The log's destination and level are
//
//	configuration — `log.file` with `log.console: false` moves every record
//	into a file and takes the terminal away, and `log_level` decides what
//	gets said at all — while a notice is what the process says about
//	itself to the terminal it was started on. The address the service
//	serves on is one of those, and it has to be there whatever the
//	configuration says.
//
// ★ The envelope is the log's own: a notice that looked like something
//
//	else on the terminal would be a second shape on the screen.
//
// ★ The level is a private one, fixed at info, and nothing filters it: a
//
//	level is a choice about the log's verbosity, and the address is not
//	part of the log.
func NewNotice(w io.Writer) *slog.Logger {
	return New(w, nil)
}
