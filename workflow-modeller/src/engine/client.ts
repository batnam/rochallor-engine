import type { WorkflowDefinition } from '@/domain/types';
import {
  type DefinitionListResponse,
  EngineError,
  type ListParams,
  type UploadSummary,
} from './types';

export interface EngineClientConfig {
  baseUrl: string;
  authHeader?: string;
  fetch?: typeof fetch;
}

export interface EngineClient {
  listDefinitions(params?: ListParams): Promise<DefinitionListResponse>;
  getLatest(id: string): Promise<WorkflowDefinition>;
  getVersion(id: string, version: number): Promise<WorkflowDefinition>;
  upload(def: WorkflowDefinition): Promise<UploadSummary>;
  testConnection(): Promise<'ok' | 'unauthorized' | 'unreachable' | 'error'>;
}

export function createEngineClient(config: EngineClientConfig): EngineClient {
  const fetcher = config.fetch ?? fetch;
  const baseUrl = config.baseUrl.replace(/\/+$/, '');

  function headers(extra?: Record<string, string>): HeadersInit {
    const h: Record<string, string> = { Accept: 'application/json', ...(extra ?? {}) };
    if (config.authHeader && config.authHeader.trim() !== '') {
      h.Authorization = config.authHeader;
    }
    return h;
  }

  async function request<T>(path: string, init?: RequestInit): Promise<T> {
    let response: Response;
    try {
      response = await fetcher(`${baseUrl}${path}`, init);
    } catch (e) {
      throw new EngineError((e as Error).message || 'Network error', {
        status: 0,
        body: '',
        kind: 'network',
      });
    }
    const text = await response.text();
    if (!response.ok) {
      throw new EngineError(text || `${response.status} ${response.statusText}`, {
        status: response.status,
        body: text,
        kind: 'http',
      });
    }
    if (text === '') return undefined as T;
    try {
      return JSON.parse(text) as T;
    } catch (e) {
      throw new EngineError(`Invalid JSON: ${(e as Error).message}`, {
        status: response.status,
        body: text,
        kind: 'parse',
      });
    }
  }

  return {
    async listDefinitions(params) {
      const qs = new URLSearchParams();
      qs.set('page', String(params?.page ?? 0));
      qs.set('pageSize', String(params?.pageSize ?? 20));
      if (params?.keyword) qs.set('keyword', params.keyword);
      return request<DefinitionListResponse>(`/v1/definitions?${qs.toString()}`, {
        method: 'GET',
        headers: headers(),
      });
    },
    async getLatest(id) {
      return request<WorkflowDefinition>(`/v1/definitions/${encodeURIComponent(id)}`, {
        method: 'GET',
        headers: headers(),
      });
    },
    async getVersion(id, version) {
      return request<WorkflowDefinition>(
        `/v1/definitions/${encodeURIComponent(id)}/versions/${version}`,
        { method: 'GET', headers: headers() },
      );
    },
    async upload(def) {
      return request<UploadSummary>('/v1/definitions', {
        method: 'POST',
        headers: headers({ 'Content-Type': 'application/json' }),
        body: JSON.stringify(def),
      });
    },
    async testConnection() {
      try {
        await request<DefinitionListResponse>('/v1/definitions?page=0&pageSize=1', {
          method: 'GET',
          headers: headers(),
        });
        return 'ok';
      } catch (e) {
        if (e instanceof EngineError) {
          if (e.kind === 'network') return 'unreachable';
          if (e.status === 401 || e.status === 403) return 'unauthorized';
          return 'error';
        }
        return 'error';
      }
    },
  };
}
