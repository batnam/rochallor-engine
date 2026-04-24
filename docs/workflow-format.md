# Workflow JSON Format

A workflow definition is a single JSON object that describes the directed graph of steps the engine executes. Upload it once via `POST /v1/definitions`; the engine stores it and assigns a version number. All subsequent instances reference that definition by `id` and `version`.

## Quick-reference skeleton

```json
{
  "id": "my-namespace::my-workflow",
  "name": "Human-readable workflow name",
  "description": "Optional free-form description",
  "autoStartNextWorkflow": false,
  "nextWorkflowId": "",
  "steps": [ /* ordered array — first element is the entry point */ ],
  "metadata": { /* any JSON key-value pairs, stored as-is */ }
}
```

---

## Top-level fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | string | **yes** | Unique natural key. Max 256 chars. Pattern: `^[A-Za-z0-9_:\-]+$` (letters, digits, `_`, `:`, `-`). Convention: `NAMESPACE::workflow-name`. |
| `name` | string | **yes** | Human-readable label shown in logs and audit records. |
| `description` | string | no | Free-form text; stored and returned as-is. |
| `steps` | array | **yes** | Ordered step nodes. Must not be empty. The **first element** is always the entry point. |
| `autoStartNextWorkflow` | boolean | no | When `true`, the engine automatically starts a new instance of `nextWorkflowId` as soon as this workflow reaches an `END` step. |
| `nextWorkflowId` | string | conditional | Required (and must be non-empty) when `autoStartNextWorkflow` is `true`. Must match the `id` of another uploaded definition. |
| `metadata` | object | no | Arbitrary JSON key-value pairs (strings, numbers, arrays, nested objects). The engine stores the raw JSON and never inspects it. Use for categorisation, authoring info, etc. |

> **ID format rule**: `LOS::loan-registration-workflow` and `greet-workflow` are valid. `my workflow` (space) or `order@v2` (`@`) are not — the engine rejects them with HTTP 400.

---

## Step object

Every element of the `steps` array is a step object. The `type` field is the discriminator that determines which other fields are meaningful.

### Common fields (all step types)

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | string | **yes** | Step identifier — must be unique within the definition. Used in `nextStep` / `conditionalNextSteps` references. |
| `name` | string | **yes** | Human-readable label. Appears in history and audit logs. |
| `type` | string | **yes** | One of: `SERVICE_TASK`, `USER_TASK`, `DECISION`, `TRANSFORMATION`, `WAIT`, `PARALLEL_GATEWAY`, `JOIN_GATEWAY`, `END`. |
| `description` | string | no | Optional free-form text. |
| `boundaryEvents` | array | no | Timer events that fire while this step is active. See [Boundary Events](#boundary-events). Supported on `SERVICE_TASK`, `USER_TASK`, and `WAIT`. |

---

## Step types

### `SERVICE_TASK` — automated job

The engine creates a job record for this step, which the SDK worker polls, executes, and completes. This is the primary integration point between the engine and your application code.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `nextStep` | string | no       | ID of the next step after this task completes. If omitted the workflow stops at this step (use only for deliberate dead-ends or terminal tasks). |
| `jobType` | string | yes      | Label passed to the SDK worker so it knows which handler to run. Required in practice if you want workers to process the job. |
| `delegateClass` | string | no       | Advisory string preserved for compatibility. The engine stores it but never loads or reflects on it. |
| `retryCount` | integer | no       | How many times the engine retries a failed job before marking the step `FAILED`. Default `0` (no retries). |

```json
{
  "id": "send-notification",
  "name": "Send Email Notification",
  "type": "SERVICE_TASK",
  "jobType": "send-email",
  "retryCount": 3,
  "nextStep": "end-workflow"
}
```

Worker side — register a handler matching `jobType`:

```python
@registry.register("send-email")
def handle(ctx):
    email = ctx["variables"]["email"]
    send_email(email)
    return {"emailSent": True}
```

---

### `USER_TASK` — human action required

The engine pauses at this step and waits for an external `POST /v1/instances/{instanceId}/user-tasks/{userTaskId}/complete` call (e.g. from a web UI or approval system), where `userTaskId` is the stable step id from the workflow definition. The workflow resumes on `nextStep` once completed.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `nextStep` | string | no | ID of the step to advance to after completion. |
| `jobType` | string | no | Optional label for routing to different UI forms or task-list views. |

```json
{
  "id": "manager-approval",
  "name": "Manager Approval",
  "type": "USER_TASK",
  "nextStep": "process-decision",
  "boundaryEvents": [
    {
      "type": "TIMER",
      "duration": "PT24H",
      "interrupting": false,
      "targetStepId": "escalate-to-director"
    }
  ]
}
```

---

### `DECISION` — conditional branching

Evaluates a set of boolean expressions against the current workflow variables and advances to the first matching target step. Expressions are evaluated in the order they appear in the JSON object.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `conditionalNextSteps` | object | **yes** | Map of `expression → stepId`. Must have at least one entry. All target step IDs must exist in the definition. |

```json
{
  "id": "check-credit-score",
  "name": "Check Credit Score",
  "type": "DECISION",
  "conditionalNextSteps": {
    "#creditScore >= 700": "approve-loan",
    "#creditScore >= 500 && #creditScore < 700": "manual-review",
    "#creditScore < 500": "reject-loan"
  }
}
```

Expressions in `conditionalNextSteps` must evaluate to a **boolean**. A non-boolean result (e.g. a bare arithmetic expression) will fail the step. See [Expression Reference](#expression-reference) for the full syntax, operators, and built-in functions.

---

### `TRANSFORMATION` — variable mutation

Sets or rewrites workflow variables using literal values or expressions, then immediately advances to `nextStep` without creating a job or waiting for any external action.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `nextStep` | string | **yes** | Must be provided — the transformation completes instantly. |
| `transformations` | object | **yes** | Map of `variableName → value`. Values may be any JSON literal or an expression string `"${expr}"`. Must not be empty. |

Values in `transformations` may be any JSON literal or an expression string wrapped in `"${...}"`. Expressions can return any type — numbers, strings, booleans — and the result is assigned directly to the variable. See [Expression Reference](#expression-reference) for full syntax.

```json
{
  "id": "compute-fee",
  "name": "Compute Processing Fee",
  "type": "TRANSFORMATION",
  "transformations": {
    "processingFee": 50,
    "currency": "USD",
    "totalWithFee": "${loanAmount + 50}",
    "isHighValue": "${loanAmount > 100000}"
  },
  "nextStep": "notify-applicant"
}
```

---

### `WAIT` — pause until signalled

The engine parks the workflow at this step. It resumes either when an attached boundary timer fires, or when a caller posts to `POST /v1/instances/{instanceId}/signals/{waitStepId}` (where `waitStepId` is the stable step id from the definition). The request body — if present — is shallow-merged into the instance variables on resume; an empty body is valid (the signal itself is the event).

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `nextStep` | string | **yes** | Step to advance to when the wait ends. |

```json
{
  "id": "wait-for-payment",
  "name": "Wait for Payment Confirmation",
  "type": "WAIT",
  "nextStep": "verify-payment",
  "boundaryEvents": [
    {
      "type": "TIMER",
      "duration": "PT72H",
      "interrupting": false,
      "targetStepId": "payment-timeout-handler"
    }
  ]
}
```

---

### `PARALLEL_GATEWAY` + `JOIN_GATEWAY` — fan-out / fan-in

Use these two step types together to execute multiple branches concurrently. The `PARALLEL_GATEWAY` spawns all branches simultaneously; the `JOIN_GATEWAY` waits for all of them to complete before advancing.

**`PARALLEL_GATEWAY`**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `parallelNextSteps` | array of string | **yes** | IDs of steps to start in parallel. Minimum 2 entries. |
| `joinStep` | string | **yes** | ID of the `JOIN_GATEWAY` that collects this gateway's branches. |

**`JOIN_GATEWAY`**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `nextStep` | string | **yes** | Step to advance to once all parallel branches have reached this join point. |

```json
{
  "id": "parallel-checks",
  "name": "Run Checks in Parallel",
  "type": "PARALLEL_GATEWAY",
  "parallelNextSteps": ["credit-check", "identity-check", "fraud-check"],
  "joinStep": "merge-check-results"
},
{
  "id": "merge-check-results",
  "name": "Merge Check Results",
  "type": "JOIN_GATEWAY",
  "nextStep": "evaluate-checks"
}
```

> Each parallel branch must eventually reach the `JOIN_GATEWAY` via its own `nextStep` chain — not through another `PARALLEL_GATEWAY`. Nesting parallel gateways is not supported.

---

### `END` — terminal step

Marks the end of the workflow. An instance that reaches an `END` step is set to `COMPLETED` status. A definition must have at least one `END` step reachable from the first step.

No additional fields are required.

```json
{
  "id": "end-workflow",
  "name": "Workflow Complete",
  "type": "END"
}
```

---

## Boundary Events

A boundary event fires while its parent step is still active. Only `TIMER` events are supported. Attach them to `SERVICE_TASK`, `USER_TASK`, or `WAIT` steps.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `type` | string | **yes** | Must be `"TIMER"`. |
| `duration` | string | **yes** | ISO-8601 duration (e.g. `"PT30S"` = 30 seconds, `"PT24H"` = 24 hours, `"P7D"` = 7 days). |
| `interrupting` | boolean | **yes** | `false` — timer fires but the parent step continues (non-interrupting). `true` — timer cancels the parent step and redirects the flow. |
| `targetStepId` | string | **yes** | ID of the step to activate when the timer fires. |

```json
{
  "id": "checker-approval",
  "name": "Checker Approval",
  "type": "USER_TASK",
  "nextStep": "finalize",
  "boundaryEvents": [
    {
      "type": "TIMER",
      "duration": "PT30S",
      "interrupting": false,
      "targetStepId": "escalate-task"
    }
  ]
}
```

> **Design note**: `escalate-task` (the `targetStepId`) must be reachable from the definition's first step. If the escalation path should terminate the workflow, give `escalate-task` its own `nextStep` pointing to an `END` step — otherwise the workflow will remain `ACTIVE` indefinitely after escalation.

---

## Variables and data flow

Variables are a `map<string, any>` scoped to each workflow instance. They are:

- Seeded when `POST /v1/instances` is called (via the `variables` field in the request body).
- **Merged** at each step completion: a worker returns `{"key": value, ...}` and the engine deep-merges it into the instance variables. Existing keys are overwritten; keys not returned are preserved.
- Passed to every `SERVICE_TASK` job in the `Job.variables` field (the full current map).
- Evaluated by `DECISION` steps against the live variable map at the moment the step runs.
- Set by `TRANSFORMATION` steps without requiring a worker.

Variable keys are arbitrary strings. There is no schema enforcement — structure them to match what your handlers produce and your DECISION expressions consume.

---

## Expression Reference

Expressions are used in two step types:

- **`DECISION`** — each expression in `conditionalNextSteps` must return a **boolean**. The engine evaluates them in order and follows the first `true` branch. A non-boolean result fails the step.
- **`TRANSFORMATION`** — expressions inside `"${...}"` values may return **any type** (number, string, boolean). The result is assigned directly to the target variable.

### Variable references

Three styles are supported and can be mixed freely:

| Style | Example | Notes |
|-------|---------|-------|
| bare identifier | `creditScore` | Standard form |
| `#ident` | `#creditScore` | Legacy form — identical to bare identifier |
| `${ident}` | `${creditScore}` | Wraps a single identifier or full sub-expression |

**Nested field access** — if a variable `result` holds an object `{"eligible": true, "score": 720}`, access its fields with dot notation:

```
result.eligible        // → true
result.score           // → 720
${result.eligible}     // same, wrapped form
```

---

### Operators

**Comparison** (always return `bool`)

| Operator | Example |
|----------|---------|
| `==` | `status == 'APPROVED'` |
| `!=` | `retryCount != 0` |
| `>` | `age > 18` |
| `>=` | `creditScore >= 700` |
| `<` | `riskScore < 0.5` |
| `<=` | `amount <= 10000` |

**Arithmetic** (return a number — useful in `TRANSFORMATION`)

| Operator | Example |
|----------|---------|
| `+` | `baseAmount + fee` |
| `-` | `total - discount` |
| `*` | `price * quantity` |
| `/` | `totalCost / itemCount` |

**Boolean**

| Operator | Example |
|----------|---------|
| `&&` | `valid == true && creditScore >= 600` |
| `\|\|` | `bypass == true \|\| creditScore >= 800` |
| `!` | `!blacklisted` |
| `(…)` | `(a > 1 && b < 5) \|\| c == true` |

**Membership**

| Operator | Example |
|----------|---------|
| `in` | `"ADMIN" in user.roles` |

---

### Built-in functions

| Function | Signature | Returns | Description |
|----------|-----------|---------|-------------|
| `contains` | `contains(collection, element)` | `bool` | Reports whether `element` is present in `collection`. Works on arrays. |
| `len` | `len(collection)` | `int` | Returns the length of an array, map, or string. |

> `contains(col, elem)` is internally rewritten to `elem in col`. Both produce identical results — use whichever reads more naturally.

---

### Literal values

| Type | Syntax | Examples |
|------|--------|---------|
| Integer | bare number | `700`, `0`, `-1` |
| Float | decimal | `0.9`, `3.14` |
| Boolean | keyword | `true`, `false` |
| String | single or double quoted | `'APPROVED'`, `"ADMIN"` |

---

### Examples by use case

**Role check in DECISION**

```json
"conditionalNextSteps": {
  "contains(user.roles, 'ADMIN')": "admin-path",
  "contains(user.roles, 'REVIEWER')": "review-path"
}
```

**Array length guard in DECISION**

```json
"conditionalNextSteps": {
  "len(attachments) > 0": "process-attachments",
  "len(attachments) == 0": "skip-attachments"
}
```

**Arithmetic in TRANSFORMATION**

```json
"transformations": {
  "totalWithFee": "${loanAmount + processingFee}",
  "discountedPrice": "${unitPrice * quantity - discount}",
  "averageScore": "${totalScore / reviewCount}"
}
```

**Boolean flag derived from variables in TRANSFORMATION**

```json
"transformations": {
  "isEligible": "${creditScore >= 600 && !blacklisted}",
  "isHighValue": "${loanAmount > 100000}"
}
```

**Combining membership and arithmetic in DECISION**

```json
"conditionalNextSteps": {
  "contains(user.roles, 'LOAN_OFFICER') && loanAmount <= 50000": "fast-track-approve",
  "creditScore >= 700": "standard-approve",
  "creditScore < 700": "manual-review"
}
```

---

### Error behaviour

| Situation | Result |
|-----------|--------|
| `DECISION` expression returns a non-boolean (e.g. a bare number) | Step fails with a descriptive error |
| No branch matches in `DECISION` | Step fails with `DecisionNoBranchMatched` |
| Undefined variable referenced | Runtime error; step fails |
| Malformed expression (unclosed quote, syntax error) | Rejected at evaluation time; step fails |

---

## Chaining workflows

Set `autoStartNextWorkflow: true` and `nextWorkflowId` to automatically trigger a follow-on workflow when this one ends.

```json
{
  "id": "LOS::loan-registration-workflow",
  "name": "Loan Registration",
  "autoStartNextWorkflow": true,
  "nextWorkflowId": "LOS::loan-pre-approve-workflow",
  "steps": [ ... ]
}
```

The engine starts the next workflow instance with the same variables the current instance had at the time it reached `END`. There is no limit to the chain length, but cycles will loop indefinitely — design accordingly.

---

## Validation rules

The engine rejects an upload with HTTP 400 and a descriptive error if any of these rules are violated:

| Rule | Detail |
|------|--------|
| `id` is required | Non-empty, ≤ 256 characters, matches `^[A-Za-z0-9_:\-]+$` |
| `name` is required | Non-empty string at the definition level |
| `steps` is non-empty | At least one step must be present |
| Every step has `id` | Non-empty, unique within the definition |
| Every step has `name` | Non-empty string |
| Every step has a valid `type` | One of the eight supported types |
| `nextWorkflowId` required when `autoStartNextWorkflow: true` | Both fields must be set together |
| `DECISION` has `conditionalNextSteps` | At least one entry; all target IDs must exist |
| `TRANSFORMATION` has `nextStep` | Required; target must exist |
| `TRANSFORMATION` has `transformations` | At least one entry |
| `WAIT` has `nextStep` | Required; target must exist |
| `PARALLEL_GATEWAY` has `parallelNextSteps` | Minimum 2 entries; all targets must exist |
| `PARALLEL_GATEWAY` has `joinStep` | Required; target must exist |
| `JOIN_GATEWAY` has `nextStep` | Required; target must exist |
| All step references resolve | Every `nextStep`, `conditionalNextSteps` target, `parallelNextSteps`, `joinStep`, `boundaryEvents[].targetStepId` must point to an existing step ID |
| All steps are reachable | Graph walk from the first step must reach every step in the definition |
| At least one `END` step is reachable | The workflow must have a terminal state |
| Boundary event `type` is `TIMER` | No other event types are supported |
| Boundary event `duration` is non-empty | ISO-8601 string required |
| Boundary event `targetStepId` resolves | Must point to an existing step |

---

## Design guide

**Keep step IDs stable.** Instances in progress hold a reference to step IDs by name. Renaming a step ID in a new version will cause in-flight instances (which used the old version) to look up the old ID — they are unaffected because the engine pins each instance to the version it started with. However, keep IDs stable within a version to avoid confusion.

**Name your `END` steps meaningfully.** A workflow often has multiple terminal paths (approved, rejected, cancelled). Name them `end-approved`, `end-rejected`, etc. rather than a generic `end`. History records are easier to read.

**Design `escalate_task` with an exit.** A `SERVICE_TASK` boundary escalation target with no `nextStep` leaves the instance permanently `ACTIVE` after it executes. Always give it a `nextStep` to an `END` step (or re-entry point) unless you specifically intend manual intervention.

**Use namespaced IDs.** Convention: `DOMAIN::workflow-name`. This avoids collisions when multiple teams share the same engine instance (e.g. `LOS::loan-registration-workflow`, `HR::onboarding-workflow`).

**DECISION expressions are evaluated in insertion order.** Place the most specific condition first — the engine evaluates expressions in the order they appear in the JSON object and takes the first match. A catch-all branch (e.g. `#status != 'PENDING'` or a known-true literal) at the end prevents unmatched instances from stalling.

**Parallel branches must all reach the join.** Every branch started by a `PARALLEL_GATEWAY` must eventually advance to its `joinStep` via `nextStep`. If one branch has a `DECISION` that can exit to a non-join target, the join will wait forever for the missing branch.

---

## Complete annotated example

This section provides a full two-workflow chain you can upload and run end-to-end:

1. **`LOS::loan-application-full`** — validates and approves/rejects a loan application. On approval it automatically triggers the disbursement workflow.
2. **`LOS::loan-disbursement-workflow`** — receives the approved application variables, computes fees, optionally requests senior approval for large amounts, transfers funds, and notifies the customer.

---

### Workflow 1 — `LOS::loan-application-full`

Illustrates: sequential tasks, parallel risk checks, computed boolean flags via `TRANSFORMATION`, a `DECISION` gate, a `USER_TASK` with a timer boundary, and auto-chaining to the next workflow.

```json
{
  "id": "LOS::loan-application-full",
  "name": "Full Loan Application Workflow",
  "description": "Validates, scores, and approves/rejects a loan application with parallel checks.",
  "autoStartNextWorkflow": true,
  "nextWorkflowId": "LOS::loan-disbursement-workflow",
  "steps": [
    {
      "id": "validate-application",
      "name": "Validate Application Data",
      "type": "SERVICE_TASK",
      "jobType": "validate-application",
      "retryCount": 2,
      "nextStep": "parallel-risk-checks"
    },
    {
      "id": "parallel-risk-checks",
      "name": "Parallel Risk Checks",
      "type": "PARALLEL_GATEWAY",
      "parallelNextSteps": ["credit-score-check", "fraud-screening"],
      "joinStep": "merge-risk-results"
    },
    {
      "id": "credit-score-check",
      "name": "Credit Score Check",
      "type": "SERVICE_TASK",
      "jobType": "credit-score",
      "retryCount": 3,
      "nextStep": "merge-risk-results"
    },
    {
      "id": "fraud-screening",
      "name": "Fraud Screening",
      "type": "SERVICE_TASK",
      "jobType": "fraud-screen",
      "retryCount": 3,
      "nextStep": "merge-risk-results"
    },
    {
      "id": "merge-risk-results",
      "name": "Merge Risk Results",
      "type": "JOIN_GATEWAY",
      "nextStep": "compute-risk-band"
    },
    {
      "id": "compute-risk-band",
      "name": "Compute Risk Band",
      "type": "TRANSFORMATION",
      "transformations": {
        "highRisk": "${creditScore < 500 || fraudScore > 0.8}",
        "requiresManualReview": "${creditScore >= 500 && creditScore < 650}"
      },
      "nextStep": "route-application"
    },
    {
      "id": "route-application",
      "name": "Route Application",
      "type": "DECISION",
      "conditionalNextSteps": {
        "#highRisk == true": "end-rejected",
        "#requiresManualReview == true": "manual-review-task",
        "#creditScore >= 650": "auto-approve"
      }
    },
    {
      "id": "manual-review-task",
      "name": "Manual Review by Underwriter",
      "type": "USER_TASK",
      "nextStep": "process-review-decision",
      "boundaryEvents": [
        {
          "type": "TIMER",
          "duration": "PT48H",
          "interrupting": false,
          "targetStepId": "escalate-review"
        }
      ]
    },
    {
      "id": "escalate-review",
      "name": "Escalate Overdue Review",
      "type": "SERVICE_TASK",
      "jobType": "escalate-review",
      "nextStep": "end-escalated"
    },
    {
      "id": "process-review-decision",
      "name": "Process Review Decision",
      "type": "DECISION",
      "conditionalNextSteps": {
        "#reviewDecision == 'APPROVED'": "auto-approve",
        "#reviewDecision == 'REJECTED'": "end-rejected"
      }
    },
    {
      "id": "auto-approve",
      "name": "Auto-Approve Application",
      "type": "SERVICE_TASK",
      "jobType": "approve-loan",
      "retryCount": 3,
      "nextStep": "end-approved"
    },
    {
      "id": "end-approved",
      "name": "Application Approved",
      "type": "END"
    },
    {
      "id": "end-rejected",
      "name": "Application Rejected",
      "type": "END"
    },
    {
      "id": "end-escalated",
      "name": "Review Escalated",
      "type": "END"
    }
  ],
  "metadata": {
    "version": "1.0",
    "category": "LOAN_PROCESSING",
    "tags": ["loan", "credit-check", "fraud", "manual-review"],
    "complexity": "HIGH"
  }
}
```

**Variables produced** (set by workers, available to the next workflow):

| Variable | Type | Set by | Example value |
|----------|------|--------|---------------|
| `applicantId` | string | `validate-application` | `"APP-20240417-001"` |
| `loanAmount` | number | `validate-application` | `200000000` |
| `applicantEmail` | string | `validate-application` | `"nguyen.van.a@example.com"` |
| `creditScore` | number | `credit-score-check` | `720` |
| `fraudScore` | number | `fraud-screening` | `0.12` |
| `highRisk` | boolean | `compute-risk-band` (TRANSFORMATION) | `false` |
| `requiresManualReview` | boolean | `compute-risk-band` (TRANSFORMATION) | `false` |
| `loanId` | string | `approve-loan` | `"LOAN-20240417-001"` |

---

### Workflow 2 — `LOS::loan-disbursement-workflow`

Illustrates: arithmetic in `TRANSFORMATION`, a `DECISION` gate on a computed threshold, a `USER_TASK` with a timer escalation for large-amount senior approval, and a `SERVICE_TASK` chain to transfer funds and notify.

```json
{
  "id": "LOS::loan-disbursement-workflow",
  "name": "Loan Disbursement Workflow",
  "description": "Computes disbursement fees, routes large amounts through senior approval, transfers funds, and notifies the customer.",
  "steps": [
    {
      "id": "compute-disbursement",
      "name": "Compute Disbursement Details",
      "type": "TRANSFORMATION",
      "transformations": {
        "disbursementFee": "${loanAmount * 0.01}",
        "netAmount": "${loanAmount - loanAmount * 0.01}",
        "requiresSeniorApproval": "${loanAmount > 500000000}"
      },
      "nextStep": "route-disbursement"
    },
    {
      "id": "route-disbursement",
      "name": "Route by Amount",
      "type": "DECISION",
      "conditionalNextSteps": {
        "#requiresSeniorApproval == true": "senior-approval-task",
        "#requiresSeniorApproval == false": "prepare-disbursement"
      }
    },
    {
      "id": "senior-approval-task",
      "name": "Senior Officer Approval",
      "type": "USER_TASK",
      "nextStep": "check-senior-decision",
      "boundaryEvents": [
        {
          "type": "TIMER",
          "duration": "PT8H",
          "interrupting": false,
          "targetStepId": "notify-approval-overdue"
        }
      ]
    },
    {
      "id": "notify-approval-overdue",
      "name": "Notify Approval Overdue",
      "type": "SERVICE_TASK",
      "jobType": "notify-approval-overdue",
      "nextStep": "end-disbursement-timeout"
    },
    {
      "id": "check-senior-decision",
      "name": "Check Senior Decision",
      "type": "DECISION",
      "conditionalNextSteps": {
        "#seniorDecision == 'APPROVED'": "prepare-disbursement",
        "#seniorDecision == 'REJECTED'": "end-disbursement-rejected"
      }
    },
    {
      "id": "prepare-disbursement",
      "name": "Prepare Disbursement Record",
      "type": "SERVICE_TASK",
      "jobType": "prepare-disbursement",
      "retryCount": 3,
      "nextStep": "transfer-funds"
    },
    {
      "id": "transfer-funds",
      "name": "Transfer Funds to Customer",
      "type": "SERVICE_TASK",
      "jobType": "transfer-funds",
      "retryCount": 5,
      "nextStep": "notify-customer"
    },
    {
      "id": "notify-customer",
      "name": "Notify Customer of Disbursement",
      "type": "SERVICE_TASK",
      "jobType": "notify-disbursement",
      "retryCount": 2,
      "nextStep": "end-disbursed"
    },
    {
      "id": "end-disbursed",
      "name": "Disbursement Complete",
      "type": "END"
    },
    {
      "id": "end-disbursement-rejected",
      "name": "Disbursement Rejected by Senior",
      "type": "END"
    },
    {
      "id": "end-disbursement-timeout",
      "name": "Disbursement Timed Out",
      "type": "END"
    }
  ],
  "metadata": {
    "version": "1.0",
    "category": "LOAN_PROCESSING",
    "tags": ["loan", "disbursement", "fund-transfer"],
    "complexity": "MEDIUM"
  }
}
```

**Variables consumed** (passed in automatically from `LOS::loan-application-full` when the chain fires):

| Variable | Used by | Purpose |
|----------|---------|---------|
| `loanAmount` | `compute-disbursement` | Basis for fee and threshold calculations |
| `loanId` | `prepare-disbursement` worker | Links disbursement record to the approved loan |
| `applicantId` | `transfer-funds` worker | Identifies the destination account |
| `applicantEmail` | `notify-disbursement` worker | Sends confirmation email to the customer |

**Variables produced** by this workflow's workers:

| Variable | Set by | Example value |
|----------|--------|---------------|
| `disbursementId` | `prepare-disbursement` | `"DISB-20240417-001"` |
| `transferRef` | `transfer-funds` | `"TXN-20240417-88821"` |
| `seniorDecision` | `senior-approval-task` (submitted by user) | `"APPROVED"` or `"REJECTED"` |

---

### Running the full chain

#### Step 1 — Upload both definitions

Upload `LOS::loan-disbursement-workflow` **first** — it must exist before the application workflow can reference it via `nextWorkflowId`.

```bash
curl -X POST http://localhost:8080/v1/definitions \
  -H "Content-Type: application/json" \
  -d @loan-disbursement-workflow.json

curl -X POST http://localhost:8080/v1/definitions \
  -H "Content-Type: application/json" \
  -d @loan-application-full.json
```

#### Step 2 — Start an instance

```bash
curl -X POST http://localhost:8080/v1/instances \
  -H "Content-Type: application/json" \
  -d '{
    "definitionId": "LOS::loan-application-full",
    "variables": {
      "applicantId": "APP-20240417-001",
      "loanAmount": 200000000,
      "applicantEmail": "nguyen.van.a@example.com"
    },
    "businessKey": "APP-20240417-001"
  }'
```

#### Step 3 — Register workers

Each `SERVICE_TASK`'s `jobType` needs a matching worker. Minimum set for the happy path:

| `jobType` | Workflow | Must return |
|-----------|----------|-------------|
| `validate-application` | application | `applicantId`, `loanAmount`, `applicantEmail` |
| `credit-score` | application | `creditScore` (number, e.g. `720`) |
| `fraud-screen` | application | `fraudScore` (float 0–1, e.g. `0.12`) |
| `approve-loan` | application | `loanId` |
| `prepare-disbursement` | disbursement | `disbursementId` |
| `transfer-funds` | disbursement | `transferRef` |
| `notify-disbursement` | disbursement | _(no output required)_ |
| `notify-approval-overdue` | disbursement | _(no output required)_ |

#### Step 4 — Complete the user task (senior approval path only)

If `loanAmount > 500000000`, the disbursement workflow pauses at `senior-approval-task`. Complete the task by posting to the stable-id route (substitute `<instance-id>` with the id returned by `POST /v1/instances`):

```bash
curl -X POST http://localhost:8080/v1/instances/<instance-id>/user-tasks/senior-approval-task/complete \
  -H "Content-Type: application/json" \
  -d '{"variables": {"seniorDecision": "APPROVED"}}'
```

#### Expected terminal states

| Scenario | Application ends at | Disbursement ends at |
|----------|---------------------|----------------------|
| `creditScore >= 650`, no fraud, `loanAmount <= 500M` | `end-approved` | `end-disbursed` |
| `creditScore >= 650`, senior approves (`loanAmount > 500M`) | `end-approved` | `end-disbursed` |
| `creditScore >= 650`, senior rejects | `end-approved` | `end-disbursement-rejected` |
| Senior approval timer fires (8 h) | `end-approved` | `end-disbursement-timeout` |
| `creditScore < 500` or high fraud | `end-rejected` | _(not started)_ |
| Manual review → underwriter approves | `end-approved` | `end-disbursed` |
| Manual review → underwriter rejects | `end-rejected` | _(not started)_ |
| Manual review timer fires (48 h) | `end-escalated` | _(not started)_ |
