import type { StepId, WorkflowDefinition } from '@/domain/types';
import type { EdgeHandles } from '@/store/workflowStore';

export interface ExportOptions {
  includeLayout?: boolean;
  layout?: Record<StepId, { x: number; y: number }>;
  edgeHandles?: EdgeHandles;
  pretty?: boolean;
}

export function exportJson(def: WorkflowDefinition, opts: ExportOptions = {}): string {
  const final =
    opts.includeLayout && opts.layout ? embedLayout(def, opts.layout, opts.edgeHandles) : def;
  return JSON.stringify(final, null, opts.pretty === false ? undefined : 2);
}

export function embedLayout(
  def: WorkflowDefinition,
  layout: Record<StepId, { x: number; y: number }>,
  edgeHandles?: EdgeHandles,
): WorkflowDefinition {
  const metadata = (def.metadata ?? {}) as Record<string, unknown>;
  const wm = (metadata.workflowModeller ?? {}) as Record<string, unknown>;
  const wmData: Record<string, unknown> = { ...wm, layout };
  if (edgeHandles && Object.keys(edgeHandles).length > 0) {
    wmData.edgeHandles = edgeHandles;
  }
  return {
    ...def,
    metadata: { ...metadata, workflowModeller: wmData },
  };
}
