# Testing and validation

Adapted from [Ferret's testing policy](https://github.com/MontFerret/ferret/blob/3995763121b92b67f07484af1ca766e5c5235e80/docs/maintainers/engineering/testing.md).

## Contract ownership

Test behavior where it is owned: configuration validation in configuration tests,
HTTP routing and error envelopes in transport tests, registration and resource
rollback in runtime tests, and startup/shutdown at their integration boundary.
Do not reproduce Ferret's parser, compiler, or serializer test suites here.

Cover positive, negative, boundary, concurrency, cancellation, and cleanup cases
meaningfully. Prefer regression tests that fail without the fix. Use local HTTP
servers, controlled modules, and explicit synchronization rather than timing
assumptions. Fast Go tests must not require browsers, cloud credentials, or public
network access after dependencies have been downloaded.

## Commands

Start with `go test ./internal/<owning-package>`, then broaden according to risk:

- `make fmt` formats handwritten Go; inspect its rewrite surface.
- `make fmt-check` rejects unformatted Go without editing it.
- `make test` runs repository tests.
- `make api-check` validates the checked-in OpenAPI document, examples, and
  representative implemented responses using the pinned kin-openapi dependency.
- `make vet` runs static checks; `make test-race` runs tests with race detection.
- `make build` builds the executable; `make run` starts it locally.
- `make check` runs the formatting, static, contract, test, race, and build checks
  used by `.github/workflows/build.yml` on the Go version in go.mod.

Complete the [mandatory self-review](../../../AGENTS.md#mandatory-final-self-review)
after initial validation, fix actual findings, and rerun affected checks.
Report exact commands actually run and explain failures or skipped validation.

## Performance

Benchmark changes that introduce a concrete hot path or materially affect
latency, allocations, buffering, concurrency, pooling, or cleanup overhead.
Capture before/after evidence under the same environment and command, comparing
`ns/op`, `B/op`, and `allocs/op` where available. Investigate regressions and
report commands and results accurately. Documentation, ordinary metadata
handling, and foundation wiring do not require speculative benchmarks.

Tooling has a focused native-versus-service comparison for compilation and
requested inspection. It warms native parser prediction state before timing:

```sh
go test ./internal/runtimehost -run '^$' -bench '^BenchmarkTooling$' -benchmem -benchtime=500ms -count=10
```

Both variants construct source, compile once, produce the same requested native
artifacts, and close the plan. This isolates runtime adapter overhead; it does
not measure HTTP latency or establish a historical endpoint baseline. Record
the machine/toolchain, medians, bytes, and allocations; avoid treating noisy
single samples as regressions or claiming a guarantee from the comparison.

The formatting adapter has its own native-versus-service comparison.

```sh
go test ./internal/runtimehost -run '^$' -bench '^BenchmarkFormat$' -benchmem -benchtime=500ms -count=10
```

Both variants warm parser state, construct the same source, and format to a string
using one shared native formatter. Service measurements include admission,
cancellation checks, and bounded writing. No HTTP endpoint latency is measured.

When updating Ferret, run `TestFormatPreservesLiteralValues`. It compares the
compiled constants before and after formatting and formats the output again to
check idempotence. The HTTP tests exercise formatting through the public router
and check that the advertised output limit is enforced.

The execution consumer benchmark delivers a 64 KiB JSON value in 256-byte
borrowed chunks. Compare known and unknown lengths with the same byte limit.

```sh
go test ./internal/runtimehost -run '^$' -bench '^BenchmarkFragmentedOutput$' -benchmem -benchtime=300ms -count=3
```

## Packaged deployment

`make integration` runs browser fixtures through the single-container HTTP service.
`make release-snapshot` builds and imports the GoReleaser OCI archive before
running that same suite. Both require native Linux/amd64 and a working
unprivileged Chromium sandbox. See [deployment guidance](../../deployment.md).
These targets are separate from fast Go tests. Process inspection and failure
injection require a local Linux Docker daemon, Python with pidfd support, and
root access (or noninteractive `sudo`) for host procfs inspection. Container privileges are unchanged.
The suite has a separate test-only TLS fixture container. It exercises namespace
denial through the normal release entrypoint, startup connection probes from the
actual browser namespace, essential-child failure using test-side process discovery,
and whole-container shutdown under nine seconds. Local Docker success is separate from the mandatory real Cloud Run
compatibility checks in [Cloud Run validation](../../cloud-run.md).
Separate packaged cases deny NETLINK_ROUTE and NETLINK_NETFILTER sockets and
require the ordinary entry point to exit without opening HTTP. Route denial
remains a failure: live Cloud Run inspection succeeded and exposed an invalid
three-interface layout, so the conditional procfs-only replacement was not made.
