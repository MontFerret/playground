# Architecture, ownership, and lifecycle

## Package responsibilities

| Owner | Responsibility |
|---|---|
| `cmd/playground` | Signals, configuration loading, composition, listener startup, cleanup sequencing, exit status |
| `internal/config` | Documented environment/default precedence and validation before resource acquisition |
| `internal/httpapi` | Exact routing, JSON envelopes, sanitized request logging, HTTP limits and connection lifecycle |
| `internal/playground` | Transport-independent environment, tooling and execution inputs/results, and byte-span diagnostic models |
| `internal/runtimehost` | Native engine construction, standard-library selection, optional registration metadata, runtime version, native tooling and execution, and resource closure |
| `api` | Checked-in OpenAPI contract and tests against implemented behavior |

HTTP may depend on environment operations and models; runtime composition must
not depend on HTTP types, status codes, or environment-variable parsing. Avoid
frameworks or a generic backend interface. The small provider seam
contains the environment, analysis, formatting, compilation, and execution operations used
by the handler and controlled failure tests. The executable's host
seam tests startup and cleanup.

Ferret owns language parsing, diagnostics, compilation, evaluation, and value
serialization. Analysis and bytecode inspection use native
APIs because UAPI does not expose them. Formatting calls Ferret's native
formatter directly. Reuse portable types where their
semantics fit, including `api.Version`; do not force unsupported behavior
through UAPI or duplicate Ferret implementation details.

## Initialization and metadata

Startup validates configuration, constructs an immutable native formatter with
default options before acquiring the native engine, opens the listener,
and begins serving. The engine uses `ferret.New` with `stdlib.Safe()`.
Browser-free startup registers no optional modules.
Hosted startup composes a restricted HTML module from the same registration
entries used for environment metadata. The selected profile label and construction option live
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
excludes IO functions. Programs share the service process and its memory budget.
No request can configure these host services. Native HTTP is explicitly denied
through the engine network service. Runtime logging is discarded.

Capabilities are owned by working HTTP behavior. Environment discovery, analysis,
formatting, compilation, and execution are enabled.
Supported optimization/artifact constants and effective defaults are shared with
request validation and native mapping. HTTP, tooling, and execution limits are reported from
the same validated settings used for enforcement.

## Tooling operations

Analysis and compilation share one Engine.Compile path against the initialized
host. Analysis uses full optimization and renders no artifacts. Compilation uses
explicit native per-plan options. The compiler does not bind host functions or
parameter values in alpha.62.
Analysis and compilation both finish without creating a session.

The temporary plan owns the compiled program. Parameter metadata uses Plan.Params
in native first-seen order. Requested bytecode uses Plan.Marshal followed by
artifact.Unmarshal and asm.Disassemble because no public plan program accessor
exists. This supported materialization avoids private access or a second compile;
it runs only when requested. Returned data is detached and plans close on every
exit path. Native compilation alone is timed, before artifact generation/cleanup.

Runtime diagnostic extraction walks structured errors in order and separates
source errors from internal or mixed failures. It retains messages, hints, source
labels, and native byte spans without causes or stack-bearing diagnostics. HTTP
owns UTF-16 conversion and optional ranges. The pinned API exposes errors without
stable diagnostic codes or warning severity; no such data is invented.

HTTP bounds each request body and checks the source size before calling the host.
Disassembly and JSON encoding have response-size checks after materialization,
not allocation caps. Oversized output is a service limit error, never invalid FQL
or silently truncated success. Request contexts reach native compilation and are
checked between stages, without detached worker goroutines or hard preemption.

Formatting uses the shared immutable formatter, with request-local source,
diagnostics, and output. It creates no plan or session and performs no semantic
name resolution. A writer bounds retained bytes and observes context cancellation
on each write, latching its first failure; all partial output is discarded on
error. Native internal failures stay internal even if their cause resembles a
writer cancellation. Native parsing and work between writes are not preemptible.
The formatter has no closeable resources but its admitted work participates in
host shutdown. Environment discovery advertises the same output limit passed to
the formatter on each request.

## Execution and one-shot output

An admitted execution decodes the submitted bind values with Ferret's JSON
codec, shares the tooling compile path, and creates one session with the
resulting plan. Session options carry native runtime parameters and select the
JSON output codec. Modules and host permissions come from service startup.

The invocation context remains live from parameter conversion through
`Output.Consume`. The callback checks its effective context and remaining byte
allowance before copying each borrowed chunk. Presence comes from receiving a
callback. The adapter validates the complete UTF-8 JSON only after terminal
success. A program outcome may be accompanied by a cleanup error. The caller
preserves the primary failure and never delivers partial output.

Release the output before closing its session, then close the plan before
releasing admission. A canceled Consume can leave the handle unclaimed, so the
owner calls Close even after rejected admission. The shared engine outlives all
of these descendants. Alpha.62 eagerly materializes the encoded VM result in
Session.Run. Consumer limits bound delivery bytes, while upstream encoding and
VM allocations depend on the program.

## Ownership and shutdown

The executable transfers the host to serving startup, which closes it even if
listener binding fails. The server owns its listener and connections; it borrows
the handler. The handler borrows the host. Environment methods only copy immutable
metadata and acquire no native descendants.

Serving cancellation closes HTTP admission, drains HTTP using a fresh bounded
context, then cancels request contexts when the drain expires. If the drain expires, close connections and report the error. After
HTTP serving ends, close the host under a separate fresh bounded context,
joining serving and cleanup failures. Defaults permit five seconds for each
phase. Do not derive cleanup contexts from canceled serving contexts.

Host closure locks admission, rejects new tooling and execution work, and starts a single
cleanup goroutine. It waits for all admitted operations (including descendant cleanup)
before calling engine.Close, then publishes the retained
result through a closed channel, and permits concurrent or repeated waiters.
Expiry bounds the caller's wait, not the native operation. No context-aware
native close exists, so a timed-out process cannot promise completed release.
Later in-process callers can still wait for the same cleanup result.

The admission mutex serializes WaitGroup additions with the closing transition;
active work always settles before engine closure, even after a cleanup waiter
times out. Force-closing an HTTP connection alone does not prove native work has
settled.

## Upstream integration gaps

Verified against Ferret `v2.0.0-alpha.62` and API `v1.0.0-alpha.21`.

- Native `Engine` has no release-version query. UAPI `New`/`Wrap` require the host
  to supply the opaque Core version. Dependency build metadata is the source
  used here, without creating an otherwise-unused adapter.
- UAPI has execution/compilation interfaces, but no analysis, source formatter,
  or bytecode inspection interface. Native Engine.Compile, Plan.Params, the
  artifact codec, and asm.Disassemble supply implemented tooling. Standalone
  Compiler.Analyze is not used because the service shares its host compile path.
- Standalone native analysis and formatting take no context. Compiler cancellation is
  checked between synchronous phases rather than preempting each phase.
- Native engine cleanup takes no context and does not settle caller-owned
  descendants. This host bounds its wait and documents the limitation.
- Native module contracts provide a name and registration, not a version.
  The standard-library set likewise exposes selection/registration rather than
  a metadata enumeration, so this foundation reports its actual profile label.
- UAPI defines aggressive optimization, but its Ferret adapter rejects it.
- Native diagnostic positions are not the HTTP UTF-16 coordinate contract;
  portable diagnostics do not directly supply every HTTP diagnostic field.
- Plan has no program accessor; inspection currently requires a supported
  serialize/decode round trip. Disassemble returns a complete string and exposes
  neither a writer limit nor cancellation. These costs and bounds are explicit.

The [HTTP guide](../http-contract.md) records the boundary behavior that future
operation implementations must uphold. Public request data never controls host
configuration, and transport-independent code should remain usable by a future
WASM adapter.

## Hosted browser

Hosted startup verifies discovery and connects to the advertised WebSocket
through the private relay, then closes the probe before constructing the
engine. The host registers selected native functions directly using an SDK
module. The upstream HTML bootstrap hook would replace the execution's
`drivers.Container` with its shared container.

Each hosted execution owns a memory and a CDP driver. The memory default
serves static HTTP and offline parsing. Its transport applies the same
validated IPv4 dial policy as the Chromium proxy and closes idle connections
when the execution settles. The first CDP open establishes the upstream
browser-wide HTTP(S) policy. Both drivers use the same fixed origin. Each
CDP open creates an independent disposable browser context.
The driver owns page cleanup through native result and session settlement.
The host then closes the driver under one configured cleanup deadline. It
disposes page contexts concurrently and closes their owner connections.
Disposal is confirmed by Chromium's acknowledgement or by observing that the
context is absent. An unresolved cleanup failure stops host admission and
triggers serving shutdown. The host retains admission until cleanup has
settled, even if the request has already returned a sanitized failure.

During host shutdown, active and subsequent browser cleanup is capped at the
midpoint of the remaining host cleanup budget. The rest of the budget is
available for settling admitted work and closing the engine. The execution
context governs both drivers.

The unary `DOCUMENT` function selects CDP explicitly when delegating to
`lib.Open`. The driver adapter checks initial URLs and rejects options that
would enter a shared browser context. Native drivers enforce the origin on
subsequent requests. See the [hosted HTML contract](../deployment.md#hosted-html)
for the exact registrations.
Container egress controls also cover traffic outside CDP Fetch, including
WebSockets. The launcher owns a new user/network namespace
whose only usable interface is loopback. UID rules are installed inside that namespace before Chromium
starts. A permission-protected Unix socket bridges the trusted proxy to the
parent namespace. The CDP relay inherits a parent-loopback TCP listener and
connects to browser loopback on the same port (9223).
There is no outer NET_ADMIN or SYS_ADMIN requirement.
An inherited parent namespace descriptor lets the child verify a distinct
network namespace by device/inode identity. That descriptor closes before
helpers start and is protected against exec inheritance. Netlink interface
enumeration remains required; bounded procfs inspection provides an independent
diagnostic view. The namespace owner inspects links, addresses and both routing
families before allowing the inactive fallback tunnel types observed on gen2.
It checks the running browser's identity and capabilities before readiness.
API/proxy workers use UID 65532, Chromium UID 10001, and trusted namespace relays
UID 10002. Namespace bootstrap root maps to outer UID 65530. The outer root
supervisor owns identity setup and child termination; it serves no public HTTP.
The launcher owns the ephemeral profile, fixed proxy, and navigation policy.
It verifies actual renderer sandbox status and runs direct-connection probes as
the browser UID in the real namespace before starting HTTP. On termination it
signals the API first, preserves the browser through native cleanup, and then
stops the remaining process groups under one fresh 8.5-second budget. Tini reaps
orphaned descendants. The profile disappears with ephemeral container storage.
Each essential child has one Wait owner. Startup and serving observe its retained
completion channel directly; the one-shot browser-UID probe has separate startup
ownership. The filtered environment is built once by the supervisor. Unexpected
essential-child exit reports a fixed role code with numeric status. If the
proxy or namespace worker exits during serving, the supervisor stops the API
immediately so admitted work cannot return success during a graceful drain.
Normal SIGTERM retains the API drain and cleanup allowance. Both paths share
the shutdown budget and reserve time for forced termination and reaping.
See [deployment enforcement](../deployment.md).
