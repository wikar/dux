package dash

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
)

// maxDocumentBytes caps dashboard document uploads (documents are hand-sized
// JSON; anything near this is a mistake).
const maxDocumentBytes = 4 << 20

// Config configures the dashboards module.
type Config struct {
	// Root is the dashboards directory (created lazily on first write).
	Root string
	// MaxAssetBytes caps asset uploads. Default 10 MiB.
	MaxAssetBytes int64
	// RefreshFloorSeconds is the minimum allowed live-refresh interval.
	// Default 5; 0 keeps the default (use -1 to disable the check).
	RefreshFloorSeconds int
}

// Server is the /api/dash HTTP API over a Store. Mount it on the parent mux
// at /api/dash/.
type Server struct {
	cfg   Config
	store *Store
	mux   *http.ServeMux
}

func NewServer(cfg Config) (*Server, error) {
	if cfg.Root == "" {
		cfg.Root = "dashboards"
	}
	if cfg.MaxAssetBytes <= 0 {
		cfg.MaxAssetBytes = 10 << 20
	}
	if cfg.RefreshFloorSeconds == 0 {
		cfg.RefreshFloorSeconds = 5
	}
	floor := cfg.RefreshFloorSeconds
	if floor < 0 {
		floor = 0
	}
	validator, err := NewValidator(floor)
	if err != nil {
		return nil, err
	}
	s := &Server{cfg: cfg, store: NewStore(cfg.Root, validator.Validate)}

	m := http.NewServeMux()
	m.HandleFunc("GET /api/dash/dashboards", s.handleList)
	m.HandleFunc("GET /api/dash/dashboards/{path...}", s.handleGet)
	m.HandleFunc("PUT /api/dash/dashboards/{path...}", s.handlePut)
	m.HandleFunc("DELETE /api/dash/dashboards/{path...}", s.handleDelete)
	m.HandleFunc("POST /api/dash/assets/{path...}", s.handlePutAsset)
	m.HandleFunc("GET /api/dash/assets/{path...}", s.handleGetAsset)
	m.HandleFunc("GET /api/dash/theme", s.handleGetTheme)
	m.HandleFunc("PUT /api/dash/theme", s.handlePutTheme)
	m.HandleFunc("GET /api/dash/schema.json", s.handleSchema)
	s.mux = m
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

// Store exposes the underlying store (used by tests and future callers).
func (s *Server) Store() *Store { return s.store }

// ─── Handlers ────────────────────────────────────────────────────────────────

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	entries, err := s.store.List()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	// ?raw=1 downloads the document file verbatim (no envelope) — pairs with
	// PUT If-Match: * for backup/restore and agent tooling.
	if r.URL.Query().Get("raw") == "1" {
		data, etag, err := s.store.GetRaw(r.PathValue("path"))
		if err != nil {
			writeStoreError(w, err)
			return
		}
		w.Header().Set("ETag", etag)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition",
			fmt.Sprintf("attachment; filename=%q", path.Base(r.PathValue("path"))+".json"))
		_, _ = w.Write(data)
		return
	}
	doc, err := s.store.Get(r.PathValue("path"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.Header().Set("ETag", doc.ETag)
	writeJSON(w, http.StatusOK, doc)
}

func (s *Server) handlePut(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxDocumentBytes))
	if err != nil {
		jsonError(w, http.StatusRequestEntityTooLarge, "document too large")
		return
	}
	etag, created, err := s.store.Put(r.PathValue("path"), body, r.Header.Get("If-Match"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.Header().Set("ETag", etag)
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]any{"etag": etag, "created": created})
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Delete(r.PathValue("path"), r.Header.Get("If-Match")); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePutAsset(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.cfg.MaxAssetBytes))
	if err != nil {
		jsonError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("asset exceeds the %d byte limit", s.cfg.MaxAssetBytes))
		return
	}
	if len(body) == 0 {
		jsonError(w, http.StatusBadRequest, "empty asset body")
		return
	}
	mime, err := s.store.PutAsset(r.PathValue("path"), body)
	if err != nil {
		if strings.Contains(err.Error(), "unsupported asset type") {
			jsonError(w, http.StatusUnsupportedMediaType, err.Error())
			return
		}
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"path": r.PathValue("path"), "type": mime})
}

func (s *Server) handleGetAsset(w http.ResponseWriter, r *http.Request) {
	data, mime, err := s.store.GetAsset(r.PathValue("path"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("ETag", etagOf(data))
	if mime == "image/svg+xml" {
		// SVG can carry scripts; serving with a no-execute CSP neutralises them.
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	}
	_, _ = w.Write(data)
}

func (s *Server) handleGetTheme(w http.ResponseWriter, r *http.Request) {
	theme, etag, err := s.store.GetTheme()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if etag != "" {
		w.Header().Set("ETag", etag)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(theme)
}

func (s *Server) handlePutTheme(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxDocumentBytes))
	if err != nil {
		jsonError(w, http.StatusRequestEntityTooLarge, "theme too large")
		return
	}
	etag, err := s.store.PutTheme(body, r.Header.Get("If-Match"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.Header().Set("ETag", etag)
	writeJSON(w, http.StatusOK, map[string]any{"etag": etag})
}

func (s *Server) handleSchema(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/schema+json")
	_, _ = w.Write(schemaJSON)
}

// ─── Error mapping ───────────────────────────────────────────────────────────

func writeStoreError(w http.ResponseWriter, err error) {
	var ve *ValidationError
	var ce *ConflictError
	switch {
	case errors.Is(err, ErrNotFound):
		jsonError(w, http.StatusNotFound, "not found")
	case errors.Is(err, ErrPreconditionRequired):
		jsonError(w, http.StatusPreconditionRequired, err.Error())
	case errors.Is(err, ErrPreconditionFailed):
		jsonError(w, http.StatusPreconditionFailed, err.Error())
	case errors.As(err, &ce):
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":       ce.Error(),
			"currentEtag": ce.CurrentETag,
			"modified":    ce.Modified,
		})
	case errors.As(err, &ve):
		jsonError(w, http.StatusUnprocessableEntity, ve.Error())
	default:
		// Path validation and similar caller mistakes.
		jsonError(w, http.StatusBadRequest, err.Error())
	}
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
