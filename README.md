# TankMaze

Somewhere in the labyrinth, an enemy tank is moving. You don't know where — only that your sensors just spiked and you have one tick to decide: advance, turn, fire, or wait for better data. TankMaze is a strategic programming game where intelligence gathering is survival.

You don't play. You code. Write the autonomous brain that drives your tank in Go, submit it, and watch your creation navigate fog-of-war, enemy contact, and the limits of its own hardware — without you. Refine it between Game Days, climb a global ranking built on strategy not reflexes, and stay ahead: every update your opponent ships is a reason to ship a better one.

## Game Concept

You don't drive a tank — you program it, submit it, and watch it fight.

- **Write a tank in Go**: implement a `Tick(sensors Sensors) Action` function. It runs every 100 ms and returns one action (move, rotate, fire, scan, or idle). Package-level variables persist across ticks — that's your tank's memory.
- **Allocate stats**: distribute exactly 15 points across speed, sensor range, damage, armor, and fire rate. Fast tanks see less; hard-hitting tanks move slowly.
- **Develop with versions**: saves create minor versions (`v0.1`, `v0.2`, …). Promote to a major version (`v1`, `v2`, …) when the tank is competition-ready.
- **Test freely**: pit your tank against built-in AI opponents (Scout, Ranger, Bruiser, Randy) or any of your own other tanks — no match limits, no ranking impact.
- **Register for Game Day**: a configurable scheduled tournament (cron-based) where registered tanks compete in round-robin groups of 8, followed by a single-elimination bracket seeded best-vs-worst.
- **Earn global ranking points**: Game Day placement awards points based on field size and finish position. Points remain valid for a configurable period (default: 1 year), forming a rolling global leaderboard.
- **Replay and debug**: every match is recorded tick-by-tick. Replay at any speed (0.25× to 8×, or step-by-step), inspect sensor readings, memory state, and `fmt` output per tick. Export the full match as JSON for offline analysis.

## Tournament Format

Each Game Day runs in two phases, each with its own scheduled trigger:

1. **Round Robin** — tanks are pot-seeded by Global Rank into groups of 8. Every tank plays every other tank in its group. Points: flawless win (no damage received) = 2 pts, win = 1 pt, loss = 0 pts.
2. **Elimination** — top ⌊2/3⌋ per group advance (all advance if ≤ 64 tanks). Bracket is seeded globally: best vs. worst. Single elimination to the champion.

Match end: a configurable tick limit (default: 100 ticks). Tiebreakers in order — most damage dealt → most moves made → both tanks lose.

## Tech Stack

| Layer | Technology |
|---|---|
| Frontend | React 18 + TypeScript + Phaser 3 |
| Tank language | Go → WebAssembly (`GOOS=wasip1 GOARCH=wasm`) |
| WASM runtime | Wazero (pure-Go, runs in Lambda) |
| Auth | AWS Cognito + Amplify v6 |
| Real-time | AWS API Gateway WebSocket API |
| Game logic | AWS Lambda (Go) |
| State store | AWS DynamoDB |
| Hosting | AWS S3 + CloudFront |
| Infrastructure | AWS CDK v2 |

## Repository Structure

```
tankmaze/
├── packages/
│   ├── frontend/        # React + Phaser game client
│   ├── backend/         # Lambda functions + game engine (Go)
│   ├── sdk/             # Tank author SDK (Go types: Sensors, Action, TankConfig)
│   └── infrastructure/  # AWS CDK stacks
├── docs/
│   ├── functional-spec.md   # Game rules, mechanics, tournament format
│   ├── technical-spec.md    # AWS architecture, APIs, data models
│   └── architecture.md      # ADRs and sequence diagrams
└── .github/workflows/       # CI/CD pipeline
```

## Documentation

- [Functional Specification](docs/functional-spec.md) — game rules, tank API, versioning, tournament format, global ranking
- [Technical Specification](docs/technical-spec.md) — AWS architecture, APIs, data models
- [Architecture Decisions](docs/architecture.md) — ADRs and sequence diagrams

## Development Setup

> Prerequisites: Go 1.22+, Node.js 18+, pnpm 8+

### Local testing (no AWS required)

The local dev server replaces DynamoDB, S3, CodeBuild, Lambda, and Cognito with in-memory equivalents. It compiles tank WASM locally and streams match ticks over a plain WebSocket.

```bash
# Terminal 1 — backend (compiles scout + bruiser AI at startup, ~2 s)
cd packages/backend
GOTOOLCHAIN=local go run ./cmd/localserver/

# Terminal 2 — frontend
cd packages/frontend
pnpm install   # first time only
pnpm dev
```

Open **http://localhost:5173**. You are auto-logged in as `local` — no account needed.

**What you can do locally:**
- Create a tank and write its AI in the Monaco editor
- Save & Validate — compiles your Go source to WASM (uses `package tank` + `Tick` style or full `package main`)
- Promote a minor version to a major version
- Test vs AI — launches a live match against Scout, Bruiser, Ranger, or Randy and streams it to the Phaser viewer
- Watch the replay with speed controls (0.25×–8×, step-by-step)

The `.env.local` file in `packages/frontend/` points the frontend at `localhost:8080` and sets `VITE_LOCAL_DEV=true`. Remove or rename it to restore normal Cognito auth.

### Cloud deployment

> Prerequisites: Go 1.22+, AWS CLI configured, AWS CDK v2

```bash
# Build and test backend
cd packages/backend
GOTOOLCHAIN=local go build ./...
GOTOOLCHAIN=local go test ./...

# Deploy all stacks (Auth → Storage → Build → Api → Frontend)
cd packages/infrastructure
pnpm install
pnpm run build
npx cdk deploy --all

# Optionally set a custom domain
npx cdk deploy --all --context domainName=tankmaze.example.com
```

## Status

The platform is complete and production-deployed on AWS. All core features are live:

- Tank authoring: Monaco editor with hidden preamble, optional stdlib imports (`fmt`, `log`, `math`, `math/rand`, `sort`), browser-side Go syntax pre-check, and CodeBuild layer caching
- Version lifecycle: minor/major versioning, fork flow with score transfer, unsaved-changes guard
- AI reference tanks: Scout, Bruiser, Ranger, Randy — all seeded as real DB records and forkable
- Static maps: Open, Donut, X, Rooms, Double Spiral — hand-crafted layouts selectable in test matches
- Game Day scheduling: EventBridge cron triggers, round-robin pot seeding, up to 5 elimination rounds (R1–R5), single-elimination bracket with bye propagation, placement points formula
- Global leaderboard with 1-year point validity window
- Observer / replay: Phaser canvas, sensor range overlays, avatar sprites, bracket connector lines, Watch links per match, full debug panel (sensor indicators, memory JSON, console output)
- Admin panel: user management (disable, role toggle, delete), tank management (edit, force-delete), game day roster management, auto-fill bracket with all four AI tanks
- Tank avatars: 16 built-in sprites, per-tank selection, CloudFront-served uploads (API available)
- Security: SAST (gosec, staticcheck, semgrep), vulnerability scanning (govulncheck, trivy), secrets detection (trufflehog, gitleaks), BOLA and JWT tamper testing completed
- CI/CD: GitHub Actions with OIDC AWS auth; automated deploy on push to main; AI tank WASMs compiled from source on every deploy

## Image Credits

The impact and destroyed-tank sprite animations bundled in `packages/frontend/public/animations/` are derived from:

> [Animation Sprite Sheet of Bomb Explosion Sequence](https://www.vecteezy.com/vector-art/13224424-animation-sprite-sheet-of-bomb-explosion-sequence) by Vecteezy
