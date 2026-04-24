import type { LanguageFn } from "highlight.js";
import { DUX_KEYWORDS, DUX_BUILTINS } from "./duxKeywords";

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
    keyword: DUX_KEYWORDS.join(" "),
    built_in: DUX_BUILTINS.join(" "),
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
