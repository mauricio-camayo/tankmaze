'use strict';

// Convention: true = open (passable), false = wall.
// Outer boundary is always wall; SpawnA = (1,1), SpawnB = (13,13).

const N = 15;

function makeGrid(fn) {
  return Array.from({ length: N }, (_, r) =>
    Array.from({ length: N }, (_, c) => fn(r, c))
  );
}

function buildDoubleSpiral() {
  const g = makeGrid(() => false);
  const open = (r, c) => { g[r][c] = true; };

  // Arm A: clockwise from SpawnA (1,1) — verticals at cols 12, 4, 8
  for (let c = 1; c <= 12; c++) open(1,  c);   // right across top     (row 1)
  for (let r = 2; r <= 11; r++) open(r,  12);  // down right side      (col 12)
  for (let c = 4; c <= 12; c++) open(11, c);   // left across level 2  (row 11)
  for (let r = 5; r <= 10; r++) open(r,  4);   // up level 2 left      (col 4)
  for (let c = 4; c <= 8;  c++) open(5,  c);   // right level 3        (row 5)
  for (let r = 6; r <= 7;  r++) open(r,  8);   // down to center area  (col 8)
  open(7, 7);                                   // step left to center

  // Arm B: counter-clockwise from SpawnB (13,13) — verticals at cols 2, 10, 6
  for (let c = 2; c <= 13; c++) open(13, c);   // left across bottom   (row 13)
  for (let r = 3; r <= 12; r++) open(r,  2);   // up left side         (col 2)
  for (let c = 2; c <= 10; c++) open(3,  c);   // right across level 2 (row 3)
  for (let r = 4; r <= 9;  r++) open(r,  10);  // down level 2 right   (col 10)
  for (let c = 6; c <= 10; c++) open(9,  c);   // left level 3         (row 9)
  for (let r = 7; r <= 8;  r++) open(r,  6);   // up to center area    (col 6)
  open(7, 7);                                   // center (shared junction)

  return g;
}

const LAYOUTS = {
  'builtin-open': makeGrid((r, c) =>
    r > 0 && r < N - 1 && c > 0 && c < N - 1
  ),

  'builtin-donut': makeGrid((r, c) => {
    if (r === 0 || r === N - 1 || c === 0 || c === N - 1) return false;
    return r === 1 || r === N - 2 || c === 1 || c === N - 2;
  }),

  'builtin-x': makeGrid((r, c) => {
    if (r === 0 || r === N - 1 || c === 0 || c === N - 1) return false;
    return Math.abs(r - c) <= 1 || Math.abs(r + c - (N - 1)) <= 1;
  }),

  'builtin-rooms': (() => {
    const g = makeGrid((r, c) => {
      if (r === 0 || r === N - 1 || c === 0 || c === N - 1) return false;
      if (r === 7 || c === 7) return false; // interior dividers
      return true;
    });
    // single-cell doorways at the midpoint of each divider segment
    g[3][7]  = true;   // midpoint of col-7 upper half  (rows 1–6)
    g[11][7] = true;   // midpoint of col-7 lower half  (rows 8–13)
    g[7][3]  = true;   // midpoint of row-7 left half   (cols 1–6)
    g[7][11] = true;   // midpoint of row-7 right half  (cols 8–13)
    return g;
  })(),

  'builtin-double-spiral': buildDoubleSpiral(),
};

const MAPS = [
  { mapId: 'builtin-open',          slug: 'open',          name: 'Open',          description: 'Wide open field with only boundary walls' },
  { mapId: 'builtin-donut',         slug: 'donut',         name: 'Donut',         description: 'A single ring corridor inside the boundary' },
  { mapId: 'builtin-x',             slug: 'x',             name: 'X',             description: 'Two diagonal paths crossing at the center' },
  { mapId: 'builtin-rooms',         slug: 'rooms',         name: 'Rooms',         description: 'Four corner rooms connected by narrow doorways' },
  { mapId: 'builtin-double-spiral', slug: 'double-spiral', name: 'Double Spiral', description: 'Two interlocked spirals meeting at the center' },
];

const OLD_MAP_IDS = [
  'builtin-catacombs',
  'builtin-labyrinth',
  'builtin-fortress',
  'builtin-abyss',
  'builtin-arena',
];

function layoutToDynamo(grid) {
  return {
    L: grid.map(row => ({
      L: row.map(cell => ({ BOOL: cell })),
    })),
  };
}

const {
  DynamoDBClient,
  GetItemCommand,
  PutItemCommand,
  DeleteItemCommand,
} = require('@aws-sdk/client-dynamodb');

const client = new DynamoDBClient({});

exports.handler = async () => {
  const table = process.env.TABLE_NAME;
  const NOW = Math.floor(Date.now() / 1000);

  // Remove old placeholder maps (idempotent — ignores missing items)
  for (const mapId of OLD_MAP_IDS) {
    try {
      await client.send(new DeleteItemCommand({
        TableName: table,
        Key: { mapId: { S: mapId } },
      }));
      console.log(`deleted old map ${mapId}`);
    } catch (e) {
      console.log(`skip delete ${mapId}: ${e.message}`);
    }
  }

  // Seed new maps (skip if already present)
  for (const map of MAPS) {
    const existing = await client.send(new GetItemCommand({
      TableName: table,
      Key: { mapId: { S: map.mapId } },
    }));
    if (existing.Item) {
      console.log(`map ${map.mapId} already exists, skipping`);
      continue;
    }
    await client.send(new PutItemCommand({
      TableName: table,
      Item: {
        mapId:       { S: map.mapId },
        slug:        { S: map.slug },
        name:        { S: map.name },
        description: { S: map.description },
        layout:      layoutToDynamo(LAYOUTS[map.mapId]),
        isBuiltIn:   { BOOL: true },
        isActive:    { BOOL: true },
        createdAt:   { N: String(NOW) },
      },
    }));
    console.log(`seeded map ${map.mapId}`);
  }
};
