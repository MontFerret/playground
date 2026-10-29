# Architecture, ownership, and lifecycle

## Package responsibilities

| Owner | Responsibility |
|---|---|
| `cmd/playground` | Signals, configuration loading, composition, listener startup, cleanup sequencing, exit status |
| `internal/config` | Documented environment/default precedence and validation before resource acquisition |
| `internal/httpapi` | Exact routing, JSON envelopes, sanitized request logging, HTTP limits and connection lifecycle |
| `internal/playground` | Transport-independent environment models |
| `internal/runtimehost` | Native engine construction, standard-library selection, optional registration metadata, runtime version, resource closure |
| `api` | Checked-in OpenAPI contract and tests against implemented behavior |

HTTP may depend on environment operations and models; runtime composition must
not depend on HTTP types, status codes, or environment-variable parsing. Avoid
frameworks or a generic backend interface. The small environment-provider seam
exists for today's handler and controlled failure testing. The executable's host
seam tests today's cleanup boundary. Neither anticipates future operation APIs.

Ferret owns language parsing, diagnostics, compilation, evaluation, and value
serialization. Future analysis, formatting, and bytecode inspection use native
APIs because UAPI does not expose them. Reuse portable types where their
semantics fit, including `api.Version`; do not force unsupported behavior
through UAPI or duplicate Ferret implementation details.

## Initialization and metadata

Startup validates configuration, constructs a native engine, opens the listener,
and begins serving. The engine uses `ferret.New` with `stdlib.Safe()` and no
optional modules. The selected profile label and construction option live
together. Optional-module metadata is constructed from the same registration
entries passed to the engine; module versions, when available, are supplied
beside that registration. Native initialization rollback owns already-acquired
resources and eligible close hooks.

The host derives the Ferret version from `runtime/debug.ReadBuildInfo` dependency
metadata. A versioned replacement supplies the actual replacement version;
unversioned local replacements yield unknown without leaking paths. The
compiler's `2.0.0` constant is not the embedded dependency release version.
Service versions come from main-module build metadata, including VCS-derived
development versions when available; absent or `(devel)` versions become null.
The runtime host returns defensive copies of its initialized metadata, even
after closure.

The host never executes a query during construction or discovery. Ferret's
default filesystem is disabled when no root is configured; its default network
service construction does not issue a request. Safe standard-library selection
excludes IO functions but is not a security boundary for a future arbitrary
execution service. No request can configure these host services.

Capabilities are owned by working HTTP behavior. In this foundation only
environment discovery is true; compilation options and inspection arrays stay
empty. HTTP settings are reported from the same validated configuration used by
the server. Future execution, source-size, output, and concurrency limits must
not appear until implemented.

## Ownership and shutdown

The executable transfers the host to serving startup, which closes it even if
listener binding fails. The server owns its listener and connections; it borrows
the handler. The handler borrows the host. Environment methods only copy immutable
metadata and acquire no native descendants.

Serving cancellation stops admission and drains HTTP using a fresh bounded
context. If the drain expires, close connections and report the error. After
HTTP serving ends, close the host under a separate fresh bounded context,
joining serving and cleanup failures. Defaults permit five seconds for each
phase. Do not derive cleanup contexts from canceled serving contexts.

The host starts native cleanup in a single goroutine, publishes the retained
result through a closed channel, and permits concurrent or repeated waiters.
Expiry bounds the caller's wait, not the native operation. No context-aware
native close exists, so a timed-out process cannot promise completed release.
Later in-process callers can still wait for the same cleanup result.

Future native work must settle outputs, sessions, and plans before engine
closure; force-closing an HTTP connection alone does not prove native work has
settled. Keep that coordination with the layer that creates those descendants.

## Upstream integration gaps

Verified against Ferret `v2.0.0-alpha.58` and API `v1.0.0-alpha.21`:

- Native `Engine` has no release-version query. UAPI `New`/`Wrap` require the host
  to supply the opaque Core version. Dependency build metadata is the source
  used here, without creating an otherwise-unused adapter.
- UAPI has execution/compilation interfaces, but no analysis, source formatter,
  or bytecode inspection interface. Native `Compiler.Analyze`, `Formatter.Format`,
  compiled parameter metadata, and Program JSON are the relevant future APIs.
- Native analysis and formatting take no context. Compiler cancellation is
  checked between synchronous phases rather than preempting each phase.
- Native engine cleanup takes no context and does not settle caller-owned
  descendants. This host bounds its wait and documents the limitation.
- Native module contracts provide a name and registration, not a version.
  The standard-library set likewise exposes selection/registration rather than
  a metadata enumeration, so this foundation reports its actual profile label.
- UAPI defines aggressive optimization, but its Ferret adapter rejects it.
- Native diagnostic positions are not the HTTP UTF-16 coordinate contract;
  portable diagnostics do not directly supply every HTTP diagnostic field.
- The selected native formatter's panic recovery records an internal diagnostic
  without a named returned error. Before enabling formatting, investigate error
  observability and ensure recovered internal failures cannot become false
  success or expose stack-bearing diagnostic text. No upstream changes are made
  in this foundation.

The [HTTP guide](../http-contract.md) records the boundary behavior that future
operation implementations must uphold. Public request data never controls host
configuration, and transport-independent code should remain usable by a future
WASM adapter.
