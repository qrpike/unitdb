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

package memdb

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/unit-io/unitdb/filter"
	"github.com/unit-io/unitdb/wal"
)

const (
	dbVersion = 1.0

	logFileName = "data.log"

	nBlocks = 27

	nPoolSize = 27

	// maxMemSize value to limit maximum memory for the mem store.
	defaultMemSize = (int64(1) << 34) - 1

	// maxBufferSize sets Size of buffer to use for pooling.
	defaultBufferSize = 1 << 30 // maximum size of a buffer to use in bufferpool (1GB).

	// logSize sets Size of write ahead log.
	defaultLogSize = 1 << 32 // maximum size of log to grow before allocating free segments (4GB).
)

// To avoid lock bottlenecks block cache is divided into several (nShards) shards.
type (
	_TimeID      int64
	_BlockRecord struct {
		timeRecords map[_TimeID]*filter.Block
		filter      *filter.Generator
	}
	_BlockCache map[_TimeID]*_Block
)

type (
	// _Key is an internal key that includes deleted flag for the key.
	_Key struct {
		delFlag uint8 // deleted flag
		key     uint64
	}
	_BlockKey uint16
	_Block    struct {
		count        int64
		data         _DataTable
		records      map[_Key]int64 // map[key]offset
		sync.RWMutex                // Read Write mutex, guards access to internal map.
	}
)

// _DB represents mem store.
type _DB struct {
	// The db start time.
	start time.Time

	// The metrics to measure timeseries on message events.
	meter *Meter

	writeLockC chan struct{}

	// time mark to manage timeIDs
	timeMark *TimeMark
	timeLock _TimeLock

	// tiny Batch
	tinyBatch *_TinyBatch
	batchPool *_BatchPool

	// Write ahead log
	wal *wal.WAL

	// close
	closeW sync.WaitGroup
	closeC chan struct{}
	closed uint32
	closer io.Closer
}

func newBlock() *_Block {
	return &_Block{data: _DataTable{}, records: make(map[_Key]int64)}
}

// blockID gets blockID from Key using consistent hashing.
func (db *DB) blockID(key uint64) _BlockKey {
	return _BlockKey(db.consistent.FindBlock(key))
}

// iKey an internal key includes deleted flag.
func iKey(delFlag bool, k uint64) _Key {
	dFlag := uint8(0)
	if delFlag {
		dFlag = 1
	}
	return _Key{delFlag: dFlag, key: k}
}

// addTimeBlock adds unique timeID block to the set.
func (db *DB) addTimeBlock(timeID _TimeID, key uint64) error {
	blockKey := db.blockID(key)
	r, ok := db.timeBlocks[blockKey]
	if ok {
		if _, ok := r.timeRecords[timeID]; !ok {
			r.timeRecords[timeID] = filter.NewFilterBlock(r.filter.Bytes())
		}

		// Append key to bloom filter
		r.filter.Append(key)
	}

	return nil
}

func (db *DB) newTinyBatch() *_TinyBatch {
	tinyBatch := &_TinyBatch{doneChan: make(chan struct{})}
	tinyBatch.setTimeID(db.internal.timeMark.newTimeID())
	return tinyBatch
}

func (db *DB) newBatchPool(maxBatches int) *_BatchPool {
	// There must be at least one batch.
	if maxBatches < 1 {
		maxBatches = 1
	}

	pool := &_BatchPool{
		db:          db,
		maxBatches:  maxBatches,
		writeQueue:  make(chan *_TinyBatch, 1),
		batchQueue:  make(chan *_TinyBatch),
		stoppedChan: make(chan struct{}),
	}

	// start the batch dispatcher
	go pool.dispatch()

	return pool
}

func (db *DB) close() error {
	if !db.setClosed() {
		return errClosed
	}

	// Signal all goroutines.
	time.Sleep(db.opts.timeRecordInterval)
	close(db.internal.closeC)
	db.internal.batchPool.stopWait()

	// Acquire lock.
	db.internal.writeLockC <- struct{}{}

	// Wait for all goroutines to exit.
	db.internal.closeW.Wait()

	var err error
	if db.internal.closer != nil {
		if err1 := db.internal.closer.Close(); err1 != nil {
			err = err1
		}
		db.internal.closer = nil
	}

	db.internal.meter.UnregisterAll()

	return err
}

// batch starts a new batch.
func (db *DB) batch() *Batch {
	b := &Batch{db: db, tinyBatchLockC: make(chan struct{}, 1), tinyBatchGroup: make(map[_TimeID]*_TinyBatch)}

	timeID := time.Now().UTC().UnixNano()
	db.internal.timeMark.add(_TimeID(timeID))
	b.tinyBatch = &_TinyBatch{ID: timeID, doneChan: make(chan struct{})}
	b.commitComplete = make(chan struct{})

	return b
}

func (b *_Block) put(ikey _Key, data []byte) error {
	dataLen := uint32(len(data) + 8 + 1 + 4) // data len+key len+flag bit+scratch len
	off, err := b.data.allocate(dataLen)
	if err != nil {
		return err
	}
	var scratch [4]byte
	binary.LittleEndian.PutUint32(scratch[0:4], dataLen)
	if _, err := b.data.writeAt(scratch[:], off); err != nil {
		return err
	}

	// k with flag bit
	var k [9]byte
	k[0] = ikey.delFlag
	binary.LittleEndian.PutUint64(k[1:], ikey.key)
	if _, err := b.data.writeAt(k[:], off+4); err != nil {
		return err
	}
	b.records[ikey] = off
	if _, err := b.data.writeAt(data, off+8+1+4); err != nil {
		return err
	}
	if ikey.delFlag == 0 {
		b.count++
	}

	return nil
}

func (db *DB) delete(key uint64) error {
	db.mu.Lock()

	timeID := db.internal.tinyBatch.timeID()
	timeLock := db.internal.timeLock.getTimeLock(timeID)
	timeLock.Lock()
	defer timeLock.Unlock()

	block, ok := db.blockCache[timeID]
	if !ok {
		block = newBlock()
		db.blockCache[timeID] = block
	}
	db.mu.Unlock()

	// set key is deleted to persist key with timeID to the log.
	ikey := iKey(true, key)
	var data [8]byte
	binary.LittleEndian.PutUint64(data[0:8], uint64(timeID))
	block.put(ikey, data[:])
	db.internal.tinyBatch.incount()

	return nil
}

// move moves deleted records to new blockCache if the timeID of deleted key still exist in the mem store.
func (db *DB) move(timeID _TimeID) error {
	db.mu.RLock()
	block := db.blockCache[timeID]
	db.mu.RUnlock()
	block.RLock()
	defer block.RUnlock()

	// get all deleted keys
	var dkeys []_Key
	for ik := range block.records {
		if ik.delFlag == 1 {
			dkeys = append(dkeys, ik)
		}
	}

	for _, ik := range dkeys {
		off, ok := block.records[ik]
		if ok {
			scratch, err := block.data.readRaw(off, 4) // read data length.
			if err != nil {
				return err
			}
			dataLen := binary.LittleEndian.Uint32(scratch[:4])
			data, err := block.data.readRaw(off, dataLen)
			if err != nil {
				return err
			}
			if dataLen != 8+1+4+8 {
				return errBadRequest
			}
			timeID := _TimeID(binary.LittleEndian.Uint64(data[8+1+4 : dataLen]))
			db.mu.RLock()
			_, ok := db.blockCache[timeID]
			db.mu.RUnlock()
			if ok {
				return db.delete(ik.key)
			}
		}
	}
	return nil
}

// tinyWrite writes tiny batch to DB WAL.
func (db *DB) tinyWrite(tinyBatch *_TinyBatch) error {
	// Backoff to limit excess memroy usage
	// db.blockCache.Backoff()

	logWriter, err := db.internal.wal.NewWriter()
	if err != nil {
		return err
	}

	db.mu.RLock()
	block, ok := db.blockCache[tinyBatch.timeID()]
	db.mu.RUnlock()
	if !ok {
		return nil
	}
	block.RLock()
	defer block.RUnlock()
	// fmt.Println("db.tinyWrite: timeID, count, records ", tinyBatch.timeID(), block.count, block.records)
	for _, off := range block.records {
		scratch, err := block.data.readRaw(off, 4) // read data length.
		if err != nil {
			return err
		}
		dataLen := binary.LittleEndian.Uint32(scratch[:4])
		if data, err := block.data.readRaw(off, dataLen); err == nil {
			if err := <-logWriter.Append(data[4:]); err != nil {
				return err
			}
			data = nil
		}
	}

	if err := <-logWriter.SignalInitWrite(int64(tinyBatch.timeID())); err != nil {
		return err
	}

	db.internal.meter.Syncs.Inc(int64(len(block.records)))

	return nil
}

// tinyCommit commits tiny batch to DB.
func (db *DB) tinyCommit(tinyBatch *_TinyBatch) error {
	db.internal.closeW.Add(1)
	defer func() {
		tinyBatch.abort()
		db.internal.closeW.Done()
	}()

	// commit writes batches into write ahead log. The write happen synchronously.
	db.internal.writeLockC <- struct{}{}
	defer func() {
		<-db.internal.writeLockC
	}()

	if tinyBatch.len() == 0 {
		return nil
	}

	if err := db.tinyWrite(tinyBatch); err != nil {
		return err
	}

	if !tinyBatch.managed {
		db.internal.timeMark.release(tinyBatch.timeID())
	}

	return nil
}

// recovery recovers pending messages from log file.
func (db *DB) startRecover(reset bool) error {
	// Make sure we have a directory
	if err := os.MkdirAll(db.opts.logFilePath, 0777); err != nil {
		return errors.New("db.Open, Unable to create db dir")
	}

	logOpts := wal.Options{Path: db.opts.logFilePath + "/" + logFileName, TargetSize: db.opts.logSize, BufferSize: db.opts.bufferSize, Reset: reset}
	wal, needLogRecovery, err := wal.New(logOpts)
	if err != nil {
		wal.Close()
		return err
	}

	db.internal.closer = wal
	db.internal.wal = wal
	if !needLogRecovery || reset {
		return nil
	}

	// start log recovery
	r, err := wal.NewReader()
	if err != nil {
		return err
	}

	log := make(map[uint64][]byte)
	err = r.Read(func(timeID int64) (ok bool, err error) {
		l := r.Count()
		for i := uint32(0); i < l; i++ {
			logData, ok, err := r.Next()
			if err != nil {
				return false, err
			}
			if !ok {
				break
			}
			dBit := logData[0]
			key := binary.LittleEndian.Uint64(logData[1:9])
			val := logData[9:]
			if dBit == 1 {
				if _, exists := log[key]; exists {
					delete(log, key)
				}
				continue
			}
			log[key] = val
		}
		return false, nil
	})

	if err := wal.Reset(); err != nil {
		return err
	}

	// acquire write lock on recovery from log.
	db.internal.writeLockC <- struct{}{}
	defer func() {
		<-db.internal.writeLockC
	}()
	for k, val := range log {
		if _, err := db.Put(k, val); err != nil {
			return err
		}
	}
	db.internal.meter.Recovers.Inc(int64(len(log)))

	return nil
}

func (db *DB) releaseLog(timeID _TimeID) error {
	// move moves deleted keys before releasing log if the timeID of deleted keys still exist in the mem store
	db.move(timeID)

	db.mu.RLock()
	block, ok := db.blockCache[timeID]
	db.mu.RUnlock()
	block.Lock()
	defer block.Unlock()
	if !ok {
		return errEntryDoesNotExist
	}

	block.data.reset()
	db.mu.Lock()
	delete(db.blockCache, timeID)
	db.mu.Unlock()

	// signal log applied for older messages those are acknowledged or timed out.
	return db.internal.wal.SignalLogApplied(int64(timeID))
}

// tinyBatchLoop handles tiny batches.
func (db *DB) tinyBatchLoop(interval time.Duration) {
	db.internal.closeW.Add(1)
	defer db.internal.closeW.Done()
	tinyBatchTicker := time.NewTicker(interval)
	for {
		select {
		case <-db.internal.closeC:
			tinyBatchTicker.Stop()
			return
		case <-tinyBatchTicker.C:
			db.mu.RLock()
			timeID := db.internal.tinyBatch.timeID()
			timeLock := db.internal.timeLock.getTimeLock(timeID)
			timeLock.RLock()
			db.mu.RUnlock()
			if db.internal.tinyBatch.len() != 0 {
				db.internal.batchPool.write(db.internal.tinyBatch)
			}
			db.internal.tinyBatch = db.newTinyBatch()
			timeLock.RUnlock()
		}
	}
}

// setClosed flag; return true if not already closed.
func (db *DB) setClosed() bool {
	return atomic.CompareAndSwapUint32(&db.internal.closed, 0, 1)
}

// isClosed checks whether DB was closed.
func (db *DB) isClosed() bool {
	return atomic.LoadUint32(&db.internal.closed) != 0
}

// ok checks read ok status.
func (db *DB) ok() error {
	if db.isClosed() {
		return errors.New("db is closed")
	}
	return nil
}