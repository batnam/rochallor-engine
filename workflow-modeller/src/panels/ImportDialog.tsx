import { useWorkflowStore } from '@/store/workflowStore';
import { platform } from '@platform';
import { type ReactNode, useState } from 'react';

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
  const [reading, setReading] = useState(false);

  if (!open) return null;

  async function handleOpenFile(): Promise<void> {
    setReading(true);
    setLocalErrors([]);
    try {
      const file = await platform.openJson();
      if (!file) return;
      setFileName(file.name);
      setText(file.text);
    } catch (error) {
      setLocalErrors([error instanceof Error ? error.message : String(error)]);
    } finally {
      setReading(false);
    }
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
  }

  return (
    <div className="wm-dialog-backdrop">
      <dialog open className="wm-dialog" aria-labelledby="wm-import-heading">
        <h2 id="wm-import-heading">Import workflow JSON</h2>
        <p className="wm-dialog-hint">
          Paste JSON below or pick a file. Existing canvas contents are replaced.
        </p>
        <button type="button" onClick={handleOpenFile} disabled={reading}>
          {reading ? 'Opening…' : 'Choose JSON file'}
        </button>
        {fileName && <span className="wm-dialog-hint">{fileName}</span>}
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
          <button type="button" onClick={onClose} disabled={reading}>
            Cancel
          </button>
          <button
            type="button"
            className="wm-dialog-primary"
            onClick={handleSubmit}
            disabled={reading || text.trim().length === 0}
          >
            Import
          </button>
        </div>
      </dialog>
    </div>
  );
}
