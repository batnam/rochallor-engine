# Getting Started

## Prerequisites

| Tool | Version | Purpose |
|------|---------|---------|
| Go | 1.22+ | Build the engine and Go SDK |
| Docker + Docker Compose | 24+ | Run PostgreSQL |
| protoc | 3.21+ | Regenerate gRPC/proto code |
| protoc-gen-go | latest | Go proto plugin |
| protoc-gen-go-grpc | latest | Go gRPC plugin |
| Python | 3.10+ | Python SDK |
| Node.js | 20+ | Node/TypeScript SDK |
| Java | 21+ | Java SDK |
| Gradle | 8+ (wrapper included) | Java SDK build |

### Install proto plugins

```bash
# macOS
brew install protobuf

# Ubuntu / Debian
sudo apt install -y protobuf-compiler

# Go plugins
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
```

---

## Quick Start

Steps 1–3 are the same regardless of which SDK you use.

### 1. Clone and start PostgreSQL

```bash
git clone https://github.com/batnam/rochallor-engine.git
cd rochallor-engine

# Start PostgreSQL (workflow/workflow on port 5432)
docker compose -f workflow-engine/docker-compose.yml up -d
```

### 2. Generate proto code

```bash
cd workflow-engine
make proto-gen
```

### 3. Start the engine

```bash
export WE_POSTGRES_DSN="postgres://workflow:workflow@localhost:5432/workflow?sslmode=disable"
make run
# Engine listens on :8080 (REST), :9090 (gRPC), :9091 (metrics)
```

---

### 4. Install the SDK

#### Python

```bash
cd workflow-sdk-python
pip install -e ".[dev]"
```

> Python SDK supports **REST only**.

#### Go

The Go SDK is a module inside this repo — no separate install step needed.

```bash
# From any Go module that imports it:
go get github.com/batnam/rochallor-engine/workflow-sdk-go
```

#### Node / TypeScript

```bash
cd workflow-sdk-node
npm install
npm run build
```

#### Java

```bash
cd workflow-sdk-java
./gradlew build
```

The built JAR is at `build/libs/workflow-sdk-java-1.0.0.jar`. For Maven/Gradle projects, publish to your local repo:

```bash
./gradlew publishToMavenLocal
```

Then add to your `build.gradle`:

```groovy
dependencies {
    implementation 'com.batnam.rochallor-engine:workflow-sdk-java:1.0.0'
}
```

---

### 5. Upload a workflow definition

#### Python

```python
# upload_greet.py
from workflow_sdk.client.rest import RestEngineClient

client = RestEngineClient("http://localhost:8080")
definition = {
    "id": "greet-workflow",
    "name": "Greet Workflow",
    "steps": [
        {
            "id": "say-hello",
            "name": "Say Hello",
            "type": "SERVICE_TASK",
            "jobType": "greet",
            "nextStep": "end"
        },
        {"id": "end", "name": "End", "type": "END"}
    ]
}
result = client.upload_definition(definition)
print(f"Uploaded: {result['id']} v{result['version']}")
```

#### Go

```go
// upload_greet.go
package main

import (
    "bytes"
    "encoding/json"
    "fmt"
    "net/http"
)

func main() {
    definition := map[string]any{
        "id":   "greet-workflow",
        "name": "Greet Workflow",
        "steps": []any{
            map[string]any{
                "id": "say-hello", "name": "Say Hello",
                "type": "SERVICE_TASK", "jobType": "greet", "nextStep": "end",
            },
            map[string]any{"id": "end", "name": "End", "type": "END"},
        },
    }
    body, _ := json.Marshal(definition)
    resp, err := http.Post("http://localhost:8080/v1/definitions", "application/json", bytes.NewReader(body))
    if err != nil {
        panic(err)
    }
    defer resp.Body.Close()
    var result map[string]any
    json.NewDecoder(resp.Body).Decode(&result)
    fmt.Printf("Uploaded: %s v%.0f\n", result["id"], result["version"])
}
```

#### Node / TypeScript

```typescript
// upload_greet.ts
const resp = await fetch('http://localhost:8080/v1/definitions', {
    method:  'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
        id:    'greet-workflow',
        name:  'Greet Workflow',
        steps: [
            { id: 'say-hello', name: 'Say Hello', type: 'SERVICE_TASK',
              jobType: 'greet', nextStep: 'end' },
            { id: 'end', name: 'End', type: 'END' },
        ],
    }),
})
const result = await resp.json()
console.log(`Uploaded: ${result.id} v${result.version}`)
```

#### Java

```java
// UploadGreet.java
import com.batnam.workflow.sdk.client.RestEngineClient;
import java.util.List;
import java.util.Map;

public class UploadGreet {
    public static void main(String[] args) throws Exception {
        var client = new RestEngineClient("http://localhost:8080");

        var definition = Map.of(
            "id",   "greet-workflow",
            "name", "Greet Workflow",
            "steps", List.of(
                Map.of("id", "say-hello", "name", "Say Hello",
                       "type", "SERVICE_TASK", "jobType", "greet", "nextStep", "end"),
                Map.of("id", "end", "name", "End", "type", "END")
            )
        );
        var result = client.uploadDefinition(definition);
        System.out.println("Uploaded: " + result.get("id") + " v" + result.get("version"));
    }
}
```

---

### 6. Run a worker

#### Python

```python
# worker.py
import signal, threading
from workflow_sdk.client.rest import RestEngineClient
from workflow_sdk.handler.registry import HandlerRegistry
from workflow_sdk.runner.runner import Runner

client   = RestEngineClient("http://localhost:8080")
registry = HandlerRegistry()

@registry.register("greet")
def handle_greet(ctx):
    name = ctx["variables"].get("name", "World")
    print(f"Hello, {name}!")
    return {"greeting": f"Hello, {name}!"}

stop = threading.Event()
signal.signal(signal.SIGINT, lambda *_: stop.set())

runner = Runner(client=client, registry=registry, worker_id="py-worker-1")
runner.run(stop_event=stop)
```

#### Go

```go
// worker.go
package main

import (
    "context"
    "fmt"
    "os/signal"
    "syscall"

    "github.com/batnam/rochallor-engine/workflow-sdk-go/client"
    "github.com/batnam/rochallor-engine/workflow-sdk-go/handler"
    "github.com/batnam/rochallor-engine/workflow-sdk-go/runner"
)

func main() {
    engine   := client.NewRest("http://localhost:8080", "go-worker-1")
    registry := handler.New()

    registry.Register("greet", func(ctx context.Context, job handler.JobContext) (handler.Result, error) {
        name, _ := job.Variables["name"].(string)
        if name == "" {
            name = "World"
        }
        fmt.Printf("Hello, %s!\n", name)
        return handler.Result{VariablesToSet: map[string]any{"greeting": "Hello, " + name + "!"}}, nil
    })

    r := runner.New(runner.Config{WorkerID: "go-worker-1"}, engine, registry)

    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer stop()

    r.Run(ctx) // blocks until SIGINT / SIGTERM
}
```

> Swap `client.NewRest` for `client.NewGrpc("localhost:9090", "go-worker-1")` to use gRPC transport.

#### Node / TypeScript

```typescript
// worker.ts
import { RestEngineClient } from './src/client/rest.js'
import { HandlerRegistry }   from './src/handler/registry.js'
import { Runner }            from './src/runner/runner.js'

const engine   = new RestEngineClient('http://localhost:8080')
const registry = new HandlerRegistry()

registry.register('greet', async ctx => {
    const name = (ctx.variables['name'] as string) ?? 'World'
    console.log(`Hello, ${name}!`)
    return { variablesToSet: { greeting: `Hello, ${name}!` } }
})

const controller = new AbortController()
process.on('SIGINT',  () => controller.abort())
process.on('SIGTERM', () => controller.abort())

const runner = new Runner(engine, registry, { workerId: 'node-worker-1' })
await runner.run(controller.signal)
```

> Swap `RestEngineClient` for `GrpcEngineClient('localhost:9090', grpc.credentials.createInsecure())` to use gRPC transport.

#### Java

```java
// Worker.java
import com.batnam.workflow.sdk.client.RestEngineClient;
import com.batnam.workflow.sdk.handler.HandlerRegistry;
import com.batnam.workflow.sdk.runner.Runner;
import java.util.Map;

public class Worker {
    public static void main(String[] args) throws InterruptedException {
        var client   = new RestEngineClient("http://localhost:8080");
        var registry = new HandlerRegistry();

        registry.register("greet", ctx -> {
            String name = (String) ctx.variables().getOrDefault("name", "World");
            System.out.println("Hello, " + name + "!");
            return Map.of("greeting", "Hello, " + name + "!");
        });

        // parallelism=64, pollIntervalMs=500
        var runner = new Runner("java-worker-1", 64, 500, client, registry);
        runner.start();

        // Block main thread — JVM shutdown hook handles graceful drain
        Thread.currentThread().join();
    }
}
```

> Swap `RestEngineClient` for `GrpcEngineClient("localhost:9090")` to use gRPC transport.

---

### 7. Start a workflow instance

#### Python

```python
# start_instance.py
from workflow_sdk.client.rest import RestEngineClient

client   = RestEngineClient("http://localhost:8080")
instance = client.start_instance("greet-workflow", variables={"name": "Alice"})
print(f"Started instance: {instance['id']}")
```

#### Go

```go
// start_instance.go
package main

import (
    "bytes"
    "encoding/json"
    "fmt"
    "net/http"
)

func main() {
    body, _ := json.Marshal(map[string]any{
        "variables": map[string]any{"name": "Alice"},
    })
    resp, err := http.Post(
        "http://localhost:8080/v1/definitions/greet-workflow/instances",
        "application/json",
        bytes.NewReader(body),
    )
    if err != nil {
        panic(err)
    }
    defer resp.Body.Close()
    var instance map[string]any
    json.NewDecoder(resp.Body).Decode(&instance)
    fmt.Println("Started instance:", instance["id"])
}
```

#### Node / TypeScript

```typescript
// start_instance.ts
const resp = await fetch('http://localhost:8080/v1/definitions/greet-workflow/instances', {
    method:  'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ variables: { name: 'Alice' } }),
})
const instance = await resp.json()
console.log('Started instance:', instance.id)
```

#### Java

```java
// StartInstance.java
import com.batnam.workflow.sdk.client.RestEngineClient;
import java.util.Map;

public class StartInstance {
    public static void main(String[] args) throws Exception {
        var client   = new RestEngineClient("http://localhost:8080");
        var instance = client.startInstance("greet-workflow", Map.of("name", "Alice"), null, null);
        System.out.println("Started instance: " + instance.get("id"));
    }
}
```

---

## Next steps

| Topic | Doc |
|-------|-----|
| Full SDK reference — Python | [docs/sdk/python.md](sdk/python.md) |
| Full SDK reference — Go | [docs/sdk/go.md](sdk/go.md) |
| Full SDK reference — Node/TypeScript | [docs/sdk/node.md](sdk/node.md) |
| Full SDK reference — Java | [docs/sdk/java.md](sdk/java.md) |
| Workflow definition format | [docs/workflow-format.md](workflow-format.md) |
| Architecture overview | [docs/architecture.md](architecture.md) |
| Local development guide | [docs/development.md](development.md) |
