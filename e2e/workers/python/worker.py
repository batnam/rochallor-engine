"""E2E Python worker — handles python-* job types for integration tests."""

import os
import logging
import sys

from workflow_sdk.client.rest import RestEngineClient
from workflow_sdk.client.grpc import GrpcEngineClient
from workflow_sdk.handler.registry import HandlerRegistry
from workflow_sdk.runner.runner import Runner
from workflow_sdk.runner.kafka_runner import KafkaRunner


def _make_step_handler(name: str):
    def handler(ctx: dict) -> dict:
        return {name: "done"}
    return handler


def main() -> None:
    engine_url = os.environ.get("ENGINE_REST_URL", "http://localhost:8080")
    grpc_host = os.environ.get("ENGINE_GRPC_HOST", "localhost:9090")
    worker_transport = os.environ.get("WORKER_TRANSPORT", "rest")
    worker_id = os.environ.get("WORKER_ID", "worker-python-1")
    log_level_str = os.environ.get("WE_LOG_LEVEL", "INFO").upper()

    # Configure logging
    log_level = getattr(logging, log_level_str, logging.INFO)
    logging.basicConfig(
        level=log_level,
        format='{"time":"%(asctime)sZ","level":"%(levelname)s","msg":"%(message)s"}',
        datefmt='%Y-%m-%dT%H:%M:%S',
        stream=sys.stdout
    )

    logger = logging.getLogger("worker-python")

    if worker_transport == "grpc":
        client = GrpcEngineClient(grpc_host)
        logger.info(f"worker starting (grpc): host={grpc_host} workerID={worker_id}")
    else:
        client = RestEngineClient(engine_url)
        logger.info(f"worker starting (rest): engine={engine_url} workerID={worker_id}")
    registry = HandlerRegistry()

    # Linear scenario handlers
    registry.register("python-step-a", _make_step_handler("stepA"))
    registry.register("python-step-b", _make_step_handler("stepB"))
    registry.register("python-step-c", _make_step_handler("stepC"))

    # Decision scenario handlers
    registry.register("python-prepare", lambda ctx: {"result": "approved"})
    registry.register("python-handle-approved", lambda ctx: {"handled": "approved"})
    registry.register("python-handle-rejected", lambda ctx: {"handled": "rejected"})

    # Parallel scenario handlers
    registry.register("python-branch-left", lambda ctx: {"branchLeft": "done"})
    registry.register("python-branch-right", lambda ctx: {"branchRight": "done"})
    registry.register("python-merge-done", lambda ctx: {"merged": "done"})

    # User-task scenario handlers
    registry.register("python-before-review", lambda ctx: {"beforeReview": "done"})
    registry.register("python-after-review", lambda ctx: {"afterReview": "done"})

    # Timer scenario handlers
    registry.register("python-before-wait", lambda ctx: {"beforeWait": "done"})
    registry.register("python-timer-fired", lambda ctx: {"timerFired": "done"})

    # Retry-fail scenario handler: fails on first attempt (retriesRemaining == 2 == retryCount)
    def _py_flaky(ctx: dict) -> dict:
        if ctx.get("retriesRemaining", 0) == 2:
            raise Exception("simulated transient failure")
        return {"flaky": "done"}

    registry.register("python-flaky", _py_flaky)

    # Signal-user-task scenario handlers
    registry.register("python-signalwaitstep-completeusertask-start", lambda ctx: {"started": True})
    registry.register("python-signalwaitstep-completeusertask-end", lambda ctx: {"ended": True})

    # Chaining scenario handlers
    registry.register("python-chain-start", lambda ctx: {"applicantId": "123", "amount": 100.0})
    registry.register("python-chain-finalize", lambda ctx: {"finalized": True})

    # Transformation scenario handler
    registry.register("python-transform-init", lambda ctx: {"firstName": "Alice"})

    # Retry-exhausted scenario handler: always fails to exhaust all retries
    def _py_always_fail(ctx):
        raise Exception("always fails")
    registry.register("python-always-fail", _py_always_fail)

    # Decision-no-match scenario handler: sets result to "rejected" so no branch matches
    registry.register("python-prepare-no-match", lambda ctx: {"result": "rejected"})

    # Parallel-user-task scenario handler
    registry.register("python-put-svc-branch", lambda ctx: {"svcBranchDone": True})

    # Loan approval scenario handlers
    registry.register("validate-application", lambda ctx: {"applicationValidated": True})
    registry.register("credit-score", lambda ctx: {"creditScoreChecked": True})
    registry.register("fraud-screen", lambda ctx: {"fraudScreened": True})
    registry.register("escalate-review", lambda ctx: {"reviewEscalated": True})
    registry.register("approve-loan", lambda ctx: {"loanApproved": True})
    registry.register("notify-approval-overdue", lambda ctx: {"approvalOverdueNotified": True})
    registry.register("prepare-disbursement", lambda ctx: {"disbursementPrepared": True})
    registry.register("transfer-funds", lambda ctx: {"fundsTransferred": True})
    registry.register("notify-disbursement", lambda ctx: {"disbursementNotified": True})

    mode = os.environ.get("WE_DISPATCH_MODE", "polling")

    if mode == "kafka_outbox":
        brokers = os.environ.get("WE_KAFKA_SEED_BROKERS", "localhost:9092")
        runner = KafkaRunner(worker_id=worker_id, brokers=brokers, client=client, registry=registry)
        runner.run()
    else:
        runner = Runner(client, registry, worker_id=worker_id)
        runner.run()
    
    logger.info("worker stopped")


if __name__ == "__main__":
    main()
