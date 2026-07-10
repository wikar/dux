import type { Schema } from "./types";
import { DUX_KEYWORDS, DUX_BUILTINS } from "./duxKeywords";
import { isMetaTable } from "./schemaHelpers";

export interface CompletionOptions {
  /** Hide internal dux_* meta tables from table-name suggestions. */
  excludeMetaTables?: boolean;
}

/**
 * Returns the ghost-text suffix to append at the cursor position.
 * Empty string = no suggestion.
 *
 * For [ and ' contexts the ghost always includes the closing char so accepting
 * it (Tab) produces the full token.
 */
export function getCompletion(
  text: string,
  pos: number,
  schema: Schema | undefined,
  opts: CompletionOptions = {}
): string {
  if (pos === 0) return "";
  const before = text.slice(0, pos);

  const tableKeys = (): string[] => {
    if (!schema) return [];
    const keys = Object.keys(schema.Tables);
    return opts.excludeMetaTables ? keys.filter((n) => !isMetaTable(n)) : keys;
  };

  // Inside [ ... ] → complete a field / measure name
  const lastBracketOpen = before.lastIndexOf("[");
  const lastBracketClose = before.lastIndexOf("]");
  if (lastBracketOpen > lastBracketClose) {
    const typed = before.slice(lastBracketOpen + 1);

    // Detect an immediately preceding table name: word chars or 'Quoted Name'
    const precedingToken = before.slice(0, lastBracketOpen);
    let contextTable: string | undefined;
    if (schema) {
      // Try 'Quoted Table Name'[
      const quotedMatch = precedingToken.match(/'([^']+)'\s*$/);
      if (quotedMatch) {
        const name = quotedMatch[1];
        contextTable = Object.keys(schema.Tables).find(
          (t) => t.toLowerCase() === name.toLowerCase()
        );
      }
      // Try bare word identifier[
      if (!contextTable) {
        const bareMatch = precedingToken.match(/(\w+)\s*$/);
        if (bareMatch) {
          const name = bareMatch[1];
          contextTable = Object.keys(schema.Tables).find(
            (t) => t.toLowerCase() === name.toLowerCase()
          );
        }
      }
    }

    const fieldNames: string[] = [];
    if (schema) {
      if (contextTable) {
        // Only fields from the matched table
        const tbl = schema.Tables[contextTable];
        if (tbl) for (const col of Object.values(tbl.Columns)) fieldNames.push(col.Name);
        const measures = schema.Measures?.[contextTable];
        if (measures) for (const name of Object.keys(measures)) fieldNames.push(name);
      } else {
        // No table context — all fields
        for (const table of Object.values(schema.Tables))
          for (const col of Object.values(table.Columns)) fieldNames.push(col.Name);
        if (schema.Measures)
          for (const measures of Object.values(schema.Measures))
            for (const name of Object.keys(measures)) fieldNames.push(name);
      }
    }

    const upper = typed.toUpperCase();
    const match = fieldNames.find((f) =>
      typed.length === 0
        ? true
        : f.toUpperCase().startsWith(upper) && f.toUpperCase() !== upper
    );
    if (!match) return "";
    return match.slice(typed.length) + "]";
  }

  // Inside ' ... ' → complete a table name
  const quotesBefore = (before.match(/'/g) || []).length;
  if (quotesBefore % 2 === 1) {
    const lastQuote = before.lastIndexOf("'");
    const typed = before.slice(lastQuote + 1);
    const upper = typed.toUpperCase();
    const match = tableKeys().find((t) =>
      typed.length === 0
        ? true
        : t.toUpperCase().startsWith(upper) && t.toUpperCase() !== upper
    );
    if (!match) return "";
    return match.slice(typed.length) + "'";
  }

  // Plain identifier → keyword / built-in / table name
  let start = pos;
  while (start > 0 && /\w/.test(text[start - 1])) start--;
  const word = text.slice(start, pos);
  if (word.length < 1) return "";
  const upper = word.toUpperCase();
  const match = [...DUX_KEYWORDS, ...DUX_BUILTINS, ...tableKeys()].find(
    (c) => c.toUpperCase().startsWith(upper) && c.toUpperCase() !== upper
  );
  return match ? match.slice(word.length) : "";
}
