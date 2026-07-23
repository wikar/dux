package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/danielwikar/dux/internal/bootstrap"
	"github.com/danielwikar/dux/internal/ducklake"
)

type ducklakeSchedule struct {
	SchemaRefresh time.Duration
	Compact       time.Duration
	Checkpoint    time.Duration
	StartedAt     time.Time
}

func ducklakeStatusHandler(runtime *bootstrap.Runtime, service *ducklake.Service, schedule ducklakeSchedule) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		duckDB, duckLake, err := runtime.Query.Versions(r.Context())
		if err != nil {
			writeError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		snapshot, schemaVersion, err := runtime.Query.SnapshotState(r.Context())
		if err != nil {
			writeError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		publicPath := func(path string) string {
			relative, err := filepath.Rel(runtime.DBDir, path)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				return "external"
			}
			return filepath.ToSlash(relative)
		}
		lastRefresh, refreshError, refreshWarning := runtime.RefreshStatus()
		health := "healthy"
		if refreshError != "" || refreshWarning != "" {
			health = "degraded"
		}
		maintenance, maintenanceErr := service.MaintenanceRuns(5)
		imports, importsErr := service.RecentImports(5)
		operationalError := ""
		if maintenanceErr != nil || importsErr != nil {
			health = "degraded"
			operationalError = "could not read DuckLake operation history"
		}
		nextRun := func(operation string, interval time.Duration) any {
			if interval <= 0 {
				return nil
			}
			if last, ok := service.LastScheduledMaintenance(operation); ok {
				return last.Add(interval)
			}
			return schedule.StartedAt.Add(interval)
		}
		writeJSON(w, map[string]any{
			"health": health, "duckdbVersion": duckDB, "ducklakeVersion": duckLake, "ducklakeFormatVersion": runtime.Query.FormatVersion(),
			"snapshotId": snapshot, "schemaVersion": schemaVersion,
			"catalogType": "sqlite", "catalogPath": publicPath(runtime.CatalogPath), "dataPath": publicPath(runtime.DataPath),
			"activeOperationId":  service.Active(),
			"importsEnabled":     service.ImportsEnabled(),
			"settings":           runtime.Query.EffectiveSettings(),
			"lastSchemaRefresh":  lastRefresh,
			"schemaRefreshError": refreshError,
			"schemaWarning":      refreshWarning,
			"operationalError":   operationalError,
			"latestMaintenance":  maintenance,
			"latestImports":      imports,
			"scheduler": map[string]any{
				"schemaRefreshInterval": schedule.SchemaRefresh.String(),
				"compactInterval":       schedule.Compact.String(),
				"checkpointInterval":    schedule.Checkpoint.String(),
				"nextCompactAt":         nextRun("compact", schedule.Compact),
				"nextCheckpointAt":      nextRun("checkpoint", schedule.Checkpoint),
			},
		})
	}
}

func maintenanceCollectionHandler(service *ducklake.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			jobs, err := service.MaintenanceRuns(limit)
			if err != nil {
				writeError(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, map[string]any{"activeOperationId": service.Active(), "runs": jobs})
		case http.MethodPost:
			var request struct {
				Operation string `json:"operation"`
			}
			body, err := readBody(w, r)
			if err != nil {
				writeError(w, err.Error(), bodyErrorStatus(err))
				return
			}
			decoder := json.NewDecoder(bytes.NewReader(body))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&request); err != nil {
				writeError(w, err.Error(), http.StatusBadRequest)
				return
			}
			if err := decoder.Decode(&struct{}{}); err != io.EOF {
				writeError(w, "request body must contain one JSON object", http.StatusBadRequest)
				return
			}
			job, err := service.StartMaintenance(request.Operation)
			if err != nil {
				writeDuckLakeOperationError(w, err)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			writeJSON(w, job)
		}
	}
}

func maintenanceJobHandler(service *ducklake.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		job, ok := service.Maintenance(r.PathValue("id"))
		if !ok {
			writeError(w, "maintenance job not found", http.StatusNotFound)
			return
		}
		writeJSON(w, job)
	}
}

func importCollectionHandler(service *ducklake.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request ducklake.ImportRequest
		body, err := readBody(w, r)
		if err != nil {
			writeError(w, err.Error(), bodyErrorStatus(err))
			return
		}
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			writeError(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			writeError(w, "request body must contain one JSON object", http.StatusBadRequest)
			return
		}
		job, err := service.StartImport(r.Header.Get("Idempotency-Key"), request)
		if err != nil {
			writeDuckLakeOperationError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		writeJSON(w, job)
	}
}

func monitorDuckLakeSchema(ctx context.Context, runtime *bootstrap.Runtime, interval time.Duration, refresh func()) {
	if interval <= 0 {
		return
	}
	_, known, err := runtime.Query.SnapshotState(ctx)
	initialized := err == nil
	if err != nil {
		log.Printf("warning: start schema monitor: %v", err)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		_, current, err := runtime.Query.SnapshotState(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("warning: poll DuckLake schema: %v", err)
			continue
		}
		if !initialized {
			refresh()
			if _, refreshError, _ := runtime.RefreshStatus(); refreshError == "" {
				known, initialized = current, true
			}
			continue
		}
		if current != known {
			refresh()
			if _, refreshError, _ := runtime.RefreshStatus(); refreshError == "" {
				known = current
			}
		}
	}
}

func scheduleDuckLakeMaintenance(ctx context.Context, service *ducklake.Service, operation string, interval time.Duration) {
	if interval <= 0 {
		return
	}
	delay := interval
	if last, ok := service.LastScheduledMaintenance(operation); ok {
		if remaining := interval - time.Since(last); remaining > 0 {
			delay = remaining
		} else {
			delay = 0
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if _, err := service.StartScheduledMaintenance(operation); err != nil {
			log.Printf("warning: scheduled DuckLake %s: %v", operation, err)
		}
		timer.Reset(interval)
	}
}

func importJobHandler(service *ducklake.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		job, ok := service.Import(r.PathValue("id"))
		if !ok {
			writeError(w, "import job not found", http.StatusNotFound)
			return
		}
		writeJSON(w, job)
	}
}

func writeDuckLakeOperationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ducklake.ErrBusy), errors.Is(err, ducklake.ErrIdempotencyConflict), errors.Is(err, ducklake.ErrDuplicateContent):
		writeError(w, err.Error(), http.StatusConflict)
	case errors.Is(err, ducklake.ErrImportsDisabled):
		writeError(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, err.Error(), http.StatusGatewayTimeout)
	case errors.Is(err, ducklake.ErrInvalidRequest):
		writeError(w, err.Error(), http.StatusBadRequest)
	default:
		writeError(w, err.Error(), http.StatusInternalServerError)
	}
}
