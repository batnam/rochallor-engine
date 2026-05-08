import grpc from 'k6/net/grpc';
import { Rate } from 'k6/metrics';

const PROTO_ROOT = (__ENV && __ENV.PERF_PROTO_ROOT) || '../../proto';
const client = new grpc.Client();
client.load([PROTO_ROOT], 'workflow/v1/engine.proto');
let connected = false;

export const grpcErrorRate = new Rate('grpc_error_rate');

export function connectIfNeeded(grpcUrl) {
  if (!connected) {
    client.connect(grpcUrl, { plaintext: true });
    connected = true;
  }
}

export function startWorkflow(grpcUrl, definitionId, businessKey, variables) {
  connectIfNeeded(grpcUrl);
  const res = client.invoke(
    'workflow.v1.WorkflowEngine/StartInstance',
    { definitionId, businessKey, variables: variables || {} },
    { tags: { operation: 'workflow.start' } },
  );
  return { ok: res.status === grpc.StatusOK, status: res.status, message: res.message };
}

export function pollInstance(grpcUrl, instanceId) {
  connectIfNeeded(grpcUrl);
  const res = client.invoke(
    'workflow.v1.WorkflowEngine/GetInstance',
    { id: instanceId },
    { tags: { operation: 'workflow.poll' } },
  );
  return { ok: res.status === grpc.StatusOK, status: res.status, message: res.message };
}

export function getHistory(grpcUrl, instanceId) {
  connectIfNeeded(grpcUrl);
  const res = client.invoke(
    'workflow.v1.WorkflowEngine/GetInstanceHistory',
    { id: instanceId },
    { tags: { operation: 'workflow.history' } },
  );
  return { ok: res.status === grpc.StatusOK, status: res.status, message: res.message };
}

export function listInstances(grpcUrl, opts) {
  connectIfNeeded(grpcUrl);
  const payload = { page: opts.page || 0, pageSize: opts.pageSize || 20 };
  if (opts.status !== undefined) payload.status = opts.status;
  if (opts.definitionId !== undefined) payload.definitionId = opts.definitionId;
  if (opts.businessKey !== undefined) payload.businessKey = opts.businessKey;
  const res = client.invoke(
    'workflow.v1.WorkflowEngine/ListInstances',
    payload,
    { tags: { operation: 'workflow.list' } },
  );
  return { ok: res.status === grpc.StatusOK, status: res.status, message: res.message };
}

// CompleteUserTask — addresses the user_task by stable (instanceId, userTaskStepId)
// pair. `result` is merged into instance variables by the engine.
export function completeUserTask(grpcUrl, instanceId, userTaskStepId, result, completedBy) {
  connectIfNeeded(grpcUrl);
  const res = client.invoke(
    'workflow.v1.WorkflowEngine/CompleteUserTask',
    {
      instanceId,
      userTaskStepId,
      result: result || {},
      completedBy: completedBy || 'perf-k6',
    },
    { tags: { operation: 'workflow.task_complete' } },
  );
  return { ok: res.status === grpc.StatusOK, status: res.status, message: res.message };
}
