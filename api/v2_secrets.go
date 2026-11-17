package api

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

// The secret surface: two namespaces, one shape.
//
// ★ The groups **are** the namespaces: "llm" holds the model keys, "mcp"
// holds the servers' tokens, and a path segment says which one a request is
// about. That is the whole reason the name prefixes exist — the effect of
// "delete everything" is written into the path, so it cannot spill into the
// other group.
//
// ★ Nothing here ever returns a value. The interface shows **where** a
// secret is and **whether** it is in force; the value stays in the keyring
// and in the process.

const (
	secretGroupLLM = spec.SecretGroupLLM
	secretGroupMCP = spec.SecretGroupMCP

	codeSecretUnavailable = "secret_unavailable"
)

// secretGroupView is one namespace and its entries.
type secretGroupView struct {
	ID    string           `json:"id"`
	Label string           `json:"label"`
	Items []secretItemView `json:"items"`
}

// secretItemView is one entity's secret.
//
// ★ `stored` and `effective` are two different questions: "is there a key in
// the keyring" and "which key is in force". They disagree exactly when the
// environment overrides the stored one, and that disagreement is the thing
// the user has to see.
type secretItemView struct {
	Name      string `json:"name"`
	Stored    bool   `json:"stored"`
	Source    string `json:"source"`
	Effective string `json:"effective"`
	Shadowed  bool   `json:"shadowed"`
	EnvVar    string `json:"envVar"`
}

type secretListView struct {
	Groups []secretGroupView `json:"groups"`
}

type secretWriteBody struct {
	Value string `json:"value"`
}

type secretWriteResult struct {
	Group     string `json:"group"`
	Name      string `json:"name"`
	Stored    bool   `json:"stored"`
	Source    string `json:"source"`
	Effective string `json:"effective"`
	Shadowed  bool   `json:"shadowed"`
	EnvVar    string `json:"envVar"`
}

type secretDeleteResult struct {
	Group   string `json:"group"`
	Name    string `json:"name,omitempty"`
	Deleted int    `json:"deleted"`
}

// ── GET /v2/secrets ────────────────────────────────────────────────────────

// v2Secrets lists the secrets of both groups.
//
// ★ The list is driven by the **configuration**: the entries are the
// providers and the MCP servers that exist. A secret whose entity is gone
// is invisible — the keyring cannot be enumerated, and a leftover entry is
// a fact about the past that the tree no longer knows.
func (s *Server) v2Secrets(_ context.Context, c *app.RequestContext) {
	cfg := s.requireConfig(c)
	if cfg == nil {
		return
	}
	store := s.secretStore()
	out := secretListView{Groups: []secretGroupView{
		{ID: secretGroupLLM, Label: "Providers", Items: s.llmSecretItems(cfg, store)},
		{ID: secretGroupMCP, Label: "MCP servers", Items: s.mcpSecretItems(cfg, store)},
	}}
	c.JSON(consts.StatusOK, out)
}

func (s *Server) llmSecretItems(cfg *spec.Config, store spec.SecretStore) []secretItemView {
	ids := make([]string, 0, len(cfg.Providers))
	for id := range cfg.Providers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]secretItemView, 0, len(ids))
	for _, id := range ids {
		out = append(out, secretItemViewFor(id, spec.LLMEnvName(id), store))
	}
	return out
}

func (s *Server) mcpSecretItems(cfg *spec.Config, store spec.SecretStore) []secretItemView {
	names := make([]string, 0, len(cfg.MCPs))
	for name := range cfg.MCPs {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]secretItemView, 0, len(names))
	for _, name := range names {
		out = append(out, secretItemViewFor(name, spec.MCPEnvName(name), store))
	}
	return out
}

func secretItemViewFor(name, envVar string, store spec.SecretStore) secretItemView {
	st := spec.StatusFor(store, envVar)
	return secretItemView{
		Name:      name,
		Stored:    st.Stored,
		Source:    st.StoredSource,
		Effective: st.Effective,
		Shadowed:  st.Shadowed,
		EnvVar:    envVar,
	}
}

// ── PUT /v2/secrets/:group/:name ──────────────────────────────────────────

// v2SecretWrite stores one secret.
//
// ★ The value goes to the keyring and nowhere else: not into the
// configuration tree, not into a log line, not back to the caller.
//
// ★ The response says which source is **in force**, not only what was
// stored: with the environment first, a stored key can be shadowed by a
// variable the user exported an hour ago, and a write surface that says
// only "stored" leaves them believing they changed something.
func (s *Server) v2SecretWrite(_ context.Context, c *app.RequestContext) {
	group, name, ok := s.secretTarget(c)
	if !ok {
		return
	}
	var body secretWriteBody
	if err := c.BindAndValidate(&body); err != nil {
		s.v2Fail(c, consts.StatusBadRequest, codeInvalidBody, "invalid body", false)
		return
	}
	if strings.TrimSpace(body.Value) == "" {
		s.v2Fail(c, consts.StatusBadRequest, codeInvalidBody, "the value is empty", false)
		return
	}
	store := s.secretStore()
	if store == nil {
		s.v2Fail(c, consts.StatusServiceUnavailable, codeSecretUnavailable,
			"the keyring is not available on this machine", false)
		return
	}
	if err := store.Set(secretUser(group, name), body.Value); err != nil {
		s.v2Fail(c, consts.StatusInternalServerError, codeSecretUnavailable,
			"the keyring refused the value: "+err.Error(), false)
		return
	}
	// ★ The key is not in the configuration tree, so the watcher's poll
	//   would never notice that it changed. The mark is what makes "the
	//   next turn uses it" a promise, exactly as it is for a file that
	//   changed.
	s.markSecret(group, name)
	c.JSON(consts.StatusOK, secretResult(group, name, store))
}

// ── DELETE /v2/secrets/:group/:name ───────────────────────────────────────

// v2SecretDelete removes one secret.
//
// ★ Deleting a secret that is not there is not an error: the caller wanted
//
//	it gone, and it is gone. The answer says how many were removed, so a
//	silent nothing is visible.
func (s *Server) v2SecretDelete(_ context.Context, c *app.RequestContext) {
	group, name, ok := s.secretTarget(c)
	if !ok {
		return
	}
	store := s.secretStore()
	if store == nil {
		s.v2Fail(c, consts.StatusServiceUnavailable, codeSecretUnavailable,
			"the keyring is not available on this machine", false)
		return
	}
	deleted := 0
	err := store.Delete(secretUser(group, name))
	switch {
	case err == nil:
		deleted = 1
	case errors.Is(err, spec.ErrSecretNotFound):
	default:
		s.v2Fail(c, consts.StatusInternalServerError, codeSecretUnavailable,
			"the keyring refused the deletion: "+err.Error(), false)
		return
	}
	s.markSecret(group, name)
	c.JSON(consts.StatusOK, secretDeleteResult{Group: group, Name: name, Deleted: deleted})
}

// ── DELETE /v2/secrets/:group ─────────────────────────────────────────────

// v2SecretGroupDelete removes every secret of one group.
//
// ★ The blast radius is the group, and the group is in the path: a request
//
//	for "llm" cannot touch the MCP servers' tokens. That is the whole
//	point of the two namespaces.
//
// ★ It removes the secrets of the entities that **exist**. An entry whose
//
//	entity is gone stays where it is — the keyring cannot be enumerated, so
//	there is no way to know it is there.
func (s *Server) v2SecretGroupDelete(_ context.Context, c *app.RequestContext) {
	group := strings.TrimSpace(c.Param("group"))
	if !validSecretGroup(group) {
		s.v2Fail(c, consts.StatusNotFound, codeInvalidBody,
			"the group must be llm or mcp", false)
		return
	}
	cfg := s.requireConfig(c)
	if cfg == nil {
		return
	}
	store := s.secretStore()
	if store == nil {
		s.v2Fail(c, consts.StatusServiceUnavailable, codeSecretUnavailable,
			"the keyring is not available on this machine", false)
		return
	}
	deleted := 0
	for _, name := range s.secretEntities(group, cfg) {
		err := store.Delete(secretUser(group, name))
		switch {
		case err == nil:
			deleted++
			s.markSecret(group, name)
		case errors.Is(err, spec.ErrSecretNotFound):
		default:
			s.v2Fail(c, consts.StatusInternalServerError, codeSecretUnavailable,
				"the keyring refused the deletion: "+err.Error(), false)
			return
		}
	}
	c.JSON(consts.StatusOK, secretDeleteResult{Group: group, Deleted: deleted})
}

// ── helpers ───────────────────────────────────────────────────────────────

// secretStore returns the store the secrets live in, or nil when there is
// none to reach.
//
// ★ Nil is not "no keyring": it is "this server has no store wired", and
//
//	the caller turns it into a 503 rather than pretending the tree is
//	empty.
func (s *Server) secretStore() spec.SecretStore {
	if s.cfgSvc == nil {
		return nil
	}
	return s.cfgSvc.SecretStore()
}

// secretTarget reads the group and the name out of the path.
//
// ★ The name is checked against the shape its namespace allows, because the
//
//	name becomes a keyring user and an environment variable: a name with a
//	slash in it would be neither.
func (s *Server) secretTarget(c *app.RequestContext) (group, name string, ok bool) {
	group = strings.TrimSpace(c.Param("group"))
	name = strings.TrimSpace(c.Param("name"))
	if !validSecretGroup(group) {
		s.v2Fail(c, consts.StatusNotFound, codeInvalidBody, "the group must be llm or mcp", false)
		return "", "", false
	}
	switch group {
	case secretGroupLLM:
		if !decl.ValidProviderName(name) {
			s.v2Fail(c, consts.StatusBadRequest, codeInvalidBody,
				"the provider name must match "+decl.ProviderNamePattern, false)
			return "", "", false
		}
	case secretGroupMCP:
		if !decl.ValidMCPServerName(name) {
			s.v2Fail(c, consts.StatusBadRequest, codeInvalidBody,
				"the server name must match "+decl.MCPServerNamePattern, false)
			return "", "", false
		}
	}
	return group, name, true
}

func validSecretGroup(group string) bool {
	return group == secretGroupLLM || group == secretGroupMCP
}

// secretEnvName is the environment variable that overrides a secret.
//
// ★ It is the spec's own answer: the command line (`openheron secret`) and
//
//	this surface say the same thing about the same entity, and they must
//	not be able to disagree about the name.
func secretEnvName(group, name string) string {
	return spec.SecretEnvName(group, name)
}

// secretUser is the keyring user that holds a secret.
func secretUser(group, name string) string {
	return spec.SecretUser(group, name)
}

// secretEntities are the entities of one group, sorted.
func (s *Server) secretEntities(group string, cfg *spec.Config) []string {
	var names []string
	if group == secretGroupMCP {
		for name := range cfg.MCPs {
			names = append(names, name)
		}
	} else {
		for id := range cfg.Providers {
			names = append(names, id)
		}
	}
	sort.Strings(names)
	return names
}

// secretResult reports the state of one secret after a write.
func secretResult(group, name string, store spec.SecretStore) secretWriteResult {
	envVar := secretEnvName(group, name)
	st := spec.StatusFor(store, envVar)
	return secretWriteResult{
		Group:     group,
		Name:      name,
		Stored:    st.Stored,
		Source:    st.StoredSource,
		Effective: st.Effective,
		Shadowed:  st.Shadowed,
		EnvVar:    envVar,
	}
}

// markSecret tells the configuration that a secret changed.
//
// ★ It marks the file that **names** the entity. The file did not change —
//
//	the key did — but the tree it stands for did, and that is what the mark
//	means: "look again before the next turn".
func (s *Server) markSecret(group, name string) {
	if s.cfgSvc == nil {
		return
	}
	s.cfgSvc.MarkSecret(group, name)
}
