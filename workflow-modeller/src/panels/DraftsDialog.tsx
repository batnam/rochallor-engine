import { DRAFTS_LIMIT, type DraftSummary } from '@/io/drafts';
import { useDirty } from '@/store/selectors';
import { useWorkflowStore } from '@/store/workflowStore';
import { type ReactNode, useEffect, useMemo, useState } from 'react';

interface DraftsDialogProps {
  open: boolean;
  onClose: () => void;
  onMessage?: (msg: { tone: 'info' | 'error' | 'warning'; text: string }) => void;
}

export function DraftsDialog({ open, onClose, onMessage }: DraftsDialogProps): ReactNode {
  const saveDraft = useWorkflowStore((s) => s.saveDraft);
  const loadDraft = useWorkflowStore((s) => s.loadDraft);
  const deleteDraft = useWorkflowStore((s) => s.deleteDraft);
  const listDrafts = useWorkflowStore((s) => s.listDrafts);
  const workflowName = useWorkflowStore((s) => s.definition.name);
  const stepCount = useWorkflowStore((s) => s.definition.steps.length);
  const dirty = useDirty();

  const [drafts, setDrafts] = useState<DraftSummary[]>([]);
  const [name, setName] = useState('');
  const [error, setError] = useState<string | null>(null);

  // Refresh the draft list whenever the dialog opens.
  useEffect(() => {
    if (!open) return;
    setDrafts(listDrafts());
    setName(workflowName);
    setError(null);
  }, [open, listDrafts, workflowName]);

  const atCap = drafts.length >= DRAFTS_LIMIT;
  const canSave = name.trim().length > 0 && !atCap && stepCount > 0;

  const sorted = useMemo(() => [...drafts].sort((a, b) => b.savedAt - a.savedAt), [drafts]);

  if (!open) return null;

  function handleSave(): void {
    const result = saveDraft(name);
    if (!result.ok) {
      const text = reasonMessage(result.reason);
      setError(text);
      onMessage?.({ tone: 'error', text });
      return;
    }
    setError(null);
    setDrafts(listDrafts());
    onMessage?.({ tone: 'info', text: `Draft "${result.draft.name}" saved.` });
  }

  function handleRestore(id: string, draftName: string): void {
    if (dirty && !confirm('You have unsaved changes. Discard them and restore this draft?')) {
      return;
    }
    if (loadDraft(id)) {
      onMessage?.({ tone: 'info', text: `Restored draft "${draftName}".` });
      onClose();
    } else {
      onMessage?.({ tone: 'error', text: 'Draft not found (it may have been deleted).' });
      setDrafts(listDrafts());
    }
  }

  function handleDelete(id: string, draftName: string): void {
    if (!confirm(`Delete draft "${draftName}"? This cannot be undone.`)) return;
    deleteDraft(id);
    setDrafts(listDrafts());
    setError(null);
  }

  return (
    <div className="wm-dialog-backdrop">
      <dialog open className="wm-dialog" aria-labelledby="wm-drafts-heading">
        <h2 id="wm-drafts-heading">
          Drafts ({drafts.length} / {DRAFTS_LIMIT})
        </h2>
        <p className="wm-dialog-hint">
          Drafts are stored in your browser only. Use them as named snapshots of the current canvas;
          engine settings are not included.
        </p>

        <section className="wm-drafts-save">
          <div className="wm-field">
            <label className="wm-field-label" htmlFor="wm-draft-name">
              Save current as draft
            </label>
            <input
              id="wm-draft-name"
              type="text"
              className="wm-input"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Draft name"
              disabled={atCap || stepCount === 0}
            />
            {stepCount === 0 && (
              <span className="wm-field-hint">Add at least one step before saving a draft.</span>
            )}
            {atCap && (
              <span className="wm-field-warning">
                Draft limit reached. Delete one below to free a slot.
              </span>
            )}
          </div>
          <button
            type="button"
            className="wm-dialog-primary"
            onClick={handleSave}
            disabled={!canSave}
          >
            Save draft
          </button>
        </section>

        {error && (
          <ul className="wm-dialog-errors">
            <li>{error}</li>
          </ul>
        )}

        <section className="wm-drafts-list" aria-label="Saved drafts">
          {sorted.length === 0 ? (
            <p className="wm-dialog-hint">No drafts saved yet.</p>
          ) : (
            <ul className="wm-drafts">
              {sorted.map((d) => (
                <li key={d.id} className="wm-drafts-item">
                  <div className="wm-drafts-item-info">
                    <span className="wm-drafts-item-name">{d.name}</span>
                    <span className="wm-drafts-item-meta">
                      {d.stepCount} step{d.stepCount === 1 ? '' : 's'} ·{' '}
                      {new Date(d.savedAt).toLocaleString()}
                    </span>
                  </div>
                  <div className="wm-drafts-item-actions">
                    <button type="button" onClick={() => handleRestore(d.id, d.name)}>
                      Restore
                    </button>
                    <button
                      type="button"
                      className="wm-button-danger"
                      onClick={() => handleDelete(d.id, d.name)}
                    >
                      Delete
                    </button>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </section>

        <div className="wm-dialog-actions">
          <button type="button" onClick={onClose}>
            Close
          </button>
        </div>
      </dialog>
    </div>
  );
}

function reasonMessage(reason: 'CAP_REACHED' | 'NAME_REQUIRED' | 'STORAGE_FAILED'): string {
  switch (reason) {
    case 'NAME_REQUIRED':
      return 'Please enter a name for the draft.';
    case 'CAP_REACHED':
      return `You already have ${DRAFTS_LIMIT} drafts. Delete one before saving a new one.`;
    case 'STORAGE_FAILED':
      return 'Could not save to browser storage. It may be full or blocked.';
  }
}
