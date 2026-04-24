import type { StepType } from '@/domain/types';
import { useWorkflowStore } from '@/store/workflowStore';
import type { DragEvent, ReactNode } from 'react';

export const DRAG_MIME = 'application/x-workflow-modeller-step';

const STEP_TYPES: readonly StepType[] = [
  'SERVICE_TASK',
  'USER_TASK',
  'DECISION',
  'TRANSFORMATION',
  'WAIT',
  'PARALLEL_GATEWAY',
  'JOIN_GATEWAY',
  'END',
];

export function Palette(): ReactNode {
  const addStep = useWorkflowStore((s) => s.addStep);

  function handleDragStart(e: DragEvent<HTMLButtonElement>, type: StepType): void {
    e.dataTransfer.setData(DRAG_MIME, type);
    e.dataTransfer.effectAllowed = 'copy';
  }

  return (
    <aside className="wm-palette" aria-label="Step palette">
      <h2 className="wm-panel-heading">Palette</h2>
      <p className="wm-palette-hint">Drag a tile onto the canvas, or click to append.</p>
      <ul className="wm-palette-list">
        {STEP_TYPES.map((t) => (
          <li key={t}>
            <button
              type="button"
              draggable
              onDragStart={(e) => handleDragStart(e, t)}
              className={`wm-palette-item wm-palette-item--${t.toLowerCase()}`}
              onClick={() => addStep({ type: t })}
            >
              {t}
            </button>
          </li>
        ))}
      </ul>
    </aside>
  );
}
