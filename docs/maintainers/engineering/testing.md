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
assumptions. Tests must not require browsers, cloud credentials, or public
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
  used by `.github/workflows/build.yml` on Go 1.25.x and 1.27.x.

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
