import styles from "./TypeIcon.module.css";

const TYPE_MAP: Array<[RegExp, string]> = [
  [/^(TEXT|VARCHAR|CHAR|BLOB|STRING)/i,    "abc"],
  [/^(TINYINT|SMALLINT|INTEGER|BIGINT|HUGEINT|UBIGINT|UINTEGER)/i, "123"],
  [/^(DOUBLE|FLOAT|REAL|DECIMAL|NUMERIC)/i, "1.2"],
  [/^(DATE|TIMESTAMP)/i,                   "📅"],
  [/^BOOLEAN/i,                            "T/F"],
];

function iconFor(dataType: string): string {
  for (const [re, icon] of TYPE_MAP) {
    if (re.test(dataType)) return icon;
  }
  return "?";
}

export default function TypeIcon({ dataType }: { dataType: string }) {
  return (
    <span className={styles.icon} title={dataType}>
      {iconFor(dataType)}
    </span>
  );
}
