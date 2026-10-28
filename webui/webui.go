// Package webui serves the built React front end from inside the binary.
//
// 生产分发是单文件：openheron 启动后既开 /v2 接口也开界面，不依赖外部静态目录。
// 开发时不用它 —— 前端由 Vite dev server 提供，并通过代理打到这里，
// 见 docs/frontend-design.md §6。
//
// ★ 这个包自己不 embed，assets 由 main 传进来。
//
//	原因是 go:embed 的模式不能含 `..`，且相对于**本包目录**解析。
//	dist 的真实位置是仓库根下的 web/dist，而 webui 在 webui/（深一层），
//	从这里写 `//go:embed web/dist` 指向的是 webui/web/dist，不是它；
//	写 `//go:embed ../web/dist` 会被 embed 的模式校验直接拒。
//	只有**仓库根的包**（main）能用那种相对路径。
//	把「嵌入」和「服务」拆开，是这两条约束下唯一的分法。
//
//	（★ 早先这包在 internal/webui 下，注释把原因写成「在 internal/ 下」——
//	那是错的：internal 与 embed 无关，把 `internal/webui` 平移成
//	`webui` 之后约束一字未变，变的是包深度。）
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
//
// 用来区分「跑过 make web 的发布二进制」与「只有 .gitkeep 的开发树」，
// 好让启动日志说人话，而不是让人对着一个空首页排查。
func Available(assets fs.FS) bool {
	_, err := fs.Stat(assets, "index.html")
	return err == nil
}

// apiPrefixes 是「看起来像接口」的一级路径段。
//
// ★ 这份名单存在的唯一原因是 SPA 回落：路径在 assets 里找不到就回
//
//	index.html 是前端路由的正常需求，但 /v2/typo 走同一条路就会拿到
//	一份 HTML —— 用户看到的不是 404，而是「接口返回了奇怪的东西」。
//	排查方向会从「接口路径写错了」偏到「后端返回格式不对」，能绕很久。
//
//	★ `/sessions` `/agents` `/config` `/health` `/runtime` 这些**曾经**是接口
//	路径（v1 时代没有前缀），现在它们没有路由了 —— 但仍然挡住：
//	拼错的老接口应该得到一份 JSON 404，而不是一份 HTML。
//	拿到 HTML 的人会以为「接口通了但返回格式不对」，能绕很久。
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
//
// ★ 不用 hertz 的 StaticFS：它的 app.FS 只接受磁盘上的 Root 字符串，
//
//	没有 fs.FS 入口 —— 要用它就得先把 embed 内容落盘到临时目录，
//	而那会重新踩上 fs.go 那个「目录没有 index 页就一律 403」的坑。
//	这里直接读 fs.FS：少一次磁盘往返，也少一个只在生产出现的分支。
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
		// 前端路由：路径在 assets 里不存在时交给 index.html，让 React Router 接管。
		c.SetStatusCode(consts.StatusOK)
		c.SetContentType("text/html; charset=utf-8")
		c.SetBodyString(string(index))
	}, nil
}

// serveFile writes rel from assets and reports whether it existed.
//
// ★ 用 MIME 表而不是 Hertz 的 StaticFS 的自动推断：Vite 产出的
//
//	.js/.css/.svg 需要正确 Content-Type，猜错了浏览器会拒绝执行脚本。
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
