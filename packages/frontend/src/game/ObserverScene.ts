import Phaser from 'phaser';
import type { TankState, Projectile } from '../types';

const WALL   = 0x0a0a14;
const PATH   = 0x16213e;
const TANK_A = 0x60a5fa; // blue
const TANK_B = 0xf97316; // orange
const PROJ_A = 0x93c5fd;
const PROJ_B = 0xfed7aa;

// Rotation angle for each facing direction (East = 0; sprites face right by default)
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
const BASE_CELL   = 28;

// All built-in avatar texture keys (preloaded once)
const AVATAR_KEYS = Array.from({ length: 16 }, (_, i) => `avatar-tank-${i}`);

function avatarKey(url: string): string | null {
  const m = url.match(/tank-(\d+)\.png$/);
  return m ? `avatar-tank-${m[1]}` : null;
}

function makeFallbackSprite(
  scene: Phaser.Scene,
  color: number,
): Phaser.GameObjects.Container {
  const s = BASE_CELL;
  const body = scene.add.rectangle(0, 0, s * 0.72, s * 0.72, color);
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

type TankObject =
  | { kind: 'sprite'; obj: Phaser.GameObjects.Image }
  | { kind: 'fallback'; obj: Phaser.GameObjects.Container };

export class ObserverScene extends Phaser.Scene {
  private cell    = BASE_CELL;
  private offsetX = 0;
  private offsetY = 0;

  private mazeGfx!: Phaser.GameObjects.Graphics;
  private sensorGfx!: Phaser.GameObjects.Graphics;
  private projGfx!: Phaser.GameObjects.Graphics;

  private tankA!: TankObject;
  private tankB!: TankObject;
  private tankAId = '';

  constructor() {
    super({ key: 'ObserverScene' });
  }

  preload() {
    for (let i = 0; i < 16; i++) {
      this.load.image(`avatar-tank-${i}`, `/avatars/tank-${i}.png`);
    }
  }

  create() {
    this.cameras.main.setBackgroundColor(WALL);
    this.mazeGfx = this.add.graphics().setDepth(0);
    this.sensorGfx = this.add.graphics().setDepth(1);
    this.projGfx = this.add.graphics().setDepth(2);
    this.tankA = { kind: 'fallback', obj: makeFallbackSprite(this, TANK_A).setDepth(3) };
    this.tankB = { kind: 'fallback', obj: makeFallbackSprite(this, TANK_B).setDepth(3) };
  }

  /** Called from Watch.tsx after the MATCH_SNAPSHOT arrives with avatarUrls. */
  setAvatarURLs(urlA: string | undefined, urlB: string | undefined) {
    this.tankA = this.buildTankObject(urlA, TANK_A, this.tankA);
    this.tankB = this.buildTankObject(urlB, TANK_B, this.tankB);
    // Re-scale to current cell size
    const sc = this.cell / BASE_CELL;
    this.scaleTank(this.tankA, sc);
    this.scaleTank(this.tankB, sc);
  }

  private buildTankObject(
    url: string | undefined,
    fallbackColor: number,
    existing: TankObject,
  ): TankObject {
    const key = url ? avatarKey(url) : null;
    if (key && this.textures.exists(key)) {
      // Reuse existing sprite if key matches, otherwise swap
      if (existing.kind === 'sprite') {
        existing.obj.setTexture(key);
        return existing;
      }
      existing.obj.destroy();
      const img = this.add.image(0, 0, key)
        .setVisible(false)
        .setDepth(3);
      return { kind: 'sprite', obj: img };
    }
    // Fallback: keep or create container
    if (existing.kind === 'fallback') return existing;
    existing.obj.destroy();
    return { kind: 'fallback', obj: makeFallbackSprite(this, fallbackColor).setDepth(3) };
  }

  private scaleTank(t: TankObject, sc: number) {
    if (t.kind === 'sprite') {
      // Scale sprite so it fills ~72% of a cell (same visual size as the rectangle)
      const targetPx = BASE_CELL * 0.72 * sc;
      const texW = t.obj.width || 96;
      t.obj.setScale(targetPx / texW);
    } else {
      t.obj.setScale(sc);
    }
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
    this.scaleTank(this.tankA, sc);
    this.scaleTank(this.tankB, sc);
  }

  render(
    stateA: TankState,
    stateB: TankState,
    projectiles: Projectile[],
    tankAId: string,
  ) {
    this.tankAId = tankAId;
    this.drawSensorRanges(stateA, stateB);
    this.placeTank(this.tankA, stateA);
    this.placeTank(this.tankB, stateB);
    this.drawProjectiles(projectiles);
  }

  private drawSensorRanges(stateA: TankState, stateB: TankState) {
    this.sensorGfx.clear();
    const draw = (s: TankState, color: number) => {
      const r = s.config.sensorRange * this.cell;
      const cx = this.offsetX + (s.position.x + 0.5) * this.cell;
      const cy = this.offsetY + (s.position.y + 0.5) * this.cell;
      this.sensorGfx.fillStyle(color, 0.12);
      this.sensorGfx.fillCircle(cx, cy, r);
    };
    draw(stateA, TANK_A);
    draw(stateB, TANK_B);
  }

  private placeTank(t: TankObject, s: TankState) {
    const cx = this.offsetX + (s.position.x + 0.5) * this.cell;
    const cy = this.offsetY + (s.position.y + 0.5) * this.cell;
    const rot = DIR_ROT[s.facing] ?? 0;
    const alpha = 0.3 + (s.hp / 100) * 0.7;
    if (t.kind === 'sprite') {
      t.obj.setPosition(cx, cy).setRotation(rot).setAlpha(alpha).setVisible(true);
    } else {
      t.obj.setPosition(cx, cy).setRotation(rot).setAlpha(alpha).setVisible(true);
    }
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
    const t = side === 'a' ? this.tankA : this.tankB;
    const target = t.kind === 'sprite' ? t.obj : t.obj;
    this.tweens.add({
      targets: target,
      alpha: { from: target.alpha, to: 0.05 },
      duration: 60,
      yoyo: true,
    });
  }
}

// Keep this exported so Watch.tsx can import it unchanged
export { AVATAR_KEYS };
