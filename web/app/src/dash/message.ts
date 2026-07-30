/** Presentation casing for server and runtime messages without rewriting the
 * underlying error contract. */
export function displayMessage(value: unknown): string {
  if (value === null || value === undefined) return "";
  const text = value instanceof Error ? value.message : String(value);
  return text.replace(/^([^A-Za-z]*)([a-z])/, (_, prefix: string, first: string) => prefix + first.toUpperCase());
}
