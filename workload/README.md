# Filecoin Antithesis Workload

This directory contains the **stress engine** for validating Filecoin nodes (Lotus, Forest) using the [Antithesis](https://antithesis.com/) testing platform.

## Architecture

The stress engine runs as a continuous loop, randomly picking weighted actions ("vectors") from a deck and executing them against the connected Filecoin nodes. Each vector targets a specific subsystem and uses Antithesis SDK assertions to verify safety and liveness properties.

```
entrypoint.sh -> stress-engine binary
  |-- Connects to lotus0..N, forest0..N via JSON-RPC
  |-- Loads pre-funded wallets from shared keystore
  |-- Initializes EVM contract bytecodes
  |-- Resolves profile + category weights
  +-- Runs weighted action loop (pick -> execute -> assert)
```

## Quick Start

Set one env var to pick your test scenario:

```bash
STRESS_PROFILE=default     # Consensus checks + basic EVM stress (newcomer-friendly)
STRESS_PROFILE=consensus   # Heavy state/F3/cross-node validation
STRESS_PROFILE=chaos       # Adversarial: reorg, slashing, quorum boundary
STRESS_PROFILE=upgrade     # Network upgrade testing (FIP-specific vectors)
STRESS_PROFILE=nsplit      # N-split attack scenario: heavy reorg + power slashing
STRESS_PROFILE=full        # All vectors enabled at equal weight
```

To discover all profiles without reading code:

```bash
STRESS_PROFILE=help ./stress-engine
# or via docker:
docker compose run workload env STRESS_PROFILE=help ./stress-engine
```

This prints every profile, its description, and its category multipliers, then exits.

---

## Profiles In Depth

A **profile** is a named preset that controls which test vectors run and how often. When the stress engine starts, it:

1. Reads `STRESS_PROFILE` (defaults to `default` if unset)
2. Loads the profile's category multipliers
3. Applies any `STRESS_CATEGORY_*` env var overrides
4. Computes each vector's weight: `baseWeight x categoryMultiplier`
5. Applies any `STRESS_WEIGHT_*` env var overrides (highest priority)
6. Builds the deck and logs the full resolution

### Available Profiles

#### `default` -- General-purpose (newcomer-friendly)

Runs consensus health checks and basic EVM contract stress. No chaos, no resource stress. Good for verifying a node implementation works correctly under normal load.

| consensus | evm | crossnode | state | chaos | resource | upgrade |
|---|---|---|---|---|---|---|
| 3 | 2 | 2 | 1 | 0 | 0 | 0 |

**What runs:** Tipset agreement, state root comparison, F3 monitoring, contract deploys/calls, receipt audits, nonce bombardment, actor lifecycle checks.

**What doesn't run:** Reorg chaos, power slashing, gas bombs, memory bombs, storage spam.

#### `consensus` -- Heavy state/F3 validation

Maximizes consensus and cross-node checks. Useful when testing a new node implementation and you want high confidence in state agreement.

| consensus | evm | crossnode | state | chaos | resource | upgrade |
|---|---|---|---|---|---|---|
| 5 | 1 | 3 | 2 | 0 | 0 | 0 |

**What runs:** All consensus vectors at high frequency, cross-node receipt/ordering audits at 3x, state tree verification at 2x. Minimal EVM stress (just enough to create state changes).

#### `chaos` -- Adversarial testing

Actively tries to break things: network partitions, miner slashing, double-spend races, resource exhaustion. Use this to test your node's resilience under attack.

| consensus | evm | crossnode | state | chaos | resource | upgrade |
|---|---|---|---|---|---|---|
| 2 | 1 | 1 | 0 | 3 | 2 | 0 |

**What runs:** Rapid partition/heal cycles (`DoReorgChaos`), power-aware miner slashing, adversarial mempool attacks, gas wars, gas/memory/storage bombs. Consensus checks still active at lower frequency to detect if chaos caused permanent damage.

#### `upgrade` -- Network upgrade testing

Tests behavior around a network upgrade epoch. Combines consensus monitoring with upgrade-specific FIP vectors. Branch off main, add your FIP vectors to the `upgrade` category, and run this profile.

| consensus | evm | crossnode | state | chaos | resource | upgrade |
|---|---|---|---|---|---|---|
| 3 | 1 | 2 | 1 | 0 | 0 | 3 |

**What runs:** Consensus checks to verify all nodes agree through the upgrade, cross-node audits, and any vectors in the `upgrade` category at high weight. The `upgrade` category is where FIP-specific test vectors are registered (e.g., precompile tests, new opcode tests, gas pricing changes).

#### `nsplit` -- N-split attack scenario

Simulates the Wang 2023 n-split attack: heavy reorg chaos and power slashing to test whether nodes can maintain consensus when the network is deliberately partitioned and miners are slashed below quorum thresholds.

| consensus | evm | crossnode | state | chaos | resource | upgrade |
|---|---|---|---|---|---|---|
| 3 | 0 | 1 | 0 | 5 | 0 | 0 |

**What runs:** `DoReorgChaos` and `DoPowerAwareSlash` at very high frequency (5x multiplier). No EVM stress -- the goal is pure consensus/network chaos. Consensus monitoring stays active to detect persistent forks.

#### `full` -- Everything enabled

Every vector category at equal weight. Useful for broad coverage runs or when you don't know what to test.

| consensus | evm | crossnode | state | chaos | resource | upgrade |
|---|---|---|---|---|---|---|
| 2 | 2 | 2 | 2 | 2 | 2 | 2 |

### FOC Auto-Detection

When the FOC compose profile is active (`docker compose --profile foc up`), the engine auto-detects FOC mode via DNS lookup and activates a special FOC profile:

| consensus | foc | everything else |
|---|---|---|
| 3 | 1 | 0 |

This runs consensus health checks plus the full FOC lifecycle (proofset creation, piece upload, retrieval, payment rail settlement). You can override this by setting `STRESS_PROFILE` explicitly -- e.g., `STRESS_PROFILE=chaos` will run chaos vectors alongside FOC vectors.

---

## How Weight Resolution Works

Each vector has three things that determine its deck weight:

1. **Base weight** (1-3): How important it is *within* its category. Most vectors are 1. `DoStateAudit` and `DoContractCall` are 2 because they're the most thorough checks in their categories.

2. **Category multiplier** (from profile): How much the profile emphasizes this category. A multiplier of 0 disables the entire category.

3. **Env var override** (optional): Bypasses the calculation entirely.

The resolution order, highest priority first:

```
STRESS_WEIGHT_<VECTOR>=N     --> use N directly (ignores base and multiplier)
     |
     v (if not set)
baseWeight x multiplier       --> computed from profile + STRESS_CATEGORY_ overrides
     |
     v (if category missing from profile)
0                             --> vector disabled
```

### Examples

```bash
# Use the chaos profile as-is
STRESS_PROFILE=chaos

# Use chaos profile but also enable upgrade vectors
STRESS_PROFILE=chaos STRESS_CATEGORY_UPGRADE=3

# Use default profile but crank reorg testing to 10
STRESS_PROFILE=default STRESS_WEIGHT_REORG=10

# Use default profile but enable the entire chaos category
STRESS_PROFILE=default STRESS_CATEGORY_CHAOS=3

# Use consensus profile, disable EVM entirely, boost one specific vector
STRESS_PROFILE=consensus STRESS_CATEGORY_EVM=0 STRESS_WEIGHT_STATE_AUDIT=20

# Disable a single vector from an otherwise-active category
STRESS_PROFILE=full STRESS_WEIGHT_MEMORY_BOMB=0
```

---

## Categories

Vectors are grouped into categories. Each profile sets a **multiplier** per category.

| Category | What it tests | Vectors |
|---|---|---|
| `consensus` | Chain agreement, state roots, F3 health | TipsetConsensus, HeightProgression, PeerCount, HeadComparison, StateRootComparison, StateAudit, F3FinalityMonitor |
| `evm` | FVM/EVM contract lifecycle | DeployContracts, ContractCall, SelfDestructCycle, ConflictingContractCalls |
| `crossnode` | Multi-node divergence detection | ReceiptAudit, MessageOrderingAttack, NonceBombard, GasExhaustionEdge |
| `state` | State tree consistency, compute verification | ActorMigrationStress, ActorLifecycleStress, HeavyCompute |
| `chaos` | Adversarial, reorg, slashing, mempool races | ReorgChaos, PowerAwareSlash, QuorumBoundaryTest, TransferMarket, GasWar, Adversarial |
| `resource` | Node resource limits (gas, memory, storage) | MaxBlockGas, LogBlaster, MemoryBomb, StorageSpam |
| `upgrade` | Network upgrade FIP-specific tests | *(add on a branch -- see branching model below)* |
| `foc` | Filecoin On-Chain Cloud lifecycle | FOCLifecycle, FOCUpload, FOCAddPieces, FOCMonitor, FOCRetrieve, FOCTransfer, FOCSettle, FOCWithdraw, FOCDeletePiece, FOCDeleteDataSet |

---

## Running Locally

### List available profiles

```bash
cd workload
go build ./cmd/stress-engine
STRESS_PROFILE=help ./stress-engine
```

### Run with docker compose

```bash
# Build and start with the default profile
make build-workload && make up

# Watch the startup log to see resolved weights
docker compose logs -f workload 2>&1 | head -60

# Run with a different profile
docker compose run -e STRESS_PROFILE=chaos workload

# Run with overrides
docker compose run \
  -e STRESS_PROFILE=default \
  -e STRESS_CATEGORY_CHAOS=3 \
  -e STRESS_WEIGHT_REORG=10 \
  workload
```

### Change profiles on a running stack

Edit `docker-compose.yaml` and change `STRESS_PROFILE=default` to your desired profile, then restart the workload:

```bash
docker compose restart workload
docker compose logs -f workload
```

### Verify profile resolution

The startup log always prints the full configuration. Look for the `[init] === Stress Engine Configuration ===` block:

```
[init] === Stress Engine Configuration ===
[init] profile: chaos
[init]   Adversarial: reorg, slashing, quorum boundary
[init] category multipliers:
[init]   consensus    2
[init]   evm          1
[init]   crossnode    1
[init]   state        0
[init]   chaos        5  (override: STRESS_CATEGORY_CHAOS=5)
[init]   resource     2
[init]   upgrade      0
[init]   foc          0
[init] enabled vectors:
[init]   DoTipsetConsensus                  cat=consensus    base=1 x mult=2 => 2
[init]   DoStateAudit                       cat=consensus    base=2 x mult=2 => 4
[init]   DoReorgChaos                       cat=chaos        base=1 x mult=5 => 10  (override: STRESS_WEIGHT_REORG)
[init]   DoPowerAwareSlash                  cat=chaos        base=1 x mult=5 => 5
[init]   ...
[init] deck built with 42 entries
```

Each line shows the vector name, its category, base weight, multiplier, and final resolved weight. If a weight was overridden by an env var, it's annotated.

---

## All Vectors (Reference)

### Consensus (`consensus_vectors.go`)

| Vector | Env Var | Base | Description |
|---|---|---|---|
| `DoTipsetConsensus` | `STRESS_WEIGHT_TIPSET_CONSENSUS` | 1 | All nodes agree on tipset at a finalized height |
| `DoHeightProgression` | `STRESS_WEIGHT_HEIGHT_PROGRESSION` | 1 | All node heights within 10 epochs of each other |
| `DoPeerCount` | `STRESS_WEIGHT_PEER_COUNT` | 1 | Every node has at least 1 peer |
| `DoHeadComparison` | `STRESS_WEIGHT_HEAD_COMPARISON` | 1 | Finalized tipset keys match across nodes |
| `DoStateRootComparison` | `STRESS_WEIGHT_STATE_ROOT` | 1 | Parent state roots match at finalized height |
| `DoStateAudit` | `STRESS_WEIGHT_STATE_AUDIT` | 2 | State roots + parent messages/receipts match |
| `DoF3FinalityMonitor` | `STRESS_WEIGHT_F3_MONITOR` | 1 | Tracks F3 instance per-node, checks for regression |

### EVM / FVM (`evm_vectors.go`)

| Vector | Env Var | Base | Description |
|---|---|---|---|
| `DoDeployContracts` | `STRESS_WEIGHT_DEPLOY` | 1 | Deploy EVM contracts via EAM.CreateExternal |
| `DoContractCall` | `STRESS_WEIGHT_CONTRACT_CALL` | 2 | Invoke contracts: recursion, delegatecall, tokens |
| `DoSelfDestructCycle` | `STRESS_WEIGHT_SELFDESTRUCT` | 1 | Deploy, destroy, cross-node state verify |
| `DoConflictingContractCalls` | `STRESS_WEIGHT_CONTRACT_RACE` | 1 | Same-nonce conflicting calls to different nodes |

### Cross-Node Divergence (`crossnode_vectors.go`)

| Vector | Env Var | Base | Description |
|---|---|---|---|
| `DoReceiptAudit` | `STRESS_WEIGHT_RECEIPT_AUDIT` | 1 | Compare receipt fields (exit, gas, return) across all nodes |
| `DoMessageOrderingAttack` | `STRESS_WEIGHT_MSG_ORDERING` | 1 | Multiple wallets send to same contract via different nodes |
| `DoNonceBombard` | `STRESS_WEIGHT_NONCE_BOMBARD` | 1 | Gapped nonces to node A, fill gaps via node B |
| `DoGasExhaustionEdge` | `STRESS_WEIGHT_GAS_EXHAUST` | 1 | High-gas call competes with small transfers |

### State Tree (`state_vectors.go`, `consensus_vectors.go`)

| Vector | Env Var | Base | Description |
|---|---|---|---|
| `DoActorMigrationStress` | `STRESS_WEIGHT_ACTOR_MIGRATION` | 1 | Burst-create actors, destroy some, verify HAMT |
| `DoActorLifecycleStress` | `STRESS_WEIGHT_ACTOR_LIFECYCLE` | 1 | Full lifecycle: deploy, fund, invoke, destroy, interact with dead |
| `DoHeavyCompute` | `STRESS_WEIGHT_HEAVY_COMPUTE` | 1 | Re-execute StateCompute at finalized epochs, verify root |

### Chaos / Adversarial (`reorg_vectors.go`, `miner_disruption_vectors.go`, `mempool_vectors.go`)

| Vector | Env Var | Base | Description |
|---|---|---|---|
| `DoReorgChaos` | `STRESS_WEIGHT_REORG` | 1 | Partition node, mine 1-3 blocks, heal -- rapid split/heal cycles |
| `DoPowerAwareSlash` | `STRESS_WEIGHT_POWER_SLASH` | 1 | Fabricate equivocation faults with quorum guards |
| `DoQuorumBoundaryTest` | `STRESS_WEIGHT_QUORUM_STALL` | 1 | Slash enough to break F3 quorum, check stall/recovery |
| `DoTransferMarket` | `STRESS_WEIGHT_TRANSFER` | 1 | Random FIL transfers between wallets |
| `DoGasWar` | `STRESS_WEIGHT_GAS_WAR` | 1 | Same-nonce replacement with higher gas premium |
| `DoAdversarial` | `STRESS_WEIGHT_ADVERSARIAL` | 1 | Double-spend races, invalid signatures, nonce races |

### Resource Stress (`evm_vectors.go`)

| Vector | Env Var | Base | Description |
|---|---|---|---|
| `DoMaxBlockGas` | `STRESS_WEIGHT_MAX_BLOCK_GAS` | 1 | Tight keccak256 loop to max block gas |
| `DoLogBlaster` | `STRESS_WEIGHT_LOG_BLASTER` | 1 | Emits massive events to stress receipt/bloom storage |
| `DoMemoryBomb` | `STRESS_WEIGHT_MEMORY_BOMB` | 1 | Allocates EVM memory with quadratic cost |
| `DoStorageSpam` | `STRESS_WEIGHT_STORAGE_SPAM` | 1 | Writes many unique storage slots per call |

### FOC (`foc_vectors.go`)

| Vector | Env Var | Base | Description |
|---|---|---|---|
| `DoFOCLifecycle` | `STRESS_WEIGHT_FOC_LIFECYCLE` | 3 | Sequential state machine (Init through Ready) |
| `DoFOCUploadPiece` | `STRESS_WEIGHT_FOC_UPLOAD` | 2 | Upload random data to Curio PDP API |
| `DoFOCAddPieces` | `STRESS_WEIGHT_FOC_ADD_PIECES` | 1 | Add pieces to on-chain proofset |
| `DoFOCMonitorProofSet` | `STRESS_WEIGHT_FOC_MONITOR` | 3 | Query proofset health + USDFC balances |
| `DoFOCRetrieveAndVerify` | `STRESS_WEIGHT_FOC_RETRIEVE` | 1 | Download piece and verify CID |
| `DoFOCTransfer` | `STRESS_WEIGHT_FOC_TRANSFER` | 1 | ERC-20 USDFC transfer |
| `DoFOCSettle` | `STRESS_WEIGHT_FOC_SETTLE` | 1 | Settle active payment rail |
| `DoFOCWithdraw` | `STRESS_WEIGHT_FOC_WITHDRAW` | 1 | Withdraw USDFC from FilecoinPay |
| `DoFOCDeletePiece` | `STRESS_WEIGHT_FOC_DELETE_PIECE` | 0 | Schedule piece deletion (opt-in) |
| `DoFOCDeleteDataSet` | `STRESS_WEIGHT_FOC_DELETE_DS` | 0 | Delete dataset + reset lifecycle (opt-in) |

---

## Extending the Stress Engine

### Adding a New Vector

1. Implement `DoMyVector()` in the appropriate `*_vectors.go` file
2. Add one line to `coreVectors()` in `profiles.go`:
   ```go
   {"DoMyVector", "STRESS_WEIGHT_MY_VECTOR", DoMyVector, CatEVM, 1},
   ```
3. The vector inherits its weight from whichever profile is active. No need to touch any profile definitions unless you want to boost it in a specific profile.

### Adding a New Category

1. Add a constant in `profiles.go`:
   ```go
   CatMyCategory Category = "mycategory"  // what it tests
   ```
2. Add it to `allCategories` slice
3. Add multiplier entries to whichever profiles should enable it. Profiles that omit a category default to multiplier 0 (disabled).

### Adding a New Profile

Add an entry to the `profiles` map in `profiles.go`:

```go
"myprofile": {
    Name:        "myprofile",
    Description: "Short description of when to use this",
    Multipliers: map[Category]int{
        CatConsensus: 3, CatChaos: 5,
        // omitted categories default to 0
    },
},
```

Select with `STRESS_PROFILE=myprofile`. It automatically appears in `STRESS_PROFILE=help` output.

---

## Branching Model: Running Your Own Tests

The **main branch is the scaffold**. It runs the `default` profile for nightly CI. Implementation teams branch off main to run their own test campaigns.

### How it works

```
main (scaffold)
  |-- STRESS_PROFILE=default
  |-- Nightly CI runs on Antithesis
  |-- All profiles, categories, and core vectors
  |
  +-- branch: nv28-testing
  |     |-- Adds FIP-0112, FIP-0113, FIP-0114 vectors to `upgrade` category
  |     |-- Sets STRESS_PROFILE=upgrade in docker-compose.yaml
  |     +-- Runs on Antithesis independently
  |
  +-- branch: forest-sync-testing
  |     |-- Adds Forest-specific sync vectors
  |     |-- Sets STRESS_PROFILE=consensus
  |     +-- Overrides: STRESS_CATEGORY_CROSSNODE=5
  |
  +-- branch: nsplit-research
        |-- Adds custom attack vectors to `chaos` category
        |-- Sets STRESS_PROFILE=nsplit
        +-- Runs targeted adversarial campaigns
```

### Creating a test campaign branch

1. **Branch off main:**
   ```bash
   git checkout main && git pull
   git checkout -b my-test-campaign
   ```

2. **Add your vectors** to the appropriate `*_vectors.go` file and register in `coreVectors()`:
   ```go
   // in profiles.go, coreVectors()
   {"DoMyTest", "STRESS_WEIGHT_MY_TEST", DoMyTest, CatUpgrade, 1},
   ```

3. **Set your profile** in `docker-compose.yaml`:
   ```yaml
   - STRESS_PROFILE=upgrade
   ```
   Or create a custom profile in `profiles.go` if the existing ones don't fit.

4. **Push and run** on Antithesis via the GitHub Actions workflow.

5. **No need to merge back to main.** Campaign branches are independent. When the campaign ends (e.g., nv28 ships to mainnet), the branch can be archived or deleted.

### What stays on main

- The profile system and all built-in profiles
- Core vectors (consensus, evm, crossnode, state, chaos, resource)
- The `upgrade` category (empty on main -- populated on campaign branches)
- The `default` profile for nightly runs

### What lives on branches

- FIP-specific or campaign-specific vectors
- Custom profiles (if needed)
- Profile selection in docker-compose (`STRESS_PROFILE=upgrade`)
- Any upgrade epoch configuration changes in `.env`

---

## Other Configuration

| Env Var | Description | Default |
|---|---|---|
| `STRESS_NODES` | Comma-separated node names | `lotus0` |
| `STRESS_RPC_PORT` | RPC port for Lotus nodes | `1234` |
| `STRESS_FOREST_RPC_PORT` | RPC port for Forest nodes | `3456` |
| `STRESS_KEYSTORE_PATH` | Path to pre-funded wallet keystore | `/shared/configs/stress_keystore.json` |
| `STRESS_WAIT_HEIGHT` | Block height to wait for before starting | `10` |
| `STRESS_DEBUG` | Set to `1` for verbose per-action logging | unset |

## Source Files

```
workload/cmd/stress-engine/
|-- main.go                      # Entry point, deck builder, action loop
|-- profiles.go                  # Profiles, categories, vector registry, weight resolution
|-- helpers.go                   # Shared: baseMsg, signMsg, pushMsg, nodeType
|-- mempool_vectors.go           # Transfer, gas war, adversarial vectors
|-- evm_vectors.go               # Contract deploy, invoke, selfdestruct, resource stress
|-- consensus_vectors.go         # Tipset consensus, height, peers, state roots, audit
|-- crossnode_vectors.go         # Receipt audit, msg ordering, nonce bombard
|-- state_vectors.go             # Actor migration, lifecycle stress
|-- reorg_vectors.go             # Partition/mine/heal chaos cycles
|-- miner_disruption_vectors.go  # Power slashing, F3 monitoring, quorum tests
|-- foc_vectors.go               # FOC lifecycle + steady-state vectors
+-- contracts.go                 # EVM bytecodes, ABI encoding
```

## Building

```bash
cd workload
go build ./cmd/stress-engine
# or build Docker image:
docker build -t workload:test .
```

## Assertions

Uses Antithesis SDK assertions:
```go
assert.Always(condition, "id", details)    // Must always hold (safety)
assert.Sometimes(condition, "id", details) // Must hold at least once (liveness)
```
