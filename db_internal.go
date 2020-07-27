/*
 * Copyright 2020 Saffat Technologies, Ltd.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package unitdb

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
	"sort"
	"sync/atomic"
	"time"

	"github.com/golang/snappy"
	"github.com/unit-io/bpool"
	"github.com/unit-io/unitdb/message"
)

const (
	entriesPerIndexBlock = 255 // (4096 i.e blocksize - 14 fixed/16 i.e entry size)
	seqsPerWindowBlock   = 335 // ((4096 i.e. blocksize - 26 fixed)/12 i.e. window entry size)
	// MaxBlocks       = math.MaxUint32
	nShards       = 16
	indexPostfix  = ".index"
	dataPostfix   = ".data"
	windowPostfix = ".win"
	logPostfix    = ".log"
	leasePostfix  = ".lease"
	lockPostfix   = ".lock"
	idSize        = 9 // message ID prefix with additional encryption bit
	filterPostfix = ".filter"
	version       = 1 // file format version

	// maxExpDur expired keys are deleted from DB after durType*maxExpDur.
	// For example if durType is Minute and maxExpDur then
	// all expired keys are deleted from db in 1 minutes
	maxExpDur = 1

	// maxTopicLength is the maximum size of a topic in bytes.
	maxTopicLength = 1 << 16

	// maxValueLength is the maximum size of a value in bytes.
	maxValueLength = 1 << 30

	// maxKeys is the maximum numbers of keys in the DB.
	maxKeys = math.MaxInt64

	// maxSeq is the maximum number of seq supported.
	maxSeq = math.MaxUint64
)

type dbInfo struct {
	encryption  int8
	sequence    uint64
	logSequence uint64
	count       uint64
	blockIdx    int32
	windowIdx   int32
	cacheID     uint64
}

func (db *DB) writeHeader(writeFreeList bool) error {
	if writeFreeList {
		db.lease.defrag()
		if err := db.lease.write(); err != nil {
			logger.Error().Err(err).Str("context", "db.writeHeader")
			return err
		}
	}
	h := header{
		signature: signature,
		version:   version,
		dbInfo: dbInfo{
			encryption: db.encryption,
			sequence:   atomic.LoadUint64(&db.sequence),
			count:      atomic.LoadUint64(&db.count),
			blockIdx:   db.blocks(),
			windowIdx:  db.timeWindow.windowIndex(),
			cacheID:    db.cacheID,
		},
	}
	return db.index.writeMarshalableAt(h, 0)
}

func (db *DB) readHeader() error {
	h := &header{}
	if err := db.index.readUnmarshalableAt(h, headerSize, 0); err != nil {
		logger.Error().Err(err).Str("context", "db.readHeader")
		return err
	}
	// if !bytes.Equal(h.signature[:], signature[:]) {
	// 	return errCorrupted
	// }
	db.dbInfo = h.dbInfo
	db.timeWindow.setWindowIndex(db.dbInfo.windowIdx)
	if err := db.lease.read(); err != nil {
		if err == io.EOF {
			return nil
		}
		logger.Error().Err(err).Str("context", "db.readHeader")
		return err
	}
	return nil
}

// loadTopicHash loads topic and offset from window file
func (db *DB) loadTrie() error {
	topics := make(map[uint64]int64) // map[topicHash]offset
	err := db.timeWindow.foreachWindowBlock(func(curw windowHandle) (bool, error) {
		w := &curw
		if w.entryIdx == 0 {
			return false, nil
		}
		if _, ok := topics[w.topicHash]; !ok {
			topics[w.topicHash] = w.offset
		}
		if w.next != 0 {
			// iterate till first win block of topic is found
			return false, nil
		}
		we := w.entries[0]
		off := blockOffset(startBlockIndex(we.Seq()))
		b := blockHandle{file: db.index, offset: off}
		if err := b.read(); err != nil {
			if err == io.EOF {
				return false, nil
			}
			return true, err
		}
		entryIdx := -1
		for i := 0; i < entriesPerIndexBlock; i++ {
			s := b.entries[i]
			if s.seq == we.Seq() { //topic exist in db
				entryIdx = i
				break
			}
		}
		if entryIdx == -1 {
			return false, nil
		}
		// fmt.Println("db.loadTrie: seq, entryIdx ", we.Seq(), entryIdx)
		s := b.entries[entryIdx]

		if s.topicSize == 0 {
			return false, nil
		}

		rawtopic, err := db.data.readTopic(s)
		if err != nil {
			return true, err
		}
		t := new(message.Topic)
		err = t.Unmarshal(rawtopic)
		if err != nil {
			return true, err
		}
		if ok := db.trie.add(topic{hash: w.topicHash, offset: topics[w.topicHash]}, t.Parts, t.Depth); !ok {
			// fmt.Println("db.loadTrie: topic exist in the trie ", w.topicHash, topics[w.topicHash])
			logger.Info().Str("context", "db.loadTrie: topic exist in the trie")
			return false, nil
		}
		return false, nil
	})
	return err
}

// Close closes the DB.
func (db *DB) close() error {
	if !db.setClosed() {
		return errClosed
	}

	// Signal all goroutines.
	close(db.closeC)
	time.Sleep(db.opts.TinyBatchWriteInterval)

	// Acquire lock.
	db.writeLockC <- struct{}{}
	db.syncLockC <- struct{}{}

	// Wait for all goroutines to exit.
	db.closeW.Wait()
	return nil
}

func (db *DB) readEntry(topicHash uint64, seq uint64) (slot, error) {
	blockID := startBlockIndex(seq)
	memseq := db.cacheID ^ seq
	data, err := db.mem.Get(uint64(blockID), memseq)
	if err != nil {
		return slot{}, errMsgIDDeleted
	}
	if data != nil {
		var e entry
		e.UnmarshalBinary(data[:entrySize])
		s := slot{
			seq:       e.seq,
			topicSize: e.topicSize,
			valueSize: e.valueSize,

			cacheBlock: data[entrySize:],
		}
		return s, nil
	}

	off := blockOffset(startBlockIndex(seq))
	bh := blockHandle{file: db.index, offset: off}
	if err := bh.read(); err != nil {
		return slot{}, err
	}

	for i := 0; i < entriesPerIndexBlock; i++ {
		s := bh.entries[i]
		if s.seq == seq {
			return s, nil
		}
	}
	return slot{}, nil
}

// lookups are performed in following order
// ilookup lookups in memory entries from timeWindow
// lookup lookups persisted entries from timeWindow file
func (db *DB) lookup(q *Query) error {
	topics := db.trie.lookup(q.parts, q.depth, q.topicType)
	sort.Slice(topics[:], func(i, j int) bool {
		return topics[i].offset > topics[j].offset
	})
	for _, topic := range topics {
		if len(q.winEntries) > q.Limit {
			break
		}
		limit := q.Limit - len(q.winEntries)
		wEntries := db.timeWindow.lookup(topic.hash, topic.offset, q.cutoff, limit)
		for _, we := range wEntries {
			q.winEntries = append(q.winEntries, query{topicHash: topic.hash, seq: we.Seq()})
		}
	}
	sort.Slice(q.winEntries[:], func(i, j int) bool {
		return q.winEntries[i].seq > q.winEntries[j].seq
	})
	return nil
}

// newBlock adds new block to DB and it returns block offset
func (db *DB) newBlock() (int64, error) {
	off, err := db.index.extend(blockSize)
	db.addBlocks(1)
	return off, err
}

// newBlock adds new block to DB and it returns block offset
func (db *DB) extendBlocks(nBlocks int32) error {
	if _, err := db.index.extend(uint32(nBlocks) * blockSize); err != nil {
		return err
	}
	db.addBlocks(nBlocks)
	return nil
}

func (db *DB) parseTopic(contract uint32, topic []byte) (*message.Topic, uint32, error) {
	t := new(message.Topic)

	//Parse the Key
	t.ParseKey(topic)
	// Parse the topic
	t.Parse(contract, true)
	if t.TopicType == message.TopicInvalid {
		return nil, 0, errBadRequest
	}
	// In case of ttl, add ttl to the msg and store to the db
	if ttl, ok := t.TTL(); ok {
		return t, ttl, nil
	}
	return t, 0, nil
}

func (db *DB) setEntry(e *Entry, encr bool) error {
	//message ID is the database key
	var id message.ID
	var eBit uint8
	var seq uint64
	var rawTopic []byte
	if !e.parsed {
		if e.Contract == 0 {
			e.Contract = message.MasterContract
		}
		t, ttl, err := db.parseTopic(e.Contract, e.Topic)
		if err != nil {
			return err
		}
		if e.ExpiresAt == 0 && ttl > 0 {
			e.ExpiresAt = ttl
		}
		t.AddContract(e.Contract)
		e.topicHash = t.GetHash(e.Contract)
		// topic is added to DB if it is new topic entry
		if _, ok := db.trie.getOffset(e.topicHash); !ok {
			rawTopic = t.Marshal()
			e.topicSize = uint16(len(rawTopic))
		}
		e.parsed = true
	}
	if e.ID != nil {
		id = message.ID(e.ID)
		seq = id.Sequence()
	} else {
		if ok, s := db.data.lease.getSlot(e.topicHash); ok {
			db.meter.Leased.Inc(1)
			seq = s
		} else {
			seq = db.nextSeq()
		}
		id = message.NewID(seq)
	}
	if seq == 0 {
		panic("db.setEntry: seq is zero")
	}

	id.SetContract(e.Contract)
	e.seq = seq
	e.expiresAt = e.ExpiresAt
	val := snappy.Encode(nil, e.Payload)
	if db.encryption == 1 || encr {
		eBit = 1
		val = db.mac.Encrypt(nil, val)
	}
	e.valueSize = uint32(len(val))
	mLen := entrySize + idSize + uint32(e.topicSize) + uint32(e.valueSize)
	e.cacheEntry = make([]byte, mLen)
	entryData, err := e.MarshalBinary()
	if err != nil {
		return err
	}
	copy(e.cacheEntry, entryData)
	copy(e.cacheEntry[entrySize:], id.Prefix())
	e.cacheEntry[entrySize+idSize-1] = byte(eBit)
	// topic data is added on new topic entry and subsequent entries does not pack the topic data.
	if e.topicSize != 0 {
		copy(e.cacheEntry[entrySize+idSize:], rawTopic)
	}
	copy(e.cacheEntry[entrySize+idSize+uint32(e.topicSize):], val)

	return nil
}

// tinyCommit commits tiny batch to write ahead log
func (db *DB) tinyCommit() error {
	if err := db.ok(); err != nil {
		return err
	}

	if db.tinyBatch.count() == 0 {
		return nil
	}

	logWriter, err := db.wal.NewWriter()
	if err != nil {
		return err
	}
	// commit writes batches into write ahead log. The write happen synchronously.
	db.closeW.Add(1)
	db.writeLockC <- struct{}{}
	defer func() {
		db.tinyBatch.buffer.Reset()
		db.tinyBatch.reset()
		<-db.writeLockC
		db.closeW.Done()
	}()
	offset := uint32(0)
	buf := db.tinyBatch.buffer.Bytes()
	for i := uint32(0); i < db.tinyBatch.count(); i++ {
		dataLen := binary.LittleEndian.Uint32(buf[offset : offset+4])
		data := buf[offset+4 : offset+dataLen]
		if err := <-logWriter.Append(data); err != nil {
			return err
		}
		offset += dataLen
	}

	db.setLogSeq(db.seq())
	if err := <-logWriter.SignalInitWrite(db.logSeq()); err != nil {
		return err
	}
	db.meter.Puts.Inc(int64(db.tinyBatch.count()))
	return nil
}

// commit commits batches to write ahead log
func (db *DB) commit(l int, buf *bpool.Buffer) error {
	if err := db.ok(); err != nil {
		return err
	}

	// commit writes batches into write ahead log. The write happen synchronously.
	db.writeLockC <- struct{}{}
	db.closeW.Add(1)
	defer func() {
		buf.Reset()
		<-db.writeLockC
		db.closeW.Done()
	}()

	logWriter, err := db.wal.NewWriter()
	if err != nil {
		return err
	}

	offset := uint32(0)
	data := buf.Bytes()
	for i := 0; i < l; i++ {
		dataLen := binary.LittleEndian.Uint32(data[offset : offset+4])
		if err := <-logWriter.Append(data[offset+4 : offset+dataLen]); err != nil {
			return err
		}
		offset += dataLen
	}

	db.setLogSeq(db.seq())
	return <-logWriter.SignalInitWrite(db.logSeq())
}

// delete deletes the given key from the DB.
func (db *DB) delete(topicHash, seq uint64) error {
	if db.flags.Immutable {
		return nil
	}
	// Test filter block for the message id presence
	if !db.filter.Test(seq) {
		return nil
	}
	db.meter.Dels.Inc(1)
	blockID := startBlockIndex(seq)
	memseq := db.cacheID ^ seq
	if err := db.mem.Remove(uint64(blockID), memseq); err != nil {
		return err
	}
	blockIdx := startBlockIndex(seq)
	if blockIdx > db.blocks() {
		return nil // no record to delete
	}
	blockWriter := newBlockWriter(&db.index, nil)
	e, err := blockWriter.del(seq)
	if err != nil {
		return err
	}
	db.lease.free(e.seq, e.msgOffset, e.mSize())
	db.decount(1)
	if db.syncWrites {
		return db.sync()
	}
	return nil
}

// seq current seq of the DB.
func (db *DB) seq() uint64 {
	return atomic.LoadUint64(&db.sequence)
}

func (db *DB) nextSeq() uint64 {
	return atomic.AddUint64(&db.sequence, 1)
}

// LogSeq current log sequence of the DB.
func (db *DB) logSeq() uint64 {
	return atomic.LoadUint64(&db.logSequence)
}

func (db *DB) setLogSeq(seq uint64) error {
	atomic.StoreUint64(&db.logSequence, seq)
	return nil
}

// blocks returns the total blocks in the DB.
func (db *DB) blocks() int32 {
	return atomic.LoadInt32(&db.blockIdx)
}

// addBlock adds new block to the DB.
func (db *DB) addBlocks(nBlocks int32) int32 {
	return atomic.AddInt32(&db.blockIdx, nBlocks)
}

func (db *DB) incount(count uint64) uint64 {
	return atomic.AddUint64(&db.count, count)
}

func (db *DB) decount(count uint64) uint64 {
	return atomic.AddUint64(&db.count, -count)
}

// Set closed flag; return true if not already closed.
func (db *DB) setClosed() bool {
	return atomic.CompareAndSwapUint32(&db.closed, 0, 1)
}

// Check whether DB was closed.
func (db *DB) isClosed() bool {
	return atomic.LoadUint32(&db.closed) != 0
}

// Check read ok status.
func (db *DB) ok() error {
	if db.isClosed() {
		return errors.New("wal is closed")
	}
	return nil
}
