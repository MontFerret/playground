# Go code style

Adapted from [Ferret's Go style](https://github.com/MontFerret/ferret/blob/3995763121b92b67f07484af1ca766e5c5235e80/docs/maintainers/engineering/code-style.md).
Preserve these rules and their exceptions in handwritten Go, including tests.

## Types and files

- Group related package-level declarations in `type ( ... )`, including structs,
  interfaces, aliases, primitives, and types with methods. Normally put a file's
  related types in one group; do not split them just because they have methods.
- A standalone declaration is appropriate when the file has one type. Do not
  create an artificial one-element group.
- Organize files by cohesive responsibility and ownership, not one file per type.
  Keep related small types together and methods close to their types.
- Avoid generic `helpers.go` or `utils.go` dumping grounds and excessive splitting.
- Within each file, place public declarations before private declarations. Keep
  public methods/functions grouped before private implementation helpers.

## Method ownership and constructor exception

Behavior that owns a type's state, invariants, lifecycle, or resources is a
method. A file containing methods must not also contain unrelated package-level
functions. Package functions can coexist with methods only when they construct
or initialize a type owned by that file, including explicit construction
functions not named `New`. Put genuinely package-level behavior in a separate,
responsibility-focused file. Being used by only one method is not an exception.

Do not split a cohesive family of behavioral types to enforce one type per file.
Small passive, method-free, function-local types are fine when used only in that
function. Promote meaningful reusable domain concepts to private package types.

## Semantic comments

Normally document exported functions and methods, especially ownership and
cross-package contracts. Comment private code when semantics, invariants,
cancellation, cleanup, or protocol behavior would otherwise be unclear.
Explain why or what must remain true; do not mechanically restate signatures.
For example, document whether `Close` retains errors and permits repeated calls.

## Control flow

- Keep a producer adjacent to the immediate check of its result:
  `value, err := operation()` followed directly by `if err != nil`.
- Separate that producer/check unit from preceding independent work.
- Separate independent `if` blocks with a blank line. After completed control
  flow, separate the next independent statement or logical unit.
- Put a blank line before `return` or `break` when another statement precedes it
  in the same block. Do not add a leading blank line when it is the first statement.
- Run `make fmt`; formatting alone does not verify the semantic conventions above.
