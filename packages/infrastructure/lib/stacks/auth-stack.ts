import { Stack, StackProps, CfnOutput, SecretValue } from 'aws-cdk-lib';
import * as cognito from 'aws-cdk-lib/aws-cognito';
import { Construct } from 'constructs';

export class AuthStack extends Stack {
  readonly userPool: cognito.UserPool;
  readonly userPoolClient: cognito.UserPoolClient;

  constructor(scope: Construct, id: string, props: StackProps) {
    super(scope, id, props);

    const googleClientId = this.node.tryGetContext('googleClientId') as string | undefined;
    const callbackUrls = ['http://localhost:5173'];
    if (process.env.SITE_URL) callbackUrls.push(process.env.SITE_URL);

    this.userPool = new cognito.UserPool(this, 'UserPool', {
      userPoolName: 'tankmaze-users',
      selfSignUpEnabled: true,
      signInAliases: { email: true },
      autoVerify: { email: true },
      standardAttributes: {
        email: { required: true, mutable: true }, // mutable required for Google IdP to map email
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

    const domainPrefix = `tankmaze-${this.account}`;
    this.userPool.addDomain('Domain', {
      cognitoDomain: { domainPrefix },
    });

    const supportedIdentityProviders = [cognito.UserPoolClientIdentityProvider.COGNITO];
    let googleIdp: cognito.UserPoolIdentityProviderGoogle | undefined;

    if (googleClientId) {
      googleIdp = new cognito.UserPoolIdentityProviderGoogle(this, 'GoogleIdP', {
        userPool: this.userPool,
        clientId: googleClientId,
        clientSecretValue: SecretValue.ssmSecure('/tankmaze/google/client-secret'),
        scopes: ['email', 'profile', 'openid'],
        attributeMapping: {
          email: cognito.ProviderAttribute.GOOGLE_EMAIL,
          givenName: cognito.ProviderAttribute.GOOGLE_NAME,
        },
      });
      supportedIdentityProviders.push(cognito.UserPoolClientIdentityProvider.GOOGLE);
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

    new CfnOutput(this, 'UserPoolId', { value: this.userPool.userPoolId });
    new CfnOutput(this, 'UserPoolClientId', { value: this.userPoolClient.userPoolClientId });
    new CfnOutput(this, 'CognitoDomain', {
      value: `${domainPrefix}.auth.${this.region}.amazoncognito.com`,
    });
  }
}
