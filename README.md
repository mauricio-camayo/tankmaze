# TankMaze

A real-time browser-based tactical game. One or two players navigate a randomly generated labyrinth inside armored tanks with limited, sensor-based awareness. A third-party observer can watch the full arena in real time.

## Game Concept

- **Players** drive tanks with no full-map view — only sensor readings (wall distances, proximity alerts).
- **Three tank archetypes**: Scout (fast), Ranger (long sensors), Bruiser (high damage/armor).
- **Combat**: fire projectiles that travel through the maze until hitting a wall or the opponent.
- **Observers**: see the full maze, both tanks, health bars, and projectiles via a shareable link.

## Tech Stack

| Layer | Technology |
|---|---|
| Frontend | React 18 + TypeScript + Phaser 3 |
| Auth | AWS Cognito + Amplify v6 |
| Real-time | AWS API Gateway WebSocket API |
| Game logic | AWS Lambda (Node.js 20 + TypeScript) |
| State store | AWS DynamoDB |
| Hosting | AWS S3 + CloudFront |
| Infrastructure | AWS CDK v2 |
| Monorepo | pnpm workspaces |

## Repository Structure

```
tankmaze/
├── packages/
│   ├── frontend/        # React + Phaser game client
│   ├── backend/         # Lambda functions + game logic
│   ├── shared/          # Shared TypeScript types and maze generator
│   └── infrastructure/  # AWS CDK stacks
├── docs/
│   ├── functional-spec.md   # Game rules, mechanics, roles
│   ├── technical-spec.md    # Architecture, APIs, data models
│   └── architecture.md      # ADRs and sequence diagrams
└── .github/workflows/       # CI/CD pipeline
```

## Documentation

- [Functional Specification](docs/functional-spec.md) — game rules, mechanics, player roles
- [Technical Specification](docs/technical-spec.md) — AWS architecture, APIs, data models
- [Architecture Decisions](docs/architecture.md) — ADRs and sequence diagrams

## Development Setup

> Prerequisites: Node.js 20+, pnpm 9+, AWS CLI configured, AWS CDK v2

```bash
# Install dependencies
pnpm install

# Type-check all packages
pnpm -r typecheck

# Run tests
pnpm -r test

# Deploy infrastructure (requires AWS credentials)
cd packages/infrastructure
pnpm cdk deploy --all

# Start frontend dev server
cd packages/frontend
pnpm dev
```

## Status

Specification phase complete. Implementation in progress.
