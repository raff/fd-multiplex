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
	"io"
	"log"
	"net"
	"sync"
	"time"
)

const (
	INITIAL_BUFFER_SIZE = 256
	MAX_CHANNELS        = 256

	headerLength = 5 // 6 // 1:magic + 4:size + 1:channel
	magic        = 0x69
)

type MultiplexError string

func (e MultiplexError) Error() string {
	return "multiplex error: " + string(e)
}

var (
	CHANNEL_IGNORED = MultiplexError("channel ignored")
	CHANNEL_TIMEOUT = MultiplexError("channel timeout")
	CHANNEL_CLOSED  = MultiplexError("channel closed")
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
		if buf.offset > 0 {
			copy(buf.data, buf.data[buf.offset:buf.offset+buf.length])
			buf.offset = 0
		}
		return true
	}

	// Case 4: shrink or extend buffer
	for allocateLen < newLen {
		allocateLen *= 2
	}

	newbuf := make([]byte, allocateLen)
	copy(newbuf, buf.data[buf.offset:buf.offset+buf.length])
	buf.data = newbuf
	buf.offset = 0

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
		return 0, CHANNEL_IGNORED
	}

	copyLen := len(dst)

	if buf.length < copyLen {
		copyLen = buf.length
		dst = dst[:copyLen]
	}

	copy(dst, buf.data[buf.offset:buf.offset+copyLen])
	buf.offset += copyLen
	buf.length -= copyLen
	buf.newData -= copyLen

	if buf.newData < 0 {
		buf.newData = 0
	}
	if buf.length <= 0 {
		buf.length = 0
		buf.newData = 0
		buf.offset = 0
	}

	return copyLen, nil
}

func (c *Multiplex) Read(channelId uint, dst []byte) (int, error) {
	if !c.lock_channel(channelId) {
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
	if timeout != time.Duration(0) {
		conn.SetReadDeadline(time.Now().Add(timeout))
	}

	position := 0
	length := len(buffer)

	for position < length {
		bytesRead, err := conn.Read(buffer[position:])
		if err != nil {
			if err == io.EOF {
				log.Println("conn_read", "CLOSED")
				return 0, CHANNEL_CLOSED
			} else if neterr, ok := err.(net.Error); ok && neterr.Timeout() {
				log.Println("conn_read", "TIMEOUT")
				return 0, CHANNEL_TIMEOUT
			} else {
				// we should do better than this
				log.Println("conn_read", err)
				return 0, CHANNEL_CLOSED
			}
		} else {
			position += bytesRead
		}
	}

	return position, nil
}

func (c *Multiplex) select_channel(timeout time.Duration, channelId uint) (uint, error) {
	if c == nil {
		return 0, CHANNEL_CLOSED
	}

	// Check if data is available somewhere
	if channelId < c.max_channels {
		if buf := c.channels[channelId]; buf != nil && buf.length > 0 && buf.newData != 0 {
			buf.newData = 0
			return channelId, nil
		}
	}

	for i := 0; i < int(c.max_channels); i++ {
		if buf := c.channels[i]; buf != nil && buf.length > 0 && buf.newData != 0 {
			buf.newData = 0
			return uint(i), nil
		}
	}

	//
	var prefixBuffer [headerLength]byte
	n, err := conn_read(c.conn, timeout, prefixBuffer[:])
	if err != nil {
		return 0, err
	}
	if n != headerLength {
		log.Println("expected", headerLength, "read", n)
		return 0, CHANNEL_IGNORED
	}

	/*
			if prefixBuffer[0] != magic {
				log.Println("expected", magic, "got", prefixBuffer)
		                return 0, CHANNEL_IGNORED
			}
	*/

	//
	dataLength := int(prefixBuffer[0])<<24 | int(prefixBuffer[1])<<16 | int(prefixBuffer[2])<<8 | int(prefixBuffer[3])<<0
	channelId = uint(prefixBuffer[4])

	buffer := make([]byte, dataLength-1)
	start := 0
	for start < dataLength-1 {
		n, err = conn_read(c.conn, time.Duration(0), buffer[start:])
		if err != nil {
			return 0, err
		}
		if n == 0 {
			log.Println("select_channel", "expected", len(buffer)-start, "got 0")
		}
		start += n
	}

	if c.channels[channelId] == nil {
		return channelId, CHANNEL_IGNORED
	}
	c.write_channel(channelId, buffer)
	return channelId, nil
}

func (c *Multiplex) Select(timeout time.Duration) (uint, error) {
	c.Lock()
	defer c.Unlock()

	return c.select_channel(timeout, c.max_channels)
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
		if length >= buf.length {
			length = buf.length
		}

		copy(dst, buf.data[buf.offset:buf.offset+length])
		buf.offset += length
		buf.length -= length
		return length, nil
	}

        receiveId, err := c.select_channel(timeout, channelId)
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
        for {
	    c.Lock()
	    n, err := c.receive_channel(timeout, channelId, data)
            if err != CHANNEL_IGNORED {
                c.Unlock()
                return n, err
            }

            c.Unlock()
        }

        // unreachable
        return 0, CHANNEL_IGNORED
}

// ----------------------------------------------------------------------
//
//   SEND LOGIC
//
// ----------------------------------------------------------------------
func (c *Multiplex) Send(channelId uint, src []byte) (int, error) {
	if len(src) == 0 {
		return 0, nil
	}

	c.Lock()
	defer c.Unlock()

	length := len(src) + 1

	buffer := []byte{
		//magic,
		(byte)((length >> 24) & 0xFF),
		(byte)((length >> 16) & 0xFF),
		(byte)((length >> 8) & 0xFF),
		(byte)((length >> 0) & 0xFF),
		(byte)(channelId & 0xFF)}

	buffer = append(buffer, src...)

	n, err := c.conn.Write(buffer)
	if n != len(buffer) || err != nil {
		log.Println("sent ", n, "expected", len(buffer), err)
	} else {
		n -= headerLength
	}

	return n, err
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
	return append([]byte(nil), buf.data[buf.offset:buf.offset+buf.length]...)
}
