import { Stack, StackProps, CfnOutput } from 'aws-cdk-lib';
import * as iam from 'aws-cdk-lib/aws-iam';
import { Construct } from 'constructs';

interface GithubOidcStackProps extends StackProps {
  /** GitHub org/repo that is allowed to assume the deploy role, e.g. "myorg/tankmaze" */
  githubRepo: string;
  /** Branch that is allowed to deploy (default: main) */
  deployBranch?: string;
}

/**
 * Provisions the GitHub Actions OIDC provider and a deploy IAM role.
 * Deploy this stack once before the first CDK deploy:
 *
 *   pnpm cdk deploy TankmAzeGithubOidc --context githubRepo=<org/repo>
 *
 * Then set the output role ARN as the AWS_DEPLOY_ROLE_ARN GitHub secret.
 *
 * Permission model:
 *   - This role assumes the CDK bootstrap roles (deploy, file-publishing, lookup).
 *   - The CDK CFN execution role (AdministratorAccess) creates all stack resources.
 *   - Only the S3 sync and CloudFront invalidation CI steps run under this role directly.
 */
export class GithubOidcStack extends Stack {
  constructor(scope: Construct, id: string, props: GithubOidcStackProps) {
    super(scope, id, props);

    const { githubRepo, deployBranch = 'main' } = props;

    const provider = new iam.OpenIdConnectProvider(this, 'GithubOidcProvider', {
      url: 'https://token.actions.githubusercontent.com',
      clientIds: ['sts.amazonaws.com'],
      // Thumbprint list is managed by AWS; the value below is the well-known
      // GitHub thumbprint required by IAM OIDC provider registration.
      thumbprints: ['6938fd4d98bab03faadb97b34396831e3780aea1'],
    });

    const deployRole = new iam.Role(this, 'GithubDeployRole', {
      roleName: 'tankmaze-github-deploy',
      assumedBy: new iam.WebIdentityPrincipal(provider.openIdConnectProviderArn, {
        StringEquals: {
          'token.actions.githubusercontent.com:aud': 'sts.amazonaws.com',
        },
        StringLike: {
          // Allow pushes to the deploy branch and pull_request events (for cdk diff).
          'token.actions.githubusercontent.com:sub': [
            `repo:${githubRepo}:ref:refs/heads/${deployBranch}`,
            `repo:${githubRepo}:pull_request`,
          ],
        },
      }),
      description: 'Assumed by GitHub Actions to deploy TankMaze infrastructure',
    });

    // CDK bootstrap roles handle all CloudFormation and resource-creation operations.
    // The CFN execution role (cdk-hnb659fds-cfn-exec-role-*) carries AdministratorAccess
    // so no service-specific permissions (Lambda, DynamoDB, etc.) are needed here.
    const cdkQualifier = 'hnb659fds';
    deployRole.addToPolicy(new iam.PolicyStatement({
      sid: 'CdkBootstrapRoles',
      actions: ['sts:AssumeRole'],
      resources: [
        `arn:aws:iam::${this.account}:role/cdk-${cdkQualifier}-deploy-role-${this.account}-${this.region}`,
        `arn:aws:iam::${this.account}:role/cdk-${cdkQualifier}-file-publishing-role-${this.account}-${this.region}`,
        `arn:aws:iam::${this.account}:role/cdk-${cdkQualifier}-lookup-role-${this.account}-${this.region}`,
      ],
    }));

    // Direct S3 access for `aws s3 sync packages/frontend/dist s3://$FRONTEND_BUCKET`
    // (runs outside CDK, using the deploy role directly).
    deployRole.addToPolicy(new iam.PolicyStatement({
      sid: 'FrontendBucketSync',
      actions: [
        's3:PutObject',
        's3:GetObject',
        's3:DeleteObject',
        's3:ListBucket',
        's3:GetBucketLocation',
      ],
      resources: [
        `arn:aws:s3:::tankmaze*`,
        `arn:aws:s3:::tankmaze*/*`,
      ],
    }));

    // Direct CloudFront access for `aws cloudfront create-invalidation`
    // (runs outside CDK, using the deploy role directly).
    deployRole.addToPolicy(new iam.PolicyStatement({
      sid: 'CloudFrontInvalidation',
      actions: ['cloudfront:CreateInvalidation'],
      resources: [`arn:aws:cloudfront::${this.account}:distribution/*`],
    }));

    new CfnOutput(this, 'DeployRoleArn', {
      value: deployRole.roleArn,
      description: 'Set this as the AWS_DEPLOY_ROLE_ARN GitHub secret',
    });
  }
}
