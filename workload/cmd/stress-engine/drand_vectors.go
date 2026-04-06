package main

import (
	"encoding/hex"
	"fmt"
	"log"
	"strings"

	"github.com/antithesishq/antithesis-sdk-go/assert"

	"github.com/filecoin-project/go-state-types/abi"
)

// ===========================================================================
// DoDrandBeaconAudit — Cross-node drand beacon entry consistency
//
// Picks a random finalized height, collects BeaconEntries from every node's
// block headers at that height, and asserts they are identical. Beacon
// entries are deterministic (drand round -> BLS signature) so any mismatch
// in finalized blocks indicates a consensus or drand integration bug.
//
// Covers issue #229 scenario 7 (beacon entry audit) and validates
// lotus#11500 concern 1 (correctness of drand usage across implementations).
// ===========================================================================

// beaconFingerprint builds a canonical fingerprint of all beacon entries
// in a block. Multiple entries occur when null rounds are backfilled.
func beaconFingerprint(entries []beaconEntryData) string {
	if len(entries) == 0 {
		return "empty"
	}
	parts := make([]string, len(entries))
	for i, e := range entries {
		parts[i] = fmt.Sprintf("%d:%s", e.round, e.sig)
	}
	return strings.Join(parts, "|")
}

type beaconEntryData struct {
	round uint64
	sig   string
}

func DoDrandBeaconAudit() {
	if len(nodeKeys) < 2 {
		return
	}
	if !allNodesPastEpoch(f3MinEpoch) {
		return
	}

	snap := getFinalizedSnapshots()
	finalizedHeight, _ := snapshotMinHeight(snap)
	if finalizedHeight < finalizedMinHeight {
		return
	}

	// Pick a random finalized height to audit
	checkHeight := abi.ChainEpoch(rngIntn(int(finalizedHeight)) + 1)

	// Collect beacon fingerprints from each node at this height.
	type beaconResult struct {
		fingerprint string
		actualHeight abi.ChainEpoch // actual tipset height (may differ from checkHeight on null rounds)
		entryCount  int
		lastRound   uint64
		lastSig     string // first 16 hex chars for logging
	}

	results := make(map[string]beaconResult) // nodeName -> result
	var errs int

	for name, s := range snap {
		if s.err != nil {
			errs++
			continue
		}

		ts, err := nodes[name].ChainGetTipSetByHeight(ctx, checkHeight, s.key)
		if err != nil {
			log.Printf("[drand-audit] ChainGetTipSetByHeight(%d) failed on %s: %v", checkHeight, name, err)
			errs++
			continue
		}

		blks := ts.Blocks()
		if len(blks) == 0 {
			continue
		}

		// All blocks in a tipset share the same beacon entries; use the first block.
		rawEntries := blks[0].BeaconEntries

		entries := make([]beaconEntryData, len(rawEntries))
		for i, be := range rawEntries {
			entries[i] = beaconEntryData{
				round: be.Round,
				sig:   hex.EncodeToString(be.Data),
			}
		}

		fp := beaconFingerprint(entries)

		var lastRound uint64
		var lastSig string
		if len(entries) > 0 {
			last := entries[len(entries)-1]
			lastRound = last.round
			lastSig = last.sig
			if len(lastSig) > 16 {
				lastSig = lastSig[:16]
			}
		}

		results[name] = beaconResult{
			fingerprint:  fp,
			actualHeight: ts.Height(),
			entryCount:   len(entries),
			lastRound:    lastRound,
			lastSig:      lastSig,
		}
	}

	responded := len(results)
	if responded < 2 {
		return
	}

	// Check that all nodes returned the same actual height — if not, null-round
	// resolution diverged (different finalized anchors), skip this check.
	heights := make(map[abi.ChainEpoch]int)
	for _, r := range results {
		heights[r.actualHeight]++
	}
	if len(heights) > 1 {
		debugLog("[drand-audit] height=%d returned different actual heights across nodes (null-round divergence), skipping",
			checkHeight)
		return
	}

	// Group by fingerprint
	groups := make(map[string][]string) // fingerprint -> []nodeName
	for name, r := range results {
		groups[r.fingerprint] = append(groups[r.fingerprint], name)
	}

	allMatch := len(groups) == 1

	// Pick a sample for logging
	var sampleRound uint64
	var sampleSig string
	var sampleHeight abi.ChainEpoch
	for _, r := range results {
		sampleRound = r.lastRound
		sampleSig = r.lastSig
		sampleHeight = r.actualHeight
		break
	}

	assert.Always(allMatch, "Drand beacon entries match across all nodes at finalized height", map[string]any{
		"height":         checkHeight,
		"actual_height":  sampleHeight,
		"finalized_at":   finalizedHeight,
		"beacon_round":   sampleRound,
		"beacon_sig":     sampleSig,
		"unique_beacons": len(groups),
		"nodes_checked":  responded,
		"errors":         errs,
	})

	if !allMatch {
		log.Printf("[drand-audit] MISMATCH at height %d (actual %d): %d unique beacon entries across %d nodes",
			checkHeight, sampleHeight, len(groups), responded)
		for fp, names := range groups {
			truncFp := fp
			if len(truncFp) > 60 {
				truncFp = truncFp[:60] + "..."
			}
			log.Printf("[drand-audit]   fingerprint=%s nodes=%v", truncFp, names)
		}
	} else {
		debugLog("[drand-audit] height=%d actual=%d beacon_round=%d sig=%s nodes=%d OK",
			checkHeight, sampleHeight, sampleRound, sampleSig, responded)
	}
}
