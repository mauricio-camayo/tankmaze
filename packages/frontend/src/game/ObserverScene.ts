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

// Deterministic default: same hash as AvatarPicker.tsx's defaultAvatarUrl.
function defaultAvatarKey(tankId: string): string {
  const idx = tankId.split('').reduce((a, c) => a + c.charCodeAt(0), 0) % 16;
  return `avatar-tank-${idx}`;
}

type TankObject =
  | { kind: 'sprite'; obj: Phaser.GameObjects.Image }
  | { kind: 'fallback'; obj: Phaser.GameObjects.Container };

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

  // Animation state
  private prevProjs: Projectile[] = [];
  private prevHpA = 100;
  private prevHpB = 100;
  private destroyedA: Phaser.GameObjects.Image | null = null;
  private destroyedB: Phaser.GameObjects.Image | null = null;
  private destroyTimerA: Phaser.Time.TimerEvent | null = null;
  private destroyTimerB: Phaser.Time.TimerEvent | null = null;

  constructor() {
    super({ key: 'ObserverScene' });
  }

  preload() {
    for (let i = 0; i < 16; i++) {
      this.load.image(`avatar-tank-${i}`, `/avatars/tank-${i}.png`);
    }
    this.load.image('impact',      '/animations/impact.png');
    this.load.image('destroyed-0', '/animations/destroyed-0.png');
    this.load.image('destroyed-1', '/animations/destroyed-1.png');
  }

  create() {
    this.cameras.main.setBackgroundColor(WALL);
    this.mazeGfx = this.add.graphics().setDepth(0);
    this.sensorGfx = this.add.graphics().setDepth(1);
    this.projGfx = this.add.graphics().setDepth(2);
    this.tankA = { kind: 'fallback', obj: makeFallbackSprite(this, TANK_A).setDepth(3) };
    this.tankB = { kind: 'fallback', obj: makeFallbackSprite(this, TANK_B).setDepth(3) };
  }

  /** Called from Watch.tsx after the MATCH_SNAPSHOT arrives with avatarUrls and tankIds. */
  setAvatarURLs(
    urlA: string | undefined, urlB: string | undefined,
    tankIdA?: string, tankIdB?: string,
  ) {
    this.tankA = this.buildTankObject(urlA, tankIdA, TANK_A, this.tankA);
    this.tankB = this.buildTankObject(urlB, tankIdB, TANK_B, this.tankB);
    // Re-scale to current cell size
    const sc = this.cell / BASE_CELL;
    this.scaleTank(this.tankA, sc);
    this.scaleTank(this.tankB, sc);
  }

  private buildTankObject(
    url: string | undefined,
    tankId: string | undefined,
    fallbackColor: number,
    existing: TankObject,
  ): TankObject {
    // Resolve texture key: explicit avatar URL → hash-derived default → rectangle fallback
    const key = url ? avatarKey(url) : (tankId ? defaultAvatarKey(tankId) : null);
    if (key && this.textures.exists(key)) {
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
    // Rectangle fallback (only reached if textures somehow not loaded)
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

    // Reset animation state for the new match
    this.destroyedA?.destroy();
    this.destroyedB?.destroy();
    this.destroyedA = null;
    this.destroyedB = null;
    this.destroyTimerA?.remove();
    this.destroyTimerB?.remove();
    this.destroyTimerA = null;
    this.destroyTimerB = null;
    this.prevProjs = [];
    this.prevHpA = 100;
    this.prevHpB = 100;
  }

  render(
    stateA: TankState,
    stateB: TankState,
    projectiles: Projectile[],
    tankAId: string,
  ) {
    this.tankAId = tankAId;

    // Detect destroyed tanks (HP drops to 0)
    if (this.prevHpA > 0 && stateA.hp <= 0) {
      this.playDestroyed(stateA.position, 'a');
    }
    if (this.prevHpB > 0 && stateB.hp <= 0) {
      this.playDestroyed(stateB.position, 'b');
    }
    this.prevHpA = stateA.hp;
    this.prevHpB = stateB.hp;

    // Detect projectile hits (present last tick, absent this tick)
    this.detectHits(projectiles);
    this.prevProjs = [...projectiles];

    this.drawSensorRanges(stateA, stateB);
    this.placeTank(this.tankA, stateA);
    this.placeTank(this.tankB, stateB);
    this.drawProjectiles(projectiles, stateA, stateB);
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

  private drawProjectiles(projs: Projectile[], stateA: TankState, stateB: TankState) {
    this.projGfx.clear();
    const r = this.cell * 0.15;
    const tracerLen = this.cell * 0.4;
    for (const p of projs) {
      const isA = p.ownerTankId === this.tankAId;
      const color = isA ? PROJ_A : PROJ_B;
      const tankColor = isA ? TANK_A : TANK_B;
      const cx = this.offsetX + (p.position.x + 0.5) * this.cell;
      const cy = this.offsetY + (p.position.y + 0.5) * this.cell;

      // Trailing dash from firing tank center to projectile
      const owner = isA ? stateA : stateB;
      const tx = this.offsetX + (owner.position.x + 0.5) * this.cell;
      const ty = this.offsetY + (owner.position.y + 0.5) * this.cell;
      this.drawDashedLine(tx, ty, cx, cy, tankColor, 0.5);

      // Projectile dot
      this.projGfx.fillStyle(color, 1);
      this.projGfx.fillCircle(cx, cy, r);

      // Direction tracer
      const [dx, dy] = DIR_VEC[p.direction] ?? [0, -1];
      this.projGfx.lineStyle(2, color, 0.7);
      this.projGfx.beginPath();
      this.projGfx.moveTo(cx, cy);
      this.projGfx.lineTo(cx + dx * tracerLen, cy + dy * tracerLen);
      this.projGfx.strokePath();
    }
  }

  private drawDashedLine(x1: number, y1: number, x2: number, y2: number, color: number, alpha: number) {
    const dashLen = 3, gapLen = 3;
    const dx = x2 - x1, dy = y2 - y1;
    const len = Math.sqrt(dx * dx + dy * dy);
    if (len < 1) return;
    const nx = dx / len, ny = dy / len;
    this.projGfx.lineStyle(1, color, alpha);
    let pos = 0, on = true;
    while (pos < len) {
      const seg = Math.min(on ? dashLen : gapLen, len - pos);
      if (on) {
        this.projGfx.beginPath();
        this.projGfx.moveTo(x1 + nx * pos, y1 + ny * pos);
        this.projGfx.lineTo(x1 + nx * (pos + seg), y1 + ny * (pos + seg));
        this.projGfx.strokePath();
      }
      pos += seg;
      on = !on;
    }
  }

  private detectHits(projs: Projectile[]) {
    if (this.prevProjs.length === 0) return;
    // Build lookup of current projectile positions by owner
    const currSet = new Set(projs.map(p => `${p.ownerTankId},${p.position.x},${p.position.y}`));
    for (const prev of this.prevProjs) {
      const [dx, dy] = DIR_VEC[prev.direction] ?? [0, 0];
      const expectedKey = `${prev.ownerTankId},${prev.position.x + dx},${prev.position.y + dy}`;
      if (!currSet.has(expectedKey)) {
        // Projectile didn't reach expected next cell — it hit something
        this.playImpact({ x: prev.position.x + dx, y: prev.position.y + dy });
      }
    }
  }

  private playImpact(pos: { x: number; y: number }) {
    const cx = this.offsetX + (pos.x + 0.5) * this.cell;
    const cy = this.offsetY + (pos.y + 0.5) * this.cell;
    const img = this.add.image(cx, cy, 'impact')
      .setDisplaySize(this.cell, this.cell)
      .setDepth(8)
      .setAlpha(0.9);
    this.tweens.add({
      targets: img,
      displayWidth:  this.cell * 2.2,
      displayHeight: this.cell * 2.2,
      alpha: 0,
      duration: 380,
      ease: 'Power2',
      onComplete: () => img.destroy(),
    });
  }

  private playDestroyed(pos: { x: number; y: number }, side: 'a' | 'b') {
    const cx = this.offsetX + (pos.x + 0.5) * this.cell;
    const cy = this.offsetY + (pos.y + 0.5) * this.cell;
    const sz = this.cell * 1.5;

    const prev = side === 'a' ? this.destroyedA : this.destroyedB;
    const prevTimer = side === 'a' ? this.destroyTimerA : this.destroyTimerB;
    prev?.destroy();
    prevTimer?.remove();

    const img = this.add.image(cx, cy, 'destroyed-0')
      .setDisplaySize(sz, sz)
      .setDepth(9);
    let frame = 0;
    const timer = this.time.addEvent({
      delay: 250,
      callback: () => {
        frame = 1 - frame;
        img.setTexture(frame === 0 ? 'destroyed-0' : 'destroyed-1');
      },
      loop: true,
    });
    if (side === 'a') {
      this.destroyedA = img;
      this.destroyTimerA = timer;
    } else {
      this.destroyedB = img;
      this.destroyTimerB = timer;
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
