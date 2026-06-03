import { App } from 'aws-cdk-lib';
import { AuthStack } from './stacks/auth-stack';
import { StorageStack } from './stacks/storage-stack';
import { BuildStack } from './stacks/build-stack';
import { ApiStack } from './stacks/api-stack';
import { FrontendStack } from './stacks/frontend-stack';
import { GithubOidcStack } from './stacks/github-oidc-stack';

const app = new App();
const env = {
  account: process.env.CDK_DEFAULT_ACCOUNT,
  region: process.env.CDK_DEFAULT_REGION,
};

const githubRepo = app.node.tryGetContext('githubRepo') as string | undefined;
if (githubRepo) {
  new GithubOidcStack(app, 'TankmAzeGithubOidc', { env, githubRepo });
}

// Pool IDs are passed as context so ApiStack and FrontendStack hold no
// cross-stack CloudFormation imports from AuthStack. This lets us recreate
// the UserPool in AuthStack without hitting the "export in use" error.
// On first deploy after pool recreation, set these context values to the
// OLD pool IDs so the stacks deploy cleanly; then update the GitHub secrets
// with the new IDs and trigger a second deploy.
const userPoolId     = app.node.tryGetContext('userPoolId')     as string ?? '';
const userPoolClientId = app.node.tryGetContext('userPoolClientId') as string ?? '';

const auth    = new AuthStack(app, 'TankmAzeAuth', { env });
const storage = new StorageStack(app, 'TankmAzeStorage', { env });
const build   = new BuildStack(app, 'TankmAzeBuild', {
  env,
  wasmBucket: storage.wasmBucket,
  tables: storage.tables,
});
const api = new ApiStack(app, 'TankmAzeApi', {
  env,
  tables: storage.tables,
  wasmBucket: storage.wasmBucket,
  matchLogsBucket: storage.matchLogsBucket,
  codebuildProject: build.project,
  userPoolId,
  userPoolClientId,
});
const frontend = new FrontendStack(app, 'TankmAzeFrontend', {
  env,
  userPoolId,
  userPoolClientId,
  wsEndpoint: api.wsEndpoint,
  httpEndpoint: api.httpEndpoint,
});

// AuthStack must deploy AFTER ApiStack and FrontendStack so that those stacks
// can drop their old Fn::ImportValue references before AuthStack tries to
// delete (and recreate) the UserPool and its exported attributes.
auth.addDependency(api);
auth.addDependency(frontend);
