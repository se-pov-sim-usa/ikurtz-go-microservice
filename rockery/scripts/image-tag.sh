#!/usr/bin/env bash
# Compute and VALIDATE the image tag for a build. Prints the tag on stdout; exits non-zero with a
# clear message if the input is missing or malformed. One script, used by Make, CI, and any pipeline,
# so every tagging strategy behaves identically everywhere.
#
#   scripts/image-tag.sh git_sha                 -> 3f9c2ab         (short SHA of HEAD)
#   scripts/image-tag.sh semver 1.4.2            -> 1.4.2           (explicit version, or read from ./VERSION)
#   scripts/image-tag.sh git_tag                 -> v1.4.2          (the tag pointing at HEAD; fails if none)
#   scripts/image-tag.sh existing 3f9c2ab        -> 3f9c2ab         (validate an already-built tag, for deploy/promotion)
#
# Env overrides (useful in CI where checkouts are shallow): GIT_SHA, GIT_TAG.
set -euo pipefail

strategy="${1:-}"
value="${2:-}"

die() { echo "image-tag: $*" >&2; exit 1; }

# Docker/ECR tag rules: <=128 chars, [A-Za-z0-9_.-], not starting with . or -
valid_tag() { [[ "$1" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$ ]]; }

case "$strategy" in
  git_sha)
    sha="${GIT_SHA:-$(git rev-parse HEAD 2>/dev/null || true)}"
    [[ "$sha" =~ ^[0-9a-f]{7,40}$ ]] || die "git_sha: could not determine a commit SHA (is this a git checkout?)"
    tag="${sha:0:7}"
    ;;
  semver)
    if [[ -z "$value" && -f VERSION ]]; then value="$(tr -d '[:space:]' < VERSION)"; fi
    [[ -n "$value" ]] || die "semver: pass a version (scripts/image-tag.sh semver 1.2.3) or create a VERSION file"
    value="${value#v}"
    [[ "$value" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$ ]] \
      || die "semver: '$value' is not valid semantic version (expected MAJOR.MINOR.PATCH[-prerelease])"
    tag="$value"
    ;;
  git_tag)
    t="${GIT_TAG:-$(git describe --tags --exact-match HEAD 2>/dev/null || true)}"
    [[ -n "$t" ]] || die "git_tag: HEAD has no git tag. Tag the commit (git tag v1.2.3) or use another strategy"
    tag="$t"
    ;;
  existing)
    # Deploy-time: validate a tag that was already built and pushed (e.g. promoting dev's tag to prod).
    [[ -n "$value" ]] || die "existing: pass the tag, e.g. scripts/image-tag.sh existing 3f9c2ab"
    tag="$value"
    ;;
  *)
    die "usage: image-tag.sh <git_sha|semver|git_tag|existing> [version-or-tag]"
    ;;
esac

valid_tag "$tag" || die "'$tag' is not a valid image tag"
[[ "$tag" != "latest" ]] || die "'latest' is not allowed: tags must be immutable"
echo "$tag"
