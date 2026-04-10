# PoC 005: CBOR Memory Amplification — Test Report

**Date**: April 10, 2026
**Tested by**: Parth Shah
**Status**: 2-hour Antithesis run submitted (ephemeral, amplification-only profile)

---

## What We Tested

A reported vulnerability in Lotus's chain exchange CBOR deserialization: a malicious peer can send a 120 MB ChainExchange response that causes ~960 MB of heap allocation on the receiving node (8x wire-to-heap amplification ratio).

The attack fills message arrays with CBOR null bytes (`0xF6`). Each null costs 1 byte on the wire but occupies an 8-byte nil pointer in the pre-allocated slice. By staying within Lotus's per-field limit (150,000 elements) and compounding across 419 tipsets, the attacker fits the payload under the 120 MiB wire limit while triggering ~960 MB of heap allocation.

The claim is that with 5 concurrent sync workers, this produces ~4.8 GB of heap pressure — enough to OOM-kill a node.

## Bug Class

This is the same class as the chain exchange goroutine report from earlier: **P2P protocol abuse via expensive/oversized messages to a protocol handler**. The common pattern is:

1. Attacker joins the network as a peer
2. Sends valid-looking but adversarial protocol messages
3. Victim's deserialization/processing is more expensive than the wire cost

The standard mitigation for this class is the **libp2p resource manager**, which constrains per-peer memory, streams, and connections.

## Why This May Not Be a Real-World Bug

Several factors limit the practical impact on mainnet:

**1. The libp2p resource manager limits this on mainnet.** The resource manager constrains per-peer memory, streams, and connections. A single attacker peer cannot hold 5 concurrent sync streams and push 120 MB responses through a mainnet-configured node with default resource manager limits. The PoC and the Antithesis devnet do not run with mainnet resource manager configuration.

**2. The PoC is a unit test, not a network attack.** It crafts CBOR payloads and measures `runtime.MemStats` in-process. No actual P2P connection, no resource manager, no peer scoring — none of the real-world defenses are present.

**3. The "eclipse with 4 peers" claim is unrealistic on mainnet.** Bootstrap threshold is 4, but mainnet nodes quickly connect to dozens of peers. Eclipsing a node requires controlling the majority of its peer connections, which is substantially harder than the report suggests.

**4. Peer scoring and validation exist.** Sync peers that deliver invalid responses get penalized in the peer tracker. `processResponse` rejecting the payload means the peer gets scored negatively, and subsequent requests are routed to other peers. The attacker's window is limited to the first few bad responses before they're deprioritized.

**5. The 8x amplification is real but bounded.** 120 MB to 962 MB is notable, but on a mainnet node with 64+ GB RAM (typical for storage providers), a transient ~5 GB spike during sync from a bad peer is annoying but not a crash. The GC reclaims it after the response is rejected.

**6. This is fundamentally the same class as the previous chain exchange report.** Sending expensive/oversized P2P messages to a protocol handler. The resource manager is the standard mitigation.

## What We Did Anyway

We integrated the PoC into our protocol-fuzzer and ran it against a live Antithesis devnet to see if the bug produces observable impact under test conditions (where the resource manager is not configured to mainnet defaults).

### How we tested it

Full details on the integration process, including code changes, build steps, and run commands, are in [`docs/guide-poc-to-fuzzer.md`](guide-poc-to-fuzzer.md).

Summary of what was done:

| Step | Action |
|------|--------|
| **PoC validation** | Ran the standalone PoC locally — confirmed 8.03x amplification ratio |
| **Attack code** | Created `amplification_attack.go` in the protocol-fuzzer with 4 variants (10/100/419 tipsets + alloc-before-read) |
| **Isolated profile** | Created `env.amplification` — disables stress-engine and all other fuzzer vectors, runs only this attack |
| **Build** | Built workload and config Docker images locally |
| **Push** | Pushed to private GAR registry (`workload:amplification-v1`, `config:amplification-v1`) — no code pushed to GitHub |
| **Run** | Submitted a 2-hour ephemeral Antithesis run via `snouty run --param custom.setup=amplification` |

### What we expect to see

| Outcome | What it tells us |
|---------|-----------------|
| Lotus nodes OOM-killed | The amplification is severe enough to crash nodes even without mainnet resource limits |
| Nodes degrade but recover | GC handles the pressure, but there's measurable impact on sync latency |
| No observable impact | The devnet nodes absorb the memory spike without issue |
| Fuzzer timeouts | The nodes don't initiate sync from the malicious peer (Hello weight claims may be ignored) |

### Impact on nightly runs

None. The images were pushed with custom tags (`amplification-v1`). Nightly runs use `latest` tags and are completely unaffected.

## Recommendation

**Monitor the Antithesis run results.** If the devnet nodes crash or show significant degradation, it may be worth verifying whether the mainnet resource manager configuration adequately bounds the per-peer allocation during ChainExchange deserialization. If the nodes absorb it without issue, this confirms the existing defenses are sufficient and the report can be closed as informational.

Regardless of the Antithesis outcome, the amplification vector is now permanently available in the fuzzer (gated behind `FUZZER_WEIGHT_CHAINEXCHANGE_AMPLIFICATION`, default 0) for regression testing if Lotus's CBOR deserialization or resource manager configuration changes in the future.
