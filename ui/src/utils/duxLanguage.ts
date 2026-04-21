import type { LanguageFn } from "highlight.js";

// DUX highlight.js language definition.
// Token mapping (Catppuccin Mocha):
//   keyword  → control keywords (DEFINE, EVALUATE, MEASURE, RETURN, VAR) → mauve
//   built_in → function names                                              → blue
//   string   → double-quoted string literals                               → green
//   title    → [Column Reference]                                          → peach
//   meta     → 'Quoted Table Name'                                         → teal
//   number   → numeric literals                                            → peach
//   comment  → // and -- line comments                                     → surface2

const duxLanguage: LanguageFn = (hljs) => ({
  name: "DUX",
  case_insensitive: true,
  keywords: {
    keyword: "DEFINE EVALUATE MEASURE RETURN VAR AND OR NOT TRUE FALSE",
    built_in:
      "SUM AVERAGE COUNT COUNTA COUNTBLANK COUNTROWS DISTINCTCOUNT " +
      "MIN MAX MEDIAN SUMX AVERAGEX COUNTX MINX MAXX CONCATENATEX " +
      "SUMMARIZECOLUMNS FILTER CALCULATE ADDCOLUMNS SELECTCOLUMNS " +
      "TOPN UNION INTERSECT EXCEPT VALUES DISTINCT TREATAS " +
      "DIVIDE ISBLANK BLANK IF SWITCH",
  },
  contains: [
    hljs.COMMENT("//", "$"),
    hljs.COMMENT("--", "$"),
    // Double-quoted string literals
    { className: "string",  begin: '"', end: '"' },
    // [Column Reference]
    { className: "title",   begin: "\\[", end: "\\]" },
    // 'Quoted Table Name'
    { className: "meta",    begin: "'", end: "'" },
    // Numeric literals
    hljs.C_NUMBER_MODE,
  ],
});

export default duxLanguage;
