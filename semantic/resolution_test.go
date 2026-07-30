package semantic_test

import (
	"testing"

	"github.com/danielwikar/dux/parser"
	"github.com/danielwikar/dux/semantic"
)

func resolutionSchema(t *testing.T) *semantic.Schema {
	t.Helper()
	schema := semantic.NewSchema()
	schema.Tables["sales"] = &semantic.Table{Name: "sales", Columns: map[string]*semantic.Column{
		"product": {Name: "product"}, "amount": {Name: "amount"},
	}}
	schema.Tables["products"] = &semantic.Table{Name: "products", Columns: map[string]*semantic.Column{
		"product": {Name: "product"}, "category": {Name: "category"},
	}}
	if err := schema.AddMeasureFromExpr("sales", "Total Amount", "SUM(sales[amount])"); err != nil {
		t.Fatal(err)
	}
	return schema
}

func resolveQuery(t *testing.T, input string) (*parser.Query, *semantic.Resolution) {
	t.Helper()
	query, err := parser.Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	resolver := &semantic.Resolver{Schema: resolutionSchema(t)}
	if err := resolver.Resolve(query); err != nil {
		t.Fatal(err)
	}
	return query, resolver.Result()
}

func TestResolutionPreservesReferenceKinds(t *testing.T) {
	query, result := resolveQuery(t, `EVALUATE ROW("M", [Total Amount], "C", sales[amount])`)
	fc := query.Evaluate.Table.Func
	measure := fc.Args[1].Left.ColRef
	column := fc.Args[3].Left.ColRef
	if ref := result.Refs[measure]; ref.Kind != semantic.RefMeasure || ref.Table != "sales" || ref.Measure == nil {
		t.Fatalf("measure ref = %#v", ref)
	}
	if ref := result.Refs[column]; ref.Kind != semantic.RefColumn || ref.Table != "sales" || ref.Column != "amount" {
		t.Fatalf("column ref = %#v", ref)
	}
}

func TestResolutionTracksProjectionAndVarLineage(t *testing.T) {
	query, result := resolveQuery(t, `EVALUATE
		VAR ps = SELECTCOLUMNS(products, "Renamed", products[product], "Computed", products[product] & "x")
		RETURN ps`)
	shape := result.VarShapes["ps"]
	if !shape.Known || len(shape.Columns) != 2 {
		t.Fatalf("shape = %#v", shape)
	}
	if shape.Columns[0].Output != "Renamed" || shape.Columns[0].Lineage == nil || shape.Columns[0].Lineage.Column != "product" {
		t.Fatalf("renamed = %#v", shape.Columns[0])
	}
	if shape.Columns[1].Lineage != nil {
		t.Fatalf("computed column unexpectedly has lineage: %#v", shape.Columns[1])
	}
	if !result.TableShapes[query.Evaluate.Table].Known {
		t.Fatal("RETURN var shape is unknown")
	}
}

func TestResolutionSetOperationLineage(t *testing.T) {
	query, result := resolveQuery(t, `EVALUATE UNION(
		SELECTCOLUMNS(products, "P", products[product]),
		SELECTCOLUMNS(products, "P", products[product]))`)
	shape := result.TableShapes[query.Evaluate.Table]
	if len(shape.Columns) != 1 || shape.Columns[0].Lineage == nil {
		t.Fatalf("shape = %#v", shape)
	}
}
