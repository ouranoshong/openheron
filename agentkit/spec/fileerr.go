package spec

import "errors"

// FileError says which file a failure is about.
//
// ★ The loader used to answer with a sentence — "config: parse
//
//	agents/main.md: …" — and the sentence was the only place the path
//	existed. A reader that wants to know *which file* is broken had to
//	take the path out of the text, and a cross-file rule (an agent whose
//	model is not declared) named no path at all: the editor then put that
//	error on the file the user was editing, which is where it did not
//	belong.
//
// ★ The type carries the path and the message separately, and the message
//
//	is unchanged — `Error()` is the inner error's, so every log line and
//	every test that reads one still reads the same sentence.
type FileError struct {
	// Path is relative to the configuration root.
	Path string

	Err error
}

func (e *FileError) Error() string { return e.Err.Error() }

func (e *FileError) Unwrap() error { return e.Err }

// fileErrorPath reports the file an error is about.
//
// ★ It walks the chain rather than looking at the outermost error: the
//
//	loader wraps a per-file failure in "config: parse <path>: …", and the
//	path lives in the wrapper, while the cross-file rules carry their own
//	`FileError` at the bottom. The first one found wins — the outermost
//	file error is the one that names the file the user was editing.
func fileErrorPath(err error) string {
	var fe *FileError
	if errors.As(err, &fe) {
		return fe.Path
	}
	return ""
}
