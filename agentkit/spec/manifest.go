package spec

// Manifest is the configuration surface as one value: the partitions, the
// files that exist in them, the template a new file starts from, and the
// vocabulary the editor offers.
//
// ★ It lives here, and not in the HTTP layer that first needed it, because
// three callers ask the same question and must get one answer:
// `/v2/settings`, `openheron init`, and the `config_*` tools an agent can
// call. Assembled in the handler, the other two would each grow their own
// copy — and a copy of "which files are there" is exactly how `init` and the
// editor drift apart.
//
// ★ The fields carry `json` tags because this value *is* a view: the config
// layer is the only thing that knows the answer, and restating its shape in
// a second struct would be a second thing to update. Same reason
// `build.SkillInfo` is tagged where it is defined.
type Manifest struct {
	Categories []ManifestCategory `json:"categories"`
	Items      []ManifestItem     `json:"items"`

	// Templates is keyed two ways, on purpose: by a partition's directory
	// ("agents") for the skeleton of a section, and by a file's relative
	// path ("agents/main.md") for the files that ship as templates.
	//
	// ★ The per-file entry wins over the section entry: a section skeleton
	// can be nothing but comments, and a file built from it would hold no
	// YAML document at all.
	Templates map[string]string `json:"templates"`

	ModelRefs       []string            `json:"modelRefs"`
	ReasoningLevels map[string][]string `json:"reasoningLevels"`

	// ConfigVersion is the fingerprint of the hot partitions: what a client
	// echoes back so the answer it shows matches the tree on disk.
	ConfigVersion string `json:"configVersion"`
}

// ManifestCategory is one partition of the configuration root.
type ManifestCategory struct {
	ID       string `json:"id"`
	Editable bool   `json:"editable"`
	Hot      bool   `json:"hot"`

	// Note explains a category the editor must not write. It is copy that
	// belongs to the configuration layer — "not hot-reloaded, so a change
	// needs a restart" is a fact about this tree, not about the interface
	// that happens to display it.
	Note string `json:"note,omitempty"`
}

// ManifestItem is one file of a partition.
type ManifestItem struct {
	Path     string `json:"path"`
	Exists   bool   `json:"exists"`
	Hot      bool   `json:"hot"`
	Revision string `json:"revision,omitempty"`
}

// Manifest assembles the configuration surface.
//
// live is the configuration the process is running, and it is read for two
// things only: the model references a new agent file may name, and the
// reasoning vocabulary the model picker offers. Both are live values — the
// tree on disk may hold a file the running process has not loaded yet — so
// they cannot be read from disk here. A nil live is legal and yields empty
// vocabulary, which is what a process with no configuration has.
func (s *Service) Manifest(live *Config) (*Manifest, error) {
	refs := live.ModelRefs()

	m := &Manifest{
		Categories:      make([]ManifestCategory, 0, len(Categories)),
		Items:           []ManifestItem{},
		Templates:       map[string]string{},
		ModelRefs:       refs,
		ReasoningLevels: live.ReasoningLevels(),
		ConfigVersion:   s.Fingerprint(),
	}

	for _, cat := range Categories {
		view := ManifestCategory{ID: cat.Dir, Editable: cat.Editable, Hot: cat.Hot()}
		if !cat.Editable {
			view.Note = "not hot-reloaded; changes apply on restart"
		}
		m.Categories = append(m.Categories, view)
		m.Templates[cat.Dir] = Skeleton(cat.Dir, refs)
		for rel, tpl := range Templates(cat.Dir) {
			m.Templates[rel] = tpl
		}

		names, err := s.ListFiles(cat)
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			rel := cat.Dir + "/" + name
			item := ManifestItem{Path: rel, Exists: true, Hot: cat.Hot()}
			if _, rev, err := s.ReadFile(rel); err == nil {
				item.Revision = rev
			}
			m.Items = append(m.Items, item)
		}
	}
	return m, nil
}
