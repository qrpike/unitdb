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
	"encoding/binary"
)

var (
	signature     = [7]byte{'u', 'n', 'i', 't', 'd', 'b', '\xfe'}
	logHeaderSize = 28
	headerSize    = uint32(47)
)

type _LogInfo struct {
	version    uint16
	status     LogStatus
	timeID     int64
	entryCount uint32
	size       uint32
	offset     int64

	_ [28]byte
}

// MarshalBinary serialized logInfo into binary data.
func (l _LogInfo) MarshalBinary() ([]byte, error) {
	buf := make([]byte, logHeaderSize)
	binary.LittleEndian.PutUint16(buf[:2], l.version)
	binary.LittleEndian.PutUint16(buf[2:4], uint16(l.status))
	binary.LittleEndian.PutUint64(buf[4:12], uint64(l.timeID))
	binary.LittleEndian.PutUint32(buf[12:16], l.entryCount)
	binary.LittleEndian.PutUint32(buf[16:20], l.size)
	binary.LittleEndian.PutUint64(buf[20:28], uint64(l.offset))
	return buf, nil
}

// UnmarshalBinary deserialized logInfo from binary data.
func (l *_LogInfo) UnmarshalBinary(data []byte) error {
	l.version = binary.LittleEndian.Uint16(data[:2])
	l.status = LogStatus(binary.LittleEndian.Uint16(data[2:4]))
	l.timeID = int64(binary.LittleEndian.Uint64(data[4:12]))
	l.entryCount = binary.LittleEndian.Uint32(data[12:16])
	l.size = binary.LittleEndian.Uint32(data[16:20])
	l.offset = int64(binary.LittleEndian.Uint64(data[20:28]))
	return nil
}

type _Header struct {
	signature [7]byte
	version   uint32
	segments  _Segments
	_         [2]byte
}

// MarshalBinary serialized header into binary data.
func (h _Header) MarshalBinary() ([]byte, error) {
	buf := make([]byte, headerSize)
	copy(buf[:7], h.signature[:])
	binary.LittleEndian.PutUint32(buf[7:11], h.version)
	binary.LittleEndian.PutUint32(buf[11:15], h.segments[0].size)
	binary.LittleEndian.PutUint64(buf[15:23], uint64(h.segments[0].offset))
	binary.LittleEndian.PutUint32(buf[23:27], h.segments[1].size)
	binary.LittleEndian.PutUint64(buf[27:35], uint64(h.segments[1].offset))
	binary.LittleEndian.PutUint32(buf[35:39], h.segments[2].size)
	binary.LittleEndian.PutUint64(buf[39:47], uint64(h.segments[2].offset))
	return buf, nil
}

// UnmarshalBinary deserialized header from binary data.
func (h *_Header) UnmarshalBinary(data []byte) error {
	copy(h.signature[:], data[:7])
	h.version = binary.LittleEndian.Uint32(data[7:11])
	h.segments[0].size = binary.LittleEndian.Uint32(data[11:15])
	h.segments[0].offset = int64(binary.LittleEndian.Uint64(data[15:23]))
	h.segments[1].size = binary.LittleEndian.Uint32(data[23:27])
	h.segments[1].offset = int64(binary.LittleEndian.Uint64(data[27:35]))
	h.segments[2].size = binary.LittleEndian.Uint32(data[35:39])
	h.segments[2].offset = int64(binary.LittleEndian.Uint64(data[39:47]))
	return nil
}
