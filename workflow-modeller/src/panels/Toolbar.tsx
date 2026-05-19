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
  onOpenDrafts: () => void;
  onUploadResult: (result: { ok: boolean; message: string }) => void;
}

export function Toolbar({
  onImport,
  onExport,
  onOpenSettings,
  onOpenEngineBrowser,
  onOpenDrafts,
  onUploadResult,
}: ToolbarProps): ReactNode {
  const dirty = useDirty();
  const { errors, diagnostics } = useValidationSummary();
  const engine = useEngineConnection();
  const runValidation = useWorkflowStore((s) => s.runValidation);
  const uploadToEngine = useWorkflowStore((s) => s.uploadToEngine);
  const newWorkflow = useWorkflowStore((s) => s.newWorkflow);
  const stepCount = useWorkflowStore((s) => s.definition.steps.length);
  const { fitView } = useReactFlow();
  const [uploading, setUploading] = useState(false);

  function handleNewWorkflow(): void {
    if (stepCount === 0 && !dirty) {
      newWorkflow();
      return;
    }
    const msg = dirty
      ? 'You have unsaved changes. Discard them and start a new workflow?'
      : 'Clear the current workflow and start fresh?';
    if (!confirm(msg)) return;
    newWorkflow();
  }

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
      <button type="button" onClick={handleNewWorkflow}>
        New Workflow
      </button>
      <button type="button" onClick={onImport}>
        Import Workflow
      </button>
      <button type="button" onClick={onExport} disabled={blocksExport} title={blockedTitle}>
        Export Workflow
      </button>
      <button type="button" onClick={onOpenDrafts}>
        Save / Load Drafts Workflows
      </button>
      <button type="button" onClick={runValidation}>
        Validate Workflow
      </button>
      <button type="button" onClick={() => fitView({ duration: 250, padding: 0.2 })}>
        Fit to screen
      </button>
      <button type="button" onClick={onOpenEngineBrowser}>
        Load Workflow from engine
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
        Engine Settings
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
