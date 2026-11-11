// Package fsx is a bounded fork of github.com/cloudwego/eino-ext/adk/backend/local@v0.2.2
// (Apache-2.0, header retained below).
//
// Why a fork: the upstream Config has exactly one field (ValidateCommand), so there is no
// place to pass a root, an ignore list or a result cap; deep.Config only exposes Backend /
// Shell / StreamingShell, so the middleware's own knobs are unreachable from here too. The
// backend is the only injection point, which is why the bounds live in this file and not in
// a copied middleware — see docs/fs-backend-design.md.
//
// Two different boundaries live here:
//   - writes are confined to Root + ExtraRoots (resolveWrite);
//   - all seven methods are bounded in how far they read (ls/glob caps, grep cap, ctx).
//
// Reads are deliberately NOT confined: execute can already cat any file, so confining
// read_file bought no safety, only "file not found" for a project that lived one directory
// over. The walk bound is what the original accident needed.
//
// Upstream diff to keep in mind when syncing: resolve / inside / ignored / bounded GlobInfo
// and GrepRaw, the caps, and the ctx checks are ours; Read / Write / Edit / the execute
// plumbing are upstream code.

/*
 * Copyright 2026 CloudWeGo Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package fsx

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/cloudwego/eino/adk/filesystem"
	"github.com/cloudwego/eino/schema"
)

// Caps. They are constants, not configuration: nothing has needed a different value yet,
// and a knob nobody turns is a knob nobody tests.
const (
	maxLsEntries   = 200
	maxGlobResults = 200
	maxGlobDepth   = 16
	// One grep result. Upstream has no cap at all on this path, so a one-letter
	// pattern across a build tree could still be read into memory whole.
	maxGrepMatches = 200
	// walkBudget is how long a walk may run.
	//
	// ★ The depth and result caps bound how much comes back; they do not
	//   bound how long the walk takes. A pattern over a mounted volume
	//   visits millions of entries and returns two matches — and the turn
	//   is blocked the whole time, because the tool call is synchronous.
	//   The budget is the bound on the cost, not on the result.
	walkBudget = 10 * time.Second
)

// Junk that must never be walked. The session state directory is deliberately NOT in this
// list: uploaded files live under it and the run prompt points the model there.
var ignoredNames = map[string]bool{
	".git":                      true,
	"node_modules":              true,
	"$RECYCLE.BIN":              true,
	"System Volume Information": true,
}

type Config struct {
	// Root is the project root: relative paths resolve against it and an empty path
	// means it. Writes outside Root + ExtraRoots are refused; reads are not confined
	// (execute can read anything anyway — see the package comment).
	// An empty Root means the process working directory.
	Root string

	// ExtraRoots are writable and are never the default for an empty path.
	// The session workspace (uploads) and the skills directory belong here.
	ExtraRoots []string

	// Shell is the program a command line is handed to: the program and the
	// flags that come before the command. Empty means /bin/sh -c.
	//
	// ★ The decision belongs to the caller, not to this package: the tool's
	//   description tells the model which dialect to write, and the two have
	//   to be the same answer.
	Shell []string

	// ShellPathDir is prepended to the child's PATH. A shell that was found
	// outside PATH (Git for Windows, MSYS2) cannot find its own utilities
	// otherwise: the shell starts, and then `ls` is not a command.
	ShellPathDir string

	ValidateCommand func(string) error
}

type Local struct {
	root            string
	roots           []string
	validateCommand func(string) error
	// shell is the program and the flags; the command line is appended.
	shell []string
	// shellPathDir is prepended to the child's PATH.
	shellPathDir string
	// walkBudget is the per-walk time budget.
	//
	// ★ It is a field so a test can shorten it; production always gets the
	//   package constant.
	walkBudget time.Duration
}

var defaultValidateCommand = func(string) error {
	return nil
}

// New builds the backend. Every root is made absolute and symlink-free once, so the
// containment check compares real paths.
func New(cfg Config) (*Local, error) {
	root := strings.TrimSpace(cfg.Root)
	if root == "" {
		root = "."
	}
	abs, err := canonical(root)
	if err != nil {
		return nil, fmt.Errorf("fsx: root %q: %w", cfg.Root, err)
	}
	roots := []string{abs}
	for _, r := range cfg.ExtraRoots {
		if strings.TrimSpace(r) == "" {
			continue
		}
		extra, err := canonical(r)
		if err != nil {
			return nil, fmt.Errorf("fsx: extra root %q: %w", r, err)
		}
		roots = append(roots, extra)
	}
	validate := defaultValidateCommand
	if cfg.ValidateCommand != nil {
		validate = cfg.ValidateCommand
	}
	shell := cfg.Shell
	if len(shell) == 0 {
		shell = []string{"/bin/sh", "-c"}
	}
	return &Local{
		root:            abs,
		roots:           roots,
		validateCommand: validate,
		shell:           shell,
		shellPathDir:    cfg.ShellPathDir,
		walkBudget:      walkBudget,
	}, nil
}

// canonical resolves absolute paths and symlinks. A missing directory is not an error:
// roots are created by the process (the session directory) and by the user, not here.
func canonical(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real, nil
	}
	return filepath.Clean(abs), nil
}

// resolve turns a request path into an absolute path.
//
// An empty path means the project root (never "/") and a relative path is joined onto the
// root; beyond that it only canonicalises. Reads are not confined on purpose — see the
// package comment. Writes go through resolveWrite, which is where the boundary is.
func (s *Local) resolve(p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return s.root, nil
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(s.root, p)
	}
	p = filepath.Clean(p)
	if real, err := filepath.EvalSymlinks(p); err == nil {
		p = real
	}
	return p, nil
}

// resolveWrite is the write boundary: the target, or the deepest ancestor that exists,
// must resolve inside Root or an ExtraRoot. The ancestor walk is needed because the file
// itself may not exist yet, so EvalSymlinks cannot resolve it.
//
// ★ Only "this component does not exist" moves the walk up. Any other failure — permission,
// I/O, a symlink loop — is a directory we cannot inspect, and treating that as "no symlink
// here" would be exactly the hole the walk exists to close. (The first version did: it
// returned the unchecked lexical path.)
func (s *Local) resolveWrite(p string) (string, error) {
	abs, err := s.resolve(p)
	if err != nil {
		return "", err
	}
	if !s.inside(abs) {
		return "", fmt.Errorf("path %q is outside the project root %q", abs, s.root)
	}
	for dir := filepath.Dir(abs); ; dir = filepath.Dir(dir) {
		real, err := filepath.EvalSymlinks(dir)
		if err == nil {
			if !s.inside(real) {
				return "", fmt.Errorf("path %q resolves outside the project root %q", abs, s.root)
			}
			return abs, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("cannot verify that %q is inside %q: %w", abs, s.root, err)
		}
		if filepath.Dir(dir) == dir {
			return abs, nil
		}
	}
}

func (s *Local) inside(p string) bool {
	for _, root := range s.roots {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			continue
		}
		if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func (s *Local) ignored(d os.DirEntry) bool {
	return ignoredNames[d.Name()]
}

func (s *Local) LsInfo(ctx context.Context, req *filesystem.LsInfoRequest) ([]filesystem.FileInfo, error) {
	path, err := s.resolve(req.Path)
	if err != nil {
		return nil, err
	}

	// os.ReadDir sorts by file name already; upstream sorted a second time.
	entries, err := os.ReadDir(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		if os.IsPermission(err) {
			return nil, fmt.Errorf("permission denied: %s", path)
		}
		return nil, fmt.Errorf("failed to read directory: %w", err)
	}

	files := make([]filesystem.FileInfo, 0, len(entries)+1)
	for i, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if i == maxLsEntries {
			// shortcut: the ls tool renders only FileInfo.Path, so a cap can only be
			// stated as one more "entry". Upgrade when the tool layer is ours.
			files = append(files, filesystem.FileInfo{
				Path: fmt.Sprintf("… %d more entries; list a narrower path", len(entries)-maxLsEntries),
			})
			break
		}
		fi := filesystem.FileInfo{Path: entry.Name(), IsDir: entry.IsDir()}
		if fi.IsDir {
			// The renderer drops IsDir, so the trailing slash is what tells the model
			// this is a directory it can list and cannot read.
			fi.Path += "/"
		}
		if info, err := entry.Info(); err == nil {
			fi.Size = info.Size()
			fi.ModifiedAt = info.ModTime().UTC().Format(time.RFC3339)
		}
		files = append(files, fi)
	}
	return files, nil
}

func (s *Local) Read(ctx context.Context, req *filesystem.ReadRequest) (*filesystem.FileContent, error) {
	path, err := s.resolve(req.FilePath)
	if err != nil {
		return nil, err
	}

	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("file not found: %s", path)
		}
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}
	if info.Size() == 0 {
		return &filesystem.FileContent{}, nil
	}

	offset := req.Offset
	if offset <= 0 {
		offset = 1
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 2000
	}

	reader := bufio.NewReader(file)
	var result strings.Builder
	lineNum := 1
	linesRead := 0

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		line, err := reader.ReadString('\n')
		if line != "" {
			if lineNum >= offset {
				result.WriteString(line)
				linesRead++
				if linesRead >= limit {
					break
				}
			}
			lineNum++
		}
		if err != nil {
			if err != io.EOF {
				return nil, fmt.Errorf("error reading file: %w", err)
			}
			break
		}
	}

	return &filesystem.FileContent{
		Content: strings.TrimSuffix(result.String(), "\n"),
	}, nil
}

type rgJSON struct {
	Type string `json:"type"`
	Data struct {
		Path struct {
			Text string `json:"text"`
		} `json:"path"`
		LineNumber int `json:"line_number"`
		Lines      struct {
			Text string `json:"text"`
		} `json:"lines"`
	} `json:"data"`
}

func (s *Local) GrepRaw(ctx context.Context, req *filesystem.GrepRequest) ([]filesystem.GrepMatch, error) {
	if req.Pattern == "" {
		return nil, fmt.Errorf("pattern is required")
	}
	path, err := s.resolve(req.Path)
	if err != nil {
		return nil, err
	}

	args := []string{"--json"}
	if req.CaseInsensitive {
		args = append(args, "-i")
	}
	if req.EnableMultiline {
		args = append(args, "-U", "--multiline-dotall")
	}
	if req.FileType != "" {
		args = append(args, "--type", req.FileType)
	} else if req.Glob != "" {
		args = append(args, "--glob", req.Glob)
	}
	if req.AfterLines > 0 {
		args = append(args, "-A", fmt.Sprintf("%d", req.AfterLines))
	}
	if req.BeforeLines > 0 {
		args = append(args, "-B", fmt.Sprintf("%d", req.BeforeLines))
	}
	// The pattern comes from the model: "--" keeps "-foo" a pattern, not a flag.
	args = append(args, "--", req.Pattern, path)

	// Read the results through a pipe instead of Output(): upstream buffered the whole
	// match list first, so `grep("a")` over a build tree put the entire result in memory
	// twice (the buffer, then strings.Split). One result per line, stop at the cap.
	//
	// ★ The same budget as the glob's walk: rg walks the tree it is given, and
	//   the tool call is synchronous — a search over a mounted volume would
	//   block the turn for as long as the disk takes.
	caller := ctx
	ctx, cancel := context.WithTimeout(ctx, s.walkBudget)
	defer cancel()
	cmd := exec.CommandContext(ctx, "rg", args...)
	cmd.Dir = s.root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to read ripgrep output: %w", err)
	}
	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, fmt.Errorf("ripgrep (rg) is not installed or not in PATH. Please install it: https://github.com/BurntSushi/ripgrep#installation")
		}
		return nil, fmt.Errorf("failed to execute ripgrep: %w", err)
	}

	matches := make([]filesystem.GrepMatch, 0, 32)
	capped := false
	scanner := bufio.NewScanner(stdout)
	// Each line carries a whole source line, so the default 64 KiB limit would stop
	// the scan on a generated or minified file.
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	for scanner.Scan() {
		var data rgJSON
		if err := json.Unmarshal(scanner.Bytes(), &data); err != nil {
			continue
		}
		if data.Type != "match" && data.Type != "context" {
			continue
		}
		matches = append(matches, filesystem.GrepMatch{
			Path:    data.Data.Path.Text,
			Line:    data.Data.LineNumber,
			Content: strings.TrimRight(data.Data.Lines.Text, "\n"),
		})
		if len(matches) >= maxGrepMatches {
			capped = true
			break
		}
	}
	scanErr := scanner.Err()
	if errors.Is(scanErr, bufio.ErrTooLong) {
		// One source line is past the buffer: the rest of the result is lost, so
		// report it like the cap instead of failing the whole call.
		scanErr = nil
		capped = true
	}
	// Stop rg instead of letting it finish a scan we no longer read.
	if capped {
		cancel()
	}
	waitErr := cmd.Wait()

	if err := caller.Err(); err != nil {
		return nil, err
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		// ★ The search ran out of its budget. Say what happened: a raw
		//   "exit status 1" or "context deadline exceeded" is something
		//   the model cannot act on.
		return nil, fmt.Errorf("the search hit its %s budget; narrow the path or the pattern", s.walkBudget)
	}
	if scanErr != nil {
		return nil, fmt.Errorf("failed to read ripgrep output: %w", scanErr)
	}
	if waitErr != nil && !capped {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			if exitErr.ExitCode() == 1 {
				return []filesystem.GrepMatch{}, nil
			}
			return nil, fmt.Errorf("ripgrep failed with exit code %d: %s", exitErr.ExitCode(), stderr.String())
		}
		return nil, fmt.Errorf("failed to execute ripgrep: %w", waitErr)
	}
	if capped {
		// The renderer prints "path:line:content" per match, so a cap can only be
		// stated as one more match. Upgrade when the tool layer is ours.
		matches = append(matches, filesystem.GrepMatch{
			Path:    path,
			Content: fmt.Sprintf("… %d matches is the cap; narrow the pattern or the path", maxGrepMatches),
		})
	}
	return matches, nil
}

func (s *Local) GlobInfo(ctx context.Context, req *filesystem.GlobInfoRequest) ([]filesystem.FileInfo, error) {
	base, err := s.resolve(req.Path)
	if err != nil {
		return nil, err
	}
	pattern, err := s.globPattern(base, req.Pattern)
	if err != nil {
		return nil, err
	}

	var matches []string
	capped := false
	timedOut := false
	// The deadline is the walk's budget. It starts when the walk starts, not
	// when the tool was called: the time spent resolving the path is the
	// tool's, the time spent walking is the walk's.
	deadline := time.Now().Add(s.walkBudget)
	segments := patternSegments(pattern)
	err = filepath.WalkDir(base, func(p string, d os.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if time.Now().After(deadline) {
			// ★ The walk ran out of time. Stopping here is better than
			//   blocking the turn: the model gets what was found and a
			//   note to narrow the path.
			timedOut = true
			return filepath.SkipAll
		}
		if walkErr != nil {
			// An unreadable subtree skips itself; it does not abort the whole search.
			// (The observed failure was an I/O error on /mnt/d/$RECYCLE.BIN.)
			return nil
		}

		rel, relErr := filepath.Rel(base, p)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}

		if d.IsDir() {
			if s.ignored(d) || strings.Count(rel, "/") >= maxGlobDepth {
				return filepath.SkipDir
			}
			// ★ Prune by the pattern: a directory whose own name cannot be
			//   the pattern's next segment cannot hold a match, so there
			//   is no reason to walk it. "docs/**" walks docs/ and
			//   nothing else; "*.go" does not walk a single directory.
			if !mayHold(segments, rel) {
				return filepath.SkipDir
			}
			return nil
		}

		if ok, _ := doublestar.Match(pattern, rel); ok {
			matches = append(matches, rel)
			if len(matches) >= maxGlobResults {
				capped = true
				return filepath.SkipAll
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(matches)

	files := make([]filesystem.FileInfo, 0, len(matches)+1)
	for _, match := range matches {
		files = append(files, filesystem.FileInfo{Path: match})
	}
	if capped {
		// shortcut: same renderer limit as ls — a cap can only be stated as one more
		// "entry". Upgrade when the tool layer is ours.
		files = append(files, filesystem.FileInfo{
			Path: fmt.Sprintf("… %d matches is the cap; narrow the pattern or the path", maxGlobResults),
		})
	}
	if timedOut {
		// ★ The walk ran out of its budget. Say so: a truncated search that
		//   looks complete is how a model concludes that a file does not
		//   exist.
		files = append(files, filesystem.FileInfo{
			Path: fmt.Sprintf("… the search hit its %s budget; narrow the path", s.walkBudget),
		})
	}
	return files, nil
}

// patternSegments splits a glob pattern into its path segments.
//
// ★ "**" is a segment of its own: it matches any number of directories, so
//
//	the pruning must let everything through at that depth.
func patternSegments(pattern string) []string {
	parts := strings.Split(strings.Trim(pattern, "/"), "/")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

// mayHold says whether a directory can contain a match for the pattern.
//
// ★ The directory's relative path tells the depth, and the pattern's segment
//
//	at that depth tells the name the directory would have to have. A
//	directory that cannot be that name cannot hold the match below it.
func mayHold(segments []string, rel string) bool {
	if len(segments) == 0 {
		return false
	}
	depth := strings.Count(rel, "/")
	if depth >= len(segments) {
		// The pattern is exhausted at this depth: only the files here can
		// match, and the pattern's last segment is a file pattern.
		return false
	}
	seg := segments[depth]
	if seg == "**" {
		return true
	}
	name := rel
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	ok, err := doublestar.Match(seg, name)
	if err != nil {
		return true
	}
	return ok
}

// globPattern makes an absolute pattern relative to the walk root.
//
// The run prompt tells the model to pass "<projectRoot>/**/*.go" (agentkit/run/input.go),
// while matches are relative to the root: without this the call silently found nothing.
func (s *Local) globPattern(base, pattern string) (string, error) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return "", errors.New("pattern is required")
	}
	if !filepath.IsAbs(pattern) {
		return filepath.ToSlash(pattern), nil
	}
	rel, err := filepath.Rel(base, filepath.Clean(pattern))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("pattern %q is outside %q", pattern, base)
	}
	return filepath.ToSlash(rel), nil
}

func (s *Local) Write(ctx context.Context, req *filesystem.WriteRequest) error {
	path, err := s.resolveWrite(req.FilePath)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create parent directory: %w", err)
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to open file for writing: %w", err)
	}
	defer file.Close()

	if _, err := file.Write([]byte(req.Content)); err != nil {
		return fmt.Errorf("failed to write to file: %w", err)
	}

	return nil
}

func (s *Local) Edit(ctx context.Context, req *filesystem.EditRequest) error {
	path, err := s.resolveWrite(req.FilePath)
	if err != nil {
		return err
	}

	if req.OldString == "" {
		return fmt.Errorf("old string is required")
	}

	if req.OldString == req.NewString {
		return fmt.Errorf("new string must be different from old string")
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	text := string(content)
	count := strings.Count(text, req.OldString)

	if count == 0 {
		return fmt.Errorf("string not found in file: '%s'", req.OldString)
	}
	if count > 1 && !req.ReplaceAll {
		return fmt.Errorf("string '%s' appears multiple times. Use replace_all=True to replace all occurrences", req.OldString)
	}

	var newText string
	if req.ReplaceAll {
		newText = strings.Replace(text, req.OldString, req.NewString, -1)
	} else {
		newText = strings.Replace(text, req.OldString, req.NewString, 1)
	}

	return os.WriteFile(path, []byte(newText), 0644)
}

func (s *Local) ExecuteStreaming(ctx context.Context, input *filesystem.ExecuteRequest) (result *schema.StreamReader[*filesystem.ExecuteResponse], err error) {
	if input.Command == "" {
		return nil, fmt.Errorf("command is required")
	}

	if err := s.validateCommand(input.Command); err != nil {
		return nil, err
	}

	cmd, stdout, stderr, err := s.initStreamingCmd(ctx, input.Command)
	if err != nil {
		return nil, err
	}

	sr, w := schema.Pipe[*filesystem.ExecuteResponse](100)

	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		_ = stderr.Close()
		go sendErrorAndClose(w, fmt.Errorf("failed to start command: %w", err))
		return sr, nil
	}

	if input.RunInBackendGround {
		s.runCmdInBackground(ctx, cmd, stdout, stderr, w)
		return sr, nil
	}

	go s.streamCmdOutput(ctx, cmd, stdout, stderr, w)

	return sr, nil
}

// initStreamingCmd creates command with stdout and stderr pipes.
func (s *Local) initStreamingCmd(ctx context.Context, command string) (*exec.Cmd, io.ReadCloser, io.ReadCloser, error) {
	// ★ The program is the caller's decision (see Config.Shell): the tool's
	//   description names the same dialect the command runs in.
	args := make([]string, 0, len(s.shell))
	args = append(args, s.shell[1:]...)
	args = append(args, command)
	cmd := exec.CommandContext(ctx, s.shell[0], args...)
	// Relative paths in a shell command mean the project root, not wherever the server
	// happened to be started.
	cmd.Dir = s.root
	// ★ The shell's own directory goes first: a shell that was found outside
	//   PATH (Git for Windows, MSYS2) cannot find `ls` without it.
	if s.shellPathDir != "" {
		cmd.Env = append(os.Environ(), "PATH="+s.shellPathDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdout.Close()
		return nil, nil, nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	return cmd, stdout, stderr, nil
}

// runCmdInBackground executes command in background without waiting for completion.
// The caller controls timeout/cancellation via ctx.Done().
func (s *Local) runCmdInBackground(ctx context.Context, cmd *exec.Cmd, stdout, stderr io.ReadCloser, w *schema.StreamWriter[*filesystem.ExecuteResponse]) {
	go func() {
		defer func() {
			if pe := recover(); pe != nil {
				_ = cmd.Process.Kill()
			}
			_ = stdout.Close()
			_ = stderr.Close()
		}()

		done := make(chan struct{})
		go func() {
			drainPipesConcurrently(stdout, stderr)
			_ = cmd.Wait()
			close(done)
		}()

		select {
		case <-done:
		case <-ctx.Done():
			_ = cmd.Process.Kill()
		}
	}()

	go func() {
		defer w.Close()
		w.Send(&filesystem.ExecuteResponse{Output: "command started in background\n", ExitCode: new(int)}, nil)
	}()
}

// drainPipesConcurrently consumes stdout and stderr concurrently to prevent pipe blocking.
func drainPipesConcurrently(stdout, stderr io.Reader) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(io.Discard, stdout)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(io.Discard, stderr)
	}()
	wg.Wait()
}

// streamCmdOutput handles streaming command output to the writer.
func (s *Local) streamCmdOutput(ctx context.Context, cmd *exec.Cmd, stdout, stderr io.ReadCloser, w *schema.StreamWriter[*filesystem.ExecuteResponse]) {
	defer func() {
		if pe := recover(); pe != nil {
			w.Send(nil, newPanicErr(pe, debug.Stack()))
			return
		}
		w.Close()
	}()

	stderrData, stderrErr := s.readStderrAsync(stderr)

	hasOutput, err := s.streamStdout(ctx, cmd, stdout, w)
	if err != nil {
		w.Send(nil, err)
		return
	}

	if stdError := <-stderrErr; stdError != nil {
		w.Send(nil, stdError)
		return
	}

	s.handleCmdCompletion(cmd, stderrData, hasOutput, w)
}

// readStderrAsync reads stderr in a separate goroutine.
func (s *Local) readStderrAsync(stderr io.Reader) (*[]byte, <-chan error) {
	stderrData := new([]byte)
	stderrErr := make(chan error, 1)

	go func() {
		defer func() {
			if pe := recover(); pe != nil {
				stderrErr <- newPanicErr(pe, debug.Stack())
				return
			}
			close(stderrErr)
		}()
		var err error
		*stderrData, err = io.ReadAll(stderr)
		if err != nil {
			stderrErr <- fmt.Errorf("failed to read stderr: %w", err)
		}
	}()

	return stderrData, stderrErr
}

// streamStdout streams stdout line by line to the writer.
func (s *Local) streamStdout(ctx context.Context, cmd *exec.Cmd, stdout io.Reader, w *schema.StreamWriter[*filesystem.ExecuteResponse]) (bool, error) {
	scanner := bufio.NewScanner(stdout)
	hasOutput := false

	for scanner.Scan() {
		hasOutput = true
		line := scanner.Text() + "\n"
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			return hasOutput, ctx.Err()
		default:
			w.Send(&filesystem.ExecuteResponse{Output: line}, nil)
		}
	}

	if err := scanner.Err(); err != nil {
		return hasOutput, fmt.Errorf("error reading stdout: %w", err)
	}

	return hasOutput, nil
}

// handleCmdCompletion handles command completion and sends final response.
func (s *Local) handleCmdCompletion(cmd *exec.Cmd, stderrData *[]byte, hasOutput bool, w *schema.StreamWriter[*filesystem.ExecuteResponse]) {
	if err := cmd.Wait(); err != nil {
		exitCode := 0
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			exitCode = exitError.ExitCode()
		}
		if len(*stderrData) > 0 {
			w.Send(nil, fmt.Errorf("command exited with non-zero code %d: %s", exitCode, string(*stderrData)))
			return
		}
		w.Send(nil, fmt.Errorf("command exited with non-zero code %d", exitCode))
		return
	}

	if !hasOutput {
		w.Send(&filesystem.ExecuteResponse{ExitCode: new(int)}, nil)
	}
}

// sendErrorAndClose sends an error to the stream and closes it.
func sendErrorAndClose(w *schema.StreamWriter[*filesystem.ExecuteResponse], err error) {
	defer w.Close()
	w.Send(nil, err)
}

type panicErr struct {
	info  any
	stack []byte
}

func (p *panicErr) Error() string {
	return fmt.Sprintf("panic error: %v, \nstack: %s", p.info, string(p.stack))
}

func newPanicErr(info any, stack []byte) error {
	return &panicErr{
		info:  info,
		stack: stack,
	}
}
