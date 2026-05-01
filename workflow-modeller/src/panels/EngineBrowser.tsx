import { createEngineClient } from '@/engine/client';
import type { DefinitionSummary } from '@/engine/types';
import { EngineError } from '@/engine/types';
import { useEngineConnection } from '@/store/selectors';
import { useWorkflowStore } from '@/store/workflowStore';
import { type ReactNode, useCallback, useEffect, useMemo, useState } from 'react';

interface EngineBrowserProps {
  open: boolean;
  onClose: () => void;
  onLoaded?: () => void;
}

const PAGE_SIZE = 20;

export function EngineBrowser({ open, onClose, onLoaded }: EngineBrowserProps): ReactNode {
  const engine = useEngineConnection();
  const loadFromEngine = useWorkflowStore((s) => s.loadFromEngine);

  const client = useMemo(() => createEngineClient(engine), [engine]);

  const [items, setItems] = useState<DefinitionSummary[]>([]);
  const [page, setPage] = useState(0);
  const [total, setTotal] = useState(0);
  const [keyword, setKeyword] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<{ id: string; version: number | undefined } | null>(
    null,
  );

  const refresh = useCallback(async (): Promise<void> => {
    setLoading(true);
    setError(null);
    try {
      const res = await client.listDefinitions({ keyword, page, pageSize: PAGE_SIZE });
      setItems(res.items ?? []);
      setTotal(res.total ?? 0);
    } catch (e) {
      setError(e instanceof EngineError ? `${e.message}` : 'Unable to list definitions');
    } finally {
      setLoading(false);
    }
  }, [client, keyword, page]);

  useEffect(() => {
    if (open) refresh();
  }, [open, refresh]);

  if (!open) return null;

  async function load(): Promise<void> {
    if (!selected) return;
    setLoading(true);
    setError(null);
    try {
      await loadFromEngine(selected.id, selected.version);
      onLoaded?.();
      onClose();
    } catch (e) {
      setError(e instanceof EngineError ? e.message : 'Load failed');
    } finally {
      setLoading(false);
    }
  }

  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  return (
    <div className="wm-dialog-backdrop">
      <dialog
        open
        className="wm-dialog wm-dialog--wide"
        aria-labelledby="wm-engine-browser-heading"
      >
        <h2 id="wm-engine-browser-heading">Load from engine</h2>
        <p className="wm-dialog-hint">{engine.baseUrl}</p>
        <div className="wm-engine-search">
          <input
            type="text"
            className="wm-input"
            placeholder="filter by keyword"
            value={keyword}
            onChange={(e) => setKeyword(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                setPage(0);
                refresh();
              }
            }}
          />
          <button
            type="button"
            onClick={() => {
              setPage(0);
              refresh();
            }}
          >
            Search
          </button>
        </div>
        {loading && <p className="wm-field-hint">Loading…</p>}
        {error && <p className="wm-field-error">Unable to list definitions: {error}</p>}
        {!loading && !error && items.length === 0 && (
          <p className="wm-field-hint">No definitions found.</p>
        )}
        <ul className="wm-engine-list">
          {items.map((d) => {
            const versions = d.versions ?? [];
            const latest = versions[versions.length - 1];
            const isSelected = selected?.id === d.id;
            return (
              <li key={d.id} className={`wm-engine-row ${isSelected ? 'is-selected' : ''}`}>
                <button
                  type="button"
                  className="wm-engine-row-main"
                  onClick={() => setSelected({ id: d.id, version: undefined })}
                >
                  <span className="wm-engine-row-id">{d.id}</span>
                  <span className="wm-engine-row-name">{d.name}</span>
                </button>
                {isSelected && versions.length > 0 && (
                  <select
                    className="wm-input"
                    value={selected?.version ?? 'latest'}
                    onChange={(e) =>
                      setSelected({
                        id: d.id,
                        version: e.target.value === 'latest' ? undefined : Number(e.target.value),
                      })
                    }
                  >
                    <option value="latest">latest (v{latest})</option>
                    {[...versions].reverse().map((v) => (
                      <option key={v} value={v}>
                        v{v}
                      </option>
                    ))}
                  </select>
                )}
              </li>
            );
          })}
        </ul>
        <div className="wm-engine-paging">
          <button
            type="button"
            onClick={() => setPage((p) => Math.max(0, p - 1))}
            disabled={page === 0}
          >
            ← prev
          </button>
          <span>
            page {page + 1} of {totalPages}
          </span>
          <button
            type="button"
            onClick={() => setPage((p) => p + 1)}
            disabled={page + 1 >= totalPages}
          >
            next →
          </button>
        </div>
        <div className="wm-dialog-actions">
          <button type="button" onClick={onClose}>
            Cancel
          </button>
          <button
            type="button"
            className="wm-dialog-primary"
            onClick={load}
            disabled={!selected || loading}
          >
            Load
          </button>
        </div>
      </dialog>
    </div>
  );
}
