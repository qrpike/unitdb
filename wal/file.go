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

package wal

import (
	"encoding"
	"fmt"
	"io"
	"os"
)

type (
	_Segment struct {
		offset int64
		size   uint32
	}
	_File struct {
		*os.File
		segments   _Segments
		size       int64
		targetSize int64
	}
)

type _Segments [3]_Segment

func openFile(name string, targetSize int64) (_File, error) {
	fileFlag := os.O_CREATE | os.O_RDWR
	fileMode := os.FileMode(0666)

	f := _File{}
	fi, err := os.OpenFile(name, fileFlag, fileMode)
	if err != nil {
		return f, err
	}
	f.File = fi

	stat, err := fi.Stat()
	if err != nil {
		return f, err
	}
	f.size = stat.Size()
	f.targetSize = targetSize

	return f, err
}

func newSegments() _Segments {
	segments := _Segments{}
	segments[0] = _Segment{offset: int64(headerSize), size: 0}
	segments[1] = _Segment{offset: int64(headerSize), size: 0}
	return segments
}

func (sg *_Segments) currSize() uint32 {
	return sg[1].size
}

func (sg *_Segments) recoveryOffset(offset int64) int64 {
	if offset == sg[0].offset {
		offset += int64(sg[0].size)
	}
	if offset == sg[1].offset {
		offset += int64(sg[1].size)
	}
	if offset == sg[2].offset {
		offset += int64(sg[2].size)
	}
	return offset
}

func (sg *_Segments) freeSize(offset int64) uint32 {
	if offset == sg[0].offset {
		return sg[0].size
	}
	if offset == sg[1].offset {
		return sg[1].size
	}
	if offset == sg[2].offset {
		return sg[2].size
	}
	return 0
}

func (sg *_Segments) allocate(size uint32) int64 {
	off := sg[1].offset
	sg[1].size -= size
	sg[1].offset += int64(size)
	return off
}

func (sg *_Segments) free(offset int64, size uint32) (ok bool) {
	if sg[0].offset+int64(sg[0].size) == offset {
		sg[0].size += size
		return true
	}
	if sg[1].offset+int64(sg[1].size) == offset {
		sg[1].size += size
		return true
	}
	return false
}

func (sg *_Segments) swap(targetSize int64) error {
	if sg[1].size != 0 && sg[1].offset+int64(sg[1].size) == sg[2].offset {
		sg[1].size += sg[2].size
		sg[2].size = 0
	}
	if targetSize < int64(sg[0].size) {
		sg[2].offset = sg[1].offset
		sg[2].size = sg[1].size
		sg[1].offset = sg[0].offset
		sg[1].size = sg[0].size
		sg[0].size = 0
		fmt.Println("wal.Swap: segments ", sg)
	}
	return nil
}

func (f *_File) truncate(size int64) error {
	if err := f.Truncate(size); err != nil {
		return err
	}
	f.size = size
	return nil
}

// copy copies the file to a new file.
func (f *_File) copy(bufferSize int64) (int64, error) {
	if err := f.File.Sync(); err != nil {
		return 0, err
	}
	stat, err := f.File.Stat()
	if err != nil || stat.Size() == int64(0) {
		return 0, err
	}
	newName := fmt.Sprintf("%s.%d", f.File.Name(), f.File.Fd())
	newFile, err := os.OpenFile(newName, os.O_CREATE|os.O_RDWR, os.FileMode(0666))
	if err != nil {
		return 0, err
	}

	bufSize := stat.Size()
	if bufSize > bufferSize {
		bufSize = bufferSize
	}

	buf := make([]byte, bufSize)
	size := int64(0)
	for {
		n, err := f.File.Read(buf)
		if err != nil && err != io.EOF {
			return 0, err
		}
		if n == 0 {
			break
		}

		if _, err := newFile.Write(buf[:n]); err != nil {
			return 0, err
		}
		size += int64(n)
		if bufSize <= stat.Size()-size {
			bufSize = stat.Size() - size
			buf = make([]byte, bufSize)
		}
	}
	return size, err
}

func (f *_File) reset() error {
	f.size = 0
	if err := f.truncate(0); err != nil {
		return err
	}
	if _, err := f.Seek(0, 0); err != nil {
		return err
	}
	return nil
}

func (f *_File) allocate(size uint32) (int64, error) {
	if size == 0 {
		panic("unable to allocate zero bytes")
	}
	// Allocation to free segment happens when log reaches its target size to avoid fragmentation.
	if f.targetSize > (f.size+int64(size)) || f.segments.currSize() < size {
		off := f.size
		if err := f.Truncate(off + int64(size)); err != nil {
			return 0, err
		}
		f.size += int64(size)
		return off, nil
	}
	off := f.segments.allocate(size)

	return off, nil
}

func (f *_File) readAt(buf []byte, off int64) (int, error) {
	return f.ReadAt(buf, off)
}

func (f *_File) writeMarshalableAt(m encoding.BinaryMarshaler, off int64) error {
	buf, err := m.MarshalBinary()
	if err != nil {
		return err
	}
	_, err = f.WriteAt(buf, off)
	return err
}

func (f *_File) readUnmarshalableAt(m encoding.BinaryUnmarshaler, size uint32, off int64) error {
	buf := make([]byte, size)
	if _, err := f.ReadAt(buf, off); err != nil {
		return err
	}
	return m.UnmarshalBinary(buf)
}

func (f *_File) Size() int64 {
	return f.size
}
