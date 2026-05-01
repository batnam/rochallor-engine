import type { StepType } from '@/domain/types';
import { useWorkflowStore } from '@/store/workflowStore';
import type { CSSProperties, DragEvent, ReactNode } from 'react';

export const DRAG_MIME = 'application/x-workflow-modeller-step';

type PaletteShape = 'rect' | 'circle' | 'diamond' | 'diamond-sm';

interface StepMeta {
  label: string;
  colorVar: string;
  shape: PaletteShape;
}

const STEP_META: Record<StepType, StepMeta> = {
  SERVICE_TASK: { label: 'Service Task', colorVar: '--step-service-task', shape: 'rect' },
  USER_TASK: { label: 'User Task', colorVar: '--step-user-task', shape: 'rect' },
  DECISION: { label: 'Decision', colorVar: '--step-decision', shape: 'diamond' },
  TRANSFORMATION: { label: 'Transformation', colorVar: '--step-transformation', shape: 'rect' },
  WAIT: { label: 'Wait', colorVar: '--step-wait', shape: 'circle' },
  PARALLEL_GATEWAY: {
    label: 'Parallel Gateway',
    colorVar: '--step-parallel-gateway',
    shape: 'diamond-sm',
  },
  JOIN_GATEWAY: { label: 'Join Gateway', colorVar: '--step-join-gateway', shape: 'diamond-sm' },
  END: { label: 'End', colorVar: '--step-end', shape: 'rect' },
};

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

function ShapeMini({ shape, colorVar }: { shape: PaletteShape; colorVar: string }): ReactNode {
  const vars = { '--shape-color': `var(${colorVar})` } as CSSProperties;
  return (
    <span className="wm-palette-shape-wrap">
      <span className={`wm-palette-shape-${shape}`} style={vars} />
    </span>
  );
}

export function Palette(): ReactNode {
  const addStep = useWorkflowStore((s) => s.addStep);

  function handleDragStart(e: DragEvent<HTMLButtonElement>, type: StepType): void {
    e.dataTransfer.setData(DRAG_MIME, type);
    e.dataTransfer.effectAllowed = 'copy';
  }

  return (
    <aside className="wm-palette" aria-label="Step palette">
      <h2 className="wm-panel-heading">Palette</h2>
      <p className="wm-palette-hint">Drag onto canvas or click to append.</p>
      <ul className="wm-palette-list">
        {STEP_TYPES.map((t) => {
          const { label, colorVar, shape } = STEP_META[t];
          return (
            <li key={t}>
              <button
                type="button"
                draggable
                onDragStart={(e) => handleDragStart(e, t)}
                className="wm-palette-item"
                onClick={() => addStep({ type: t })}
              >
                <ShapeMini shape={shape} colorVar={colorVar} />
                <span>{label}</span>
              </button>
            </li>
          );
        })}
      </ul>
    </aside>
  );
}
