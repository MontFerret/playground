# HTTP behavior and first-release contract

[OpenAPI](../api/openapi.yaml) is the versioned contract. `/api/v1` and the
document's `info.version` describe that contract, separately from service and
runtime versions. Only environment discovery is implemented. Planned operations
are marked in the specification; their documented 200 responses are future
models, not current promises that the routes are callable.

## Implemented behavior

`GET /api/v1/environment` describes the initialized native host, working service
capabilities, and effective HTTP settings. All other paths receive JSON `404`.
Unsupported methods on the exact environment path receive JSON `405` with
`Allow: GET`. This includes OPTIONS and HEAD; HEAD suppresses the response body.
There are no automatic redirects for trailing slashes and no CORS policy yet.

Application responses use `application/json`. Errors contain only
`error.code` and `error.message`; they omit source, parameters, stack traces,
configuration, and dependency error details. Internal failures and recovered
handler panics receive `500` with `internal_error`. Operational logs likewise
omit payloads, panic values, and raw dependency errors from request handling.

Malformed HTTP, oversized headers (including `431`), disconnects, and connection
deadlines can be handled by Go's HTTP server before or outside the application
handler. They may have standard-library text responses or no response at all;
the JSON envelope applies to application-generated responses. Raw net/http log
events are reduced to a generic event so an unexpected transport panic does not
dump its text or stack into operational logs.

## Planned request decoding

All four POST operations currently return `404`, regardless of body or media
type. Their future handlers must:

- Enforce a finite configured byte limit while reading, including chunked
  requests. Reject oversized bodies with `413`/`request_too_large`.
- Accept `application/json`, optionally with `charset=utf-8`; reject other media
  types or charsets with `415`/`unsupported_media_type`.
- Require exactly one non-null JSON object. Reject malformed JSON, trailing
  documents, missing required fields, unknown fields, and unsupported options
  with `400`/`invalid_request`.
- Apply unknown-field rejection to service request objects, including source and
  options. Bind-parameter keys are intentionally open JSON object members.
- Keep public request data separate from host configuration: no filesystem
  roots, network clients, module loading, or arbitrary operator options.

Source has required `text` and optional display-label `name`. Omitted names use
`anonymous`. Empty text is a source-validation outcome, not a missing field.
Bind parameters are JSON objects and must use Ferret's parameter conversion.
Compilation accepts `none`, `basic`, or `full` optimization (default `full`);
UAPI's `aggressive` level is unsupported by this Ferret dependency. Compile
inspection reserves `parameters` and native `bytecode` JSON only. Returned
artifacts contain only requested entries; no signatures, AST, IR, or assembly
are fabricated. Bytecode fields belong to the reported Ferret version.

No POST body limit or execution/output budget is currently advertised. Later
handlers must implement their finite bounds before adding them to environment
metadata. `429`/`rate_limited` is reserved for future admission controls, with
`Retry-After` when known; no limiter exists now. `503`/`service_unavailable` is
reserved for future temporary service failures.

## Program outcomes and locations

Analysis, formatting, and compilation return `ok` or `invalid` operation models
with diagnostics. Execution additionally supports `failed` for submitted-program
or operation-budget failures. These are 200 program outcomes, not transport
errors. Internal engine/service defects must instead produce sanitized HTTP
service errors, not be mislabeled as failures in submitted FQL.

Diagnostics have severity, phase, message, optional stable code, optional
location, and optional hint. Native kinds do not necessarily provide stable
codes, severity, or operation phase directly. Adapters must map documented
engine information and operation context, never derive codes from message text
or return raw native diagnostic objects containing source or internal causes.

HTTP locations use zero-based lines and UTF-16 code units within each line.
Ranges have exclusive ends and can be zero-width at EOF. Native source positions
use one-based lines and UTF-8 byte columns, with zero-based byte spans. UAPI
deliberately leaves offset units to producers. A future adapter must use the
submitted text to convert both ends, including CRLF and supplementary Unicode
characters. These conversion functions are not needed by environment discovery
and are not implemented prematurely.

## Buffered execution output

A successful response requires `output`, which may be JSON `null`. A failed or
invalid response omits `output` entirely. The schemas reject both a successful
response without output and a failed response with `output: null`.

Future execution must consume and finalize Ferret's one-shot output, enforce
finite byte limits during consumption, and close abandoned output, sessions,
and plans in ownership order. Buffer the complete JSON response before sending
headers; never expose partial engine output as successful JSON. Preserve native
JSON bytes rather than decode/re-encode through host floating-point values.
There is no HTTP streaming framing in v1's initial response contract.

Timings are monotonic elapsed milliseconds: total time is present, and compile
and execute times appear only for reached phases. Native execution also encodes
output, so execute time includes encoding/consumption; no separate encoding
timer is invented. Native synchronous phases cannot promise hard preemption
merely because their caller has a context deadline.
