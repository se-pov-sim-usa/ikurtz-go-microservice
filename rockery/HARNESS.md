# Building the Harness pipeline for Rockery

What the repo gives a Harness pipeline, and what you configure on the Harness side. The repo has no
pipeline definition, on purpose: it only has to expose a few stable, pipeline-agnostic entry points.

## The contract: what Harness consumes from this repo

| Need | Where | How a pipeline uses it |
|---|---|---|
| Run unit tests | `go test -race ./...` | CI **Run** step. Needs Go 1.24 (use a `golang:1.24` image) |
| Lint the chart for all tiers | `make helm-lint` (or the `helm lint` loop in the Makefile) | CI **Run** step, image with `helm` |
| Compute + validate the image tag | `scripts/image-tag.sh <git_sha\|semver\|git_tag\|existing> [value]` | CI **Run** step; exposes the tag as an output variable. Honors `GIT_SHA` / `GIT_TAG` env, since CI checkouts are often shallow |
| Build the image | `Dockerfile` (repo root, context `.`) | CI **Build and Push to ECR** step. Build args: `VERSION` (the tag) and `COMMIT` (the SHA) |
| Deploy to Kubernetes | `charts/rockery` (Helm chart) | CD **Service**: Helm chart manifest from this Git repo, chart path `charts/rockery` |
| Per-environment config | `charts/rockery/values/{dev,preprod,prod}.yaml` | CD Service **values files**, selected by environment |
| Per-cluster config / rollout group | `charts/rockery/values/clusters/<cluster>.yaml` | Optional extra values file for each target cluster (sets `cluster.name` and `cluster.group`) |
| Verify a deployment | `GET /healthz`, `/readyz`, `/version`, `scripts/smoke-test.sh <url>` | CD **Shell Script** / HTTP step after rollout |
| Rollback | Helm release history | Harness rollback, or a failure strategy of Rollback |

## The artifact handoff (CI -> CD)

CI produces **one thing**: an image in ECR with an immutable tag. The chart wants it as a single value:

```yaml
# Values override in the Harness service (inline or a file), resolved by Harness before Helm runs.
image:
  reference: <+artifacts.primary.image>      # full path incl. registry host and tag
```

Per the [Harness artifact docs](https://developer.harness.io/docs/continuous-delivery/deploy-srv-diff-platforms/kubernetes/cd-kubernetes-category/add-artifacts-for-kubernetes-deployments),
`<+artifacts.primary.image>` resolves to the registry host + path + tag, while `imagePath` drops the host, which is
why the chart takes a full `image.reference` rather than assembling one from pieces. The chart's schema
rejects a reference with no tag or digest, and rejects `:latest`.

Audit stamps are optional pod annotations; fill them from Harness expressions so each pod records the run that produced it:

```yaml
deploy:
  runId: <+pipeline.executionId>
  triggeredBy: <+pipeline.triggeredBy.name>
  gitSha: <+codebase.commitSha>      # only if the pipeline has a codebase
```

Check these expression names against your Harness version before relying on them.

## CI stage, step by step

1. **Run: test.** `go test -race -count=1 ./...`
2. **Run: lint chart.** `make helm-lint`
3. **Run: compute tag.** Declare an output variable, e.g. `TAG`:
   ```bash
   # git_sha strategy (default)
   export GIT_SHA=<+codebase.commitSha>
   TAG="$(./scripts/image-tag.sh git_sha)"
   # release builds from a git tag: TAG="$(./scripts/image-tag.sh semver "<+codebase.tag>")"
   ```
   Run it as a pipeline choice (runtime input `tagStrategy`: `git_sha | semver | git_tag`) to support all strategies in
   one reusable template. The script fails the step on malformed input and on `latest`.
4. **Build and Push to ECR.** Per the [step settings](https://developer.harness.io/docs/continuous-integration/use-ci/build-and-upload-artifacts/build-and-push/build-and-push-to-ecr-step-settings):
   AWS connector, **Region**, **Account Id**, **Image Name** `rockery`, **Tags** the output variable from step 3
   (`<+steps.<step_id>.output.outputVariables.TAG>`), **Build Arguments** `VERSION` = the tag, `COMMIT` = the SHA.
   Create the repo first with `infra/ecr.sh` (immutable tags, scan on push).

## CD stage, step by step

1. **Service** (type: Kubernetes, manifest type: Helm Chart from Git, repo = this repo, chart path `charts/rockery`).
   Values files: `charts/rockery/values/<+env.name>.yaml`, plus the per-cluster file when fanning out.
   Artifact source: **Amazon ECR**, repo `rockery`, tag as a runtime input (or the CI output).
   Add the `image.reference` override above.
2. **Environment + Infrastructure:** one environment per tier (`dev`, `preprod`, `prod`; the name must match a
   values file, and the chart's schema only allows those three). The infrastructure definition points at the EKS cluster
   and namespace `rockery` through a Kubernetes connector (via the delegate).
3. **Rollout:** the chart already has readiness/startup probes, a rolling update with `maxUnavailable: 0`,
   a PodDisruptionBudget and an HPA. Add a failure strategy of **Rollback** on the deploy step.
4. **Verify:** a Shell Script or HTTP step against `/readyz` and `/version` (the latter returns the deployed
   version, environment and cluster, so the step can assert it deployed what it meant to).
5. **Promotion:** pass the same tag from `dev` to `preprod` to `prod` (no rebuild); add manual approvals between tiers.

## Cluster prerequisites (not in the chart)

- **Namespace** `rockery`, ideally labeled `pod-security.kubernetes.io/enforce=restricted` (the workload complies).
  Let the Harness infrastructure definition create it, or create it ahead of time.
- **AWS Load Balancer Controller** (`infra/alb-controller.sh`) if you turn on the ALB ingress (`preprod`/`prod` do).
- **metrics-server** for the HPA (the `eksctl` config installs it).
- Delegate connectivity to the cluster API, and nodes that can pull from ECR.

## Harness-side setup (maps to the POV "Connectors" sheet)

- **Delegate** installed in/near the cluster (the Terraform project covers this).
- **GitHub** connector to the repo, **AWS** connector for ECR, **Kubernetes** connector for the cluster.
- Everything above is created in Harness; none of it exists in this repo.

## Notes

- **`helm test` hook:** the chart's test pod is **off** by default (`tests.enabled: false`) so a pipeline that renders
  the chart with `helm template` never applies it as a normal Pod. Enable it only where Helm itself runs the release
  (`make deploy` does).
- **Deployment type:** Harness can deploy a Helm chart as a Kubernetes service (it renders and applies manifests and manages
  its own release tracking) or as a Native Helm service. The chart works with either; Helm release history and
  `helm rollback` only exist with Native Helm or the Makefile path.
