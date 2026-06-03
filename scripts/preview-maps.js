#!/usr/bin/env node
'use strict';

// Copies only the layout-generation code from map-seeder/index.js
// and prints each map as ASCII art.
// Run: node scripts/preview-maps.js

const N = 15;

function makeGrid(fn) {
  return Array.from({ length: N }, (_, r) =>
    Array.from({ length: N }, (_, c) => fn(r, c))
  );
}

function buildDoubleSpiral() {
  const g = makeGrid(() => false);
  const open = (r, c) => { g[r][c] = true; };
  for (let c = 1; c <= 12; c++) open(1,  c);
  for (let r = 2; r <= 11; r++) open(r,  12);
  for (let c = 4; c <= 12; c++) open(11, c);
  for (let r = 5; r <= 10; r++) open(r,  4);
  for (let c = 4; c <= 8;  c++) open(5,  c);
  for (let r = 6; r <= 7;  r++) open(r,  8);
  open(7, 7);
  for (let c = 2; c <= 13; c++) open(13, c);
  for (let r = 3; r <= 12; r++) open(r,  2);
  for (let c = 2; c <= 10; c++) open(3,  c);
  for (let r = 4; r <= 9;  r++) open(r,  10);
  for (let c = 6; c <= 10; c++) open(9,  c);
  for (let r = 7; r <= 8;  r++) open(r,  6);
  open(7, 7);
  return g;
}

const LAYOUTS = {
  'Open':          makeGrid((r, c) => r > 0 && r < N-1 && c > 0 && c < N-1),
  'Donut':         makeGrid((r, c) => {
                     if (r===0||r===N-1||c===0||c===N-1) return false;
                     return r===1||r===N-2||c===1||c===N-2;
                   }),
  'X':             makeGrid((r, c) => {
                     if (r===0||r===N-1||c===0||c===N-1) return false;
                     return Math.abs(r-c)<=1 || Math.abs(r+c-(N-1))<=1;
                   }),
  'Rooms':         (() => {
                     const g = makeGrid((r, c) => {
                       if (r===0||r===N-1||c===0||c===N-1) return false;
                       if (r===7||c===7) return false;
                       return true;
                     });
                     g[3][7]=true; g[11][7]=true; g[7][3]=true; g[7][11]=true;
                     return g;
                   })(),
  'Double Spiral': buildDoubleSpiral(),
};

for (const [name, grid] of Object.entries(LAYOUTS)) {
  console.log(`\n=== ${name} ===`);
  for (let r = 0; r < N; r++) {
    const row = grid[r].map((cell, c) => {
      if ((r === 1 && c === 1))  return 'A';   // SpawnA
      if ((r === 13 && c === 13)) return 'B';  // SpawnB
      return cell ? '·' : '█';
    }).join(' ');
    console.log(row);
  }
}
