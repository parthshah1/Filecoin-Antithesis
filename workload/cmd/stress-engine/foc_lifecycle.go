package main

import (
	"log"
	"math/big"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
	"github.com/filecoin-project/lotus/api"
)

// ===========================================================================
// FOC Lifecycle Vectors (Phase 2)
//
// Active EVM transactions exercising the FOC user flow end-to-end.
// Each vector requires focConfig.ClientKey to be set.
//
//   L1 (Sometimes) USDFC transfer between wallets submitted
//   L2 (Always)    USDFC deposit increases FilecoinPay account balance
//   L3 (Sometimes) FWSS operator approval set on FilecoinPay
//   L4 (Sometimes) FilecoinPay rail settlement submitted
//   L5 (Sometimes) FilecoinPay withdrawal submitted
//
// All transactions use sendEthTx() → EIP-1559 + SigTypeDelegated signing.
// ===========================================================================

const (
	focTxWait     = 15 * time.Second // time to wait for tx inclusion
	focUSDFCUnit  = 1e18             // 1 USDFC in wei
)

// DoFocTransferUSDFC transfers a small amount of USDFC from the client wallet
// to the deployer wallet, exercising ERC-20 transfer under fault injection.
func DoFocTransferUSDFC() {
	if focConfig == nil || focConfig.ClientKey == nil || focConfig.USDFCAddr == nil {
		return
	}

	_, node := pickNode()

	// Transfer 1–5 USDFC (in wei)
	amount := new(big.Int).Mul(
		big.NewInt(int64(rngIntn(5)+1)),
		big.NewInt(focUSDFCUnit),
	)

	calldata := append(append([]byte{}, focSigTransfer...),
		encodeAddress(focConfig.DeployerEthAddr)...,
	)
	calldata = append(calldata, encodeBigInt(amount)...)

	ok := sendEthTx(node, focConfig.ClientKey, focConfig.USDFCAddr, calldata, "foc-transfer")

	log.Printf("[foc-transfer] amount=%s ok=%v", amount, ok)

	assert.Sometimes(ok, "USDFC transfer between wallets submitted", map[string]any{
		"amount":   amount.String(),
		"from":     focConfig.ClientEthAddr,
		"to":       focConfig.DeployerEthAddr,
	})
}

// DoFocDeposit approves FilecoinPay as a USDFC spender and deposits funds into
// the client's FilecoinPay account. Asserts the deposit increases the balance
// by exactly the deposited amount.
func DoFocDeposit() {
	if focConfig == nil || focConfig.ClientKey == nil ||
		focConfig.USDFCAddr == nil || focConfig.FilPayAddr == nil {
		return
	}

	_, node := pickNode()

	// Deposit 1–10 USDFC
	amount := new(big.Int).Mul(
		big.NewInt(int64(rngIntn(10)+1)),
		big.NewInt(focUSDFCUnit),
	)

	// Read balance before.
	fundsBefore := readAccountFunds(node, focConfig.FilPayAddr, focConfig.USDFCAddr, focConfig.ClientEthAddr)
	log.Printf("[foc-deposit] amount=%s funds_before=%s", amount, fundsBefore)

	// Step 1: approve(FilecoinPay, amount) on USDFC
	approveData := append(append([]byte{}, focSigApprove...),
		encodeAddress(focConfig.FilPayAddr)...,
	)
	approveData = append(approveData, encodeBigInt(amount)...)
	if !sendEthTx(node, focConfig.ClientKey, focConfig.USDFCAddr, approveData, "foc-approve") {
		return
	}

	// Step 2: deposit(usdfc, clientEthAddr, amount) on FilecoinPay
	depositData := append(append([]byte{}, focSigDeposit...),
		encodeAddress(focConfig.USDFCAddr)...,
	)
	depositData = append(depositData, encodeAddress(focConfig.ClientEthAddr)...)
	depositData = append(depositData, encodeBigInt(amount)...)
	if !sendEthTx(node, focConfig.ClientKey, focConfig.FilPayAddr, depositData, "foc-deposit") {
		return
	}

	time.Sleep(focTxWait)

	fundsAfter := readAccountFunds(node, focConfig.FilPayAddr, focConfig.USDFCAddr, focConfig.ClientEthAddr)
	increased := new(big.Int).Sub(fundsAfter, fundsBefore)
	deposited := increased.Cmp(amount) == 0

	log.Printf("[foc-deposit] funds_after=%s increased=%s expected=%s deposited=%v", fundsAfter, increased, amount, deposited)

	assert.Always(deposited, "USDFC deposit increases FilecoinPay account balance", map[string]any{
		"amount":       amount.String(),
		"funds_before": fundsBefore.String(),
		"funds_after":  fundsAfter.String(),
		"increased_by": increased.String(),
	})

	// Cache for adversarial vectors.
	focConfig.LastDepositAmount = amount
}

// DoFocApproveOperator grants the FWSS contract operator rights on the client's
// FilecoinPay account, allowing FWSS to create and manage payment rails.
func DoFocApproveOperator() {
	if focConfig == nil || focConfig.ClientKey == nil ||
		focConfig.FilPayAddr == nil || focConfig.USDFCAddr == nil || focConfig.FWSSAddr == nil {
		return
	}

	_, node := pickNode()

	// setOperatorApproval(token, operator, approved, rateAllowance, lockupAllowance, maxLockupPeriod)
	//   rateAllowance  = 1,000 USDFC / epoch
	//   lockupAllowance = 10,000 USDFC total
	//   maxLockupPeriod = 2,880 epochs (~1 day on Filecoin)
	rateAllowance   := new(big.Int).Mul(big.NewInt(1_000), big.NewInt(focUSDFCUnit))
	lockupAllowance := new(big.Int).Mul(big.NewInt(10_000), big.NewInt(focUSDFCUnit))
	maxLockupPeriod := big.NewInt(2_880)

	calldata := append(append([]byte{}, focSigSetOpApproval...),
		encodeAddress(focConfig.USDFCAddr)...,
	)
	calldata = append(calldata, encodeAddress(focConfig.FWSSAddr)...)
	calldata = append(calldata, encodeBool(true)...)
	calldata = append(calldata, encodeBigInt(rateAllowance)...)
	calldata = append(calldata, encodeBigInt(lockupAllowance)...)
	calldata = append(calldata, encodeBigInt(maxLockupPeriod)...)

	ok := sendEthTx(node, focConfig.ClientKey, focConfig.FilPayAddr, calldata, "foc-approve-operator")

	log.Printf("[foc-approve-operator] rate=%s lockup=%s maxPeriod=%s ok=%v", rateAllowance, lockupAllowance, maxLockupPeriod, ok)

	assert.Sometimes(ok, "FWSS operator approval set on FilecoinPay", map[string]any{
		"fwss_addr":       focConfig.FWSSAddr,
		"rate_allowance":  rateAllowance.String(),
		"lockup_allowance": lockupAllowance.String(),
	})
}

// DoFocDiscoverAndSettleRail discovers payment rails for the client and settles
// the first one found up to the current chain epoch.
func DoFocDiscoverAndSettleRail() {
	if focConfig == nil || focConfig.ClientKey == nil ||
		focConfig.FilPayAddr == nil || focConfig.USDFCAddr == nil {
		return
	}

	_, node := pickNode()

	// Discover rails: getRailsForPayerAndToken(payer, token, offset=0, limit=1)
	calldata := append(append([]byte{}, focSigGetRailsByPayer...),
		encodeAddress(focConfig.ClientEthAddr)...,
	)
	calldata = append(calldata, encodeAddress(focConfig.USDFCAddr)...)
	calldata = append(calldata, encodeUint256(0)...) // offset
	calldata = append(calldata, encodeUint256(1)...) // limit

	raw, err := ethCallRaw(node, focConfig.FilPayAddr, calldata)
	if err != nil {
		log.Printf("[foc-settle] getRailsForPayerAndToken failed: %v", err)
		return
	}

	// ABI return: (tuple[] results, uint256 nextOffset, uint256 total)
	// Layout: [offset_ptr:32][nextOffset:32][total:32][array_len:32][rail_struct...]
	// We only need total (word at byte 64) and the first railId (word at byte 128).
	if len(raw) < 96 {
		log.Printf("[foc-settle] no rails found (response too short)")
		return
	}
	total := new(big.Int).SetBytes(raw[64:96])
	if total.Sign() == 0 {
		log.Printf("[foc-settle] no rails found for client")
		return
	}

	// The first rail struct starts at byte 128; its first field is railId (uint256).
	if len(raw) < 160 {
		log.Printf("[foc-settle] rail data truncated")
		return
	}
	railID := new(big.Int).SetBytes(raw[128:160])
	focConfig.ActiveRailID = railID
	log.Printf("[foc-settle] found rail_id=%s total_rails=%s", railID, total)

	// settleRail(railId, currentEpoch)
	head, err := node.ChainHead(ctx)
	if err != nil {
		log.Printf("[foc-settle] ChainHead failed: %v", err)
		return
	}
	epoch := big.NewInt(int64(head.Height()))

	settleData := append(append([]byte{}, focSigSettleRail...),
		encodeBigInt(railID)...,
	)
	settleData = append(settleData, encodeBigInt(epoch)...)

	ok := sendEthTx(node, focConfig.ClientKey, focConfig.FilPayAddr, settleData, "foc-settle")

	log.Printf("[foc-settle] rail_id=%s epoch=%s ok=%v", railID, epoch, ok)

	assert.Sometimes(ok, "FilecoinPay rail settlement submitted", map[string]any{
		"rail_id": railID.String(),
		"epoch":   epoch.String(),
	})
}

// DoFocWithdraw withdraws a portion of the client's available FilecoinPay funds
// back to their wallet.
func DoFocWithdraw() {
	if focConfig == nil || focConfig.ClientKey == nil ||
		focConfig.FilPayAddr == nil || focConfig.USDFCAddr == nil {
		return
	}

	_, node := pickNode()

	funds := readAccountFunds(node, focConfig.FilPayAddr, focConfig.USDFCAddr, focConfig.ClientEthAddr)
	log.Printf("[foc-withdraw] available_funds=%s", funds)
	if funds.Sign() == 0 {
		log.Printf("[foc-withdraw] no funds available to withdraw")
		return
	}

	// Withdraw min(funds, 1 USDFC)
	maxWithdraw := big.NewInt(focUSDFCUnit)
	withdrawAmt := new(big.Int).Set(funds)
	if withdrawAmt.Cmp(maxWithdraw) > 0 {
		withdrawAmt.Set(maxWithdraw)
	}

	calldata := append(append([]byte{}, focSigWithdraw...),
		encodeAddress(focConfig.USDFCAddr)...,
	)
	calldata = append(calldata, encodeBigInt(withdrawAmt)...)

	ok := sendEthTx(node, focConfig.ClientKey, focConfig.FilPayAddr, calldata, "foc-withdraw")

	log.Printf("[foc-withdraw] withdraw_amt=%s ok=%v", withdrawAmt, ok)

	assert.Sometimes(ok, "FilecoinPay withdrawal submitted", map[string]any{
		"withdraw_amt":    withdrawAmt.String(),
		"available_funds": funds.String(),
	})
}

// DoFocCreateRail creates a direct FilecoinPay payment rail from the client wallet
// to the deployer wallet. No PDP or FWSS involvement — pure FilecoinPay. After the
// tx is included, it discovers the rail and caches the rail ID for future settle calls.
func DoFocCreateRail() {
	if focConfig == nil || focConfig.ClientKey == nil ||
		focConfig.USDFCAddr == nil || focConfig.FilPayAddr == nil ||
		focConfig.DeployerEthAddr == nil || focConfig.ClientEthAddr == nil {
		return
	}

	_, node := pickNode()

	// createRail(token, from, to, validator=0, commissionRateBps=0, serviceFeeRecipient=0)
	calldata := append(append([]byte{}, focSigCreateRail...),
		encodeAddress(focConfig.USDFCAddr)...,
	)
	calldata = append(calldata, encodeAddress(focConfig.ClientEthAddr)...)
	calldata = append(calldata, encodeAddress(focConfig.DeployerEthAddr)...)
	calldata = append(calldata, encodeAddress(nil)...)   // validator = address(0)
	calldata = append(calldata, encodeUint256(0)...)     // commissionRateBps = 0
	calldata = append(calldata, encodeAddress(nil)...)   // serviceFeeRecipient = address(0)

	ok := sendEthTx(node, focConfig.ClientKey, focConfig.FilPayAddr, calldata, "foc-create-rail")
	log.Printf("[foc-create-rail] from=%x to=%x ok=%v", focConfig.ClientEthAddr, focConfig.DeployerEthAddr, ok)

	assert.Sometimes(ok, "FilecoinPay rail created from client to deployer", map[string]any{
		"from": focConfig.ClientEthAddr,
		"to":   focConfig.DeployerEthAddr,
	})

	if !ok {
		return
	}

	// Wait for inclusion then discover and cache the rail ID.
	time.Sleep(focTxWait)
	discoverActiveRail(node)
}

// discoverActiveRail queries getRailsForPayerAndToken for the client and caches
// the first result in focConfig.ActiveRailID for downstream settle/modify vectors.
func discoverActiveRail(node api.FullNode) {
	calldata := append(append([]byte{}, focSigGetRailsByPayer...),
		encodeAddress(focConfig.ClientEthAddr)...,
	)
	calldata = append(calldata, encodeAddress(focConfig.USDFCAddr)...)
	calldata = append(calldata, encodeUint256(0)...) // offset
	calldata = append(calldata, encodeUint256(1)...) // limit

	raw, err := ethCallRaw(node, focConfig.FilPayAddr, calldata)
	if err != nil {
		log.Printf("[foc-create-rail] getRailsForPayerAndToken failed: %v", err)
		return
	}
	if len(raw) < 96 {
		return
	}
	total := new(big.Int).SetBytes(raw[64:96])
	if total.Sign() == 0 || len(raw) < 160 {
		log.Printf("[foc-create-rail] no rails found after creation")
		return
	}
	railID := new(big.Int).SetBytes(raw[128:160])
	focConfig.ActiveRailID = railID
	log.Printf("[foc-create-rail] cached active rail_id=%s (total=%s)", railID, total)
}

// DoFocModifyRailPayment sets a small payment rate on the active rail so that
// subsequent settle calls actually transfer tokens. Only runs if a rail exists.
func DoFocModifyRailPayment() {
	if focConfig == nil || focConfig.ClientKey == nil ||
		focConfig.ActiveRailID == nil || focConfig.FilPayAddr == nil {
		return
	}

	_, node := pickNode()

	// Set rate = 1 USDFC/epoch, oneTimePayment = 0
	rate := big.NewInt(focUSDFCUnit)

	calldata := append(append([]byte{}, focSigModifyRailPayment...),
		encodeBigInt(focConfig.ActiveRailID)...,
	)
	calldata = append(calldata, encodeBigInt(rate)...)
	calldata = append(calldata, encodeBigInt(big.NewInt(0))...) // oneTimePayment = 0

	ok := sendEthTx(node, focConfig.ClientKey, focConfig.FilPayAddr, calldata, "foc-modify-rail")
	log.Printf("[foc-modify-rail] rail_id=%s rate=%s ok=%v", focConfig.ActiveRailID, rate, ok)

	assert.Sometimes(ok, "FilecoinPay rail payment rate set", map[string]any{
		"rail_id": focConfig.ActiveRailID.String(),
		"rate":    rate.String(),
	})
}
