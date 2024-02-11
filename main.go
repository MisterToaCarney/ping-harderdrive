package main

import (
	"encoding/binary"
	"fmt"
	"net"
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

type Request struct {
	To      string
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

func Ping(conn *icmp.PacketConn, address net.Addr, data []byte, seq int, id int) {
	message := icmp.Message{Type: ipv4.ICMPTypeEcho, Code: 0, Body: &icmp.Echo{ID: id, Seq: seq, Data: data}}
	b, err := message.Marshal(nil)
	if err != nil {
		fmt.Println(err)
		return
	}

	conn.WriteTo(b, address)
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

func SendRequests(conn *icmp.PacketConn, c chan Request) {
	for request := range c {
		addr, err := net.ResolveIPAddr("ip4", request.To)
		if err != nil {
			fmt.Println(err)
			continue
		}
		Ping(conn, addr, request.Payload, request.Seq, request.ID)
	}
}

// func ReadFile(filename string) []byte {
// 	dat, err := os.ReadFile(filename)
// 	if err != nil {
// 		panic(err)
// 	}
// 	return dat
// }

func CheckIntroduced(health map[int][]bool, targetLength int) bool {
	if len(health) >= targetLength {
		target := health[len(health)-1]
		introduced := true
		for _, status := range target {
			if status {
				introduced = false
			}
		}
		return introduced
	}
	return false
}

func IntroduceChunk(seq int, outgoingRequests chan Request, healthChan chan map[int][]bool, peers []string) {
	targetChunkCount := len(<-healthChan) + 1
	peerCounter := 0

	for {
		outgoingRequests <- Request{To: peers[peerCounter], Seq: seq, ID: 0, Payload: make([]byte, CHUNK_SIZE)}
		peerCounter++
		deadline := time.Now().Add(500 * time.Millisecond)

	checkLoop:
		for {
			select {
			case health := <-healthChan:
				if time.Since(deadline) > 0 {
					break checkLoop
				}
				introduced := CheckIntroduced(health, targetChunkCount)
				if introduced {
					return
				}

			case <-time.After(100 * time.Millisecond):
				break checkLoop
			}
		}
	}
}

func IntroduceChunks(count int, outgoingRequests chan Request, healthChan chan map[int][]bool, peers []string) {
	for i := 0; i < count; i++ {
		IntroduceChunk(i, outgoingRequests, healthChan, peers)
	}
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
				out[listing.Seq][i] = time.Since(dupTime) > 200*time.Millisecond
			}
		}

		if time.Since(lastPrint) >= 300*time.Millisecond {
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

	fmt.Println("Found peers")

	incomingReplies := make(chan Reply)
	outgoingRequests := make(chan Request)
	statusUpdates := make(chan StatusUpdate, 4096)
	health := make(chan map[int][]bool)
	introduceHealth := make(chan map[int][]bool)

	go GetReplies(conn, incomingReplies)
	go SendRequests(conn, outgoingRequests)
	go Monitor(statusUpdates, health)

	peerCounter := 0

	go IntroduceChunks(70, outgoingRequests, introduceHealth, peers)

	for reply := range incomingReplies {
		currentHealth := <-health
		select {
		case introduceHealth <- currentHealth:
		default:
		}

		if reply.ID >= NUM_REPEATS {
			continue
		}

		outPeer := peers[peerCounter%len(peers)]
		peerCounter++
		outgoingRequests <- Request{To: outPeer, Seq: reply.Seq, ID: reply.ID, Payload: reply.Payload}

		statusUpdates <- StatusUpdate{ReplyAt: time.Now(), Addr: reply.From.String(), Seq: reply.Seq, ID: reply.ID}

		for id, status := range currentHealth[reply.Seq] {
			if status {
				outgoingRequests <- Request{To: reply.From.String(), Seq: reply.Seq, ID: id, Payload: reply.Payload}
			}
		}
	}
}
