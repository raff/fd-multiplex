package multiplex

/* The MIT License (MIT)
 *
 * Copyright (c) 2013 Yannick Scherer
 * Copyright (c) 2014 Raffaele Sena
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy of
 * this software and associated documentation files (the "Software"), to deal in
 * the Software without restriction, including without limitation the rights to
 * use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of
 * the Software, and to permit persons to whom the Software is furnished to do so,
 * subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS
 * FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR
 * COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER
 * IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN
 * CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
 */

// ----------------------------------------------------------------------
//
//   CONCEPT
//
// ----------------------------------------------------------------------
// We assume that packet fragments arrive in-order on the given file
// descriptor. By prefixing a packet with 4 bytes of length (aligned
// right, zero left-padded, includes length of channel ID) and a single
// byte containing the channel ID, we can reassemble the packet and
// decide which channel it belongs to.
//
// Channels have to be activated before use, creating a receive buffer
// that is dynamically extended and reduced when needed. Using a
// blocking 'select' function (with a timeout), we can retrieve the
// ID of a channel that has new data available.
//
// Error and status codes (e.g. "data was received on a channel that
// is not active") are negative, while channel IDs are positive or zero.

import (
	"errors"
	"io"
	//"log"
	"net"
	"sync"
	"time"
)

const (
	INITIAL_BUFFER_SIZE = 256
	MAX_CHANNELS        = 256
)

var (
	CHANNEL_IGNORED = errors.New("channel ignored")
	CHANNEL_TIMEOUT = errors.New("channel timeout")
	CHANNEL_CLOSED  = errors.New("channel closed")
)

type ChannelBuffer struct {
	data    []byte // receive buffer
	offset  int    // current read offset
	length  int    // current read length
	initial int    // minimum capacity
	newData int    // 0 = no new data since last 'select'
}

type Multiplex struct {
	conn         net.Conn                     // network connection
	max_channels uint                         // maximum number of channels (0 <= max_channels <= MAX_CHANNELS)
	channels     [MAX_CHANNELS]*ChannelBuffer // O(1) lookup for channels

	sync.Mutex // for exclusive access
}

func (c *Multiplex) LockChannel(channelId uint) bool {
	// lock the Multiplex, but return 0 only if the given channel exists
	c.Lock()

	if c.channels[channelId] == nil {
		c.Unlock()
		return false
	}

	return true
}

// ----------------------------------------------------------------------
//
//   UTILS
//
// ----------------------------------------------------------------------
// -- MUTEX

func (c *Multiplex) lock_channel(channelId uint) bool {
	// lock the Multiplex, but return true only if the given channel exists
	c.Lock()

	if c.channels[channelId] == nil {
		c.Unlock()
		return false
	}

	return true
}

// ----------------------------------------------------------------------
//
//   BASICS
//
// ----------------------------------------------------------------------
// -- CREATE
func NewMultiplex(conn net.Conn) *Multiplex {
	return NewMultiplexEx(conn, MAX_CHANNELS)
}

func NewMultiplexEx(conn net.Conn, max_channels uint) *Multiplex {
	if max_channels < 0 || max_channels > MAX_CHANNELS {
		return nil
	}

	return &Multiplex{conn: conn, max_channels: max_channels}
}

// -- ACTIVATE CHANNEL
func (c *Multiplex) enable_channel(channelId uint, initialBufferSize int) {
	if c != nil && channelId >= 0 && channelId <= (c.max_channels-1) && c.channels[channelId] == nil {
		if initialBufferSize <= 0 {
			initialBufferSize = INITIAL_BUFFER_SIZE
		}

		buf := &ChannelBuffer{data: make([]byte, initialBufferSize), initial: initialBufferSize}
		c.channels[channelId] = buf
	}
}

func (c *Multiplex) Enable(channelId uint, initialBufferSize int) {
	c.Lock()
	c.enable_channel(channelId, initialBufferSize)
	c.Unlock()
}

func (c *Multiplex) EnableRange(minChannel, maxChannel uint, initialBufferSize int) {
	c.Lock()
	for i := minChannel; i <= maxChannel; i++ {
		c.enable_channel(i, initialBufferSize)
	}
	c.Unlock()
}

func (c *Multiplex) Disable(channelId uint) {
	if c.lock_channel(channelId) {
		c.channels[channelId] = nil
		c.Unlock()
	}
}

// ----------------------------------------------------------------------
//
//   REALLOCATION
//
// ----------------------------------------------------------------------
// We double the buffer size if necessary, and we reduce it by at least
// half if less than 25% is filled.
func (c *Multiplex) reallocate_channel(channelId uint, additionalDataSize int) bool {
	if c == nil || c.channels[channelId] == nil {
		return false
	}

	buf := c.channels[channelId]
	newLen := buf.offset + buf.length + additionalDataSize
	allocateLen := len(buf.data) // cap() ?

	if allocateLen > newLen*4 { // Case 1: buffer is too empty (less than 25%)
		allocateLen = buf.initial
	} else if allocateLen >= newLen { // Case 2: buffer is big enough
		return true
	} else if allocateLen >= (buf.length + additionalDataSize) { // Case 3: move data within buffer (set offset to 0)
		buf.data = buf.data[buf.offset : buf.length+additionalDataSize]
		buf.offset = 0
		return true
	}

	// Case 4: shrink or extend buffer
	for allocateLen < newLen {
		allocateLen *= 2
	}

	if allocateLen <= len(buf.data) {
		buf.data = buf.data[:buf.offset+allocateLen]
	} else {
		newbuf := make([]byte, allocateLen)
		copy(newbuf, buf.data[buf.offset:buf.offset+buf.length])
		buf.data = newbuf
		buf.offset = 0
	}

	return true
}

// ----------------------------------------------------------------------
//
//   MODIFY BUFFER
//
// ----------------------------------------------------------------------
func (c *Multiplex) write_channel(channelId uint, data []byte) {
	length := len(data)

	if c.reallocate_channel(channelId, length) {
		buf := c.channels[channelId]
		if buf != nil {
			copy(buf.data[buf.offset:], data)
			buf.length += length
			buf.newData = length
		}
	}
}

func (c *Multiplex) Write(channelId uint, data []byte) {
	if c.lock_channel(channelId) {
		c.write_channel(channelId, data)
		c.Unlock()
	}
}

func (c *Multiplex) copy_channel(channelId uint, dst []byte) (int, error) {
	buf := c.channels[channelId]
	if buf == nil {
		return 0, CHANNEL_IGNORED
	}

	copyLen := len(dst)

	if buf.length < len(dst) {
		copyLen = buf.length
	}

	copy(dst, buf.data[buf.offset:buf.offset+copyLen])
	return copyLen, nil
}

func (c *Multiplex) Copy(channelId uint, dst []byte) (int, error) {
	if !c.lock_channel(channelId) {
		return 0, CHANNEL_CLOSED
	}

	defer c.Unlock()
	return c.copy_channel(channelId, dst)
}

func (c *Multiplex) read_channel(channelId uint, dst []byte) (int, error) {
	buf := c.channels[channelId]
	if buf == nil {
		//log.Println("read_channel", channelId, "IGNORED")
		return 0, CHANNEL_IGNORED
	}

	copyLen := len(dst)

	if buf.length < copyLen {
		copyLen = buf.length
	}

	copy(dst, buf.data[buf.offset:buf.offset+copyLen])
	buf.offset += copyLen
	buf.length -= copyLen
	buf.newData -= copyLen

	if buf.newData < 0 {
		buf.newData = 0
	}

	return copyLen, nil
}

func (c *Multiplex) Read(channelId uint, dst []byte) (int, error) {
	if !c.lock_channel(channelId) {
		//log.Println("Read", channelId, "IGNORED")
		return 0, CHANNEL_CLOSED
	}

	defer c.Unlock()
	return c.read_channel(channelId, dst)
}

func (c *Multiplex) clear_channel(channelId uint) {
	buf := c.channels[channelId]
	buf.offset = 0
	buf.length = 0
	buf.newData = 0
}

func (c *Multiplex) Clear(channelId uint) {
	if c.lock_channel(channelId) {
		c.clear_channel(channelId)
		c.Unlock()
	}
}

// ----------------------------------------------------------------------
//
//   RECEIVE LOGIC
//
// ----------------------------------------------------------------------
func conn_read(conn net.Conn, timeout time.Duration, buffer []byte) (int, error) {
	position := 0
	length := len(buffer)

	conn.SetReadDeadline(time.Now().Add(timeout))

	for position < length {
		bytesRead, err := conn.Read(buffer[position:])
		//log.Println("conn_read", "read", bytesRead, "error", err)
		if err != nil {
			if err == io.EOF {
				//log.Println("conn_read", "CLOSED")
				return 0, CHANNEL_CLOSED
			} else if neterr, ok := err.(net.Error); ok && neterr.Timeout() {
				//log.Println("conn_read", "TIMEOUT")
				return 0, CHANNEL_TIMEOUT
			} else {
				// we should do better than this
				//log.Println("conn_read", err)
				return 0, CHANNEL_CLOSED
			}
		} else {
			position += bytesRead
		}
	}
	return length, nil
}

func (c *Multiplex) select_channel(timeout time.Duration) (uint, error) {
	if c == nil {
		return 0, CHANNEL_CLOSED
	}

	// Check if data is available somewhere
	for i := 0; i < int(c.max_channels); i++ {
		if buf := c.channels[i]; buf != nil && buf.length > 0 && buf.newData != 0 {
			buf.newData = 0
			return uint(i), nil
		}
	}

	//
	var prefixBuffer [5]byte
	_, err := conn_read(c.conn, timeout, prefixBuffer[:])
	if err != nil {
		return 0, err
	}

	//
	dataLength := int((prefixBuffer[0] << 24) | (prefixBuffer[1] << 16) | (prefixBuffer[2] << 8) | (prefixBuffer[3] << 0))
	channelId := uint(prefixBuffer[4])

	buffer := make([]byte, dataLength-1)
	_, err = conn_read(c.conn, timeout, buffer)
	if err != nil {
		return 0, err
	}
	if c.channels[channelId] == nil {
		return 0, CHANNEL_IGNORED
	}
	c.write_channel(channelId, buffer)
	return channelId, nil
}

func (c *Multiplex) Select(timeout time.Duration) (uint, error) {
	c.Lock()
	defer c.Unlock()

	return c.select_channel(timeout)
}

func (c *Multiplex) Ignore(channelId uint) {
	c.Lock()
	c.channels[channelId].newData = 0
	c.Unlock()
}

func (c *Multiplex) receive_channel(timeout time.Duration, channelId uint, dst []byte) (int, error) {
	if c == nil {
		return 0, CHANNEL_CLOSED
	}

	buf := c.channels[channelId]
	if buf == nil {
		return 0, CHANNEL_IGNORED
	}

	length := len(dst)

	// Check if data is already buffered.
	if buf.length > 0 {
		if length <= buf.length {
			copy(dst, buf.data[buf.offset:])
			buf.offset += length
			buf.length -= length
			return length, nil
		} else {
			tmpLen := buf.length
			copy(dst, buf.data[buf.offset:buf.offset+tmpLen])
			buf.offset = 0
			buf.length = 0
			return tmpLen, nil
		}
	}

	// Receive on the given Channel
	receiveId, err := c.select_channel(timeout)
	if err != nil {
		return 0, err
	}
	if receiveId != channelId {
		return 0, CHANNEL_IGNORED
	}

	// Copy from ChannelBuffer
	return c.read_channel(channelId, dst)
}

func (c *Multiplex) Receive(timeout time.Duration, channelId uint, data []byte) (int, error) {
	c.Lock()
	defer c.Unlock()
	return c.receive_channel(timeout, channelId, data)
}

// ----------------------------------------------------------------------
//
//   SEND LOGIC
//
// ----------------------------------------------------------------------
func (c *Multiplex) Send(channelId uint, src []byte) int {
	if len(src) == 0 {
		return -1
	}

	c.Lock()
	defer c.Unlock()

	length := len(src) + 1

	header := []byte{
		(byte)((length >> 24) & 0xFF),
		(byte)((length >> 16) & 0xFF),
		(byte)((length >> 8) & 0xFF),
		(byte)((length >> 0) & 0xFF),
		(byte)(channelId & 0xFF)}

	// TODO: check errors!
	len1, _ := c.conn.Write(header)
	len2, _ := c.conn.Write(src)
	return len1 + len2
}

// ----------------------------------------------------------------------
//
//   BUFFER INSPECTION
//
// ----------------------------------------------------------------------
func (c *Multiplex) Length(channelId uint) int {
	if !c.lock_channel(channelId) {
		return -1
	}

	defer c.Unlock()
	return c.channels[channelId].length
}

func (c *Multiplex) LastReceived(channelId uint) int {
	if !c.lock_channel(channelId) {
		return -1
	}

	defer c.Unlock()
	return c.channels[channelId].newData
}

func (c *Multiplex) Get(channelId uint) []byte {
	c.Lock()
	defer c.Unlock()

	buf := c.channels[channelId]
	return buf.data[buf.offset:]
}

func (c *Multiplex) Dup(channelId uint) []byte {
	c.Lock()
	defer c.Unlock()

	buf := c.channels[channelId]
	return append([]byte(nil), buf.data[buf.offset:]...)
}
