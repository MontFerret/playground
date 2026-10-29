# Ferret Playground API

A Go REST service embedding Ferret v2. This foundation implements environment
discovery. Analysis, formatting, compilation, and execution are specified for a
later task and are not registered as HTTP handlers.

## Run locally

Requires Go 1.25 or newer and Make. Dependencies are pinned to Ferret
`v2.0.0-alpha.58` and Universal API `v1.0.0-alpha.21`; both declare Go 1.25.
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
    "version": "v2.0.0-alpha.58",
    "standardLibrary": "safe",
    "modules": []
  },
  "capabilities": {
    "environment": true,
    "analyze": false,
    "format": false,
    "compile": false,
    "execute": false,
    "debug": false,
    "functionCatalog": false,
    "optimizationLevels": [],
    "inspectionArtifacts": []
  },
  "limits": {
    "http": {
      "maxHeaderBytes": 16384,
      "readHeaderTimeoutMs": 5000,
      "readTimeoutMs": 10000,
      "writeTimeoutMs": 15000,
      "idleTimeoutMs": 60000
    }
  }
}
```

The service constructs a real native engine with `stdlib.Safe()`, which excludes
filesystem and network standard-library groups. No optional modules are
registered. Environment requests read detached metadata; they do not evaluate
FQL, launch browsers, or make external requests. The profile is not a sandbox
claim for future execution endpoints.

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
| `PLAYGROUND_LISTEN_ADDR` | `127.0.0.1:8080` | TCP host:port; port 0 selects an available port |
| `PLAYGROUND_READ_HEADER_TIMEOUT` | `5s` | Time allowed to read request headers |
| `PLAYGROUND_READ_TIMEOUT` | `10s` | Time allowed to read a complete request |
| `PLAYGROUND_WRITE_TIMEOUT` | `15s` | HTTP response write deadline |
| `PLAYGROUND_IDLE_TIMEOUT` | `60s` | Idle keep-alive timeout |
| `PLAYGROUND_MAX_HEADER_BYTES` | `16384` | `net/http.Server.MaxHeaderBytes` setting |
| `PLAYGROUND_SHUTDOWN_TIMEOUT` | `5s` | Maximum graceful HTTP drain wait |
| `PLAYGROUND_CLEANUP_TIMEOUT` | `5s` | Separate maximum runtime cleanup wait |

Durations use Go syntax and must be positive. Header size must be a positive
integer within the server's integer range. Addresses require a valid host:port
and numeric port from 0 to 65535. An empty host, as in `:8080`, binds all
interfaces and must be an explicit operator choice. Environment discovery
reports installed HTTP settings only, in fractional milliseconds where needed.
`MaxHeaderBytes` has net/http's parsing tolerance, rather than defining an exact
wire-byte cap. It is not a request-body limit.

For example:

```sh
PLAYGROUND_LISTEN_ADDR=127.0.0.1:9090 PLAYGROUND_SHUTDOWN_TIMEOUT=3s ./bin/playground
```

Send SIGINT (Ctrl-C) or SIGTERM to the built executable to stop it. HTTP drains
first; expired drains force-close remaining connections. Native cleanup then
uses a fresh context, even after serving cancellation. The default combined
drain and cleanup wait is at most approximately ten seconds, plus scheduling
overhead. Cleanup starts once and retains its error. Ferret's contextless native
`Close()` cannot be forcibly interrupted; a cleanup timeout is reported as
failure and the process may exit while that native call is still running.
Startup, serving, drain, and cleanup failures produce a nonzero exit status.

## HTTP surface

| Method | Path | Status |
|---|---|---|
| GET | `/api/v1/environment` | Implemented |
| POST | `/api/v1/analyze` | Planned: source diagnostics |
| POST | `/api/v1/format` | Planned: canonical source formatting |
| POST | `/api/v1/compile` | Planned: compilation, parameters, and bytecode inspection |
| POST | `/api/v1/execute` | Planned: bounded execution and buffered JSON output |

Planned routes currently return JSON `404`. The environment path supports GET
only; unsupported methods receive `405` and `Allow: GET`. HEAD error responses
contain no body. Application errors share a stable envelope:

```json
{"error":{"code":"not_found","message":"The requested route does not exist."}}
```

See the [OpenAPI contract](api/openapi.yaml) and
[HTTP behavior guide](docs/http-contract.md) for planned request decoding,
program outcomes, diagnostic coordinates, output presence, and HTTP errors.
There is no HTTP streaming, debugger session, function catalog, rate limiter,
frontend, WASM adapter, or deployment profile in this foundation.

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
checks on Go 1.25.x and 1.27.x.

Tests use loopback listeners and controlled modules; after downloading Go
dependencies, they need no public network access, Chromium, or credentials.
Race detection requires a supported platform and C toolchain.

Package ownership and upstream gaps are in the
[maintainer architecture guide](docs/maintainers/architecture.md).
Contributors and agents should start with the
[maintainer index](docs/maintainers/README.md) and [agent workflow](AGENTS.md).
