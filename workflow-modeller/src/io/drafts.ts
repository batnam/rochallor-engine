import type { StepId, WorkflowDefinition } from '@/domain/types';
import type { EdgeHandles } from '@/store/workflowStore';

const DRAFTS_KEY = 'workflow-modeller:drafts:v1';

/** Maximum number of drafts retained at any time. */
export const DRAFTS_LIMIT = 10;

export interface Draft {
  id: string;
  name: string;
  savedAt: number;
  definition: WorkflowDefinition;
  layout: Record<StepId, { x: number; y: number }>;
  edgeHandles: EdgeHandles;
}

export interface DraftSummary {
  id: string;
  name: string;
  savedAt: number;
  stepCount: number;
}

export type SaveDraftResult =
  | { ok: true; draft: Draft }
  | { ok: false; reason: 'CAP_REACHED' | 'NAME_REQUIRED' | 'STORAGE_FAILED' };

function readRaw(): Draft[] {
  try {
    const raw = localStorage.getItem(DRAFTS_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    return parsed.filter(isDraft);
  } catch {
    return [];
  }
}

function isDraft(value: unknown): value is Draft {
  if (!value || typeof value !== 'object') return false;
  const v = value as Record<string, unknown>;
  return (
    typeof v.id === 'string' &&
    typeof v.name === 'string' &&
    typeof v.savedAt === 'number' &&
    !!v.definition &&
    typeof v.definition === 'object'
  );
}

function writeRaw(drafts: Draft[]): boolean {
  try {
    localStorage.setItem(DRAFTS_KEY, JSON.stringify(drafts));
    return true;
  } catch {
    return false;
  }
}

export function listDrafts(): DraftSummary[] {
  return readRaw().map(toSummary);
}

function toSummary(d: Draft): DraftSummary {
  return {
    id: d.id,
    name: d.name,
    savedAt: d.savedAt,
    stepCount: d.definition.steps?.length ?? 0,
  };
}

export function getDraft(id: string): Draft | null {
  return readRaw().find((d) => d.id === id) ?? null;
}

export function saveDraft(input: {
  name: string;
  definition: WorkflowDefinition;
  layout: Record<StepId, { x: number; y: number }>;
  edgeHandles: EdgeHandles;
}): SaveDraftResult {
  const name = input.name.trim();
  if (!name) return { ok: false, reason: 'NAME_REQUIRED' };
  const drafts = readRaw();
  if (drafts.length >= DRAFTS_LIMIT) return { ok: false, reason: 'CAP_REACHED' };
  const draft: Draft = {
    id: newId(),
    name,
    savedAt: Date.now(),
    definition: input.definition,
    layout: input.layout,
    edgeHandles: input.edgeHandles,
  };
  drafts.push(draft);
  if (!writeRaw(drafts)) return { ok: false, reason: 'STORAGE_FAILED' };
  return { ok: true, draft };
}

export function deleteDraft(id: string): void {
  const remaining = readRaw().filter((d) => d.id !== id);
  writeRaw(remaining);
}

/** Test seam: wipe every stored draft. */
export function _clearAllDrafts(): void {
  try {
    localStorage.removeItem(DRAFTS_KEY);
  } catch {
    // ignore — non-browser env
  }
}

function newId(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID();
  }
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
}
