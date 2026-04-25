import type { StepId, WorkflowDefinition } from '@/domain/types';

export interface ExportOptions {
  includeLayout?: boolean;
  layout?: Record<StepId, { x: number; y: number }>;
  pretty?: boolean;
}

export function exportJson(def: WorkflowDefinition, opts: ExportOptions = {}): string {
  const final = opts.includeLayout && opts.layout ? embedLayout(def, opts.layout) : def;
  return JSON.stringify(final, null, opts.pretty === false ? undefined : 2);
}

function embedLayout(
  def: WorkflowDefinition,
  layout: Record<StepId, { x: number; y: number }>,
): WorkflowDefinition {
  const metadata = (def.metadata ?? {}) as Record<string, unknown>;
  const wm = (metadata.workflowModeller ?? {}) as Record<string, unknown>;
  return {
    ...def,
    metadata: { ...metadata, workflowModeller: { ...wm, layout } },
  };
}
