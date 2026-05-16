# Rochallor Workflow Engine

A lightweight, language-agnostic workflow engine built in Go, using PostgreSQL for persistence.

By default it uses `FOR UPDATE SKIP LOCKED` for job distribution across competing workers. An opt-in **Kafka + Transaction Outbox** mode is available for high-throughput deployments.

## What it does

Define long-running business processes as a graph of steps — service tasks executed by your code, user tasks waiting for human input, decisions branching on variables, parallel branches, timers, and chained sub-workflows. The engine stores all state in PostgreSQL and hands work out to SDK workers in any language.

**Typical use cases**

- Loan / credit origination — validate → risk checks (parallel) → decision → manual review
- Order fulfilment — reserve stock → charge payment → dispatch → notify
- Onboarding flows — collect documents → KYC → account creation → welcome email
- Approval pipelines — multi-step human approval with escalation timers

## Quick links

| | |
|---|---|
| [Getting Started](getting-started.md) | Install prerequisites, run the engine locally |
| [Architecture](architecture.md) | How the engine works internally |
| [Workflow Format](workflow-format.md) | JSON schema for workflow definitions |
| [Configuration](configuration.md) | All environment variables and config options |
| [Helm](helm.md) | Deploy on Kubernetes |

## SDK References

Pick the language that matches your worker:

- [Go SDK](sdk/go.md)
- [Python SDK](sdk/python.md)
- [Node / TypeScript SDK](sdk/node.md)
- [Java SDK](sdk/java.md)
