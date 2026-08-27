import * as path from 'path';
import { Stack, StackProps, CfnOutput, RemovalPolicy, Duration } from 'aws-cdk-lib';
import * as s3 from 'aws-cdk-lib/aws-s3';
import * as cloudfront from 'aws-cdk-lib/aws-cloudfront';
import * as origins from 'aws-cdk-lib/aws-cloudfront-origins';
import * as route53 from 'aws-cdk-lib/aws-route53';
import * as route53targets from 'aws-cdk-lib/aws-route53-targets';
import * as wafv2 from 'aws-cdk-lib/aws-wafv2';
import * as acm from 'aws-cdk-lib/aws-certificatemanager';
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

    // WAFv2 WebACL — CloudFront WAFs must be created in us-east-1 (INFRA-CF-WAF).
    // Uses managed OWASP Top 10 rule set + a rate-based rule (1000 req / 5 min per IP).
    // Off by default — WAF has a flat $5/mo Web ACL fee + $1/mo per rule
    // regardless of traffic, which doesn't pencil out for a low-traffic
    // personal project. Re-enable for a nominal cost once real public
    // traffic (or a real threat model) justifies it:
    //   cdk deploy --context enableWaf=true
    const enableWaf = this.node.tryGetContext('enableWaf') === 'true' || this.node.tryGetContext('enableWaf') === true;
    const webAcl = enableWaf ? new wafv2.CfnWebACL(this, 'WebACL', {
      name: 'tankmaze-cf-waf',
      scope: 'CLOUDFRONT',
      defaultAction: { allow: {} },
      visibilityConfig: {
        cloudWatchMetricsEnabled: true,
        metricName: 'tankmaze-cf-waf',
        sampledRequestsEnabled: true,
      },
      rules: [
        {
          name: 'AWSManagedRulesCommonRuleSet',
          priority: 1,
          overrideAction: { none: {} },
          visibilityConfig: {
            cloudWatchMetricsEnabled: true,
            metricName: 'AWSManagedRulesCommonRuleSet',
            sampledRequestsEnabled: true,
          },
          statement: {
            managedRuleGroupStatement: {
              vendorName: 'AWS',
              name: 'AWSManagedRulesCommonRuleSet',
            },
          },
        },
        {
          name: 'RateLimitPerIP',
          priority: 2,
          action: { block: {} },
          visibilityConfig: {
            cloudWatchMetricsEnabled: true,
            metricName: 'RateLimitPerIP',
            sampledRequestsEnabled: true,
          },
          statement: {
            rateBasedStatement: {
              limit: 1000,
              aggregateKeyType: 'IP',
              evaluationWindowSec: 300,
            },
          },
        },
      ],
    }) : undefined;

    // INFRA-CF-HEADERS: security response headers on every CloudFront response.
    const responseHeadersPolicy = new cloudfront.ResponseHeadersPolicy(this, 'SecurityHeaders', {
      securityHeadersBehavior: {
        strictTransportSecurity: {
          accessControlMaxAge: Duration.days(365),
          includeSubdomains: true,
          override: true,
        },
        contentTypeOptions: { override: true },
        frameOptions: {
          frameOption: cloudfront.HeadersFrameOption.DENY,
          override: true,
        },
        referrerPolicy: {
          referrerPolicy: cloudfront.HeadersReferrerPolicy.STRICT_ORIGIN_WHEN_CROSS_ORIGIN,
          override: true,
        },
        xssProtection: { protection: true, modeBlock: true, override: true },
      },
    });

    // Redirects any non-canonical host (e.g. the raw *.cloudfront.net domain)
    // to https://tankmaze.org, preserving path and query string (item 199).
    const canonicalHostRedirect = new cloudfront.Function(this, 'CanonicalHostRedirect', {
      code: cloudfront.FunctionCode.fromFile({
        filePath: path.join(__dirname, '../../lib/cloudfront-functions/canonical-host-redirect.js'),
      }),
      runtime: cloudfront.FunctionRuntime.JS_2_0,
    });

    // Registers tankmaze.org as a valid alias on the distribution so CloudFront
    // accepts that Host directly (item 208) — imports the cert requested and
    // DNS-validated out of band (Cloudflare CNAME), since this account's
    // deploy role has no acm:* permissions to request one via CDK itself.
    //   cdk deploy --context certificateArn=arn:aws:acm:us-east-1:...
    const certificateArn = this.node.tryGetContext('certificateArn') as string | undefined;
    const siteDomain = 'tankmaze.org';
    const certificate = certificateArn
      ? acm.Certificate.fromCertificateArn(this, 'SiteCertificate', certificateArn)
      : undefined;

    // CloudFront distribution with OAC (Origin Access Control)
    const distribution = new cloudfront.Distribution(this, 'Distribution', {
      ...(certificate ? { domainNames: [siteDomain], certificate } : {}),
      defaultBehavior: {
        origin: origins.S3BucketOrigin.withOriginAccessControl(siteBucket),
        viewerProtocolPolicy: cloudfront.ViewerProtocolPolicy.REDIRECT_TO_HTTPS,
        cachePolicy: cloudfront.CachePolicy.CACHING_OPTIMIZED,
        responseHeadersPolicy,
        compress: true,
        functionAssociations: [
          {
            function: canonicalHostRedirect,
            eventType: cloudfront.FunctionEventType.VIEWER_REQUEST,
          },
        ],
      },
      defaultRootObject: 'index.html',
      // TLS 1.2 minimum — drops TLS 1.0/1.1 (POODLE, BEAST) (INFRA-CF-TLS).
      minimumProtocolVersion: cloudfront.SecurityPolicyProtocol.TLS_V1_2_2021,
      sslSupportMethod: cloudfront.SSLMethod.SNI,
      // WAF association (INFRA-CF-WAF) — omitted entirely when enableWaf is off.
      ...(webAcl ? { webAclId: webAcl.attrArn } : {}),
      // Access logging — provides visibility into cache performance and attack traffic
      // (INFRA-CF-LOG).
      enableLogging: true,
      logFilePrefix: 'cf-logs/',
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
