module github.com/tankmaze/backend

go 1.22.12

replace github.com/tankmaze/sdk => ../sdk

require (
	github.com/aws/aws-lambda-go v1.47.0
	github.com/aws/aws-sdk-go-v2 v1.38.2
	github.com/aws/aws-sdk-go-v2/config v1.27.43
	github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue v1.15.25
	github.com/aws/aws-sdk-go-v2/feature/dynamodb/expression v1.7.60
	github.com/aws/aws-sdk-go-v2/service/apigatewaymanagementapi v1.24.0
	github.com/aws/aws-sdk-go-v2/service/codebuild v1.60.0
	github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider v1.48.0
	github.com/aws/aws-sdk-go-v2/service/dynamodb v1.39.2
	github.com/aws/aws-sdk-go-v2/service/lambda v1.70.0
	github.com/aws/aws-sdk-go-v2/service/s3 v1.65.3
	github.com/aws/aws-sdk-go-v2/service/scheduler v1.12.0
	github.com/gorilla/websocket v1.5.3
	github.com/tankmaze/sdk v0.0.0-00010101000000-000000000000
	github.com/tetratelabs/wazero v1.7.3
)

require (
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.6.10 // indirect
	github.com/aws/aws-sdk-go-v2/credentials v1.17.41 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.16.17 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.5 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.5 // indirect
	github.com/aws/aws-sdk-go-v2/internal/ini v1.8.1 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.3.21 // indirect
	github.com/aws/aws-sdk-go-v2/service/dynamodbstreams v1.24.12 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.12.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.4.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/endpoint-discovery v1.10.8 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.12.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.18.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/ses v1.34.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.24.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.28.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.32.2 // indirect
	github.com/aws/smithy-go v1.23.0 // indirect
	github.com/jmespath/go-jmespath v0.4.0 // indirect
)
