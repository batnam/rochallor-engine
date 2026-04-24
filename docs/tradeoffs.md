# Tradeoffs: Polling vs Event-Driven (Kafka + Outbox)

**Feature**: Event-Driven Job Dispatch via Kafka + Transaction Outbox (Opt-In)
**Branch**: `006-kafka-outbox-dispatch`
**Companion to**: `spec.md`, `research.md`
**Purpose**: An honest comparison of the two dispatch modes this feature makes available. Readers should come away understanding **when each mode wins** and **what they are signing up for** if they opt into event-driven mode.

---

## Side-by-side comparison

| Dimension | Polling (`SELECT … FOR UPDATE SKIP LOCKED`) | Event-Driven (Kafka + Transaction Outbox) |
|---|---|---|
| **Infrastructure dependencies** | PostgreSQL only | PostgreSQL + Kafka cluster |
| **Source of truth** | One (the `job` table row) | Two (outbox row + broker offsets), kept consistent by the outbox pattern |
| **Delivery semantics** | Strong — the row lock is the single truth | At-least-once; idempotency is the worker's responsibility |
| **Dispatch latency (p50)** | Bounded below by half the poll interval (≈200–500 ms typical) | Milliseconds — push-based end-to-end |
| **Dispatch throughput ceiling** | Caps at the point where PG row-lock contention dominates | Caps at broker throughput — orders of magnitude higher in practice |
| **Engine replica scaling** | Adding replicas increases PG connection pressure without adding dispatch throughput | Adding replicas scales throughput approximately linearly until broker/worker capacity bounds |
| **Ordering guarantee** | Effectively FIFO per `job_type` across the deployment | FIFO per workflow instance (partition key = `instance_id`); not globally FIFO per job type |
| **Backpressure visibility** | Hidden — observable only indirectly via lock-wait metrics | Explicit — outbox backlog gauge, consumer lag per group |
| **Replay / DLQ / retention** | Must be built on top of the DB by hand | First-class broker primitives |
| **Multi-region / DR** | Hard — replicating a work-queue table reliably is non-trivial | Easier — broker-level replication (MirrorMaker 2, Redpanda replicas) is a well-worn path |
| **Debugging "where's my job?"** | One SQL query | Four-to-five checkpoints: outbox row → producer ack → broker topic → consumer group → worker log |
| **Schema evolution (on-wire events)** | N/A — workers pull structured DB rows | Must respect Protobuf field-number rules; a single wrong renumber is a production hazard |
| **Baseline infrastructure cost at small scale** | Near zero | Non-trivial — even a minimal HA Kafka setup costs money and ops time |
| **Team cognitive load** | Low (SQL state machine) | Higher (partitions, consumer groups, offsets, rebalances, leader election) |
| **Blast radius if infra is down** | PG down = whole engine down anyway | Kafka down = dispatch stops, but engine write path still works (outbox keeps accepting rows) |
| **Testing surface** | PostgreSQL testcontainer | PostgreSQL testcontainer + Redpanda testcontainer + broker-outage / rebalance simulation |
| **New failure modes introduced** | — | Broker outage, consumer rebalance storm, partition imbalance, relay crash mid-batch, split-brain if advisory lock is misconfigured |

---

## Advantages of event-driven mode that polling genuinely cannot match

These are *structural* wins — you cannot get them by tuning the polling path harder.

1. **No row-lock contention on dispatch.** The core motivation. At a certain volume (tens of thousands of pending jobs with many competing worker fleets), PostgreSQL's row-lock wait dominates and polling cannot scale further on the same hardware. Event-driven dispatch takes the hot path off the database entirely.
2. **Engine replica scaling becomes approximately linear.** In polling mode, every replica competes for the same rows — adding replicas increases contention. In event-driven mode, replicas are independent producers; the broker handles fan-out. Operators who care about horizontal scale for job-creation throughput get real leverage here.
3. **Dispatch latency drops from poll-interval-floor to milliseconds.** For workflows where a job created at `t=0` must reach a worker immediately rather than within the next poll cycle, this is a qualitative difference, not a quantitative one.
4. **Backpressure becomes observable.** Consumer lag and outbox backlog are directly measurable and directly alertable. Polling mode hides the same information inside lock-wait metrics that are harder to threshold and harder to explain to a non-DBA.
5. **Worker fleets become isolated from the database.** Workers talk to a broker, not to PG. A misbehaving worker fleet (hot-loop connections, leaked prepared statements, whatever) can hurt the broker but cannot take down PG — which is also where the engine's definitions, instances, and audit log live.
6. **The broker becomes a reusable spine for other events.** If the system later needs to emit `instance_started` / `instance_completed` events to other services, the broker is already there. Polling mode would require a second ad-hoc distribution channel for that use case.

---

## Disadvantages of event-driven mode that cannot be fixed by documentation

These are costs you pay forever once you flip the switch. Operators should read them as the price list.

1. **Dual-source state.** The outbox row and the broker's internal offsets are both state. The outbox pattern guarantees durability of the *handoff*, but the cognitive load of reasoning about "where the job currently is" does not go away. A new contributor has to learn both halves.
2. **At-least-once delivery becomes a worker contract.** Workers will occasionally see the same job twice (producer retries, relay restarts, broker replays). The engine's completion path is idempotent. The *handler's side-effects* (charging a card, sending an email, mutating an external system) must also be idempotent. The most expensive bugs in event-driven systems are non-idempotent handlers written by developers who forgot to read the contract.
3. **Failure-mode count multiplies.** Broker connectivity, consumer rebalance, partition imbalance, relay lag, split-brain advisory lock, topic deletion, ACL misconfiguration — each is a new class of incident with its own dashboard, alert, and runbook. Polling mode has one failure class (the DB is down).
4. **Ordering semantics change shape.** Polling gives effective global FIFO per `job_type`. Event-driven gives per-instance FIFO. If any workflow silently depends on cross-instance order-of-arrival, it will break subtly when the mode is flipped. This is why the spec now explicitly calls it out as an operator review item (see `spec.md` Assumptions).
5. **Ops cost is non-zero even at low traffic.** A production Kafka cluster is three brokers minimum for HA, plus monitoring, plus schema discipline. Redpanda lowers this floor but does not eliminate it. At low traffic, event-driven mode is strictly more expensive to run than polling mode.
6. **Testing is more complex.** Simulating broker outage, rebalance storms, large-payload rejection, and relay-crash-mid-batch all require real broker test infrastructure. This feature's test plan (see `tasks.md`) includes ~10 integration tests just for the new path.
7. **Schema evolution requires discipline.** Protobuf field-number rules are a real contract. One PR that renumbers a field, or reuses a removed number, is a production outage waiting for its trigger. This is a new review discipline the team must adopt.
8. **Minimum dispatch latency at low load may be slightly higher.** Polling can be extremely fast for a single job in an idle system (one `UPDATE … RETURNING`). Event-driven has to go `outbox-commit → relay batch → broker ack → consumer fetch`. The p50 at high load is much lower for event-driven, but the best-case single-job latency on an idle system can be lower for polling.

---

## Decision rubric: when to flip the switch

Polling mode is the correct default. Event-driven mode is the correct choice **only when both of the following are true**:

1. The workload has hit, or will measurably hit within 6–12 months, the polling path's row-lock contention ceiling. The concrete signal is sustained `wait_event_type='Lock'` on the `job` table during load, or observable engine throughput plateauing regardless of added replicas.
2. The operating team either has Kafka operational expertise, or has a credible plan to invest in it. Kafka is not a "set it and forget it" dependency; treating it as one is how production incidents are made.

If *either* condition fails, polling mode remains the better engineering choice — even if event-driven looks more modern. Simpler wins at the scale it fits.

This rubric is also why the feature ships as opt-in rather than as a replacement. There is no "right" dispatch mode in isolation; there is only a right mode *for this workload at this team's maturity*.

---