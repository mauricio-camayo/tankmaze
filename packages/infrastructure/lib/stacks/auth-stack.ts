import { Stack, StackProps, CfnOutput, SecretValue } from 'aws-cdk-lib';
import * as cognito from 'aws-cdk-lib/aws-cognito';
import * as acm from 'aws-cdk-lib/aws-certificatemanager';
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

    new CfnOutput(this, 'UserPoolId',     { value: this.userPool.userPoolId });
    new CfnOutput(this, 'UserPoolClientId', { value: this.userPoolClient.userPoolClientId });
    new CfnOutput(this, 'CognitoDomain',  { value: authDomainName });
    new CfnOutput(this, 'CognitoDomainCloudFrontTarget', {
      value: domain.cloudFrontDomainName,
      description: 'Point a Cloudflare CNAME for auth.tankmaze.org at this value',
    });
  }
}
