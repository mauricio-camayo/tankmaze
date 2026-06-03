# Deploying TankMaze

One-time setup to get CI/CD running end-to-end.

---

## 1. Bootstrap CDK

Run once per AWS account/region before the first deploy:

```bash
cd packages/infrastructure
pnpm install
pnpm cdk bootstrap aws://<ACCOUNT_ID>/<REGION>
```

## 2. Create the GitHub OIDC deploy role

This provisions the OIDC trust and the IAM role that GitHub Actions will assume.
Replace `<org/repo>` with your GitHub repository (e.g. `acme/tankmaze`):

```bash
cd packages/infrastructure
pnpm cdk deploy TankmAzeGithubOidc --context githubRepo=<org/repo>
```

Copy the `DeployRoleArn` output value — you'll need it in the next step.

## 3. Deploy the main stacks (first time)

```bash
pnpm cdk deploy --all --context githubRepo=<org/repo> --require-approval never
```

After this succeeds the remaining secret values are available as CloudFormation outputs:

```bash
# List all stack outputs at once
aws cloudformation describe-stacks --query 'Stacks[].Outputs[].[OutputKey,OutputValue]' --output table
```

## 4. Configure GitHub secrets

Go to **Settings → Secrets and variables → Actions** in your GitHub repository and add:

| Secret | Where to find the value |
|---|---|
| `AWS_DEPLOY_ROLE_ARN` | `TankmAzeGithubOidc` stack output `DeployRoleArn` |
| `FRONTEND_BUCKET` | `TankmAzeFrontend` stack output (S3 bucket name) |
| `CLOUDFRONT_DISTRIBUTION_ID` | `TankmAzeFrontend` stack output |
| `VITE_USER_POOL_ID` | `TankmAzeAuth` stack output |
| `VITE_USER_POOL_CLIENT_ID` | `TankmAzeAuth` stack output |
| `VITE_WS_ENDPOINT` | `TankmAzeApi` stack output `WssEndpoint` |
| `VITE_API_ENDPOINT` | `TankmAzeApi` stack output `HttpEndpoint` |

## 5. Set the AI tank env vars on the Lambda

After the first deploy, create the scout and bruiser tanks via the API, then set their IDs on the tank-api Lambda:

```bash
aws lambda update-function-configuration \
  --function-name <TankApi function name> \
  --environment "Variables={...,SCOUT_TANK_ID=<id>,SCOUT_VERSION=v1,BRUISER_TANK_ID=<id>,BRUISER_VERSION=v1}"
```

## 6. Verify

Push a commit to `main`. The GitHub Actions `CI` workflow should:
1. Build and test Go + TypeScript
2. Run `cdk deploy --all` (Lambdas compiled by CDK's local bundler)
3. Build the frontend with the Vite env vars from secrets
4. Sync frontend to S3 and invalidate CloudFront

PRs get a `cdk diff` comment instead of a deploy.
