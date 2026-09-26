import { useWorkflowStore } from '@/store/workflowStore';
import { platform } from '@platform';
import { type ReactNode, useEffect, useMemo, useState } from 'react';

interface ExportDialogProps {
  open: boolean;
  onClose: () => void;
}

export function ExportDialog({ open, onClose }: ExportDialogProps): ReactNode {
  const exportToJson = useWorkflowStore((s) => s.exportToJson);
  const definitionName = useWorkflowStore((s) => s.definition.name);
  const [includeLayout, setIncludeLayout] = useState(true);
  const [copied, setCopied] = useState(false);
  const [saving, setSaving] = useState(false);
  const [message, setMessage] = useState('');
  const [error, setError] = useState('');

  useEffect(() => {
    if (open) {
      setMessage('');
      setError('');
      setCopied(false);
    }
  }, [open]);

  const text = useMemo(
    () => (open ? exportToJson({ includeLayout }) : ''),
    [open, exportToJson, includeLayout],
  );

  if (!open) return null;

  async function handleCopy(): Promise<void> {
    setError('');
    try {
      await platform.copyText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch (error) {
      setError(error instanceof Error ? error.message : String(error));
    }
  }

  async function handleSave(): Promise<void> {
    setSaving(true);
    setMessage('');
    setError('');
    try {
      const result = await platform.saveJson(`${slug(definitionName)}.json`, text);
      setMessage(
        result === 'saved'
          ? 'File saved.'
          : result === 'download-started'
            ? 'Download started.'
            : '',
      );
    } catch (error) {
      setError(error instanceof Error ? error.message : String(error));
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="wm-dialog-backdrop">
      <dialog open className="wm-dialog" aria-labelledby="wm-export-heading">
        <h2 id="wm-export-heading">Export workflow JSON</h2>
        <label className="wm-field wm-field--inline">
          <input
            type="checkbox"
            checked={includeLayout}
            onChange={(e) => setIncludeLayout(e.target.checked)}
          />
          <span>
            Include canvas layout in <code>metadata.workflowModeller.layout</code>
          </span>
        </label>
        <textarea
          className="wm-dialog-textarea"
          value={text}
          readOnly
          rows={14}
          spellCheck={false}
        />
        {message && <output className="wm-dialog-hint">{message}</output>}
        {error && (
          <p role="alert" className="wm-dialog-errors">
            {error}
          </p>
        )}
        <div className="wm-dialog-actions">
          <button type="button" onClick={onClose} disabled={saving}>
            Close
          </button>
          <button type="button" onClick={handleCopy}>
            {copied ? 'Copied ✓' : 'Copy to clipboard'}
          </button>
          <button
            type="button"
            className="wm-dialog-primary"
            onClick={handleSave}
            disabled={saving}
          >
            {saving ? 'Saving…' : 'Save JSON'}
          </button>
        </div>
      </dialog>
    </div>
  );
}

function slug(s: string): string {
  return (
    s
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/^-+|-+$/g, '') || 'workflow'
  );
}
