package api

import (
	"context"
	"errors"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/ouranoshong/openheron/projects"
)

// codeProjectNotFound is the project-shaped 404.
//
// ★ It is its own code because "no such project" and "no such session" are
//
//	different mistakes, and a client that has to tell them apart should not
//	have to parse a message.
const codeProjectNotFound = "project_not_found"

// projectView is one project as the API shows it.
//
// ★ The session list is **not** here: the client gets the sessions from
//
//	`GET /v2/sessions`, each with its `projectId`, and groups them itself.
//	Repeating the membership in two places would be two answers to "which
//	sessions belong to this project".
type projectView struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Root  string `json:"root"`
	Order int    `json:"order"`
	// SessionCount is how many sessions the project holds. It is a count
	// and not the list: the list is the sessions endpoint's answer.
	SessionCount int `json:"sessionCount"`
}

// projectScope is the project this process focuses on.
//
// ★ It is null in the multi-project mode — the process serves every project —
//
//	and the client reads it to decide **how many groups** it renders.
type projectScope struct {
	ProjectID string `json:"projectId"`
	Name      string `json:"name"`
	Root      string `json:"root"`
}

type projectList struct {
	Items []projectView `json:"items"`
	Scope *projectScope `json:"scope"`
}

// ── GET /v2/projects ──────────────────────────────────────────────────────

func (s *Server) v2Projects(ctx context.Context, c *app.RequestContext) {
	reg := s.projectRegistry()
	if reg == nil {
		s.v2Fail(c, consts.StatusServiceUnavailable, codeInternal,
			"the project registry is not wired", false)
		return
	}
	snap, err := reg.Snapshot(ctx)
	if err != nil {
		s.v2FailInternal(c, err)
		return
	}
	out := projectList{Items: make([]projectView, 0, len(snap.Projects))}
	for _, p := range snap.Projects {
		out.Items = append(out.Items, projectView{
			ID:           p.ID,
			Name:         p.Name,
			Root:         p.Root,
			Order:        p.Order,
			SessionCount: len(p.Sessions),
		})
	}
	out.Scope = s.projectScopeOf(snap)
	c.JSON(consts.StatusOK, out)
}

// projectScopeOf answers "which project does this process focus on".
//
// ★ It reads the **registry**, not a remembered name: the scope is a project
//
//	in the table, and a table the user edited is the truth.
func (s *Server) projectScopeOf(snap *projects.Snapshot) *projectScope {
	id := strings.TrimSpace(s.scopeProjectID)
	if id == "" || snap == nil {
		return nil
	}
	p, ok := snap.Get(id)
	if !ok {
		return nil
	}
	return &projectScope{ProjectID: p.ID, Name: p.Name, Root: p.Root}
}

// ── POST /v2/projects ─────────────────────────────────────────────────────

type projectBody struct {
	Name string `json:"name"`
	Root string `json:"root"`
}

func (s *Server) v2CreateProject(ctx context.Context, c *app.RequestContext) {
	reg := s.projectRegistry()
	if reg == nil {
		s.v2Fail(c, consts.StatusServiceUnavailable, codeInternal,
			"the project registry is not wired", false)
		return
	}
	var body projectBody
	if err := c.Bind(&body); err != nil {
		s.v2Fail(c, consts.StatusBadRequest, codeInvalidBody, "invalid body", false)
		return
	}
	root := strings.TrimSpace(body.Root)
	if root == "" {
		s.v2Fail(c, consts.StatusBadRequest, codeInvalidBody,
			"the project root must not be empty", false)
		return
	}
	p, err := reg.Create(ctx, strings.TrimSpace(body.Name), root)
	if err != nil {
		s.v2FailInternal(c, err)
		return
	}
	c.JSON(consts.StatusCreated, projectView{
		ID:           p.ID,
		Name:         p.Name,
		Root:         p.Root,
		Order:        p.Order,
		SessionCount: len(p.Sessions),
	})
}

// ── PATCH /v2/projects/{id} ───────────────────────────────────────────────

type projectPatchBody struct {
	Name  *string `json:"name"`
	Root  *string `json:"root"`
	Order *int    `json:"order"`
}

func (s *Server) v2UpdateProject(ctx context.Context, c *app.RequestContext) {
	reg := s.projectRegistry()
	if reg == nil {
		s.v2Fail(c, consts.StatusServiceUnavailable, codeInternal,
			"the project registry is not wired", false)
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		s.v2Fail(c, consts.StatusBadRequest, codeInvalidBody,
			"the project id must not be empty", false)
		return
	}
	var body projectPatchBody
	if err := c.Bind(&body); err != nil {
		s.v2Fail(c, consts.StatusBadRequest, codeInvalidBody, "invalid body", false)
		return
	}
	p, err := reg.Update(ctx, id, projects.Patch{
		Name:  body.Name,
		Root:  body.Root,
		Order: body.Order,
	})
	if err != nil {
		if errors.Is(err, projects.ErrNotFound) {
			s.v2Fail(c, consts.StatusNotFound, codeProjectNotFound, err.Error(), false)
			return
		}
		s.v2Fail(c, consts.StatusBadRequest, codeInvalidBody, err.Error(), false)
		return
	}
	c.JSON(consts.StatusOK, projectView{
		ID:           p.ID,
		Name:         p.Name,
		Root:         p.Root,
		Order:        p.Order,
		SessionCount: len(p.Sessions),
	})
}

// ── DELETE /v2/projects/{id} ──────────────────────────────────────────────

func (s *Server) v2DeleteProject(ctx context.Context, c *app.RequestContext) {
	reg := s.projectRegistry()
	if reg == nil {
		s.v2Fail(c, consts.StatusServiceUnavailable, codeInternal,
			"the project registry is not wired", false)
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		s.v2Fail(c, consts.StatusBadRequest, codeInvalidBody,
			"the project id must not be empty", false)
		return
	}
	if err := reg.Delete(ctx, id); err != nil {
		if errors.Is(err, projects.ErrNotFound) {
			s.v2Fail(c, consts.StatusNotFound, codeProjectNotFound, err.Error(), false)
			return
		}
		s.v2FailInternal(c, err)
		return
	}
	// ★ The sessions are not deleted: they fall back to "no project". A
	//   project is a label on a directory, and removing the label must not
	//   remove the work.
	c.JSON(consts.StatusOK, map[string]any{"id": id})
}
