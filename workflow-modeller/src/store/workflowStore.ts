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
import { exportJson as ioExport } from '@/io/export';
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

export interface WorkflowState {
  definition: WorkflowDefinition;
  layout: Record<StepId, { x: number; y: number }>;
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
  select(sel: Selection): void;
  runValidation(): void;
  setEngineConnection(patch: Partial<EngineConnection>): void;

  importFromJson(text: string, meta?: { name?: string }): ImportResult;
  exportToJson(opts?: { includeLayout?: boolean }): string;
  loadFromEngine(id: string, version?: number): Promise<void>;
  uploadToEngine(): Promise<{ version: number }>;
  reset(): void;
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
    set({
      definition: result.def,
      layout: {},
      selection: { kind: 'none' },
      validation: { diagnostics: [] },
      dirty: false,
      importWarnings: result.warnings,
      source: { kind: 'file', ...(meta?.name !== undefined ? { name: meta.name } : {}) },
    });
    return { ok: true, warnings: result.warnings };
  },

  exportToJson(opts) {
    return ioExport(get().definition, {
      includeLayout: opts?.includeLayout ?? false,
      layout: get().layout,
    });
  },

  async loadFromEngine(_id, _version) {
    throw new Error('loadFromEngine will be wired in Phase 6 (US4).');
  },

  async uploadToEngine() {
    throw new Error('uploadToEngine will be wired in Phase 6 (US4).');
  },

  reset() {
    set(initialState());
  },
});

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
      limit: 100,
    }),
    {
      name: 'workflow-modeller:state:v1',
      partialize: (state) => ({
        definition: state.definition,
        layout: state.layout,
        engine: { baseUrl: state.engine.baseUrl, authHeader: state.engine.authHeader },
        source: state.source,
      }),
      version: 1,
    },
  ),
);
