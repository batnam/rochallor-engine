import { useEngineConnection } from '@/store/selectors';
import { useWorkflowStore } from '@/store/workflowStore';
import { type ReactNode, useState } from 'react';

interface SettingsDialogProps {
  open: boolean;
  onClose: () => void;
}

export function SettingsDialog({ open, onClose }: SettingsDialogProps): ReactNode {
  const engine = useEngineConnection();
  const setEngineConnection = useWorkflowStore((s) => s.setEngineConnection);
  const testEngineConnection = useWorkflowStore((s) => s.testEngineConnection);

  const [baseUrl, setBaseUrl] = useState(engine.baseUrl);
  const [authHeader, setAuthHeader] = useState(engine.authHeader);
  const [testing, setTesting] = useState(false);
  const [result, setResult] = useState<string | null>(null);

  if (!open) return null;

  function save(): void {
    setEngineConnection({ baseUrl: baseUrl.replace(/\/+$/, ''), authHeader });
    onClose();
  }

  async function test(): Promise<void> {
    setTesting(true);
    setResult(null);
    setEngineConnection({ baseUrl: baseUrl.replace(/\/+$/, ''), authHeader });
    try {
      const status = await testEngineConnection();
      setResult(status);
    } finally {
      setTesting(false);
    }
  }

  return (
    <div className="wm-dialog-backdrop">
      <dialog open className="wm-dialog" aria-labelledby="wm-settings-heading">
        <h2 id="wm-settings-heading">Engine settings</h2>
        <label className="wm-field">
          <span className="wm-field-label">Engine base URL</span>
          <input
            type="text"
            className="wm-input"
            value={baseUrl}
            onChange={(e) => setBaseUrl(e.target.value)}
            placeholder="http://localhost:8080"
          />
        </label>
        <label className="wm-field">
          <span className="wm-field-label">Authorization header</span>
          <input
            type="text"
            className="wm-input"
            value={authHeader}
            onChange={(e) => setAuthHeader(e.target.value)}
            placeholder="Bearer …"
          />
          <span className="wm-field-hint">Sent verbatim. Leave blank for no auth.</span>
        </label>
        <div className="wm-settings-test">
          <button type="button" onClick={test} disabled={testing}>
            {testing ? 'Testing…' : 'Test connection'}
          </button>
          {result && (
            <span className={`wm-status wm-status--${result}`} aria-label={`Status: ${result}`}>
              {result}
            </span>
          )}
        </div>
        <div className="wm-dialog-actions">
          <button type="button" onClick={onClose}>
            Cancel
          </button>
          <button type="button" className="wm-dialog-primary" onClick={save}>
            Save
          </button>
        </div>
      </dialog>
    </div>
  );
}
