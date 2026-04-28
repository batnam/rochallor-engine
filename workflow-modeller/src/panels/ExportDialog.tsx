import { useWorkflowStore } from '@/store/workflowStore';
import { type ReactNode, useMemo, useState } from 'react';

interface ExportDialogProps {
  open: boolean;
  onClose: () => void;
}

export function ExportDialog({ open, onClose }: ExportDialogProps): ReactNode {
  const exportToJson = useWorkflowStore((s) => s.exportToJson);
  const definitionName = useWorkflowStore((s) => s.definition.name);
  const [includeLayout, setIncludeLayout] = useState(false);
  const [copied, setCopied] = useState(false);

  const text = useMemo(
    () => (open ? exportToJson({ includeLayout }) : ''),
    [open, exportToJson, includeLayout],
  );

  if (!open) return null;

  function handleCopy(): void {
    void navigator.clipboard.writeText(text).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    });
  }

  function handleDownload(): void {
    const blob = new Blob([text], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `${slug(definitionName)}.json`;
    a.click();
    URL.revokeObjectURL(url);
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
        <div className="wm-dialog-actions">
          <button type="button" onClick={onClose}>
            Close
          </button>
          <button type="button" onClick={handleCopy}>
            {copied ? 'Copied ✓' : 'Copy to clipboard'}
          </button>
          <button type="button" className="wm-dialog-primary" onClick={handleDownload}>
            Download JSON
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
