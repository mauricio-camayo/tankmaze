import { App } from 'aws-cdk-lib';
import { AuthStack } from './stacks/auth-stack';
import { StorageStack } from './stacks/storage-stack';
import { BuildStack } from './stacks/build-stack';
import { ApiStack } from './stacks/api-stack';
import { FrontendStack } from './stacks/frontend-stack';

const app = new App();
const env = {
  account: process.env.CDK_DEFAULT_ACCOUNT,
  region: process.env.CDK_DEFAULT_REGION,
};

const auth = new AuthStack(app, 'TankmAzeAuth', { env });
const storage = new StorageStack(app, 'TankmAzeStorage', { env });
const build = new BuildStack(app, 'TankmAzeBuild', {
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
  userPool: auth.userPool,
  userPoolClient: auth.userPoolClient,
});
new FrontendStack(app, 'TankmAzeFrontend', {
  env,
  userPool: auth.userPool,
  userPoolClient: auth.userPoolClient,
  wsEndpoint: api.wsEndpoint,
  httpEndpoint: api.httpEndpoint,
});
