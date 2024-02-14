package main

import (
	"encoding/binary"
	"fmt"
	"net"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/time/rate"
)

const CHUNK_SIZE = 1455
const NUM_REPEATS = 3
const NUM_CHUNKS = 10
const PACKET_RATE_LIMIT = 2500

type Reply struct {
	From    net.Addr
	Seq     int
	ID      int
	Payload []byte
}

type Request struct {
	Seq     int
	ID      int
	Payload []byte
}

type ReplyUpdate struct {
	ReplyAt time.Time
	Addr    string
	Seq     int
	ID      int
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

func SendRequests(conn *icmp.PacketConn, requests chan Request, peers []string) {
	parsedPeers := make([]*net.IPAddr, 0, len(peers))
	peerCounter := 0

	packetPeriod := time.Second / PACKET_RATE_LIMIT
	limiter := rate.NewLimiter(rate.Every(packetPeriod), 16)

	for _, unparsedPeer := range peers {
		addr, err := net.ResolveIPAddr("ip4", unparsedPeer)
		if err != nil {
			fmt.Println(err)
			continue
		}
		parsedPeers = append(parsedPeers, addr)
	}

	for request := range requests {
		addr := parsedPeers[peerCounter%len(parsedPeers)]
		peerCounter++
		r := limiter.Reserve()
		time.Sleep(r.Delay())
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

func PrintStatus(status [][]time.Duration) {
	fmt.Println()
	for seq, reps := range status {
		fmt.Print(seq, "	")
		points := 0
		for _, duration := range reps {
			if duration > 1000*time.Millisecond {
				fmt.Print("failed", "	")
				points += 2
			} else if duration > 500*time.Millisecond {
				fmt.Print("slow", "	")
				points += 1
			} else {
				fmt.Print(".", "	")
			}
		}
		fmt.Print("|")
		for i := 0; i < points; i++ {
			fmt.Print("X")
		}
		fmt.Println()
	}
}

func Monitor(replyUpdates chan ReplyUpdate, statusChan chan [][]time.Duration) {
	lastReplies := make([][]time.Time, NUM_CHUNKS)
	for i := 0; i < NUM_CHUNKS; i++ {
		lastReplies[i] = make([]time.Time, NUM_REPEATS)
	}

	var lastPrint time.Time

	for {
		for len(replyUpdates) > 0 {
			update := <-replyUpdates
			if update.ID >= NUM_REPEATS || update.Seq >= NUM_CHUNKS {
				continue
			}
			lastReplies[update.Seq][update.ID] = update.ReplyAt
		}

		now := time.Now()

		out := make([][]time.Duration, NUM_CHUNKS)
		for seq, reps := range lastReplies {
			out[seq] = make([]time.Duration, NUM_REPEATS)
			for id, replyAt := range reps {
				out[seq][id] = now.Sub(replyAt)
			}
		}

		if time.Since(lastPrint) > 100*time.Millisecond {
			PrintStatus(out)
			lastPrint = time.Now()
		}

		statusChan <- out
	}
}

func Replenish(statusChan chan [][]time.Duration, requestChan chan Request) {
	var lastReplenish time.Time
	for status := range statusChan {
		if time.Since(lastReplenish) < time.Second {
			continue
		}

		for seq, reps := range status {
			shouldReplenish := true
			for _, duration := range reps {
				if duration < 5000*time.Millisecond {
					shouldReplenish = false
				}
			}
			if shouldReplenish {
				fmt.Println("Replenishing", seq)
				for i := 0; i < NUM_REPEATS; i++ {
					requestChan <- Request{Seq: seq, ID: i, Payload: make([]byte, CHUNK_SIZE)}
				}

			}
		}
		lastReplenish = time.Now()
	}
}

func main() {
	conn, err := icmp.ListenPacket("ip4:icmp", "0.0.0.0")
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	peers := FindReliablePeers(400)

	fmt.Println("Found peers")

	incomingReplies := make(chan Reply)
	outgoingRequests := make(chan Request)
	replyUpdates := make(chan ReplyUpdate, 4096)
	health := make(chan [][]time.Duration)
	replenishHealthChan := make(chan [][]time.Duration)

	go GetReplies(conn, incomingReplies)
	go SendRequests(conn, outgoingRequests, peers)
	go Monitor(replyUpdates, health)
	go Replenish(replenishHealthChan, outgoingRequests)

	currentHealth := <-health
	replenishHealthChan <- currentHealth

	lastRetransmits := make([][]time.Time, NUM_CHUNKS)
	for i := 0; i < NUM_CHUNKS; i++ {
		lastRetransmits[i] = make([]time.Time, NUM_REPEATS)
	}

	for reply := range incomingReplies {
		if reply.ID >= NUM_REPEATS || reply.Seq >= NUM_CHUNKS || len(reply.Payload) != CHUNK_SIZE {
			continue
		}

		currentHealth = <-health
		replenishHealthChan <- currentHealth

		outgoingRequests <- Request{Seq: reply.Seq, ID: reply.ID, Payload: reply.Payload}
		replyUpdates <- ReplyUpdate{ReplyAt: time.Now(), Addr: reply.From.String(), Seq: reply.Seq, ID: reply.ID}

		// Retransmit slow chunks
		for id, duration := range currentHealth[reply.Seq] {
			if duration > 400*time.Millisecond && time.Since(lastRetransmits[reply.Seq][reply.ID]) > 400*time.Millisecond {
				outgoingRequests <- Request{Seq: reply.Seq, ID: id, Payload: reply.Payload}
				lastRetransmits[reply.Seq][reply.ID] = time.Now()
			}
		}
	}
}
