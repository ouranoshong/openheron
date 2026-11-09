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
)

//go:embed all:files
var files embed.FS

// root is the directory the template files live in, inside the embed.
const root = "files"

// File returns one template's content, by its path under files/.
//
// ★ The path is the one the template is addressed by everywhere else:
//
//	"mcps/example.yaml", "tools/http.yaml", "llm/openai.yaml".
func File(rel string) (string, error) {
	raw, err := files.ReadFile(path.Join(root, rel))
	if err != nil {
		return "", fmt.Errorf("templates: read %s: %w", rel, err)
	}
	return string(raw), nil
}

// Dir returns every template under one directory, keyed by its path.
//
// ★ The keys keep the directory ("tools/http.yaml"), because that is the
//
//	shape the callers use: the editor's manifest and the init plan both
//	address a template by the path it is written to.
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
		content, err := File(path.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out[path.Join(dir, e.Name())] = content
	}
	return out, nil
}

// Has reports whether one template exists.
func Has(rel string) bool {
	_, err := files.ReadFile(path.Join(root, rel))
	return err == nil
}

// placeholder is the `{{name}}` or `{{name|fallback}}` form the templates use.
var placeholder = regexp.MustCompile(`\{\{([a-z_]+)(?:\|([^}]*))?\}\}`)

// renderers say how a placeholder's value is written into the file.
//
// ★ `api_key_env` is the reason the placeholders exist at all: the file
//
//	carries `${NAME}`, not the secret, so the key never lands in the
//	configuration.
var renderers = map[string]func(string) string{
	"model":       func(v string) string { return v },
	"base_url":    func(v string) string { return v },
	"api_key_env": func(v string) string { return "${" + v + "}" },
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
