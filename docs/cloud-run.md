# Cloud Run compatibility and isolated validation

The deployment target is one Linux/amd64 container on standard second-generation
Cloud Run. No sidecars, separate browser service, Sandboxes preview, added outer
capabilities, or disabled Chromium sandbox are part of this design.

Local Docker verification is complete only for the tested Linux host. **Live
Cloud Run validation failed and blocks release.** In particular,
Cloud Run must allow the new user/network namespace and namespace-local nftables
under its standard policy. Failure leaves HTTP unopened and logs a fixed stage
code. Do not bypass the check or weaken isolation to make a revision ready.

The [execution environment](https://docs.cloud.google.com/run/docs/configuring/execution-environments)
documents namespace support, while the [runtime contract](https://docs.cloud.google.com/run/docs/container-contract)
restricts privileges and grants ten seconds after SIGTERM. Those documents are
not evidence that this image passed its checks on Cloud Run.

## Prerequisites

Perform this procedure only as a separately approved cloud operation. It writes
a unique private validation image reference and creates a separate authenticated
service named `playground-validation`. Its first healthy revision receives only
explicit authenticated test requests at that service's URL. The production
`playground` service and its traffic are not changed. Do not reuse an unrelated
existing validation service. An existing approved service with a healthy baseline
can instead use a tagged zero-traffic revision.

Use project `montferret`, region `us-east1`, and a dedicated runtime service
account `playground-runtime@montferret.iam.gserviceaccount.com` with no project
roles or Google API permissions. The platform's image-pull service agent needs
repository read access. The deploying identity needs Cloud Run deployment and
service-account-use permissions; the testing identity needs Cloud Run Invoker.
The release service account and GitHub credentials are never attached to the
runtime container.

The runtime account was created during the 2026-11-03 validation without project
role grants. Reuse it; do not attach the default Compute Engine identity.

Start validation with two vCPUs, 2 GiB memory, HTTP concurrency 16, execution
capacity two, and one validation instance. HTTP concurrency above two permits
discovery and immediate application 429 responses while execution slots are full.

Use the newly verified unified OCI archive from `make release-snapshot` or the
intended release job. The prior API-only image is superseded. Preserve the exact
archive, descriptor, source revision, browser digest and all validation output.
A snapshot is a validation candidate and remains ineligible for release aliases.

## Prepare the exact validation image

Authenticate Skopeo with an approved Google identity and repository Writer access.
Use an explicit writable REGISTRY_AUTH_FILE, as shown in the release recovery
guide. From the repository root:

```sh
set -euo pipefail
cloud_run_service=playground-validation
cloud_run_digest=$(jq -er 'select(.verified == true) | .digest' dist/image.json)
cloud_run_repository=us-east1-docker.pkg.dev/montferret/playground/playground
cloud_run_reference="$cloud_run_repository:validation-${cloud_run_digest#sha256:}"
skopeo inspect --raw oci-archive:dist/playground.oci.tar > /tmp/playground-validation-manifest.json
test "$(skopeo manifest-digest /tmp/playground-validation-manifest.json)" = "$cloud_run_digest"
skopeo copy --preserve-digests oci-archive:dist/playground.oci.tar "docker://$cloud_run_reference"
skopeo inspect --raw "docker://$cloud_run_reference" > /tmp/playground-validation-remote.json
test "$(skopeo manifest-digest /tmp/playground-validation-remote.json)" = "$cloud_run_digest"
```

This does not move a full-version, minor or latest tag. Registry writes and later
publication remain separately authorized actions.

## Deploy the separate authenticated service

The command uses the image's single-container launcher, the standard gen2
runtime policy, and no Cloud Run preview features:

```sh
gcloud run deploy "$cloud_run_service" --project=montferret --region=us-east1 \
  --image="$cloud_run_repository@$cloud_run_digest" --execution-environment=gen2 \
  --command=/usr/bin/tini --args=--,/usr/bin/playground,container \
  --service-account=playground-runtime@montferret.iam.gserviceaccount.com \
  --cpu=2 --memory=2Gi --no-cpu-boost --concurrency=16 --timeout=30s \
  --min-instances=0 --max-instances=1 --port=8080 \
  --no-allow-unauthenticated --clear-secrets \
  --set-env-vars=PLAYGROUND_MAX_CONCURRENT_EXECUTIONS=2,PLAYGROUND_ALLOWED_ORIGINS=https://ferretlang.org,PLAYGROUND_SHUTDOWN_TIMEOUT=4s,PLAYGROUND_CLEANUP_TIMEOUT=3s \
  --startup-probe=httpGet.path=/api/v1/environment,httpGet.port=8080,periodSeconds=2,timeoutSeconds=2,failureThreshold=30
gcloud run services describe "$cloud_run_service" --project=montferret --region=us-east1 \
  --format=json > /tmp/playground-validation-service.json
cloud_run_revision=$(jq -er '.status.latestCreatedRevisionName' /tmp/playground-validation-service.json)
gcloud run revisions describe "$cloud_run_revision" --project=montferret --region=us-east1 \
  --format=json > /tmp/playground-validation-revision.json
jq -e --arg digest "$cloud_run_digest" '
  (.status.imageDigest | endswith($digest)) and
  any(.status.conditions[]; .type == "Ready" and .status == "True")
' /tmp/playground-validation-revision.json
cloud_run_url=$(jq -er '.status.url' /tmp/playground-validation-service.json)
```

Inspect the saved revision: exactly one container; gen2 execution; CPU/memory,
concurrency, runtime identity, image digest, port and probe match the command;
no inherited secret bindings, extra arguments, or isolation overrides. A startup
failure is a compatibility blocker. The launcher emits fixed codes such as
`namespace_start_failed`, `namespace_policy_unavailable`, and
`renderer_sandbox_unavailable`, without raw Chromium output.
Namespace and interface inspection logs include only a distinct-namespace
boolean, interface counts, loopback-only booleans and numeric errno. The parent
namespace descriptor is closed before helper or Chromium startup.

The [tagged revision procedure](https://docs.cloud.google.com/run/docs/rollouts-rollbacks-traffic-migration#deploying_new_tagged_revisions)
is available when a validation service already has a healthy baseline. The first
revision of the separate service does not use `--no-traffic`.

## Check authenticated HTTP behavior

Require anonymous invocation to return 401 or 403. Invoke the validation URL with a
short-lived identity token from the approved testing identity, keeping the token
out of logs. Verify all five endpoints and these minimum acceptance checks:

- `/environment` reports the intended service version, Ferret alpha.61, HTML
  rc.24, safe library and execution capacity two. Its successful startup proves
  the same artifact passed renderer and browser-namespace bypass probes before
  opening HTTP.
- Parameterized execution returns complete output; the checked-in product query
  works against HTTPS Mockery. Native download, file, metadata and direct-CDP
  submissions produce failed outcomes without output.
- CORS succeeds only for the configured origin. With one instance and two
  signaled long-running executions, discovery remains responsive and another
  execute receives prompt 429. Use an approved Mockery hold scenario; record a
  gap if no controlled scenario is available rather than substituting sleeps.
- Inspect Cloud Monitoring for peak memory/CPU and restarts. Save revision,
  source SHA, image/browser digests, limits, and observed timings with the result.

For example, keep the identity token on stdin to curl instead of its command line:

```sh
cloud_run_token=$(gcloud auth print-identity-token)
printf 'header = "Authorization: Bearer %s"\n' "$cloud_run_token" |
  curl --config - --fail --silent --show-error "$cloud_run_url/api/v1/environment"
unset cloud_run_token
```

## Observe shutdown and retain evidence

After completing requests, allow the separate minimum-zero candidate to scale
down. Do not direct production traffic to it. Inspect the revision's logs:

```sh
gcloud logging read \
  "resource.type=\"cloud_run_revision\" AND resource.labels.service_name=\"$cloud_run_service\" AND resource.labels.revision_name=\"$cloud_run_revision\" AND jsonPayload.msg=\"Container cleanup finished\"" \
  --project=montferret --freshness=1d --format='table(timestamp,jsonPayload.elapsedMs)'
```

Require paired cleanup-start/finish events below 9000 ms, successful process
termination and no forced-kill or memory-limit restart. Missing shutdown evidence
is an outstanding check. The local integration suite separately forces SIGTERM
during a fixture-signaled request and verifies graceful completion, cancellation,
child failure, reaping and exit below nine seconds. These local results must be
reported separately from observed Cloud Run results; natural idle scale-down
does not prove the active-request cancellation case on Cloud Run.

Export the service/revision configuration, invocation policy and logs before
deleting the temporary validation service. Retain the private immutable candidate
image reference and local OCI archive as evidence. Do not delete `playground`.

Record every missing or failed live case as a release blocker. Repeat this gate
when the browser base, namespace/policy launcher, or lifecycle changes.
Production traffic switching, public service
IAM, registry promotion and the website's Worker-payload migration are separate.

## Observed validation result

On 2026-11-03, authenticated service `playground-validation` was configured for
gen2, two vCPUs, 2 GiB memory, HTTP concurrency 16, execution capacity two, HTTP
startup probing, a four-second drain and three-second cleanup. Its runtime
identity was `playground-runtime@montferret.iam.gserviceaccount.com`, with no
project role grants. The private OCI archive was copied with digest preservation.

Revision `playground-validation-00001-xgd` ran manifest
`sha256:15b7a2685c1d67fbdba04a92dc3a90704aa32c2f46eb3adaac4b81278b0ff2fa`,
using Chromium 156.0.8078.80, Linux/amd64 browser manifest
`sha256:78c7fd698d088c639bc58221eb36565ce2c93a0b4f5af2cdf77fd3fb741eebf4`.
The locally verified candidate reported:

```json
{"distinct":true}
{"netlinkOK":true,"netlinkErrno":0,"netlinkInterfaceCount":3,"netlinkOnlyLoopback":false,"procfsOK":true,"procfsErrno":0,"procfsInterfaceCount":3,"procfsOnlyLoopback":false}
{"stage":"network_interface_layout_invalid","errno":0,"exitStatus":0}
```

The container exited with status 1 and initialization rollback finished in 1 ms.
Netlink is available; changing the enumeration source would not correct the
actual layout. The required loopback-only gate remains enforced. There is no
automatic interface removal, host-network fallback, extra outer capability or
disabled sandbox.

Nftables, renderer sandbox, steady-state resource usage, authenticated endpoint
behavior and serving shutdown were not reached on Cloud Run. They remain blocked
live checks, independently of passing Linux packaged tests. Production service
`playground` was not changed.
