# 🪨 Rockery: the Pet Rock Adoption Agency

A production-shaped Go microservice built to be **deployed to EKS with Helm** and **published to Amazon ECR**,
with the CI/CD pipeline built in **Harness**. The domain is silly on purpose; the plumbing is the point.

**Stack:** Go 1.24 (stdlib only) · Docker (distroless, non-root) · Helm chart (per-environment and per-cluster values) ·
Amazon ECR · EKS (`eksctl`)

> Building the pipeline? Start with **[HARNESS.md](HARNESS.md)**: the exact repo entry points a pipeline uses,
> the CI -> CD artifact handoff, and step-by-step stage layouts.

## Layout

```
main.go, main_test.go        service + tests
Dockerfile                   multi-stage -> distroless nonroot (build args: VERSION, COMMIT)
VERSION                      default version for the semver tag strategy
scripts/image-tag.sh         computes + validates image tags (git_sha | semver | git_tag | existing)
scripts/smoke-test.sh        post-deploy check against any URL
charts/rockery/              the Helm chart
  values.yaml                safe base defaults
  values.schema.json         validates inputs: env tier, image ref/tag required, rejects :latest
  values/{dev,preprod,prod}.yaml        per-environment overrides
  values/clusters/<cluster>.yaml        per-cluster overrides + rollout group
  templates/                 deployment, service, hpa, pdb, ingress, serviceaccount, (opt-in) test pod
infra/                       eksctl cluster config, ECR bootstrap, ALB controller install
Makefile                     local developer convenience only (the pipeline does not need it)
HARNESS.md                   how to wire this repo into a Harness pipeline
```

Values layer left to right, later wins:
`values.yaml` -> `values/<env>.yaml` -> `values/clusters/<cluster>.yaml` -> the image override from the pipeline.

## API

| Method | Path | |
|---|---|---|
| GET / POST | `/rocks` | List (`?available=true`) / add |
| GET | `/rocks/{id}` · `/rocks/{id}/mood` | One rock / its mood |
| POST | `/rocks/{id}/adopt` | `{"adopter":"you"}`; 409 if taken |
| GET | `/healthz` · `/readyz` | Liveness · readiness (fails during shutdown drain) |
| GET | `/metrics` | Prometheus |
| GET | `/version` | version, commit, **environment, cluster, cluster_group**, pod |

## Image tags

One script, used by the Makefile and by a Harness Run step. Inputs are validated and `latest` is refused.

| Strategy | Example | Result |
|---|---|---|
| `git_sha` | `scripts/image-tag.sh git_sha` | short SHA of HEAD (or `$GIT_SHA`) |
| `semver` | `scripts/image-tag.sh semver v1.4.2` | `1.4.2`; strict MAJOR.MINOR.PATCH[-pre]; reads `VERSION` if no value |
| `git_tag` | `scripts/image-tag.sh git_tag` | the git tag on HEAD (or `$GIT_TAG`); fails if none |
| `existing` | `scripts/image-tag.sh existing 3f9c2ab` | validates an already-built tag, for promotion |

## The image contract (what CI hands to CD)

The chart takes one immutable image, either way:

```bash
--set image.reference=123456789012.dkr.ecr.us-west-2.amazonaws.com/rockery:3f9c2ab     # what Harness passes
--set image.repository=123456789012.dkr.ecr.us-west-2.amazonaws.com/rockery --set image.tag=3f9c2ab   # what make passes
```

## Run it locally

Prerequisites: `go` 1.24+, `docker` (buildx), `aws` CLI, `eksctl`, `kubectl`, `helm` 3.x.

```bash
export AWS_REGION=us-west-2

make test helm-lint        # Go tests (race detector) + lint/render the chart for every env and cluster
make run                   # service on :8080;  ./scripts/smoke-test.sh http://localhost:8080

make ecr-create            # one-time: ECR repo (immutable tags, scan on push)
make cluster-create        # one-time, ~15-20 min
make cluster-addons        # one-time: AWS Load Balancer Controller

make push                  # build + push to ECR (TAG_STRATEGY=git_sha|semver|git_tag)
make deploy ENV=dev        # helm upgrade --install --atomic, then helm test
make deploy ENV=prod CLUSTER=example-prod-use1-a TAG_STRATEGY=existing TAG_VALUE=3f9c2ab
make status | history | rollback
```

Tear down: `make cluster-delete` (uninstalls the release first so the ALB goes away). The cluster costs real money.

### Clusters and rollout groups

To add a cluster, copy `values/clusters/example-prod-use1-a.yaml` and set `cluster.name` and `cluster.group`
(`canary`, `wave-1`, ...). The group is stamped on every pod as a label so a pipeline can roll out one group at a time.
Production clusters do not each need their own chart or pipeline, only a small values file.

### What you can see on a running deployment

- Pods carry `app.kubernetes.io/version` (the image tag), environment, cluster and group labels.
- Optional annotations record the deploy run id, who triggered it, and the git SHA (`deploy.*` values).
- `GET /version` answers "what is this pod running?" without cluster access.

## How this maps to the POV success criteria

Argo CD / GitOps items are out of scope. Many criteria are Harness platform features that an app repo cannot
satisfy; this covers only what the service and repo influence.

| Criterion | Status |
|---|---|
| Build and publish container images to Amazon ECR | **Repo ready**: Dockerfile, `infra/ecr.sh`, `make push`; the Harness *Build and Push to ECR* step is configured in Harness (see HARNESS.md) |
| Support externally built images (no hard dependency on Harness CI) | **Done**: the chart needs only an image reference, however it was built |
| Multiple tagging strategies with required, validated inputs | **Done**: `scripts/image-tag.sh` + chart schema |
| CI output usable as input to CD (artifact handoff) | **Done**: one value, `image.reference` |
| Single reusable pipeline template supports all tagging strategies | **Repo ready**: one script takes the strategy as an argument; the Harness template is yet to be built |
| Helm-based configuration with per-environment overrides | **Done**: `values/<env>.yaml`, `values/clusters/*.yaml` |
| Pipeline templates support dev/preprod/prod tiers | **Chart side done** (environment enum); the Harness templates are yet to be built |
| Fan-out to up to 29 EKS clusters, groupable | **Chart side done** (cluster + group values and labels); orchestration is Harness's multi-environment deployment |
| Automated rollback on failure or failed health checks | **Chart side done** (probes, zero-unavailable rolling update, PDB); the failure strategy is set in Harness |
| Rollback without a new pipeline run | Helm-level via `make rollback`; the Harness UI rollback is Harness-side |
| Clear visibility on what is deployed | **Done**: labels, `/version`, `make status` |
| Auditable direct deployment | **Cluster side done** (run id / actor / SHA annotations); Harness adds its own audit trail |
| Drift detection | **Limitation**: Helm does not detect live drift; use Harness drift detection or the `helm diff` plugin |
| Onboarding a new service without building pipelines from scratch | **Partly**: this chart and contract are copyable; real template-first onboarding needs Harness templates |
| Everything else (IaC, secrets, RBAC/SSO, STO, approvals, scale, API/MCP, ops) | Not applicable to the service repo |

## Known limits (deliberate)

- **Data is in memory.** Each replica has its own rocks. Fine for demonstrating deploys; back `Store` with DynamoDB/RDS for real use.
- **HTTP only** on the ALB until you add an ACM certificate (see `ingress.annotations` in `values.yaml`).
- The Makefile pins to **Helm 3** (`--atomic`); Helm 4 renames it `--rollback-on-failure`.
- Not run end-to-end against real AWS/EKS or Harness. Validated with Go tests, `helm lint`, and render/schema checks for every environment.
