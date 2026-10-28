// Package webui serves the built React front end from inside the binary.
package webui

import (
	"context"
	"io/fs"
	"mime"
	"path"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// Available reports whether assets contains a real build.
func Available(assets fs.FS) bool {
	_, err := fs.Stat(assets, "index.html")
	return err == nil
}

var apiPrefixes = []string{"/v2", "/sessions", "/agents", "/config", "/health", "/runtime"}

// looksLikeAPI reports whether path belongs to the HTTP API surface.
func looksLikeAPI(path string) bool {
	for _, p := range apiPrefixes {
		if path == strings.TrimSuffix(p, "/") || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

// Handler returns the SPA handler serving assets under prefix.
func Handler(assets fs.FS, prefix string) (app.HandlerFunc, error) {
	index, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		return nil, err
	}

	return func(_ context.Context, c *app.RequestContext) {
		full := string(c.Request.Path())
		rel := strings.TrimPrefix(strings.TrimPrefix(full, prefix), "/")
		if serveFile(c, assets, rel) {
			return
		}
		if looksLikeAPI(full) {
			c.SetStatusCode(consts.StatusNotFound)
			c.SetContentType("application/json; charset=utf-8")
			c.SetBodyString(`{"error":"no such endpoint","code":"not_found","retryable":false}`)
			return
		}
		c.SetStatusCode(consts.StatusOK)
		c.SetContentType("text/html; charset=utf-8")
		c.SetBodyString(string(index))
	}, nil
}

// serveFile writes rel from assets and reports whether it existed.
func serveFile(c *app.RequestContext, assets fs.FS, rel string) bool {
	if rel == "" || strings.HasSuffix(rel, "/") {
		return false
	}
	b, err := fs.ReadFile(assets, path.Clean(rel))
	if err != nil {
		return false
	}
	ctype := mime.TypeByExtension(path.Ext(rel))
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	c.SetStatusCode(consts.StatusOK)
	c.SetContentType(ctype)
	c.SetBodyString(string(b))
	return true
}
