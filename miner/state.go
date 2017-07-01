package miner

import (
	"errors"
	"github.com/lisgie/bazo_miner/protocol"
	"github.com/lisgie/bazo_miner/storage"
	"golang.org/x/crypto/sha3"
	"log"
)

func isRootKey(hash [32]byte) bool {
	_, exists := storage.RootKeys[hash]
	return exists
}

func fundsStateChange(txSlice []*protocol.FundsTx) error {

	for index, tx := range txSlice {

		var err error
		//check if we have to issue new coins
		for hash, rootAcc := range storage.RootKeys {
			if hash == tx.From {
				log.Printf("Root Key Transaction: %x\n", hash[0:8])

				if rootAcc.Balance+tx.Amount+tx.Fee > protocol.MAX_MONEY {
					log.Printf("Root Account overflows (%v) with given transaction amount (%v) and fee (%v).\n", rootAcc.Balance, tx.Amount, tx.Fee)
					err = errors.New("Sender does not exist in the State.")
				}

				rootAcc.Balance += tx.Amount
				rootAcc.Balance += tx.Fee
			}
		}

		accSender, accReceiver := storage.GetAccountFromHash(tx.From), storage.GetAccountFromHash(tx.To)
		if accSender == nil {
			log.Printf("CRITICAL: Sender does not exist in the State: %x\n", tx.From[0:8])
			err = errors.New("Sender does not exist in the State.")
		}

		if accReceiver == nil {
			log.Printf("CRITICAL: Receiver does not exist in the State: %x\n", tx.To[0:8])
			err = errors.New("Receiver does not exist in the State.")
		}

		//also check for txCnt
		if tx.TxCnt != accSender.TxCnt {
			log.Printf("Sender txCnt does not match: %v (tx.txCnt) vs. %v (state txCnt)\n", tx.TxCnt, accSender.TxCnt)
			err = errors.New("TxCnt mismatch!")
		}

		if (tx.Amount + tx.Fee) > accSender.Balance {
			log.Printf("Sender does not have enough balance: %x\n", accSender.Balance)
			err = errors.New("Sender does not have enough funds for the transaction.")
		}

		//overflow protection
		if tx.Amount+accReceiver.Balance > protocol.MAX_MONEY {
			log.Printf("Transaction amount (%v) would lead to balance overflow at the receiver account (%v)\n", tx.Amount, accReceiver.Balance)
			err = errors.New("Transaction amount would lead to balance overflow at the receiver account\n")
		}

		if err != nil {
			//was it the first tx in the block, no rollback needed
			if index == 0 {
				return err
			}
			fundsStateChangeRollback(txSlice[0 : index-1])
			return err
		}

		//we're manipulating pointer, no need to write back
		accSender.TxCnt += 1
		accSender.Balance -= tx.Amount
		accReceiver.Balance += tx.Amount
	}

	return nil
}

//for normal accounts, it
func accStateChange(txSlice []*protocol.AccTx) error {

	for _, tx := range txSlice {

		switch tx.Header {
		case 1:
			//first bit set, given account will be a new root account
			newAcc := protocol.Account{Address: tx.PubKey}
			storage.RootKeys[sha3.Sum256(tx.PubKey[:])] = &newAcc
			continue
		case 2:
			//second bit set, delete account from root account
			delete(storage.RootKeys, sha3.Sum256(tx.PubKey[:]))
			continue
		}

		//create a regular account
		addressHash := sha3.Sum256(tx.PubKey[:])
		acc := storage.GetAccountFromHash(addressHash)
		if acc != nil {
			log.Printf("CRITICAL: Address already exists in the state: %x\n", addressHash[0:4])
			return errors.New("CRITICAL: Address already exists in the state")
		}
		newAcc := protocol.Account{Address: tx.PubKey}
		storage.State[addressHash] = &newAcc
	}
	return nil
}

//we accept config slices with unknown id, but don't act on the payload
func configStateChange(configTxSlice []*protocol.ConfigTx, blockHash [32]byte) {

	if len(configTxSlice) == 0 {
		return
	}
	var change bool
	for _, tx := range configTxSlice {
		switch tx.Id {
		case protocol.FEE_MINIMUM_ID:
			if parameterBoundsChecking(protocol.FEE_MINIMUM_ID, tx.Payload) {
				FEE_MINIMUM = tx.Payload
				change = true
			}
		case protocol.BLOCK_SIZE_ID:
			if parameterBoundsChecking(protocol.BLOCK_SIZE_ID, tx.Payload) {
				BLOCK_SIZE = tx.Payload
				change = true
			}
		case protocol.DIFF_INTERVAL_ID:
			if parameterBoundsChecking(protocol.DIFF_INTERVAL_ID, tx.Payload) {
				DIFF_INTERVAL = tx.Payload
				change = true
			}
		case protocol.BLOCK_INTERVAL_ID:
			if parameterBoundsChecking(protocol.BLOCK_INTERVAL_ID, tx.Payload) {
				BLOCK_INTERVAL = tx.Payload
				change = true
			}
		case protocol.BLOCK_REWARD_ID:
			if parameterBoundsChecking(protocol.BLOCK_REWARD_ID, tx.Payload) {
				BLOCK_REWARD = tx.Payload
				change = true
			}
		}
	}

	//only add a new parameter struct if something meaningful actually changed
	if change {
		parameterSlice = append(parameterSlice, parameters{
			blockHash,
			FEE_MINIMUM,
			BLOCK_SIZE,
			DIFF_INTERVAL,
			BLOCK_INTERVAL,
			BLOCK_REWARD,
		})
		activeParameters = &parameterSlice[len(parameterSlice)-1]
	}
}

func collectTxFees(fundsTxSlice []*protocol.FundsTx, accTxSlice []*protocol.AccTx, configTxSlice []*protocol.ConfigTx, minerHash [32]byte) error {

	var tmpFundsTx []*protocol.FundsTx
	var tmpAccTx []*protocol.AccTx
	var tmpConfigTx []*protocol.ConfigTx

	minerAcc := storage.GetAccountFromHash(minerHash)

	//subtract fees from sender (check if that is allowed has already been done in the block validation)
	for _, tx := range fundsTxSlice {
		//preventing protocol account from overflowing
		if minerAcc.Balance+tx.Fee > protocol.MAX_MONEY {
			//rollback of all perviously transferred transaction fees to the protocol's account
			collectTxFeesRollback(tmpFundsTx, tmpAccTx, tmpConfigTx, minerHash)
			log.Printf("Miner balance (%v) overflows with transaction fee (%v).\n", minerAcc.Balance, tx.Fee)
			return errors.New("Miner balance overflows with transaction fee.\n")
		}
		minerAcc.Balance += tx.Fee

		senderAcc := storage.GetAccountFromHash(tx.From)
		senderAcc.Balance -= tx.Fee

		tmpFundsTx = append(tmpFundsTx, tx)
	}

	for _, tx := range accTxSlice {
		if minerAcc.Balance+tx.Fee > protocol.MAX_MONEY {
			//rollback of all perviously transferred transaction fees to the protocol's account
			collectTxFeesRollback(tmpFundsTx, tmpAccTx, tmpConfigTx, minerHash)
			log.Printf("Miner balance (%v) overflows with transaction fee (%v).\n", minerAcc.Balance, tx.Fee)
			return errors.New("Miner balance overflows with transaction fee.\n")
		}

		//money gets created from thin air
		//no need to subtract money from root key
		minerAcc.Balance += tx.Fee
		tmpAccTx = append(tmpAccTx, tx)
	}

	for _, tx := range configTxSlice {
		if minerAcc.Balance+tx.Fee > protocol.MAX_MONEY {
			//rollback of all perviously transferred transaction fees to the protocol's account
			collectTxFeesRollback(tmpFundsTx, tmpAccTx, tmpConfigTx, minerHash)
			log.Printf("Miner balance (%v) overflows with transaction fee (%v).\n", minerAcc.Balance, tx.Fee)
			return errors.New("Miner balance overflows with transaction fee.\n")
		}
		minerAcc.Balance += tx.Fee
		tmpConfigTx = append(tmpConfigTx, tx)
	}

	return nil
}

func collectBlockReward(reward uint64, minerHash [32]byte) error {
	miner := storage.GetAccountFromHash(minerHash)

	if miner == nil {
		return errors.New("Miner doesn't exist in the state!")
	}

	if miner.Balance+reward > protocol.MAX_MONEY {
		log.Printf("Miner balance (%v) overflows with block reward (%v).\n", miner.Balance, reward)
		return errors.New("Miner balance overflows with transaction fee.\n")
	}
	miner.Balance += reward
	return nil
}

func printState() {
	log.Println("State updated: ")
	for key, acc := range storage.State {
		log.Printf("%x: %v\n", key[0:10], acc)
	}
}