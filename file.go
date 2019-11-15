package tracedb

import (
	"encoding"
	"math/rand"
	"os"

	"github.com/allegro/bigcache"
	"github.com/saffat-in/tracedb/fs"
)

type file struct {
	fs.FileManager
	size int64

	cache   *bigcache.BigCache
	cacheID uint64
}

func openFile(fsyst fs.FileSystem, name string, flag int, perm os.FileMode) (file, error) {
	fi, err := fsyst.OpenFile(name, flag, perm)
	f := file{}
	if err != nil {
		return f, err
	}
	f.FileManager = fi
	stat, err := fi.Stat()
	if err != nil {
		return f, err
	}
	f.size = stat.Size()

	cache, err := bigcache.NewBigCache(config)
	if err != nil {
		return f, err
	}
	f.cache = cache
	f.cacheID = uint64(rand.Uint32())<<32 + uint64(rand.Uint32())
	return f, err
}

func (f *file) extend(size uint32) (int64, error) {
	off := f.size
	if err := f.Truncate(off + int64(size)); err != nil {
		return 0, err
	}
	f.size += int64(size)

	if f.FileManager.Type() == "MemoryMap" {
		return off, f.FileManager.(*fs.OSFile).Mmap(f.size)
	} else {
		return off, nil
	}

}

func (f *file) append(data []byte) (int64, error) {
	off := f.size
	if _, err := f.WriteAt(data, off); err != nil {
		return 0, err
	}
	f.size += int64(len(data))
	if f.FileManager.Type() == "MemoryMap" {
		return off, f.FileManager.(*fs.OSFile).Mmap(f.size)
	} else {
		return off, nil
	}
}

func (f *file) writeMarshalableAt(m encoding.BinaryMarshaler, off int64) error {
	buf, err := m.MarshalBinary()
	if err != nil {
		return err
	}
	_, err = f.WriteAt(buf, off)
	return err
}

func (f *file) readUnmarshalableAt(m encoding.BinaryUnmarshaler, size uint32, off int64) error {
	buf := make([]byte, size)
	if _, err := f.ReadAt(buf, off); err != nil {
		return err
	}
	return m.UnmarshalBinary(buf)
}
