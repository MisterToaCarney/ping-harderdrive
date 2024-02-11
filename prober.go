package main

import (
	"crypto/rand"
	"fmt"
	"net"
	"net/netip"
	"time"

	"golang.org/x/net/icmp"
)

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
				if latency > 350*time.Millisecond {
					hosts = append(hosts, reply.From.String())
					fmt.Print(".")
				}

			case <-time.After(750 * time.Millisecond):
				doneListening = true
			}
		}
	}

	fmt.Println()
	return hosts[:numHosts]

}
