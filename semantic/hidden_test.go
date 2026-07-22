package semantic_test

import (
	"strings"
	"testing"

	"github.com/danielwikar/dux/semantic"
)

func TestHiddenTOMLRoundTrip(t *testing.T) {
	schema := semantic.NewSchema()
	schema.SetTableHidden("bev.Venue", true)
	schema.SetColumnHidden("bev.Sales", "OrderId", true)

	data, err := semantic.ExportDuxTOML(schema)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "[[hidden]]") {
		t.Fatalf("export missing [[hidden]] entries:\n%s", text)
	}

	reloaded := semantic.NewSchema()
	if err := semantic.LoadDuxTOMLBytes(data, reloaded); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !reloaded.HiddenTables["bev.venue"] {
		t.Errorf("table hidden flag lost in round trip")
	}
	if !reloaded.HiddenColumns["bev.sales"]["orderid"] {
		t.Errorf("column hidden flag lost in round trip")
	}
}

func TestSetHiddenStampsLiveTables(t *testing.T) {
	schema := semantic.NewSchema()
	schema.Tables["bev.Sales"] = &semantic.Table{
		Name: "Sales",
		Columns: map[string]*semantic.Column{
			"OrderId": {Name: "OrderId", DataType: "VARCHAR"},
		},
	}

	// Case-insensitive application onto the live structs.
	schema.SetTableHidden("BEV.SALES", true)
	schema.SetColumnHidden("bev.sales", "orderid", true)
	if !schema.Tables["bev.Sales"].Hidden {
		t.Errorf("table Hidden flag not stamped")
	}
	if !schema.Tables["bev.Sales"].Columns["OrderId"].Hidden {
		t.Errorf("column Hidden flag not stamped")
	}

	schema.SetTableHidden("bev.Sales", false)
	if schema.Tables["bev.Sales"].Hidden {
		t.Errorf("table Hidden flag not cleared")
	}

	schema.ClearHidden()
	if schema.Tables["bev.Sales"].Columns["OrderId"].Hidden {
		t.Errorf("ClearHidden did not clear column flag")
	}

	// ApplyHiddenFlags re-stamps from the maps after re-introspection.
	schema.SetColumnHidden("bev.sales", "orderid", true)
	schema.Tables["bev.Sales"].Columns["OrderId"].Hidden = false
	schema.ApplyHiddenFlags()
	if !schema.Tables["bev.Sales"].Columns["OrderId"].Hidden {
		t.Errorf("ApplyHiddenFlags did not re-stamp column flag")
	}
}
