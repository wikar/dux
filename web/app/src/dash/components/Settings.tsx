import type { DashElement } from "../types";
import ElementSettings from "./ElementSettings";
import DashboardSettings from "./DashboardSettings";

/** Settings for the selected element, or the dashboard when none selected. */
export default function Settings({ el }: { el: DashElement | null }) {
  return el ? <ElementSettings el={el} /> : <DashboardSettings />;
}
