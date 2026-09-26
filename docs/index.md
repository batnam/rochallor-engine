# Rochallor Workflow Engine

A lightweight, language-agnostic workflow engine built in Go, using PostgreSQL for persistence.

By default it uses `FOR UPDATE SKIP LOCKED` for job distribution across competing workers. An opt-in **Kafka + Transaction Outbox** mode is available for high-throughput deployments.

## What it does

Define long-running business processes as a graph of steps — service tasks executed by your code, user tasks waiting for human input, decisions branching on variables, parallel branches, timers, and chained sub-workflows. The engine stores all state in PostgreSQL and hands work out to SDK workers in any language.

Use [Workflow Modeller](modeller.md) to design these workflows visually, either in
your browser or in the desktop app for Windows, macOS, and Linux. Both versions
use the same editor and JSON format. You can edit files locally and connect to a
running engine when you are ready to upload them.

**Typical use cases**

- Loan / credit origination — validate → risk checks (parallel) → decision → manual review
- Order fulfilment — reserve stock → charge payment → dispatch → notify
- Onboarding flows — collect documents → KYC → account creation → welcome email
- Approval pipelines — multi-step human approval with escalation timers

## Quick links

| | |
|---|---|
| [Getting Started](getting-started.md) | Install prerequisites, run the engine locally |
| [Workflow Modeller](modeller.md) | Use the web or desktop editor, save files, and connect to the engine |
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
