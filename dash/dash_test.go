package dash_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielwikar/dux/dash"
)

// newTestServer returns the API over a temp store root.
func newTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "dashboards")
	srv, err := dash.NewServer(dash.Config{Root: root, MaxAssetBytes: 1 << 20})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts, root
}

// validDoc returns a minimal valid dashboard document.
func validDoc() string {
	return `{
		"version": 1,
		"canvas": { "width": 1280, "height": 720 },
		"elements": [
			{ "id": "t1", "type": "text", "layout": { "x": 0, "y": 0, "w": 200, "h": 100 },
			  "text": { "markdown": "# Hello" } }
		]
	}`
}

func do(t *testing.T, method, url, body string, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

func bodyJSON(t *testing.T, res *http.Response) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.NewDecoder(res.Body).Decode(&m); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return m
}

func TestDashboardCRUD(t *testing.T) {
	ts, root := newTestServer(t)
	url := ts.URL + "/api/dash/dashboards/Sales/Weekly%20Revenue"

	// Create without If-Match.
	res := do(t, "PUT", url, validDoc(), nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d", res.StatusCode)
	}
	etag := res.Header.Get("ETag")
	if etag == "" {
		t.Fatal("create: missing ETag header")
	}

	// The file exists on disk, pretty-printed, at the lower-cased identity path.
	data, err := os.ReadFile(filepath.Join(root, "sales", "weekly revenue.json"))
	if err != nil {
		t.Fatalf("expected lower-cased file on disk: %v", err)
	}
	if !strings.Contains(string(data), "\n  \"canvas\"") {
		t.Errorf("file should be pretty-printed, got:\n%s", data)
	}

	// GET returns the envelope with document + ETag; identity is lower-case.
	res = do(t, "GET", url, "", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("get: expected 200, got %d", res.StatusCode)
	}
	if res.Header.Get("ETag") != etag {
		t.Errorf("get: ETag mismatch: %q vs %q", res.Header.Get("ETag"), etag)
	}
	doc := bodyJSON(t, res)
	if doc["valid"] != true || doc["name"] != "weekly revenue" || doc["path"] != "sales/weekly revenue" {
		t.Errorf("get envelope wrong: %+v", doc)
	}

	// Any casing addresses the same dashboard.
	res = do(t, "GET", ts.URL+"/api/dash/dashboards/SALES/WEEKLY%20REVENUE", "", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("case-insensitive get: expected 200, got %d", res.StatusCode)
	}

	// Listing shows it under the lower-cased path.
	res = do(t, "GET", ts.URL+"/api/dash/dashboards", "", nil)
	var list []map[string]any
	if err := json.NewDecoder(res.Body).Decode(&list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 || list[0]["path"] != "sales/weekly revenue" || list[0]["valid"] != true {
		t.Fatalf("list wrong: %+v", list)
	}

	// Update without If-Match → 428.
	res = do(t, "PUT", url, validDoc(), nil)
	if res.StatusCode != http.StatusPreconditionRequired {
		t.Fatalf("blind update: expected 428, got %d", res.StatusCode)
	}

	// Update with correct If-Match → 200, new content.
	updated := strings.Replace(validDoc(), `"# Hello"`, `"# Updated"`, 1)
	res = do(t, "PUT", url, updated, map[string]string{"If-Match": etag})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("update: expected 200, got %d", res.StatusCode)
	}
	etag2 := res.Header.Get("ETag")
	if etag2 == etag {
		t.Error("update should change the ETag")
	}

	// Update with the stale ETag → 409 with the current ETag.
	res = do(t, "PUT", url, validDoc(), map[string]string{"If-Match": etag})
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("stale update: expected 409, got %d", res.StatusCode)
	}
	conflict := bodyJSON(t, res)
	if conflict["currentEtag"] != etag2 {
		t.Errorf("conflict should carry current etag, got %+v", conflict)
	}

	// If-Match against a non-existent document → 412.
	res = do(t, "PUT", ts.URL+"/api/dash/dashboards/nope", validDoc(), map[string]string{"If-Match": etag})
	if res.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("if-match on absent: expected 412, got %d", res.StatusCode)
	}

	// Delete with stale If-Match → 409; with correct → 204; then GET → 404.
	res = do(t, "DELETE", url, "", map[string]string{"If-Match": etag})
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("stale delete: expected 409, got %d", res.StatusCode)
	}
	res = do(t, "DELETE", url, "", map[string]string{"If-Match": etag2})
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: expected 204, got %d", res.StatusCode)
	}
	res = do(t, "GET", url, "", nil)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("get after delete: expected 404, got %d", res.StatusCode)
	}
}

func TestValidationErrors(t *testing.T) {
	ts, _ := newTestServer(t)
	url := ts.URL + "/api/dash/dashboards/bad"

	cases := []struct {
		name string
		doc  string
		want string
	}{
		{"missing version", `{"canvas":{"width":1280,"height":720},"elements":[]}`, "version"},
		{"bad element type", `{"version":1,"canvas":{"width":1280,"height":720},"elements":[
			{"id":"a","type":"hologram","layout":{"x":0,"y":0,"w":10,"h":10}}]}`, "/elements/0/type"},
		{"duplicate ids", `{"version":1,"canvas":{"width":1280,"height":720},"elements":[
			{"id":"a","type":"text","layout":{"x":0,"y":0,"w":10,"h":10}},
			{"id":"a","type":"text","layout":{"x":0,"y":0,"w":10,"h":10}}]}`, "duplicate element id"},
		{"refresh below floor", `{"version":1,"canvas":{"width":1280,"height":720},"elements":[],
			"refresh":{"enabled":true,"intervalSeconds":2}}`, "floor"},
		{"not json", `{nope`, "JSON"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := do(t, "PUT", url, tc.doc, nil)
			if res.StatusCode != http.StatusUnprocessableEntity {
				t.Fatalf("expected 422, got %d", res.StatusCode)
			}
			if msg := bodyJSON(t, res)["error"].(string); !strings.Contains(msg, tc.want) {
				t.Errorf("expected error mentioning %q, got %q", tc.want, msg)
			}
		})
	}

	// Disabled refresh below the floor is fine (only live refresh matters).
	ok := `{"version":1,"canvas":{"width":1280,"height":720},"elements":[],
		"refresh":{"enabled":false,"intervalSeconds":2}}`
	if res := do(t, "PUT", url, ok, nil); res.StatusCode != http.StatusCreated {
		t.Errorf("disabled refresh below floor should save, got %d", res.StatusCode)
	}
}

func TestExternalEdits(t *testing.T) {
	ts, root := newTestServer(t)

	// A file written directly to disk (git pull, agent, editor) appears.
	if err := os.MkdirAll(filepath.Join(root, "Ops"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Ops", "External.json"), []byte(validDoc()), 0644); err != nil {
		t.Fatal(err)
	}
	// A mangled file appears too — flagged, not hidden.
	if err := os.WriteFile(filepath.Join(root, "Ops", "Broken.json"), []byte(`{"version": 1, "canv`), 0644); err != nil {
		t.Fatal(err)
	}

	res := do(t, "GET", ts.URL+"/api/dash/dashboards", "", nil)
	var list []map[string]any
	if err := json.NewDecoder(res.Body).Decode(&list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 entries, got %d: %+v", len(list), list)
	}
	// Externally created mixed-case files are listed under lower-cased identities.
	byPath := map[string]map[string]any{}
	for _, e := range list {
		byPath[e["path"].(string)] = e
	}
	if byPath["ops/external"]["valid"] != true {
		t.Errorf("external valid file should be valid: %+v", byPath["ops/external"])
	}
	broken := byPath["ops/broken"]
	if broken["valid"] != false || broken["error"] == nil {
		t.Errorf("broken file should be listed invalid with an error: %+v", broken)
	}

	// GET of the broken file returns the raw text for repair.
	res = do(t, "GET", ts.URL+"/api/dash/dashboards/Ops/Broken", "", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("get broken: expected 200, got %d", res.StatusCode)
	}
	doc := bodyJSON(t, res)
	if doc["valid"] != false || !strings.Contains(doc["raw"].(string), "canv") {
		t.Errorf("broken doc envelope should carry raw text: %+v", doc)
	}

	// An external in-place edit is picked up on the next read (mtime/size).
	res = do(t, "GET", ts.URL+"/api/dash/dashboards/Ops/External", "", nil)
	firstETag := res.Header.Get("ETag")
	edited := strings.Replace(validDoc(), `"# Hello"`, `"# Edited on disk"`, 1)
	if err := os.WriteFile(filepath.Join(root, "Ops", "External.json"), []byte(edited), 0644); err != nil {
		t.Fatal(err)
	}
	res = do(t, "GET", ts.URL+"/api/dash/dashboards/Ops/External", "", nil)
	if res.Header.Get("ETag") == firstETag {
		t.Error("external edit should change the served ETag without restart")
	}
}

func TestPathValidation(t *testing.T) {
	ts, root := newTestServer(t)

	bad := []string{
		"..%2Fescape",     // traversal
		"a%2F..%2F..%2Fb", // nested traversal
		"con%3Aaux",       // invalid characters
		"theme",           // reserved for the global theme
		"x.json",          // extension must be implicit
		"trailing.%20",    // segment ending in space
	}
	for _, p := range bad {
		res := do(t, "PUT", ts.URL+"/api/dash/dashboards/"+p, validDoc(), nil)
		if res.StatusCode != http.StatusBadRequest && res.StatusCode != http.StatusNotFound {
			t.Errorf("path %q: expected 400/404, got %d", p, res.StatusCode)
		}
	}

	// Nothing escaped the root.
	entries, err := os.ReadDir(filepath.Dir(root))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != filepath.Base(root) {
			t.Errorf("unexpected file outside root: %s", e.Name())
		}
	}
}

func TestAssets(t *testing.T) {
	ts, _ := newTestServer(t)

	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 64)...)

	// Upload + fetch a PNG.
	res := do(t, "POST", ts.URL+"/api/dash/assets/Sales/assets/bg.png", string(png), nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("upload png: expected 201, got %d", res.StatusCode)
	}
	res = do(t, "GET", ts.URL+"/api/dash/assets/Sales/assets/bg.png", "", nil)
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("get png: got %d / %s", res.StatusCode, res.Header.Get("Content-Type"))
	}

	// Disallowed extension → 415.
	res = do(t, "POST", ts.URL+"/api/dash/assets/evil.html", "<script>", nil)
	if res.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("html asset: expected 415, got %d", res.StatusCode)
	}

	// Oversize → 413 (test server caps at 1 MiB).
	big := strings.Repeat("x", (1<<20)+1)
	res = do(t, "POST", ts.URL+"/api/dash/assets/big.png", big, nil)
	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize: expected 413, got %d", res.StatusCode)
	}

	// SVG is served with a no-execute CSP.
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`
	res = do(t, "POST", ts.URL+"/api/dash/assets/logo.svg", svg, nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("upload svg: expected 201, got %d", res.StatusCode)
	}
	res = do(t, "GET", ts.URL+"/api/dash/assets/logo.svg", "", nil)
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("svg should carry a no-execute CSP, got %q", csp)
	}
	if res.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("assets should be served with nosniff")
	}
}

func TestTheme(t *testing.T) {
	ts, _ := newTestServer(t)

	// Missing theme reads as {} with no ETag.
	res := do(t, "GET", ts.URL+"/api/dash/theme", "", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("get empty theme: %d", res.StatusCode)
	}
	if m := bodyJSON(t, res); len(m) != 0 {
		t.Errorf("empty theme should be {}, got %+v", m)
	}

	// Create, then update with If-Match; reject non-objects.
	res = do(t, "PUT", ts.URL+"/api/dash/theme", `{"palette":["#89b4fa","#fab387"]}`, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("put theme: expected 200, got %d", res.StatusCode)
	}
	etag := res.Header.Get("ETag")

	res = do(t, "PUT", ts.URL+"/api/dash/theme", `["not","an","object"]`, map[string]string{"If-Match": etag})
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("array theme: expected 422, got %d", res.StatusCode)
	}

	res = do(t, "PUT", ts.URL+"/api/dash/theme", `{"palette":[]}`, map[string]string{"If-Match": etag})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("update theme: expected 200, got %d", res.StatusCode)
	}

	res = do(t, "GET", ts.URL+"/api/dash/theme", "", nil)
	if m := bodyJSON(t, res); fmt.Sprint(m["palette"]) != "[]" {
		t.Errorf("theme round trip failed: %+v", m)
	}
}

func TestSchemaServed(t *testing.T) {
	ts, _ := newTestServer(t)
	res := do(t, "GET", ts.URL+"/api/dash/schema.json", "", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("schema: %d", res.StatusCode)
	}
	var schema map[string]any
	if err := json.NewDecoder(res.Body).Decode(&schema); err != nil {
		t.Fatalf("schema is not JSON: %v", err)
	}
	if schema["title"] == nil || schema["$defs"] == nil {
		t.Errorf("schema looks wrong: keys %v", len(schema))
	}
}

// TestImportRule enforces the module boundary: the dashboards package must
// never depend on the DUX query engine or go-duckdb, so the standalone duxuid
// binary stays CGO-free.
func TestImportRule(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go binary not available")
	}
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	forbidden := []string{
		"github.com/danielwikar/dux/semantic",
		"github.com/danielwikar/dux/executor",
		"github.com/danielwikar/dux/emitter",
		"github.com/danielwikar/dux/parser",
		"github.com/duckdb/duckdb-go",
	}
	deps := string(out)
	for _, f := range forbidden {
		if strings.Contains(deps, f) {
			t.Errorf("dashboards must not depend on %s (breaks the CGO-free duxuid boundary)", f)
		}
	}
}
