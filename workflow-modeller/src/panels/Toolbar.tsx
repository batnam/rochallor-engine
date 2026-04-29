import { layoutWithElk } from '@/canvas/layout';
import { toEdges, toNodes } from '@/domain/graph';
import { EngineError } from '@/engine/types';
import { useDirty, useEngineConnection, useValidationSummary } from '@/store/selectors';
import { useWorkflowStore } from '@/store/workflowStore';
import { useReactFlow } from '@xyflow/react';
import { type ReactNode, useState } from 'react';

function useTemporal(): {
  undo: () => void;
  redo: () => void;
  pastCount: number;
  futureCount: number;
} {
  const temporal = (
    useWorkflowStore as unknown as {
      temporal: {
        getState: () => {
          undo: () => void;
          redo: () => void;
          pastStates: unknown[];
          futureStates: unknown[];
        };
      };
    }
  ).temporal;
  const state = temporal.getState();
  return {
    undo: state.undo,
    redo: state.redo,
    pastCount: state.pastStates.length,
    futureCount: state.futureStates.length,
  };
}

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
  const reset = useWorkflowStore((s) => s.reset);
  const uploadToEngine = useWorkflowStore((s) => s.uploadToEngine);

  function handleDiscard(): void {
    if (!confirm('Discard the current draft? This cannot be undone.')) return;
    reset();
    const persistApi = (useWorkflowStore as unknown as { persist?: { clearStorage?: () => void } })
      .persist;
    persistApi?.clearStorage?.();
  }

  const { undo, redo, pastCount, futureCount } = useTemporal();
  const { fitView } = useReactFlow();

  async function handleTidy(): Promise<void> {
    try {
      const def = useWorkflowStore.getState().definition;
      const positions = await layoutWithElk(toNodes(def), toEdges(def));
      useWorkflowStore.setState({ layout: positions });
      // Allow one render cycle for ReactFlow to reflect updated positions before fitting
      setTimeout(() => fitView({ duration: 250, padding: 0.2 }), 50);
    } catch (e) {
      console.error('Tidy layout failed', e);
    }
  }
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
      <button type="button" onClick={handleTidy}>
        Tidy layout
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
        {uploading ? 'Uploading…' : 'Upload'}
      </button>
      <button type="button" onClick={onOpenSettings}>
        Settings
      </button>
      <button type="button" onClick={undo} disabled={pastCount === 0}>
        Undo
      </button>
      <button type="button" onClick={redo} disabled={futureCount === 0}>
        Redo
      </button>
      <button type="button" onClick={handleDiscard}>
        Discard draft
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
