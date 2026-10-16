import { Component, type ReactNode } from "react";
import type { DashElement } from "../types";

/** Nothing an element renders may reach the React root: an escaping error
 *  unmounts the whole SPA, leaving a blank page instead of one broken element.
 *  fallback turns the caught error into what takes its place. */
export class ElementBoundary extends Component<
  { fallback: (error: Error) => ReactNode; children: ReactNode },
  { error: Error | null }
> {
  state: { error: Error | null } = { error: null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  render() {
    const { error } = this.state;
    return error ? this.props.fallback(error) : this.props.children;
  }
}

/** Header/floating controls run the same data hooks as the body (the CSV button
 *  and the refresh dot both read the element query), so they can throw for the
 *  same reasons — and they hang outside the body's boundary. Chrome that fails
 *  is worth less than the visual it decorates: it disappears silently. */
export function ControlsBoundary({ el, children }: { el: DashElement; children: ReactNode }) {
  // Changing visual type resets any caught control error.
  return (
    <ElementBoundary key={el.type} fallback={() => null}>
      {children}
    </ElementBoundary>
  );
}
