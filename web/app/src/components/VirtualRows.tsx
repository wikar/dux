import { useVirtualizer } from "@tanstack/react-virtual";
import { Fragment, type ReactNode, type RefObject } from "react";

/** Shared row window for flat result tables. Feature tables keep their own
 *  sorting, cell rendering, and row interactions through the children slot. */
export default function VirtualRows<T>({
  rows,
  scrollRef,
  columnCount,
  rowHeight = 25,
  overscan = 12,
  children,
}: {
  rows: readonly T[];
  scrollRef: RefObject<HTMLDivElement | null>;
  columnCount: number;
  rowHeight?: number;
  overscan?: number;
  children: (row: T, index: number) => ReactNode;
}) {
  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => rowHeight,
    overscan,
  });
  const items = virtualizer.getVirtualItems();
  const padTop = items.length > 0 ? items[0].start : 0;
  const padBottom = items.length > 0 ? virtualizer.getTotalSize() - items[items.length - 1].end : 0;

  return (
    <tbody>
      {padTop > 0 && <tr aria-hidden="true"><td colSpan={columnCount} style={{ height: padTop, padding: 0, border: 0 }} /></tr>}
      {items.map((item) => <Fragment key={item.key}>{children(rows[item.index], item.index)}</Fragment>)}
      {padBottom > 0 && <tr aria-hidden="true"><td colSpan={columnCount} style={{ height: padBottom, padding: 0, border: 0 }} /></tr>}
    </tbody>
  );
}
