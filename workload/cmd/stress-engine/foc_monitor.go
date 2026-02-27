package main

import (
	"log"
	"math/big"

	"github.com/antithesishq/antithesis-sdk-go/assert"
)

// ===========================================================================
// FOC Monitor: Payment Invariant Assertions (Phase 1)
//
// Read-only. Zero transactions. Queries USDFC token state and SP registration
// via eth_call on every invocation. Asserts three invariants:
//
//   P1 (Always) USDFC tracked balances do not exceed total supply
//   P2 (Always) No USDFC balance exceeds total supply (uint256 underflow guard)
//   P3 (Always) Curio storage provider remains registered in SP Registry
//
// P1/P2 are skipped if USDFC_ADDRESS was not present in environment.env.
// Automatically disabled when FOC compose profile is not active (focConfig==nil).
// ===========================================================================

func DoFocMonitor() {
	if focConfig == nil {
		return
	}

	_, node := pickNode()

	// P1 + P2: USDFC token invariants (skipped if USDFC_ADDRESS was not deployed).
	if focConfig.USDFCAddr != nil {
		// Read USDFC total supply.
		totalSupply, err := ethCallUint256(node, focConfig.USDFCAddr, focSigTotalSupply)
		if err != nil {
			log.Printf("[foc-monitor] totalSupply failed: %v", err)
			return
		}

		// Read balance for each tracked address, returning 0 on nil or error.
		readBal := func(addr []byte) *big.Int {
			if addr == nil {
				return big.NewInt(0)
			}
			calldata := append(append([]byte{}, focSigBalanceOf...), encodeAddress(addr)...)
			bal, err := ethCallUint256(node, focConfig.USDFCAddr, calldata)
			if err != nil {
				log.Printf("[foc-monitor] balanceOf %x failed: %v", addr, err)
				return big.NewInt(0)
			}
			return bal
		}

		clientBal   := readBal(focConfig.ClientEthAddr)
		spBal       := readBal(focConfig.SPEthAddr)
		deployerBal := readBal(focConfig.DeployerEthAddr)
		payBal      := readBal(focConfig.FilPayAddr)

		// P1: token conservation — sum of tracked balances must not exceed total supply.
		// A violation means tokens appeared from nowhere (minting bug) or the supply
		// tracker is broken.
		trackedSum := new(big.Int).Add(clientBal, spBal)
		trackedSum.Add(trackedSum, deployerBal)
		trackedSum.Add(trackedSum, payBal)
		supplyConserved := totalSupply.Cmp(trackedSum) >= 0

		log.Printf("[foc-monitor] P1/P2 supply=%s tracked=%s client=%s sp=%s deployer=%s filpay=%s conserved=%v",
			totalSupply, trackedSum, clientBal, spBal, deployerBal, payBal, supplyConserved)

		assert.Always(supplyConserved, "USDFC tracked balances do not exceed total supply", map[string]any{
			"total_supply": totalSupply.String(),
			"tracked_sum":  trackedSum.String(),
			"client_bal":   clientBal.String(),
			"sp_bal":       spBal.String(),
			"deployer_bal": deployerBal.String(),
			"filpay_bal":   payBal.String(),
		})

		// P2: no individual balance exceeds total supply.
		// A uint256 underflow wraps to a huge number; this catches that.
		noUnderflow := clientBal.Cmp(totalSupply) <= 0 &&
			spBal.Cmp(totalSupply) <= 0 &&
			deployerBal.Cmp(totalSupply) <= 0 &&
			payBal.Cmp(totalSupply) <= 0

		assert.Always(noUnderflow, "No USDFC balance exceeds total supply (uint256 underflow guard)", map[string]any{
			"total_supply": totalSupply.String(),
			"client_bal":   clientBal.String(),
			"sp_bal":       spBal.String(),
			"deployer_bal": deployerBal.String(),
			"filpay_bal":   payBal.String(),
		})
	}

	// P3: the Curio SP registered during setup must remain registered.
	// addressToProviderId(spAddr) returns 0 if the address is unknown.
	if focConfig.SPEthAddr != nil && focConfig.RegistryAddr != nil {
		calldata := append(append([]byte{}, focSigAddrToProvId...), encodeAddress(focConfig.SPEthAddr)...)
		provId, err := ethCallUint256(node, focConfig.RegistryAddr, calldata)
		if err != nil {
			log.Printf("[foc-monitor] addressToProviderId failed: %v", err)
		} else {
			spRegistered := provId.Sign() != 0
			log.Printf("[foc-monitor] P3 sp_eth=%x provider_id=%s registered=%v", focConfig.SPEthAddr, provId, spRegistered)

			assert.Always(spRegistered, "Curio storage provider remains registered in SP Registry", map[string]any{
				"provider_id": provId.String(),
				"sp_eth_addr": focConfig.SPEthAddr,
			})
		}
	}

}
