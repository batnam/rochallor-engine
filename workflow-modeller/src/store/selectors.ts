import { toEdges, toNodes } from '@/domain/graph';
import type { Diagnostic, GraphEdge, GraphNode } from '@/domain/types';
import { type EngineConnection, type WorkflowStore, useWorkflowStore } from './workflowStore';

export function useNodes(): GraphNode[] {
  return useWorkflowStore((s) => toNodes(s.definition));
}

export function useEdges(): GraphEdge[] {
  return useWorkflowStore((s) => toEdges(s.definition));
}

export interface ValidationSummary {
  errors: number;
  warnings: number;
  diagnostics: Diagnostic[];
  lastRunAt: number | undefined;
}

export function useValidationSummary(): ValidationSummary {
  return useWorkflowStore((s) => {
    const errors = s.validation.diagnostics.filter((d) => d.severity === 'error').length;
    const warnings = s.validation.diagnostics.filter((d) => d.severity === 'warning').length;
    return {
      errors,
      warnings,
      diagnostics: s.validation.diagnostics,
      lastRunAt: s.validation.lastRunAt,
    };
  });
}

export function useDirty(): boolean {
  return useWorkflowStore((s) => s.dirty);
}

export function useEngineConnection(): EngineConnection {
  return useWorkflowStore((s) => s.engine);
}

export function useSelection(): WorkflowStore['selection'] {
  return useWorkflowStore((s) => s.selection);
}
