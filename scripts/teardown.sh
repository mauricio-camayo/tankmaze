#!/usr/bin/env bash
# Tears down all TankMaze infrastructure from AWS.
# Resources with RemovalPolicy.RETAIN (DynamoDB tables, S3 buckets) are
# deleted explicitly after the CDK stacks are destroyed.
set -euo pipefail

AWS="/usr/local/bin/aws"
REGION="us-east-1"
ACCOUNT="897722684267"
CDK_QUALIFIER="hnb659fds"  # default CDK bootstrap qualifier

echo "=== TankMaze Teardown ==="
echo "Account : $ACCOUNT"
echo "Region  : $REGION"
echo ""
read -r -p "This will DELETE all TankMaze infrastructure. Type 'yes' to continue: " confirm
if [ "$confirm" != "yes" ]; then
  echo "Aborted."
  exit 0
fi

# ---- 1. Destroy CDK stacks (reverse dependency order) ----------------------

echo ""
echo ">>> Destroying CDK stacks..."
cd "$(dirname "$0")/../packages/infrastructure"

pnpm cdk destroy \
  TankmAzeFrontend \
  TankmAzeApi \
  TankmAzeBuild \
  TankmAzeStorage \
  TankmAzeAuth \
  TankmAzeGithubOidc \
  --context githubRepo=mauricio-camayo/tankmaze \
  --force

# ---- 2. Delete retained S3 buckets (must be emptied first) -----------------

BUCKETS=(
  "tankmazefrontend-sitebucket397a1860-zkikt38yihlv"
  "tankmaze-wasm-artifacts-$ACCOUNT"
  "tankmaze-match-logs-$ACCOUNT"
)

echo ""
echo ">>> Deleting retained S3 buckets..."
for bucket in "${BUCKETS[@]}"; do
  if $AWS s3api head-bucket --bucket "$bucket" --region "$REGION" 2>/dev/null; then
    echo "  Emptying s3://$bucket ..."
    $AWS s3 rm "s3://$bucket" --recursive --region "$REGION"
    # Remove any versioned objects and delete markers
    versions=$($AWS s3api list-object-versions --bucket "$bucket" --region "$REGION" \
      --query '{Objects: Versions[].{Key:Key,VersionId:VersionId}}' --output json 2>/dev/null || echo '{"Objects":null}')
    if [ "$(echo "$versions" | python3 -c 'import sys,json; d=json.load(sys.stdin); print(len(d["Objects"] or []))')" -gt 0 ]; then
      echo "$versions" | $AWS s3api delete-objects --bucket "$bucket" --region "$REGION" --delete file:///dev/stdin
    fi
    markers=$($AWS s3api list-object-versions --bucket "$bucket" --region "$REGION" \
      --query '{Objects: DeleteMarkers[].{Key:Key,VersionId:VersionId}}' --output json 2>/dev/null || echo '{"Objects":null}')
    if [ "$(echo "$markers" | python3 -c 'import sys,json; d=json.load(sys.stdin); print(len(d["Objects"] or []))')" -gt 0 ]; then
      echo "$markers" | $AWS s3api delete-objects --bucket "$bucket" --region "$REGION" --delete file:///dev/stdin
    fi
    echo "  Deleting s3://$bucket ..."
    $AWS s3api delete-bucket --bucket "$bucket" --region "$REGION"
    echo "  Deleted."
  else
    echo "  s3://$bucket not found — skipping."
  fi
done

# ---- 3. Delete retained DynamoDB tables ------------------------------------

TABLES=(
  "tankmaze-tanks"
  "tankmaze-tank-versions"
  "tankmaze-matches"
  "tankmaze-connections"
  "tankmaze-gamedays"
  "tankmaze-rankings"
  "tankmaze-maps"
)

echo ""
echo ">>> Deleting retained DynamoDB tables..."
for table in "${TABLES[@]}"; do
  if $AWS dynamodb describe-table --table-name "$table" --region "$REGION" 2>/dev/null | grep -q TableName; then
    echo "  Deleting $table ..."
    $AWS dynamodb delete-table --table-name "$table" --region "$REGION" --output text --query 'TableDescription.TableStatus'
    echo "  Deleted."
  else
    echo "  $table not found — skipping."
  fi
done

# ---- 4. Tear down CDK bootstrap (CDKToolkit stack + staging bucket) ---------

echo ""
read -r -p "Also destroy CDK bootstrap (CDKToolkit stack)? [y/N] " bootstrap_confirm
if [ "$bootstrap_confirm" = "y" ] || [ "$bootstrap_confirm" = "Y" ]; then
  CDK_BUCKET="cdk-$CDK_QUALIFIER-assets-$ACCOUNT-$REGION"
  echo ">>> Emptying CDK staging bucket s3://$CDK_BUCKET ..."
  if $AWS s3api head-bucket --bucket "$CDK_BUCKET" --region "$REGION" 2>/dev/null; then
    $AWS s3 rm "s3://$CDK_BUCKET" --recursive --region "$REGION"
    $AWS s3api delete-bucket --bucket "$CDK_BUCKET" --region "$REGION"
  fi

  echo ">>> Deleting CDKToolkit stack..."
  $AWS cloudformation delete-stack --stack-name CDKToolkit --region "$REGION"
  $AWS cloudformation wait stack-delete-complete --stack-name CDKToolkit --region "$REGION"
  echo ">>> CDKToolkit deleted."
fi

echo ""
echo "=== Teardown complete ==="
