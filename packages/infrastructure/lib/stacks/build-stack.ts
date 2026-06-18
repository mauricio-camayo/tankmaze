import { Stack, StackProps } from 'aws-cdk-lib';
import * as codebuild from 'aws-cdk-lib/aws-codebuild';
import * as ec2 from 'aws-cdk-lib/aws-ec2';
import * as iam from 'aws-cdk-lib/aws-iam';
import * as logs from 'aws-cdk-lib/aws-logs';
import * as s3 from 'aws-cdk-lib/aws-s3';
import { Construct } from 'constructs';
import { TableSet } from './storage-stack';

interface BuildStackProps extends StackProps {
  wasmBucket: s3.Bucket;
  tables: TableSet;
}

export class BuildStack extends Stack {
  readonly project: codebuild.Project;

  constructor(scope: Construct, id: string, props: BuildStackProps) {
    super(scope, id, props);

    // Isolated VPC — no internet egress, VPC gateway endpoints for S3 and DynamoDB.
    // This prevents compiled WASM from making outbound network calls at build time.
    const vpc = new ec2.Vpc(this, 'BuildVpc', {
      maxAzs: 2,
      natGateways: 0,
      subnetConfiguration: [
        {
          cidrMask: 24,
          name: 'isolated',
          subnetType: ec2.SubnetType.PRIVATE_ISOLATED,
        },
      ],
    });

    // Gateway endpoints allow the build container to reach S3 and DynamoDB
    // without traversing the public internet.
    vpc.addGatewayEndpoint('S3Endpoint', {
      service: ec2.GatewayVpcEndpointAwsService.S3,
    });
    vpc.addGatewayEndpoint('DynamoEndpoint', {
      service: ec2.GatewayVpcEndpointAwsService.DYNAMODB,
    });

    // Interface endpoint so CodeBuild can stream logs to CloudWatch from the
    // isolated VPC (no internet egress means the public endpoint is unreachable).
    vpc.addInterfaceEndpoint('CwLogsEndpoint', {
      service: ec2.InterfaceVpcEndpointAwsService.CLOUDWATCH_LOGS,
    });

    // CodeBuild IAM role
    const role = new iam.Role(this, 'TankCompilerRole', {
      assumedBy: new iam.ServicePrincipal('codebuild.amazonaws.com'),
    });
    props.wasmBucket.grantReadWrite(role);
    props.tables.tankVersions.grantWriteData(role);

    // Buildspec — downloads source from S3, builds WASM, uploads artifact.
    // Uses Go 1.21 from the standard managed image (highest available in STANDARD_7_0).
    // The SDK module is served from a GOPROXY file:// directory seeded in S3 so
    // no external module downloads are needed and no replace directives are required.
    const buildSpec = codebuild.BuildSpec.fromObject({
      version: '0.2',
      cache: {
        // Persist the Go build cache and module proxy between builds.
        // CodeBuild restores this from S3 before pre_build and saves it after post_build.
        paths: [
          '/root/.cache/go-build/**/*',
          '/tmp/goproxy/**/*',
        ],
      },
      phases: {
        install: {
          'runtime-versions': { golang: '1.21' },
        },
        pre_build: {
          commands: [
            // Seed the local GOPROXY from S3, but skip if a prior build cached it locally.
            'if [ ! -f /tmp/goproxy/github.com/tankmaze/sdk/@v/list ]; then aws s3 cp s3://$WASM_BUCKET/goproxy/ /tmp/goproxy/ --recursive; fi',

            // Download tank source
            'mkdir -p /tmp/build',
            'aws s3 cp s3://$WASM_BUCKET/$SOURCE_S3_KEY /tmp/build/main.go',

            // go.mod — no replace directive needed; GOPROXY resolves the SDK locally
            'printf "module tank\\ngo 1.21\\nrequire github.com/tankmaze/sdk v0.0.0\\n" > /tmp/build/go.mod',
          ],
        },
        build: {
          commands: [
            'cd /tmp/build',
            'CGO_ENABLED=0 GOOS=wasip1 GOARCH=wasm GOTOOLCHAIN=local GONOSUMDB=* GONOSUMCHECK=* GOPROXY=file:///tmp/goproxy go build -mod=mod -o /tmp/tank.wasm .',
            'SHA256=$(sha256sum /tmp/tank.wasm | awk \'{print $1}\')',
          ],
        },
        post_build: {
          commands: [
            [
              'if [ "$CODEBUILD_BUILD_SUCCEEDING" = "1" ]; then',
              '  aws s3api put-object --bucket $WASM_BUCKET --key $OUTPUT_WASM_KEY --body /tmp/tank.wasm --tagging "versionType=minor"',
              '  && aws dynamodb update-item',
              '    --table-name $TANK_VERSIONS_TABLE',
              '    --key "{\\"tankId\\":{\\"S\\":\\"$TANK_ID\\"},\\"version\\":{\\"S\\":\\"$VERSION\\"}}"',
              '    --update-expression "SET compileStatus = :s, wasmS3Key = :k, wasmSha256 = :h"',
              '    --expression-attribute-values "{\\":s\\":{\\"S\\":\\"ready\\"},\\":k\\":{\\"S\\":\\"$OUTPUT_WASM_KEY\\"},\\":h\\":{\\"S\\":\\"$SHA256\\"}}"',
              '    --region $AWS_DEFAULT_REGION',
              '  || aws dynamodb update-item',
              '    --table-name $TANK_VERSIONS_TABLE',
              '    --key "{\\"tankId\\":{\\"S\\":\\"$TANK_ID\\"},\\"version\\":{\\"S\\":\\"$VERSION\\"}}"',
              '    --update-expression "SET compileStatus = :s, compileError = :e"',
              '    --expression-attribute-values "{\\":s\\":{\\"S\\":\\"failed\\"},\\":e\\":{\\"S\\":\\"upload failed\\"}}"',
              '    --region $AWS_DEFAULT_REGION;',
              'else',
              '  ERR=$(cat /tmp/build_error.txt 2>/dev/null || echo "build failed");',
              '  aws dynamodb update-item',
              '    --table-name $TANK_VERSIONS_TABLE',
              '    --key "{\\"tankId\\":{\\"S\\":\\"$TANK_ID\\"},\\"version\\":{\\"S\\":\\"$VERSION\\"}}"',
              '    --update-expression "SET compileStatus = :s, compileError = :e"',
              '    --expression-attribute-values "{\\":s\\":{\\"S\\":\\"failed\\"},\\":e\\":{\\"S\\":\\"$ERR\\"}}"',
              '    --region $AWS_DEFAULT_REGION;',
              'fi',
            ].join(' '),
          ],
        },
      },
    });

    this.project = new codebuild.Project(this, 'TankCompiler', {
      projectName: 'tank-compiler',
      role,
      vpc,
      subnetSelection: { subnetType: ec2.SubnetType.PRIVATE_ISOLATED },
      buildSpec,
      // S3-backed build cache — persists Go build artifacts and the module proxy
      // between runs so warm compiles skip recompiling unchanged packages.
      cache: codebuild.Cache.bucket(props.wasmBucket, { prefix: 'codebuild-cache' }),
      environment: {
        buildImage: codebuild.LinuxBuildImage.STANDARD_7_0,
        computeType: codebuild.ComputeType.SMALL,
        environmentVariables: {
          WASM_BUCKET: { value: props.wasmBucket.bucketName },
        },
      },
      // TANK_ID, VERSION, SOURCE_S3_KEY, OUTPUT_WASM_KEY, TANK_VERSIONS_TABLE
      // are passed as per-build overrides by the tank-api Lambda.
    });
  }
}
