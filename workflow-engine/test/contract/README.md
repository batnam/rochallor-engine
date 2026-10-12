# REST contract replay

From the repository root:

```sh
python3 -m venv /tmp/engine-contract
/tmp/engine-contract/bin/pip install -r workflow-engine/test/contract/requirements.txt
/tmp/engine-contract/bin/python -m unittest discover -s workflow-engine/test/contract -p 'test_*.py' -v
/tmp/engine-contract/bin/python workflow-engine/test/contract/run.py --output=testresults/contract
```

The command builds the current engine, creates disposable PostgreSQL/Kafka containers with unique names and loopback ports, and invokes `make contract-replay` for each dispatch mode. It saves engine, database, broker and replay logs, then removes only its own containers. No existing database or worker is used. `--mode=polling` and `--engine=/absolute/path/to/binary` select a mode or an already-built candidate.

Against an existing **test** engine, install the requirements in the active Python environment, then run `WE_REST_PORT=8080 WE_DISPATCH_MODE=polling make -C workflow-engine contract-replay`. The replay writes uniquely named definitions and instances, including a deliberately FAILED instance. Use a disposable test database.

Each successful response is checked against the status-specific OpenAPI 3.1 schema, including nested references, required fields, types and RFC 3339 timestamps. Successful requests are schema-validated too. Invalid-input probes deliberately bypass request validation. Wrong HTTP status/content type, invalid JSON, schema mismatch, failed business assertions, unreachable server or missing format validator make the command exit non-zero.

## Coverage at T01

| Endpoint | Assertions |
|---|---|
| POST `/v1/definitions` | 201 schema and id/version; invalid definition 400; XML 415 |
| GET `/v1/definitions/{id}` | 200 schema and unchanged steps; missing id 404 |
| GET `/v1/definitions/{id}/versions/{version}` | 200 schema |
| POST `/v1/instances` | 201 schema, ACTIVE service task or COMPLETED end-only workflow; missing definitionId 400 |
| GET `/v1/instances/{id}` | 200 schema; missing id 404; final COMPLETED/FAILED and merged output/failure reason |
| POST `/v1/jobs/poll` | polling: empty response, required input 400, one claimed job/variables/retry budget/expiry schema; Kafka: 410 schema |
| POST `/v1/jobs/{id}/complete` | 204 empty response and completed workflow |
| POST `/v1/jobs/{id}/fail` | non-retryable 204 empty response and failed workflow |

T01 has 18 polling and 11 Kafka requests. This is coverage of these flows, not a claim of full API conformance. Lease duration behavior belongs to R14; readiness and heartbeat extend this suite in R10/R15. gRPC behavior is covered by component/E2E gates and gains lease/heartbeat contract cases in those releases.

## Current coverage after R10/R14/R15

The cumulative candidate runs **31 polling + 13 Kafka requests**. It adds
`GET /readyz`, validates default/custom polling expiry and invalid durations,
and exercises heartbeat acceptance, wrong-owner rejection, invalid duration,
and terminal-attempt rejection after both complete and fail. Real REST/gRPC
lease API and PostgreSQL invariant tests additionally verify DB-clock timing,
concurrent claim, lease loss and lifecycle races. The historical T01 counts
above describe its baseline-only tooling gate.

## Baseline contract decisions

The existing REST representation is authoritative for these documentation corrections; T01 changes no engine runtime behavior:

- Common errors have `error` and `reason`, not `message`/`see`.
- Disabled polling has its own `error`, `dispatch_mode`, `hint` representation. Its strict schema is separate from common errors.
- Empty polls return `jobs: null`; populated polls return an array. Both representations remain supported by existing SDKs.
- A job exposes `stepExecutionId`, not a definition `stepId`. The latter is available through execution history, not the polling DTO.
- Poll validation already returns 400; that response was missing from OpenAPI.

Unverified surfaces remain explicit follow-up coverage: definition/instance list pagination, history, cancel, manual retry, user-task completion and signals. In particular, the legacy history DTO lacks JSON tags (capitalized fields), while OpenAPI describes camelCase; this discrepancy is not fixed or reported conformant by T01. Input bounds/defaults outside the exercised cases, including the old leaseSeconds description, are not behavior guarantees from schema validation. R14 resolves lease semantics.
