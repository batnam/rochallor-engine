import { useWorkflowStore } from '@/store/workflowStore';
import { type ChangeEvent, type ReactNode, useRef, useState } from 'react';

interface ImportDialogProps {
  open: boolean;
  onClose: () => void;
  onImported?: (warnings: string[]) => void;
  onError?: (errors: string[]) => void;
}

export function ImportDialog({ open, onClose, onImported, onError }: ImportDialogProps): ReactNode {
  const importFromJson = useWorkflowStore((s) => s.importFromJson);
  const [text, setText] = useState('');
  const [fileName, setFileName] = useState<string | undefined>();
  const [localErrors, setLocalErrors] = useState<string[]>([]);
  const fileInput = useRef<HTMLInputElement>(null);

  if (!open) return null;

  function handleFileChange(e: ChangeEvent<HTMLInputElement>): void {
    const file = e.target.files?.[0];
    if (!file) return;
    setFileName(file.name);
    file.text().then((content) => setText(content));
  }

  function handleSubmit(): void {
    const result = importFromJson(text, fileName ? { name: fileName } : undefined);
    if (!result.ok) {
      setLocalErrors(result.errors ?? []);
      onError?.(result.errors ?? []);
      return;
    }
    setLocalErrors([]);
    onImported?.(result.warnings ?? []);
    onClose();
    setText('');
    setFileName(undefined);
    if (fileInput.current) fileInput.current.value = '';
  }

  return (
    <div className="wm-dialog-backdrop">
      <dialog open className="wm-dialog" aria-labelledby="wm-import-heading">
        <h2 id="wm-import-heading">Import workflow JSON</h2>
        <p className="wm-dialog-hint">
          Paste JSON below or pick a file. Existing canvas contents are replaced.
        </p>
        <input
          ref={fileInput}
          type="file"
          accept="application/json,.json"
          onChange={handleFileChange}
        />
        <textarea
          className="wm-dialog-textarea"
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder='{ "id": "…", "name": "…", "steps": [ … ] }'
          rows={14}
          spellCheck={false}
        />
        {localErrors.length > 0 && (
          <ul className="wm-dialog-errors">
            {localErrors.map((err, i) => (
              <li key={`${i}-${err}`}>{err}</li>
            ))}
          </ul>
        )}
        <div className="wm-dialog-actions">
          <button type="button" onClick={onClose}>
            Cancel
          </button>
          <button
            type="button"
            className="wm-dialog-primary"
            onClick={handleSubmit}
            disabled={text.trim().length === 0}
          >
            Import
          </button>
        </div>
      </dialog>
    </div>
  );
}
