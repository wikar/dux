package semantic_test

import (
	"strings"
	"testing"

	"github.com/danielwikar/dux/semantic"
)

func TestHiddenTOMLRoundTrip(t *testing.T) {
	schema := semantic.NewSchema()
	schema.SetTableHidden("atp.rounds", true)
	schema.SetColumnHidden("atp.matches", "winner_id", true)

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
	if !reloaded.HiddenTables["atp.rounds"] {
		t.Errorf("table hidden flag lost in round trip")
	}
	if !reloaded.HiddenColumns["atp.matches"]["winner_id"] {
		t.Errorf("column hidden flag lost in round trip")
	}
}

func TestSetHiddenStampsLiveTables(t *testing.T) {
	schema := semantic.NewSchema()
	schema.Tables["atp.Matches"] = &semantic.Table{
		Name: "Matches",
		Columns: map[string]*semantic.Column{
			"Winner_ID": {Name: "Winner_ID", DataType: "BIGINT"},
		},
	}

	// Case-insensitive application onto the live structs.
	schema.SetTableHidden("ATP.MATCHES", true)
	schema.SetColumnHidden("atp.matches", "winner_id", true)
	if !schema.Tables["atp.Matches"].Hidden {
		t.Errorf("table Hidden flag not stamped")
	}
	if !schema.Tables["atp.Matches"].Columns["Winner_ID"].Hidden {
		t.Errorf("column Hidden flag not stamped")
	}

	schema.SetTableHidden("atp.Matches", false)
	if schema.Tables["atp.Matches"].Hidden {
		t.Errorf("table Hidden flag not cleared")
	}

	schema.ClearHidden()
	if schema.Tables["atp.Matches"].Columns["Winner_ID"].Hidden {
		t.Errorf("ClearHidden did not clear column flag")
	}

	// ApplyHiddenFlags re-stamps from the maps after re-introspection.
	schema.SetColumnHidden("atp.matches", "winner_id", true)
	schema.Tables["atp.Matches"].Columns["Winner_ID"].Hidden = false
	schema.ApplyHiddenFlags()
	if !schema.Tables["atp.Matches"].Columns["Winner_ID"].Hidden {
		t.Errorf("ApplyHiddenFlags did not re-stamp column flag")
	}
}
