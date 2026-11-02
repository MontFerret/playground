# Ferret Playground API

A Go REST service for analyzing, formatting, compiling, and executing FQL with Ferret v2.
Compilation can return disassembled bytecode and referenced parameters. Use the
environment endpoint to discover the runtime version, capabilities, and limits.

## Packaged deployment

On native Linux/amd64 with Docker Compose v2.24.4 or later, run:

```sh
make up
```

This starts the API, sandboxed Chromium, private relay, and restricted egress proxy.
Only the API port is published. [Deployment and examples](docs/deployment.md)
covers host prerequisites, resource limits, browser ownership, and the product demo.
[Release setup and recovery](docs/releasing.md) covers the two registries.

```sh
make integration       # private fixtures, actual development API image
make release-snapshot  # GoReleaser OCI archive, actual release entry point
make down
```

## Run locally

Requires Go 1.27 or newer and Make. Dependencies are pinned to Ferret
`v2.0.0-alpha.61` and Universal API `v1.0.0-alpha.21`. The service minimum is owned by go.mod.
There is no browser, credential, Node.js, or external service requirement.

```sh
go mod download
make build
./bin/playground
```

`make run` also starts the service with `go run ./cmd/playground`. The default
address is `127.0.0.1:8080`. In another terminal:

```sh
curl --fail --silent --show-error http://127.0.0.1:8080/api/v1/environment
```

Example response when service build-version metadata is unavailable:

```json
{
  "apiVersion": "v1",
  "service": {"name": "ferret-playground", "version": null},
  "runtime": {
    "name": "ferret",
    "version": "v2.0.0-alpha.61",
    "standardLibrary": "safe",
    "modules": []
  },
  "capabilities": {
    "environment": true,
    "analyze": true,
    "format": true,
    "compile": true,
    "execute": true,
    "debug": false,
    "functionCatalog": false,
    "optimizationLevels": ["none", "basic", "full"],
    "defaultOptimization": "full",
    "inspectionArtifacts": ["bytecode", "parameters"]
  },
  "limits": {
    "http": {
      "maxHeaderBytes": 16384,
      "maxRequestBodyBytes": 1048576,
      "maxResponseBytes": 2097152,
      "readHeaderTimeoutMs": 5000,
      "readTimeoutMs": 10000,
      "writeTimeoutMs": 15000,
      "idleTimeoutMs": 60000
    },
    "execution": {"maxConcurrentExecutions": 2, "maxTimeoutMs": 5000, "maxOutputBytes": 1048576},
    "tooling": {"maxSourceBytes": 65536, "maxBytecodeBytes": 1048576, "maxFormattedSourceBytes": 1048576}
  }
}
```

The service constructs a real native engine with `stdlib.Safe()`, which excludes
filesystem and network standard-library groups. Browser-free startup registers
no optional modules. The packaged deployment registers the restricted HTML
surface described in [deployment guidance](docs/deployment.md). Environment
requests read detached metadata; they do not evaluate
FQL, launch browsers, or make external requests. The standard-library profile
controls function availability. Execution shares the service process.

The runtime version comes from dependency build metadata, honoring versioned
replacements. Unversioned local replacements and missing development metadata
produce `null`, never a fictitious version or a leaked local path. Service build
metadata is separate and can include a VCS-derived development version with a
`+dirty` suffix; it is `null` only when unavailable. `apiVersion` identifies the
REST contract only.

## Configuration

Documented environment variables override defaults. Explicit empty values are
invalid. There are no configuration files, automatic `.env` loading, or CLI
configuration flags. Validate all settings before acquiring runtime resources.

| Variable | Default | Meaning |
|---|---|---|
| `PLAYGROUND_MAX_CONCURRENT_EXECUTIONS` | `2` | Non-queued HTTP execution slots, held through cleanup |
| `PLAYGROUND_ALLOWED_ORIGINS` | `https://ferretlang.org` | Comma-separated exact CORS origins |
| `PLAYGROUND_CDP_ORIGIN` | unset | Operator-owned private HTTP CDP origin, enables hosted HTML |
| `PLAYGROUND_LISTEN_ADDR` | `127.0.0.1:8080` | TCP host:port; port 0 selects an available port |
| `PLAYGROUND_READ_HEADER_TIMEOUT` | `5s` | Time allowed to read request headers |
| `PLAYGROUND_READ_TIMEOUT` | `10s` | Time allowed to read a complete request |
| `PLAYGROUND_WRITE_TIMEOUT` | `15s` | HTTP response write deadline |
| `PLAYGROUND_IDLE_TIMEOUT` | `60s` | Idle keep-alive timeout |
| `PLAYGROUND_MAX_HEADER_BYTES` | `16384` | `net/http.Server.MaxHeaderBytes` setting |
| `PLAYGROUND_SHUTDOWN_TIMEOUT` | `5s` | Maximum graceful HTTP drain wait |
| `PLAYGROUND_CLEANUP_TIMEOUT` | `5s` | Separate maximum admitted-work and runtime cleanup wait |
| `PLAYGROUND_MAX_REQUEST_BODY_BYTES` | `1048576` | Maximum POST body bytes, enforced while reading |
| `PLAYGROUND_MAX_SOURCE_BYTES` | `65536` | Maximum UTF-8 source bytes before native tooling |
| `PLAYGROUND_MAX_BYTECODE_BYTES` | `1048576` | Maximum returned disassembly text bytes, checked after generation |
| `PLAYGROUND_MAX_FORMATTED_SOURCE_BYTES` | `1048576` | Maximum UTF-8 bytes of formatted source, enforced on each write |
| `PLAYGROUND_MAX_EXECUTION_TIMEOUT` | `5s` | Maximum time from native operation entry through result consumption |
| `PLAYGROUND_MAX_OUTPUT_BYTES` | `1048576` | Maximum encoded execution result bytes retained for delivery |
| `PLAYGROUND_MAX_RESPONSE_BYTES` | `2097152` | Maximum encoded JSON response bytes including newline, checked after encoding |

Durations use Go syntax and must be positive. Header size must be a positive
integer within the server's integer range. Addresses require a valid host:port
and numeric port from 0 to 65535. An empty host, as in `:8080`, binds all
interfaces and must be an explicit operator choice. Environment discovery
reports effective HTTP, tooling, and execution limits, in fractional milliseconds where needed.
Byte limits must be positive representable integers below the platform maximum;
the response limit must be at least 256 bytes so fixed error envelopes fit.
`MaxHeaderBytes` has net/http's parsing tolerance, rather than defining an exact
wire-byte cap. It is not a request-body limit.

For example:

```sh
PLAYGROUND_LISTEN_ADDR=127.0.0.1:9090 PLAYGROUND_SHUTDOWN_TIMEOUT=3s ./bin/playground
```

Send SIGINT (Ctrl-C) or SIGTERM to the built executable to stop it. HTTP drains
first; expired drains force-close remaining connections. Host closure then stops
admission, waits for active operations and their output, session, and plan cleanup, and closes
the native engine. The cleanup wait uses a fresh context after serving
cancellation. The default combined drain and cleanup wait is at most approximately ten seconds, plus scheduling
overhead. Cleanup starts once and retains its error. Native compilation phases
and contextless `Close()` cannot be forcibly interrupted; a cleanup timeout is reported as
failure and the process may exit while admitted work or native cleanup is still
running. The engine is never closed ahead of admitted work.
Startup, serving, drain, and cleanup failures produce a nonzero exit status.

## HTTP surface

| Method | Path | Status |
|---|---|---|
| GET | `/api/v1/environment` | Implemented |
| POST | `/api/v1/analyze` | Implemented: native compilation diagnostics |
| POST | `/api/v1/format` | Implemented with Ferret's default formatting options |
| POST | `/api/v1/compile` | Implemented: compilation, parameters, and text bytecode inspection |
| POST | `/api/v1/execute` | Implemented with bounded execution and buffered JSON output |

Unavailable routes return JSON `404`. The environment path supports GET.
Analysis, formatting, compilation, and execution support POST. Other methods receive `405` with the
corresponding `Allow` header. HEAD error responses
contain no body. Application errors share a stable envelope:

```json
{"error":{"code":"not_found","message":"The requested route does not exist."}}
```

See the [OpenAPI contract](api/openapi.yaml) and
[HTTP behavior guide](docs/http-contract.md) for strict request decoding,
program outcomes, diagnostic coordinates, output presence, and HTTP errors.
Execution admission has a configurable concurrency cap. The packaged browser,
network policy, and CORS settings are described in [deployment guidance](docs/deployment.md).

### Analyze and inspect

```sh
curl --silent --show-error http://127.0.0.1:8080/api/v1/analyze \
  -H 'Content-Type: application/json' \
  --data '{"source":{"name":"example.fql","text":"RETURN @value + 1"}}'

curl --silent --show-error http://127.0.0.1:8080/api/v1/compile \
  -H 'Content-Type: application/json' \
  --data '{"source":{"text":"RETURN @value + 1"},"options":{"optimization":"none"},"include":["bytecode","parameters"]}'
```

Analysis uses native compilation with `full` optimization and returns
`{"status":"ok","diagnostics":[]}` or HTTP 200 with `status: "invalid"` and
diagnostics. Parsing, lexical binding, and native compiler checks run. Host
functions and parameter values are resolved at runtime in this dependency:
`RETURN MISSING_FUNCTION()` and `RETURN @value` can analyze successfully.
Success does not establish runtime success. Tooling never creates a session or
executes the submitted program.

Compilation accepts `none`, `basic`, or `full` (default). `include` accepts
`bytecode` and `parameters`, each at most once; omitted or empty inclusion
returns `artifacts: {}`. Bytecode is `{ "format": "text", "text": "..." }`
from Ferret's disassembler. Parameter metadata is a distinct first-seen-order
name array, including `[]` when requested for a parameter-free program. It
contains no supplied values or inferred types, defaults, or requiredness.
Both artifacts come from one compilation. Invalid compilation omits artifacts.
`timings.compileMs` measures native compilation, excluding artifacts and cleanup.

Source names are display labels and default to `anonymous`. Missing/null text is
an HTTP 400 error; empty text reaches Ferret. Diagnostics use zero-based lines,
UTF-16 character offsets, and exclusive ends. A location without a reliable
native span retains its source label and omits its range.

Oversized requests or source receive `413/request_too_large`; oversized artifacts
or encoded responses receive `422/response_too_large`, never truncated success
or invalid-source diagnostics. Ferret builds the full disassembly string before
its size can be checked. JSON encoding likewise precedes its response-size
check. These limits do not guarantee an allocation ceiling. Cancellation is
checked before work and between synchronous stages, without detached compilation
goroutines. Every temporary plan is closed before its result is returned.

### Format source

```sh
curl --fail --silent --show-error http://127.0.0.1:8080/api/v1/format \
  -H 'Content-Type: application/json' \
  --data '{"source":{"name":"example.fql","text":"RETURN    1"}}'
```

```json
{"status":"ok","diagnostics":[],"formattedSource":"return 1"}
```

Formatting accepts only `source` and uses native defaults. It requires no parameter
values or host-function registration. Invalid FQL returns HTTP 200 with
`status: "invalid"` and diagnostics, omitting `formattedSource`.
Output that exceeds the advertised formatted-source or JSON response limit
returns `422/response_too_large`. The writer checks cancellation and the text limit
on every write. Native parsing and work between writes run synchronously.

### Execute with bind parameters

```sh
curl --silent --show-error http://127.0.0.1:8080/api/v1/execute \
  -H 'Content-Type: application/json' \
  --data '{"source":{"text":"RETURN @order.id"},"params":{"order":{"id":"A1"}},"options":{"timeoutMs":500}}'
```

```json
{"status":"ok","diagnostics":[],"output":"A1","timings":{"compileMs":0.3,"executeMs":0.2,"totalMs":0.6}}
```

The service compiles once and runs a fresh session with request-local JSON
parameters. A successful JSON null appears as `"output":null`. A compile
rejection returns `invalid` with the same diagnostics as `/compile`. Inspect
`diagnostics[0].code` on a `failed` response for `runtime_error`,
`execution_timeout`, or `output_too_large`. All three outcomes use HTTP 200.
Failed and invalid responses omit `output`.

The earliest request, server, or client deadline applies through output
consumption. Native compiler phases, host functions that ignore cancellation,
and synchronous cleanup can delay completion. Ferret alpha.61 materializes
encoded output during the run. The result limit bounds bytes retained for
delivery. VM memory and native encoder allocations can exceed that limit.

## Development and validation

```sh
make fmt
make check
```

Individual targets are `build`, `run`, `test`, `fmt`, `fmt-check`, `vet`,
`api-check`, and `test-race`. `make check` runs all non-mutating validation targets
and builds the executable in ignored `bin/`. Contract checks use
`kin-openapi v0.133.0` only in tests, validating schemas, examples, and actual
handler responses without fetching external references. CI runs these same
checks on the Go version declared in go.mod.

Tests use loopback listeners and controlled modules; after downloading Go
dependencies, they need no public network access, Chromium, or credentials.
Race detection requires a supported platform and C toolchain.

Package ownership and upstream gaps are in the
[maintainer architecture guide](docs/maintainers/architecture.md).
Contributors and agents should start with the
[maintainer index](docs/maintainers/README.md) and [agent workflow](AGENTS.md).
