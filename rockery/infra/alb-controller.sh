#!/usr/bin/env bash
# Install the AWS Load Balancer Controller so the Ingress can create an ALB.
# Prereqs: cluster created with `iam.withOIDC: true`, plus aws, eksctl, kubectl, helm, curl.
set -euo pipefail

CLUSTER="${CLUSTER_NAME:-rockery-demo}"
REGION="${AWS_REGION:-us-west-2}"
ACCOUNT_ID="$(aws sts get-caller-identity --query Account --output text)"
POLICY_NAME="AWSLoadBalancerControllerIAMPolicy"
POLICY_ARN="arn:aws:iam::${ACCOUNT_ID}:policy/${POLICY_NAME}"
# Pin the controller version whose IAM policy we download; bump deliberately.
LBC_VERSION="${LBC_VERSION:-v2.13.0}"

if ! aws iam get-policy --policy-arn "$POLICY_ARN" >/dev/null 2>&1; then
  curl -fsSLo /tmp/alb-iam-policy.json \
    "https://raw.githubusercontent.com/kubernetes-sigs/aws-load-balancer-controller/${LBC_VERSION}/docs/install/iam_policy.json"
  aws iam create-policy --policy-name "$POLICY_NAME" --policy-document file:///tmp/alb-iam-policy.json >/dev/null
  echo "Created IAM policy $POLICY_NAME"
fi

eksctl create iamserviceaccount \
  --cluster "$CLUSTER" --region "$REGION" \
  --namespace kube-system --name aws-load-balancer-controller \
  --attach-policy-arn "$POLICY_ARN" \
  --override-existing-serviceaccounts --approve

helm repo add eks https://aws.github.io/eks-charts >/dev/null
helm repo update >/dev/null
helm upgrade --install aws-load-balancer-controller eks/aws-load-balancer-controller \
  --namespace kube-system \
  --set clusterName="$CLUSTER" \
  --set serviceAccount.create=false \
  --set serviceAccount.name=aws-load-balancer-controller \
  --wait

echo "AWS Load Balancer Controller is installed."
