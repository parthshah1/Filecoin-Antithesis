# /new-vector -- Interactive Test Vector Scaffolding

## When to trigger
Use this skill when the user wants to create a new stress test vector, add a new workload action, or asks how to write a vector for this repo.

## Instructions

You are scaffolding a new test vector for the Filecoin-Antithesis stress engine. Follow these steps exactly.

### Step 1: Gather requirements

Ask the user the following questions. Skip any they have already answered in their message.

1. **What does the vector test?** (brief description of the behavior or invariant)
2. **Which profile(s) should it target?**
   - `default` -- general stress testing (2 lotus, 2 miners, mixed vectors)
   - `consensus` -- EC/F3 safety under adversarial partitions (4 lotus, 4 miners, 1 forest, assertion-heavy, no message traffic)
   - `drand` -- beacon fault isolation (2 lotus, 2 miners, 1 forest, drand-focused)
   - `foc` -- Curio PDP testing (3 lotus, 3 miners, FOC lifecycle + Curio API)
   - `nightly` -- full regression (everything enabled at moderate weights)
   - `fip` -- FIP-specific upgrade testing (state audit heavy)
3. **What assertion type?**
   - Safety invariant (`assert.Always`) -- must NEVER be violated, even under fault injection
   - Liveness property (`assert.Sometimes`) -- should eventually hold; transient failures expected
   - Coverage marker (`assert.Reachable`) -- just confirms the code path was reached
4. **Does it send messages on-chain?** (determines which helpers to use)
5. **What default weight?** (0 = opt-in only, 1-3 = normal, 4+ = high frequency)

### Step 2: Read current patterns

Before generating any code, read these files to match current conventions exactly:

```
workload/cmd/stress-engine/main.go       -- buildDeck() structure, weightedAction type, globals
workload/cmd/stress-engine/helpers.go    -- all available helper functions and their signatures
```

Then read the vector file most similar to what the user wants:

| User's Category | File to Read |
|----------------|--------------|
| Consensus / assertion-only | `consensus_vectors.go` (DoTipsetConsensus, DoStateAudit) |
| Message-sending / mempool | `mempool_vectors.go` (DoTransferMarket, DoGasWar) |
| EVM / contract | `evm_vectors.go` (DoDeployContracts, DoContractCall) |
| Cross-node comparison | `crossnode_vectors.go` (DoReceiptAudit) |
| FOC / Curio PDP | `foc_vectors.go` (DoFOCUploadPiece, requireReady()) AND `workload/internal/foc/eth.go` |
| Reorg / partition | `reorg_vectors.go` (DoReorgChaos) |
| State tree | `state_vectors.go` (DoActorMigrationStress) |
| Miner / power | `miner_disruption_vectors.go` (DoPowerAwareSlash) |
| Drand | `drand_vectors.go` (DoDrandBeaconAudit) |

All vector files are in `workload/cmd/stress-engine/`.

### Step 3: Choose file placement

Place the new vector in the existing `*_vectors.go` file matching its category. Only create a new `*_vectors.go` file if the category is genuinely new. The file must be in `workload/cmd/stress-engine/` and use `package main`.

### Step 4: Generate the vector function

Use the appropriate template based on the target profile. Match the exact style of existing vectors in the file you read in Step 2.

---

**Template A -- Message-sending vector (default/stress/nightly profiles):**

```go
// ===========================================================================
// Vector: DoXxxYyy (Category Description)
// Stresses: what this tests
// ===========================================================================

func DoXxxYyy() {
	// Early guards
	if len(nodeKeys) < 2 {
		return
	}

	// Pick random node and wallet
	fromAddr, fromKI := pickWallet()
	toAddr, _ := pickWallet()
	if fromAddr == toAddr {
		return
	}

	nodeName, node := pickNode()

	// Build and send message
	amount := abi.NewTokenAmount(int64(rngIntn(100) + 1))
	msg := baseMsg(fromAddr, toAddr, amount)

	msgCid, ok := pushMsgWithCid(node, msg, fromKI, "xxx-yyy")
	if !ok {
		return
	}

	// Wait for on-chain inclusion
	result := waitForMsg(node, msgCid, "xxx-yyy")
	if result == nil {
		return
	}

	// Assert
	assert.Sometimes(result.Receipt.ExitCode.IsSuccess(), "Xxx yyy succeeds", map[string]any{
		"node":      nodeName,
		"exit_code": result.Receipt.ExitCode,
	})

	debugLog("[xxx-yyy] OK: %s via %s", fromAddr.String()[:12], nodeName)
}
```

---

**Template B -- Assertion-only vector (consensus profile):**

```go
// ===========================================================================
// Vector: DoXxxYyy (Consensus Description)
// Asserts: what safety/liveness property
// ===========================================================================

func DoXxxYyy() {
	if len(nodeKeys) < 2 {
		return
	}

	// Collect data from all nodes
	type result struct {
		node string
		val  string
	}
	var results []result

	for _, name := range nodeKeys {
		// Query each node...
		val, err := nodes[name].SomeRPC(ctx, ...)
		if err != nil {
			debugLog("[xxx-yyy] %s error: %v", name, err)
			continue
		}
		results = append(results, result{node: name, val: fmt.Sprintf("%v", val)})
	}

	if len(results) < 2 {
		return
	}

	// Compare across nodes
	allMatch := true
	for i := 1; i < len(results); i++ {
		if results[i].val != results[0].val {
			allMatch = false
			break
		}
	}

	details := map[string]any{
		"respondents": len(results),
	}
	for _, r := range results {
		details["val_"+r.node] = r.val
	}

	assert.Always(allMatch, "Xxx yyy consistent across nodes", details)
}
```

---

**Template C -- FOC vector (foc profile):**

```go
// ===========================================================================
// Vector: DoFOCXxxYyy (FOC Description)
// Stresses: what Curio/PDP behavior
// ===========================================================================

func DoFOCXxxYyy() {
	s, ok := requireReady()
	if !ok {
		return
	}

	node := focNode()

	// Use FOC helpers from workload/internal/foc/
	calldata := foc.BuildCalldata(foc.SigXxx, foc.EncodeAddress(targetAddr))

	ok = foc.SendEthTxConfirmed(ctx, node, focCfg.ClientKey, focCfg.ContractAddr, calldata, "foc-xxx")

	assert.Sometimes(ok, "FOC xxx succeeds", map[string]any{
		"proofset_id": s.OnChainDataSetID,
	})

	debugLog("[foc-xxx] OK")
}
```

---

### Naming conventions (MUST follow)

| Convention | Rule | Example |
|-----------|------|---------|
| Function name | Exported PascalCase, `Do` prefix | `DoGasWar`, `DoFOCUploadPiece` |
| Env var | `STRESS_WEIGHT_` + SCREAMING_SNAKE (drop the `Do` prefix) | `STRESS_WEIGHT_GAS_WAR`, `STRESS_WEIGHT_FOC_UPLOAD` |
| Log tag | lowercase-hyphenated in brackets | `[gas-war]`, `[foc-upload]` |
| RNG | `rngIntn()` / `rngChoice()` only | NEVER `math/rand` |
| Assertion details | Always `map[string]any{...}` | NEVER `nil` for Always/Sometimes |
| Globals | Access directly | `ctx`, `nodes`, `nodeKeys`, `nonces`, `keystore`, `addrs` |
| FOC imports | `"workload/internal/foc"` | Use `foc.SendEthTx`, `foc.BuildCalldata`, etc. |

### Step 5: Generate `buildDeck()` registration

Read `workload/cmd/stress-engine/main.go` and add the entry in the correct section:

**For consensus vectors** (always active, both profiles):
```go
// Add to the `consensus` slice in buildDeck():
{"DoXxxYyy", "STRESS_WEIGHT_XXX_YYY", DoXxxYyy, DEFAULT_WEIGHT},
```

**For stress vectors** (skipped when FOC active):
```go
// Add to the `stress` slice in buildDeck():
{"DoXxxYyy", "STRESS_WEIGHT_XXX_YYY", DoXxxYyy, DEFAULT_WEIGHT},
```

**For FOC vectors** (only when `focCfg != nil`):
```go
// Add inside the `if focCfg != nil { actions = append(actions, ...` block:
weightedAction{"DoFOCXxxYyy", "STRESS_WEIGHT_FOC_XXX_YYY", DoFOCXxxYyy, DEFAULT_WEIGHT},
```

### Step 6: Generate `docker-compose.yaml` env var

Read `docker-compose.yaml`, find the workload service environment section. Add the new variable in the correct comment-delimited group:

```yaml
- STRESS_WEIGHT_XXX_YYY=${STRESS_WEIGHT_XXX_YYY:-DEFAULT_WEIGHT}
```

### Step 7: Generate profile env entries

This is the most important step for profile awareness. Read EACH `env.*` file to see its format and existing weight section. Then add the new variable to ALL profiles:

**For a stress vector targeting default + nightly:**
| Profile | Value | Rationale |
|---------|-------|-----------|
| `env.foc` | `STRESS_WEIGHT_XXX_YYY=0` | Not relevant to FOC testing |
| `env.consensus` | `STRESS_WEIGHT_XXX_YYY=0` | Consensus profile runs assertions only |
| `env.drand` | `STRESS_WEIGHT_XXX_YYY=0` | Not relevant to drand testing |
| `env.nightly` | `STRESS_WEIGHT_XXX_YYY=2` | Included in full regression |
| `env.fip` | `STRESS_WEIGHT_XXX_YYY=0` | Not relevant to FIP testing |

**For a consensus vector:**
| Profile | Value | Rationale |
|---------|-------|-----------|
| `env.consensus` | `STRESS_WEIGHT_XXX_YYY=5` | Primary target profile |
| `env.foc` | `STRESS_WEIGHT_XXX_YYY=1` | Low -- secondary |
| `env.drand` | `STRESS_WEIGHT_XXX_YYY=3` | Medium -- health checks useful |
| `env.nightly` | `STRESS_WEIGHT_XXX_YYY=3` | Medium -- full regression |
| `env.fip` | `STRESS_WEIGHT_XXX_YYY=2` | Medium -- upgrade safety |

**For a FOC vector:**
| Profile | Value | Rationale |
|---------|-------|-----------|
| `env.foc` | `STRESS_WEIGHT_FOC_XXX_YYY=3` | Primary target profile |
| `env.consensus` | `STRESS_WEIGHT_FOC_XXX_YYY=0` | No Curio in consensus profile |
| `env.drand` | `STRESS_WEIGHT_FOC_XXX_YYY=0` | No Curio in drand profile |
| `env.nightly` | `STRESS_WEIGHT_FOC_XXX_YYY=0` | FOC vectors need FOC compose profile |
| `env.fip` | `STRESS_WEIGHT_FOC_XXX_YYY=0` | No Curio in FIP profile |

Adjust weights based on user's input. Always set to 0 for profiles where the vector cannot or should not run.

### Step 8: Present the complete changeset

Present all changes as a numbered checklist before implementing:

1. **Vector function** -- which file, full code
2. **buildDeck() registration** -- which section of `main.go`, the exact line
3. **docker-compose.yaml** -- the env var line and which section
4. **Profile entries** -- each `env.*` file, the line to add and where in the file

Ask the user to confirm before making any edits. Once confirmed, implement all changes (Steps 4-7), then proceed to Step 9.

### Step 9: Build and test the new vector in isolation

After implementing the changes, build and run the new vector in isolation to verify it compiles and executes correctly. This step uses the TARGET PROFILE the user specified in Step 1.

#### 9a: Build the stress engine

```bash
cd workload && go build ./cmd/stress-engine
```

If the build fails, fix the compile errors before proceeding. Do NOT skip this step.

#### 9b: Create a temporary isolated test env file

Copy the user's target profile and override ALL `STRESS_WEIGHT_*` values to 0, except the new vector's weight which gets set to 1. This ensures ONLY the new vector runs during the test.

1. Read the target profile file (e.g., `env.foc` if user chose FOC)
2. Create a temporary file `env.test-vector` at the repo root based on that profile
3. In the temporary file, set every `STRESS_WEIGHT_*` line to `=0` EXCEPT the new vector's env var which should be `=1`
4. For FOC vectors: also keep `STRESS_WEIGHT_FOC_LIFECYCLE=3` (the lifecycle state machine must run to reach Ready state, otherwise the new vector will never fire)

Example for a new stress vector `STRESS_WEIGHT_MY_VECTOR`:
```bash
# All other weights zeroed out:
STRESS_WEIGHT_TIPSET_CONSENSUS=0
STRESS_WEIGHT_HEIGHT_PROGRESSION=0
# ... every other weight = 0 ...
STRESS_WEIGHT_MY_VECTOR=1   # <-- ONLY this vector runs
```

Example for a new FOC vector `STRESS_WEIGHT_FOC_MY_VECTOR`:
```bash
# All other weights zeroed out, except lifecycle (needed to reach Ready):
STRESS_WEIGHT_FOC_LIFECYCLE=3   # <-- keep: must reach Ready state
STRESS_WEIGHT_FOC_MY_VECTOR=1   # <-- the vector under test
# ... everything else = 0 ...
```

#### 9c: Determine the correct compose command

Based on the target profile, select the right docker compose invocation:

| Target Profile | Compose Command |
|---------------|----------------|
| `default` / `nightly` | `docker compose --env-file env.test-vector up -d` |
| `consensus` / `drand` | `docker compose --env-file env.test-vector --profile full up -d` |
| `foc` | `docker compose --env-file env.test-vector --profile foc up -d` |

#### 9d: Run and verify

1. Start the stack:
   ```bash
   docker compose --env-file env.test-vector [--profile PROFILE] up -d
   ```

2. Wait for the workload container to start, then tail its logs looking for the new vector's log tag:
   ```bash
   docker compose logs -f workload 2>&1 | grep -E '\[(NEW_TAG|engine)\]'
   ```
   Replace `NEW_TAG` with the vector's log tag (e.g., `xxx-yyy`).

3. Verify these three things in the logs:
   - **Deck registration**: `[init] action DoXxxYyy: weight=1` appears during startup
   - **Execution**: The vector's log tag appears (e.g., `[xxx-yyy] OK: ...`) showing it ran
   - **No panics/crashes**: The workload container stays running without restarts

4. If the vector is assertion-heavy, also check for assertion output:
   ```bash
   docker compose logs workload 2>&1 | grep -i 'assert\|FAIL\|panic'
   ```

#### 9e: Tear down and clean up

```bash
docker compose --env-file env.test-vector [--profile PROFILE] down
rm env.test-vector
```

Remove the temporary test env file. Do NOT commit it.

#### 9f: Report results

Tell the user:
- Whether the build succeeded
- Whether the vector appeared in the deck (`[init]` log line)
- Whether the vector executed at least once (log tag appeared)
- Any errors, panics, or assertion failures observed
- If everything passed, the vector is ready for commit
