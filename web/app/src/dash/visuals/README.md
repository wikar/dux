# Dashboard visuals

Each persisted type has one entry module with its registry metadata and a named React body:

| Type | Entry module |
| --- | --- |
| bar / line / area / combo | `BarChart.tsx` / `LineChart.tsx` / `AreaChart.tsx` / `ComboChart.tsx` |
| donut | `DonutChart.tsx` |
| table / pivot | `Table.tsx` / `PivotTable.tsx` |
| kpi / slicer | `KpiCard.tsx` / `Slicer.tsx` |
| text / image | `TextBox.tsx` / `ImageBox.tsx` |
| map | `Map.tsx` (loads the heavy `MapBody.tsx` lazily) |

`index.ts` registers these definitions in palette order. `QueryVisualDef` bodies receive prepared `DataBodyProps`; `StaticVisualDef` bodies receive only the element. Map and slicer run their own queries and provide a `RefreshStatus` component that calls the appropriate data hook directly.

`ElementView` composes every visual inside `components/ElementFrame` using children. It owns editing gestures; the frame owns the existing container and title bar. `ElementBody` handles query lifetime and error isolation. Its `VisualSurface` composes body content, actions, and loading/error overlays. `useVisualData` shares data preparation and cross-filter wiring without choosing how marks render.

Cartesian visual bodies compose `charts/CartesianFrame` with children returned by the pure mark factories in `charts/series.tsx`. Series metadata supplies axes and domains; the frame never renders marks or inspects children. The factories return actual Recharts primitives so they remain visible to the chart engine. Keep each type's series builder and options in its entry module. Donut owns its pie rendering; map and tables use their own engines.

To add a visual, create its entry module with a named body, register it in `index.ts`, and add its persisted type to `../types.ts` and `dash/schema.json` at the repository root. Use an existing shared frame when the visualization has the same structure. Preserve query cache keys, type-keyed error boundaries, and map lazy loading when changing composition.
