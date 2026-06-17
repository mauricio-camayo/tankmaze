import * as path from 'path';
import * as childProcess from 'child_process';
import { Stack, StackProps, CfnOutput, Duration, RemovalPolicy } from 'aws-cdk-lib';
import * as lambda from 'aws-cdk-lib/aws-lambda';
import * as iam from 'aws-cdk-lib/aws-iam';
import * as codebuild from 'aws-cdk-lib/aws-codebuild';
import * as s3 from 'aws-cdk-lib/aws-s3';
import * as apigwv2 from 'aws-cdk-lib/aws-apigatewayv2';
import * as apigwv2integrations from 'aws-cdk-lib/aws-apigatewayv2-integrations';
import * as apigwv2authorizers from 'aws-cdk-lib/aws-apigatewayv2-authorizers';
import * as scheduler from 'aws-cdk-lib/aws-scheduler';
import * as cloudwatch from 'aws-cdk-lib/aws-cloudwatch';
import * as sqs from 'aws-cdk-lib/aws-sqs';
import { Construct } from 'constructs';
import { TableSet, tableEnvVars } from './storage-stack';

interface ApiStackProps extends StackProps {
  tables: TableSet;
  wasmBucket: s3.Bucket;
  matchLogsBucket: s3.Bucket;
  codebuildProject: codebuild.Project;
  userPoolId: string;
  userPoolClientId: string;
}

export class ApiStack extends Stack {
  readonly wsEndpoint: string;
  readonly httpEndpoint: string;

  constructor(scope: Construct, id: string, props: ApiStackProps) {
    super(scope, id, props);

    const { tables, wasmBucket, matchLogsBucket, codebuildProject, userPoolId, userPoolClientId } = props;
    const backendDir = path.join(__dirname, '../../../backend');

    // ---- Helper: build a Go Lambda from cmd/<name> ----------------------

    const goLambda = (id: string, cmd: string, env: Record<string, string>, timeoutSeconds = 29): lambda.Function => {
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
        timeout: Duration.seconds(timeoutSeconds),
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

    // DLQ for EventBridge Scheduler — failed invocations land here so they are
    // immediately visible via the alarm below (closes the observability gap from bug #119).
    const schedulerDLQ = new sqs.Queue(this, 'SchedulerDLQ', {
      queueName: 'tankmaze-scheduler-dlq',
      retentionPeriod: Duration.days(14),
    });
    schedulerDLQ.addToResourcePolicy(new iam.PolicyStatement({
      principals: [new iam.ServicePrincipal('scheduler.amazonaws.com')],
      actions: ['sqs:SendMessage'],
      resources: [schedulerDLQ.queueArn],
      conditions: {
        ArnEquals: {
          'aws:SourceArn': `arn:aws:scheduler:${this.region}:${this.account}:schedule/tankmaze-gamedays/*`,
        },
      },
    }));
    new cloudwatch.Alarm(this, 'SchedulerDLQAlarm', {
      alarmName: 'tankmaze-scheduler-dlq-depth',
      alarmDescription: 'EventBridge Scheduler failed to invoke tournament-scheduler — a game day phase was silently dropped',
      metric: schedulerDLQ.metricApproximateNumberOfMessagesVisible({
        period: Duration.minutes(5),
        statistic: 'Sum',
      }),
      threshold: 1,
      evaluationPeriods: 1,
      comparisonOperator: cloudwatch.ComparisonOperator.GREATER_THAN_OR_EQUAL_TO_THRESHOLD,
      treatMissingData: cloudwatch.TreatMissingData.NOT_BREACHING,
    });

    // ---- Lambda functions ----------------------------------------------

    // ranking-updater — no external Lambda deps
    const rankingUpdater = goLambda('RankingUpdater', 'ranking-updater', {
      ...tableEnvVars(tables),
    });
    tables.rankings.grantReadWriteData(rankingUpdater);
    tables.tanks.grantReadWriteData(rankingUpdater);
    tables.gamedays.grantReadWriteData(rankingUpdater);

    // tournament-scheduler function name/ARN declared early so match-runner can
    // reference it without creating a circular CDK dependency.
    // NOTE: this.formatArn() uses '/' as the default separator, producing
    // function/name — but Lambda ARNs require the colon form function:name.
    // Using a template literal avoids that bug without introducing a CDK token
    // dependency on the Lambda resource itself.
    const tournamentSchedulerFunctionName = 'tankmaze-tournament-scheduler';
    const tournamentSchedulerArn = `arn:aws:lambda:${this.region}:${this.account}:function:${tournamentSchedulerFunctionName}`;

    // match-runner — needs WebSocket APIGW endpoint added after API is created
    // 300s: cold-start WASM JIT compilation can take 60-120s per module × 2;
    // warm containers reuse /tmp Wazero cache and finish in <40s total.
    const matchRunner = goLambda('MatchRunner', 'match-runner', {
      ...tableEnvVars(tables),
      WASM_BUCKET:                   wasmBucket.bucketName,
      MATCH_LOGS_BUCKET:             matchLogsBucket.bucketName,
      // Used by maybeAdvanceTournament to trigger the next phase when all
      // matches in a game day end — makes phase transitions event-driven.
      TOURNAMENT_SCHEDULER_FUNCTION: tournamentSchedulerArn,
    }, 300);
    wasmBucket.grantRead(matchRunner);
    matchLogsBucket.grantWrite(matchRunner);
    tables.matches.grantReadWriteData(matchRunner);
    tables.connections.grantReadData(matchRunner);
    tables.tankVersions.grantReadWriteData(matchRunner);
    tables.gamedays.grantReadData(matchRunner);
    tables.maps.grantReadData(matchRunner);

    // tournament-scheduler
    // tournament-scheduler timeout: must exceed match-runner's 300s for the
    // synchronous championship final invocation plus overhead.
    const tournamentScheduler = goLambda('TournamentScheduler', 'tournament-scheduler', {
      ...tableEnvVars(tables),
      MATCH_RUNNER_FUNCTION:     matchRunner.functionArn,
      RANKING_UPDATER_FUNCTION:  rankingUpdater.functionArn,
      SCHEDULER_INVOKE_ROLE_ARN: schedulerInvokeRole.roleArn,
      SCHEDULER_DLQ_ARN:         schedulerDLQ.queueArn,
      TOURNAMENT_SCHEDULER_FUNCTION: tournamentSchedulerArn,
      SCOUT_TANK_ID:             'builtin-scout',
      SCOUT_VERSION:             'v1',
      BRUISER_TANK_ID:           'builtin-bruiser',
      BRUISER_VERSION:           'v1',
    }, 330);
    // Pin the actual function to the name we referenced above.
    (tournamentScheduler.node.defaultChild as lambda.CfnFunction).functionName = tournamentSchedulerFunctionName;
    tables.gamedays.grantReadWriteData(tournamentScheduler);
    tables.matches.grantReadWriteData(tournamentScheduler);
    tables.tanks.grantReadData(tournamentScheduler);
    tables.tankVersions.grantReadData(tournamentScheduler);
    tables.connections.grantReadData(tournamentScheduler);
    matchRunner.grantInvoke(tournamentScheduler);
    rankingUpdater.grantInvoke(tournamentScheduler);
    // IAM identity-based policy is sufficient for same-account Lambda invocations;
    // uses the pre-computed ARN string to avoid a circular CDK dependency.
    matchRunner.addToRolePolicy(new iam.PolicyStatement({
      actions: ['lambda:InvokeFunction'],
      resources: [tournamentSchedulerArn],
    }));

    // Allow the deployer IAM user to invoke these Lambdas directly — needed
    // for manual recovery when EventBridge-triggered runs stall.
    const deployerArn = `arn:aws:iam::${this.account}:user/deployer`;
    for (const fn of [tournamentScheduler, matchRunner, rankingUpdater]) {
      fn.addPermission('DeployerInvoke', {
        principal: new iam.ArnPrincipal(deployerArn),
        action: 'lambda:InvokeFunction',
      });
    }
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
      // Use the literal ARN (not a CDK token) to avoid a circular dependency:
      // schedulerInvokeRole policy → tournamentScheduler ← schedulerInvokeRole env var.
      resources: [tournamentSchedulerArn],
    }));

    // CloudWatch alarm: alert when tournament-scheduler Lambda errors so silent
    // phase-transition failures are immediately visible (root cause of bug #106).
    new cloudwatch.Alarm(this, 'TournamentSchedulerErrorAlarm', {
      alarmName: 'tankmaze-tournament-scheduler-errors',
      alarmDescription: 'tournament-scheduler Lambda returned an error — a Game Day phase may have silently failed to advance',
      metric: tournamentScheduler.metricErrors({
        period: Duration.minutes(5),
        statistic: 'Sum',
      }),
      threshold: 1,
      evaluationPeriods: 1,
      comparisonOperator: cloudwatch.ComparisonOperator.GREATER_THAN_OR_EQUAL_TO_THRESHOLD,
      treatMissingData: cloudwatch.TreatMissingData.NOT_BREACHING,
    });

    // wss-handler — needs WebSocket APIGW endpoint added after API is created
    const wssHandler = goLambda('WssHandler', 'wss-handler', {
      ...tableEnvVars(tables),
      MATCH_LOGS_BUCKET: matchLogsBucket.bucketName,
    });
    tables.connections.grantReadWriteData(wssHandler);
    tables.matches.grantReadData(wssHandler);
    tables.tankVersions.grantReadData(wssHandler);
    tables.gamedays.grantReadData(wssHandler);
    tables.maps.grantReadData(wssHandler);
    matchLogsBucket.grantRead(wssHandler);

    // tank-api
    const tankApi = goLambda('TankApi', 'tank-api', {
      ...tableEnvVars(tables),
      WASM_BUCKET:                   wasmBucket.bucketName,
      MATCH_LOGS_BUCKET:             matchLogsBucket.bucketName,
      CODEBUILD_PROJECT:             codebuildProject.projectName,
      MATCH_RUNNER_FUNCTION:         matchRunner.functionArn,
      SCOUT_TANK_ID:                 'builtin-scout',
      SCOUT_VERSION:                 'v1',
      BRUISER_TANK_ID:               'builtin-bruiser',
      BRUISER_VERSION:               'v1',
      USER_POOL_ID:                  userPoolId,
      SCHEDULER_INVOKE_ROLE_ARN:     schedulerInvokeRole.roleArn,
      SCHEDULER_DLQ_ARN:             schedulerDLQ.queueArn,
      TOURNAMENT_SCHEDULER_FUNCTION: tournamentScheduler.functionArn,
    });
    tables.tanks.grantReadWriteData(tankApi);
    tables.tankVersions.grantReadWriteData(tankApi);
    tables.matches.grantReadWriteData(tankApi);
    tables.gamedays.grantReadWriteData(tankApi);
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
    // Cognito admin operations for the admin panel
    tankApi.addToRolePolicy(new iam.PolicyStatement({
      actions: [
        'cognito-idp:ListUsers',
        'cognito-idp:ListUsersInGroup',
        'cognito-idp:AdminListGroupsForUser',
        'cognito-idp:AdminDisableUser',
        'cognito-idp:AdminEnableUser',
        'cognito-idp:AdminAddUserToGroup',
        'cognito-idp:AdminRemoveUserFromGroup',
        'cognito-idp:AdminDeleteUser',
      ],
      resources: [`arn:aws:cognito-idp:${this.region}:${this.account}:userpool/${userPoolId}`],
    }));

    // EventBridge Scheduler permissions for game day CRUD
    tankApi.addToRolePolicy(new iam.PolicyStatement({
      actions: [
        'scheduler:CreateSchedule',
        'scheduler:DeleteSchedule',
        'scheduler:GetSchedule',
      ],
      resources: [
        `arn:aws:scheduler:${this.region}:${this.account}:schedule/tankmaze-gamedays/*`,
      ],
    }));
    tankApi.addToRolePolicy(new iam.PolicyStatement({
      actions: ['iam:PassRole'],
      resources: [schedulerInvokeRole.roleArn],
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
      `https://cognito-idp.${this.region}.amazonaws.com/${userPoolId}`,
      { jwtAudience: [userPoolClientId] },
    );

    // Public routes — no authorizer needed
    const publicPaths = [
      '/maps',
      '/rankings',
      '/gamedays',
      '/gamedays/{gameDayId}',
      '/tanks/ai',
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
