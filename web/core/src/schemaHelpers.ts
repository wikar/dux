// Tables whose names start with "dux_" (or come from the "dux" metadata DB)
// are internal — hide from public views.
export const isMetaTable = (name: string): boolean => {
  const dot = name.indexOf(".");
  if (dot >= 0) return name.slice(0, dot) === "dux";
  return name.startsWith("dux_");
};

// Resolve a possibly bare table name (e.g. "matches") to its fully-qualified
// schema key (e.g. "analytics.Sales") using the list of known table keys.
export const resolveTable = (name: string, tableKeys: string[]): string => {
  if (tableKeys.includes(name)) return name;
  const match = tableKeys.find((k) => k.endsWith("." + name));
  return match ?? name;
};

// A column data type that can hold calendar dates (eligible as a date-table
// date column).
export const isDateType = (dataType: string): boolean => {
  const dt = dataType.toUpperCase();
  return dt === "DATE" || dt.startsWith("TIMESTAMP");
};
