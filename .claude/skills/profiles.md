# /profiles -- Profile Inspector, Comparator, and Scaffolder

## When to trigger
Use this skill when the user asks about profiles, wants to compare profiles, inspect what vectors a profile enables, create a new profile, or validate that a profile covers all STRESS_WEIGHT variables.

## Instructions

You manage the profile system for the Filecoin-Antithesis stress engine. Profiles are `env.*` files at the repo root that override `STRESS_WEIGHT_*` defaults set in `docker-compose.yaml`.

Determine what the user wants and execute the matching workflow below.

---

## Workflow A: Inspect a profile

**Trigger:** "what does profile X do", "show me profile X", "what weights does env.foc use", "what's enabled in consensus"

### Steps:
1. Read the requested profile file (`env.foc`, `env.consensus`, `env.drand`, `env.nightly`, or `env.fip`)
2. Read `docker-compose.yaml` to extract the default `STRESS_WEIGHT_*` values from the workload service environment section
3. Read `workload/cmd/stress-engine/main.go` to see which section each vector belongs to (consensus, stress, or FOC) in `buildDeck()`

### Output:
Present a summary with two parts:

**Topology:**
- NUM_LOTUS_CLIENTS, NUM_LOTUS_MINERS, NUM_FOREST_CLIENTS
- STRESS_NODES (which nodes the workload connects to)
- F3 toggle (LOTUS_F3_ENABLED)
- Compose profile needed (e.g., `--profile foc`)

**Vector weights table:**

| Vector | Env Var | Default | Profile | Category | Status |
|--------|---------|---------|---------|----------|--------|
| DoTipsetConsensus | STRESS_WEIGHT_TIPSET_CONSENSUS | 3 | 5 | consensus | active (boosted) |
| DoTransferMarket | STRESS_WEIGHT_TRANSFER | 2 | 0 | stress | disabled |
| DoFOCLifecycle | STRESS_WEIGHT_FOC_LIFECYCLE | 3 | 6 | foc | active (boosted) |

Mark each vector as: **active** (>0), **active (boosted)** (> default), **active (reduced)** (< default but >0), or **disabled** (=0).

---

## Workflow B: Compare two profiles

**Trigger:** "diff foc vs consensus", "compare env.nightly and env.fip", "what's different between X and Y"

### Steps:
1. Read both profile files
2. Read `docker-compose.yaml` for default values

### Output:
**Topology comparison:**

| Setting | Profile A | Profile B |
|---------|-----------|-----------|
| Lotus clients | 3 | 4 |
| Miners | 3 | 4 |
| Forest clients | 0 | 1 |
| STRESS_NODES | lotus0,lotus1,lotus2 | lotus0,lotus1,lotus2,lotus3,forest0 |

**Weight differences** (only show rows that differ):

| Vector | Env Var | Profile A | Profile B | Default |
|--------|---------|-----------|-----------|---------|
| DoTransferMarket | STRESS_WEIGHT_TRANSFER | 0 | 0 | 2 |
| DoFOCLifecycle | STRESS_WEIGHT_FOC_LIFECYCLE | 6 | 0 | 3 |

**Summary:** One paragraph explaining what each profile is optimized for and the key differences.

---

## Workflow C: Validate profile coverage

**Trigger:** "are all weights covered", "validate profile X", "check for missing env vars", "audit env.foc"

### Steps:
1. Read `workload/cmd/stress-engine/main.go` -- extract every `envVar` string from all `weightedAction` entries in `buildDeck()` (consensus slice, stress slice, and FOC block)
2. Read `docker-compose.yaml` -- extract every `STRESS_WEIGHT_*` variable name from the workload service environment section
3. Read the target profile file -- extract every `STRESS_WEIGHT_*` variable name

### Output:
Report three lists:

**1. In code but missing from profile** (will silently use docker-compose default):
```
STRESS_WEIGHT_XXX_YYY  (default: 2, from docker-compose)
```
Suggest: add explicit `STRESS_WEIGHT_XXX_YYY=N` to the profile.

**2. In code but missing from docker-compose.yaml** (BUG -- env var will never reach the container):
```
STRESS_WEIGHT_XXX_YYY  (registered in buildDeck but not in docker-compose)
```
Suggest: add `- STRESS_WEIGHT_XXX_YYY=${STRESS_WEIGHT_XXX_YYY:-N}` to docker-compose.yaml.

**3. In profile but not in code** (stale -- cleanup candidate):
```
STRESS_WEIGHT_OLD_THING=5  (no matching vector in buildDeck)
```
Suggest: remove from the profile.

If all lists are empty, report "Profile is fully covered -- no gaps found."

---

## Workflow D: Scaffold a new profile

**Trigger:** "create a new profile", "make env.X", "I need a profile for testing Y"

### Steps:
1. Ask the user:
   - **Profile name:** what to call it (e.g., `env.evm-stress`)
   - **Focus area:** what this profile tests
   - **Node topology:** how many lotus clients, miners, forest clients
   - **Which vector categories to enable:** consensus, mempool, EVM, state, reorg, FOC, etc.
   - **F3 enabled?** true/false
2. Read `env.nightly` as template (most complete weight list)
3. Read `docker-compose.yaml` for the full `STRESS_WEIGHT_*` variable list
4. Read `workload/cmd/stress-engine/main.go` to get all registered vectors and their categories

### Output:
Generate the complete `env.*` file following this structure:

```bash
# ============================================
# Profile: NAME -- DESCRIPTION
# ============================================
# EXPLANATION of what this profile validates.

# ----------------------------- SCALE -----------------------------
NUM_LOTUS_CLIENTS=N
NUM_LOTUS_MINERS=N
SECTORS_PER_MINER="..."
NUM_FOREST_CLIENTS=N
STRESS_NODES=lotus0,...

# ----------------------------- F3 TOGGLE --------------------------
LOTUS_F3_ENABLED=true

# ----------------------------- GENERAL -----------------------------
SHARED_CONFIGS="/shared/configs"

# Network Heights
# (copy from env.nightly)

# ----------------------------- LOTUS -----------------------------
# (node config blocks -- copy from env.nightly, adjusted for NUM_LOTUS_CLIENTS)

# ======================== WORKLOAD PROFILE ========================
# FOCUS: description of what weights are tuned for
FUZZER_ENABLED=0
# --- Consensus vectors ---
STRESS_WEIGHT_TIPSET_CONSENSUS=N
# ... all consensus weights ...

# --- Stress vectors ---
STRESS_WEIGHT_TRANSFER=N
# ... all stress weights ...

# --- FOC vectors ---
STRESS_WEIGHT_FOC_LIFECYCLE=0
# ... all FOC weights (usually 0 unless this is an FOC profile) ...
```

**Critical rules:**
- EVERY `STRESS_WEIGHT_*` variable from docker-compose.yaml must appear in the profile
- Unused vectors must be explicitly set to 0 (do NOT omit -- omission falls through to docker-compose defaults which may be non-zero)
- Add a comment after each weight explaining why it is set to that value
- FOC vectors should be 0 unless the profile specifically uses the `foc` compose profile (needs filwizard, curio, yugabyte)
- Consensus vectors should always have non-zero values -- they are the safety net

---

## Workflow E: Show topology

**Trigger:** "what containers run in profile X", "what is the docker topology", "show me the service graph"

### Steps:
1. Read the target profile file for `NUM_LOTUS_CLIENTS`, `NUM_LOTUS_MINERS`, `NUM_FOREST_CLIENTS`
2. Read `docker-compose.yaml` for service definitions, profile gates, and healthcheck dependencies

### Output:
List which containers run and their dependencies:

**Always running (all profiles):**
- drand0, drand1, drand2 -- beacon randomness
- workload -- stress engine + entrypoint

**Lotus nodes (based on NUM_LOTUS_CLIENTS):**
- lotus0, lotus1 -- always (no profile gate)
- lotus2, lotus3 -- require `--profile full` or `--profile foc`

**Miners (based on NUM_LOTUS_MINERS):**
- lotus-miner0, lotus-miner1 -- always
- lotus-miner2, lotus-miner3 -- require `--profile full` or `--profile foc`

**Forest (based on NUM_FOREST_CLIENTS):**
- forest0 -- requires `--profile full`

**FOC-only (require `--profile foc`):**
- filwizard -- contract deployer, SP registration
- yugabyte -- Curio database
- curio -- storage provider (depends on lotus0, yugabyte, filwizard)

---

## Profile reference table

Keep this table in mind when answering any profile question:

| Profile | Focus | Lotus | Miners | Forest | Compose Profile | Key Vectors |
|---------|-------|-------|--------|--------|-----------------|-------------|
| (default) | Mixed stress | 2 | 2 | 0 | (none) | All stress + consensus |
| `env.consensus` | EC/F3 safety | 4 | 4 | 1 | `full` | Consensus only, n-split lifecycle |
| `env.drand` | Beacon faults | 2 | 2 | 1 | `full` | Drand + consensus, minimal stress |
| `env.foc` | Curio PDP | 3 | 3 | 0 | `foc` | FOC lifecycle + steady-state |
| `env.nightly` | Full regression | 2 | 2 | 0 | (none) | Everything (stress + consensus) |
| `env.fip` | FIP upgrades | 2 | 2 | 0 | (none) | State audit heavy, gas/EVM active |
