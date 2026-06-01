# TankMaze

Somewhere in the labyrinth, an enemy tank is moving. You don't know where — only that your sensors just spiked and you have one tick to decide: advance, turn, fire, or wait for better data. TankMaze is a strategic programming game where intelligence gathering is survival.

You don't play. You code. Write the autonomous brain that drives your tank in Go, submit it, and watch your creation navigate fog-of-war, enemy contact, and the limits of its own hardware — without you. Refine it between Game Days, climb a global ranking built on strategy not reflexes, and stay ahead: every update your opponent ships is a reason to ship a better one.

## Game Concept

You don't play TankMaze. You program your tank, submit it, and watch it fight.

- **Write a tank in Go**: implement a `Tick(sensors Sensors) Action` function. It runs every 100 ms and returns one action (move, rotate, fire, scan, or idle). Package-level variables persist across ticks — that's your tank's memory.
- **Allocate stats**: distribute exactly 15 points across speed, sensor range, damage, armor, and fire rate. Fast tanks see less; hard-hitting tanks move slowly.
- **Develop with versions**: saves create minor versions (`v0.1`, `v0.2`, …). Promote to a major version (`v1`, `v2`, …) when the tank is competition-ready.
- **Test freely**: pit your tank against built-in AI opponents (Scout, Ranger, Bruiser) or any of your own other tanks — no match limits, no ranking impact.
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

> Prerequisites: Go 1.22+, AWS CLI configured, AWS CDK v2

```bash
# Build all packages
go build ./...

# Run tests
go test ./...

# Deploy infrastructure (requires AWS credentials)
cd packages/infrastructure
cdk deploy --all

# Start frontend dev server
cd packages/frontend
pnpm dev
```

## Status

Specification phase complete. Implementation in progress.
