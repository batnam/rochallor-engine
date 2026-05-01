import { EngineError } from '@/engine/types';
import { useDirty, useEngineConnection, useValidationSummary } from '@/store/selectors';
import { useWorkflowStore } from '@/store/workflowStore';
import { useReactFlow } from '@xyflow/react';
import { type ReactNode, useState } from 'react';

interface ToolbarProps {
  onImport: () => void;
  onExport: () => void;
  onOpenSettings: () => void;
  onOpenEngineBrowser: () => void;
  onUploadResult: (result: { ok: boolean; message: string }) => void;
}

export function Toolbar({
  onImport,
  onExport,
  onOpenSettings,
  onOpenEngineBrowser,
  onUploadResult,
}: ToolbarProps): ReactNode {
  const dirty = useDirty();
  const { errors, diagnostics } = useValidationSummary();
  const engine = useEngineConnection();
  const runValidation = useWorkflowStore((s) => s.runValidation);
  const uploadToEngine = useWorkflowStore((s) => s.uploadToEngine);
  const { fitView } = useReactFlow();
  const [uploading, setUploading] = useState(false);

  const blocksExport = errors > 0;
  const blockingDiag = diagnostics.find((d) => d.severity === 'error');
  const blockedTitle = blocksExport
    ? `Fix ${errors} validation error(s) before exporting${
        blockingDiag ? ` — first: ${blockingDiag.code}` : ''
      }`
    : undefined;

  async function handleUpload(): Promise<void> {
    if (uploading) return;
    if (!confirm(`Upload current definition to ${engine.baseUrl}?`)) return;
    setUploading(true);
    try {
      const { version } = await uploadToEngine();
      onUploadResult({ ok: true, message: `Uploaded as version ${version}.` });
    } catch (e) {
      const msg =
        e instanceof EngineError
          ? `${e.message}`
          : e instanceof Error
            ? e.message
            : 'Upload failed';
      onUploadResult({ ok: false, message: msg });
    } finally {
      setUploading(false);
    }
  }

  return (
    <div className="wm-toolbar-actions">
      <button type="button" onClick={onImport}>
        Import
      </button>
      <button type="button" onClick={onExport} disabled={blocksExport} title={blockedTitle}>
        Export
      </button>
      <button type="button" onClick={runValidation}>
        Validate
      </button>
      <button type="button" onClick={() => fitView({ duration: 250, padding: 0.2 })}>
        Fit to screen
      </button>
      <button type="button" onClick={onOpenEngineBrowser}>
        Load from engine
      </button>
      <button
        type="button"
        onClick={handleUpload}
        disabled={blocksExport || uploading}
        title={blockedTitle}
      >
        {uploading ? 'Uploading…' : 'Upload Workflow to Engine'}
      </button>
      <button type="button" onClick={onOpenSettings}>
        Settings
      </button>
      <span
        className={`wm-engine-status wm-engine-status--${engine.status}`}
        title={`Engine: ${engine.baseUrl} (${engine.status})`}
        aria-label={`Engine status: ${engine.status}`}
      >
        ● {engine.status}
      </span>
      <span className="wm-toolbar-status">
        {dirty ? 'unsaved changes' : 'clean'} · {errors} error(s)
      </span>
    </div>
  );
}
