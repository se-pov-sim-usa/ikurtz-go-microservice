#!/usr/bin/env bash
# Idempotently create the ECR repository with scan-on-push and a lifecycle policy.
set -euo pipefail

REGION="${AWS_REGION:-us-west-2}"
REPO="${ECR_REPO:-rockery}"

if aws ecr describe-repositories --repository-names "$REPO" --region "$REGION" >/dev/null 2>&1; then
  echo "ECR repo '$REPO' already exists in $REGION"
else
  aws ecr create-repository \
    --repository-name "$REPO" \
    --region "$REGION" \
    --image-scanning-configuration scanOnPush=true \
    --image-tag-mutability IMMUTABLE >/dev/null
  echo "Created ECR repo '$REPO' in $REGION"
fi

aws ecr put-lifecycle-policy --repository-name "$REPO" --region "$REGION" --lifecycle-policy-text '{
  "rules": [{
    "rulePriority": 1,
    "description": "Keep the last 30 images",
    "selection": { "tagStatus": "any", "countType": "imageCountMoreThan", "countNumber": 30 },
    "action": { "type": "expire" }
  }]
}' >/dev/null
echo "Lifecycle policy applied"
