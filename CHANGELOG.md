# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.0.0] - 2026-05-16

Rochallor Engine v1.0.0: Lightweight, Language-Agnostic Workflow Orchestration

I'm thrilled to announce the inaugural v1.0.0 release of the Rochallor Workflow Engine!

Rochallor is built from the ground up to provide a lightweight, language-agnostic way to orchestrate long-running business processes. Written in Go and backed by PostgreSQL / Kafka (opt-in), it provides robust state management without the overhead of heavy frameworks.

### Key Features

- **Language-Agnostic SDKs**: First-class support for workers in Go, Java, Node/TypeScript, and Python. Build stateless workers in the language that fits your team.
- **Dual Dispatch Architecture**:
  - *Short-Polling (Default)*: Simple, infrastructure-light polling relying on PostgreSQL `FOR UPDATE SKIP LOCKED`. Perfect for standard workloads.
  - *Kafka + Transaction Outbox (Opt-in)*: High-throughput, event-driven push model for deployments that need to scale beyond database polling limits.
- **Rich Step Types**: Define complex graphs using `SERVICE_TASK`, `USER_TASK`, `DECISION`, `DECISION_TABLE`, `PARALLEL_GATEWAY`, `WAIT`, and more, entirely via JSON definitions.
- **Resilient Execution**: At-least-once delivery semantics, automatic lease expiration, idempotent completions, and built-in retry mechanisms.
- **Stateless by Design**: Workers hold no in-memory state between jobs, making scaling and redeployments trivial.
- **Visual Modeller**: Transform static JSON definitions into interactive, readable diagrams for instant architectural clarity.

Check out our [User Guide](README.md) to spin up the engine and run your first workflow!
