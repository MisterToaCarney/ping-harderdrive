package main

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

const CHUNK_SIZE = 1450
const NUM_REPEATS = 3

type Reply struct {
	From    net.Addr
	Seq     int
	ID      int
	Payload []byte
}

type StatusUpdate struct {
	ReplyAt time.Time
	Addr    string
	Seq     int
	ID      int
}

type StatusListing struct {
	Seq  int
	Dups []time.Time
}

func Ping(conn *icmp.PacketConn, rawAddress string, data []byte, seq int, id int) {
	addr, err := net.ResolveIPAddr("ip4", rawAddress)
	if err != nil {
		fmt.Println(err)
		return
	}

	message := icmp.Message{Type: ipv4.ICMPTypeEcho, Code: 0, Body: &icmp.Echo{ID: id, Seq: seq, Data: data}}
	b, err := message.Marshal(nil)
	if err != nil {
		fmt.Println(err)
		return
	}

	conn.WriteTo(b, addr)
}

func DecodeEchoPacket(data []byte) (uint16, uint16, []byte) {
	idRaw := data[0:2]
	seqRaw := data[2:4]
	payload := data[4:]

	seq := binary.BigEndian.Uint16(seqRaw)
	id := binary.BigEndian.Uint16(idRaw)

	return id, seq, payload
}

func GetReplies(conn *icmp.PacketConn, c chan Reply) {
	for {
		reply := make([]byte, 1500)

		n, peer, err := conn.ReadFrom(reply)
		if err != nil {
			fmt.Println(err)
			return
		}

		recievedMessage, err := icmp.ParseMessage(1, reply[:n])
		if err != nil {
			fmt.Println(err)
			continue
		}

		if recievedMessage.Type == ipv4.ICMPTypeEchoReply {
			encoded, err := recievedMessage.Body.Marshal(1)
			if err != nil {
				fmt.Println(err)
				continue
			}
			id, seq, payload := DecodeEchoPacket(encoded)

			c <- Reply{From: peer, Seq: int(seq), ID: int(id), Payload: payload}

		}
	}
}

func ReadFile(filename string) []byte {
	dat, err := os.ReadFile(filename)
	if err != nil {
		panic(err)
	}
	return dat
}

func InitialTransmit(conn *icmp.PacketConn, data []byte, peers []string) {
	seq := 0
	peerCounter := 0

	for start := 0; start < len(data); start += CHUNK_SIZE {
		end := start + CHUNK_SIZE
		if end > len(data) {
			end = len(data)
		}

		for i := 0; i < NUM_REPEATS; i++ {
			peer := peers[peerCounter%len(peers)]
			peerCounter++
			Ping(conn, peer, data[start:end], seq, i)
		}

		seq++
	}
}

func WriteFile(filename string, data []byte) {
	os.WriteFile(filename, data, 0644)
}

func Monitor(statusUpdates chan StatusUpdate, state chan map[int][]bool) {
	stats := make(map[int]StatusListing)
	lastPrint := time.Now()

	for {
		for len(statusUpdates) > 0 {
			update := <-statusUpdates
			listing := stats[update.Seq]
			listing.Seq = update.Seq
			if listing.Dups == nil {
				listing.Dups = make([]time.Time, NUM_REPEATS)
			}
			listing.Dups[update.ID] = update.ReplyAt
			stats[listing.Seq] = listing
		}

		out := make(map[int][]bool)

		for i := 0; i < len(stats); i++ {
			listing := stats[i]
			out[listing.Seq] = make([]bool, len(listing.Dups))
			for i, dupTime := range listing.Dups {
				out[listing.Seq][i] = time.Since(dupTime) > 400*time.Millisecond
			}
		}

		if time.Since(lastPrint) >= 500*time.Millisecond {
			lastPrint = time.Now()
			fmt.Println()
			for i := 0; i < len(out); i++ {
				fmt.Print(i, "	")
				for _, state := range out[i] {
					if state {
						fmt.Print("failed", "	")
					} else {
						fmt.Print(".", "	")
					}
				}
				fmt.Println()
			}
		}

		state <- out
	}
}

func main() {
	conn, err := icmp.ListenPacket("ip4:icmp", "0.0.0.0")
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	peers := FindPeers(200)

	incomingReplies := make(chan Reply)
	statusUpdates := make(chan StatusUpdate, 4096)
	health := make(chan map[int][]bool)

	go GetReplies(conn, incomingReplies)
	go Monitor(statusUpdates, health)

	InitialTransmit(conn, ReadFile("test.bin"), peers)

	peerCounter := 0

	for reply := range incomingReplies {
		outPeer := peers[peerCounter%len(peers)]
		peerCounter++
		Ping(conn, outPeer, reply.Payload, int(reply.Seq), reply.ID)

		thisUpdate := StatusUpdate{ReplyAt: time.Now(), Addr: reply.From.String(), Seq: reply.Seq, ID: reply.ID}
		statusUpdates <- thisUpdate

		currentHealth := <-health
		for id, status := range currentHealth[reply.Seq] {
			if status {
				Ping(conn, reply.From.String(), reply.Payload, reply.Seq, id)
			}
		}

		// start := reply.Seq * CHUNK_SIZE
		// for i, dat := range reply.Payload {
		// 	buffIdx := start + i
		// 	recvBuffer[buffIdx] = dat
		// }
	}
}
