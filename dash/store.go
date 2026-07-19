// Package dashboards is the self-contained DUX UI backend module: a JSON
// file store for dashboard documents plus the /api/dash HTTP API. Dashboards
// live as one pretty-printed JSON file per dashboard under a root directory —
// git-versionable and directly editable by humans and agents; the file's
// relative path (without .json) is the dashboard's identity, and directories
// are the folder hierarchy.
//
// IMPORT RULE: this package must never import the DUX query engine
// (semantic/executor/emitter/parser) or go-duckdb — it is mounted in-process
// by duxd AND compiled into the standalone, CGO-free duxuid binary.
package dash

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// themeFile is the reserved root-level file holding the global theme tokens.
const themeFile = "theme.json"

// Typed store errors, mapped to HTTP statuses by the handlers.
var (
	ErrNotFound             = errors.New("not found")
	ErrPreconditionRequired = errors.New("If-Match required: the dashboard already exists")
	ErrPreconditionFailed   = errors.New("If-Match given but the dashboard does not exist")
)

// ConflictError reports an If-Match mismatch (the file changed since the
// client loaded it — another tab, another user, an agent, or git).
type ConflictError struct {
	CurrentETag string
	Modified    time.Time
}

func (e *ConflictError) Error() string {
	return "dashboard changed since it was loaded (ETag mismatch)"
}

// Entry is one dashboard in the listing.
type Entry struct {
	Path     string    `json:"path"`
	Name     string    `json:"name"`
	Modified time.Time `json:"modified"`
	ETag     string    `json:"etag"`
	Valid    bool      `json:"valid"`
	Error    string    `json:"error,omitempty"`
}

// Document is the GET envelope for a single dashboard. For files that fail
// JSON parsing, Raw carries the text so the client can offer raw editing.
type Document struct {
	Entry
	Document json.RawMessage `json:"document,omitempty"`
	Raw      string          `json:"raw,omitempty"`
}

// Store is the dashboard file store. External edits (git, agents, editors)
// are picked up automatically: cached validation results are keyed on file
// size + mtime and re-checked on every read.
type Store struct {
	root     string
	validate func([]byte) error

	mu    sync.Mutex // guards writes and the cache
	cache map[string]*cacheEntry
}

type cacheEntry struct {
	size    int64
	modTime time.Time
	etag    string
	valid   bool
	verr    string
}

func NewStore(root string, validate func([]byte) error) *Store {
	return &Store{root: root, validate: validate, cache: make(map[string]*cacheEntry)}
}

// Root returns the store's root directory.
func (s *Store) Root() string { return s.root }

// ─── Path rules ──────────────────────────────────────────────────────────────

// invalidPathChars are rejected in path segments (Windows-invalid plus quotes).
const invalidPathChars = `<>:"|?*\`

// CleanPath validates and normalises a dashboard/asset path: slash-separated
// segments, no traversal, no Windows-hostile names, and **lower-cased** — the
// store's identities are lower-case so behaviour is identical on
// case-insensitive (Windows/macOS) and case-sensitive (Linux) filesystems.
// Files created outside the API with mixed-case names are resolved
// case-insensitively and listed under their lower-cased identity.
func CleanPath(p string) (string, error) {
	p = strings.Trim(strings.ReplaceAll(p, "\\", "/"), "/")
	if p == "" {
		return "", fmt.Errorf("empty path")
	}
	if len(p) > 512 {
		return "", fmt.Errorf("path too long")
	}
	segs := strings.Split(p, "/")
	if len(segs) > 12 {
		return "", fmt.Errorf("path too deep")
	}
	for _, seg := range segs {
		if seg == "" || seg == "." || seg == ".." {
			return "", fmt.Errorf("invalid path segment %q", seg)
		}
		if strings.ContainsAny(seg, invalidPathChars) {
			return "", fmt.Errorf("path segment %q contains invalid characters", seg)
		}
		if strings.HasSuffix(seg, " ") || strings.HasSuffix(seg, ".") {
			return "", fmt.Errorf("path segment %q must not end with a space or dot", seg)
		}
		for _, r := range seg {
			if r < 0x20 || r == 0x7f {
				return "", fmt.Errorf("path segment contains control characters")
			}
		}
	}
	return strings.ToLower(strings.Join(segs, "/")), nil
}

// resolvePath maps a lower-cased slash-relative path to its absolute on-disk
// location, matching each segment case-insensitively so externally created
// mixed-case files resolve on case-sensitive filesystems. The boolean reports
// whether the file exists; when false, the returned path is the canonical
// (lower-case) location for creating it.
func (s *Store) resolvePath(relLower string) (string, bool) {
	abs := filepath.Join(s.root, filepath.FromSlash(relLower))
	if _, err := os.Stat(abs); err == nil {
		return abs, true
	}
	cur := s.root
	segs := strings.Split(relLower, "/")
	for i, seg := range segs {
		entries, err := os.ReadDir(cur)
		if err != nil {
			return abs, false
		}
		found := ""
		wantDir := i < len(segs)-1
		for _, e := range entries {
			if strings.ToLower(e.Name()) == seg && e.IsDir() == wantDir {
				found = e.Name()
				break
			}
		}
		if found == "" {
			return abs, false
		}
		cur = filepath.Join(cur, found)
	}
	return cur, true
}

// cleanDashboardPath applies CleanPath plus dashboard-specific reservations.
func cleanDashboardPath(p string) (string, error) {
	p, err := CleanPath(p)
	if err != nil {
		return "", err
	}
	if strings.EqualFold(p, "theme") {
		return "", fmt.Errorf("%q is reserved for the global theme", p)
	}
	if strings.HasSuffix(strings.ToLower(p), ".json") {
		return "", fmt.Errorf("dashboard paths must not include the .json extension")
	}
	return p, nil
}

// ─── ETag ────────────────────────────────────────────────────────────────────

func etagOf(data []byte) string {
	sum := sha256.Sum256(data)
	return `"` + hex.EncodeToString(sum[:]) + `"`
}

// etagsEqual compares ETags ignoring optional quotes and W/ prefixes.
func etagsEqual(a, b string) bool {
	norm := func(s string) string {
		s = strings.TrimSpace(s)
		s = strings.TrimPrefix(s, "W/")
		return strings.Trim(s, `"`)
	}
	return norm(a) != "" && norm(a) == norm(b)
}

// ─── Cache ───────────────────────────────────────────────────────────────────

// stat reads a file's cached etag/validation, refreshing when size or mtime
// changed. absPath is the resolved on-disk location for the cache key.
func (s *Store) stat(cleanPath, absPath string, info fs.FileInfo, data []byte) (*cacheEntry, []byte, error) {
	s.mu.Lock()
	ce := s.cache[cleanPath]
	if ce != nil && ce.size == info.Size() && ce.modTime.Equal(info.ModTime()) {
		s.mu.Unlock()
		return ce, data, nil
	}
	s.mu.Unlock()

	if data == nil {
		var err error
		data, err = os.ReadFile(absPath)
		if err != nil {
			return nil, nil, err
		}
	}
	ce = &cacheEntry{size: info.Size(), modTime: info.ModTime(), etag: etagOf(data), valid: true}
	if err := s.validate(data); err != nil {
		ce.valid = false
		ce.verr = err.Error()
	}
	s.mu.Lock()
	s.cache[cleanPath] = ce
	s.mu.Unlock()
	return ce, data, nil
}

// ─── Operations ──────────────────────────────────────────────────────────────

// List walks the store and returns every dashboard, sorted by path. Externally
// added, changed, or deleted files are reflected without any restart.
func (s *Store) List() ([]Entry, error) {
	var entries []Entry
	err := filepath.WalkDir(s.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) && p == s.root {
				return filepath.SkipAll // no dashboards dir yet — empty store
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".json") {
			return nil
		}
		rel, err := filepath.Rel(s.root, p)
		if err != nil {
			return err
		}
		relSlash := strings.ToLower(filepath.ToSlash(rel))
		clean := strings.TrimSuffix(relSlash, ".json")
		if relSlash == themeFile {
			return nil // the global theme is not a dashboard
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		ce, _, err := s.stat(clean, p, info, nil)
		if err != nil {
			return err
		}
		entries = append(entries, Entry{
			Path:     clean,
			Name:     path.Base(clean),
			Modified: info.ModTime().UTC(),
			ETag:     ce.etag,
			Valid:    ce.valid,
			Error:    ce.verr,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	if entries == nil {
		entries = []Entry{}
	}
	return entries, nil
}

// Get returns the document envelope for one dashboard.
func (s *Store) Get(rawPath string) (*Document, error) {
	clean, err := cleanDashboardPath(rawPath)
	if err != nil {
		return nil, err
	}
	abs, exists := s.resolvePath(clean + ".json")
	if !exists {
		return nil, ErrNotFound
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	ce, data, err := s.stat(clean, abs, info, data)
	if err != nil {
		return nil, err
	}
	doc := &Document{Entry: Entry{
		Path:     clean,
		Name:     path.Base(clean),
		Modified: info.ModTime().UTC(),
		ETag:     ce.etag,
		Valid:    ce.valid,
		Error:    ce.verr,
	}}
	if json.Valid(data) {
		doc.Document = json.RawMessage(data)
	} else {
		doc.Raw = string(data)
	}
	return doc, nil
}

// GetRaw returns a dashboard's file bytes verbatim with their ETag — the
// download form of Get (round-trips through PUT with If-Match: *).
func (s *Store) GetRaw(rawPath string) (data []byte, etag string, err error) {
	clean, err := cleanDashboardPath(rawPath)
	if err != nil {
		return nil, "", err
	}
	abs, exists := s.resolvePath(clean + ".json")
	if !exists {
		return nil, "", ErrNotFound
	}
	data, err = os.ReadFile(abs)
	if err != nil {
		return nil, "", err
	}
	return data, etagOf(data), nil
}

// Put creates or replaces a dashboard with optimistic concurrency:
//
//	file absent  + no If-Match → create
//	file absent  + If-Match    → ErrPreconditionFailed (client expected an existing doc)
//	file present + no If-Match → ErrPreconditionRequired (no blind overwrites)
//	file present + stale match → *ConflictError
//	If-Match: *                → unconditional create-or-overwrite (agent/tooling upload)
//
// The document is validated before anything touches disk, normalised to
// pretty-printed form (files are a git-diff surface), and written atomically
// (temp file + rename — no torn JSON, ever).
func (s *Store) Put(rawPath string, body []byte, ifMatch string) (etag string, created bool, err error) {
	clean, err := cleanDashboardPath(rawPath)
	if err != nil {
		return "", false, err
	}
	if err := s.validate(body); err != nil {
		return "", false, &ValidationError{Err: err}
	}
	pretty, err := prettyJSON(body)
	if err != nil {
		return "", false, &ValidationError{Err: err}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Resolve case-insensitively: an externally created mixed-case file is
	// updated in place (its casing is git's business, not ours).
	target, exists := s.resolvePath(clean + ".json")
	switch {
	case ifMatch == "*":
		// Deliberate overwrite: skip all precondition checks.
		created = !exists
	case !exists:
		if ifMatch != "" {
			return "", false, ErrPreconditionFailed
		}
		created = true
	default:
		existing, readErr := os.ReadFile(target)
		if readErr != nil {
			return "", false, readErr
		}
		if ifMatch == "" {
			return "", false, ErrPreconditionRequired
		}
		if !etagsEqual(ifMatch, etagOf(existing)) {
			info, _ := os.Stat(target)
			ce := &ConflictError{CurrentETag: etagOf(existing)}
			if info != nil {
				ce.Modified = info.ModTime().UTC()
			}
			return "", false, ce
		}
	}

	if err := writeAtomic(target, pretty); err != nil {
		return "", false, err
	}
	delete(s.cache, clean)
	return etagOf(pretty), created, nil
}

// Delete removes a dashboard. A non-empty ifMatch must match the current
// content (same conflict semantics as Put); empty ifMatch deletes outright.
func (s *Store) Delete(rawPath string, ifMatch string) error {
	clean, err := cleanDashboardPath(rawPath)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	target, exists := s.resolvePath(clean + ".json")
	if !exists {
		return ErrNotFound
	}
	existing, readErr := os.ReadFile(target)
	if readErr != nil {
		return readErr
	}
	if ifMatch != "" && ifMatch != "*" && !etagsEqual(ifMatch, etagOf(existing)) {
		return &ConflictError{CurrentETag: etagOf(existing)}
	}
	if err := os.Remove(target); err != nil {
		return err
	}
	delete(s.cache, clean)
	return nil
}

// ─── Theme ───────────────────────────────────────────────────────────────────

// GetTheme returns the global theme tokens and their ETag. A missing file is
// an empty theme: ({}, "", nil).
func (s *Store) GetTheme() (json.RawMessage, string, error) {
	data, err := os.ReadFile(filepath.Join(s.root, themeFile))
	if os.IsNotExist(err) {
		return json.RawMessage("{}"), "", nil
	}
	if err != nil {
		return nil, "", err
	}
	return data, etagOf(data), nil
}

// PutTheme replaces the global theme with the same optimistic-concurrency
// rules as Put (a missing file counts as absent; If-Match: * overwrites
// unconditionally — the import path).
func (s *Store) PutTheme(body []byte, ifMatch string) (string, error) {
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		return "", &ValidationError{Err: fmt.Errorf("theme must be a JSON object: %w", err)}
	}
	pretty, err := prettyJSON(body)
	if err != nil {
		return "", &ValidationError{Err: err}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	target := filepath.Join(s.root, themeFile)
	existing, readErr := os.ReadFile(target)
	switch {
	case ifMatch == "*":
		if readErr != nil && !os.IsNotExist(readErr) {
			return "", readErr
		}
	case os.IsNotExist(readErr):
		if ifMatch != "" {
			return "", ErrPreconditionFailed
		}
	case readErr != nil:
		return "", readErr
	default:
		if ifMatch == "" {
			return "", ErrPreconditionRequired
		}
		if !etagsEqual(ifMatch, etagOf(existing)) {
			return "", &ConflictError{CurrentETag: etagOf(existing)}
		}
	}
	if err := writeAtomic(target, pretty); err != nil {
		return "", err
	}
	return etagOf(pretty), nil
}

// ─── Assets ──────────────────────────────────────────────────────────────────

// assetMIME maps allowed asset extensions to their served content type.
var assetMIME = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".webp": "image/webp",
	".svg":  "image/svg+xml",
}

// GetAsset reads an asset and its content type. Assets are image files kept
// on disk under the dashboards root (deployed alongside the documents) —
// there is no upload path; only assetMIME extensions are served.
func (s *Store) GetAsset(rawPath string) (data []byte, mime string, err error) {
	clean, err := CleanPath(rawPath)
	if err != nil {
		return nil, "", err
	}
	mime, ok := assetMIME[path.Ext(clean)]
	if !ok {
		return nil, "", ErrNotFound
	}
	abs, exists := s.resolvePath(clean)
	if !exists {
		return nil, "", ErrNotFound
	}
	data, err = os.ReadFile(abs)
	if err != nil {
		return nil, "", err
	}
	return data, mime, nil
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// ValidationError marks a 422: the payload is structurally unacceptable.
type ValidationError struct{ Err error }

func (e *ValidationError) Error() string { return e.Err.Error() }
func (e *ValidationError) Unwrap() error { return e.Err }

// prettyJSON re-indents a JSON document with stable formatting (2-space
// indent, trailing newline) so on-disk files diff cleanly in git.
func prettyJSON(data []byte) ([]byte, error) {
	var v any
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber() // preserve numeric representation
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("not valid JSON: %w", err)
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// writeAtomic writes data to target via a same-directory temp file + rename,
// creating parent directories as needed. Readers never observe torn writes.
func writeAtomic(target string, data []byte) error {
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return err
	}
	tmp := filepath.Join(dir, ".tmp-"+hex.EncodeToString(suffix[:]))
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
