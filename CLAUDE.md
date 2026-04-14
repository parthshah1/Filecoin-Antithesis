# Filecoin-Antithesis

Antithesis-based chaos testing harness for the Filecoin blockchain. Runs a private devnet with Lotus nodes, miners, drand beacons, and optionally Curio storage provider, then randomly executes weighted test vectors to find bugs.

## Custom Skills for New Contributors

Use these built-in Claude Code skills to get oriented and productive:

- `/explain` -- Ask any architectural question ("how does the deck work?", "what does DoGasWar test?", "how does the FOC lifecycle work?")
- `/new-vector` -- Scaffold a new test vector with correct conventions, profile wiring, and env var setup
- `/profiles` -- Inspect, compare, validate, or create test profiles (env.foc, env.consensus, etc.)

## Build & Run

- Build workload: `cd workload && go build ./cmd/stress-engine`
- Build Docker image: `make build-workload`
- Run default profile: `docker compose up`
- Run FOC profile: `docker compose --env-file env.foc --profile foc up`
- Run consensus profile: `docker compose --env-file env.consensus --profile full up`

## Code Conventions

- Stress engine is a flat Go package (`package main`) in `workload/cmd/stress-engine/` -- no interfaces, no DI
- Test vectors are `DoXxxYyy()` functions in `*_vectors.go` files
- Randomness: always use `rngIntn()` / `rngChoice()` (Antithesis SDK), never `math/rand`
- Assertions: `assert.Always()` = safety, `assert.Sometimes()` = liveness, `assert.Reachable()` = coverage
- Weights: `STRESS_WEIGHT_*` env vars control vector frequency in the weighted deck
- Profiles: `env.*` files at repo root override defaults from `docker-compose.yaml`
