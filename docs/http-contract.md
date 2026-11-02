# HTTP behavior and first-release contract

[OpenAPI](../api/openapi.yaml) owns `/api/v1`. REST, service, and runtime versions
are separate. The service exposes environment discovery, analysis, formatting,
compilation, and execution.

## Routing and application errors

`GET /api/v1/environment` describes the initialized host, working capabilities,
and effective limits. `/api/v1/analyze`, `/api/v1/format`, `/api/v1/compile`, and `/api/v1/execute` accept POST. Other
methods on these exact paths receive `405/method_not_allowed` and `Allow`.
Unavailable and unknown paths receive `404/not_found`. HEAD suppresses response
bodies. Configured origins receive exact-origin CORS headers on success and error responses.
Preflight OPTIONS validates the route method and Content-Type request header.
Responses vary on Origin, and preflight also varies on the requested method and headers.

Application responses use `application/json` and `X-Content-Type-Options: nosniff`.
Errors contain only `error.code` and `error.message`. No raw dependency error,
source body, parameter values, configuration, panic value, or stack is included
in application error envelopes or operational logs. Structured source diagnostics
are returned only in invalid-program responses, with upstream messages and hints.

| HTTP | Error code | Meaning |
|---|---|---|
| 400 | `invalid_request` | Malformed or schema-invalid JSON/options |
| 403 | `cors_denied` | Preflight origin, method, or requested headers denied |
| 429 | `execution_capacity_exceeded` | Every execution slot is occupied |
| 408 | `request_canceled`, `request_timeout` | Canceled context or expired deadline |
| 413 | `request_too_large` | Request-body or source byte limit exceeded |
| 415 | `unsupported_media_type` | Unsupported Content-Type or parameters |
| 422 | `response_too_large` | Formatted source, disassembly, or encoded response exceeded its byte limit |
| 500 | `internal_error` | Unexpected native error, panic, artifact, or cleanup failure |
| 503 | `service_unavailable` | Runtime closure prevents admission |

Cancellation responses are possible only while the transport remains writable.
Malformed HTTP, oversized headers, disconnects, and connection deadlines can be
handled by net/http outside the application and may yield text or no response.
Raw net/http logs are reduced to generic events.

Execution admission precedes body reads and provider calls. When all slots are
occupied, the service immediately returns 429/execution_capacity_exceeded. Slots
remain occupied through output settlement and cleanup. Other operations and
preflight do not use these slots. Draining rejects new operations with 503.

## Strict requests and bounds

Accept `application/json`, optionally with `charset=utf-8` (case-insensitive
charset), and no other media-type parameters. Require exactly one non-null JSON
object. Reject trailing documents, unknown or duplicate object fields, incorrect
field casing, and null values for non-nullable fields. These rules also apply to
nested source and options objects.

Source requires string `text` and optionally string `name`. Omitted name becomes
`anonymous`; names, including path-shaped labels, are never opened as files.
Missing/null text is invalid request data. Empty text is passed to Ferret, which
returns an invalid-program outcome. Analysis and formatting accept only
`source`. Compilation also accepts `options` and `include`.

Defaults and their environment overrides are in the [README](../README.md#configuration):

| Effective setting | Default | Enforcement |
|---|---:|---|
| `limits.http.maxRequestBodyBytes` | 1 MiB | Bounded reading, including unknown-length/chunked bodies |
| `limits.tooling.maxSourceBytes` | 64 KiB | UTF-8 text bytes before native work |
| `limits.tooling.maxBytecodeBytes` | 1 MiB | Disassembly string bytes after upstream generation |
| `limits.tooling.maxFormattedSourceBytes` | 1 MiB | UTF-8 bytes accepted by the formatter's output writer |
| `limits.execution.maxTimeoutMs` | 5000 ms | Single native operation deadline, shortened by request context and `options.timeoutMs` |
| `limits.execution.maxOutputBytes` | 1 MiB | Encoded execution result bytes checked before retention |
| `limits.http.maxResponseBytes` | 2 MiB | Complete JSON bytes, including final newline, before response headers |

No output is silently truncated. Disassembly and JSON encoding materialize their
complete results before the respective checks. Source limits do not guarantee
bounds on compiler or disassembler allocations, and output settings are response
limits rather than allocation limits. The minimum configurable response limit is
256 bytes so fixed sanitized error envelopes always fit. A very small response
limit can also reject environment metadata.

## Analysis and compilation

Both operations use the initialized native engine's compilation path and never
create sessions or execute FQL. Analysis uses full optimization. It checks
parsing, lexical bindings, and the semantic checks provided by native compilation.
Host-function resolution and parameter binding remain runtime concerns in
alpha.61. Unknown host functions and unbound parameters can compile successfully.
The playground supplies no additional resolver and makes no runtime-success claim.

Compilation maps `none`, `basic`, and `full` explicitly to native per-plan
options; default is `full`. `aggressive` is unsupported. The same supported values
and default are reported in environment metadata.

`include` accepts `bytecode` and `parameters`, with duplicate names rejected.
Omitted or empty inclusion returns an empty artifacts object. Parameter metadata
comes from `Plan.Params()`: referenced names are unique and ordered by native
first encounter, including references inside UDF bodies. Requested empty metadata
is `[]`; no values, types, defaults, or requiredness are inferred.

Bytecode is upstream disassembly in `{format: "text", text: "..."}`. It is display
text specific to the reported runtime version. The plan exposes no program
accessor, so requested disassembly uses `Plan.Marshal`, `artifact.Unmarshal`, and
`asm.Disassemble`. No private access, instruction decoder, or second compilation
is used. Serialization and disassembly are skipped when bytecode is not requested.

Success returns `status: "ok"` and `diagnostics: []`; source rejection returns
HTTP 200 with `status: "invalid"` and structured diagnostics. Compilation success
includes artifacts and timings; invalid compilation includes timings but no
artifacts. Analysis includes neither. `compileMs` is monotonic elapsed time around
`Engine.Compile`, including native compile hooks and excluding source creation,
artifact rendering, plan closure, and HTTP encoding.

## Formatting

`POST /api/v1/format` uses Ferret's default formatter without compilation or
host-function resolution. HTTP 200 with `status: "ok"` includes empty diagnostics
and the complete native output in `formattedSource`. A syntax error returns HTTP
200 with `status: "invalid"` and diagnostics referring to the submitted source.
That response omits `formattedSource`.

The output writer enforces `PLAYGROUND_MAX_FORMATTED_SOURCE_BYTES` on each write
and discards partial output on failure. Exceeding the limit returns 422. The
complete JSON response also has a separate limit checked after encoding.
The writer observes cancellation between writes. Parsing and native formatting
between writes are synchronous, and the byte limit applies only to emitted text.

## Diagnostic coordinates and failures

Native structured diagnostic kinds determine source rejection versus internal
failure. Syntax errors use phase `parse`; semantic/name/type/unsupported-source
errors use `compile` consistently across tooling adapters. Alpha.61 exposes errors, without a warning
severity channel or stable diagnostic codes. The adapter invents neither.
Messages and hints are copied without parsing rendered terminal errors. Ordering
follows the upstream error tree; the main span is preferred, otherwise the first
span. Internal diagnostics, unknown error kinds, and mixed source/internal errors
produce sanitized service errors. For analysis and compilation, cleanup failures take precedence over success,
source rejection, or simultaneous cancellation.

HTTP positions use zero-based lines and UTF-16 units within a line. Native compiler and formatter
spans are zero-based UTF-8 byte offsets with exclusive ends. Conversion uses the
submitted text; supplementary characters occupy two UTF-16 units and CRLF is one
line break. EOF can be a zero-width range, including on an empty last line. Native
recovery can place an insertion before trailing whitespace; that position is
preserved. Missing, reversed, out-of-range, or non-character-boundary spans omit
the range rather than fabricating precision. Source identity remains present.

## Cancellation and ownership

The request context reaches native compilation. Cancellation is checked before
work, between supported native phases and artifact stages, and after cleanup.
The compiler phases, serialization, disassembly, and native close are synchronous;
no hard preemption guarantee is made. HTTP write deadlines are connection settings,
not a compiler execution budget. Compilation is never detached into a background
goroutine to simulate cancellation.

The host owns its engine and formatter. Each compilation owns its temporary plan. Plans close
before detached results return, including on artifact-limit or cancellation
failure. Host closure rejects new admissions and waits for active operations
and descendant cleanup before engine closure. A fresh cleanup context bounds the caller's
wait; expiration does not abandon ownership ordering or forcibly terminate native
work. No cache, retained plan identifier, or session registry is introduced.

## Buffered execution

`POST /api/v1/execute` accepts `source`, an optional `params` object, and
optional `options.optimization` and `options.timeoutMs`. The optimization values
and default match compilation. Supply timeouts as positive signed 64-bit integer
literals in milliseconds. Values above the server maximum select that maximum.
The request context can always shorten the deadline. Request-body validation
precedes the native execution budget.

Bind values can contain nested arrays, objects, and explicit null. Object keys
must be unique at every depth. Ferret's native JSON decoder preserves signed
64-bit integer literals and handles other numeric representations according to
its codec semantics. A bind value the codec cannot represent receives HTTP 400.

| HTTP 200 status | Response |
|---|---|
| `ok` | Empty diagnostics and the complete native JSON value in `output`, including null. Successful absence of content omits the field. |
| `invalid` | Parse or compile diagnostics identical to `/compile`. Execution did not start. |
| `failed` | A diagnostic with code `runtime_error`, `execution_timeout`, or `output_too_large`. Output is omitted. |

A failed program never publishes bytes delivered before the terminal error.
Present zero-byte content, malformed JSON from the encoder, and cleanup-only
failures produce a sanitized HTTP 500. An earlier program failure retains its
category when cleanup also fails. The separate HTTP response limit includes the
envelope and final newline and still returns 422 when exceeded.

Timings use elapsed milliseconds. `compileMs` measures the same native call as
`/compile`. `executeMs` measures session creation through output settlement,
including encoding. Omitted phase timings indicate the phase was never reached.
`totalMs` includes parameter conversion and cleanup and excludes HTTP encoding.

A deadline during an admitted execution produces `failed/execution_timeout`,
including when it comes from the request context. A disconnected client cancels
the invocation and may receive no response. Ferret observes cancellation at VM
safepoints and codec checkpoints. Synchronous compiler phases, host calls, and
native closure can delay settlement. The service does not retry the run.

## Packaged lifecycle

The API and isolated Chromium run in one container. The launcher verifies the
renderer sandbox and actual browser-namespace egress before HTTP starts. It
keeps CDP and proxy sockets private, and fixes the runtime's CDP origin
internally. Native FQL networking and filesystem access remain disabled.

The packaged listener binds `0.0.0.0:$PORT` when Cloud Run supplies PORT, with an
explicit PLAYGROUND_LISTEN_ADDR taking precedence. Core development still
uses loopback by default. HTTP paths, CORS, admission, diagnostics and outcome
models are unchanged by packaging.

The container permits at most four seconds of HTTP drain and three seconds of
native cleanup. Browser/proxy processes remain available until the API has
settled descendants. An absolute 8.5-second container shutdown budget terminates
remaining process groups. Unexpected essential-child failure shuts down the
whole container. See [Cloud Run validation](cloud-run.md) for the required live
platform checks, separately from local packaged verification.
