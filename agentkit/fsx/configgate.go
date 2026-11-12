package fsx

// ConfigGate is the door a write that lands inside the configuration root
// goes through.
//
// ★ The filesystem does not know what a configuration file is: it asks this
//
//	interface, and the answer comes from the caller. That keeps the package
//	free of any openheron dependency — it is a leaf backend, and it stays
//	one (see docs/tools-design.md).
type ConfigGate interface {
	// OwnsPath reports whether this absolute path is a configuration file.
	//
	// ★ It is asked with an **absolute, symlink-resolved** path: the
	//   containment check already ran, so the answer may assume the path is
	//   inside the writable roots.
	OwnsPath(absPath string) bool

	// ReadConfig returns a configuration file's content and revision, the
	// way the configuration service sees it.
	//
	// ★ The revision is what the write will be based on, so it has to come
	//   from the same place the write goes — one read, one revision, one
	//   truth.
	ReadConfig(absPath string) (content []byte, rev string, err error)

	// WriteConfig writes it back. baseRev is the revision the content was
	// based on; an empty one means "whatever is on disk now".
	//
	// ★ The checks live on the other side of this call: the caller is the
	//   configuration service, and what a configuration file may contain
	//   (it must parse, the tree must still hold together, no secret may
	//   land in it) is that service's business.
	WriteConfig(absPath string, content []byte, baseRev string) error
}
