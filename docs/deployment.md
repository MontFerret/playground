# Single-container deployment

The product is one Linux/amd64 image containing the API, Chromium, and private
supervision, proxy, and control processes. It requires working unprivileged user
namespaces, namespace-local nftables, and Chromium's renderer sandbox. Every
required check runs before HTTP starts. Unsupported isolation fails closed.
There is no `--no-sandbox` fallback.

## Local startup

Use native Linux/amd64 and Docker Engine 25 or later:

```sh
make up
make down
```

`make up` builds and starts one container named `playground`, waits for health,
and publishes only `127.0.0.1:8080`. `PLAYGROUND_PORT` changes the local port.
`make down` removes the container and its anonymous base-image volume.
Compose and external browser/proxy services are not required.

The checked-in seccomp profile allows namespace creation locally. The selected
identity-management capabilities are a subset of Docker's defaults; neither
outer NET_ADMIN nor SYS_ADMIN is granted. Cloud Run must pass the same checks
under its own standard second-generation runtime policy.

| Resource | Packaged setting |
|---|---|
| CPU | 2 CPUs shared by all processes |
| Memory | 2 GiB shared by all processes |
| Local temporary storage | 1 GiB tmpfs; ephemeral in Cloud Run |
| Shared memory | 64 MiB; Chromium uses temporary files with `--disable-dev-shm-usage` |
| Execution admission | 2 slots per API instance |
| HTTP drain / cleanup | At most 4 seconds / 3 seconds |
| Container shutdown | 8.5-second absolute budget, below Cloud Run's 10 seconds |

Cloud Run configuration and the required live compatibility gate are described
in [Cloud Run validation](cloud-run.md). No release or deployment should claim
Cloud Run compatibility based only on Docker tests.

## Ownership and enforcement

The trusted outer supervisor starts a non-root proxy, creates a private browser
user/network namespace, and connects it through permission-protected Unix
sockets. The namespace has only loopback and no external interface or route.
Its bootstrap root maps to outer UID 65530. It installs UID-specific nftables
rules there, then starts Chromium as UID 10001 and trusted relays as UID 10002.
The API and outer serving helpers run as UID 65532.
Inherited supplementary groups are cleared before any worker starts.

The supervisor passes its network-namespace descriptor to the namespace child.
The child requires a different namespace device/inode identity and closes that
descriptor before starting helpers. Netlink remains the authoritative interface
check; a bounded, strictly parsed `/proc/self/net/dev` view supplies independent
diagnostics. Exactly one interface, `lo`, is required. Fixed failure stages and
numeric errno/child exit status distinguish inspection, layout, loopback and
nftables failures. Logs contain counts and booleans, never interface names,
addresses, raw child errors or request data.

The browser UID may open only the namespace proxy port, and may reply to
established trusted CDP connections. Other TCP, UDP, IPv6, DNS, metadata,
loopback control, and direct external connections are denied. The Unix bridges
are inaccessible to that UID. The proxy accepts only CONNECT to
`mockery.ferretlang.org:443`, rejects every nonpublic IPv4 DNS answer, and dials the
validated numeric public address without a second lookup.

Readiness checks actual renderer UIDs, nested PID namespaces and additional
seccomp filters. It also executes direct-connection and Unix-socket bypass
probes as the browser UID inside the actual namespace, with a positive proxy
control. The CDP relay binds only parent loopback. These probes run once during
normal startup; the API opens only after they succeed. Integration tests inspect and
signal children through test-side process discovery, without a production
operator socket or published PID inventory.

Chromium's profile is private ephemeral storage. Managed policy blocks file and
privileged-scheme navigation, downloads and file selection. Chromium uses a
fixed proxy with its implicit loopback bypass disabled. Browser/relay stderr,
proxy errors, and native runtime logs are discarded. Operational events contain
fixed stage codes and shutdown durations, never FQL, parameters, page content,
or credentials.

On SIGTERM the supervisor signals the API first. HTTP admission closes and the
API drains before cancellation and native/browser cleanup. Only afterwards are
browser and proxy process groups stopped. Tini reaps orphaned descendants.
Unexpected browser, proxy or relay exit terminates the whole container. The
same bounded cleanup covers partial initialization. The profile is discarded
with container storage instead of recursively walking caches during shutdown.

## Examples

| Example | Registrations | Host access |
|---|---|---|
| `RETURN @value` | Safe standard library | None |
| [Product extraction](../examples/products.fql) | Restricted HTML DOCUMENT and CDP | HTTPS Mockery origin |

```sh
curl -fsS http://127.0.0.1:8080/api/v1/execute \
  -H 'Content-Type: application/json' \
  -d '{"source":{"text":"RETURN @value"},"params":{"value":{"answer":42}}}'
python3 -c 'import json,pathlib; print(json.dumps({"source":{"text":pathlib.Path("examples/products.fql").read_text()}}))' |
  curl -fsS http://127.0.0.1:8080/api/v1/execute \
    -H 'Content-Type: application/json' --data-binary @-
```

The public product FQL is unchanged. Initial navigation subscribes to lifecycle
events before navigating, avoiding the pinned HTML module's fast-page race.
Only upstream DOCUMENT is registered. DOWNLOAD and PARSE remain unavailable.
Request options cannot select a shared profile, cookies, arbitrary headers,
init scripts, user agents, or another driver. Each execution lazily creates a
fresh disposable browser context; its pages, popups and state are removed
before its execution slot is released.

Pins remain Go 1.27, Ferret `v2.0.0-alpha.61`, Universal API
`v1.0.0-alpha.21`, and HTML `v1.0.0-rc.24`. The browser is MontFerret Chrome
156.0.8078.80, Linux/amd64 manifest
`sha256:78c7fd698d088c639bc58221eb36565ce2c93a0b4f5af2cdf77fd3fb741eebf4`.
The local seccomp profile derives from [Playwright v1.55.0](https://github.com/microsoft/playwright/blob/v1.55.0/utils/docker/seccomp_profile.json),
with chroot permitted for the nested user namespace.

## Verification and cutover

`make integration` uses the development image. `make release-snapshot` imports
its OCI archive and runs the same suite with API builds disabled. Both remove
containers, anonymous volumes and test networks on every exit. A separate
private TLS fixture uses JavaScript-rendered cards, explicit hold/release
signals, storage probes and reachable forbidden-request canaries. It routes the
Mockery hostname only inside the test container and trusts one ephemeral SPKI.
After caching images, `PLAYGROUND_USE_CACHED_IMAGES=1 make integration` skips
builds and pulls. No public fixture website or cloud credentials are needed.

Local namespace, UID policy, Unix bridging and renderer verification passed in
an Ubuntu 24.04 Linux/amd64 QEMU guest (4 vCPUs, 4 GiB RAM). The container used
the settings above. Real Cloud Run compatibility remains a release blocker
until the separate procedure passes. Apple Silicon instruction emulation does
not provide the renderer sandbox evidence and fails readiness.

The 2026-11-02 cached/offline archive run passed on Ubuntu 24.04.5 x86_64,
kernel 6.8.0-142-generic. Whole-container shutdown measured 4.294 seconds with a
draining request and 6.866 seconds with forced cancellation. Namespace-denial
rollback, browser/relay failure, reaping and all HTTP/browser assertions passed.
The tested unified OCI manifest is
`sha256:4e74bf4f0e59e4dff25dac12659a4546df294afdbed351f53db210952b3afbb5`.
This is local snapshot evidence, without registry publication or Cloud Run
validation. The suite prints runner resources and shutdown measurements on
every passing run; retain that output with the archive and `dist/image.json`.

Live gen2 validation on 2026-11-03 failed the loopback-only layout requirement.
The child namespace differed from its parent, and Netlink and procfs both
successfully reported three interfaces. The container exited before nftables,
Chromium or HTTP startup; initialization rollback took 1 ms. This result does
not justify a procfs-only replacement for interface enumeration. The layout
gate remains enforced, and the current Cloud Run configuration is a release
blocker. See [the retained validation record](cloud-run.md#observed-validation-result).

The website still sends Worker's `{text, params}` payload and reads raw output.
It must use `/api/v1/execute`, `source.text`, the outcome envelope, and 429
handling. Website migration, production deployment, and release tags remain
separate actions. The earlier API-only artifact is superseded by this packaging.
