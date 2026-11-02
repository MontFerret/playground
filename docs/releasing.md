# Release setup and recovery

GoReleaser v2.19.0 owns the service version, binary, archive, checksums, image
destinations and alias policy. Its post-build hook builds the Linux/amd64 release
image once, exports an OCI archive, imports that archive, and runs the packaged
integration suite with API builds disabled. Publication requires the resulting
verified descriptor and copies the existing archive with digest preservation.

The release workflow runs on `v*` tags and serializes registry publication.
It creates a draft GitHub release and retains the OCI archive and descriptor
as workflow artifacts even after failure. The production API image has exactly
these two destinations.

| Registry | Image | Access |
|---|---|---|
| GHCR | `ghcr.io/montferret/playground` | Public, anonymous pull |
| Artifact Registry | `us-east1-docker.pkg.dev/montferret/playground/playground` | Private, authenticated pull |

A tag `v1.2.3-rc.1` publishes `1.2.3-rc.1` to both destinations. A stable
`v1.2.3` publishes `1.2.3`, verifies both digests and visibility, then moves
`1.2` and `latest` to that digest. Existing conflicting full-version tags
cause failure. A network or authentication failure during inspection also
causes failure without overwriting the full version.

## Credential-free verification

Install Go as declared in go.mod, Docker Compose v2.24.4 or later, Python 3,
OpenSSL, curl, jq, Skopeo, and GoReleaser v2.19.0. Use native Linux/amd64 with working
unprivileged user namespaces and Chromium's nested seccomp sandbox.

```sh
make check
make integration
make release-snapshot
```

Snapshots build the same release entry point and runtime configuration, and
require neither registry credentials nor public test websites. The browser,
base images, Go modules and integration images must be cached for offline use.
Snapshot descriptors cannot be published by the release publisher.

## First-release settings

Create the GitHub `release` environment. Permit deployment from release tags
only and restrict tag creation to maintainers. The workflow reads these values
from the GitHub `secrets` context. The existing organization secrets must grant
MontFerret/playground access; repository or `release` environment secrets with
the same names are also supported.

| Name | Value |
|---|---|
| `GCP_WORKLOAD_IDENTITY_PROVIDER` | Full Google WIF provider resource name |
| `GCP_SERVICE_ACCOUNT_EMAIL` | Dedicated Playground release service account |

The job receives only GitHub's automatic GITHUB_TOKEN for GHCR. Allow Actions
to publish packages and create draft releases. The image source label links
the package to MontFerret/playground. After the first successful full-version
push, change the GHCR playground package visibility to public in package
settings. Publication stops before stable aliases until anonymous access works.
Use an initial prerelease and the recovery procedure to complete this gate.
If GHCR denies a pull-token lookup for a new package, the publisher confirms
package absence through GitHub's API using GITHUB_TOKEN before creating it.
An existing inaccessible package still causes a failure.

Provision a private standard Docker Artifact Registry repository with mutable
tags so stable aliases can move. Run the following as a project administrator,
substituting the organization-owned identity pool and service account names.

```sh
gcloud services enable artifactregistry.googleapis.com iamcredentials.googleapis.com sts.googleapis.com --project=montferret
gcloud artifacts repositories create playground --project=montferret --location=us-east1 \
  --repository-format=docker --description='Ferret Playground API'
gcloud iam service-accounts create playground-release --project=montferret
gcloud artifacts repositories add-iam-policy-binding playground \
  --project=montferret --location=us-east1 \
  --member=serviceAccount:playground-release@montferret.iam.gserviceaccount.com \
  --role=roles/artifactregistry.writer
```

Do not grant `allUsers` or `allAuthenticatedUsers` repository access.
Grant the service account Writer only on this repository. Its Google identity
must have no project-wide registry write role. Production readers receive
repository-scoped Artifact Registry Reader separately.

Create an OIDC WIF provider with issuer `https://token.actions.githubusercontent.com`.
Map `google.subject=assertion.sub`,
`attribute.repository=assertion.repository`, and
`attribute.ref=assertion.ref`. Restrict its attribute condition to
`assertion.repository == 'MontFerret/playground' && assertion.ref.startsWith('refs/tags/v')`.
Bind the intended repository principal to the service account with
`roles/iam.workloadIdentityUser`.

```sh
project_number=$(gcloud projects describe montferret --format='value(projectNumber)')
gcloud iam workload-identity-pools create playground-github --project=montferret --location=global
gcloud iam workload-identity-pools providers create-oidc playground \
  --project=montferret --location=global --workload-identity-pool=playground-github \
  --issuer-uri=https://token.actions.githubusercontent.com \
  --attribute-mapping='google.subject=assertion.sub,attribute.repository=assertion.repository,attribute.ref=assertion.ref' \
  --attribute-condition="assertion.repository == 'MontFerret/playground' && assertion.ref.startsWith('refs/tags/v')"
gcloud iam service-accounts add-iam-policy-binding \
  playground-release@montferret.iam.gserviceaccount.com --project=montferret \
  --role=roles/iam.workloadIdentityUser \
  --member="principalSet://iam.googleapis.com/projects/$project_number/locations/global/workloadIdentityPools/playground-github/attribute.repository/MontFerret/playground"
```

The provider resource name is
`projects/<project-number>/locations/global/workloadIdentityPools/playground-github/providers/playground`.
Store that value and the service account email in the corresponding GitHub
secrets. The workflow uses an access token and creates no service-account key
file.

Google provisioning and GHCR package visibility are first-release operations.
They have not been verified against a real release in this change.
[Google access control](https://docs.cloud.google.com/artifact-registry/docs/access-control)
and [GitHub package visibility](https://docs.github.com/en/packages/learn-github-packages/configuring-a-packages-access-control-and-visibility)
describe the owning settings.

## Verify pulls and identity

After setting the intended version, these commands verify the actual registry
artifacts. Skopeo's no-creds flags force anonymous access.

```sh
version=1.2.3-rc.1
public=ghcr.io/montferret/playground
private=us-east1-docker.pkg.dev/montferret/playground/playground
pull_dir=$(mktemp -d)
skopeo copy --src-no-creds "docker://$public:$version" "dir:$pull_dir/public"
gcloud auth print-access-token |
  skopeo login --username oauth2accesstoken --password-stdin us-east1-docker.pkg.dev
skopeo copy "docker://$private:$version" "dir:$pull_dir/private"
if skopeo copy --src-no-creds "docker://$private:$version" "dir:$pull_dir/anonymous"; then
  echo 'Unexpected anonymous Google access' >&2
  exit 1
fi
# Confirm that the failure above is 401/403/permission denial, not a network error.
skopeo inspect --raw "docker://$public:$version" > "$pull_dir/public-manifest"
skopeo inspect --raw "docker://$private:$version" > "$pull_dir/private-manifest"
test "$(skopeo manifest-digest "$pull_dir/public-manifest")" = \
     "$(skopeo manifest-digest "$pull_dir/private-manifest")"
```

## Recover a partial publication

Recover the `playground-image-<tag>` workflow artifact into `dist/` so it
contains `playground.oci.tar` and `image.json`. Check that the descriptor has
`verified: true` and comes from the intended trusted tag job. Fix the failed
authentication, permissions, visibility or registry availability first.

```sh
# Authenticate with a maintainer-approved GHCR token and a Google identity with
# repository-scoped access, or run these steps in the existing trusted release job.
skopeo login ghcr.io
gcloud auth print-access-token |
  skopeo login --username oauth2accesstoken --password-stdin us-east1-docker.pkg.dev
bash scripts/release/publish.sh
```

Recovery verifies the local archive digest, skips matching full-version tags,
publishes to the missing destination, and repeats digest and access checks.
It advances stable aliases only after both full-version references match.
The same command completes a failed alias update. It never builds an image.
