// @vitest-environment node
import type { WorkflowDefinition } from '@/domain/types';
import { createEngineClient } from '@/engine/client';
import { EngineError } from '@/engine/types';
import { http, HttpResponse } from 'msw';
import { setupServer } from 'msw/node';
import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest';

const BASE = 'http://mock-engine.example';

const sampleDef: WorkflowDefinition = {
  id: 'wf-1',
  name: 'Sample',
  steps: [{ id: 's1', name: 'End', type: 'END' }],
};

const recordedRequests: Request[] = [];

const server = setupServer(
  http.get(`${BASE}/v1/definitions`, async ({ request }) => {
    recordedRequests.push(request);
    const url = new URL(request.url);
    return HttpResponse.json({
      items: [
        { id: 'wf-1', name: 'Sample', versions: [1, 2] },
        { id: 'wf-2', name: 'Other', versions: [1] },
      ],
      page: Number(url.searchParams.get('page') ?? '0'),
      pageSize: Number(url.searchParams.get('pageSize') ?? '20'),
      total: 2,
    });
  }),
  http.get(`${BASE}/v1/definitions/:id`, ({ params, request }) => {
    recordedRequests.push(request);
    if (params.id === 'missing') return HttpResponse.text('Definition not found', { status: 404 });
    return HttpResponse.json({ ...sampleDef, id: params.id as string, version: 2 });
  }),
  http.get(`${BASE}/v1/definitions/:id/versions/:version`, ({ params, request }) => {
    recordedRequests.push(request);
    return HttpResponse.json({
      ...sampleDef,
      id: params.id as string,
      version: Number(params.version),
    });
  }),
  http.post(`${BASE}/v1/definitions`, async ({ request }) => {
    recordedRequests.push(request);
    const body = (await request.json()) as WorkflowDefinition;
    if (body.id === 'reject-me') {
      return HttpResponse.text('validation error: bad shape', { status: 400 });
    }
    return HttpResponse.json({ id: body.id, name: body.name, version: 7 }, { status: 201 });
  }),
);

beforeAll(() => server.listen({ onUnhandledRequest: 'error' }));
afterEach(() => {
  recordedRequests.length = 0;
  server.resetHandlers();
});
afterAll(() => server.close());

describe('engine client', () => {
  function newClient() {
    return createEngineClient({ baseUrl: BASE, authHeader: 'Bearer xyz' });
  }

  it('listDefinitions sends paging params and returns parsed body', async () => {
    const client = newClient();
    const res = await client.listDefinitions({ page: 1, pageSize: 5, keyword: 'sample' });
    expect(res.items).toHaveLength(2);
    const req = recordedRequests[0];
    expect(req).toBeDefined();
    if (!req) throw new Error('no request');
    const url = new URL(req.url);
    expect(url.searchParams.get('page')).toBe('1');
    expect(url.searchParams.get('pageSize')).toBe('5');
    expect(url.searchParams.get('keyword')).toBe('sample');
  });

  it('attaches the configured Authorization header on every call', async () => {
    const client = newClient();
    await client.listDefinitions();
    const req = recordedRequests[0];
    if (!req) throw new Error('no request');
    expect(req.headers.get('authorization')).toBe('Bearer xyz');
  });

  it('omits the Authorization header when none is configured', async () => {
    const naked = createEngineClient({ baseUrl: BASE });
    await naked.listDefinitions();
    const req = recordedRequests[0];
    if (!req) throw new Error('no request');
    expect(req.headers.get('authorization')).toBeNull();
  });

  it('getLatest fetches /v1/definitions/{id}', async () => {
    const client = newClient();
    const def = await client.getLatest('wf-1');
    expect(def.id).toBe('wf-1');
    expect(def.version).toBe(2);
  });

  it('getVersion fetches the version-pinned endpoint', async () => {
    const client = newClient();
    const def = await client.getVersion('wf-1', 1);
    expect(def.version).toBe(1);
  });

  it('upload posts JSON and returns the new version', async () => {
    const client = newClient();
    const res = await client.upload(sampleDef);
    expect(res).toMatchObject({ id: 'wf-1', version: 7 });
    const req = recordedRequests[0];
    if (!req) throw new Error('no request');
    expect(req.method).toBe('POST');
    expect(req.headers.get('content-type')).toContain('application/json');
  });

  it('surfaces 400 body verbatim as EngineError.body', async () => {
    const client = newClient();
    await expect(client.upload({ ...sampleDef, id: 'reject-me' })).rejects.toMatchObject({
      name: 'EngineError',
      status: 400,
      body: 'validation error: bad shape',
      kind: 'http',
    });
  });

  it('surfaces 404 from getLatest as EngineError', async () => {
    const client = newClient();
    await expect(client.getLatest('missing')).rejects.toBeInstanceOf(EngineError);
  });

  it('translates a network failure into a network EngineError', async () => {
    const client = newClient();
    server.use(http.get(`${BASE}/v1/definitions`, () => HttpResponse.error()));
    await expect(client.listDefinitions()).rejects.toMatchObject({
      name: 'EngineError',
      kind: 'network',
    });
  });

  it('testConnection maps statuses', async () => {
    const client = newClient();
    expect(await client.testConnection()).toBe('ok');

    server.use(
      http.get(`${BASE}/v1/definitions`, () => HttpResponse.text('nope', { status: 401 })),
    );
    expect(await client.testConnection()).toBe('unauthorized');

    server.use(
      http.get(`${BASE}/v1/definitions`, () => HttpResponse.text('boom', { status: 500 })),
    );
    expect(await client.testConnection()).toBe('error');

    server.use(http.get(`${BASE}/v1/definitions`, () => HttpResponse.error()));
    expect(await client.testConnection()).toBe('unreachable');
  });

  it('strips trailing slashes from baseUrl', async () => {
    const slashy = createEngineClient({ baseUrl: `${BASE}/` });
    await slashy.listDefinitions();
    const req = recordedRequests[0];
    if (!req) throw new Error('no request');
    expect(req.url.startsWith(`${BASE}/v1/definitions`)).toBe(true);
  });
});
