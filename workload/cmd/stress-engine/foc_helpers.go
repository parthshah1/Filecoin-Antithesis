package main

import (
	"encoding/hex"
	"fmt"
	"log"
	"math/big"
	"os"
	"strings"
	"sync"

	secp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"

	"github.com/filecoin-project/go-address"
	filbig "github.com/filecoin-project/go-state-types/big"
	"github.com/filecoin-project/go-state-types/crypto"
	"github.com/filecoin-project/lotus/api"
	"github.com/filecoin-project/lotus/chain/types"
	"github.com/filecoin-project/lotus/chain/types/ethtypes"
	"github.com/filecoin-project/lotus/lib/sigs"
	_ "github.com/filecoin-project/lotus/lib/sigs/delegated" // register SigTypeDelegated signer
)

// FOCConfig holds all addresses and keys parsed from /shared/environment.env.
// A nil value means the FOC profile is not active.
type FOCConfig struct {
	// Contract addresses (20-byte slices)
	USDFCAddr    []byte
	FilPayAddr   []byte
	FWSSAddr     []byte
	PDPAddr      []byte
	RegistryAddr []byte

	// Wallet ETH addresses (20-byte slices)
	DeployerEthAddr []byte
	ClientEthAddr   []byte
	SPEthAddr       []byte // derived from SPKey via secp256k1 + keccak256

	// Private keys (raw 32-byte)
	DeployerKey []byte
	ClientKey   []byte
	SPKey       []byte

	// Runtime state populated by lifecycle vectors
	ActiveRailID      *big.Int
	LastDepositAmount *big.Int
}

// ---------------------------------------------------------------------------
// Function selectors — computed at init time using calcSelector() from contracts.go.
// ---------------------------------------------------------------------------

var (
	// USDFC (ERC20)
	focSigTotalSupply = calcSelector("totalSupply()")
	focSigBalanceOf   = calcSelector("balanceOf(address)")
	focSigTransfer    = calcSelector("transfer(address,uint256)")
	focSigApprove     = calcSelector("approve(address,uint256)")

	// FilecoinPayV1
	focSigAccounts        = calcSelector("accounts(address,address)")
	focSigDeposit         = calcSelector("deposit(address,address,uint256)")
	focSigSetOpApproval   = calcSelector("setOperatorApproval(address,address,bool,uint256,uint256,uint256)")
	focSigSettleRail      = calcSelector("settleRail(uint256,uint256)")
	focSigGetRailsByPayer = calcSelector("getRailsForPayerAndToken(address,address,uint256,uint256)")
	focSigWithdraw        = calcSelector("withdraw(address,uint256)")

	// ServiceProviderRegistry
	focSigAddrToProvId = calcSelector("addressToProviderId(address)")

	// FilecoinWarmStorageService
	focSigTerminateService = calcSelector("terminateService(uint256)")

	// FilecoinPayV1 — rail lifecycle
	focSigCreateRail        = calcSelector("createRail(address,address,address,address,uint256,address)")
	focSigModifyRailPayment = calcSelector("modifyRailPayment(uint256,uint256,uint256)")
)

// ---------------------------------------------------------------------------
// Environment parsing
// ---------------------------------------------------------------------------

// parseFOCEnvironment reads /shared/environment.env (written by filwizard) and
// /shared/curio/private_key (written by Curio init). Returns nil if the
// environment file does not exist, meaning the FOC compose profile is not active.
func parseFOCEnvironment() *FOCConfig {
	data, err := os.ReadFile("/shared/environment.env")
	if err != nil {
		return nil // FOC profile not active
	}

	env := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			env[parts[0]] = parts[1]
		}
	}

	cfg := &FOCConfig{}
	cfg.USDFCAddr    = parseEthAddrHex(env["USDFC_ADDRESS"])
	cfg.FilPayAddr   = parseEthAddrHex(env["FILECOIN_PAY_ADDRESS"])
	cfg.FWSSAddr     = parseEthAddrHex(env["FWSS_PROXY_ADDRESS"])
	cfg.PDPAddr      = parseEthAddrHex(env["PDP_VERIFIER_PROXY_ADDRESS"])
	cfg.RegistryAddr = parseEthAddrHex(env["SERVICE_PROVIDER_REGISTRY_PROXY_ADDRESS"])

	cfg.DeployerKey = parseHexKey(env["DEPLOYER_PRIVATE_KEY"])
	cfg.ClientKey   = parseHexKey(env["CLIENT_PRIVATE_KEY"])

	// Always derive ETH addresses from the private keys so msg.sender in
	// transactions is guaranteed to match the address we use for eth_calls.
	// Reading CLIENT_ETH_ADDRESS / DEPLOYER_ETH_ADDRESS from the env file and
	// using them directly caused deposit reverts when the env-file address didn't
	// exactly match deriveEthAddr(key).
	if cfg.ClientKey != nil {
		cfg.ClientEthAddr = deriveEthAddr(cfg.ClientKey)
	} else {
		cfg.ClientEthAddr = parseEthAddrHex(env["CLIENT_ETH_ADDRESS"])
	}
	if cfg.DeployerKey != nil {
		cfg.DeployerEthAddr = deriveEthAddr(cfg.DeployerKey)
	} else {
		cfg.DeployerEthAddr = parseEthAddrHex(env["DEPLOYER_ETH_ADDRESS"])
	}

	// SP key lives in a separate file written by Curio (raw hex, no 0x prefix).
	if spData, err := os.ReadFile("/shared/curio/private_key"); err == nil {
		spHex := strings.TrimSpace(string(spData))
		cfg.SPKey = parseHexKey(spHex)
		if cfg.SPKey != nil {
			cfg.SPEthAddr = deriveEthAddr(cfg.SPKey)
		}
	}

	if cfg.FilPayAddr == nil {
		log.Printf("[foc] environment.env found but FILECOIN_PAY_ADDRESS missing or invalid")
		return nil
	}
	if cfg.USDFCAddr == nil {
		log.Printf("[foc] WARN: USDFC_ADDRESS missing — token invariant assertions will be skipped")
	}

	log.Printf("[foc] FOC environment loaded: USDFC=%x FilPay=%x Registry=%x SP=%x client=%x deployer=%x",
		cfg.USDFCAddr, cfg.FilPayAddr, cfg.RegistryAddr, cfg.SPEthAddr, cfg.ClientEthAddr, cfg.DeployerEthAddr)
	return cfg
}

// ---------------------------------------------------------------------------
// eth_call helpers
// ---------------------------------------------------------------------------

// ethCallUint256 performs an eth_call and decodes the returned 32-byte uint256.
func ethCallUint256(node api.FullNode, to []byte, calldata []byte) (*big.Int, error) {
	toEth, err := ethtypes.CastEthAddress(to)
	if err != nil {
		return nil, err
	}
	result, err := node.EthCall(ctx, ethtypes.EthCall{
		To:   &toEth,
		Data: ethtypes.EthBytes(calldata),
	}, ethtypes.NewEthBlockNumberOrHashFromPredefined("latest"))
	if err != nil {
		return nil, err
	}
	if len(result) < 32 {
		return big.NewInt(0), nil
	}
	return new(big.Int).SetBytes(result[len(result)-32:]), nil
}

// ethCallBool performs an eth_call and decodes the returned value as bool.
func ethCallBool(node api.FullNode, to []byte, calldata []byte) (bool, error) {
	n, err := ethCallUint256(node, to, calldata)
	if err != nil {
		return false, err
	}
	return n.Sign() != 0, nil
}

// ethCallRaw performs an eth_call and returns the raw byte result.
func ethCallRaw(node api.FullNode, to []byte, calldata []byte) ([]byte, error) {
	toEth, err := ethtypes.CastEthAddress(to)
	if err != nil {
		return nil, err
	}
	result, err := node.EthCall(ctx, ethtypes.EthCall{
		To:   &toEth,
		Data: ethtypes.EthBytes(calldata),
	}, ethtypes.NewEthBlockNumberOrHashFromPredefined("latest"))
	if err != nil {
		return nil, err
	}
	return []byte(result), nil
}

// readAccountFunds reads the `funds` field from FilecoinPay's accounts(token, owner).
// The function returns a 4-tuple; funds is the first uint256.
func readAccountFunds(node api.FullNode, filPayAddr, tokenAddr, ownerAddr []byte) *big.Int {
	calldata := append(append([]byte{}, focSigAccounts...),
		encodeAddress(tokenAddr)...,
	)
	calldata = append(calldata, encodeAddress(ownerAddr)...)
	result, err := ethCallRaw(node, filPayAddr, calldata)
	if err != nil {
		log.Printf("[foc] readAccountFunds failed: %v", err)
		return big.NewInt(0)
	}
	if len(result) < 32 {
		return big.NewInt(0)
	}
	return new(big.Int).SetBytes(result[:32])
}

// ---------------------------------------------------------------------------
// Transaction helpers
// ---------------------------------------------------------------------------

// ethNonces is a local nonce cache for EVM transactions to avoid concurrent
// goroutines fetching the same nonce from the node and colliding in the mpool.
var (
	ethNonces   = map[address.Address]uint64{}
	ethNoncesMu sync.Mutex
)

// sendEthTx signs and submits an EIP-1559 EVM transaction via EthSendRawTransaction.
// Uses SigTypeDelegated — the correct signing type for EVM transactions on Filecoin.
// Returns true if the transaction was accepted by the mempool.
func sendEthTx(node api.FullNode, privKey []byte, toAddr []byte, calldata []byte, tag string) bool {
	if len(privKey) != 32 {
		log.Printf("[%s] invalid private key length %d", tag, len(privKey))
		return false
	}

	senderAddr, err := deriveFilAddr(privKey)
	if err != nil {
		log.Printf("[%s] deriveFilAddr failed: %v", tag, err)
		return false
	}

	// Acquire the next nonce under lock to prevent concurrent goroutines from
	// fetching the same nonce and colliding in the mpool.
	ethNoncesMu.Lock()
	nonce, known := ethNonces[senderAddr]
	if !known {
		n, err := node.MpoolGetNonce(ctx, senderAddr)
		if err != nil {
			ethNoncesMu.Unlock()
			log.Printf("[%s] MpoolGetNonce failed: %v", tag, err)
			return false
		}
		nonce = n
	}
	ethNonces[senderAddr] = nonce + 1
	ethNoncesMu.Unlock()

	toEth, err := ethtypes.CastEthAddress(toAddr)
	if err != nil {
		log.Printf("[%s] CastEthAddress failed: %v", tag, err)
		return false
	}

	tx := ethtypes.Eth1559TxArgs{
		ChainID:              31415926,
		Nonce:                int(nonce),
		To:                   &toEth,
		Value:                filbig.Zero(),
		MaxFeePerGas:         types.NanoFil,
		MaxPriorityFeePerGas: filbig.NewInt(0),
		GasLimit:             3_000_000,
		Input:                calldata,
		V:                    filbig.Zero(),
		R:                    filbig.Zero(),
		S:                    filbig.Zero(),
	}

	preimage, err := tx.ToRlpUnsignedMsg()
	if err != nil {
		log.Printf("[%s] ToRlpUnsignedMsg failed: %v", tag, err)
		return false
	}

	sig, err := sigs.Sign(crypto.SigTypeDelegated, privKey, preimage)
	if err != nil {
		log.Printf("[%s] sigs.Sign failed: %v", tag, err)
		return false
	}

	if err := tx.InitialiseSignature(*sig); err != nil {
		log.Printf("[%s] InitialiseSignature failed: %v", tag, err)
		return false
	}

	signed, err := tx.ToRlpSignedMsg()
	if err != nil {
		log.Printf("[%s] ToRlpSignedMsg failed: %v", tag, err)
		return false
	}

	_, err = node.EthSendRawTransaction(ctx, signed)
	if err != nil {
		log.Printf("[%s] EthSendRawTransaction failed: %v", tag, err)
		// Reset cache so the next call re-syncs from the node.
		ethNoncesMu.Lock()
		delete(ethNonces, senderAddr)
		ethNoncesMu.Unlock()
		return false
	}

	log.Printf("[%s] tx submitted: from=%s nonce=%d to=%x", tag, senderAddr, nonce, toAddr)
	return true
}

// ---------------------------------------------------------------------------
// ABI encoding helpers
// ---------------------------------------------------------------------------

// encodeBigInt ABI-encodes a *big.Int as a 32-byte big-endian uint256.
func encodeBigInt(n *big.Int) []byte {
	buf := make([]byte, 32)
	if n != nil {
		b := n.Bytes()
		if len(b) <= 32 {
			copy(buf[32-len(b):], b)
		}
	}
	return buf
}

// encodeBool ABI-encodes a bool as a 32-byte value (0 or 1).
func encodeBool(b bool) []byte {
	buf := make([]byte, 32)
	if b {
		buf[31] = 1
	}
	return buf
}

// ---------------------------------------------------------------------------
// Address derivation helpers
// ---------------------------------------------------------------------------

// parseEthAddrHex parses a 0x-prefixed hex Ethereum address into 20 bytes.
func parseEthAddrHex(s string) []byte {
	s = strings.TrimPrefix(strings.ToLower(s), "0x")
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 20 {
		return nil
	}
	return b
}

// parseHexKey parses a hex-encoded 32-byte private key (with or without 0x prefix).
func parseHexKey(s string) []byte {
	s = strings.TrimPrefix(strings.ToLower(s), "0x")
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 32 {
		return nil
	}
	return b
}

// deriveEthAddr derives the Ethereum address from a raw 32-byte secp256k1 private key.
func deriveEthAddr(privKey []byte) []byte {
	pk := secp256k1.PrivKeyFromBytes(privKey)
	pub := pk.PubKey().SerializeUncompressed() // 65 bytes: 0x04 + X + Y
	addr, err := ethtypes.EthAddressFromPubKey(pub)
	if err != nil {
		log.Printf("[foc] deriveEthAddr failed: %v", err)
		return nil
	}
	return addr
}

// deriveFilAddr derives the Filecoin f4 (delegated) address from a secp256k1 private key.
func deriveFilAddr(privKey []byte) (address.Address, error) {
	ethAddrBytes := deriveEthAddr(privKey)
	if ethAddrBytes == nil {
		return address.Undef, fmt.Errorf("deriveEthAddr returned nil")
	}
	ea, err := ethtypes.CastEthAddress(ethAddrBytes)
	if err != nil {
		return address.Undef, err
	}
	return ea.ToFilecoinAddress()
}
