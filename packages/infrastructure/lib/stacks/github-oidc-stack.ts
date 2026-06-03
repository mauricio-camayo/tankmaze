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

    // CDK deploy needs broad CloudFormation + resource creation permissions.
    // Scoped to resources with the tankmaze prefix where possible.
    deployRole.addManagedPolicy(
      iam.ManagedPolicy.fromAwsManagedPolicyName('AWSCloudFormationFullAccess'),
    );

    deployRole.addToPolicy(new iam.PolicyStatement({
      sid: 'LambdaAccess',
      actions: [
        'lambda:CreateFunction',
        'lambda:UpdateFunctionCode',
        'lambda:UpdateFunctionConfiguration',
        'lambda:DeleteFunction',
        'lambda:GetFunction',
        'lambda:ListFunctions',
        'lambda:AddPermission',
        'lambda:RemovePermission',
        'lambda:TagResource',
        'lambda:PublishLayerVersion',
      ],
      resources: ['*'],
    }));

    deployRole.addToPolicy(new iam.PolicyStatement({
      sid: 'S3Access',
      actions: [
        's3:CreateBucket',
        's3:DeleteBucket',
        's3:PutBucketPolicy',
        's3:DeleteBucketPolicy',
        's3:PutBucketVersioning',
        's3:PutLifecycleConfiguration',
        's3:PutBucketTagging',
        's3:PutBucketCORS',
        's3:PutBucketWebsite',
        's3:PutBucketPublicAccessBlock',
        's3:GetBucketPublicAccessBlock',
        's3:PutEncryptionConfiguration',
        's3:GetEncryptionConfiguration',
        's3:GetBucketLocation',
        's3:ListBucket',
        's3:PutObject',
        's3:GetObject',
        's3:DeleteObject',
        's3:GetBucketPolicy',
      ],
      resources: ['*'],
    }));

    deployRole.addToPolicy(new iam.PolicyStatement({
      sid: 'DynamoDBAccess',
      actions: [
        'dynamodb:CreateTable',
        'dynamodb:DeleteTable',
        'dynamodb:DescribeTable',
        'dynamodb:UpdateTable',
        'dynamodb:UpdateTimeToLive',
        'dynamodb:DescribeTimeToLive',
        'dynamodb:TagResource',
        'dynamodb:ListTagsOfResource',
        'dynamodb:DescribeContinuousBackups',
        'dynamodb:UpdateContinuousBackups',
      ],
      resources: ['*'],
    }));

    deployRole.addToPolicy(new iam.PolicyStatement({
      sid: 'CognitoAccess',
      actions: [
        'cognito-idp:CreateUserPool',
        'cognito-idp:DeleteUserPool',
        'cognito-idp:UpdateUserPool',
        'cognito-idp:DescribeUserPool',
        'cognito-idp:CreateUserPoolClient',
        'cognito-idp:DeleteUserPoolClient',
        'cognito-idp:UpdateUserPoolClient',
        'cognito-idp:DescribeUserPoolClient',
        'cognito-idp:CreateGroup',
        'cognito-idp:DeleteGroup',
        'cognito-idp:GetGroup',
        'cognito-idp:ListGroups',
      ],
      resources: ['*'],
    }));

    deployRole.addToPolicy(new iam.PolicyStatement({
      sid: 'ApiGatewayAccess',
      actions: ['apigateway:*'],
      resources: ['*'],
    }));

    deployRole.addToPolicy(new iam.PolicyStatement({
      sid: 'CloudFrontAccess',
      actions: [
        'cloudfront:CreateDistribution',
        'cloudfront:UpdateDistribution',
        'cloudfront:DeleteDistribution',
        'cloudfront:GetDistribution',
        'cloudfront:GetDistributionConfig',
        'cloudfront:CreateInvalidation',
        'cloudfront:CreateOriginAccessControl',
        'cloudfront:DeleteOriginAccessControl',
        'cloudfront:GetOriginAccessControl',
        'cloudfront:TagResource',
      ],
      resources: ['*'],
    }));

    deployRole.addToPolicy(new iam.PolicyStatement({
      sid: 'CodeBuildAccess',
      actions: [
        'codebuild:CreateProject',
        'codebuild:UpdateProject',
        'codebuild:DeleteProject',
        'codebuild:BatchGetProjects',
      ],
      resources: ['*'],
    }));

    deployRole.addToPolicy(new iam.PolicyStatement({
      sid: 'EventBridgeSchedulerAccess',
      actions: [
        'scheduler:CreateScheduleGroup',
        'scheduler:DeleteScheduleGroup',
        'scheduler:GetScheduleGroup',
        'scheduler:ListScheduleGroups',
      ],
      resources: ['*'],
    }));

    deployRole.addToPolicy(new iam.PolicyStatement({
      sid: 'IAMAccess',
      actions: [
        'iam:CreateRole',
        'iam:DeleteRole',
        'iam:GetRole',
        'iam:UpdateRole',
        'iam:AttachRolePolicy',
        'iam:DetachRolePolicy',
        'iam:PutRolePolicy',
        'iam:DeleteRolePolicy',
        'iam:GetRolePolicy',
        'iam:PassRole',
        'iam:CreateOpenIDConnectProvider',
        'iam:DeleteOpenIDConnectProvider',
        'iam:GetOpenIDConnectProvider',
        'iam:TagOpenIDConnectProvider',
        'iam:TagRole',
      ],
      resources: ['*'],
    }));

    deployRole.addToPolicy(new iam.PolicyStatement({
      sid: 'EC2VpcAccess',
      actions: [
        'ec2:CreateVpc',
        'ec2:DeleteVpc',
        'ec2:DescribeVpcs',
        'ec2:ModifyVpcAttribute',
        'ec2:CreateSubnet',
        'ec2:DeleteSubnet',
        'ec2:DescribeSubnets',
        'ec2:CreateSecurityGroup',
        'ec2:DeleteSecurityGroup',
        'ec2:DescribeSecurityGroups',
        'ec2:AuthorizeSecurityGroupEgress',
        'ec2:RevokeSecurityGroupEgress',
        'ec2:CreateVpcEndpoint',
        'ec2:DeleteVpcEndpoints',
        'ec2:DescribeVpcEndpoints',
        'ec2:ModifyVpcEndpoint',
        'ec2:DescribeRouteTables',
        'ec2:DescribeAvailabilityZones',
        'ec2:DescribeAccountAttributes',
        'ec2:DescribeDhcpOptions',
        'ec2:CreateTags',
        'ec2:DeleteTags',
      ],
      resources: ['*'],
    }));

    deployRole.addToPolicy(new iam.PolicyStatement({
      sid: 'SsmAccess',
      actions: [
        'ssm:GetParameter',
        'ssm:PutParameter',
        'ssm:DeleteParameter',
      ],
      resources: [`arn:aws:ssm:*:${this.account}:parameter/cdk-bootstrap/*`],
    }));

    deployRole.addToPolicy(new iam.PolicyStatement({
      sid: 'Route53Access',
      actions: [
        'route53:ChangeResourceRecordSets',
        'route53:GetHostedZone',
        'route53:ListHostedZones',
        'route53:ListResourceRecordSets',
      ],
      resources: ['*'],
    }));

    new CfnOutput(this, 'DeployRoleArn', {
      value: deployRole.roleArn,
      description: 'Set this as the AWS_DEPLOY_ROLE_ARN GitHub secret',
    });
  }
}
