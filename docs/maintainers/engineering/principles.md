# Engineering principles

Adapted from [Ferret's principles](https://github.com/MontFerret/ferret/blob/3995763121b92b67f07484af1ca766e5c5235e80/docs/maintainers/engineering/principles.md).

## Ownership and focused changes

Implement behavior at its owning boundary, described in the
[architecture guide](../architecture.md). Ferret owns language semantics and
serialization; HTTP owns transport; runtime composition owns registration and
resources. Do not reproduce an owning dependency's behavior in this service.

Choose the smallest change that completely solves the problem. Abstractions,
package moves, exports, and supporting cleanup need a concrete correctness,
ownership, or maintainability reason. Leave unrelated correct code alone.

## Correctness and compatibility

Preserve correctness, lifecycle, and ownership before optimizing. Follow the
[testing policy](testing.md) for meaningful performance changes.

The versioned HTTP contract is API-sensitive. Preserve documented behavior or
make intentional changes explicit with regression coverage. Keep REST version,
service version, and runtime version separate. Do not expose internal errors or
unstable host implementation details as accidental contracts.

Keep packages internal unless an external consumer requires otherwise. Export
only the cross-package contracts needed now, with comments describing their use
and ownership. Do not add debugger or future execution infrastructure merely to
anticipate a later task.

## Documentation as part of changes

Evaluate documentation impact for every non-trivial change and update affected
guidance, OpenAPI schemas, examples, configuration, workflows, and ownership
documentation in the same change. Verify local links and documented commands.
Avoid mechanical documentation churn when behavior is unchanged.

Document concrete upstream limitations rather than silently inventing APIs or
editing dependencies. Report any unavailable required documentation follow-up
precisely. This repository owns its service documentation; changing upstream
repositories requires separate task scope.
