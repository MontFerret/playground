# Native formatter regression history

The playground enables `POST /api/v1/format` with Ferret `v2.0.0-alpha.61`.
Alpha.59 fixed the Unicode and panic failures recorded below. Alpha.60 fixed
both literal-preservation failures. The native formatter preserves the literal
body and keeps its original delimiter when changing quotes would alter the
value. The table shows FQL source and output from the published alpha.60 module.

| Input | First and subsequent formatted output | Compiled string value |
|---|---|---|
| `RETURN "a\b"` | `return "a\b"` | `a\b` |
| `RETURN 'a"b'` | `return 'a"b'` | `a"b` |

Three consecutive formatting passes preserved the compiled string constants.
The playground regression test checks the constants and the stability of the
formatted text through the runtime adapter. [Literal-preservation tests](../internal/runtimehost/format_literals_test.go)

## Historical alpha.59 literal-preservation failures

Reproduced with default `formatter.New()`, a normal `strings.Builder`, and the
published `v2.0.0-alpha.59` module. `compiler.New().Compile` succeeds before and
after formatting. Comparing the public `Program.Constants` proves changed
meaning without executing either program.

Source cells below show literal FQL, not JSON-escaped strings:

| Input | First formatted output | Second formatted output | Compiled value change |
|---|---|---|---|
| `RETURN "a\b"` | `return "a\\b"` | `return "a\\\\b"` | One backslash becomes two, then four |
| `RETURN 'a"b'` | `return "a""b"` | `return "a""b"` | One double quote becomes two |

Every formatting call returns `nil`. The first case violates both preservation
and idempotence. The second is idempotent but changes the string constant, so an
idempotence-only gate would miss it. These are source-only defects, independent
of the earlier writer fault injection.

`pkg/formatter/internal/strings.go` preserves unrecognized backslash pairs while
reading literals, then doubles backslashes when emitting them. The native
compiler's `pkg/compiler/internal/literal_helpers.go` preserves these pairs
rather than decoding doubled backslashes. The formatter also changes quote
styles by doubling delimiters, while the compiler retains those doubled quotes
in the compiled string. Correct formatter emission must preserve the existing
compiler-decoded value, including when choosing quote delimiters. Keep that
correction upstream; do not add service-owned literal parsing or rewriting.

A minimal native-only reproduction (using `context`, `fmt`, `strings`, and
Ferret's `pkg/compiler`, `pkg/formatter`, and `pkg/source`):

```go
f, _ := formatter.New()
c, _ := compiler.New()
text := `RETURN "a\b"`
for range 3 {
    program, compileErr := c.Compile(context.Background(), source.NewAnonymous(text))
    var out strings.Builder
    formatErr := f.Format(&out, source.NewAnonymous(text))
    fmt.Printf("source=%q constants=%q compileErr=%v formatErr=%v output=%q\n",
        text, program.Constants, compileErr, formatErr, out.String())
    text = out.String()
}
```

Repeat with the FQL source `RETURN 'a"b'`. Regressions must compare native compiled
string values before and after formatting as well as repeated formatting. Cover
both quote styles, embedded delimiters, single and repeated backslashes, unknown
escapes, newline/tab escapes, and Unicode. These tests need no program execution.

## Historical alpha.58 failures, fixed in alpha.59

These historical results were reproduced using the then-pinned
`github.com/MontFerret/ferret/v2 v2.0.0-alpha.58`, default `formatter.New()`, and
`(*formatter.Formatter).Format(io.Writer, source.Source)` on Go 1.27.3. Normal
source tests use a `bytes.Buffer`. No execution, browser, or network is involved.

### Source-only corruption and diagnostic offsets

| Input source (escaped) | Expected | Observed |
|---|---|---|
| `// é\nRETURN 1` | Preserve the comment and single return statement | `err == nil`, output `"// é\nreturn 1\n1\n"` |
| `// 😀\nRETURN 1` | Valid UTF-8 preserving the comment and program | `err == nil`, output `"// \xf0\x9f\nreturn 1\nN 1\n"`, invalid UTF-8 |
| `RETURN ["😀", /* keep */ 1]` | Preserve the literal, comment, and program | `err == nil`, output `"return [\n    \"😀\",\n    1\n]\n1]\n"` |
| `RETURN "😀" +` | Syntax diagnostic with native byte span `[14,15)` | Formatter span `[11,12)`; native compiler correctly returns `[14,15)` |

A minimal source-only reproduction:

```go
package main

import (
    "bytes"
    "fmt"

    "github.com/MontFerret/ferret/v2/pkg/formatter"
    "github.com/MontFerret/ferret/v2/pkg/source"
)

func main() {
    f, err := formatter.New()
    if err != nil {
        panic(err)
    }
    var out bytes.Buffer
    err = f.Format(&out, source.NewAnonymous("// é\nRETURN 1"))
    fmt.Printf("error=%v output=%q\n", err, out.String())
}
```

The source-only failures concern output correctness, preservation, and diagnostic
locations. ANTLR token start/stop offsets count runes. In
`pkg/formatter/internal/trivia.go`, `startIndex`, `stopIndex`, `tokenStart`, and
`tokenStop` feed these offsets to `sliceBetween`, which slices the UTF-8 string as
bytes. `pkg/formatter/formatter.go` also returns parser diagnostics without the
rune-to-byte normalization performed by `pkg/compiler/compiler.go` and
`pkg/compiler/source_offsets.go`.

The smallest upstream correction is consistent rune-to-byte conversion at the
formatter's source-slicing and published-span boundaries. Reuse an upstream offset
utility where appropriate; do not duplicate parser or formatter logic in the
playground. Add regressions for BMP and supplementary Unicode before and inside
comments/literals, multiline/CRLF and EOF diagnostics, preservation, valid UTF-8,
and formatting idempotence.

### Suppressed panic errors

For source `RETURN 1`, replace the buffer above with this controlled writer:

```go
type panicWriter struct {
    buffer bytes.Buffer
    writes int
}

func (w *panicWriter) Write(p []byte) (int, error) {
    w.writes++
    if w.writes > 1 {
        panic("controlled writer failure")
    }
    return w.buffer.Write(p)
}
```

Call `f.Format(w, source.NewAnonymous("RETURN 1"))` with `w := &panicWriter{}`.
Expected: a detectable internal failure, with partial output unusable as success.
Observed: `err == nil`, `w.buffer.String() == "return"`, and two writes attempted.
This is fault injection, not a demonstrated source-only panic trigger. The
source-only corruptions above are independently reproducible with a normal writer.

In `pkg/formatter/formatter.go`, `Format` has an unnamed `error` result. Its defer
recovers a panic and adds `diagnostics.UnexpectedError` to a local error handler,
but never assigns a returned error. Recovery therefore returns the zero error
value; callers cannot observe the recorded diagnostic. The diagnostic message can
also contain panic/stack text and must never be exposed by the HTTP adapter.

The smallest upstream propagation fix is a named error result assigned from the
structured error handler during recovery. Preserve ordinary writer errors and
structured internal-error identity. Add regression tests for panics before any
output and after partial output, ordinary writer errors, and correct internal
classification without exposing panic/stack text at service boundaries.
