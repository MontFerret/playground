# Cloud Run compatibility and no-traffic validation

The deployment target is one Linux/amd64 container on standard second-generation
Cloud Run. No sidecars, separate browser service, Sandboxes preview, added outer
capabilities, or disabled Chromium sandbox are part of this design.

Local Docker verification is complete only for the tested Linux host. **Real
Cloud Run compatibility is not yet verified and blocks release.** In particular,
Cloud Run must allow the new user/network namespace and namespace-local nftables
under its standard policy. Failure leaves HTTP unopened and logs a fixed stage
code. Do not bypass the check or weaken isolation to make a revision ready.

The [execution environment](https://docs.cloud.google.com/run/docs/configuring/execution-environments)
documents namespace support, while the [runtime contract](https://docs.cloud.google.com/run/docs/container-contract)
restricts privileges and grants ten seconds after SIGTERM. Those documents are
not evidence that this image passed its checks on Cloud Run.

## Prerequisites

Perform this procedure only as a separately approved cloud operation. It writes
a private validation image reference and creates a zero-traffic revision. Use an
existing authenticated validation service with an approved baseline revision;
provisioning its first revision and invocation IAM is a separate setup step.

Use project `montferret`, region `us-east1`, and a dedicated runtime service
account `playground-runtime@montferret.iam.gserviceaccount.com` with no project
roles or Google API permissions. The platform's image-pull service agent needs
repository read access. The deploying identity needs Cloud Run deployment and
service-account-use permissions; the testing identity needs Cloud Run Invoker.
The release service account and GitHub credentials are never attached to the
runtime container.

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

## Deploy a tagged revision with zero traffic

The command uses the image's single-container launcher, the standard gen2
runtime policy, and no Cloud Run preview features:

```sh
gcloud run deploy "$cloud_run_service" --project=montferret --region=us-east1 \
  --image="$cloud_run_repository@$cloud_run_digest" --execution-environment=gen2 \
  --command=/usr/bin/tini --args=--,/usr/bin/playground,container \
  --service-account=playground-runtime@montferret.iam.gserviceaccount.com \
  --cpu=2 --memory=2Gi --no-cpu-boost --concurrency=16 --timeout=30s \
  --min-instances=0 --max-instances=1 --port=8080 \
  --no-traffic --tag=validation --no-allow-unauthenticated --clear-secrets \
  --set-env-vars=PLAYGROUND_MAX_CONCURRENT_EXECUTIONS=2,PLAYGROUND_ALLOWED_ORIGINS=https://ferretlang.org,PLAYGROUND_SHUTDOWN_TIMEOUT=4s,PLAYGROUND_CLEANUP_TIMEOUT=3s \
  --startup-probe=httpGet.path=/api/v1/environment,httpGet.port=8080,periodSeconds=2,timeoutSeconds=2,failureThreshold=30
gcloud run services describe "$cloud_run_service" --project=montferret --region=us-east1 \
  --format=json > /tmp/playground-validation-service.json
cloud_run_revision=$(jq -er '.status.traffic[] | select(.tag == "validation") | .revisionName' /tmp/playground-validation-service.json)
cloud_run_url=$(jq -er '.status.traffic[] | select(.tag == "validation") | .url' /tmp/playground-validation-service.json)
gcloud run revisions describe "$cloud_run_revision" --project=montferret --region=us-east1 \
  --format=json > /tmp/playground-validation-revision.json
jq -e --arg digest "$cloud_run_digest" '
  (.status.imageDigest | endswith($digest)) and
  any(.status.conditions[]; .type == "Ready" and .status == "True")
' /tmp/playground-validation-revision.json
jq -e --arg revision "$cloud_run_revision" '
  [.status.traffic[] | select(.revisionName == $revision) | (.percent // 0)] | all(. == 0)
' /tmp/playground-validation-service.json
```

Inspect the saved revision: exactly one container; gen2 execution; CPU/memory,
concurrency, runtime identity, image digest, port and probe match the command;
no inherited secret bindings, extra arguments, or isolation overrides. A startup
failure is a compatibility blocker. The launcher emits fixed codes such as
`namespace_start_failed`, `namespace_policy_unavailable`, and
`renderer_sandbox_unavailable`, without raw Chromium output.

The [tagged revision procedure](https://docs.cloud.google.com/run/docs/rollouts-rollbacks-traffic-migration#deploying_new_tagged_revisions)
allows authenticated testing without changing the serving revision's traffic.

## Check authenticated HTTP behavior

Require anonymous invocation to return 401 or 403. Invoke the tagged URL with a
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

After completing requests, allow the zero-traffic, minimum-zero candidate to
scale down. Do not direct production traffic to it. Inspect the revision's logs:

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

Record every missing or failed live case as a release blocker. Repeat this gate
when the browser base, namespace/policy launcher, or lifecycle changes. Remove
the validation tag/revision and private validation image reference only through
the approved cleanup operation. Production traffic switching, public service
IAM, registry promotion and the website's Worker-payload migration are separate.
