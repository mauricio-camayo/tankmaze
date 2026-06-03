import { Stack, StackProps, CfnOutput, RemovalPolicy } from 'aws-cdk-lib';
import * as s3 from 'aws-cdk-lib/aws-s3';
import * as cloudfront from 'aws-cdk-lib/aws-cloudfront';
import * as origins from 'aws-cdk-lib/aws-cloudfront-origins';
import * as route53 from 'aws-cdk-lib/aws-route53';
import * as route53targets from 'aws-cdk-lib/aws-route53-targets';
import { Construct } from 'constructs';

interface FrontendStackProps extends StackProps {
  userPoolId: string;
  userPoolClientId: string;
  wsEndpoint: string;
  httpEndpoint: string;
}

export class FrontendStack extends Stack {
  constructor(scope: Construct, id: string, props: FrontendStackProps) {
    super(scope, id, props);

    const { userPoolId, userPoolClientId, wsEndpoint, httpEndpoint } = props;

    // Private S3 bucket — served exclusively through CloudFront
    const siteBucket = new s3.Bucket(this, 'SiteBucket', {
      blockPublicAccess: s3.BlockPublicAccess.BLOCK_ALL,
      enforceSSL: true,
      removalPolicy: RemovalPolicy.RETAIN,
    });

    // CloudFront distribution with OAC (Origin Access Control)
    const distribution = new cloudfront.Distribution(this, 'Distribution', {
      defaultBehavior: {
        origin: origins.S3BucketOrigin.withOriginAccessControl(siteBucket),
        viewerProtocolPolicy: cloudfront.ViewerProtocolPolicy.REDIRECT_TO_HTTPS,
        cachePolicy: cloudfront.CachePolicy.CACHING_OPTIMIZED,
        compress: true,
      },
      defaultRootObject: 'index.html',
      // SPA fallback: serve index.html for any 404 so the React router works
      errorResponses: [
        {
          httpStatus: 403,
          responseHttpStatus: 200,
          responsePagePath: '/index.html',
        },
        {
          httpStatus: 404,
          responseHttpStatus: 200,
          responsePagePath: '/index.html',
        },
      ],
    });

    // Optional Route53 A record — only created when `domainName` CDK context is set.
    //   cdk deploy --context domainName=tankmaze.example.com
    const domainName = this.node.tryGetContext('domainName') as string | undefined;
    if (domainName) {
      const zone = route53.HostedZone.fromLookup(this, 'HostedZone', { domainName });
      new route53.ARecord(this, 'SiteARecord', {
        zone,
        target: route53.RecordTarget.fromAlias(
          new route53targets.CloudFrontTarget(distribution),
        ),
      });
    }

    // Outputs consumed by CI/CD (GitHub Actions)
    new CfnOutput(this, 'SiteBucketName',          { value: siteBucket.bucketName });
    new CfnOutput(this, 'DistributionId',           { value: distribution.distributionId });
    new CfnOutput(this, 'DistributionDomain',       { value: distribution.distributionDomainName });
    new CfnOutput(this, 'ViteUserPoolId',           { value: userPoolId });
    new CfnOutput(this, 'ViteUserPoolClientId',     { value: userPoolClientId });
    new CfnOutput(this, 'ViteWsEndpoint',           { value: wsEndpoint });
    new CfnOutput(this, 'ViteApiEndpoint',          { value: httpEndpoint });
  }
}
