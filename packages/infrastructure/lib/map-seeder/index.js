'use strict';

// Deterministic recursive-backtracking maze generator (15×15).
// All outer cells are walls; spawn points (1,1) and (13,13) are always open.
function generateMaze(seed) {
  const N = 15;
  const grid = Array.from({ length: N }, () => Array(N).fill(true));

  // LCG RNG seeded deterministically
  let s = (seed * 2654435761) >>> 0;
  function rand(n) {
    s = (Math.imul(s, 1664525) + 1013904223) >>> 0;
    return s % n;
  }

  function carve(r, c) {
    grid[r][c] = false;
    const dirs = [[0, 2], [0, -2], [2, 0], [-2, 0]];
    for (let i = dirs.length - 1; i > 0; i--) {
      const j = rand(i + 1);
      [dirs[i], dirs[j]] = [dirs[j], dirs[i]];
    }
    for (const [dr, dc] of dirs) {
      const nr = r + dr;
      const nc = c + dc;
      if (nr > 0 && nr < N - 1 && nc > 0 && nc < N - 1 && grid[nr][nc]) {
        grid[r + dr / 2][c + dc / 2] = false;
        carve(nr, nc);
      }
    }
  }

  carve(1, 1);
  return grid;
}

// Convert [][]bool to DynamoDB wire format: {L: [{L: [{BOOL: x}, ...]}, ...]}
function layoutToDynamo(grid) {
  return {
    L: grid.map(row => ({
      L: row.map(cell => ({ BOOL: cell })),
    })),
  };
}

const MAPS = [
  { mapId: 'builtin-catacombs', slug: 'catacombs', name: 'Catacombs',  description: 'Dense underground passages', seed: 1001 },
  { mapId: 'builtin-labyrinth', slug: 'labyrinth', name: 'Labyrinth',  description: 'A classic winding maze',     seed: 1337 },
  { mapId: 'builtin-fortress',  slug: 'fortress',  name: 'Fortress',   description: 'Fortified stone corridors',  seed: 2048 },
  { mapId: 'builtin-abyss',     slug: 'abyss',     name: 'Abyss',      description: 'Treacherous deep passages',  seed: 3141 },
  { mapId: 'builtin-arena',     slug: 'arena',     name: 'Arena',      description: 'A combat arena',             seed: 9999 },
];

const NOW = Math.floor(Date.now() / 1000);

const {
  DynamoDBClient,
  GetItemCommand,
  PutItemCommand,
} = require('@aws-sdk/client-dynamodb');

const client = new DynamoDBClient({});

exports.handler = async () => {
  const table = process.env.TABLE_NAME;
  for (const map of MAPS) {
    const existing = await client.send(new GetItemCommand({
      TableName: table,
      Key: { mapId: { S: map.mapId } },
    }));
    if (existing.Item) {
      console.log(`map ${map.mapId} already exists, skipping`);
      continue;
    }
    const layout = generateMaze(map.seed);
    await client.send(new PutItemCommand({
      TableName: table,
      Item: {
        mapId:       { S: map.mapId },
        slug:        { S: map.slug },
        name:        { S: map.name },
        description: { S: map.description },
        layout:      layoutToDynamo(layout),
        isBuiltIn:   { BOOL: true },
        isActive:    { BOOL: true },
        createdAt:   { N: String(NOW) },
      },
    }));
    console.log(`seeded map ${map.mapId}`);
  }
};
