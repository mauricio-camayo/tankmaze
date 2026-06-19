import Phaser from 'phaser';
import type { TankState, Projectile } from '../types';

const WALL   = 0x0a0a14;
const PATH   = 0x16213e;
const TANK_A = 0x60a5fa; // blue
const TANK_B = 0xf97316; // orange
const PROJ_A = 0x93c5fd;
const PROJ_B = 0xfed7aa;

// Rotation angle for each facing direction (East = 0)
const DIR_ROT: Record<string, number> = {
  E: 0,
  S: Math.PI / 2,
  W: Math.PI,
  N: -Math.PI / 2,
};

const DIR_VEC: Record<string, [number, number]> = {
  N: [0, -1],
  S: [0,  1],
  E: [1,  0],
  W: [-1, 0],
};

// Canvas fits inside this square; maze is centered within it
const CANVAS_SIZE = 560;
const BASE_CELL   = 28; // cell size tank sprites are drawn at

function makeTankSprite(
  scene: Phaser.Scene,
  color: number,
): Phaser.GameObjects.Container {
  // Body square
  const s = BASE_CELL;
  const body = scene.add.rectangle(0, 0, s * 0.72, s * 0.72, color);
  // Arrow pointing right (East) by default; rotated with the container
  const tip = s * 0.32, half = s * 0.18;
  const arrow = scene.add.triangle(
    0, 0,
    -tip, -half,
    -tip,  half,
     tip,  0,
    0xffffff, 0.85,
  );
  return scene.add.container(0, 0, [body, arrow]).setVisible(false);
}

export class ObserverScene extends Phaser.Scene {
  private cell    = BASE_CELL;
  private offsetX = 0;
  private offsetY = 0;

  private mazeGfx!: Phaser.GameObjects.Graphics;
  private projGfx!: Phaser.GameObjects.Graphics;
  private spA!: Phaser.GameObjects.Container;
  private spB!: Phaser.GameObjects.Container;
  private tankAId = '';

  constructor() {
    super({ key: 'ObserverScene' });
  }

  create() {
    this.cameras.main.setBackgroundColor(WALL);
    this.mazeGfx = this.add.graphics().setDepth(0);
    this.projGfx = this.add.graphics().setDepth(1);
    this.spA = makeTankSprite(this, TANK_A).setDepth(2);
    this.spB = makeTankSprite(this, TANK_B).setDepth(2);
  }

  initMaze(maze: boolean[][]) {
    const rows = maze.length;
    const cols = maze[0]?.length ?? 0;
    if (!rows || !cols) return;

    this.cell = Math.floor(Math.min(CANVAS_SIZE / rows, CANVAS_SIZE / cols));
    const mazeW = cols * this.cell;
    const mazeH = rows * this.cell;
    this.offsetX = Math.floor((CANVAS_SIZE - mazeW) / 2);
    this.offsetY = Math.floor((CANVAS_SIZE - mazeH) / 2);

    this.mazeGfx.clear();
    // Fill canvas background
    this.mazeGfx.fillStyle(WALL, 1);
    this.mazeGfx.fillRect(0, 0, CANVAS_SIZE, CANVAS_SIZE);

    for (let r = 0; r < rows; r++) {
      for (let c = 0; c < cols; c++) {
        this.mazeGfx.fillStyle(maze[r][c] ? WALL : PATH, 1);
        this.mazeGfx.fillRect(
          this.offsetX + c * this.cell + 1,
          this.offsetY + r * this.cell + 1,
          this.cell - 2,
          this.cell - 2,
        );
      }
    }

    const sc = this.cell / BASE_CELL;
    this.spA.setScale(sc);
    this.spB.setScale(sc);
  }

  render(
    stateA: TankState,
    stateB: TankState,
    projectiles: Projectile[],
    tankAId: string,
  ) {
    this.tankAId = tankAId;
    this.placeTank(this.spA, stateA);
    this.placeTank(this.spB, stateB);
    this.drawProjectiles(projectiles);
  }

  private placeTank(c: Phaser.GameObjects.Container, s: TankState) {
    c.setPosition(
      this.offsetX + (s.position.x + 0.5) * this.cell,
      this.offsetY + (s.position.y + 0.5) * this.cell,
    );
    c.setRotation(DIR_ROT[s.facing] ?? 0);
    c.setAlpha(0.3 + (s.hp / 100) * 0.7);
    c.setVisible(true);
  }

  private drawProjectiles(projs: Projectile[]) {
    this.projGfx.clear();
    const r = this.cell * 0.15;
    const tracerLen = this.cell * 0.4;
    for (const p of projs) {
      const color = p.ownerTankId === this.tankAId ? PROJ_A : PROJ_B;
      const cx = this.offsetX + (p.position.x + 0.5) * this.cell;
      const cy = this.offsetY + (p.position.y + 0.5) * this.cell;
      this.projGfx.fillStyle(color, 1);
      this.projGfx.fillCircle(cx, cy, r);
      const [dx, dy] = DIR_VEC[p.direction] ?? [0, -1];
      this.projGfx.lineStyle(2, color, 0.7);
      this.projGfx.beginPath();
      this.projGfx.moveTo(cx, cy);
      this.projGfx.lineTo(cx + dx * tracerLen, cy + dy * tracerLen);
      this.projGfx.strokePath();
    }
  }

  flashHit(side: 'a' | 'b') {
    const sprite = side === 'a' ? this.spA : this.spB;
    this.tweens.add({
      targets: sprite,
      alpha: { from: sprite.alpha, to: 0.05 },
      duration: 60,
      yoyo: true,
    });
  }
}
