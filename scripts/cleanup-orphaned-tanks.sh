#!/usr/bin/env bash
# Deletes tankmaze-tanks and tankmaze-tank-versions records whose userId
# no longer exists in the current Cognito UserPool.
#
# Usage: USER_POOL_ID=us-east-1_xxx bash cleanup-orphaned-tanks.sh
#        Pass --dry-run to preview without deleting.

set -euo pipefail

DRY_RUN=false
for arg in "$@"; do [[ "$arg" == "--dry-run" ]] && DRY_RUN=true; done

POOL_ID="${USER_POOL_ID:?Set USER_POOL_ID=us-east-1_xxx}"
TANKS_TABLE="tankmaze-tanks"
VERSIONS_TABLE="tankmaze-tank-versions"

echo "==> Fetching Cognito users from pool $POOL_ID …"
VALID_SUBS=$(aws cognito-idp list-users \
  --user-pool-id "$POOL_ID" \
  --query "Users[*].Attributes[?Name=='sub'].Value" \
  --output text)

if [[ -z "$VALID_SUBS" ]]; then
  echo "ERROR: No users found. Check USER_POOL_ID and AWS credentials."
  exit 1
fi

echo "Found $(echo "$VALID_SUBS" | wc -w | tr -d ' ') valid users."

echo ""
echo "==> Scanning tankmaze-tanks …"
TANKS_JSON=$(aws dynamodb scan \
  --table-name "$TANKS_TABLE" \
  --projection-expression "tankId, userId" \
  --output json)

ORPHAN_COUNT=0

echo "$TANKS_JSON" | python3 - <<'PYEOF'
import json, subprocess, sys, os

data     = json.loads(sys.stdin.read())
items    = data.get("Items", [])
valid    = set(os.environ["VALID_SUBS"].split())
dry_run  = os.environ.get("DRY_RUN") == "true"
tanks_tbl    = os.environ["TANKS_TABLE"]
versions_tbl = os.environ["VERSIONS_TABLE"]
orphans  = 0

for item in items:
    tank_id = item.get("tankId", {}).get("S", "")
    user_id = item.get("userId", {}).get("S", "")
    if user_id in valid:
        continue
    orphans += 1
    print(f"  ORPHAN: tankId={tank_id}  userId={user_id}")
    if dry_run:
        continue

    # Delete all versions first
    vers_json = subprocess.check_output([
        "aws", "dynamodb", "query",
        "--table-name", versions_tbl,
        "--key-condition-expression", "tankId = :tid",
        "--expression-attribute-values", json.dumps({":tid": {"S": tank_id}}),
        "--projection-expression", "tankId, version",
        "--output", "json",
    ])
    for v in json.loads(vers_json).get("Items", []):
        ver = v["version"]["S"]
        subprocess.check_call([
            "aws", "dynamodb", "delete-item",
            "--table-name", versions_tbl,
            "--key", json.dumps({"tankId": {"S": tank_id}, "version": {"S": ver}}),
        ])
        print(f"    deleted version {ver}")

    # Delete the tank record
    subprocess.check_call([
        "aws", "dynamodb", "delete-item",
        "--table-name", tanks_tbl,
        "--key", json.dumps({"tankId": {"S": tank_id}}),
    ])
    print(f"    deleted tank")

print(f"\n{'DRY RUN — ' if dry_run else ''}{'Would delete' if dry_run else 'Deleted'} {orphans} orphaned tank(s).")
PYEOF
