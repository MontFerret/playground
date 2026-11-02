# Packaged deployment

The first supported platform is native Linux/amd64. Install Docker and Docker
Compose v2.24.4 or later, and allow unprivileged user namespaces on the host.
The browser uses the checked-in seccomp profile and its own namespace and
seccomp sandbox. Readiness inspects an actual renderer's nested PID namespace
and additional seccomp filters. An unhealthy browser prevents API startup.
Apple Silicon emulation does not provide the required inner seccomp evidence.

From a clean checkout, start the complete deployment with `make up`. The only
published port is `127.0.0.1:8080`. Set `PLAYGROUND_PORT` to select another
local port. `make down` removes the deployment and ephemeral policy volume.
The Docker daemon is required to apply the one-shot network namespace policy.

| Service | CPU limit | Memory limit | Identity and ownership |
|---|---|---|---|
| API | 1 | 256 MiB | UID 65532, owns native engine and request descendants |
| Chromium | 2 | 1 GiB, 256 MiB shared memory | UID 10001, Compose owns process and ephemeral profile |
| Egress proxy | 0.5 | 64 MiB | UID 65532, owns CONNECT tunnels |
| CDP relay | shared deployment resources | shared deployment resources | UID 10002, trusted loopback control relay |
| Firewall setup | startup only | startup only | Root with NET_ADMIN only, installs rules before Chromium starts, then exits |

All serving processes have capabilities dropped and read-only roots.
Chromium's writable profile is an ephemeral tmpfs. The proxy and API receive
no cloud credentials. The Chromium image is pinned to Chrome 148.0.7778.178,
Linux/amd64 manifest `sha256:08afea9e2d64b281c4b7e361d8349ac8c2685bee537e947639da66b858def763`.
The seccomp profile derives from Playwright v1.55.0's
[Docker profile](https://github.com/microsoft/playwright/blob/v1.55.0/utils/docker/seccomp_profile.json),
with chroot permitted for the nested user namespace.

## Examples and compatibility

| Example | Required registrations | Host access |
|---|---|---|
| `RETURN @value` | Safe standard library | None |
| [Product extraction](../examples/products.fql) | Restricted HTML DOCUMENT, CDP driver, safe conversions and strings | HTTPS Mockery origin |

```sh
curl -fsS http://127.0.0.1:8080/api/v1/execute \
  -H 'Content-Type: application/json' \
  -d '{"source":{"text":"RETURN @value"},"params":{"value":{"answer":42}}}'
python3 -c 'import json,pathlib; print(json.dumps({"source":{"text":pathlib.Path("examples/products.fql").read_text()}}))' |
  curl -fsS http://127.0.0.1:8080/api/v1/execute \
    -H 'Content-Type: application/json' --data-binary @-
```

The product query is copied from the public
[playground](https://ferretlang.org/try/). It uses CDP, waits for a nonempty
product-card collection and returns brands, titles and numeric prices.
The FQL is unchanged. The adapter subscribes to CDP lifecycle events before
initial navigation because the pinned module can miss completion on fast pages.
The existing upstream page loader handles the completed document.

Worker's broader module set is outside the hosted example contract. Only
DOCUMENT is registered from the upstream HTML library. DOWNLOAD and PARSE are
unavailable. DOCUMENT options may select the cdp driver and a shorter load
timeout. Cookies, headers, init scripts, viewport, user-agent and reuse settings
are host-owned. Pages in one execution share a fresh incognito context, and
its closure removes storage and popups before an execution slot is released.

## Enforcement

| Restriction | Enforcement owner |
|---|---|
| Concurrent execution cap | HTTP handler, acquired before reading a POST body, released after native and browser cleanup |
| Native HTTP and filesystem | Safe standard-library selection, explicit disabled Ferret HTTP client, no filesystem root |
| Initial document URL and host options | Run-local CDP driver |
| Redirects, subresources, frames, workers, WebSockets | Fixed manual proxy and single exact CONNECT authority |
| DNS rebinding and internal addresses | Proxy resolves IPv4 once, rejects nonpublic answers, dials the checked address |
| Direct sockets, UDP, DNS, localhost and CDP access from browser | UID-specific inet nftables output rules before Docker DNS translation |
| File and privileged-scheme navigation | Chromium managed URL policy and driver validation |
| CDP control | Private relay, distinct trusted UID, no published control port |
| CORS | Exact operator origins, Content-Type preflight, headers installed before route and error handling |
| Sensitive logs | Discarded native/browser/relay diagnostics, sanitized HTTP and startup events |

The browser UID may connect only to the proxy's pinned deployment IP and port
3128. Its only loopback traffic is replies to established trusted CDP connections.
The same inet rules cover IPv6. The proxy permits only
`mockery.ferretlang.org:443`. It does not forward plain HTTP or expose another
proxy target. The test fixture changes DNS only inside a disconnected Compose
network. It uses a synthetic public address and an ephemeral TLS key, with
a test-only certificate SPKI exception.

On SIGTERM, admission closes, active HTTP requests get the grace period, and
expired requests are canceled before resource cleanup. Defaults allow five
seconds each for drain and cleanup. Compose allows fifteen seconds before a
forced kill. The suite uses five seconds for drain and four for cleanup, and
asserts exit within twelve seconds.

## Verification and cutover gates

`make integration` starts the actual development image. `make release-snapshot`
imports the OCI archive and runs the same suite with API builds disabled.
Both trap failures and remove the test project, networks and volumes.
The private fixture uses JavaScript to render cards, explicit hold/release
signals, state probes, and reachable forbidden-request canaries.

The first run pulls the missing browser and Python fixture images and builds
the local service images. Startup then uses only those prepared images, with
API builds disabled when verifying an imported release archive.
After caching the images, use `PLAYGROUND_USE_CACHED_IMAGES=1 make integration`
to skip image preparation and avoid builds and pulls. The suite requires
Python 3, OpenSSL and Docker Compose, and does not require cloud credentials
or public fixture websites.

Native Linux/amd64 packaged verification is required before cutover. The
website currently sends Worker's `{text, params}` body and reads raw output.
It needs to call `/api/v1/execute` with `source.text`, consume the outcome
envelope and handle 429. Switching the website and production deployment
require separate changes. Any failed sandbox, isolation, egress or shutdown
assertion blocks release verification.


Verified on 2026-11-02 with the pinned amd64 browser and both the development
image and GoReleaser OCI archive. The host was an Ubuntu 24.04 amd64 guest,
Linux 6.8.0-142-generic, with 4 vCPUs and 4 GiB RAM under QEMU on Apple Silicon.
The HTTP checker ran inside the guest. The development-image suite measured
4.13 seconds for graceful shutdown and 6.98 seconds for forced cancellation,
including Docker command overhead, within its twelve-second bound.
The resource settings in the table above were used unchanged. Local
`make check` passed with Go 1.27.3 on Darwin/arm64. Tagged publication, Google
IAM, GHCR visibility, and native GitHub Actions execution remain first-release
or CI checks.
