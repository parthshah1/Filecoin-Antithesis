# Antithesis Testing with the Filecoin Network

## Purpose

This repository provides a comprehensive testing framework for the Filecoin network using the [Antithesis](https://antithesis.com/) autonomous testing platform. It validates multiple Filecoin implementations (Lotus, Forest, Curio) through deterministic, fault-injected testing.

## Setup Overview

The system runs **9 containers** by default (12 with `--profile foc`):
- **Drand cluster**: `drand0`, `drand1`, `drand2` (randomness beacon)
- **Lotus nodes**: `lotus0`, `lotus1` (Go implementation)
- **Lotus miners**: `lotus-miner0`, `lotus-miner1`
- **Forest node**: `forest0` (Rust implementation)
- **Workload**: Go stress engine container

With `--profile foc` (Filecoin Open Contracts stack):
- **FilWizard**: Contract deployment and environment wiring
- **Curio**: Storage provider with PDP support
- **Yugabyte**: Database for Curio state

## Quick Start

### Prerequisites
- Docker and Docker Compose
- Make

### Build and Run
```bash
# Build all images
make build-all

# Start protocol stack (drand + lotus + forest + workload)
make up

# Start full FOC stack (adds filwizard + curio + yugabyte)
make up-foc

# View logs
make logs

# Stop and cleanup
make cleanup
```

### Available Make Commands
```bash
make help            # Show all commands
make build-all       # Build all images
make build-lotus     # Build Lotus image
make build-forest    # Build Forest image
make build-drand     # Build Drand image
make build-workload  # Build workload image
make build-curio     # Build Curio image
make build-filwizard # Build FilWizard image
make build-infra     # Build infrastructure (drand)
make build-nodes     # Build all node images (lotus, forest, curio)
make up              # Start default services
make up-foc          # Start all + FOC services
make down            # Stop default services
make down-foc        # Stop all services including FOC
make logs            # Follow logs
make restart         # Restart containers
make all             # Build all images and start localnet
make rebuild         # Clean rebuild (down + cleanup + build + up)
make rebuild-foc     # Clean rebuild with FOC profile
make cleanup         # Stop and clean data
make show-versions   # Show image version tags
```

## Stress Engine

The workload container runs a **stress engine** that continuously picks weighted actions ("vectors") and executes them against Lotus and Forest nodes. Each vector uses Antithesis SDK assertions to verify safety and liveness.

### Profiles

Pick a test scenario with one env var:

```bash
STRESS_PROFILE=default     # Consensus checks + basic EVM stress (newcomer-friendly)
STRESS_PROFILE=consensus   # Heavy state/F3/cross-node validation
STRESS_PROFILE=chaos       # Adversarial: reorg, slashing, quorum boundary
STRESS_PROFILE=upgrade     # Network upgrade testing (FIP-specific vectors)
STRESS_PROFILE=nsplit      # N-split attack scenario: heavy reorg + power slashing
STRESS_PROFILE=full        # All vectors enabled at equal weight
```

Set `STRESS_PROFILE=help` to list all profiles with descriptions and category multipliers.

Vectors are grouped into **categories** (consensus, evm, crossnode, state, chaos, resource, upgrade, foc). Each profile sets a multiplier per category. Fine-tune with:

```bash
STRESS_CATEGORY_CHAOS=5          # Scale an entire category
STRESS_WEIGHT_REORG=10           # Override a single vector
```

The startup log shows exactly how each weight was resolved.

For full documentation see [workload/README.md](workload/README.md):
- [Profiles in depth](workload/README.md#profiles-in-depth) -- what each profile runs and when to use it
- [How weight resolution works](workload/README.md#how-weight-resolution-works) -- override layers explained with examples
- [All vectors reference](workload/README.md#all-vectors-reference) -- every vector, its category, base weight, and env var
- [Extending the stress engine](workload/README.md#extending-the-stress-engine) -- how to add vectors, categories, or profiles
- [Running locally](workload/README.md#running-locally) -- docker compose commands and profile switching
- [Branching model](workload/README.md#branching-model-running-your-own-tests) -- how teams branch off main for test campaigns

### FOC Profile

When the FOC compose profile is active (`--profile foc`), the engine auto-detects FOC mode and runs consensus + FOC lifecycle vectors. An explicit `STRESS_PROFILE` can override this.

A separate `foc-sidecar` process monitors on-chain FOC contract state and emits safety assertions. See [workload/FOC.md](workload/FOC.md) for architecture details.

### Reorg Safety

All state-sensitive assertions use `ChainGetFinalizedTipSet` so they are safe during partition/reorg chaos injected by Antithesis.

## Antithesis Integration

### Fault Injection
Antithesis automatically injects faults (crashes, network partitions, thread pausing) after the workload signals "setup complete".

### SDK Assertions
Test properties use the Antithesis Go SDK:
- `assert.Always()` — Must always hold
- `assert.Sometimes()` — Must hold at least once
- `assert.Reachable()` — Code path must be reached
- `assert.Unreachable()` — Code path must never be reached

### Running Tests on Antithesis
1. Push images to Antithesis registry
2. Use GitHub Actions to trigger tests
3. Review reports in Antithesis dashboard

### GitHub Actions Workflows

#### Build & Push Workflows

Each component has a dedicated build workflow that builds the Docker image and pushes it to the Antithesis GAR registry.

| Workflow | Trigger | Components |
|----------|---------|------------|
| `build_push_lotus.yml` | Nightly (6 PM EST) + Manual | Lotus |
| `build_push_forest.yml` | Nightly (6 PM EST) + Manual | Forest |
| `build_push_curio.yml` | Nightly (6 PM EST) + Manual | Curio |
| `build_push_drand.yml` | Manual | Drand |
| `build_push_workload.yml` | Manual | Workload |
| `build_push_filwizard.yml` | Manual | FilWizard |
| `build_push_config.yml` | Manual | Config |

Scheduled builds fetch the latest commit from the upstream repo and tag the image as `latest`. Manual builds accept a commit hash or tag as input.

#### PR Antithesis Test (`pr_antithesis_test.yml`)

Automatically builds and tests changes on a PR when specific labels are applied:

- **`antithesis-test-filecoin`** — Triggers a test on the `filecoin` endpoint
- **`antithesis-test-foc`** — Triggers a test on the `filecoin-foc` endpoint

The workflow detects which components changed, builds only those images, and triggers a 1-hour ephemeral Antithesis test. Unchanged components use the existing `latest` images from the registry.

#### Run Antithesis Test (`run_antithesis_test.yml`)

Triggers an Antithesis test run. Runs nightly (12-hour runs) for both the Implementors and FOC teams. Can also be triggered manually with custom image tags, duration, endpoint selection, and smoke test flags.

#### Run Antithesis Upgrade Test (`run_antithesis_upgradetest.yml`)

Manual-only workflow for testing image upgrades mid-run. Specify a base set of images, then an upgrade image and tag to swap in during the test.

#### List Registry Images (`list_registry_images.yml`)

Manual-only workflow to list recent image tags in the Antithesis GAR registry. Select a component from the dropdown and the workflow outputs the most recent images with tags, digests, and timestamps. Results appear directly on the workflow run summary page.

#### Get Logs (`get_logs.yml`)

Manual-only workflow to retrieve test logs from Antithesis.

## Directory Structure

```
├── drand/               # Drand beacon build
├── lotus/               # Lotus node build and scripts
├── forest/              # Forest node build and scripts
├── curio/               # Curio storage provider build  [--profile foc]
├── filwizard/           # Contract deployment container [--profile foc]
├── yugabyte/            # YugabyteDB for Curio         [--profile foc]
├── workload/            # Stress engine
│   ├── cmd/
│   │   ├── stress-engine/       # Fuzz driver source
│   │   │   ├── main.go              # Entry point, deck builder, action loop
│   │   │   ├── helpers.go           # Shared message helpers
│   │   │   ├── mempool_vectors.go   # Transfer, gas war, adversarial
│   │   │   ├── evm_vectors.go       # Contract deploy, invoke, selfdestruct, resource stress
│   │   │   ├── consensus_vectors.go # Tipset consensus, height, peers, state roots, audit
│   │   │   ├── crossnode_vectors.go # Receipt audit, msg ordering, nonce bombard
│   │   │   ├── state_vectors.go     # Actor migration, lifecycle stress
│   │   │   ├── reorg_vectors.go     # Partition/mine/heal chaos cycles
│   │   │   ├── foc_vectors.go       # FOC lifecycle + steady-state vectors
│   │   │   └── contracts.go         # EVM bytecodes, ABI encoding
│   │   ├── foc-sidecar/         # Independent FOC safety monitor
│   │   ├── genesis-prep/        # Wallet generation for stress testing
│   │   └── setup-complete/      # Antithesis lifecycle signal utility
│   ├── internal/
│   │   ├── chain/               # RPC client (Lotus + Forest)
│   │   └── foc/                 # FOC contract interaction libraries
│   ├── entrypoint/              # Container startup scripts
│   ├── FOC.md                   # FOC architecture documentation
│   └── Dockerfile
├── scripts/             # Helper scripts (run-local.sh)
├── data/                # Runtime data (git-ignored, created on start)
├── shared/              # Shared configs between containers (git-ignored)
├── versions.env         # Version pins — change to test a new client version
├── Makefile             # Build commands
├── docker-compose.yaml  # Service definitions
└── cleanup.sh           # Data cleanup script
```

## Configuration

### Environment Variables
Located in `.env`:
- Node data directories
- Port configurations
- Shared volume paths

### Version Pinning
Located in `versions.env` — change these to test a specific upstream commit or tag:
```env
# Implementation versions — commit hashes or tags from upstream repos
LOTUS_COMMIT=latest
FOREST_COMMIT=latest
CURIO_COMMIT=latest
DRAND_TAG=latest

# Internal versions — built from this repo
WORKLOAD_TAG=latest
FILWIZARD_TAG=latest
CONFIG_TAG=latest
```



## Scaling Nodes

The network topology is dynamically scalable. Three variables in `docker-compose.yaml` control the counts:

| Variable | Controls | Default |
|---|---|---|
| `NUM_LOTUS_CLIENTS` | Total Lotus full nodes (including those paired with miners) | 2 |
| `NUM_LOTUS_MINERS` | Genesis miners + miner processes (must be <= `NUM_LOTUS_CLIENTS`) | 2 |
| `NUM_FOREST_CLIENTS` | Forest full nodes | 1 |

The start scripts use bash indirect expansion to resolve per-node variables dynamically — for example, `LOTUS_${N}_DATA_DIR` resolves to the value of `LOTUS_0_DATA_DIR`, `LOTUS_1_DATA_DIR`, etc. Peer connection loops iterate the count variables, so no script changes are needed when scaling.

### Adding a Lotus Full Node (no miner)

Example: adding `lotus2` as a pure full node that validates, syncs, and serves RPC but doesn't produce blocks.

**1. `.env`** — Add per-node variables:
```env
# Lotus 2 (full node, no miner)
LOTUS_2_DATA_DIR=/lotus2
LOTUS_2_PATH=${LOTUS_2_DATA_DIR}/lotus2-net
LOTUS_2_API_LISTENADDRESS=/dns/lotus2/tcp/${LOTUS_RPC_PORT}/http
LOTUS_2_LIBP2P_LISTENADDRESSES=/ip4/lotus2/tcp/${LOTUS_P2P_PORT}
```

**2. `docker-compose.yaml`** — Bump count and add service:
```yaml
# In filecoin_service template:
- NUM_LOTUS_CLIENTS=3    # was 2
# Add volume:
- ./data/lotus2:${LOTUS_2_DATA_DIR}

# Add service:
lotus2:
  <<: [ *filecoin_service, *needs_lotus0_healthy ]
  image: lotus:${LOTUS_2_TAG:-${LOTUS_TAG:-latest}}
  container_name: lotus2
  entrypoint: [ "./scripts/start-lotus.sh", "2" ]
  healthcheck:
    <<: *healthcheck_settings
    test: curl --fail http://lotus2:1234/health/livez

# Update workload:
- STRESS_NODES=lotus0,lotus1,lotus2,forest0
# Add workload volume:
- ./data/lotus2:/root/devgen/lotus2
```

No miner variables or miner service needed. `NUM_LOTUS_MINERS` stays unchanged.

### Adding a Lotus Miner

Miners are paired 1:1 with Lotus nodes — miner N connects to lotus node N. To add miner 2, you must first have lotus2.

**1. `.env`** — Add miner variables (actor address follows `t01NNN` pattern):
```env
LOTUS_MINER_2_ACTOR_ADDRESS=t01002
LOTUS_MINER_2_PATH=${LOTUS_2_DATA_DIR}/lotus-miner2-net
LOTUS_MINER_2_API_LISTENADDRESS=/dns/lotus-miner2/tcp/${LOTUS_MINER_RPC_PORT}/http
```

**2. `docker-compose.yaml`** — Bump miner count, add dependency template and service:
```yaml
# In filecoin_service template:
- NUM_LOTUS_MINERS=3     # was 2

# Add dependency template:
needs_lotus2_healthy: &needs_lotus2_healthy
  depends_on:
    lotus2:
      condition: service_healthy

# Add service:
lotus-miner2:
  <<: [ *filecoin_service, *needs_lotus2_healthy ]
  image: lotus:${LOTUS_MINER_2_TAG:-${LOTUS_TAG:-latest}}
  container_name: lotus-miner2
  entrypoint: [ "./scripts/start-lotus-miner.sh", "2" ]
```

`setup-genesis.sh` will automatically pre-seal sectors for the new miner (it iterates `NUM_LOTUS_MINERS`).

### Adding a Forest Node

**1. `.env`** — Add per-node variables:
```env
FOREST_1_DATA_DIR=/forest1
FOREST_1_F3_SIDECAR_RPC_ENDPOINT=forest1:${F3_RPC_PORT}
```

**2. `docker-compose.yaml`** — Bump count and add service:
```yaml
# In filecoin_service template:
- NUM_FOREST_CLIENTS=2   # was 1
# Add volume:
- ./data/forest1:${FOREST_1_DATA_DIR}

# Add service:
forest1:
  <<: [ *filecoin_service, *needs_lotus0_healthy ]
  image: forest:latest
  container_name: forest1
  entrypoint: [ "./scripts/start-forest.sh", "1" ]

# Update workload:
- STRESS_NODES=lotus0,lotus1,forest0,forest1
# Add workload volume:
- ./data/forest1:/root/devgen/forest1
```

## Documentation

- [Antithesis Documentation](https://antithesis.com/docs/)
- [Lotus Documentation](https://lotus.filecoin.io/)
- [Forest Documentation](https://chainsafe.github.io/forest/)
- [FilWizard](https://github.com/parthshah1/FilWizard) — Contract deployment tool
- [FOC Architecture](workload/FOC.md) — FOC testing design and vectors
