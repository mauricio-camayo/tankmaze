import * as path from 'path';
import * as childProcess from 'child_process';
import { Stack, StackProps, CfnOutput, Duration, RemovalPolicy } from 'aws-cdk-lib';
import * as lambda from 'aws-cdk-lib/aws-lambda';
import * as iam from 'aws-cdk-lib/aws-iam';
import * as cognito from 'aws-cdk-lib/aws-cognito';
import * as codebuild from 'aws-cdk-lib/aws-codebuild';
import * as s3 from 'aws-cdk-lib/aws-s3';
import * as apigwv2 from 'aws-cdk-lib/aws-apigatewayv2';
import * as apigwv2integrations from 'aws-cdk-lib/aws-apigatewayv2-integrations';
import * as apigwv2authorizers from 'aws-cdk-lib/aws-apigatewayv2-authorizers';
import * as scheduler from 'aws-cdk-lib/aws-scheduler';
import { Construct } from 'constructs';
import { TableSet, tableEnvVars } from './storage-stack';

interface ApiStackProps extends StackProps {
  tables: TableSet;
  wasmBucket: s3.Bucket;
  matchLogsBucket: s3.Bucket;
  codebuildProject: codebuild.Project;
  userPool: cognito.UserPool;
  userPoolClient: cognito.UserPoolClient;
}

export class ApiStack extends Stack {
  readonly wsEndpoint: string;
  readonly httpEndpoint: string;

  constructor(scope: Construct, id: string, props: ApiStackProps) {
    super(scope, id, props);

    const { tables, wasmBucket, matchLogsBucket, codebuildProject, userPool, userPoolClient } = props;
    const backendDir = path.join(__dirname, '../../../backend');

    // ---- Helper: build a Go Lambda from cmd/<name> ----------------------

    const goLambda = (id: string, cmd: string, env: Record<string, string>): lambda.Function => {
      return new lambda.Function(this, id, {
        runtime: lambda.Runtime.PROVIDED_AL2023,
        architecture: lambda.Architecture.ARM_64,
        handler: 'bootstrap',
        code: lambda.Code.fromAsset(backendDir, {
          bundling: {
            image: lambda.Runtime.PROVIDED_AL2023.bundlingImage,
            local: {
              tryBundle(outDir: string): boolean {
                try {
                  childProcess.execSync(
                    `GOTOOLCHAIN=local GOOS=linux GOARCH=arm64 go build -tags lambda.norpc -o ${outDir}/bootstrap ./cmd/${cmd}`,
                    { cwd: backendDir, stdio: 'inherit' },
                  );
                  return true;
                } catch {
                  return false;
                }
              },
            },
            command: [
              'bash', '-c',
              `GOTOOLCHAIN=local GOOS=linux GOARCH=arm64 go build -tags lambda.norpc -o /asset-output/bootstrap ./cmd/${cmd}`,
            ],
          },
        }),
        environment: env,
        timeout: Duration.seconds(29),
        memorySize: 256,
      });
    };

    // ---- EventBridge Scheduler group -----------------------------------
    // Individual schedules (one per Game Day phase) are created at runtime
    // by the admin when configuring a Game Day.

    new scheduler.CfnScheduleGroup(this, 'SchedulerGroup', {
      name: 'tankmaze-gamedays',
    });

    // IAM role that EventBridge Scheduler assumes to invoke tournament-scheduler.
    const schedulerInvokeRole = new iam.Role(this, 'SchedulerInvokeRole', {
      assumedBy: new iam.ServicePrincipal('scheduler.amazonaws.com'),
    });

    // ---- Lambda functions ----------------------------------------------

    // ranking-updater — no external Lambda deps
    const rankingUpdater = goLambda('RankingUpdater', 'ranking-updater', {
      ...tableEnvVars(tables),
    });
    tables.rankings.grantReadWriteData(rankingUpdater);
    tables.tanks.grantReadWriteData(rankingUpdater);
    tables.gamedays.grantReadWriteData(rankingUpdater);

    // match-runner — needs WebSocket APIGW endpoint added after API is created
    const matchRunner = goLambda('MatchRunner', 'match-runner', {
      ...tableEnvVars(tables),
      WASM_BUCKET:        wasmBucket.bucketName,
      MATCH_LOGS_BUCKET:  matchLogsBucket.bucketName,
    });
    wasmBucket.grantRead(matchRunner);
    matchLogsBucket.grantWrite(matchRunner);
    tables.matches.grantReadWriteData(matchRunner);
    tables.connections.grantReadData(matchRunner);
    tables.tankVersions.grantReadWriteData(matchRunner);
    tables.gamedays.grantReadData(matchRunner);

    // tournament-scheduler
    const tournamentScheduler = goLambda('TournamentScheduler', 'tournament-scheduler', {
      ...tableEnvVars(tables),
      MATCH_RUNNER_FUNCTION:    matchRunner.functionArn,
      RANKING_UPDATER_FUNCTION: rankingUpdater.functionArn,
    });
    tables.gamedays.grantReadWriteData(tournamentScheduler);
    tables.matches.grantReadWriteData(tournamentScheduler);
    tables.tankVersions.grantReadData(tournamentScheduler);
    tables.connections.grantReadData(tournamentScheduler);
    matchRunner.grantInvoke(tournamentScheduler);
    rankingUpdater.grantInvoke(tournamentScheduler);
    tournamentScheduler.addToRolePolicy(new iam.PolicyStatement({
      actions: [
        'scheduler:CreateSchedule',
        'scheduler:GetSchedule',
        'scheduler:DeleteSchedule',
        'scheduler:UpdateSchedule',
      ],
      resources: [
        `arn:aws:scheduler:${this.region}:${this.account}:schedule/tankmaze-gamedays/*`,
      ],
    }));
    // Allow tournament-scheduler to pass the scheduler invoke role
    tournamentScheduler.addToRolePolicy(new iam.PolicyStatement({
      actions: ['iam:PassRole'],
      resources: [schedulerInvokeRole.roleArn],
    }));
    schedulerInvokeRole.addToPolicy(new iam.PolicyStatement({
      actions: ['lambda:InvokeFunction'],
      resources: [tournamentScheduler.functionArn],
    }));

    // wss-handler — needs WebSocket APIGW endpoint added after API is created
    const wssHandler = goLambda('WssHandler', 'wss-handler', {
      ...tableEnvVars(tables),
      MATCH_LOGS_BUCKET: matchLogsBucket.bucketName,
    });
    tables.connections.grantReadWriteData(wssHandler);
    tables.matches.grantReadData(wssHandler);
    tables.gamedays.grantReadData(wssHandler);
    matchLogsBucket.grantRead(wssHandler);

    // tank-api
    const tankApi = goLambda('TankApi', 'tank-api', {
      ...tableEnvVars(tables),
      WASM_BUCKET:           wasmBucket.bucketName,
      MATCH_LOGS_BUCKET:     matchLogsBucket.bucketName,
      CODEBUILD_PROJECT:     codebuildProject.projectName,
      MATCH_RUNNER_FUNCTION: matchRunner.functionArn,
      // SCOUT_TANK_ID, SCOUT_VERSION, BRUISER_TANK_ID, BRUISER_VERSION set via
      // SSM Parameter Store or manually after first deploy.
    });
    tables.tanks.grantReadWriteData(tankApi);
    tables.tankVersions.grantReadWriteData(tankApi);
    tables.matches.grantReadWriteData(tankApi);
    tables.gamedays.grantReadData(tankApi);
    tables.rankings.grantReadData(tankApi);
    tables.maps.grantReadWriteData(tankApi);
    wasmBucket.grantReadWrite(tankApi);
    matchLogsBucket.grantRead(tankApi);
    tankApi.addToRolePolicy(new iam.PolicyStatement({
      actions: ['codebuild:StartBuild'],
      resources: [codebuildProject.projectArn],
    }));
    matchRunner.grantInvoke(tankApi);
    // Allow pre-signed URL generation for match tick logs
    tankApi.addToRolePolicy(new iam.PolicyStatement({
      actions: ['s3:GetObject'],
      resources: [matchLogsBucket.arnForObjects('*')],
    }));

    // ---- WebSocket API (observer) --------------------------------------

    const wssApi = new apigwv2.WebSocketApi(this, 'WssApi', {
      apiName: 'tankmaze-wss',
      connectRouteOptions: {
        integration: new apigwv2integrations.WebSocketLambdaIntegration('WssConnect', wssHandler),
      },
      disconnectRouteOptions: {
        integration: new apigwv2integrations.WebSocketLambdaIntegration('WssDisconnect', wssHandler),
      },
      defaultRouteOptions: {
        integration: new apigwv2integrations.WebSocketLambdaIntegration('WssDefault', wssHandler),
      },
    });

    const wssStage = new apigwv2.WebSocketStage(this, 'WssStage', {
      webSocketApi: wssApi,
      stageName: 'prod',
      autoDeploy: true,
    });

    const apigwEndpoint = `https://${wssApi.apiId}.execute-api.${this.region}.amazonaws.com/${wssStage.stageName}`;
    wssHandler.addEnvironment('APIGW_ENDPOINT', apigwEndpoint);
    matchRunner.addEnvironment('APIGW_ENDPOINT', apigwEndpoint);

    // Grant manage-connections permission (posting to WebSocket clients)
    const wssExecuteArn = `arn:aws:execute-api:${this.region}:${this.account}:${wssApi.apiId}/*`;
    const wssPolicy = new iam.PolicyStatement({
      actions: ['execute-api:ManageConnections'],
      resources: [wssExecuteArn],
    });
    wssHandler.addToRolePolicy(wssPolicy);
    matchRunner.addToRolePolicy(wssPolicy);

    this.wsEndpoint = wssStage.url;

    // ---- HTTP API (REST) -----------------------------------------------

    const httpApi = new apigwv2.HttpApi(this, 'HttpApi', {
      apiName: 'tankmaze-http',
      corsPreflight: {
        allowOrigins: ['*'],
        allowMethods: [apigwv2.CorsHttpMethod.ANY],
        allowHeaders: ['Content-Type', 'Authorization'],
      },
    });

    const tankApiIntegration = new apigwv2integrations.HttpLambdaIntegration(
      'TankApiInteg',
      tankApi,
    );

    const jwtAuthorizer = new apigwv2authorizers.HttpJwtAuthorizer(
      'CognitoAuth',
      userPool.userPoolProviderUrl,
      { jwtAudience: [userPoolClient.userPoolClientId] },
    );

    // Public routes — no authorizer needed
    const publicPaths = [
      '/maps',
      '/rankings',
      '/gamedays/{gameDayId}',
    ];
    for (const p of publicPaths) {
      httpApi.addRoutes({
        path: p,
        methods: [apigwv2.HttpMethod.GET],
        integration: tankApiIntegration,
      });
    }

    // All other routes require a valid Cognito JWT.
    // Deliberately excludes OPTIONS so managed CORS handles preflight without auth.
    httpApi.addRoutes({
      path: '/{proxy+}',
      methods: [
        apigwv2.HttpMethod.GET,
        apigwv2.HttpMethod.POST,
        apigwv2.HttpMethod.PUT,
        apigwv2.HttpMethod.PATCH,
        apigwv2.HttpMethod.DELETE,
        apigwv2.HttpMethod.HEAD,
      ],
      integration: tankApiIntegration,
      authorizer: jwtAuthorizer,
    });

    this.httpEndpoint = httpApi.apiEndpoint;

    // ---- Outputs -------------------------------------------------------

    new CfnOutput(this, 'WssEndpoint',  { value: this.wsEndpoint });
    new CfnOutput(this, 'HttpEndpoint', { value: this.httpEndpoint });
    new CfnOutput(this, 'MatchRunnerFunctionArn',    { value: matchRunner.functionArn });
    new CfnOutput(this, 'RankingUpdaterFunctionArn', { value: rankingUpdater.functionArn });
    new CfnOutput(this, 'SchedulerInvokeRoleArn',    { value: schedulerInvokeRole.roleArn });
  }
}
