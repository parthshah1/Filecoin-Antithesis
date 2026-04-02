// profiles.go — Profile-based vector management for the stress engine.
//
// # How it works
//
// Vectors are grouped into categories (consensus, evm, chaos, etc.).
// A profile sets a multiplier for each category. Each vector has a
// base weight (1-3) within its category. The final deck weight is:
//
//	finalWeight = baseWeight × categoryMultiplier
//
// # Override layers (highest priority wins)
//
//  1. STRESS_WEIGHT_<VECTOR>    — override a single vector's weight
//  2. STRESS_CATEGORY_<CAT>     — override a category's multiplier
//  3. STRESS_PROFILE=<name>     — select a profile (default: "default")
//
// # Adding a new vector
//
// Add one line to coreVectors() with the appropriate category.
// The vector inherits its weight from whichever profile is active.
//
// # Adding a new profile
//
// Add an entry to the profiles map below. Select with STRESS_PROFILE=<name>.
//
// # Discovering profiles
//
// Set STRESS_PROFILE=help to print all available profiles and exit.

package main

import (
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// Categories
// ---------------------------------------------------------------------------

// Category groups vectors by what they test.
type Category string

const (
	CatConsensus  Category = "consensus"  // chain agreement, state roots, F3 health
	CatEVM        Category = "evm"        // FVM/EVM contract deploy, invoke, lifecycle
	CatCrossNode  Category = "crossnode"  // multi-node divergence detection
	CatState      Category = "state"      // state tree (HAMT) consistency, compute verification
	CatChaos      Category = "chaos"      // reorg, slashing, adversarial, mempool races
	CatResource   Category = "resource"   // node resource limits (gas, memory, storage)
	CatUpgrade    Category = "upgrade"    // network upgrade / FIP-specific vectors
	CatFOC        Category = "foc"        // Filecoin On-Chain Cloud lifecycle
)

// allCategories defines the display order for logging and help output.
var allCategories = []Category{
	CatConsensus, CatEVM, CatCrossNode, CatState,
	CatChaos, CatResource, CatUpgrade, CatFOC,
}

// ---------------------------------------------------------------------------
// Profile
// ---------------------------------------------------------------------------

// Profile defines a named preset of category multipliers.
type Profile struct {
	Name        string
	Description string
	Multipliers map[Category]int
}

// profiles contains all available named profiles.
// Order doesn't matter — they're keyed by name and sorted for display.
var profiles = map[string]Profile{
	"default": {
		Name:        "default",
		Description: "General-purpose: consensus checks + basic EVM stress",
		Multipliers: map[Category]int{
			CatConsensus: 3, CatEVM: 2, CatCrossNode: 2,
			CatState: 1, CatChaos: 0, CatResource: 0,
		},
	},
	"consensus": {
		Name:        "consensus",
		Description: "Heavy state/F3/cross-node validation",
		Multipliers: map[Category]int{
			CatConsensus: 5, CatEVM: 1, CatCrossNode: 3,
			CatState: 2, CatChaos: 0, CatResource: 0,
		},
	},
	"chaos": {
		Name:        "chaos",
		Description: "Adversarial: reorg, slashing, quorum boundary",
		Multipliers: map[Category]int{
			CatConsensus: 2, CatEVM: 1, CatCrossNode: 1,
			CatState: 0, CatChaos: 3, CatResource: 2,
		},
	},
	"upgrade": {
		Name:        "upgrade",
		Description: "Network upgrade testing: FIP-specific vectors + consensus",
		Multipliers: map[Category]int{
			CatConsensus: 3, CatEVM: 1, CatCrossNode: 2,
			CatState: 1, CatChaos: 0, CatResource: 0, CatUpgrade: 3,
		},
	},
	"nsplit": {
		Name:        "nsplit",
		Description: "N-split attack scenario: heavy reorg + power slashing",
		Multipliers: map[Category]int{
			CatConsensus: 3, CatEVM: 0, CatCrossNode: 1,
			CatState: 0, CatChaos: 5, CatResource: 0,
		},
	},
	"full": {
		Name:        "full",
		Description: "All vectors enabled at equal weight",
		Multipliers: map[Category]int{
			CatConsensus: 2, CatEVM: 2, CatCrossNode: 2,
			CatState: 2, CatChaos: 2, CatResource: 2, CatUpgrade: 2,
		},
	},
}

// ---------------------------------------------------------------------------
// Vector definition
// ---------------------------------------------------------------------------

// vectorDef describes a single test vector with its category and base weight.
type vectorDef struct {
	Name       string   // function name, e.g. "DoStateAudit"
	EnvVar     string   // override env var, e.g. "STRESS_WEIGHT_STATE_AUDIT"
	Fn         func()   // the vector function
	Category   Category // which category this belongs to
	BaseWeight int      // importance within category (1-3)
}

// ---------------------------------------------------------------------------
// Vector registry
// ---------------------------------------------------------------------------

// coreVectors returns all non-FOC vectors grouped by category.
func coreVectors() []vectorDef {
	return []vectorDef{
		// ---- consensus: chain agreement, state roots, F3 health ----
		{"DoTipsetConsensus", "STRESS_WEIGHT_TIPSET_CONSENSUS", DoTipsetConsensus, CatConsensus, 1},
		{"DoHeightProgression", "STRESS_WEIGHT_HEIGHT_PROGRESSION", DoHeightProgression, CatConsensus, 1},
		{"DoPeerCount", "STRESS_WEIGHT_PEER_COUNT", DoPeerCount, CatConsensus, 1},
		{"DoHeadComparison", "STRESS_WEIGHT_HEAD_COMPARISON", DoHeadComparison, CatConsensus, 1},
		{"DoStateRootComparison", "STRESS_WEIGHT_STATE_ROOT", DoStateRootComparison, CatConsensus, 1},
		{"DoStateAudit", "STRESS_WEIGHT_STATE_AUDIT", DoStateAudit, CatConsensus, 2},
		{"DoF3FinalityMonitor", "STRESS_WEIGHT_F3_MONITOR", DoF3FinalityMonitor, CatConsensus, 1},

		// ---- evm: FVM/EVM contract deploy, invoke, lifecycle ----
		{"DoDeployContracts", "STRESS_WEIGHT_DEPLOY", DoDeployContracts, CatEVM, 1},
		{"DoContractCall", "STRESS_WEIGHT_CONTRACT_CALL", DoContractCall, CatEVM, 2},
		{"DoSelfDestructCycle", "STRESS_WEIGHT_SELFDESTRUCT", DoSelfDestructCycle, CatEVM, 1},
		{"DoConflictingContractCalls", "STRESS_WEIGHT_CONTRACT_RACE", DoConflictingContractCalls, CatEVM, 1},

		// ---- crossnode: multi-node divergence detection ----
		{"DoReceiptAudit", "STRESS_WEIGHT_RECEIPT_AUDIT", DoReceiptAudit, CatCrossNode, 1},
		{"DoMessageOrderingAttack", "STRESS_WEIGHT_MSG_ORDERING", DoMessageOrderingAttack, CatCrossNode, 1},
		{"DoNonceBombard", "STRESS_WEIGHT_NONCE_BOMBARD", DoNonceBombard, CatCrossNode, 1},
		{"DoGasExhaustionEdge", "STRESS_WEIGHT_GAS_EXHAUST", DoGasExhaustionEdge, CatCrossNode, 1},

		// ---- state: state tree consistency, compute verification ----
		{"DoActorMigrationStress", "STRESS_WEIGHT_ACTOR_MIGRATION", DoActorMigrationStress, CatState, 1},
		{"DoActorLifecycleStress", "STRESS_WEIGHT_ACTOR_LIFECYCLE", DoActorLifecycleStress, CatState, 1},
		{"DoHeavyCompute", "STRESS_WEIGHT_HEAVY_COMPUTE", DoHeavyCompute, CatState, 1},

		// ---- chaos: reorg, slashing, adversarial, mempool races ----
		{"DoReorgChaos", "STRESS_WEIGHT_REORG", DoReorgChaos, CatChaos, 1},
		{"DoPowerAwareSlash", "STRESS_WEIGHT_POWER_SLASH", DoPowerAwareSlash, CatChaos, 1},
		{"DoQuorumBoundaryTest", "STRESS_WEIGHT_QUORUM_STALL", DoQuorumBoundaryTest, CatChaos, 1},
		{"DoTransferMarket", "STRESS_WEIGHT_TRANSFER", DoTransferMarket, CatChaos, 1},
		{"DoGasWar", "STRESS_WEIGHT_GAS_WAR", DoGasWar, CatChaos, 1},
		{"DoAdversarial", "STRESS_WEIGHT_ADVERSARIAL", DoAdversarial, CatChaos, 1},

		// ---- resource: node resource limits ----
		{"DoMaxBlockGas", "STRESS_WEIGHT_MAX_BLOCK_GAS", DoMaxBlockGas, CatResource, 1},
		{"DoLogBlaster", "STRESS_WEIGHT_LOG_BLASTER", DoLogBlaster, CatResource, 1},
		{"DoMemoryBomb", "STRESS_WEIGHT_MEMORY_BOMB", DoMemoryBomb, CatResource, 1},
		{"DoStorageSpam", "STRESS_WEIGHT_STORAGE_SPAM", DoStorageSpam, CatResource, 1},
	}
}

// focVectors returns FOC-specific vectors (only used when focCfg != nil).
func focVectors() []vectorDef {
	return []vectorDef{
		{"DoFOCLifecycle", "STRESS_WEIGHT_FOC_LIFECYCLE", DoFOCLifecycle, CatFOC, 3},
		{"DoFOCUploadPiece", "STRESS_WEIGHT_FOC_UPLOAD", DoFOCUploadPiece, CatFOC, 2},
		{"DoFOCAddPieces", "STRESS_WEIGHT_FOC_ADD_PIECES", DoFOCAddPieces, CatFOC, 1},
		{"DoFOCMonitorProofSet", "STRESS_WEIGHT_FOC_MONITOR", DoFOCMonitorProofSet, CatFOC, 3},
		{"DoFOCRetrieveAndVerify", "STRESS_WEIGHT_FOC_RETRIEVE", DoFOCRetrieveAndVerify, CatFOC, 1},
		{"DoFOCTransfer", "STRESS_WEIGHT_FOC_TRANSFER", DoFOCTransfer, CatFOC, 1},
		{"DoFOCSettle", "STRESS_WEIGHT_FOC_SETTLE", DoFOCSettle, CatFOC, 1},
		{"DoFOCWithdraw", "STRESS_WEIGHT_FOC_WITHDRAW", DoFOCWithdraw, CatFOC, 1},
		{"DoFOCDeletePiece", "STRESS_WEIGHT_FOC_DELETE_PIECE", DoFOCDeletePiece, CatFOC, 0},
		{"DoFOCDeleteDataSet", "STRESS_WEIGHT_FOC_DELETE_DS", DoFOCDeleteDataSet, CatFOC, 0},
	}
}

// ---------------------------------------------------------------------------
// Profile resolution
// ---------------------------------------------------------------------------

// resolveProfile selects the active profile and computes final category
// multipliers after applying STRESS_CATEGORY_* overrides.
func resolveProfile() (Profile, map[Category]int) {
	profileName := os.Getenv("STRESS_PROFILE")

	// Help / unknown profile: print available profiles and exit
	if profileName == "help" {
		printProfileHelp()
		os.Exit(0)
	}

	// Auto-detect FOC profile when no explicit profile is set
	if profileName == "" && focCfg != nil {
		return Profile{
			Name:        "foc (auto-detected)",
			Description: "FOC mode: consensus + FOC lifecycle, other categories disabled",
			Multipliers: map[Category]int{
				CatConsensus: 3, CatFOC: 1,
			},
		}, applyCategoryOverrides(map[Category]int{CatConsensus: 3, CatFOC: 1})
	}

	// Default profile when nothing is set
	if profileName == "" {
		profileName = "default"
	}

	profile, ok := profiles[profileName]
	if !ok {
		log.Printf("[init] unknown profile %q — available profiles:", profileName)
		printProfileHelp()
		os.Exit(1)
	}

	// Copy multipliers so overrides don't mutate the profile definition
	mults := make(map[Category]int, len(profile.Multipliers))
	for k, v := range profile.Multipliers {
		mults[k] = v
	}

	return profile, applyCategoryOverrides(mults)
}

// applyCategoryOverrides checks for STRESS_CATEGORY_* env vars and applies them.
func applyCategoryOverrides(mults map[Category]int) map[Category]int {
	for _, cat := range allCategories {
		envKey := "STRESS_CATEGORY_" + strings.ToUpper(string(cat))
		if v, ok := envIntIfSet(envKey); ok {
			mults[cat] = v
		}
	}
	return mults
}

// resolveWeight computes the final weight for a vector.
// Individual STRESS_WEIGHT_* overrides take highest priority.
func resolveWeight(v vectorDef, mults map[Category]int) int {
	// Layer 1: individual env var override (highest priority)
	if w, ok := envIntIfSet(v.EnvVar); ok {
		return w
	}
	// Layer 2: baseWeight × category multiplier
	return v.BaseWeight * mults[v.Category]
}

// ---------------------------------------------------------------------------
// Help and logging
// ---------------------------------------------------------------------------

// printProfileHelp prints all available profiles with descriptions.
func printProfileHelp() {
	fmt.Println()
	fmt.Println("Available stress engine profiles:")
	fmt.Println()

	// Sort profile names for stable output
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		p := profiles[name]
		fmt.Printf("  %-12s %s\n", name, p.Description)

		// Print category multipliers
		parts := make([]string, 0, len(allCategories))
		for _, cat := range allCategories {
			if m := p.Multipliers[cat]; m > 0 {
				parts = append(parts, fmt.Sprintf("%s=%d", cat, m))
			}
		}
		if len(parts) > 0 {
			fmt.Printf("  %-12s categories: %s\n", "", strings.Join(parts, ", "))
		}
		fmt.Println()
	}

	fmt.Println("Usage:")
	fmt.Println("  STRESS_PROFILE=<name>              Select a profile")
	fmt.Println("  STRESS_CATEGORY_<CAT>=<N>          Override a category multiplier")
	fmt.Println("  STRESS_WEIGHT_<VECTOR>=<N>         Override a single vector's weight")
	fmt.Println()
}

// logProfileSummary logs the active profile and category multipliers.
func logProfileSummary(profile Profile, mults map[Category]int) {
	log.Println("[init] === Stress Engine Configuration ===")
	log.Printf("[init] profile: %s", profile.Name)
	if profile.Description != "" {
		log.Printf("[init]   %s", profile.Description)
	}
	log.Println("[init] category multipliers:")
	for _, cat := range allCategories {
		m := mults[cat]
		envKey := "STRESS_CATEGORY_" + strings.ToUpper(string(cat))
		suffix := ""
		if _, ok := envIntIfSet(envKey); ok {
			suffix = fmt.Sprintf("  (override: %s=%d)", envKey, m)
		}
		log.Printf("[init]   %-12s %d%s", cat, m, suffix)
	}
	log.Println("[init] enabled vectors:")
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// envIntIfSet returns (value, true) if the env var is set and valid,
// or (0, false) if absent. This distinguishes "not set" from "set to 0".
func envIntIfSet(key string) (int, bool) {
	v := os.Getenv(key)
	if v == "" {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		log.Printf("[config] invalid int for %s=%q, ignoring", key, v)
		return 0, false
	}
	return n, true
}
