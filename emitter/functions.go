package emitter

import (
	"sort"
	"strings"
)

//go:generate go run ../cmd/genfuncs

var tableFuncs = map[string]bool{
	"FILTER": true, "SUMMARIZECOLUMNS": true, "ROW": true,
	"ADDCOLUMNS": true, "SELECTCOLUMNS": true, "UNION": true,
	"INTERSECT": true, "EXCEPT": true, "TOPN": true, "DISTINCT": true,
	"VALUES": true, "ALL": true, "ALLEXCEPT": true, "CROSSJOIN": true,
	"GENERATE": true, "GENERATEALL": true, "DATESYTD": true,
	"DATESQTD": true, "DATESMTD": true, "SAMEPERIODLASTYEAR": true,
	"DATEADD": true, "PREVIOUSYEAR": true, "PREVIOUSQUARTER": true,
	"PREVIOUSMONTH": true, "PREVIOUSDAY": true, "NEXTYEAR": true,
	"NEXTQUARTER": true, "NEXTMONTH": true, "NEXTDAY": true,
	"DATESBETWEEN": true, "DATESINPERIOD": true, "CALENDAR": true,
	"CALENDARAUTO": true, "RELATEDTABLE": true,
	"CALCULATETABLE": true,
}

var coreFuncs = []string{
	"COUNTBLANK", "COUNTROWS", "DISTINCTCOUNT", "CONCATENATEX",
	"CALCULATE", "TREATAS", "REMOVEFILTERS", "KEEPFILTERS",
	"TOTALYTD", "TOTALQTD", "TOTALMTD", "LASTNONBLANK", "DATE",
	"RELATED", "DIVIDE", "ISBLANK", "BLANK", "IF", "SWITCH",
	"NOT", "AND", "OR", "ROLLUPADDISSUBTOTAL", "ROLLUPGROUP",
}

func isTableFunc(name string) bool { return tableFuncs[strings.ToUpper(name)] }

// ImplementedFunctions is the function vocabulary shared with editor tooling.
func ImplementedFunctions() []string {
	set := map[string]bool{}
	for name := range simpleAggs {
		set[name] = true
	}
	for name := range iterAggs {
		set[name] = true
	}
	for name := range tableFuncs {
		set[name] = true
	}
	for name := range scalarFuncs {
		set[name] = true
	}
	for name := range identityFuncs {
		set[name] = true
	}
	for _, name := range coreFuncs {
		set[name] = true
	}
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
