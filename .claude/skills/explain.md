# /explain -- Filecoin-Antithesis Architecture Guide

## When to trigger
Use this skill when the user asks architectural questions about the repo, wants to understand how a component works, asks about profiles, vectors, the deck system, FOC lifecycle, F3/consensus, Antithesis SDK, docker topology, or any "how does X work" question about this codebase.

## Instructions

You are answering architectural questions about the Filecoin-Antithesis chaos testing harness. Always read the actual source files to answer -- never rely on stale memory or embedded snippets. Below is a topic-to-file map. For any question, identify the relevant topic(s), read the listed files, and synthesize an accurate answer.

### Step 1: Identify the topic area

Map the user's question to one or more of these topic areas:

| Topic | Files to Read |
|-------|--------------|
| **Deck system / main loop** | `workload/cmd/stress-engine/main.go` — `buildDeck()`, main loop, globals, `weightedAction` type |
| **Helper functions** | `workload/cmd/stress-engine/helpers.go` — `baseMsg`, `signMsg`, `pushMsg`, `pushMsgWithCid`, `waitForMsg`, `pushMsgManualNonce`, `pickTwoDistinctNodes`, `verifyActorConsistency`, `debugLog` |
| **Consensus vectors** | `workload/cmd/stress-engine/consensus_vectors.go` — `DoTipsetConsensus`, `DoHeightProgression`, `DoPeerCount`, `DoHeadComparison`, `DoStateRootComparison`, `DoStateAudit`, `DoHeavyCompute` |
| **F3 finality** | `workload/cmd/stress-engine/consensus_vectors.go` — `DoF3FinalityMonitor`, `DoF3FinalityAgreement` |
| **Mempool / transfer vectors** | `workload/cmd/stress-engine/mempool_vectors.go` — `DoTransferMarket`, `DoGasWar`, `DoAdversarial`, `doNonceRace` |
| **EVM contract vectors** | `workload/cmd/stress-engine/evm_vectors.go` — `DoDeployContracts`, `DoContractCall`, `DoSelfDestructCycle`, `DoConflictingContractCalls`, `DoMaxBlockGas`, `DoLogBlaster`, `DoMemoryBomb`, `DoStorageSpam` |
| **Contract bytecodes / EVM helpers** | `workload/cmd/stress-engine/contracts.go` — embedded hex bytecodes, `initContractBytecodes`, `calcSelector`, `encodeUint256`, `cborWrapCalldata`, `deployContract`, `invokeContract` |
| **Cross-node vectors** | `workload/cmd/stress-engine/crossnode_vectors.go` — `DoReceiptAudit`, `DoMessageOrderingAttack`, `DoNonceBombard`, `DoGasExhaustionEdge` |
| **State tree vectors** | `workload/cmd/stress-engine/state_vectors.go` — `DoActorMigrationStress`, `DoActorLifecycleStress` |
| **Drand vectors** | `workload/cmd/stress-engine/drand_vectors.go` — `DoDrandBeaconAudit` |
| **Reorg vectors** | `workload/cmd/stress-engine/reorg_vectors.go` — `DoReorgChaos` (partition/heal cycles, power-aware victim selection) |
| **N-split / consensus lifecycle** | `workload/cmd/stress-engine/nsplit_vectors.go` — `startConsensusTestLifecycle`, partition strategies, attack types |
| **Miner disruption / power** | `workload/cmd/stress-engine/miner_disruption_vectors.go` — `DoPowerAwareSlash`, F3 power table, `minerPowerInfo` |
| **FOC lifecycle** | `workload/cmd/stress-engine/foc_vectors.go` — state machine (`focLifecycleState`: Init->Approved->Deposited->OperatorApproved->DataSetCreated->Ready), `DoFOCLifecycle`, all steady-state vectors (`DoFOCUploadPiece`, `DoFOCAddPieces`, `DoFOCMonitorProofSet`, `DoFOCRetrieveAndVerify`, `DoFOCTransfer`, `DoFOCSettle`, `DoFOCWithdraw`, `DoFOCDeletePiece`, `DoFOCDeleteDataSet`) |
| **FOC config / parsing** | `workload/internal/foc/config.go` — `Config` struct, `ParseEnvironment`, contract addresses, key loading |
| **FOC EVM helpers** | `workload/internal/foc/eth.go` — `SendEthTx`, `SendEthTxConfirmed`, `BuildCalldata`, `EthCallUint256`, `EncodeAddress`, `EncodeBigInt` |
| **FOC ABI selectors** | `workload/internal/foc/selectors.go` — all Solidity function selectors (`SigApprove`, `SigDeposit`, `SigCreateDataSet`, etc.) |
| **FOC Curio API** | `workload/internal/foc/curio.go` — `CreateDataSetHTTP`, `UploadPiece`, `AddPiecesHTTP`, `DownloadPiece`, `GetDataSet` |
| **FOC EIP-712** | `workload/internal/foc/eip712.go` — `SignEIP712CreateDataSet` |
| **FOC CommP** | `workload/internal/foc/commp.go` — `CalculatePieceCID` |
| **FOC sidecar** | `workload/cmd/foc-sidecar/` — independent invariant checker running alongside stress engine |
| **Protocol fuzzer** | `workload/cmd/protocol-fuzzer/` — gossip, F3, hello protocol fuzzing |
| **Chain client** | `workload/internal/chain/client.go` — `ConnectNodes`, JWT auth, Forest port handling |
| **Genesis prep** | `workload/cmd/genesis-prep/main.go` — deterministic wallet generation (HKDF-SHA256), genesis allocs |
| **Profiles** | `env.foc`, `env.consensus`, `env.drand`, `env.nightly`, `env.fip` — read the `STRESS_WEIGHT` section |
| **Docker topology** | `docker-compose.yaml` — services, volumes, profiles, healthchecks, env var defaults |
| **Entrypoint / boot sequence** | `workload/entrypoint/entrypoint.sh` — genesis-prep, chain wait, FOC setup, readiness markers, process launch |
| **Build system** | `Makefile`, `Dockerfile` |

### Step 2: Read the files

Read each relevant file using the Read tool. For large files, read only the sections relevant to the question. Do NOT guess or paraphrase -- always read first.

### Step 3: Answer with precision

- Quote exact function names, type names, and variable names from the source
- Explain the "why" behind design decisions when visible from comments or structure
- If the user asks about a **vector**: explain its purpose, what it asserts, which profile(s) it runs under, and its default weight
- If the user asks about a **profile**: compare its `STRESS_WEIGHT_*` settings against the defaults in `docker-compose.yaml` and describe the node topology
- Always mention the **assertion type** used and why: `Always` = safety invariant (must never be violated), `Sometimes` = liveness (should eventually hold), `Reachable` = coverage marker
- For **FOC vectors**: explain which lifecycle state they require and what FOC helpers they use
- For **Docker/topology questions**: describe the service dependency graph and which compose profile gates each service

### Key architectural facts

These are stable facts about the codebase that won't change between file reads:

1. **Flat package**: All stress-engine code is `package main` under `workload/cmd/stress-engine/`. No interfaces, no dependency injection -- just globals and flat functions.
2. **Deck system**: `buildDeck()` creates a flat `[]namedAction` by replicating each action `weight` times. The main loop picks uniformly at random via `rngIntn(len(deck))`.
3. **Profile branching**: In `buildDeck()`, consensus vectors are always included. Stress vectors are added only when `focCfg == nil`. FOC vectors are added only when `focCfg != nil`.
4. **Deterministic RNG**: All randomness via `random.GetRandom()` and `random.RandomChoice()` from the Antithesis SDK -- never `math/rand`. This enables deterministic replay of failures.
5. **Assertions**: `assert.Always(cond, msg, details)` = safety. `assert.Sometimes(cond, msg, details)` = liveness. `assert.Reachable(msg, details)` = coverage. Details must be `map[string]any`, never nil.
6. **FOC lifecycle**: Sequential state machine driven by deck picks of `DoFOCLifecycle`. Steady-state vectors call `requireReady()` and return early if lifecycle hasn't reached `focStateReady`.
7. **N-split lifecycle**: Background goroutine (`startConsensusTestLifecycle`), not in the deck. Runs partition-attack-heal cycles. Only when `focCfg == nil`.
8. **Profile files**: `env.*` files set shell variables that docker-compose substitutes into `${VAR:-default}` expressions. They control node count, network heights, and `STRESS_WEIGHT_*` overrides.
9. **Local signing**: Uses `sigs.Sign()` + `MpoolPush()`, not `node.WalletSign()`. Requires blank import of `_ "github.com/filecoin-project/lotus/lib/sigs/secp"`.
