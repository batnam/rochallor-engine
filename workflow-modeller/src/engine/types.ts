import type { WorkflowDefinition } from '@/domain/types';

export interface DefinitionSummary {
  id: string;
  name: string;
  versions: number[];
  [key: string]: unknown;
}

export interface DefinitionListResponse {
  items: DefinitionSummary[];
  page: number;
  pageSize: number;
  total: number;
}

export interface UploadSummary {
  id: string;
  name: string;
  version: number;
  [key: string]: unknown;
}

export interface ListParams {
  keyword?: string;
  page?: number;
  pageSize?: number;
}

export class EngineError extends Error {
  readonly status: number;
  readonly body: string;
  readonly kind: 'http' | 'network' | 'parse';

  constructor(message: string, opts: { status: number; body: string; kind: EngineError['kind'] }) {
    super(message);
    this.name = 'EngineError';
    this.status = opts.status;
    this.body = opts.body;
    this.kind = opts.kind;
  }
}

export type DefinitionPayload = WorkflowDefinition;
