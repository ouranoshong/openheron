// Package templates holds the files that `openheron init` writes and that
// the configuration editor scaffolds.
//
// ★ It is a **leaf**: it imports nothing of openheron's, so both the
//
//	configuration layer (`agentkit/spec`, which serves the editor) and the
//	command layer (`initcfg`) can depend on it. ★ That is the whole point
//	of the package — the two of them need the **same** files, and before
//	this package existed the only way to share them was to copy them, and
//	two copies of one file's content are two copies that drift.
//
// The files live under files/ so that `go:embed` can reach them: an embed
// can only see the tree of its own package.
package templates

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strings"
)

//go:embed all:files
var files embed.FS

// root is the directory the template files live in, inside the embed.
const root = "files"

// isScaffold reports whether one file is a **scaffold** rather than a file
// to install.
//
// ★ The leading underscore is the mark: `llm/_generic.yaml` is the fallback
//
//	that is chosen and never copied, and `mcps/_example.yaml` is the
//	skeleton a new MCP server starts from. A file whose name starts with
//	`_` is therefore **not** part of the tree that gets written — it is the
//	source of a scaffold.
//
// ★ The rule lives here, in the package that owns the files: the alternative
//
//	is a list of names in the layer that copies them, and a list of names
//	is a list that drifts from the tree it describes.
func isScaffold(name string) bool {
	return strings.HasPrefix(name, "_")
}

// File returns one template's content, by its path under files/.
//
// ★ The path is the one the template is addressed by everywhere else:
//
//	"mcps/_example.yaml", "tools/http.yaml", "llm/openai.yaml".
//
// ★ A scaffold is addressable here like any other file: `File` answers
//
//	"does this exact path exist", and the underscore rule is about what
//	gets **copied**, not about what can be read.
func File(rel string) (string, error) {
	raw, err := files.ReadFile(path.Join(root, rel))
	if err != nil {
		return "", fmt.Errorf("templates: read %s: %w", rel, err)
	}
	return string(raw), nil
}

// Nested returns every template under one directory that holds a **folder
// per entity**, keyed by the path it is written to.
//
// ★ The nested partition is `skills/`: a skill is a folder, and the folder
//
//	name is the skill's name, so the file sits one level deeper than the
//	partition's own directory. A flat reader cannot see it — `Dir` skips
//	the directories — and the editor and `openheron init` need to see it
//	the same way.
//
// ★ The keys keep the whole path ("skills/code-review/SKILL.md"), because
//
//	that is the shape the callers use: they address a template by the path
//	it is written to.
func Nested(dir string) (map[string]string, error) {
	out := map[string]string{}
	entries, err := fs.ReadDir(files, path.Join(root, dir))
	if err != nil {
		return nil, fmt.Errorf("templates: read %s: %w", dir, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub, err := fs.ReadDir(files, path.Join(root, dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("templates: read %s/%s: %w", dir, e.Name(), err)
		}
		for _, f := range sub {
			if f.IsDir() {
				continue
			}
			if isScaffold(f.Name()) {
				continue
			}
			rel := path.Join(dir, e.Name(), f.Name())
			content, err := File(rel)
			if err != nil {
				return nil, err
			}
			out[rel] = content
		}
	}
	return out, nil
}

// Dir returns every template under one directory, keyed by its path.
//
// ★ The keys keep the directory ("tools/http.yaml"), because that is the
//
//	shape the callers use: the editor's manifest and the init plan both
//	address a template by the path it is written to.
//
// ★ A **scaffold** — a file whose name starts with `_` — is not in the
//
//	answer: it is the source of a skeleton, not a file to install, and the
//	callers of this function copy what it returns.
func Dir(dir string) (map[string]string, error) {
	out := map[string]string{}
	entries, err := fs.ReadDir(files, path.Join(root, dir))
	if err != nil {
		return nil, fmt.Errorf("templates: read %s: %w", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if isScaffold(e.Name()) {
			continue
		}
		content, err := File(path.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out[path.Join(dir, e.Name())] = content
	}
	return out, nil
}

// Has reports whether one template exists.
//
// ★ It is an exact-path question, so a **scaffold** is found here too: the
//
//	underscore rule is about what gets copied, and a caller that asks
//	"is llm/_generic.yaml there?" wants to know that it is.
func Has(rel string) bool {
	_, err := files.ReadFile(path.Join(root, rel))
	return err == nil
}

// placeholder is the `{{name}}` or `{{name|fallback}}` form the templates use.
var placeholder = regexp.MustCompile(`\{\{([a-z_]+)(?:\|([^}]*))?\}\}`)

// renderers say how a placeholder's value is written into the file.
//
// ★ There is no renderer for a secret any more: the api key is **derived**
//
//	from the provider's name, so no file carries a reference to it and no
//	template has to hand one out. What is left is the two things a file
//	cannot guess — the model and the endpoint — and both are written
//	verbatim.
var renderers = map[string]func(string) string{
	"model":    func(v string) string { return v },
	"base_url": func(v string) string { return v },
}

// Default returns the value a placeholder carries in the template itself.
//
// ★ It is how a template says "use this unless the caller says otherwise":
//
//	`{{model|deepseek-flash}}` hands out "deepseek-flash".
func Default(content, key string) string {
	for _, g := range placeholder.FindAllStringSubmatch(content, -1) {
		if g[1] == key && g[2] != "" {
			return g[2]
		}
	}
	return ""
}

// Render expands the placeholders of one template.
//
// ★ The value comes first, then the template's own fallback, then the
//
//	caller's. A placeholder with no value at all is an error rather than
//	an empty string: a template that silently loses a field is a
//	configuration that silently loses a field.
func Render(content string, values, fallbacks map[string]string) (string, error) {
	if values == nil {
		values = map[string]string{}
	}
	if fallbacks == nil {
		fallbacks = map[string]string{}
	}
	var missing []string
	out := placeholder.ReplaceAllStringFunc(content, func(m string) string {
		g := placeholder.FindStringSubmatch(m)
		key, fallback := g[1], g[2]
		v := values[key]
		if v == "" {
			v = fallback
		}
		if v == "" {
			v = fallbacks[key]
		}
		render, ok := renderers[key]
		if v == "" || !ok {
			missing = append(missing, key)
			return m
		}
		return render(v)
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("templates: the template needs a value for %q", missing[0])
	}
	return out, nil
}
