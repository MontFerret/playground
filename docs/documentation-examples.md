# FQL v2 documentation compatibility

This inventory uses [MontFerret/montferret.github.io at `fdef45d`](https://github.com/MontFerret/montferret.github.io/tree/fdef45d11dc78c22d0d6859dc2819236847e8ad7).
It covers the 260 `editor` blocks under `content/docs`, their initial
parameters, and nearby instructions. The existing Playground workspace under
`content/try` is the legacy compatibility check. The 61 blog blocks are
historical, including the FQL v2 syntax preview.

The browser-free baseline submitted the documentation blocks to
`/api/v1/execute` with their initial parameters. It returned 209 `ok`
outcomes and 51 failures. The HTML checks below used the same endpoint against
public Mockery with a dedicated local Chromium 158.0.8127.42, a five-second
execution deadline, and a 1 MiB encoded-output cap. Packaged validation uses
a separate controlled HTTPS fixture and the production browser configuration.
Both `make integration` and the archive-backed test in `make release-snapshot`
hit the five-second CDP deadline in an emulated Linux/amd64 VM. A diagnostic
run with more time for CDP verified extraction, interaction, screenshot
dimensions, pagination, egress denials, and memory cancellation. That run
later hit a namespace cleanup timeout during the outer-proxy fault case.
Packaged verification on a native runner remains open.

## Compatibility matrix

| Website page and section | Required capability or initial parameters | Disposition and evidence |
| --- | --- | --- |
| Language → Expressions → Variable references | Safe string functions | Safe runtime. HTTP test expects `"Hello, Ada"`. |
| Language → Bind Parameters → first example, and Query Results | `{"active":true}` with filter and projection, and a separate collection loop | Safe runtime. HTTP tests expect `["Ada","Linus"]` and `["/docs/","/try/"]`. |
| Bind Parameters → Passing parameter values, and Capability Types → Error behavior | Omitted `name`, or a value without query, iteration, or dispatch capability | Intentional diagnostic cases. Baseline returned `failed`. Task 2 should preserve the diagnostic teaching point. |
| Quick Start → Query HTML, Static Pages, Getting Started → A first look at FQL, and Language Overview → Collections and Queries | Native memory HTTP and CSS/CSSX queries. Static Pages → Use parameters starts with `{"url":"https://mockery.ferretlang.org/"}`. | Hosted HTML. Public runs returned the Mockery title, 12 articles or headings, 17 links, 24 product rows from the first numbered page, and 14 headings from the parameterized query. The missing-element and sorted-product examples returned their documented shapes. |
| Host Values → Opaque by default, and Static Pages → Parse HTML you already have | Offline memory parsing. The latter is a code block taking `@html` rather than an editor block. | Hosted HTML. HTTP tests check the exact Host Values example's anchor markup, and supplied HTML in `@html` returning its title. |
| Language → Host values and Serialization → Iterable values, and Error handling and resilience → Return a fallback value | Memory page properties, element encoding, and optional extraction | Hosted HTML. Public runs returned the Mockery title and URL, an array of 24 elements, and a null fallback. |
| Quick Start → Drive a browser, Browser-driven Pages → Open a page with a browser, Wait for a value, Extract from a browser-backed page, and Navigate within a page | Native CDP with the Mockery origin, DOM queries, polling, and navigation events | Hosted HTML. Public runs returned the title, delayed product text, 12 browser-rendered products, and the changed title after navigation. The legacy `DOCUMENT` query also returned 12 products. |
| Language Overview → Waiting is explicit | CDP and value polling | Hosted HTML. The public run returned the first delayed-request response. |
| Page Interactions → runnable examples | Native dispatch and waits on CDP values, plus `web::html::hover` in the helper example | Hosted HTML. Public runs observed the card count increase to 24, the 60 matches for `camera`, the 30 results for laptops, the form submission, focus and hover status, navigation, the multi-step product list, and results from the error-recovery example. |
| Screenshots and PDFs → Take a screenshot | Native CDP snapshot and base64 output | Hosted HTML. The public full-viewport JPEG decoded successfully from 445,416 base64 characters. The packaged fixture also checks an 80×48 PNG. |
| Pagination → Click a “next” button, Infinite scrolling, and Iterate numbered pages by URL | CDP navigation and `scroll_bottom`, or memory HTTP over numbered URLs | Hosted HTML. The original public click and HTTP loops each returned 120 items within two seconds. Infinite scrolling returned eight appended titles. |
| Browser-driven Pages → Wait for an element, Wait for network idle, and Handle timeouts | CDP waits under a five-second overall deadline | Three exact public runs returned `execution_timeout`. Keep these examples outside the hosted migration until revised and rechecked. |
| Language Overview → SQLite and YAML examples, and Basic Types → Binary values | Application registrations for `db::sqlite` and `yaml`, or general network access for `io::net::http::get` | Local runtime or original host. All three failed the browser-free execution check. |
| Worker → Runtime capabilities | CDP access to `https://ferretlang.org` | Worker example. Hosted HTML allows the HTTPS Mockery origin only. |
| Testing guide, and Screenshots and PDFs → file and PDF recipes | Lab suite and fixture behavior, filesystem writes, or PDF generation | Keep local instructions. Pure FQL assertions may run in the safe profile. The deliberately false assertion fails as designed, and the example using `t::not::include` failed. Core alpha.62 registers `t::not::contains`. |
| Blog release examples and syntax preview | Version-specific FQL or host functions | Historical. Keep their version-specific treatment. |

## Task 2 gates

The examples with observed hosted results above are the candidates for the
routing change after packaged verification. Three browser-guide examples
need revisions first.

- **Wait for an element.** Waiting for the populated state and reading the
  matched element's text returned `Ready after 1500ms` locally. The readiness
  selector is `[data-testid="delayed-long"][data-state="ready"]`.
- **Wait for network idle.** The event did not arrive on the public landing
  page within the execution budget. Verify the example on a controlled network
  scenario before routing it to Playground.
- **Handle timeouts.** The five-second FQL wait starts after browser navigation.
  Shorten the in-script wait so the fallback can run within the service
  deadline.

See [Hosted HTML](deployment.md#hosted-html) for the function list and driver
restrictions. CDP parsing and shared cookies are unavailable in the hosted
profile because rc.30 can use the browser's default context for them. Direct
downloads bypass rc.30’s driver policy. Both remain outside the migration set.
