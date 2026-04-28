import { toEdges, toNodes } from '@/domain/graph';
import type { Diagnostic, GraphEdge, GraphNode, StepId } from '@/domain/types';
import { useMemo } from 'react';
import { type EngineConnection, type WorkflowStore, useWorkflowStore } from './workflowStore';

export function useNodes(): GraphNode[] {
  const definition = useWorkflowStore((s) => s.definition);
  return useMemo(() => toNodes(definition), [definition]);
}

export function useEdges(): GraphEdge[] {
  const definition = useWorkflowStore((s) => s.definition);
  return useMemo(() => toEdges(definition), [definition]);
}

export interface ValidationSummary {
  errors: number;
  warnings: number;
  diagnostics: Diagnostic[];
  lastRunAt: number | undefined;
}

export function useValidationSummary(): ValidationSummary {
  const validation = useWorkflowStore((s) => s.validation);
  return useMemo(() => {
    const errors = validation.diagnostics.filter((d) => d.severity === 'error').length;
    const warnings = validation.diagnostics.filter((d) => d.severity === 'warning').length;
    return {
      errors,
      warnings,
      diagnostics: validation.diagnostics,
      lastRunAt: validation.lastRunAt,
    };
  }, [validation]);
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

export interface NodeDiagnosticSummary {
  errors: number;
  warnings: number;
  diagnostics: Diagnostic[];
}

export function useDiagnosticsForNode(nodeId: StepId): NodeDiagnosticSummary {
  const validation = useWorkflowStore((s) => s.validation);
  return useMemo(() => {
    const diagnostics = validation.diagnostics.filter((d) => d.nodeId === nodeId);
    return {
      errors: diagnostics.filter((d) => d.severity === 'error').length,
      warnings: diagnostics.filter((d) => d.severity === 'warning').length,
      diagnostics,
    };
  }, [validation, nodeId]);
}
