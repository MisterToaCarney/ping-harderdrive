package main

import (
	"cmp"
	"crypto/rand"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/time/rate"
)

type PeerStats struct {
	Peer              string
	SentPackets       int
	ReceivedPackets   int
	PacketSuccessRate float32
}

func (stat *PeerStats) IncrementSentPackets() {
	stat.SentPackets++
	stat.CalculateSuccessRate()
}

func (stat *PeerStats) IncrementReceivedPackets() {
	stat.ReceivedPackets++
	stat.CalculateSuccessRate()
}

func (stat *PeerStats) CalculateSuccessRate() float32 {
	stat.PacketSuccessRate = float32(stat.ReceivedPackets) / float32(stat.SentPackets)
	return stat.PacketSuccessRate
}

var networks = []string{
	"0.0.0.0/8",
	"10.0.0.0/8",
	"100.64.0.0/10",
	"127.0.0.0/8",
	"169.254.0.0/16",
	"172.16.0.0/12",
	"192.0.0.0/24",
	"192.0.0.0/29",
	"192.0.0.8/32",
	"192.0.0.9/32",
	"192.0.0.170/32",
	"192.0.0.171/32",
	"192.0.2.0/24",
	"192.31.196.0/24",
	"192.52.193.0/24",
	"192.88.99.0/24",
	"192.168.0.0/16",
	"192.175.48.0/24",
	"198.18.0.0/15",
	"198.51.100.0/24",
	"203.0.113.0/24",
	"240.0.0.0/4",
	"255.255.255.255/32",
	"224.0.0.0/4",
}

var parsedNetworks []netip.Prefix

func parseNetworks() {
	if len(parsedNetworks) != 0 {
		return
	}

	for _, rawNetwork := range networks {
		network, err := netip.ParsePrefix(rawNetwork)
		if err != nil {
			fmt.Println(err)
			continue
		}

		parsedNetworks = append(parsedNetworks, network)
	}
}

func GeneratePublicIP() netip.Addr {
	parseNetworks()
	var addr netip.Addr
	goodAddress := false
	for !goodAddress {
		addrBytes := make([]byte, 4)
		rand.Read(addrBytes)
		addr = netip.AddrFrom4([4]byte(addrBytes))
		goodAddress = true
		for _, network := range parsedNetworks {
			if network.Contains(addr) {
				goodAddress = false
				break
			}
		}
	}

	return addr
}

func NetipAddrToAddr(addr netip.Addr) net.Addr {
	out, _ := net.ResolveIPAddr("ip4", addr.String())
	return out
}

func FindPeers(numHosts int) []string {
	hosts := make([]string, 0, numHosts)

	conn, err := icmp.ListenPacket("ip4:icmp", "0.0.0.0")
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	fmt.Println("Finding Peers")

	replyChan := make(chan Reply, 1000)

	go GetReplies(conn, replyChan)

	payload := make([]byte, 1400)
	rand.Read(payload)

	for len(hosts) < numHosts {
		for i := 0; i < 200; i++ {
			candidate := GeneratePublicIP()
			Ping(conn, NetipAddrToAddr(candidate), payload, 0, 420)
		}
		sentAt := time.Now()

		doneListening := false
		for !doneListening {
			select {
			case reply := <-replyChan:
				receivedAt := time.Now()
				latency := receivedAt.Sub(sentAt)
				if latency > 250*time.Millisecond {
					hosts = append(hosts, reply.From.String())
					fmt.Print(".")
				}

			case <-time.After(500 * time.Millisecond):
				doneListening = true
			}
		}
	}

	fmt.Println()
	return hosts[:numHosts]
}

func MonitorPeers(peers []string, requestChan chan string, replyChan chan Reply, resultsRequest chan bool, resultsChan chan []PeerStats) {
	peerStatsMap := make(map[string]*PeerStats)
	peerStatsSlice := make([]*PeerStats, 0, len(peers))

	for _, peer := range peers {
		peerStats := &PeerStats{Peer: peer, SentPackets: 0, ReceivedPackets: 0, PacketSuccessRate: 0}
		peerStatsMap[peer] = peerStats
		peerStatsSlice = append(peerStatsSlice, peerStats)
	}

	for {
		select {
		case request := <-requestChan:
			if peerStatsMap[request] != nil {
				peerStatsMap[request].IncrementSentPackets()
			}
		case reply := <-replyChan:
			if peerStatsMap[reply.From.String()] != nil {
				peerStatsMap[reply.From.String()].IncrementReceivedPackets()
			}
		case <-resultsRequest:
			slices.SortFunc(peerStatsSlice, func(a, b *PeerStats) int {
				return cmp.Compare(a.PacketSuccessRate, b.PacketSuccessRate)
			})
			out := make([]PeerStats, 0, len(peerStatsSlice))
			for _, peerStats := range peerStatsSlice {
				out = append(out, *peerStats)
			}
			resultsChan <- out
			return
		}
	}
}

func TestPeers(peers []string) []string {
	fmt.Println("Testing Peers")
	packetPeriod := time.Second / 1000
	parsedPeers := make([]net.Addr, 0, len(peers))
	for _, peer := range peers {
		addr, err := net.ResolveIPAddr("ip4", peer)
		if err != nil {
			continue
		}
		parsedPeers = append(parsedPeers, addr)
	}

	conn, err := icmp.ListenPacket("ip4:icmp", "0.0.0.0")
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	replyChan := make(chan Reply, 4096)
	requestChan := make(chan string, 4096)
	resultsRequestChan := make(chan bool)
	peerStatsChan := make(chan []PeerStats)
	go GetReplies(conn, replyChan)
	go MonitorPeers(peers, requestChan, replyChan, resultsRequestChan, peerStatsChan)

	payload := make([]byte, 1450)

	limiter := rate.NewLimiter(rate.Every(packetPeriod), 50)

	for seq := 0; seq < 50; seq++ {
		fmt.Print(".")
		for _, peer := range parsedPeers {
			r := limiter.Reserve()
			time.Sleep(r.Delay())
			Ping(conn, peer, payload, seq, 0)
			requestChan <- peer.String()
		}
	}

	time.Sleep(time.Second)

	resultsRequestChan <- true
	stats := <-peerStatsChan

	fmt.Println()

	goodPeers := make([]string, 0)

	for _, stat := range stats {
		if stat.PacketSuccessRate >= 0.98 {
			goodPeers = append(goodPeers, stat.Peer)
		}
	}

	fmt.Println("Found", len(goodPeers), "good peers out of", len(parsedPeers), "peers.")
	time.Sleep(time.Second)
	return goodPeers
}

func FindReliablePeers(count int) []string {
	peers := FindPeers(count)
	goodPeers := TestPeers(peers)
	return goodPeers
}
