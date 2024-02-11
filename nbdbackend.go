package main

type WriteRequest struct {
	Seq     int
	Payload []byte
}

type PingBackend struct {
	ReadChannel  chan Reply
	WriteChannel chan WriteRequest
}

// func (b *PingBackend) ReadAt(p []byte, off int64) (n int, err error) {
// 	requestedLength := int64(len(p))

// 	startOffset := off
// 	endOffset := off + requestedLength

// 	startChunk := startOffset / CHUNK_SIZE
// 	endChunk := endOffset / CHUNK_SIZE

// 	firstChunkBegin := startOffset % CHUNK_SIZE
// 	firstChunkEnd := max(CHUNK_SIZE, firstChunkBegin+requestedLength)

// 	lastChunkEnd := endOffset % CHUNK_SIZE
// 	lastChunkBegin := min(0, lastChunkEnd-requestedLength)

// 	dataByChunk := make(map[int][]byte)
// 	finishedReading := false

// 	for !finishedReading {
// 		select {
// 		case reply := <-b.ReadChannel:
// 			if reply.Seq == int(startChunk) {
// 				dataByChunk[reply.Seq] = reply.Payload[firstChunkBegin:firstChunkEnd]
// 			} else if reply.Seq == int(endChunk) {
// 				dataByChunk[reply.Seq] = reply.Payload[lastChunkBegin:lastChunkEnd]
// 			} else if reply.Seq > int(startChunk) && reply.Seq < int(endChunk) {
// 				dataByChunk[reply.Seq] = reply.Payload
// 			}

// 			if len(dataByChunk) >= int(endChunk-startChunk+1) {
// 				finishedReading = true
// 			}

// 		case <-time.After(time.Second):
// 			finishedReading = true
// 		}
// 	}

// 	for chunk, data := range dataByChunk {

// 	}

// 	return len(p)

// }
