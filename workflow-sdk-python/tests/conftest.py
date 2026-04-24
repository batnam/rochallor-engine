"""Shared pytest fixtures for the workflow SDK test suite."""

from __future__ import annotations

import pytest


BASE_URL = "http://localhost:8080"


@pytest.fixture
def base_url() -> str:
    """Base URL for the workflow engine (used in client tests)."""
    return BASE_URL


@pytest.fixture
def sample_job() -> dict:
    """A minimal job dict as returned by the engine's poll endpoint."""
    return {
        "id": "job-001",
        "jobType": "process-order",
        "instanceId": "inst-001",
        "stepId": "step-1",
        "stepExecutionId": "exec-001",
        "retriesRemaining": 3,
        "variables": {"orderId": "ORD-42"},
        "lockExpiresAt": "2026-04-13T10:00:00Z",
    }


@pytest.fixture
def sample_definition() -> dict:
    """A minimal workflow definition dict."""
    return {
        "id": "order-workflow",
        "name": "Order Workflow",
        "steps": [
            {
                "id": "process-step",
                "name": "Process Order",
                "type": "SERVICE_TASK",
                "jobType": "process-order",
                "retryCount": 3,
            }
        ],
    }


@pytest.fixture
def sample_instance() -> dict:
    """A minimal workflow instance summary dict."""
    return {
        "id": "inst-001",
        "definitionId": "order-workflow",
        "definitionVersion": 1,
        "status": "ACTIVE",
        "currentStepIds": ["process-step"],
        "variables": {"orderId": "ORD-42"},
        "startedAt": "2026-04-13T09:00:00Z",
    }
