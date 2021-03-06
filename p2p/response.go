package p2p

import (
	"bytes"
	"encoding/binary"
	"github.com/lisgie/bazo_miner/protocol"
	"github.com/lisgie/bazo_miner/storage"
	"strconv"
	"strings"
)

//This file responds to incoming requests from miners in a synchronous fashion
func txRes(p *peer, payload []byte, txKind uint8) {

	var txHash [32]byte
	copy(txHash[:], payload[0:32])

	var tx protocol.Transaction
	//Check closed and open storage if the tx is available
	openTx := storage.ReadOpenTx(txHash)
	closedTx := storage.ReadClosedTx(txHash)

	if openTx != nil {
		tx = openTx
	} else if closedTx != nil {
		tx = closedTx
	}

	//In case it was not found, send a corresponding message back
	if tx == nil {
		packet := BuildPacket(NOT_FOUND, nil)
		sendData(p, packet)
		return
	}

	var packet []byte
	switch txKind {
	case FUNDSTX_REQ:
		packet = BuildPacket(FUNDSTX_RES, tx.Encode())
	case ACCTX_REQ:
		packet = BuildPacket(ACCTX_RES, tx.Encode())
	case CONFIGTX_REQ:
		packet = BuildPacket(CONFIGTX_RES, tx.Encode())
	}

	sendData(p, packet)
}

//Here as well, checking open and closed block storage
func blockRes(p *peer, payload []byte) {

	var (
		blockHash [32]byte
		block     *protocol.Block
	)

	copy(blockHash[:], payload[0:32])

	block = storage.ReadClosedBlock(blockHash)
	if block == nil {
		block = storage.ReadOpenBlock(blockHash)
	}

	if block == nil {
		packet := BuildPacket(NOT_FOUND, nil)
		sendData(p, packet)
		return
	}

	packet := BuildPacket(BLOCK_RES, block.Encode())
	sendData(p, packet)
}

//Responds to an account request from another miner
func accRes(p *peer, payload []byte) {

	var hash [32]byte
	copy(hash[:], payload[0:32])
	acc := storage.GetAccountFromHash(hash)
	encodedAcc := acc.Encode()

	if encodedAcc == nil {
		packet := BuildPacket(NOT_FOUND, nil)
		sendData(p, packet)
		return
	}
	packet := BuildPacket(ACC_RES, encodedAcc)
	sendData(p, packet)
}

//Completes the handshake with another miner
func pongRes(p *peer, payload []byte) {

	//Payload consists of a 2 bytes array (port number [big endian encoded])
	port := _pongRes(payload)

	if port != "" {
		p.listenerPort = port
	} else {
		p.conn.Close()
		return
	}

	//Restrict amount of connected miners
	if peers.len() >= MAX_MINERS {
		return
	}

	go minerConn(p)
	//Complete handshake
	packet := BuildPacket(MINER_PONG, nil)
	sendData(p, packet)
}

//Decouple the function for testing
func _pongRes(payload []byte) string {
	if len(payload) == PORT_SIZE {
		return strconv.Itoa(int(binary.BigEndian.Uint16(payload[0:PORT_SIZE])))
	} else {
		return ""
	}
}

func neighborRes(p *peer) {
	//only supporting ipv4 addresses for now, makes fixed-size structure easier
	//in the future following structure is possible:
	//1) nr of ipv4 addresses, 2) nr of ipv6 addresses, followed by list of both
	var packet []byte
	var ipportList []string
	peerList := peers.getAllPeers()

	for _, p := range peerList {
		ipportList = append(ipportList, p.getIPPort())
	}

	packet = BuildPacket(NEIGHBOR_RES, _neighborRes(ipportList))
	sendData(p, packet)
}

//Decouple functionality to facilitate testing
func _neighborRes(ipportList []string) (payload []byte) {

	payload = make([]byte, len(ipportList)*6) //6 = size of ipv4 address + port
	index := 0
	for _, ipportIter := range ipportList {
		ipport := strings.Split(ipportIter, ":")
		split := strings.Split(ipport[0], ".")

		//Serializing IP:Port addr tuples
		for ipv4addr := 0; ipv4addr < 4; ipv4addr++ {
			addrPart, err := strconv.Atoi(split[ipv4addr])
			if err != nil {
				return nil
			}
			payload[index] = byte(addrPart)
			index++
		}

		port, _ := strconv.ParseUint(ipport[1], 10, 16)

		//serialize port number
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, port)
		payload[index] = buf.Bytes()[len(buf.Bytes())-2]
		index++
		payload[index] = buf.Bytes()[len(buf.Bytes())-1]
		index++
	}

	return payload
}
