package main

import (
	"cmp"
	"fmt"
	"net"
	"os"
	"slices"

	"github.com/pojntfx/go-nbd/pkg/backend"
	"github.com/pojntfx/go-nbd/pkg/client"
	"github.com/pojntfx/go-nbd/pkg/server"
)

type WriteRequest struct {
	Seq         int
	ExtentStart int
	ExtentEnd   int
	Payload     []byte
}

type PingBackend struct {
	ReadChannel  chan Reply
	WriteChannel chan WriteRequest
	SyncChannel  chan bool
}

type DataChunk struct {
	Seq  int64
	Data []byte
}

func NewPingBackend(readChan chan Reply, writeChan chan WriteRequest, syncChan chan bool) *PingBackend {
	return &PingBackend{ReadChannel: readChan, WriteChannel: writeChan, SyncChannel: syncChan}
}

func GetChunkExtents(startOffset, endOffset int64) map[int64][2]int64 {
	requestedLength := endOffset - startOffset

	startChunk := startOffset / CHUNK_SIZE
	endChunk := endOffset / CHUNK_SIZE

	chunkExtents := make(map[int64][2]int64)

	for c := startChunk; c <= endChunk; c++ {
		var begin, end int64
		if c == startChunk {
			begin = startOffset % CHUNK_SIZE
			end = min(CHUNK_SIZE, begin+requestedLength)
		} else if c == endChunk {
			end = endOffset % CHUNK_SIZE
			begin = max(0, end-requestedLength)
		} else {
			begin = int64(0)
			end = int64(CHUNK_SIZE)
		}

		if begin != end {
			chunkExtents[c] = [2]int64{begin, end}
		}
	}

	return chunkExtents
}

func GetLocalChunkExtents(chunkExtents map[int64][2]int64) map[int64][2]int64 {
	seqSlice := make([]int64, 0, len(chunkExtents))
	for seq := range chunkExtents {
		seqSlice = append(seqSlice, seq)
	}
	slices.Sort(seqSlice)

	currentIdx := int64(0)
	localChunkExtents := make(map[int64][2]int64)
	for _, seq := range seqSlice {
		extents := chunkExtents[seq]
		length := extents[1] - extents[0]
		localChunkExtents[seq] = [2]int64{currentIdx, currentIdx + length}
		currentIdx += length
	}

	return localChunkExtents
}

func ReadData(readChan chan Reply, chunkExtents map[int64][2]int64) []byte {
	chunkedDataSlice := make([]DataChunk, 0, len(chunkExtents))
	chunkedDataMap := make(map[int]DataChunk)

	totalRequested := int64(0)
	for _, val := range chunkExtents {
		totalRequested += val[1] - val[0]
	}

	for len(chunkedDataMap) < len(chunkExtents) {
		reply := <-readChan
		extents, ok := chunkExtents[int64(reply.Seq)]
		if !ok {
			continue
		}
		chunkedDataMap[reply.Seq] = DataChunk{Seq: int64(reply.Seq), Data: reply.Payload[extents[0]:extents[1]]}
	}

	for _, chunk := range chunkedDataMap {
		chunkedDataSlice = append(chunkedDataSlice, chunk)
	}

	slices.SortFunc(chunkedDataSlice, func(a, b DataChunk) int {
		return cmp.Compare(a.Seq, b.Seq)
	})

	data := make([]byte, 0, totalRequested)
	for _, chunk := range chunkedDataSlice {
		data = append(data, chunk.Data...)
	}

	return data
}

func (b *PingBackend) ReadAt(p []byte, off int64) (n int, err error) {
	requestedLength := int64(len(p))
	startOffset := off
	endOffset := off + requestedLength

	chunkExtents := GetChunkExtents(startOffset, endOffset)

	copy(p, ReadData(b.ReadChannel, chunkExtents))

	return len(p), nil
}

func (b *PingBackend) WriteAt(p []byte, off int64) (n int, err error) {
	requestedLength := int64(len(p))
	startOffset := off
	endOffset := off + requestedLength

	chunkExtents := GetChunkExtents(startOffset, endOffset)
	localChunkExtents := GetLocalChunkExtents(chunkExtents)

	for seq, chunkExtents := range chunkExtents {
		localExtents := localChunkExtents[seq]

		payload := make([]byte, localExtents[1]-localExtents[0])
		copy(payload, p[localExtents[0]:localExtents[1]])

		b.WriteChannel <- WriteRequest{
			Seq:         int(seq),
			ExtentStart: int(chunkExtents[0]),
			ExtentEnd:   int(chunkExtents[1]),
			Payload:     payload,
		}
	}

	b.Sync()

	return len(p), nil
}

func (b *PingBackend) Sync() error {
	<-b.SyncChannel
	return nil
}

func (b *PingBackend) Size() (int64, error) {
	return int64(CHUNK_SIZE) * int64(NUM_CHUNKS), nil
}

func ExposeBackend(replyChan chan Reply, writeRequestChan chan WriteRequest, syncChan chan bool, readyChan chan bool) {
	b := NewPingBackend(replyChan, writeRequestChan, syncChan)
	l, err := net.Listen("tcp", "127.0.0.1:10809")
	if err != nil {
		panic(err)
	}

	readyChan <- true

	for {
		conn, err := l.Accept()
		if err != nil {
			continue
		}

		go ServeNBD(b, conn)
	}
}

func ServeNBD(backend backend.Backend, conn net.Conn) {
	export := []*server.Export{{
		Name:        "pinghdd",
		Description: "Stores data in icmp echo requests",
		Backend:     backend,
	}}

	options := server.Options{
		ReadOnly:           false,
		MinimumBlockSize:   1,
		PreferredBlockSize: 1024,
		MaximumBlockSize:   1024,
	}

	err := server.Handle(conn, export, &options)

	if err != nil {
		fmt.Println(err)
		return
	}
}

func ConnectNBD() {
	conn, err := net.Dial("tcp", "127.0.0.1:10809")
	if err != nil {
		panic(err)
	}

	f, err := os.Open("/dev/nbd0")
	if err != nil {
		panic(err)
	}

	err = client.Connect(conn, f, &client.Options{
		ExportName: "pinghdd",
		BlockSize:  1024,
	})

	if err != nil {
		panic(err)
	}
}
