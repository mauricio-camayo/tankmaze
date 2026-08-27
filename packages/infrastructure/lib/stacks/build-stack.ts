import { Duration, Stack, StackProps } from 'aws-cdk-lib';
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

    // INFRA-VPC-FLOW: flow logs provide forensic evidence of unexpected traffic
    // from the isolated build VPC (Trivy AWS-0178). Explicit retention on the
    // log group — the addFlowLog default never expires, which quietly
    // accrues storage cost forever.
    const flowLogGroup = new logs.LogGroup(this, 'FlowLogGroup', {
      retention: logs.RetentionDays.TWO_WEEKS,
    });
    vpc.addFlowLog('FlowLog', {
      destination: ec2.FlowLogDestination.toCloudWatchLogs(flowLogGroup),
      trafficType: ec2.FlowLogTrafficType.ALL,
    });

    // Gateway endpoints allow the build container to reach S3 and DynamoDB
    // without traversing the public internet. Gateway endpoints are free
    // (unlike interface endpoints, which bill per-AZ per-hour) — this is
    // also why CodeBuild logs are routed to S3 (below) instead of
    // CloudWatch Logs: it avoids needing a CLOUDWATCH_LOGS interface
    // endpoint (~$13/mo for 2 AZs) just so the isolated build container can
    // stream a few minutes of logs a day.
    vpc.addGatewayEndpoint('S3Endpoint', {
      service: ec2.GatewayVpcEndpointAwsService.S3,
    });
    vpc.addGatewayEndpoint('DynamoEndpoint', {
      service: ec2.GatewayVpcEndpointAwsService.DYNAMODB,
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

            // Download tank source into a subdirectory so we can add a package main
            // wrapper alongside it. The user code is package tank; the wrapper is
            // package main and imports tank/tank.
            'mkdir -p /tmp/build/tank',
            'aws s3 cp s3://$WASM_BUCKET/$SOURCE_S3_KEY /tmp/build/tank/source.go',

            // go.mod — no replace directive needed; GOPROXY resolves the SDK locally
            'printf "module tank\\ngo 1.21\\nrequire github.com/tankmaze/sdk v0.0.0\\n" > /tmp/build/go.mod',

            // Inject the package main wrapper that wires the host WASM imports to
            // the user's Tick/Config exports. Without this, go build produces a .a
            // archive (package tank is not main) instead of a WASM binary.
            `printf 'package main\\n\\nimport (\\n\\t"encoding/json"\\n\\t"unsafe"\\n\\n\\ttankmaze "github.com/tankmaze/sdk"\\n\\t"tank/tank"\\n)\\n\\n//go:wasmimport tankmaze sensors_get\\n//go:noescape\\nfunc sensorsGet(ptr unsafe.Pointer, cap int32) int32\\n\\n//go:wasmimport tankmaze config_register\\n//go:noescape\\nfunc configRegister(ptr unsafe.Pointer, length int32)\\n\\n//go:wasmimport tankmaze action_put\\nfunc actionPut(encoded int32)\\n\\nfunc encode(a tankmaze.Action) int32 { return int32(a.Type)*10 + int32(a.Direction) }\\n\\nvar cfgJSON = func() []byte { b, _ := json.Marshal(tank.Config); return b }()\\n\\nfunc main() {\\n\\tconfigRegister(unsafe.Pointer(&cfgJSON[0]), int32(len(cfgJSON)))\\n\\tbuf := make([]byte, 4096)\\n\\tfor {\\n\\t\\tn := sensorsGet(unsafe.Pointer(&buf[0]), int32(len(buf)))\\n\\t\\tif n < 0 {\\n\\t\\t\\treturn\\n\\t\\t}\\n\\t\\tvar s tankmaze.Sensors\\n\\t\\tif err := json.Unmarshal(buf[:n], &s); err != nil {\\n\\t\\t\\tactionPut(encode(tankmaze.Action{Type: tankmaze.Idle}))\\n\\t\\t\\tcontinue\\n\\t\\t}\\n\\t\\tactionPut(encode(tank.Tick(s)))\\n\\t}\\n}\\n' > /tmp/build/main.go`,
          ],
        },
        build: {
          commands: [
            'cd /tmp/build',
            'CGO_ENABLED=0 GOOS=wasip1 GOARCH=wasm GOTOOLCHAIN=local GONOSUMDB=* GONOSUMCHECK=* GOPROXY=file:///tmp/goproxy go build -mod=mod -o /tmp/tank.wasm . 2>/tmp/build_error.txt',
            'SHA256=$(sha256sum /tmp/tank.wasm | awk \'{print $1}\')',
          ],
        },
        post_build: {
          commands: [
            [
              'if [ "$CODEBUILD_BUILD_SUCCEEDING" = "1" ]; then',
              '  aws s3 cp /tmp/tank.wasm s3://$WASM_BUCKET/$OUTPUT_WASM_KEY',
              '  && aws s3api put-object-tagging --bucket $WASM_BUCKET --key $OUTPUT_WASM_KEY --tagging \'{"TagSet":[{"Key":"versionType","Value":"minor"}]}\'',
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
              '  ERR=$(cat /tmp/build_error.txt 2>/dev/null | tr \'\\n\\r\\t\' \'   \' | tr -d \'"\\\\\\\\\'  | cut -c1-500 || echo "build failed");',
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
      // Logs go to S3 (reachable via the free gateway endpoint above) instead
      // of CloudWatch Logs, so the isolated VPC needs no interface endpoint
      // for it. role already has read/write on the whole bucket.
      logging: {
        s3: {
          bucket: props.wasmBucket,
          prefix: 'build-logs',
        },
        cloudWatch: {
          enabled: false,
        },
      },
      environment: {
        buildImage: codebuild.LinuxBuildImage.STANDARD_7_0,
        computeType: codebuild.ComputeType.SMALL,
        environmentVariables: {
          WASM_BUCKET: { value: props.wasmBucket.bucketName },
        },
      },
      // Builds typically complete in 1–3 min (warm) or 3–5 min (cold).
      // 10-minute hard cap prevents runaway compiles from blocking the queue.
      timeout: Duration.minutes(10),
      // TANK_ID, VERSION, SOURCE_S3_KEY, OUTPUT_WASM_KEY, TANK_VERSIONS_TABLE
      // are passed as per-build overrides by the tank-api Lambda.
    });
  }
}
