package dash

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// schemaURLRe matches the validator's header line, which embeds the compiled
// schema's resource URL (a local file path) — noise we keep out of API errors.
var schemaURLRe = regexp.MustCompile(`jsonschema validation failed with '[^']*'`)

// cleanSchemaError rewrites the validator's multi-line report into a compact,
// path-free message: "does not match the dashboard schema: at '/x': ...".
func cleanSchemaError(msg string) string {
	msg = schemaURLRe.ReplaceAllString(msg, "")
	var parts []string
	for _, line := range strings.Split(msg, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
		if line != "" {
			parts = append(parts, line)
		}
	}
	return "does not match the dashboard schema: " + strings.Join(parts, "; ")
}

// schemaJSON is the dashboard document JSON Schema (v1). It is both served at
// GET /api/dash/schema.json (so editors and agents can validate before saving)
// and used server-side for validation — one source of truth.
//
//go:embed schema.json
var schemaJSON []byte

// refreshFloorSeconds is the minimum allowed live-refresh interval.
const refreshFloorSeconds = 5

// validator validates dashboard documents against the embedded schema plus
// the structural rules a JSON Schema cannot express (unique element ids,
// refresh floor).
type validator struct {
	schema *jsonschema.Schema
}

func newValidator() (*validator, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaJSON))
	if err != nil {
		return nil, fmt.Errorf("parse embedded dashboard schema: %w", err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("dashboard.schema.json", doc); err != nil {
		return nil, fmt.Errorf("register dashboard schema: %w", err)
	}
	schema, err := c.Compile("dashboard.schema.json")
	if err != nil {
		return nil, fmt.Errorf("compile dashboard schema: %w", err)
	}
	return &validator{schema: schema}, nil
}

// Validate checks data as a dashboard document. The returned error message is
// user-facing (surfaced in 422 responses and listing error badges).
func (v *validator) Validate(data []byte) error {
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("not valid JSON: %w", err)
	}
	if err := v.schema.Validate(inst); err != nil {
		return errors.New(cleanSchemaError(err.Error()))
	}

	// Rules beyond the schema: decode just the parts we need.
	var doc struct {
		Elements []struct {
			ID string `json:"id"`
		} `json:"elements"`
		Refresh *struct {
			Enabled         bool `json:"enabled"`
			IntervalSeconds int  `json:"intervalSeconds"`
		} `json:"refresh"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("not valid JSON: %w", err)
	}
	seen := make(map[string]bool, len(doc.Elements))
	for _, el := range doc.Elements {
		if seen[el.ID] {
			return fmt.Errorf("duplicate element id %q", el.ID)
		}
		seen[el.ID] = true
	}
	if doc.Refresh != nil && doc.Refresh.Enabled &&
		doc.Refresh.IntervalSeconds < refreshFloorSeconds {
		return fmt.Errorf("refresh.intervalSeconds %d is below the server floor of %d seconds",
			doc.Refresh.IntervalSeconds, refreshFloorSeconds)
	}
	return nil
}
