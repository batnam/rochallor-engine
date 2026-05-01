import { renameStepId as applyRename } from '@/domain/rename';
import type {
  Diagnostic,
  EdgeVariant,
  Step,
  StepId,
  StepType,
  WorkflowDefinition,
} from '@/domain/types';
import { validate } from '@/domain/validate';
import { type EngineClient, createEngineClient } from '@/engine/client';
import { EngineError } from '@/engine/types';
import { embedLayout, exportJson as ioExport } from '@/io/export';
import { importJson as ioImport } from '@/io/import';
import { temporal } from 'zundo';
import { create } from 'zustand';
import { persist } from 'zustand/middleware';

export interface EngineConnection {
  baseUrl: string;
  authHeader: string;
  status: 'unknown' | 'ok' | 'unreachable' | 'unauthorized' | 'error';
  lastCheckedAt?: number;
}

export type Selection =
  | { kind: 'none' }
  | { kind: 'step'; id: StepId }
  | { kind: 'edge'; from: StepId; to: StepId; variant: EdgeVariant };

export type Source =
  | { kind: 'new' }
  | { kind: 'file'; name?: string }
  | { kind: 'engine'; definitionId: string; version: number };

export interface NewStepTemplate {
  type: StepType;
  id?: StepId;
  name?: string;
}

export interface ImportResult {
  ok: boolean;
  errors?: string[];
  warnings?: string[];
}

export type EdgeHandles = Record<
  string,
  { sourceHandle?: string | null; targetHandle?: string | null }
>;

export interface WorkflowState {
  definition: WorkflowDefinition;
  layout: Record<StepId, { x: number; y: number }>;
  edgeHandles: EdgeHandles;
  selection: Selection;
  validation: { lastRunAt?: number; diagnostics: Diagnostic[] };
  dirty: boolean;
  importWarnings: string[];
  engine: EngineConnection;
  source: Source;
}

export interface WorkflowActions {
  addStep(template: NewStepTemplate): StepId;
  deleteStep(id: StepId, replacement?: StepId): void;
  renameStepId(oldId: StepId, newId: StepId): void;
  updateStepProperty(id: StepId, key: string, value: unknown): void;
  addEdge(from: StepId, to: StepId, variant: EdgeVariant, meta?: Record<string, unknown>): void;
  removeEdge(from: StepId, to: StepId, variant: EdgeVariant, key?: string): void;
  setDefinitionMeta(patch: Partial<WorkflowDefinition>): void;

  setLayout(id: StepId, pos: { x: number; y: number }): void;
  setEdgeHandle(edgeId: string, handles: EdgeHandles[string]): void;
  select(sel: Selection): void;
  runValidation(): void;
  setEngineConnection(patch: Partial<EngineConnection>): void;

  importFromJson(text: string, meta?: { name?: string }): ImportResult;
  applyDefinitionFromJson(text: string): ImportResult;
  exportToJson(opts?: { includeLayout?: boolean }): string;
  loadFromEngine(id: string, version?: number): Promise<void>;
  uploadToEngine(): Promise<{ version: number }>;
  testEngineConnection(): Promise<EngineConnection['status']>;
  /** @internal Test helper — resets store to initial state. Not exposed in the UI. */
  reset(): void;
}

let injectedClient: EngineClient | null = null;
/** Test seam: lets MSW-driven specs swap in a deterministic client. */
export function __setEngineClient(client: EngineClient | null): void {
  injectedClient = client;
}
function getClient(state: WorkflowState): EngineClient {
  return injectedClient ?? createEngineClient(state.engine);
}

export type WorkflowStore = WorkflowState & WorkflowActions;

function emptyDefinition(): WorkflowDefinition {
  return {
    id: 'new-workflow',
    name: 'Untitled workflow',
    steps: [],
  };
}

function initialState(): WorkflowState {
  return {
    definition: emptyDefinition(),
    layout: {},
    edgeHandles: {},
    selection: { kind: 'none' },
    validation: { diagnostics: [] },
    dirty: false,
    importWarnings: [],
    engine: {
      baseUrl: 'http://localhost:8080',
      authHeader: '',
      status: 'unknown',
    },
    source: { kind: 'new' },
  };
}

function defaultStepName(type: StepType): string {
  return type.toLowerCase().split('_').map(capitalize).join(' ');
}

function capitalize(s: string): string {
  if (s.length === 0) return s;
  return s.charAt(0).toUpperCase() + s.slice(1);
}

function uniqueStepId(def: WorkflowDefinition, preferred: StepId): StepId {
  const existing = new Set(def.steps.map((s) => s.id));
  if (!existing.has(preferred)) return preferred;
  let i = 2;
  while (existing.has(`${preferred}-${i}`)) i++;
  return `${preferred}-${i}`;
}

function newStep(template: NewStepTemplate, def: WorkflowDefinition): Step {
  const baseId = template.id ?? template.type.toLowerCase().replace(/_/g, '-');
  const id = uniqueStepId(def, baseId);
  const name = template.name ?? defaultStepName(template.type);
  switch (template.type) {
    case 'SERVICE_TASK':
      return { id, name, type: 'SERVICE_TASK' };
    case 'USER_TASK':
      return { id, name, type: 'USER_TASK' };
    case 'DECISION':
      return { id, name, type: 'DECISION', conditionalNextSteps: {} };
    case 'TRANSFORMATION':
      return { id, name, type: 'TRANSFORMATION', nextStep: '', transformations: {} };
    case 'WAIT':
      return { id, name, type: 'WAIT', nextStep: '' };
    case 'PARALLEL_GATEWAY':
      return { id, name, type: 'PARALLEL_GATEWAY', parallelNextSteps: [], joinStep: '' };
    case 'JOIN_GATEWAY':
      return { id, name, type: 'JOIN_GATEWAY', nextStep: '' };
    case 'END':
      return { id, name, type: 'END' };
  }
}

const creator = (
  set: (
    partial: Partial<WorkflowStore> | ((state: WorkflowStore) => Partial<WorkflowStore>),
  ) => void,
  get: () => WorkflowStore,
): WorkflowStore => ({
  ...initialState(),

  addStep(template) {
    const step = newStep(template, get().definition);
    set((state) => ({
      definition: { ...state.definition, steps: [...state.definition.steps, step] },
      dirty: true,
    }));
    return step.id;
  },

  deleteStep(id, replacement) {
    set((state) => {
      const steps = state.definition.steps.filter((s) => s.id !== id);
      let def: WorkflowDefinition = { ...state.definition, steps };
      if (replacement) {
        def = applyRename(def, replacement, replacement); // no-op rename for symmetry
      }
      // Scrub references to the deleted step.
      def = scrubRefs(def, id);
      return { definition: def, dirty: true };
    });
  },

  renameStepId(oldId, newId) {
    set((state) => ({ definition: applyRename(state.definition, oldId, newId), dirty: true }));
  },

  updateStepProperty(id, key, value) {
    set((state) => ({
      definition: {
        ...state.definition,
        steps: state.definition.steps.map((s) =>
          s.id === id ? ({ ...s, [key]: value } as Step) : s,
        ),
      },
      dirty: true,
    }));
  },

  addEdge(_from, _to, _variant, _meta) {
    // Edge mutation is expressed via updateStepProperty in Phase 3+.
    // Kept on the interface so the UI layer has a named hook.
  },

  removeEdge(_from, _to, _variant, _key) {
    // See addEdge.
  },

  setDefinitionMeta(patch) {
    set((state) => ({ definition: { ...state.definition, ...patch }, dirty: true }));
  },

  setLayout(id, pos) {
    set((state) => ({ layout: { ...state.layout, [id]: pos } }));
  },

  setEdgeHandle(edgeId, handles) {
    set((state) => ({ edgeHandles: { ...state.edgeHandles, [edgeId]: handles } }));
  },

  select(sel) {
    set({ selection: sel });
  },

  runValidation() {
    const diagnostics = validate(get().definition);
    set({ validation: { lastRunAt: Date.now(), diagnostics } });
  },

  setEngineConnection(patch) {
    set((state) => ({ engine: { ...state.engine, ...patch } }));
  },

  importFromJson(text, meta) {
    const result = ioImport(text);
    if (!result.ok) {
      return { ok: false, errors: result.errors };
    }
    const embedded = extractEmbeddedModeller(result.def);
    set({
      definition: result.def,
      layout: embedded.layout,
      edgeHandles: embedded.edgeHandles,
      selection: { kind: 'none' },
      validation: { diagnostics: [] },
      dirty: false,
      importWarnings: result.warnings,
      source: { kind: 'file', ...(meta?.name !== undefined ? { name: meta.name } : {}) },
    });
    return { ok: true, warnings: result.warnings };
  },

  applyDefinitionFromJson(text) {
    const result = ioImport(text);
    if (!result.ok) return { ok: false, errors: result.errors };
    const newIds = new Set(result.def.steps.map((s) => s.id));
    const preservedLayout = Object.fromEntries(
      Object.entries(get().layout).filter(([id]) => newIds.has(id)),
    );
    set({
      definition: result.def,
      layout: preservedLayout,
      // edgeHandles unchanged — JSON editor doesn't touch routing
      selection: { kind: 'none' },
      validation: { diagnostics: [] },
      dirty: true,
      importWarnings: result.warnings,
    });
    return { ok: true, warnings: result.warnings };
  },

  exportToJson(opts) {
    return ioExport(get().definition, {
      includeLayout: opts?.includeLayout ?? false,
      layout: get().layout,
      edgeHandles: get().edgeHandles,
    });
  },

  async loadFromEngine(id, version) {
    const client = getClient(get());
    const def =
      version === undefined ? await client.getLatest(id) : await client.getVersion(id, version);
    const embedded = extractEmbeddedModeller(def);
    set({
      definition: def,
      layout: embedded.layout,
      edgeHandles: embedded.edgeHandles,
      selection: { kind: 'none' },
      validation: { diagnostics: [] },
      dirty: false,
      importWarnings: [],
      source: { kind: 'engine', definitionId: id, version: def.version ?? version ?? 0 },
    });
  },

  async uploadToEngine() {
    const state = get();
    const client = getClient(state);
    try {
      const summary = await client.upload(
        embedLayout(state.definition, state.layout, state.edgeHandles),
      );
      set((s) => ({
        definition: { ...s.definition, version: summary.version },
        dirty: false,
        source: { kind: 'engine', definitionId: summary.id, version: summary.version },
        engine: { ...s.engine, status: 'ok', lastCheckedAt: Date.now() },
      }));
      return { version: summary.version };
    } catch (e) {
      if (e instanceof EngineError && e.kind === 'http' && e.status >= 400 && e.status < 500) {
        // Surface engine validation rejection verbatim — drift signal per R-010.
        const drift: Diagnostic = {
          code: 'STEP_TYPE_VALID',
          severity: 'error',
          message: `Engine rejected upload (${e.status}): ${e.body || e.message}`,
        };
        set((s) => ({
          validation: { lastRunAt: Date.now(), diagnostics: [...s.validation.diagnostics, drift] },
          engine: { ...s.engine, status: 'error' },
        }));
      } else if (e instanceof EngineError && e.kind === 'network') {
        set((s) => ({ engine: { ...s.engine, status: 'unreachable', lastCheckedAt: Date.now() } }));
      } else {
        set((s) => ({ engine: { ...s.engine, status: 'error' } }));
      }
      throw e;
    }
  },

  async testEngineConnection() {
    const client = getClient(get());
    const status = await client.testConnection();
    set((s) => ({ engine: { ...s.engine, status, lastCheckedAt: Date.now() } }));
    return status;
  },

  reset() {
    set(initialState());
  },
});

function extractEmbeddedModeller(def: WorkflowDefinition): {
  layout: Record<string, { x: number; y: number }>;
  edgeHandles: EdgeHandles;
} {
  try {
    const wm = (def.metadata as Record<string, unknown> | undefined)?.workflowModeller as
      | Record<string, unknown>
      | undefined;

    const rawLayout = wm?.layout as Record<string, unknown> | undefined;
    const layout: Record<string, { x: number; y: number }> = {};
    if (rawLayout && typeof rawLayout === 'object') {
      for (const [id, pos] of Object.entries(rawLayout)) {
        if (pos && typeof pos === 'object' && 'x' in pos && 'y' in pos) {
          const { x, y } = pos as { x: unknown; y: unknown };
          if (typeof x === 'number' && typeof y === 'number') layout[id] = { x, y };
        }
      }
    }

    const rawHandles = wm?.edgeHandles as EdgeHandles | undefined;
    const edgeHandles: EdgeHandles =
      rawHandles && typeof rawHandles === 'object' ? { ...rawHandles } : {};

    return { layout, edgeHandles };
  } catch {
    return { layout: {}, edgeHandles: {} };
  }
}

function scrubRefs(def: WorkflowDefinition, removedId: StepId): WorkflowDefinition {
  const steps = def.steps.map((step) => {
    switch (step.type) {
      case 'SERVICE_TASK':
      case 'USER_TASK': {
        let next = step;
        if (step.nextStep === removedId) next = { ...next, nextStep: '' };
        if (next.boundaryEvents) {
          next = {
            ...next,
            boundaryEvents: next.boundaryEvents.filter((e) => e.targetStepId !== removedId),
          };
        }
        return next;
      }
      case 'WAIT': {
        let next = step;
        if (step.nextStep === removedId) next = { ...next, nextStep: '' };
        if (next.boundaryEvents) {
          next = {
            ...next,
            boundaryEvents: next.boundaryEvents.filter((e) => e.targetStepId !== removedId),
          };
        }
        return next;
      }
      case 'TRANSFORMATION':
        return step.nextStep === removedId ? { ...step, nextStep: '' } : step;
      case 'JOIN_GATEWAY':
        return step.nextStep === removedId ? { ...step, nextStep: '' } : step;
      case 'DECISION': {
        const entries = Object.entries(step.conditionalNextSteps).filter(
          ([, target]) => target !== removedId,
        );
        return { ...step, conditionalNextSteps: Object.fromEntries(entries) };
      }
      case 'PARALLEL_GATEWAY': {
        const parallelNextSteps = step.parallelNextSteps.filter((t) => t !== removedId);
        const joinStep = step.joinStep === removedId ? '' : step.joinStep;
        return { ...step, parallelNextSteps, joinStep };
      }
      case 'END':
        return step;
    }
  });
  return { ...def, steps };
}

export const useWorkflowStore = create<WorkflowStore>()(
  persist(
    temporal(creator, {
      partialize: (state) => ({ definition: state.definition }),
      equality: (a, b) => a.definition === b.definition,
      limit: 100,
    }),
    {
      name: 'workflow-modeller:state:v1',
      partialize: (state) => ({
        definition: state.definition,
        layout: state.layout,
        edgeHandles: state.edgeHandles,
        engine: { baseUrl: state.engine.baseUrl, authHeader: state.engine.authHeader },
        source: state.source,
      }),
      version: 1,
      migrate: (persisted, fromVersion) => {
        if (fromVersion !== 1) return undefined as unknown as WorkflowState;
        return persisted as WorkflowState;
      },
    },
  ),
);
