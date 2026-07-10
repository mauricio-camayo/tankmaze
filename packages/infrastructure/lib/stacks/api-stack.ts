import * as path from 'path';
import * as childProcess from 'child_process';
import { Stack, StackProps, CfnOutput, Duration, RemovalPolicy } from 'aws-cdk-lib';
import * as lambda from 'aws-cdk-lib/aws-lambda';
import * as iam from 'aws-cdk-lib/aws-iam';
import * as codebuild from 'aws-cdk-lib/aws-codebuild';
import * as s3 from 'aws-cdk-lib/aws-s3';
import * as apigw from 'aws-cdk-lib/aws-apigateway';
import * as apigwv2 from 'aws-cdk-lib/aws-apigatewayv2';
import * as apigwv2integrations from 'aws-cdk-lib/aws-apigatewayv2-integrations';
import * as apigwv2authorizers from 'aws-cdk-lib/aws-apigatewayv2-authorizers';
import * as scheduler from 'aws-cdk-lib/aws-scheduler';
import * as cloudwatch from 'aws-cdk-lib/aws-cloudwatch';
import * as sqs from 'aws-cdk-lib/aws-sqs';
import * as logs from 'aws-cdk-lib/aws-logs';
import { Construct } from 'constructs';
import { TableSet, tableEnvVars } from './storage-stack';

interface ApiStackProps extends StackProps {
  tables: TableSet;
  wasmBucket: s3.Bucket;
  matchLogsBucket: s3.Bucket;
  tankAssetsBucket: s3.Bucket;
  codebuildProject: codebuild.Project;
  userPoolId: string;
  userPoolClientId: string;
  /** Domains to restrict CORS origins to (SEC-CORS). Defaults to '*' when not provided or empty. */
  frontendDomains?: string[];
}

export class ApiStack extends Stack {
  readonly wsEndpoint: string;
  readonly httpEndpoint: string;

  constructor(scope: Construct, id: string, props: ApiStackProps) {
    super(scope, id, props);

    const { tables, wasmBucket, matchLogsBucket, tankAssetsBucket, codebuildProject, userPoolId, userPoolClientId, frontendDomains } = props;
    const backendDir = path.join(__dirname, '../../../backend');

    // Resolve the correct Go binary. The system go may be older than what
    // go.work requires; prefer the cached toolchain if it exists.
    const go122 = '/home/macaco/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.22.12.linux-amd64/bin/go';
    const goBin = require('fs').existsSync(go122) ? go122 : (process.env.GO_BIN ?? 'go');

    // ---- Helper: build a Go Lambda from cmd/<name> ----------------------

    const goLambda = (id: string, cmd: string, env: Record<string, string>, timeoutSeconds = 29, memoryMB = 256): lambda.Function => {
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
                    `GOTOOLCHAIN=local GOOS=linux GOARCH=arm64 ${goBin} build -tags lambda.norpc -o ${outDir}/bootstrap ./cmd/${cmd}`,
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
        memorySize: memoryMB,
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
    // INFRA-SQS-ENC: encrypt DLQ at rest using AWS-managed KMS key.
    const schedulerDLQ = new sqs.Queue(this, 'SchedulerDLQ', {
      queueName: 'tankmaze-scheduler-dlq',
      retentionPeriod: Duration.days(14),
      encryption: sqs.QueueEncryption.KMS_MANAGED,
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
    const matchRunnerFunctionName = 'tankmaze-match-runner';

    // match-runner — needs WebSocket APIGW endpoint added after API is created
    // 300s: cold-start WASM JIT compilation can take 60-120s per module × 2;
    // warm containers reuse /tmp Wazero cache and finish in <40s total.
    // 1024 MB: two concurrent Go WASM instances (~17 MB min each) + Wazero JIT
    // compilation cache + match-runner Go runtime.
    const matchRunner = goLambda('MatchRunner', 'match-runner', {
      ...tableEnvVars(tables),
      WASM_BUCKET:                   wasmBucket.bucketName,
      MATCH_LOGS_BUCKET:             matchLogsBucket.bucketName,
      // Used by maybeAdvanceTournament to trigger the next phase when all
      // matches in a game day end — makes phase transitions event-driven.
      TOURNAMENT_SCHEDULER_FUNCTION: tournamentSchedulerArn,
    }, 300, 1024);
    wasmBucket.grantRead(matchRunner);
    matchLogsBucket.grantWrite(matchRunner);
    tables.matches.grantReadWriteData(matchRunner);
    tables.connections.grantReadData(matchRunner);
    tables.tanks.grantReadData(matchRunner);
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
      RANGER_TANK_ID:            'builtin-ranger',
      RANGER_VERSION:            'v1',
      RANDY_TANK_ID:             'builtin-randy',
      RANDY_VERSION:             'v1',
      MATCH_TTL_DAYS:            '7',
    }, 330);
    // Pin functions to stable names so they can be invoked by name from scripts.
    (matchRunner.node.defaultChild as lambda.CfnFunction).functionName = matchRunnerFunctionName;
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
    tables.tanks.grantReadData(wssHandler);
    tables.tankVersions.grantReadData(wssHandler);
    tables.gamedays.grantReadData(wssHandler);
    tables.maps.grantReadData(wssHandler);
    matchLogsBucket.grantRead(wssHandler);

    // tank-api
    const tankApi = goLambda('TankApi', 'tank-api', {
      ...tableEnvVars(tables),
      WASM_BUCKET:                   wasmBucket.bucketName,
      MATCH_LOGS_BUCKET:             matchLogsBucket.bucketName,
      TANK_ASSETS_BUCKET:            tankAssetsBucket.bucketName,
      CODEBUILD_PROJECT:             codebuildProject.projectName,
      MATCH_RUNNER_FUNCTION:         matchRunner.functionArn,
      SCOUT_TANK_ID:                 'builtin-scout',
      SCOUT_VERSION:                 'v1',
      BRUISER_TANK_ID:               'builtin-bruiser',
      BRUISER_VERSION:               'v1',
      RANGER_TANK_ID:                'builtin-ranger',
      RANGER_VERSION:                'v1',
      RANDY_TANK_ID:                 'builtin-randy',
      RANDY_VERSION:                 'v1',
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
    tables.platformConfig.grantReadWriteData(tankApi);
    tables.userSettings.grantReadWriteData(tankApi);
    tables.friendships.grantReadWriteData(tankApi);
    tables.messages.grantReadWriteData(tankApi);
    wasmBucket.grantReadWrite(tankApi);
    matchLogsBucket.grantRead(tankApi);
    tankAssetsBucket.grantWrite(tankApi);
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
        'cognito-idp:AdminUpdateUserAttributes',
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

    // forgot-password-worker — async-only sibling of tank-api's forgotPassword
    // handler (item 217). It has no API Gateway integration of its own, so
    // it's unreachable over HTTP at all, regardless of auth — the only way in
    // is tank-api's own lambda:InvokeFunction call, which is what keeps the
    // enumeration-safe 202 contract intact (no request ever reaches the real
    // lookup/branch logic synchronously).
    // sesSenderEmail stays unset until the SES domain identity created in
    // AuthStack (item 214) is DKIM-verified in Cloudflare; the worker no-ops
    // the IdP-notice-email branch until then.
    const sesSenderEmail = this.node.tryGetContext('sesSenderEmail') as string | undefined;
    const forgotPasswordWorker = goLambda('ForgotPasswordWorker', 'forgot-password-worker', {
      USER_POOL_ID:        userPoolId,
      USER_POOL_CLIENT_ID: userPoolClientId,
      SES_SENDER_EMAIL:    sesSenderEmail ?? '',
    });
    forgotPasswordWorker.addToRolePolicy(new iam.PolicyStatement({
      actions: ['cognito-idp:ListUsers', 'cognito-idp:ForgotPassword'],
      resources: [`arn:aws:cognito-idp:${this.region}:${this.account}:userpool/${userPoolId}`],
    }));
    // Scoped to '*' rather than the EmailIdentity's ARN to avoid a cross-stack
    // reference before it's verified; the code-level guard on an empty
    // sesSenderEmail keeps this inert until then.
    forgotPasswordWorker.addToRolePolicy(new iam.PolicyStatement({
      actions: ['ses:SendEmail', 'ses:SendRawEmail'],
      resources: ['*'],
    }));
    forgotPasswordWorker.grantInvoke(tankApi);
    tankApi.addEnvironment('FORGOT_PASSWORD_WORKER_FUNCTION', forgotPasswordWorker.functionArn);

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

    // INFRA-APIGW-LOG: account-level CloudWatch role required by API Gateway
    // before any stage can write access logs. This is a one-time account setting.
    const apigwCwRole = new iam.Role(this, 'ApiGwCwRole', {
      assumedBy: new iam.ServicePrincipal('apigateway.amazonaws.com'),
      managedPolicies: [
        iam.ManagedPolicy.fromAwsManagedPolicyName('service-role/AmazonAPIGatewayPushToCloudWatchLogs'),
      ],
    });
    const apigwAccount = new apigw.CfnAccount(this, 'ApiGwAccount', {
      cloudWatchRoleArn: apigwCwRole.roleArn,
    });

    // INFRA-APIGW-LOG: access logging for the WebSocket API stage.
    const wssAccessLogGroup = new logs.LogGroup(this, 'WssAccessLogs', {
      retention: logs.RetentionDays.ONE_MONTH,
      removalPolicy: RemovalPolicy.RETAIN,
    });

    const wssStage = new apigwv2.WebSocketStage(this, 'WssStage', {
      webSocketApi: wssApi,
      stageName: 'prod',
      autoDeploy: true,
    });
    // CDK L2 for WebSocketStage does not expose accessLogSettings natively;
    // use the underlying CfnStage to wire it.
    const wssL1 = wssStage.node.defaultChild as apigwv2.CfnStage;
    (wssL1 as any).accessLogSettings = {
      destinationArn: wssAccessLogGroup.logGroupArn,
      format: JSON.stringify({ requestId: '$context.requestId', ip: '$context.identity.sourceIp', routeKey: '$context.routeKey', status: '$context.status', connectionId: '$context.connectionId', requestTime: '$context.requestTime' }),
    };
    wssStage.node.addDependency(apigwAccount);

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

    // INFRA-APIGW-LOG: access logging for the HTTP API stage.
    const httpAccessLogGroup = new logs.LogGroup(this, 'HttpAccessLogs', {
      retention: logs.RetentionDays.ONE_MONTH,
      removalPolicy: RemovalPolicy.RETAIN,
    });

    // SEC-CORS: restrict allowed origins to the production frontend domain(s).
    // Falls back to '*' when frontendDomains is not provided (first deploy before
    // frontend stack exists). Set props.frontendDomains once the domain(s) are known.
    const allowOrigins = frontendDomains && frontendDomains.length > 0
      ? frontendDomains.map((d) => `https://${d}`)
      : ['*'];

    const httpApi = new apigwv2.HttpApi(this, 'HttpApi', {
      apiName: 'tankmaze-http',
      corsPreflight: {
        allowOrigins,
        allowMethods: [apigwv2.CorsHttpMethod.ANY],
        allowHeaders: ['Content-Type', 'Authorization'],
      },
    });

    // INFRA-APIGW-LOG: attach access log settings to the auto-created $default
    // stage via L1 escape hatch (createDefaultStage stays true so we don't
    // conflict with the existing stage resource in CloudFormation).
    const httpL1 = httpApi.defaultStage!.node.defaultChild as any;
    httpL1.accessLogSettings = {
      destinationArn: httpAccessLogGroup.logGroupArn,
      format: JSON.stringify({ requestId: '$context.requestId', ip: '$context.identity.sourceIp', routeKey: '$context.routeKey', status: '$context.status', responseLength: '$context.responseLength', requestTime: '$context.requestTime' }),
    };
    httpApi.defaultStage!.node.addDependency(apigwAccount);
    const httpDefaultStage = httpApi.defaultStage!;

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
      '/config/ads',
      '/users/{sub}',
    ];
    for (const p of publicPaths) {
      httpApi.addRoutes({
        path: p,
        methods: [apigwv2.HttpMethod.GET],
        integration: tankApiIntegration,
      });
    }
    // Public POST route — enumeration-safe forgot-password trigger (item 217).
    // No authorizer: this must be reachable by a not-yet-signed-in visitor.
    httpApi.addRoutes({
      path: '/auth/forgot-password',
      methods: [apigwv2.HttpMethod.POST],
      integration: tankApiIntegration,
    });

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
