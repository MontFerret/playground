# Agent workflow

This is the operating guide for agents changing the Ferret Playground API.
Shared engineering policy belongs in [docs/maintainers](docs/maintainers/README.md).
Adapted from Ferret main revision
[`3995763121b92b67f07484af1ca766e5c5235e80`](https://github.com/MontFerret/ferret/tree/3995763121b92b67f07484af1ca766e5c5235e80).

## Sources of truth

- `go.mod` owns dependency versions and the minimum Go version.
- `Makefile` owns validation commands; `.github/workflows/build.yml` owns CI.
- `api/openapi.yaml` owns the HTTP contract and identifies planned operations.
- Current code and tests establish implemented behavior and ownership.
- `README.md` owns local startup and configuration instructions.

Resolve disagreements against the owning source and correct stale documentation.
Do not import assumptions from Ferret v1, Worker, or unreleased local changes.

## Engineering workflow

For every non-trivial change:

1. Read the [architecture guide](docs/maintainers/architecture.md) and relevant
   [principles](docs/maintainers/engineering/principles.md),
   [Go style](docs/maintainers/engineering/code-style.md), and
   [testing policy](docs/maintainers/engineering/testing.md).
2. Identify the owning layer, intended contract, existing behavior, and resource
   ownership. Inspect selected upstream APIs rather than guessing signatures.
3. Make the smallest coherent change. Do not add speculative frameworks,
   interfaces, host options, or unrelated refactors.
4. Add meaningful contract tests; begin with focused validation and broaden to
   `make check`. Benchmark only concrete performance-sensitive changes.
5. Update the OpenAPI contract, examples, README, and maintainer guidance whenever
   their behavior or instructions change. Do not modify upstream repositories
   as a side effect; document concrete upstream gaps.
6. Perform the mandatory final self-review below, fix findings, and rerun every
   affected check before reporting completion.

## Service boundaries

- Ferret owns parser, compiler, runtime, and serialization semantics. Delegate
  to supported native APIs and reuse UAPI contracts where they fit.
- HTTP handling owns routing, protocol validation, status codes, and diagnostic
  coordinate conversion. A source name is a label, never a host file path.
- Runtime composition owns module registration, associated metadata, and owned
  resources. Public request data must not select arbitrary host configuration.
- Configuration parsing belongs at startup, not in handlers or runtime methods.
- Propagate contexts and settle children before releasing their owners. Cleanup
  must remain possible after serving cancellation and have a bounded wait.
- Capabilities and limits describe working, enforced behavior. Planned routes
  must not have success-shaped placeholders.
- API changes require matching contract and documentation changes.
- Keep transport-independent behavior usable by a future WASM adapter; do not
  move HTTP types or environment-variable parsing into runtime operations.
- Never dump source, parameters, credentials, panic values, or configuration in
  operational logs or public error responses.

## Mandatory final self-review

After initial implementation and validation, review the complete resulting
implementation and diff as a second pass, not merely a test-result check:

- **Correctness and lifecycle:** check boundaries, cancellation, concurrency,
  initialization rollback, cleanup errors, resource leaks, and error propagation.
- **Architecture and contract:** check layer ownership, dependency direction,
  compatibility, truthful metadata, and native semantics. Reject duplicated
  behavior, unnecessary exports, or speculative abstractions.
- **Clarity:** apply the Go style guide; check grouped types, declaration order,
  method ownership, cohesive files, semantic comments, and unnecessary complexity.
- **Evidence:** check meaningful positive, negative, boundary, cancellation, and
  cleanup coverage. Assess performance only where the changed path warrants it.
- **Complete scope:** remove temporary artifacts and unrelated changes; verify
  documentation, examples, links, and commands match the final behavior.

Correct actual findings and repeat affected validation. Review is not permission
to expand scope through optional style churn or upstream semantic changes.

## Completion reporting

Report changed behavior and ownership, dependency versions, documentation impact,
tests and exact validation commands actually run, review completion and findings
corrected, and remaining gaps or skipped checks. Never claim an unrun or failed
check passed. Distinguish environment limitations from product failures.
