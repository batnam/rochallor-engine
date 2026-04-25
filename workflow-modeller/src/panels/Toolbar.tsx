import { useDirty, useValidationSummary } from '@/store/selectors';
import { useWorkflowStore } from '@/store/workflowStore';
import { useReactFlow } from '@xyflow/react';
import type { ReactNode } from 'react';

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
}

export function Toolbar({ onImport, onExport }: ToolbarProps): ReactNode {
  const dirty = useDirty();
  const { errors } = useValidationSummary();
  const runValidation = useWorkflowStore((s) => s.runValidation);
  const reset = useWorkflowStore((s) => s.reset);
  const { undo, redo, pastCount, futureCount } = useTemporal();
  const { fitView } = useReactFlow();

  const blocksExport = errors > 0;

  return (
    <div className="wm-toolbar-actions">
      <button type="button" onClick={onImport}>
        Import
      </button>
      <button type="button" onClick={onExport} disabled={blocksExport}>
        Export
      </button>
      <button type="button" onClick={runValidation}>
        Validate
      </button>
      <button type="button" onClick={() => fitView({ duration: 250, padding: 0.2 })}>
        Fit to screen
      </button>
      <button type="button" disabled>
        Load from engine
      </button>
      <button type="button" disabled={blocksExport}>
        Upload
      </button>
      <button type="button" onClick={undo} disabled={pastCount === 0}>
        Undo
      </button>
      <button type="button" onClick={redo} disabled={futureCount === 0}>
        Redo
      </button>
      <button type="button" onClick={reset}>
        Reset
      </button>
      <span className="wm-toolbar-status">
        {dirty ? 'unsaved changes' : 'clean'} · {errors} error(s)
      </span>
    </div>
  );
}
