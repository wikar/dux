package semantic

import (
	"strings"
	"testing"
)

// star schema: two facts on one dimension, plus a product dim on fact_sales
// and a bidirectional bridge edge for the bidi case.
func filterSchema() *Schema {
	s := NewSchema()
	for _, name := range []string{"dates", "fact_sales", "fact_returns", "products", "categories", "bridge"} {
		s.Tables[name] = &Table{Name: name, Columns: map[string]*Column{}}
	}
	s.Relationships = append(s.Relationships,
		&Relationship{FromTable: "fact_sales", FromColumn: "datekey", ToTable: "dates", ToColumn: "datekey"},
		&Relationship{FromTable: "fact_returns", FromColumn: "datekey", ToTable: "dates", ToColumn: "datekey"},
		&Relationship{FromTable: "fact_sales", FromColumn: "pkey", ToTable: "products", ToColumn: "pkey"},
		&Relationship{FromTable: "products", FromColumn: "categorykey", ToTable: "categories", ToColumn: "categorykey"},
		&Relationship{FromTable: "bridge", FromColumn: "pkey", ToTable: "products", ToColumn: "pkey", Bidirectional: true},
	)
	return s
}

func TestFilterReaches(t *testing.T) {
	s := filterSchema()
	cases := []struct {
		src     string
		targets []string
		want    bool
	}{
		// dimension → its fact: propagates
		{"dates", []string{"fact_sales"}, true},
		{"dates", []string{"fact_returns"}, true},
		{"products", []string{"fact_sales"}, true},
		{"categories", []string{"fact_sales"}, true},
		// dimension → unrelated fact THROUGH another fact: must NOT propagate
		{"products", []string{"fact_returns"}, false},
		{"products", []string{"dates"}, false},
		// fact → its dimension: against filter direction, no propagation
		{"fact_sales", []string{"dates"}, false},
		// bidirectional edge propagates many → one
		{"bridge", []string{"products"}, true},
		{"bridge", []string{"fact_sales"}, true}, // bridge ↔ products → fact_sales
		// target set containing the source itself
		{"products", []string{"products"}, true},
	}
	for _, c := range cases {
		if got := FilterReaches(s, c.src, c.targets); got != c.want {
			t.Errorf("FilterReaches(%s → %v) = %v, want %v", c.src, c.targets, got, c.want)
		}
	}

	qualified := NewSchema()
	for _, name := range []string{"analytics.dates", "analytics.fact_sales"} {
		qualified.Tables[name] = &Table{Name: name, Columns: map[string]*Column{}}
	}
	qualified.Relationships = append(qualified.Relationships, &Relationship{
		FromTable: "analytics.fact_sales", FromColumn: "datekey",
		ToTable: "analytics.dates", ToColumn: "datekey",
	})
	if !GroupingReaches(qualified, "dates", []string{"analytics.fact_sales"}) {
		t.Fatal("qualified one-side to many-side path was not resolved")
	}
}

func TestGroupingReaches(t *testing.T) {
	s := filterSchema()
	cases := []struct {
		src     string
		targets []string
		want    bool
	}{
		{"dates", []string{"fact_sales"}, true},
		{"products", []string{"fact_sales"}, true},
		{"categories", []string{"fact_sales"}, true},
		{"fact_sales", []string{"dates"}, false},
		{"fact_returns", []string{"fact_sales"}, false},
		{"bridge", []string{"products"}, false},
		{"products", []string{"products"}, true},
	}
	for _, c := range cases {
		if got := GroupingReaches(s, c.src, c.targets); got != c.want {
			t.Errorf("GroupingReaches(%s → %v) = %v, want %v", c.src, c.targets, got, c.want)
		}
	}
}

func TestInferJoinPathRejectsAmbiguousBareTable(t *testing.T) {
	s := NewSchema()
	for _, name := range []string{"a.sales", "a.region", "b.sales", "b.region"} {
		s.Tables[name] = &Table{Name: name, Columns: map[string]*Column{}}
	}
	s.Relationships = append(s.Relationships,
		&Relationship{FromTable: "a.sales", FromColumn: "region_id", ToTable: "a.region", ToColumn: "id"},
		&Relationship{FromTable: "b.sales", FromColumn: "region_id", ToTable: "b.region", ToColumn: "id"},
	)

	_, err := InferJoinPath(s, []string{"sales", "a.region"})
	if err == nil || !strings.Contains(err.Error(), `ambiguous table "sales"`) {
		t.Fatalf("got %v, want an ambiguous table error", err)
	}

	path, err := InferJoinPath(s, []string{"a.sales", "a.region"})
	if err != nil {
		t.Fatal(err)
	}
	if len(path.Steps) != 1 || path.Steps[0].Table != "a.region" {
		t.Fatalf("unexpected qualified path: %#v", path.Steps)
	}
}

func TestJoinPathLeafEdge(t *testing.T) {
	s := NewSchema()
	for _, name := range []string{"analytics.orders", "analytics.dates", "analytics.fiscal"} {
		s.Tables[name] = &Table{Name: name, Columns: map[string]*Column{}}
	}
	leaf := JoinStep{FromTable: "analytics.orders", Table: "analytics.dates", OnFromCol: "datekey", OnToCol: "datekey"}

	if got, ok := (&JoinPath{Steps: []JoinStep{leaf}}).LeafEdge(s, "dates"); !ok || got != leaf {
		t.Fatalf("qualified leaf = %#v, %v", got, ok)
	}
	if _, ok := (&JoinPath{Steps: []JoinStep{leaf}}).LeafEdge(s, "orders"); ok {
		t.Fatal("primary table reported as leaf")
	}
	branched := &JoinPath{Steps: []JoinStep{
		leaf,
		{FromTable: "analytics.dates", Table: "analytics.fiscal", OnFromCol: "year", OnToCol: "year"},
	}}
	if _, ok := branched.LeafEdge(s, "dates"); ok {
		t.Fatal("table with a child reported as leaf")
	}
	if _, ok := branched.LeafEdge(s, "missing"); ok {
		t.Fatal("absent table reported as leaf")
	}
}

func TestInferJoinPathRejectsAmbiguousUnidirectionalPath(t *testing.T) {
	s := NewSchema()
	for _, name := range []string{"a", "b", "c", "d"} {
		s.Tables[name] = &Table{Name: name, Columns: map[string]*Column{}}
	}
	s.Relationships = []*Relationship{
		{FromTable: "a", ToTable: "b"},
		{FromTable: "b", ToTable: "d"},
		{FromTable: "a", ToTable: "c"},
		{FromTable: "c", ToTable: "d"},
	}
	if _, err := InferJoinPath(s, []string{"a", "d"}); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguous path error, got %v", err)
	}
}
