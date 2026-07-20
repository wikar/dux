package semantic

import (
	"fmt"
	"strings"
)

// MeasureFormat is the display-format metadata attached to a measure. It is a
// structured enum rather than a free-form format string so it can be validated,
// edited with plain form controls, and rendered locale-aware by clients (e.g.
// via Intl.NumberFormat in the browser).
type MeasureFormat struct {
	// Kind selects the format family: "number", "decimal", "percent",
	// "currency", or "compact".
	Kind string `json:"kind" toml:"kind"`
	// Decimals is the number of fraction digits (0–10). Nil means the client's
	// default for the kind.
	Decimals *int `json:"decimals,omitempty" toml:"decimals,omitempty"`
	// Currency is the ISO 4217 code (e.g. "SEK"). Required when Kind is
	// "currency", empty otherwise.
	Currency string `json:"currency,omitempty" toml:"currency,omitempty"`
}

// formatKinds is the set of valid MeasureFormat.Kind values.
var formatKinds = map[string]bool{
	"number": true, "decimal": true, "percent": true, "currency": true, "compact": true,
}

// Validate reports whether the format is well-formed.
func (f *MeasureFormat) Validate() error {
	if !formatKinds[f.Kind] {
		return fmt.Errorf("format kind %q is not one of number, decimal, percent, currency, compact", f.Kind)
	}
	if f.Decimals != nil && (*f.Decimals < 0 || *f.Decimals > 10) {
		return fmt.Errorf("format decimals must be between 0 and 10, got %d", *f.Decimals)
	}
	if f.Kind == "currency" {
		if len(f.Currency) != 3 {
			return fmt.Errorf("currency format requires a 3-letter ISO 4217 code, got %q", f.Currency)
		}
	} else if f.Currency != "" {
		return fmt.Errorf("currency code is only valid for the currency kind")
	}
	return nil
}

// SetMeasureFormat attaches format to the measure table[name], keyed with the
// same canonicalisation as Schema.Measures (single quotes stripped, casing
// preserved). A nil format removes any stored format.
func (s *Schema) SetMeasureFormat(table, name string, format *MeasureFormat) {
	t := StripSingleQuotes(table)
	if format == nil {
		s.DeleteMeasureFormat(t, name)
		return
	}
	if s.MeasureFormats == nil {
		s.MeasureFormats = make(map[string]map[string]*MeasureFormat)
	}
	if s.MeasureFormats[t] == nil {
		s.MeasureFormats[t] = make(map[string]*MeasureFormat)
	}
	s.MeasureFormats[t][name] = format
}

// DeleteMeasureFormat removes the stored format for table[name], if any.
func (s *Schema) DeleteMeasureFormat(table, name string) {
	t := StripSingleQuotes(table)
	if formats, ok := s.MeasureFormats[t]; ok {
		delete(formats, name)
		if len(formats) == 0 {
			delete(s.MeasureFormats, t)
		}
	}
}

// MeasureFormatFor returns the stored format for table[name], or nil.
func (s *Schema) MeasureFormatFor(table, name string) *MeasureFormat {
	t := StripSingleQuotes(table)
	if formats, ok := s.MeasureFormats[t]; ok {
		return formats[name]
	}
	// Case-insensitive fallback, matching FindTable's behaviour.
	lower := strings.ToLower(t)
	for k, formats := range s.MeasureFormats {
		if strings.ToLower(k) == lower {
			for n, f := range formats {
				if strings.EqualFold(n, name) {
					return f
				}
			}
		}
	}
	return nil
}
