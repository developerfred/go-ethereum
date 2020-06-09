// Copyright 2015 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package core

import (
	"encoding/binary"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

const (
	contractCode = "3373ffffffffffffffffffffffffffffffffffffffff1460245736601f57005b600080fd5b60016000355b81900380602a57602035603957005b604035aa00"
)

// aaTransaction creates a new AA transaction to a deployed instance of the test contract.
// Parameters are the contract address, the transaction gas limit,
// the number of loops to run inside the contract (minimum: 1, gas per loop: 26),
// whether to call PAYGAS at the end, the gas price to be returned from PAYGAS (if called).
func aaTransaction(to common.Address, gaslimit uint64, loops uint64, callPaygas bool, gasPrice *big.Int) *types.Transaction {
	data := make([]byte, 0x60)
	if loops == 0 {
		loops = 1
	}
	binary.BigEndian.PutUint64(data[0x18:0x20], loops)
	if callPaygas {
		data[0x3f] = 1
	}
	gasPriceBytes := gasPrice.Bytes()
	copy(data[0x60-len(gasPriceBytes):], gasPriceBytes)

	return types.NewTransaction(0, to, big.NewInt(0), gaslimit, big.NewInt(0), data).WithAASignature()
}

func setupBlockchain(blockGasLimit uint64) *BlockChain {
	genesis := Genesis{Config: params.AllEthashProtocolChanges, GasLimit: blockGasLimit}
	database := rawdb.NewMemoryDatabase()
	genesis.MustCommit(database)
	blockchain, _ := NewBlockChain(database, nil, genesis.Config, ethash.NewFaker(), vm.Config{}, nil)
	return blockchain
}

func testValidate(blockchain *BlockChain, statedb *state.StateDB, transaction *types.Transaction, validationGasLimit uint64, expectedErr error, t *testing.T) {
	var (
		snapshotRevisionId = statedb.Snapshot()
		context            = NewEVMContext(types.AADummyMessage, blockchain.CurrentHeader(), blockchain, &common.Address{})
		vmenv              = vm.NewEVM(context, statedb, blockchain.Config(), vm.Config{PaygasMode: vm.PaygasHalt})
		err                = Validate(transaction, types.HomesteadSigner{}, vmenv, validationGasLimit)
	)
	if err != expectedErr {
		t.Error("\n\texpected:", expectedErr, "\n\tgot:", err)
	}
	statedb.RevertToSnapshot(snapshotRevisionId)
}

func TestTransactionValidation(t *testing.T) {
	var (
		blockchain = setupBlockchain(10000000)
		statedb, _ = blockchain.State()

		key, _          = crypto.GenerateKey()
		contractCreator = crypto.PubkeyToAddress(key.PublicKey)
		contractAddress = crypto.CreateAddress(contractCreator, 0)
		tx              = &types.Transaction{}
	)
	statedb.SetBalance(contractAddress, big.NewInt(100000))
	statedb.SetCode(contractAddress, common.FromHex(contractCode))

	// test: invalid, no PAYGAS
	tx = aaTransaction(contractAddress, 100000, 1, false, big.NewInt(1))
	testValidate(blockchain, statedb, tx, 400000, ErrNoPaygas, t)

	// test: invalid, insufficient funds
	tx = aaTransaction(contractAddress, 100000, 1, true, big.NewInt(2))
	testValidate(blockchain, statedb, tx, 400000, vm.ErrPaygasInsufficientFunds, t)

	// test: invalid, gasLimit too low for intrinsic gas cost
	tx = aaTransaction(contractAddress, 1, 1, true, big.NewInt(1))
	testValidate(blockchain, statedb, tx, 400000, ErrIntrinsicGas, t)

	// test: invalid, gasLimit too low for loops
	tx = aaTransaction(contractAddress, 100000, 100000, true, big.NewInt(1))
	testValidate(blockchain, statedb, tx, 400000, vm.ErrOutOfGas, t)

	// test: valid, gasLimit < validationGasLimit
	tx = aaTransaction(contractAddress, 100000, 1, true, big.NewInt(1))
	testValidate(blockchain, statedb, tx, 400000, nil, t)

	// test: valid, validationGasLimit < gasLimit
	tx = aaTransaction(contractAddress, 100000, 1, true, big.NewInt(1))
	testValidate(blockchain, statedb, tx, 50000, nil, t)

	// test: invalid, validationGasLimit too low for intrinsic gas cost
	tx = aaTransaction(contractAddress, 100000, 1, true, big.NewInt(1))
	testValidate(blockchain, statedb, tx, 1, ErrIntrinsicGas, t)

	// test: invalid, validationGasLimit too low for loops
	tx = aaTransaction(contractAddress, 100000, 100000, true, big.NewInt(1))
	testValidate(blockchain, statedb, tx, 50000, vm.ErrOutOfGas, t)

	statedb.SetBalance(contractAddress, big.NewInt(0))

	// test: invalid, insufficient funds
	tx = aaTransaction(contractAddress, 100000, 1, true, big.NewInt(1))
	testValidate(blockchain, statedb, tx, 400000, vm.ErrPaygasInsufficientFunds, t)

	// test: valid, gasPrice 0
	tx = aaTransaction(contractAddress, 100000, 1, true, big.NewInt(0))
	testValidate(blockchain, statedb, tx, 400000, nil, t)
}

func TestInvalidEVMConfig(t *testing.T) {
	var (
		blockchain = setupBlockchain(10000000)
		statedb, _ = blockchain.State()

		context = NewEVMContext(types.AADummyMessage, blockchain.CurrentHeader(), blockchain, &common.Address{})
		vmenv   = vm.NewEVM(context, statedb, blockchain.Config(), vm.Config{})

		tx          = &types.Transaction{}
		err         = Validate(tx, types.HomesteadSigner{}, vmenv, 400000)
		expectedErr = ErrIncorrectAAConfig
	)
	if err != expectedErr {
		t.Error("\n\texpected:", expectedErr, "\n\tgot:", err)
	}
}

func TestMalformedTransaction(t *testing.T) {
	var (
		blockchain = setupBlockchain(10000000)
		statedb, _ = blockchain.State()

		context = NewEVMContext(types.AADummyMessage, blockchain.CurrentHeader(), blockchain, &common.Address{})
		vmenv   = vm.NewEVM(context, statedb, blockchain.Config(), vm.Config{PaygasMode: vm.PaygasHalt})

		key, _      = crypto.GenerateKey()
		tx          = pricedTransaction(0, 100000, big.NewInt(0), key)
		err         = Validate(tx, types.HomesteadSigner{}, vmenv, 400000)
		expectedErr = ErrMalformedTransaction
	)
	if err != expectedErr {
		t.Error("\n\texpected:", expectedErr, "\n\tgot:", err)
	}
}
