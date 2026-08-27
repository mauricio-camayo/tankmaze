import * as crypto from 'crypto';
import * as fs from 'fs';
import * as path from 'path';
import { Stack, StackProps, RemovalPolicy, Duration } from 'aws-cdk-lib';
import * as dynamodb from 'aws-cdk-lib/aws-dynamodb';
import * as s3 from 'aws-cdk-lib/aws-s3';
import * as s3deploy from 'aws-cdk-lib/aws-s3-deployment';
import * as lambda from 'aws-cdk-lib/aws-lambda';
import * as logs from 'aws-cdk-lib/aws-logs';
import * as iam from 'aws-cdk-lib/aws-iam';
import { Trigger } from 'aws-cdk-lib/triggers';
import { Construct } from 'constructs';

// Env var names read by db.New() — keep in sync with internal/db/db.go
export interface TableSet {
  tanks: dynamodb.Table;
  tankVersions: dynamodb.Table;
  matches: dynamodb.Table;
  connections: dynamodb.Table;
  gamedays: dynamodb.Table;
  gamedaySeries: dynamodb.Table;
  rankings: dynamodb.Table;
  maps: dynamodb.Table;
  platformConfig: dynamodb.Table;
  userSettings: dynamodb.Table;
  friendships: dynamodb.Table;
  messages: dynamodb.Table;
}

// Env var map for Lambda functions
export function tableEnvVars(t: TableSet): Record<string, string> {
  return {
    TANKS_TABLE:            t.tanks.tableName,
    TANK_VERSIONS_TABLE:    t.tankVersions.tableName,
    MATCHES_TABLE:          t.matches.tableName,
    CONNECTIONS_TABLE:      t.connections.tableName,
    GAMEDAYS_TABLE:         t.gamedays.tableName,
    GAMEDAY_SERIES_TABLE:   t.gamedaySeries.tableName,
    RANKINGS_TABLE:         t.rankings.tableName,
    MAPS_TABLE:             t.maps.tableName,
    PLATFORM_CONFIG_TABLE:  t.platformConfig.tableName,
    USER_SETTINGS_TABLE:    t.userSettings.tableName,
    FRIENDSHIPS_TABLE:      t.friendships.tableName,
    MESSAGES_TABLE:         t.messages.tableName,
  };
}

export class StorageStack extends Stack {
  readonly tables: TableSet;
  readonly wasmBucket: s3.Bucket;
  readonly matchLogsBucket: s3.Bucket;
  readonly tankAssetsBucket: s3.Bucket;

  constructor(scope: Construct, id: string, props: StackProps) {
    super(scope, id, props);

    // ---- DynamoDB tables -----------------------------------------------

    const tanks = new dynamodb.Table(this, 'TanksTable', {
      tableName: 'tankmaze-tanks',
      partitionKey: { name: 'tankId', type: dynamodb.AttributeType.STRING },
      billingMode: dynamodb.BillingMode.PAY_PER_REQUEST,
      pointInTimeRecovery: true,
      timeToLiveAttribute: 'ttl',
      removalPolicy: RemovalPolicy.RETAIN,
    });
    tanks.addGlobalSecondaryIndex({
      indexName: 'userId-index',
      partitionKey: { name: 'userId', type: dynamodb.AttributeType.STRING },
      projectionType: dynamodb.ProjectionType.ALL,
    });

    const tankVersions = new dynamodb.Table(this, 'TankVersionsTable', {
      tableName: 'tankmaze-tank-versions',
      partitionKey: { name: 'tankId', type: dynamodb.AttributeType.STRING },
      sortKey:      { name: 'version', type: dynamodb.AttributeType.STRING },
      billingMode: dynamodb.BillingMode.PAY_PER_REQUEST,
      pointInTimeRecovery: true,
      timeToLiveAttribute: 'ttl',
      removalPolicy: RemovalPolicy.RETAIN,
    });

    const matches = new dynamodb.Table(this, 'MatchesTable', {
      tableName: 'tankmaze-matches',
      partitionKey: { name: 'matchId', type: dynamodb.AttributeType.STRING },
      billingMode: dynamodb.BillingMode.PAY_PER_REQUEST,
      pointInTimeRecovery: true,
      timeToLiveAttribute: 'ttl',
      removalPolicy: RemovalPolicy.RETAIN,
    });

    const connections = new dynamodb.Table(this, 'ConnectionsTable', {
      tableName: 'tankmaze-connections',
      partitionKey: { name: 'connectionId', type: dynamodb.AttributeType.STRING },
      billingMode: dynamodb.BillingMode.PAY_PER_REQUEST,
      pointInTimeRecovery: true,
      timeToLiveAttribute: 'ttl',
      removalPolicy: RemovalPolicy.RETAIN,
    });
    connections.addGlobalSecondaryIndex({
      indexName: 'matchId-index',
      partitionKey: { name: 'matchId', type: dynamodb.AttributeType.STRING },
      projectionType: dynamodb.ProjectionType.ALL,
    });

    const gamedays = new dynamodb.Table(this, 'GamedaysTable', {
      tableName: 'tankmaze-gamedays',
      partitionKey: { name: 'gameDayId', type: dynamodb.AttributeType.STRING },
      billingMode: dynamodb.BillingMode.PAY_PER_REQUEST,
      pointInTimeRecovery: true,
      timeToLiveAttribute: 'ttl',
      removalPolicy: RemovalPolicy.RETAIN,
    });

    const gamedaySeries = new dynamodb.Table(this, 'GamedaySeriesTable', {
      tableName: 'tankmaze-gameday-series',
      partitionKey: { name: 'seriesId', type: dynamodb.AttributeType.STRING },
      billingMode: dynamodb.BillingMode.PAY_PER_REQUEST,
      pointInTimeRecovery: true,
      removalPolicy: RemovalPolicy.RETAIN,
    });

    const rankings = new dynamodb.Table(this, 'RankingsTable', {
      tableName: 'tankmaze-rankings',
      partitionKey: { name: 'tankId',    type: dynamodb.AttributeType.STRING },
      sortKey:      { name: 'gameDayId', type: dynamodb.AttributeType.STRING },
      billingMode: dynamodb.BillingMode.PAY_PER_REQUEST,
      pointInTimeRecovery: true,
      timeToLiveAttribute: 'ttl',
      removalPolicy: RemovalPolicy.RETAIN,
    });

    const maps = new dynamodb.Table(this, 'MapsTable', {
      tableName: 'tankmaze-maps',
      partitionKey: { name: 'mapId', type: dynamodb.AttributeType.STRING },
      billingMode: dynamodb.BillingMode.PAY_PER_REQUEST,
      pointInTimeRecovery: true,
      timeToLiveAttribute: 'ttl',
      removalPolicy: RemovalPolicy.RETAIN,
    });

    const platformConfig = new dynamodb.Table(this, 'PlatformConfigTable', {
      tableName: 'tankmaze-platform-config',
      partitionKey: { name: 'configKey', type: dynamodb.AttributeType.STRING },
      billingMode: dynamodb.BillingMode.PAY_PER_REQUEST,
      removalPolicy: RemovalPolicy.RETAIN,
    });

    const userSettings = new dynamodb.Table(this, 'UserSettingsTable', {
      tableName: 'tankmaze-user-settings',
      partitionKey: { name: 'userId', type: dynamodb.AttributeType.STRING },
      billingMode: dynamodb.BillingMode.PAY_PER_REQUEST,
      removalPolicy: RemovalPolicy.RETAIN,
    });

    // Friendships (item 223) — a friendship is stored as a pair of items, one
    // per direction (userId=A,friendId=B) and (userId=B,friendId=A), so a
    // lookup from either side is a single GetItem/Query against that user's
    // own partition. See internal/db/friendships.go.
    const friendships = new dynamodb.Table(this, 'FriendshipsTable', {
      tableName: 'tankmaze-friendships',
      partitionKey: { name: 'userId', type: dynamodb.AttributeType.STRING },
      sortKey:      { name: 'friendId', type: dynamodb.AttributeType.STRING },
      billingMode: dynamodb.BillingMode.PAY_PER_REQUEST,
      pointInTimeRecovery: true,
      removalPolicy: RemovalPolicy.RETAIN,
    });

    // Messages (item 223 Part 2) — keyed by a stable conversation ID (the
    // two participants' user IDs, sorted and joined), sorted by a
    // lexicographically-time-ordered messageId so a Query naturally returns
    // history in order and supports an efficient "since" cursor for
    // polling. 30-day retention via TTL, no manual delete UI (per the
    // item's resolved retention question).
    const messages = new dynamodb.Table(this, 'MessagesTable', {
      tableName: 'tankmaze-messages',
      partitionKey: { name: 'conversationId', type: dynamodb.AttributeType.STRING },
      sortKey:      { name: 'messageId', type: dynamodb.AttributeType.STRING },
      billingMode: dynamodb.BillingMode.PAY_PER_REQUEST,
      pointInTimeRecovery: true,
      timeToLiveAttribute: 'ttl',
      removalPolicy: RemovalPolicy.RETAIN,
    });

    this.tables = { tanks, tankVersions, matches, connections, gamedays, gamedaySeries, rankings, maps, platformConfig, userSettings, friendships, messages };

    // ---- S3 buckets ----------------------------------------------------

    this.wasmBucket = new s3.Bucket(this, 'WasmArtifacts', {
      bucketName: `tankmaze-wasm-artifacts-${this.account}`,
      blockPublicAccess: s3.BlockPublicAccess.BLOCK_ALL,
      enforceSSL: true,
      removalPolicy: RemovalPolicy.RETAIN,
      versioned: false,
      lifecycleRules: [
        {
          id: 'expire-minor-wasm',
          tagFilters: { versionType: 'minor' },
          expiration: Duration.days(90),
        },
      ],
    });

    // Deploy the GOPROXY-format directory for github.com/tankmaze/sdk@v0.0.0 so
    // CodeBuild can resolve the SDK module via GOPROXY=file:///tmp/goproxy without
    // any internet egress.
    new s3deploy.BucketDeployment(this, 'SdkProxyDeployment', {
      sources: [s3deploy.Source.asset(path.join(__dirname, '../../lib/sdk-proxy'))],
      destinationBucket: this.wasmBucket,
      destinationKeyPrefix: 'goproxy/',
      prune: false,
    });

    this.matchLogsBucket = new s3.Bucket(this, 'MatchLogs', {
      bucketName: `tankmaze-match-logs-${this.account}`,
      blockPublicAccess: s3.BlockPublicAccess.BLOCK_ALL,
      enforceSSL: true,
      removalPolicy: RemovalPolicy.RETAIN,
      lifecycleRules: [
        {
          id: 'expire-match-logs',
          expiration: Duration.days(7),
        },
        {
          // Item 35 (match data export): decompressed export copies are
          // generated on demand under exports/<matchId>.json and don't need
          // to outlive the download — expire well short of the 7-day source
          // tick log window so a stale export never outlives the ability to
          // regenerate it.
          id: 'expire-match-exports',
          prefix: 'exports/',
          expiration: Duration.hours(24),
        },
      ],
    });

    // User-uploaded tank avatars (item 158). Public-read is scoped to the
    // tank-avatars/ prefix only via a bucket policy, not a blanket
    // BlockPublicAccess override — served directly by S3 (no CloudFront
    // origin), since <img> tags don't need CORS for simple resource loads.
    this.tankAssetsBucket = new s3.Bucket(this, 'TankAssets', {
      bucketName: `tankmaze-tank-assets-${this.account}`,
      blockPublicAccess: new s3.BlockPublicAccess({
        blockPublicAcls: true,
        ignorePublicAcls: true,
        blockPublicPolicy: false,
        restrictPublicBuckets: false,
      }),
      enforceSSL: true,
      removalPolicy: RemovalPolicy.RETAIN,
    });
    this.tankAssetsBucket.addToResourcePolicy(new iam.PolicyStatement({
      sid: 'PublicReadTankAvatars',
      effect: iam.Effect.ALLOW,
      principals: [new iam.AnyPrincipal()],
      actions: ['s3:GetObject'],
      resources: [this.tankAssetsBucket.arnForObjects('tank-avatars/*')],
    }));
    // Same bucket, separate prefix, for user profile pictures (item 198) —
    // reuses item 158's upload pattern rather than a second bucket.
    this.tankAssetsBucket.addToResourcePolicy(new iam.PolicyStatement({
      sid: 'PublicReadUserAvatars',
      effect: iam.Effect.ALLOW,
      principals: [new iam.AnyPrincipal()],
      actions: ['s3:GetObject'],
      resources: [this.tankAssetsBucket.arnForObjects('user-avatars/*')],
    }));

    // ---- Built-in map seeder -------------------------------------------

    const mapSeederFn = new lambda.Function(this, 'MapSeeder', {
      runtime: lambda.Runtime.NODEJS_22_X,
      handler: 'index.handler',
      code: lambda.Code.fromAsset(path.join(__dirname, '../../lib/map-seeder')),
      environment: { TABLE_NAME: maps.tableName },
      timeout: Duration.seconds(60),
      logRetention: logs.RetentionDays.TWO_WEEKS,
    });
    maps.grantWriteData(mapSeederFn);
    maps.grantReadData(mapSeederFn);

    new Trigger(this, 'MapSeedTrigger', {
      handler: mapSeederFn,
      executeAfter: [maps],
    });

    // ---- AI tank WASM + source deployment ------------------------------
    // Uploads pre-compiled scout/bruiser binaries and their Go source to
    // the wasm-artifacts bucket under the ai/ prefix.

    const aiTanksDeploy = new s3deploy.BucketDeployment(this, 'AiTanksDeployment', {
      sources: [s3deploy.Source.asset(path.join(__dirname, '../../lib/ai-tanks'))],
      destinationBucket: this.wasmBucket,
      destinationKeyPrefix: 'ai/',
      prune: false,
    });

    // ---- AI tank DB seeder --------------------------------------------

    // The CDK Trigger fires only when the seeder Lambda's code asset changes.
    // Write a sentinel file containing the combined WASM hash so that when CI
    // recompiles the non-deterministic Go WASMs the asset changes, forcing the
    // Trigger to re-run and update wasmSha256 in DynamoDB.
    const seederDir = path.join(__dirname, '../../lib/tank-seeder');
    const wasmHasher = crypto.createHash('sha256');
    for (const name of ['scout', 'bruiser', 'ranger', 'randy']) {
      const wasmPath = path.join(__dirname, `../../lib/ai-tanks/${name}/v1/tank.wasm`);
      if (fs.existsSync(wasmPath)) wasmHasher.update(fs.readFileSync(wasmPath));
    }
    fs.writeFileSync(
      path.join(seederDir, 'wasm-hash.json'),
      JSON.stringify({ wasmContentHash: wasmHasher.digest('hex') }),
    );

    const tankSeederFn = new lambda.Function(this, 'TankSeeder', {
      runtime: lambda.Runtime.NODEJS_22_X,
      handler: 'index.handler',
      code: lambda.Code.fromAsset(path.join(__dirname, '../../lib/tank-seeder')),
      environment: {
        TANKS_TABLE:         tanks.tableName,
        TANK_VERSIONS_TABLE: tankVersions.tableName,
        WASM_BUCKET:         this.wasmBucket.bucketName,
      },
      timeout: Duration.seconds(60),
      logRetention: logs.RetentionDays.TWO_WEEKS,
    });
    tanks.grantReadWriteData(tankSeederFn);
    tankVersions.grantReadWriteData(tankSeederFn);
    this.wasmBucket.grantRead(tankSeederFn);

    new Trigger(this, 'TankSeedTrigger', {
      handler: tankSeederFn,
      executeAfter: [tanks, tankVersions, aiTanksDeploy],
    });
  }
}
