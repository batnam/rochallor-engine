import { type ReactNode, useState } from "react";
import type { Navigation } from "./routes/Navigation";
import {
  type FilterKind,
  type SavedFilter,
  loadSavedFilters,
  normalizeFilterSearch,
  savedFilterKey,
  storeSavedFilters,
} from "./saved-filter-storage";

function message(error: unknown): string {
  return error instanceof Error
    ? error.message
    : "Browser storage unavailable.";
}

export function SavedFilters({
  kind,
  search,
  navigation,
}: { kind: FilterKind; search: string; navigation: Navigation }): ReactNode {
  const [state, setState] = useState<{
    views: SavedFilter[];
    error: string | null;
  }>(() => {
    try {
      return { views: loadSavedFilters(kind), error: null };
    } catch (error) {
      return { views: [], error: message(error) };
    }
  });
  let applied: SavedFilter | undefined;
  try {
    const normalized = normalizeFilterSearch(kind, search);
    applied = state.views.find((view) => view.search === normalized);
  } catch {
    // Invalid URL filters remain editable in the ordinary search form.
  }
  // List loading states can remount this panel after applying a saved search.
  const [name, setName] = useState(applied?.name ?? "");
  const [selectedId, setSelectedId] = useState(applied?.id ?? "");
  const update = (change: (views: SavedFilter[]) => SavedFilter[]): boolean => {
    try {
      const views = storeSavedFilters(kind, change(loadSavedFilters(kind)));
      setState({ views, error: null });
      return true;
    } catch (error) {
      setState((current) => ({ ...current, error: message(error) }));
      return false;
    }
  };
  return (
    <section
      className="rm-card rm-context-card rm-saved-filters"
      aria-label="Saved filters"
    >
      <h3>Saved filters</h3>
      <p className="rm-muted">
        Save applied filters in this browser. Result data is not saved. Time
        bounds remain absolute UTC values.
      </p>
      {state.error ? (
        <p role="alert">
          Saved filters: {state.error} Ordinary searches remain available.
        </p>
      ) : null}
      <div className="rm-saved-filter-controls">
        <label className="rm-field">
          Filter name
          <input
            value={name}
            maxLength={80}
            onChange={(event) => setName(event.target.value)}
          />
        </label>
        <button
          type="button"
          className="rm-button"
          onClick={() =>
            update((views) => [
              ...views,
              {
                id: crypto.randomUUID(),
                name,
                search: normalizeFilterSearch(kind, search),
              },
            ])
          }
        >
          Save applied filters
        </button>
        <label className="rm-field">
          <span id={`saved-filter-label-${kind}`}>Saved filter</span>
          <select
            aria-labelledby={`saved-filter-label-${kind}`}
            value={selectedId}
            onChange={(event) => {
              setSelectedId(event.target.value);
              setName(
                state.views.find((view) => view.id === event.target.value)
                  ?.name ?? "",
              );
            }}
          >
            <option value="">Choose a saved filter</option>
            {state.views.map((view) => (
              <option key={view.id} value={view.id}>
                {view.name}
              </option>
            ))}
          </select>
        </label>
        <button
          type="button"
          className="rm-button"
          disabled={!selectedId}
          onClick={() => {
            try {
              const view = loadSavedFilters(kind).find(
                (item) => item.id === selectedId,
              );
              if (!view) throw new Error("Saved filter no longer exists.");
              const query = normalizeFilterSearch(kind, view.search);
              navigation.push(
                `${kind === "instances" ? "/" : "/incidents"}${query ? `?${query}` : ""}`,
              );
            } catch (error) {
              setState((current) => ({ ...current, error: message(error) }));
            }
          }}
        >
          Apply saved filter
        </button>
        <button
          type="button"
          className="rm-button"
          disabled={!selectedId}
          onClick={() =>
            update((views) =>
              views.map((view) =>
                view.id === selectedId ? { ...view, name } : view,
              ),
            )
          }
        >
          Rename saved filter
        </button>
        <button
          type="button"
          className="rm-button"
          disabled={!selectedId}
          onClick={() => {
            if (
              update((views) => views.filter((view) => view.id !== selectedId))
            ) {
              setSelectedId("");
              setName("");
            }
          }}
        >
          Delete saved filter
        </button>
        <button
          type="button"
          className="rm-button"
          onClick={() => {
            try {
              localStorage.removeItem(savedFilterKey(kind));
              setState({ views: [], error: null });
              setSelectedId("");
              setName("");
            } catch (error) {
              setState((current) => ({ ...current, error: message(error) }));
            }
          }}
        >
          Clear saved filters
        </button>
      </div>
    </section>
  );
}
