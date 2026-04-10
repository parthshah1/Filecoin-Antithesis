# Testing Security PoCs with Antithesis

How to take a vulnerability proof-of-concept, integrate it into the Filecoin Antithesis workload, and run an isolated test -- without pushing sensitive code to the public repo.

This guide covers both workload types:
- **Protocol-fuzzer** — for wire-protocol bugs (CBOR, libp2p, ChainExchange, GossipSub, F3)
- **Stress-engine** — for chain-level bugs (consensus, EVM, mempool, state, reorgs)

Uses the **CBOR Memory Amplification PoC (PoC 005)** as a worked example throughout.

---

## Table of Contents

1. [How the System Works](#1-how-the-system-works)
2. [Prerequisites](#2-prerequisites)
3. [Step 1 — Validate the PoC Locally](#3-step-1--validate-the-poc-locally)
4. [Step 2 — Write the Attack Vector](#4-step-2--write-the-attack-vector)
5. [Step 3 — Register It in the Deck](#5-step-3--register-it-in-the-deck)
6. [Step 4 — Create an Isolated Run Profile](#6-step-4--create-an-isolated-run-profile)
7. [Step 5 — Build the Docker Images](#7-step-5--build-the-docker-images)
8. [Step 6 — Push to Private Registry and Launch](#8-step-6--push-to-private-registry-and-launch)
9. [Step 7 — Triage the Results](#9-step-7--triage-the-results)
10. [Reference — Architecture Quick Look](#10-reference--architecture-quick-look)
11. [Reference — Checklist](#11-reference--checklist)

---

## 1. How the System Works

The Filecoin Antithesis harness runs a multi-node devnet (4 Lotus nodes, 4 miners, 1 Forest node) inside Antithesis. Two workload processes run alongside the network:

| Process | What it does | Source |
|---------|-------------|--------|
| **stress-engine** | Submits transactions, checks consensus, verifies state, runs EVM contracts | `workload/cmd/stress-engine/` |
| **protocol-fuzzer** | Connects as a malicious libp2p peer, sends crafted wire-protocol payloads | `workload/cmd/protocol-fuzzer/` |

Both use a **weighted deck** of actions. Each action has an environment variable controlling its weight. Setting a weight to 0 disables that action. Setting all others to 0 isolates a single action.

The entrypoint (`workload/entrypoint/entrypoint.sh`) launches both processes. Either can be disabled independently:
- `STRESS_ENGINE_ENABLED=0` — skips the stress-engine entirely
- `FUZZER_ENABLED=0` — skips the protocol-fuzzer entirely

Two Docker images control each test run:
- **workload image** — compiled binaries (stress-engine + protocol-fuzzer)
- **config image** — `docker-compose.yaml`, `.env`, and per-profile env files

Antithesis selects a profile via the `custom.setup` parameter, which swaps the active `.env` file.

**Nothing needs to be pushed to GitHub.** Images are built locally and pushed to a private Google Artifact Registry (GAR). Source code never leaves your machine.

---

## 2. Prerequisites

### Tools

| Tool | Install | Purpose |
|------|---------|---------|
| Docker | `apt install docker.io` or Docker Desktop | Build and push images |
| Go 1.23+ | `go.dev/dl` | Compile locally for quick validation |
| snouty | `curl -sSL https://antithesis.com/install-snouty \| bash` | CLI to trigger Antithesis runs |
| make | Pre-installed on most systems | Build targets |

### Credentials (one-time setup)

All credentials are in **1Password**. Ask the team for access.

```bash
# Add to ~/.bashrc or ~/.zshrc
export ANTITHESIS_TENANT=cobalt-bunny
export ANTITHESIS_USERNAME=filecoin
export ANTITHESIS_PASSWORD='<from 1Password>'
export ANTITHESIS_REPOSITORY=us-central1-docker.pkg.dev/molten-verve-216720/filecoin-repository
```

Authenticate Docker to the private registry:
```bash
# The GAR key is a JSON service account key file
cat /path/to/gar-key.json | docker login -u _json_key --password-stdin us-central1-docker.pkg.dev
```

Verify:
```bash
snouty doctor
# Should show all checks passed
```

**If you don't have credentials:** See [Alternative: gh workflow run](#alternative-gh-workflow-run) in Step 6.

---

## 3. Step 1 — Validate the PoC Locally

Before writing any workload code, confirm the vulnerability with a standalone program. This should:

- Require **zero** external dependencies (no running Lotus node, no network)
- Prove the specific claim (memory amplification ratio, crash condition, etc.)
- Produce measurable output (heap stats, panic trace, timing)

### Example: PoC 005

```bash
go run poc005_amplification/main.go --full
```
```
ATTACK: 419 tipsets x 150k msgs   119.88 MB    962.46 MB    8.03x    125,700,000
```

8x amplification confirmed. Proceed to integration.

---

## 4. Step 2 — Write the Attack Vector

Choose the right workload based on what the PoC targets:

| If the PoC targets... | Use | Directory |
|-----------------------|-----|-----------|
| Wire protocol parsing (CBOR, libp2p streams, GossipSub) | Protocol-fuzzer | `workload/cmd/protocol-fuzzer/` |
| Chain-level behavior (consensus, EVM, mempool, state, reorgs) | Stress-engine | `workload/cmd/stress-engine/` |

### For protocol-fuzzer vectors

Create a new file: `workload/cmd/protocol-fuzzer/<your_attack>.go`

You need three things:

**A payload builder** — constructs the malicious CBOR/wire-format bytes:
```go
func buildYourPayload(...) []byte {
    var buf bytes.Buffer
    // Use cbg.WriteMajorTypeHeader for raw CBOR, or helpers from cbor_helpers.go
    return buf.Bytes()
}
```

**A delivery function** — sends the payload to a target node. Choose a delivery channel:

| Channel | Protocol | Max size | When to use |
|---------|----------|----------|-------------|
| GossipSub | `/fil/blocks/`, `/fil/msgs/` | 1 MiB | Block/message validation bugs |
| ChainExchange server | `/fil/chain/xchg/0.0.1` | 120 MiB | Chain sync response bugs |
| Hello | `/fil/hello/1.0.0` | ~64 KB | Peer handshake bugs |
| F3 protocols | `/f3/granite/...` | varies | F3 consensus bugs |

For ChainExchange (most common for large payloads):
```go
func runYourAttack(ctx context.Context, target TargetNode) {
    headInfo := fetchChainHead(target.Name)
    payload := buildYourPayload(...)
    triggerBlock := buildBlockHeaderCBOR(blockHeaderOpts{
        overrideParentCIDs: headInfo.CIDs,
        overrideHeight:     headInfo.Height + 1,
        overrideWeight:     999999999,
    })
    runGenericExchangeServerAttack(ctx, target, "your-attack", payload, triggerBlock)
}
```

For GossipSub:
```go
func runYourAttack() {
    data := buildYourPayload(...)
    publishBlock(data)   // or publishMsg(data)
}
```

**A registry function**:
```go
func getAllYourAttacks() []namedAttack {
    return []namedAttack{
        {
            name:       "exchange/your-attack-name",
            targetedFn: func(t TargetNode) { runYourAttack(ctx, t) },
            targetType: nodeLotus,  // or nodeForest, nodeAny
        },
    }
}
```

### For stress-engine vectors

Create a new file: `workload/cmd/stress-engine/<your_vector>.go`

You need one function matching this signature:

```go
func DoYourVector() {
    // Pick a node
    node := nodes[randomKey()]

    // Use the Lotus RPC API to interact with the chain
    // e.g. node.MpoolPushMessage, node.StateGetActor, node.EthCall, etc.

    // Use Antithesis assertions to flag results
    assert.Always(condition, "YourVector: safety property", details)
    assert.Sometimes(condition, "YourVector: liveness property", details)
}
```

The stress-engine has access to:
- `nodes` map — RPC connections to all Lotus/Forest nodes
- `addrs` / `keystore` — pre-funded wallets for submitting transactions
- `nonces` — per-address nonce tracking
- `deployedContracts` — registry of deployed EVM contracts
- All Lotus API types via the `api.FullNode` interface

### Verify it compiles

```bash
cd workload && go build ./cmd/protocol-fuzzer/   # if fuzzer vector
cd workload && go build ./cmd/stress-engine/      # if stress vector
```

---

## 5. Step 3 — Register It in the Deck

### Protocol-fuzzer

Edit `workload/cmd/protocol-fuzzer/main.go`. In `buildDeck()`, add a new category:

```go
{"FUZZER_WEIGHT_YOUR_ATTACK", 0, getAllYourAttacks()},
```

### Stress-engine

Edit `workload/cmd/stress-engine/main.go`. In `buildDeck()`, add to the appropriate list:

```go
// In the 'stress' slice (for non-FOC vectors):
{"DoYourVector", "STRESS_WEIGHT_YOUR_VECTOR", DoYourVector, 0},

// Or in the 'consensus' slice (if it's a passive check that should always run):
{"DoYourCheck", "STRESS_WEIGHT_YOUR_CHECK", DoYourCheck, 0},
```

**The default weight must be 0.** This ensures the vector never fires during nightly or scheduled runs. It only activates when explicitly set in a profile.

---

## 6. Step 4 — Create an Isolated Run Profile

Create a new file at the repo root: `env.<your-profile>`

This file controls exactly what runs. For an isolated test, disable everything except your vector.

### If testing a protocol-fuzzer vector:

```bash
STRESS_ENGINE_ENABLED=0                    # stress-engine OFF
FUZZER_ENABLED=1                           # protocol-fuzzer ON
FUZZER_WEIGHT_YOUR_ATTACK=3               # your vector: ON
# all other FUZZER_WEIGHT_*=0
```

### If testing a stress-engine vector:

```bash
STRESS_ENGINE_ENABLED=1                    # stress-engine ON
FUZZER_ENABLED=0                           # protocol-fuzzer OFF
STRESS_WEIGHT_YOUR_VECTOR=3               # your vector: ON
# all other STRESS_WEIGHT_*=0
```

The file must also include the full node configuration (ports, paths, network heights). The simplest approach: **copy `env.nightly` and replace the workload profile section** at the bottom.

### Wire it into the config image

Add one line to `Dockerfile` (repo root):

```dockerfile
COPY ./env.<your-profile> /profiles/env.<your-profile>
```

---

## 7. Step 5 — Build the Docker Images

```bash
# Workload image (contains the new code)
make build-workload

# Config image (contains the new profile)
docker build -t config:<your-tag> -f Dockerfile .
```

### Tag for the registry

```bash
TAG=<your-tag>   # e.g. "amplification-v1", "poc007-test"
GAR=$ANTITHESIS_REPOSITORY

docker tag workload:latest $GAR/workload:$TAG
docker tag config:$TAG $GAR/config:$TAG
```

### Optional: local-only test first

To validate locally before pushing to Antithesis:
```bash
make build-workload
# Edit .env with your profile settings
make up-full
docker logs -f workload   # watch output
```

---

## 8. Step 6 — Push to Private Registry and Launch

### Push images

```bash
docker push $GAR/workload:$TAG
docker push $GAR/config:$TAG
```

Images go to `us-central1-docker.pkg.dev/molten-verve-216720/filecoin-repository/` — a **private** registry. No source code is uploaded. Only compiled Docker images.

**This does not affect nightly runs.** Nightly uses `latest` tags. Your custom tags are completely separate.

### Launch the Antithesis run

```bash
snouty run \
  --webhook filecoin \
  --config-image $GAR/config:$TAG \
  --test-name "<descriptive-name>" \
  --description "<one-line summary>" \
  --duration <minutes> \
  --ephemeral \
  --source <source-tag> \
  --param custom.setup=<your-profile>
```

| Flag | Purpose |
|------|---------|
| `--webhook filecoin` | Antithesis notebook endpoint for Filecoin |
| `--config-image` | Your pre-built config image in GAR |
| `--duration` | Test duration in **minutes** (e.g. `120` = 2 hours) |
| `--ephemeral` | Keeps the run out of findings history — use for experiments |
| `--source` | Separates property history from nightly runs |
| `--param custom.setup=` | Which `env.*` profile to activate |

### Alternative: `gh workflow run`

If you **don't** have GAR/Antithesis credentials, you can use GitHub Actions. This requires pushing code to a branch.

> **Security note:** This repo is public. Pushing PoC code makes it visible.
> For sensitive vulnerabilities, get GAR access (ask the team) or use a private fork.

```bash
git push origin your-branch

gh workflow run "Build & Push Workload" --ref your-branch -f workload_tag=$TAG
gh workflow run "Build & Push Config" --ref your-branch -f config_tag=$TAG

# Wait for builds
gh run list --workflow="Build & Push Workload" --limit 1
gh run list --workflow="Build & Push Config" --limit 1

gh workflow run "Run Antithesis Test" --ref your-branch \
  -f endpoint=filecoin -f setup=<your-profile> -f duration=2 \
  -f workload=$TAG -f config=$TAG \
  -f drand=latest -f forest=latest -f lotus=latest \
  -f curio=latest -f filwizard=latest \
  -f emails=you@example.com -f is_ephemeral=true -f is_smoke_test=false

git push origin --delete your-branch   # clean up after
```

---

## 9. Step 7 — Triage the Results

### What to look for

| Signal | Where | Meaning |
|--------|-------|---------|
| Container restarts / OOM kills | Container events | Attack crashed a node |
| `Always` assertion failures | Properties tab | A safety property was violated |
| `Sometimes` assertion failures | Properties tab | A liveness property stopped firing |
| Timeline correlation | Logs + timeline | Attack delivery timestamps align with failures |

### Using the triage skill

```
/antithesis-triage <run-url>
```

### Interpreting outcomes

| Outcome | Interpretation |
|---------|---------------|
| Node OOM-killed or crashed | Vulnerability confirmed |
| Node recovers but stalls temporarily | Attack degrades performance |
| All assertions pass | Node absorbed the attack, or payload didn't reach the vulnerable path |
| Workload logs show timeouts | Delivery mechanism needs adjustment |

---

## 10. Reference — Architecture Quick Look

### Stress-engine vector structure

```
workload/cmd/stress-engine/
├── main.go                    # buildDeck(), main loop, global state
├── consensus_vectors.go       # Passive checks: tipset agreement, height, state roots
├── crossnode_vectors.go       # Cross-node receipt/state comparison
├── evm_vectors.go             # EVM contract deploy, call, selfdestruct
├── mempool_vectors.go         # Gas wars, nonce races, adversarial txs
├── reorg_vectors.go           # Partition/heal reorg cycles
├── miner_disruption_vectors.go # Power slashing
├── nsplit_vectors.go          # Structured EC/F3 partition tests
├── drand_vectors.go           # Drand beacon consistency
├── foc_vectors.go             # Curio PDP lifecycle (FOC profile only)
├── state_vectors.go           # State tree walk, actor migration stress
├── helpers.go                 # Shared utilities
└── contracts.go               # Embedded EVM bytecodes
```

**Adding a vector**: Create a new `func DoYourVector()` in an existing or new `_vectors.go` file. Register it in `buildDeck()` in `main.go` with `{"DoYourVector", "STRESS_WEIGHT_YOUR_VECTOR", DoYourVector, 0}`.

### Protocol-fuzzer vector structure

```
workload/cmd/protocol-fuzzer/
├── main.go                    # buildDeck(), main loop, global state
├── cbor_helpers.go            # CBOR encoding + wire format builders
├── cbor_bombs.go              # CBOR length-prefix OOM / stack exhaustion
├── exchange_server.go         # ChainExchange server attacks
├── exchange_client.go         # ChainExchange client helpers
├── hello_attacks.go           # Hello protocol attacks
├── gossip_attacks.go          # GossipSub attacks
├── f3_attacks.go              # F3 consensus attacks
├── serialization_attacks.go   # Round-trip serialization bombs
├── chaos_driver.go            # libp2p connection abuse
├── amplification_attack.go    # CBOR memory amplification (PoC 005)
├── config.go                  # Env var parsing, RNG
├── discovery.go               # Peer discovery, RPC
├── identity.go                # Ephemeral libp2p host pool
├── valid_cids.go              # Pre-computed CIDs
└── mpool_attacks.go           # Mempool attacks
```

**Adding a vector**: Create a new `_attack.go` file. Export `getAllYourAttacks() []namedAttack`. Register in `buildDeck()` in `main.go` with `{"FUZZER_WEIGHT_YOUR_ATTACK", 0, getAllYourAttacks()}`.

### Profile files

```
env.nightly         # Default: all vectors at moderate weights
env.consensus       # Consensus-focused
env.drand           # Drand-focused
env.fip             # FIP testing
env.foc             # Curio PDP lifecycle
env.amplification   # PoC 005: CBOR memory amplification only
```

---

## 11. Reference — Checklist

- [ ] Standalone PoC validates the hypothesis locally
- [ ] New `.go` file in the appropriate workload directory
- [ ] Vector registered in `buildDeck()` with **default weight 0**
- [ ] `go build` succeeds for the modified command
- [ ] Profile `env.<name>` created (copied from `env.nightly`, workload section replaced)
- [ ] Profile added to `Dockerfile`: `COPY ./env.<name> /profiles/env.<name>`
- [ ] Workload image built: `make build-workload`
- [ ] Config image built: `docker build -t config:<tag> -f Dockerfile .`
- [ ] Both images tagged and pushed to private GAR
- [ ] Run launched: `snouty run --ephemeral --param custom.setup=<name>`
- [ ] Report received and triaged

---

## Worked Example: PoC 005 — CBOR Memory Amplification

**Vulnerability**: A 120 MB ChainExchange response causes ~960 MB heap allocation by filling message arrays with CBOR null bytes (1 byte wire, 8 bytes heap per nil pointer). Stays within Lotus's per-field limit of 150,000 but compounds across 419 tipsets.

| Step | What we did |
|------|------------|
| **PoC** | Standalone Go program confirmed 8.03x amplification ratio |
| **Vector** | Created `amplification_attack.go` with 4 variants (small/medium/full + alloc-before-read) |
| **Deck** | Added `FUZZER_WEIGHT_CHAINEXCHANGE_AMPLIFICATION` to `main.go` (default weight 0) |
| **Profile** | Created `env.amplification`: stress-engine OFF, only amplification fuzzer ON |
| **Config** | Added `COPY` to Dockerfile for the new profile |
| **Build** | `make build-workload` + `docker build -t config:amplification-v1 -f Dockerfile .` |
| **Push** | `docker push` both images to private GAR as `:amplification-v1` |
| **Run** | `snouty run --duration 120 --ephemeral --param custom.setup=amplification` |

**Result**: 2-hour ephemeral run submitted. No code pushed to GitHub. Nightly runs unaffected (different image tags).
