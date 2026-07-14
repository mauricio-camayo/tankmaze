import * as path from 'path';
import * as childProcess from 'child_process';
import { Stack, StackProps, CfnOutput, Duration, SecretValue } from 'aws-cdk-lib';
import * as cognito from 'aws-cdk-lib/aws-cognito';
import * as acm from 'aws-cdk-lib/aws-certificatemanager';
import * as ses from 'aws-cdk-lib/aws-ses';
import * as lambda from 'aws-cdk-lib/aws-lambda';
import * as apigwv2 from 'aws-cdk-lib/aws-apigatewayv2';
import * as apigwv2integrations from 'aws-cdk-lib/aws-apigatewayv2-integrations';
import * as dynamodb from 'aws-cdk-lib/aws-dynamodb';
import * as secretsmanager from 'aws-cdk-lib/aws-secretsmanager';
import { Construct } from 'constructs';

export class AuthStack extends Stack {
  readonly userPool: cognito.UserPool;
  readonly userPoolClient: cognito.UserPoolClient;

  constructor(scope: Construct, id: string, props: StackProps) {
    super(scope, id, props);

    const googleClientId = this.node.tryGetContext('googleClientId') as string | undefined;
    const googleClientSecret = this.node.tryGetContext('googleClientSecret') as string | undefined;
    const facebookAppId = this.node.tryGetContext('facebookAppId') as string | undefined;
    const facebookAppSecret = this.node.tryGetContext('facebookAppSecret') as string | undefined;
    // GitHub (item 233) and Discord (item 240) sign-in — both DISABLED on
    // the frontend (GITHUB_LOGIN_ENABLED/DISCORD_LOGIN_ENABLED are false in
    // Landing.tsx) and unverified end-to-end regardless of whether these
    // context values are set; see the oidc-shim block below and
    // PRIORITIES.md items 233/240 for what's still outstanding.
    const githubClientId = this.node.tryGetContext('githubClientId') as string | undefined;
    const githubClientSecret = this.node.tryGetContext('githubClientSecret') as string | undefined;
    const discordClientId = this.node.tryGetContext('discordClientId') as string | undefined;
    const discordClientSecret = this.node.tryGetContext('discordClientSecret') as string | undefined;
    // Set (via --context sesSenderEmail=no-reply@tankmaze.org) only once the
    // SES domain identity below is verified — see the DKIM CfnOutputs and
    // item 214. Same flag also un-gates forgot-password-worker (item 217)
    // in api-stack.ts. Left unset, the pool keeps sending from Cognito's
    // default no-reply@verificationemail.com sender.
    const sesSenderEmail = this.node.tryGetContext('sesSenderEmail') as string | undefined;
    const callbackUrls = ['http://localhost:5173', 'https://tankmaze.org'];
    if (process.env.SITE_URL) callbackUrls.push(process.env.SITE_URL);

    // Logical ID changed (UserPool → UserPool2) to force CloudFormation
    // replacement. The previous pool had email mutable:false which caused
    // "Attribute cannot be updated" on every Google sign-in after the first.
    // ApiStack and FrontendStack no longer hold cross-stack imports from this
    // stack (they use context strings), so the old pool can now be deleted.
    this.userPool = new cognito.UserPool(this, 'UserPool2', {
      userPoolName: 'tankmaze-users',
      selfSignUpEnabled: true,
      signInAliases: { email: true },
      autoVerify: { email: true },
      standardAttributes: {
        email:          { required: true,  mutable: true },
        givenName:      { required: false, mutable: true },
        profilePicture: { required: false, mutable: true },
      },
      passwordPolicy: {
        minLength: 8,
        requireLowercase: true,
        requireUppercase: true,
        requireDigits: true,
        requireSymbols: false,
      },
      accountRecovery: cognito.AccountRecovery.EMAIL_ONLY,
      email: sesSenderEmail
        ? cognito.UserPoolEmail.withSES({ fromEmail: sesSenderEmail, fromName: 'TankMaze' })
        : undefined,
    });

    this.userPool.addGroup('AdminGroup', {
      groupName: 'platform-admin',
      description: 'TankMaze platform administrators',
    });

    // Custom domain so Google's consent screen shows "auth.tankmaze.org"
    // instead of the default *.amazoncognito.com prefix domain. DNS is on
    // Cloudflare (not Route53), so validation is manual: after `cdk deploy`
    // starts, fetch the CNAME record ACM is waiting on and add it in
    // Cloudflare, then wait for the certificate to issue.
    const authDomainName = 'auth.tankmaze.org';
    const certificate = new acm.Certificate(this, 'AuthDomainCertificate', {
      domainName: authDomainName,
      validation: acm.CertificateValidation.fromDns(),
    });
    const domain = this.userPool.addDomain('Domain', {
      customDomain: { domainName: authDomainName, certificate },
    });

    // SES domain identity (item 214) so verification/notification emails
    // send from no-reply@tankmaze.org instead of Cognito's shared default
    // sender, which mail providers routinely spam-filter. Same DNS-is-on-
    // Cloudflare constraint as the ACM cert above: Easy DKIM verification
    // needs 3 CNAME records added manually — see the DKIM CfnOutputs below.
    // Creating this identity is deploy-safe on its own (additive, and
    // Cognito only switches to it once sesSenderEmail is set above).
    const emailIdentity = new ses.EmailIdentity(this, 'EmailIdentity', {
      identity: ses.Identity.domain('tankmaze.org'),
    });

    const supportedIdentityProviders = [cognito.UserPoolClientIdentityProvider.COGNITO];
    let googleIdp: cognito.UserPoolIdentityProviderGoogle | undefined;

    if (googleClientId && googleClientSecret) {
      googleIdp = new cognito.UserPoolIdentityProviderGoogle(this, 'GoogleIdP', {
        userPool: this.userPool,
        clientId: googleClientId,
        clientSecretValue: SecretValue.unsafePlainText(googleClientSecret),
        scopes: ['email', 'profile', 'openid'],
        attributeMapping: {
          email:          cognito.ProviderAttribute.GOOGLE_EMAIL,
          givenName:      cognito.ProviderAttribute.GOOGLE_NAME,
          profilePicture: cognito.ProviderAttribute.other('picture'),
        },
      });
      supportedIdentityProviders.push(cognito.UserPoolClientIdentityProvider.GOOGLE);
    }

    let facebookIdp: cognito.UserPoolIdentityProviderFacebook | undefined;

    if (facebookAppId && facebookAppSecret) {
      facebookIdp = new cognito.UserPoolIdentityProviderFacebook(this, 'FacebookIdP', {
        userPool: this.userPool,
        clientId: facebookAppId,
        clientSecret: facebookAppSecret,
        scopes: ['public_profile', 'email'],
        attributeMapping: {
          email:          cognito.ProviderAttribute.FACEBOOK_EMAIL,
          givenName:      cognito.ProviderAttribute.FACEBOOK_NAME,
          profilePicture: cognito.ProviderAttribute.other('picture'),
        },
      });
      supportedIdentityProviders.push(cognito.UserPoolClientIdentityProvider.FACEBOOK);
    }

    // ---- GitHub (item 233) / Discord (item 240) via a shared OIDC shim --
    // Neither provider speaks OIDC natively (no discovery doc, no
    // id_token — see cmd/oidc-shim's package doc for the full flow this
    // Lambda implements). DISABLED on the frontend regardless of whether
    // real credentials are configured here — see Landing.tsx's
    // GITHUB_LOGIN_ENABLED/DISCORD_LOGIN_ENABLED, both false. Not yet
    // verified end-to-end against a real deployed User Pool.
    const oidcShimBackendDir = path.join(__dirname, '../../../backend');
    const oidcShimGoBin = require('fs').existsSync(
      '/home/macaco/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.22.12.linux-amd64/bin/go',
    )
      ? '/home/macaco/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.22.12.linux-amd64/bin/go'
      : (process.env.GO_BIN ?? 'go');

    const buildOidcShimLambda = (id: string, env: Record<string, string>): lambda.Function =>
      new lambda.Function(this, id, {
        runtime: lambda.Runtime.PROVIDED_AL2023,
        architecture: lambda.Architecture.ARM_64,
        handler: 'bootstrap',
        code: lambda.Code.fromAsset(oidcShimBackendDir, {
          bundling: {
            image: lambda.Runtime.PROVIDED_AL2023.bundlingImage,
            local: {
              tryBundle(outDir: string): boolean {
                try {
                  childProcess.execSync(
                    `GOTOOLCHAIN=local GOOS=linux GOARCH=arm64 ${oidcShimGoBin} build -tags lambda.norpc -o ${outDir}/bootstrap ./cmd/oidc-shim`,
                    { cwd: oidcShimBackendDir, stdio: 'inherit' },
                  );
                  return true;
                } catch {
                  return false;
                }
              },
            },
            command: [
              'bash', '-c',
              'GOTOOLCHAIN=local GOOS=linux GOARCH=arm64 go build -tags lambda.norpc -o /asset-output/bootstrap ./cmd/oidc-shim',
            ],
          },
        }),
        environment: env,
        timeout: Duration.seconds(10),
        memorySize: 256,
      });

    // Ephemeral single-use authorization-code store, shared by both shims
    // (item 240's own request to share this infrastructure rather than
    // stand up a second copy) — see cmd/oidc-shim/store.go. Created lazily
    // so it doesn't exist at all unless at least one provider is configured.
    let oidcCodeTable: dynamodb.Table | undefined;
    const ensureOidcCodeTable = (): dynamodb.Table => {
      if (!oidcCodeTable) {
        oidcCodeTable = new dynamodb.Table(this, 'OidcShimCodeTable', {
          tableName: 'tankmaze-oidc-shim-codes',
          partitionKey: { name: 'code', type: dynamodb.AttributeType.STRING },
          billingMode: dynamodb.BillingMode.PAY_PER_REQUEST,
          timeToLiveAttribute: 'expiresAt',
        });
      }
      return oidcCodeTable;
    };

    const addOidcShimIdp = (
      providerKey: 'github' | 'discord',
      idpName: 'GitHub' | 'Discord',
      clientId: string,
      clientSecret: string,
    ): cognito.UserPoolIdentityProviderOidc => {
      const codeTable = ensureOidcCodeTable();

      // Generated on the shim Lambda's first cold start if this secret is
      // empty — no manual "run openssl and paste the key in" deploy step.
      const signingKeySecret = new secretsmanager.Secret(this, `${idpName}OidcSigningKey`, {
        description: `RS256 signing key for the ${idpName} OIDC shim (items 233/240) — populated by the Lambda itself on first use, not set here`,
      });

      const shimLambda = buildOidcShimLambda(`${idpName}OidcShim`, {
        PROVIDER: providerKey,
        CLIENT_ID: clientId,
        CLIENT_SECRET: clientSecret,
        CODE_TABLE_NAME: codeTable.tableName,
        SIGNING_KEY_SECRET_ARN: signingKeySecret.secretArn,
        SHIM_BASE_URL: '', // filled in via addEnvironment below, once the HTTP API exists
      });
      codeTable.grantReadWriteData(shimLambda);
      signingKeySecret.grantRead(shimLambda);
      signingKeySecret.grantWrite(shimLambda);

      const shimApi = new apigwv2.HttpApi(this, `${idpName}OidcShimApi`, {
        apiName: `tankmaze-${providerKey}-oidc-shim`,
      });
      const integration = new apigwv2integrations.HttpLambdaIntegration(`${idpName}OidcShimIntegration`, shimLambda);
      shimApi.addRoutes({ path: '/authorize', methods: [apigwv2.HttpMethod.GET], integration });
      shimApi.addRoutes({ path: '/callback', methods: [apigwv2.HttpMethod.GET], integration });
      shimApi.addRoutes({ path: '/token', methods: [apigwv2.HttpMethod.POST], integration });
      shimApi.addRoutes({ path: '/jwks', methods: [apigwv2.HttpMethod.GET], integration });
      shimApi.addRoutes({ path: '/userinfo', methods: [apigwv2.HttpMethod.GET], integration });

      shimLambda.addEnvironment('SHIM_BASE_URL', shimApi.apiEndpoint);

      // The value to paste into the real provider's OAuth App settings —
      // deliberately NOT auth.tankmaze.org/oauth2/idpresponse (Cognito's
      // own callback, one hop further downstream): GitHub/Discord redirect
      // here first, and only this shim then forwards the browser on to
      // Cognito's real callback after exchanging the code and fetching the
      // profile. See cmd/oidc-shim/main.go's package doc for the full flow.
      new CfnOutput(this, `${idpName}OidcShimCallbackUrl`, {
        value: `${shimApi.apiEndpoint}/callback`,
        description: `Set this as the ${idpName} OAuth App's redirect/callback URL`,
      });

      const idp = new cognito.UserPoolIdentityProviderOidc(this, `${idpName}IdP`, {
        userPool: this.userPool,
        name: idpName,
        clientId,
        clientSecret,
        issuerUrl: shimApi.apiEndpoint,
        endpoints: {
          authorization: `${shimApi.apiEndpoint}/authorize`,
          token: `${shimApi.apiEndpoint}/token`,
          userInfo: `${shimApi.apiEndpoint}/userinfo`,
          jwksUri: `${shimApi.apiEndpoint}/jwks`,
        },
        attributeMapping: {
          email:          cognito.ProviderAttribute.other('email'),
          givenName:      cognito.ProviderAttribute.other('given_name'),
          profilePicture: cognito.ProviderAttribute.other('picture'),
        },
        scopes: ['openid'],
      });
      supportedIdentityProviders.push(cognito.UserPoolClientIdentityProvider.custom(idpName));
      return idp;
    };

    let githubIdp: cognito.UserPoolIdentityProviderOidc | undefined;
    if (githubClientId && githubClientSecret) {
      githubIdp = addOidcShimIdp('github', 'GitHub', githubClientId, githubClientSecret);
    }
    let discordIdp: cognito.UserPoolIdentityProviderOidc | undefined;
    if (discordClientId && discordClientSecret) {
      discordIdp = addOidcShimIdp('discord', 'Discord', discordClientId, discordClientSecret);
    }

    this.userPoolClient = new cognito.UserPoolClient(this, 'UserPoolClient', {
      userPool: this.userPool,
      userPoolClientName: 'tankmaze-web',
      authFlows: {
        userPassword: true,
        userSrp: true,
      },
      oAuth: {
        flows: { authorizationCodeGrant: true },
        scopes: [cognito.OAuthScope.EMAIL, cognito.OAuthScope.OPENID, cognito.OAuthScope.PROFILE],
        callbackUrls,
        logoutUrls: callbackUrls,
      },
      supportedIdentityProviders,
    });

    if (googleIdp) {
      this.userPoolClient.node.addDependency(googleIdp);
    }
    if (facebookIdp) {
      this.userPoolClient.node.addDependency(facebookIdp);
    }
    if (githubIdp) {
      this.userPoolClient.node.addDependency(githubIdp);
    }
    if (discordIdp) {
      this.userPoolClient.node.addDependency(discordIdp);
    }

    new CfnOutput(this, 'UserPoolId',     { value: this.userPool.userPoolId });
    new CfnOutput(this, 'UserPoolClientId', { value: this.userPoolClient.userPoolClientId });
    new CfnOutput(this, 'CognitoDomain',  { value: authDomainName });
    new CfnOutput(this, 'CognitoDomainCloudFrontTarget', {
      value: domain.cloudFrontDomainName,
      description: 'Point a Cloudflare CNAME for auth.tankmaze.org at this value',
    });

    // Add these 3 as CNAME records in Cloudflare (proxy status: DNS only,
    // not proxied) to complete SES Easy DKIM verification for item 214.
    emailIdentity.dkimRecords.forEach((record, i) => {
      new CfnOutput(this, `SesDkimRecordName${i + 1}`, {
        value: record.name,
        description: `Cloudflare CNAME name (record ${i + 1} of 3) for SES domain verification`,
      });
      new CfnOutput(this, `SesDkimRecordValue${i + 1}`, {
        value: record.value,
        description: `Cloudflare CNAME target (record ${i + 1} of 3) for SES domain verification`,
      });
    });
  }
}
