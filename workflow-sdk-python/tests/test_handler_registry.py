"""Tests for the HandlerRegistry."""

from __future__ import annotations

import threading
from typing import Any

import pytest

from workflow_sdk.handler.registry import HandlerRegistry


def _noop(ctx: dict[str, Any]) -> dict[str, Any] | None:
    return None


def _greeter(ctx: dict[str, Any]) -> dict[str, Any]:
    return {"greeting": "hello"}


class TestHandlerRegistry:
    def setup_method(self) -> None:
        self.registry = HandlerRegistry()

    def test_register_and_get(self) -> None:
        self.registry.register("greet", _greeter)
        handler = self.registry.get("greet")
        assert handler is _greeter

    def test_get_unknown_returns_none(self) -> None:
        assert self.registry.get("unknown-type") is None

    def test_empty_job_type_raises_value_error(self) -> None:
        with pytest.raises(ValueError, match="empty"):
            self.registry.register("", _noop)

    def test_job_types_returns_all_registered(self) -> None:
        self.registry.register("type-a", _noop)
        self.registry.register("type-b", _noop)
        self.registry.register("type-c", _noop)
        types = self.registry.job_types()
        assert set(types) == {"type-a", "type-b", "type-c"}

    def test_job_types_empty_registry(self) -> None:
        assert self.registry.job_types() == []

    def test_register_overwrites_existing(self) -> None:
        self.registry.register("greet", _noop)
        self.registry.register("greet", _greeter)
        assert self.registry.get("greet") is _greeter

    def test_thread_safe_concurrent_registration(self) -> None:
        errors: list[Exception] = []

        def register_worker(n: int) -> None:
            try:
                for i in range(10):
                    self.registry.register(f"type-{n}-{i}", _noop)
            except Exception as e:
                errors.append(e)

        threads = [threading.Thread(target=register_worker, args=(n,)) for n in range(10)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()

        assert not errors
        # 10 threads × 10 registrations = 100 unique types
        assert len(self.registry.job_types()) == 100

    def test_thread_safe_concurrent_reads(self) -> None:
        self.registry.register("read-type", _greeter)
        errors: list[Exception] = []

        def reader() -> None:
            try:
                for _ in range(100):
                    assert self.registry.get("read-type") is _greeter
            except Exception as e:
                errors.append(e)

        threads = [threading.Thread(target=reader) for _ in range(20)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()

        assert not errors
