'use strict';

// Creates Tank + TankVersion records for the two built-in AI opponents.
// Uses fixed tankIds (builtin-scout, builtin-bruiser) so the operation is
// fully idempotent and the env vars wired to tank-api never change.

const { DynamoDBClient, GetItemCommand, PutItemCommand } = require('@aws-sdk/client-dynamodb');
const { S3Client, GetObjectCommand } = require('@aws-sdk/client-s3');
const { createHash } = require('crypto');

const dynamo = new DynamoDBClient({});
const s3client = new S3Client({});

const AI_TANKS = [
  {
    tankId:  'builtin-scout',
    name:    'Scout',
    version: 'v1',
    config:  { speed: 5, sensorRange: 3, damage: 2, armor: 2, fireRate: 3 },
    wasmKey: 'ai/scout/v1/tank.wasm',
    srcKey:  'ai/scout/v1/source.go',
  },
  {
    tankId:  'builtin-bruiser',
    name:    'Bruiser',
    version: 'v1',
    config:  { speed: 2, sensorRange: 2, damage: 5, armor: 5, fireRate: 1 },
    wasmKey: 'ai/bruiser/v1/tank.wasm',
    srcKey:  'ai/bruiser/v1/source.go',
  },
];

async function sha256OfS3Object(bucket, key) {
  const resp = await s3client.send(new GetObjectCommand({ Bucket: bucket, Key: key }));
  const hash = createHash('sha256');
  for await (const chunk of resp.Body) hash.update(chunk);
  return hash.digest('hex');
}

exports.handler = async () => {
  const tanksTable    = process.env.TANKS_TABLE;
  const versionsTable = process.env.TANK_VERSIONS_TABLE;
  const wasmBucket    = process.env.WASM_BUCKET;
  const NOW           = Math.floor(Date.now() / 1000);

  for (const t of AI_TANKS) {
    // Tank record (idempotent)
    const existingTank = await dynamo.send(new GetItemCommand({
      TableName: tanksTable,
      Key: { tankId: { S: t.tankId } },
    }));
    if (!existingTank.Item) {
      await dynamo.send(new PutItemCommand({
        TableName: tanksTable,
        Item: {
          tankId:        { S: t.tankId },
          userId:        { S: '__ai__' },
          name:          { S: t.name },
          globalScore:   { N: '0' },
          gameDaysCount: { N: '0' },
          lastActiveAt:  { N: String(NOW) },
          createdAt:     { N: String(NOW) },
        },
      }));
      console.log(`created tank ${t.tankId}`);
    } else {
      console.log(`tank ${t.tankId} already exists, skipping`);
    }

    // TankVersion record (idempotent)
    const existingVer = await dynamo.send(new GetItemCommand({
      TableName: versionsTable,
      Key: { tankId: { S: t.tankId }, version: { S: t.version } },
    }));
    if (!existingVer.Item) {
      const wasmSha256 = await sha256OfS3Object(wasmBucket, t.wasmKey);
      await dynamo.send(new PutItemCommand({
        TableName: versionsTable,
        Item: {
          tankId:        { S: t.tankId },
          version:       { S: t.version },
          versionType:   { S: 'major' },
          config:        { M: {
            speed:       { N: String(t.config.speed) },
            sensorRange: { N: String(t.config.sensorRange) },
            damage:      { N: String(t.config.damage) },
            armor:       { N: String(t.config.armor) },
            fireRate:    { N: String(t.config.fireRate) },
          }},
          wasmS3Key:     { S: t.wasmKey },
          sourceS3Key:   { S: t.srcKey },
          wasmSha256:    { S: wasmSha256 },
          compileStatus: { S: 'ready' },
          createdAt:     { N: String(NOW) },
        },
      }));
      console.log(`created version ${t.tankId}/${t.version} (sha256: ${wasmSha256.slice(0, 12)}…)`);
    } else {
      console.log(`version ${t.tankId}/${t.version} already exists, skipping`);
    }
  }
};
