package tracedb

import (
	"errors"
	"sync"
)

// ErrIterationDone is returned by ItemIterator.Next calls when there are no more items to return.
var ErrIterationDone = errors.New("no more items in iterator")

type Item struct {
	key       []byte
	value     []byte
	expiresAt uint32
	err       error
}

// ItemIterator is an iterator over DB key/value pairs. It iterates the items in an unspecified order.
type ItemIterator struct {
	db            *DB
	nextBucketIdx uint32
	item          Item
	queue         []Item
	mu            sync.Mutex
}

// Next returns the next key/value pair if available, otherwise it returns ErrIterationDone error.
func (it *ItemIterator) Next() {
	it.mu.Lock()
	defer it.mu.Unlock()

	it.db.mu.RLock()
	defer it.db.mu.RUnlock()

	if len(it.queue) == 0 {
		for it.nextBucketIdx < it.db.nBuckets {
			err := it.db.forEachBucket(it.nextBucketIdx, func(b bucketHandle) (bool, error) {
				for i := 0; i < entriesPerBucket; i++ {
					sl := b.entries[i]
					if sl.kvOffset == 0 {
						return true, nil
					}
					key, value, err := it.db.data.readKeyValue(sl)
					if err == ErrKeyExpired {
						return false, nil
					}
					if err != nil {
						return true, err
					}
					it.queue = append(it.queue, Item{key: key, value: value, expiresAt: sl.expiresAt, err: err})
				}
				return false, nil
			})
			if err != nil {
				return
			}
			it.nextBucketIdx++
			if len(it.queue) > 0 {
				break
			}
		}
	}

	if len(it.queue) > 0 {
		it.item = it.queue[0]
		it.queue = it.queue[1:]
		//return item.key, item.value, nil
	}

	//return nil, nil, ErrIterationDone
}

// Item returns pointer to the current key-value pair.
// This item is only valid until it.Next() gets called.
func (it *ItemIterator) Item() Item {
	return it.item
}

// Valid returns false when iteration is done.
func (it *ItemIterator) Valid() bool {
	if it.queue == nil {
		return false
	}
	return len(it.queue) > 0
}

// Error returns any accumulated error. Exhausting all the key/value pairs
// is not considered to be an error. A memory iterator cannot encounter errors.
func (it *ItemIterator) Error() error {
	return nil
}

// Key returns the key of the current key/value pair, or nil if done. The caller
// should not modify the contents of the returned slice, and its contents may
// change on the next call to Next.
func (it *ItemIterator) Key() []byte {
	return it.item.key
}

// Value returns the value of the current key/value pair, or nil if done. The
// caller should not modify the contents of the returned slice, and its contents
// may change on the next call to Next.
func (it *ItemIterator) Value() []byte {
	return it.item.value
}

// Release releases associated resources. Release should always succeed and can
// be called multiple times without causing error.
func (it *ItemIterator) Release() {
	return
}