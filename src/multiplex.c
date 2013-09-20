/* The MIT License (MIT)
 * 
 * Copyright (c) 2013 Yannick Scherer
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

#include <stdio.h>
#include <stdlib.h>
#include <time.h>
#include <string.h>
#include <fcntl.h>
#include "multiplex.h"

// -- Buffer Structure
typedef struct ChannelBuffer {
    char * data;    // receive buffer
    int offset;     // current read offset
    int length;     // current read length
    int capacity;   // capacity of receive buffer
    char newData;   // 0 = no new data since last 'select'
} ChannelBuffer;

// -- CREATE
Multiplex * multiplex_new(int fd) {
    Multiplex * m = (Multiplex *)calloc(1, sizeof(Multiplex));
    if (m != 0) {
        m->fd = fd;
    }
    return m;
}

// -- ACTIVATE CHANNEL
void multiplex_activate_channel(Multiplex * c, unsigned char channelId, int initialBufferSize) {
    if (c == 0 || c->channels[channelId] != 0) return;
    else {
        ChannelBuffer * buf = (ChannelBuffer *)calloc(1, sizeof(ChannelBuffer));
        if (buf != 0) {
            int size = initialBufferSize > 0 ? initialBufferSize : CHANNEL_INITIAL_BUFFER_SIZE;
            char * stream = (char *)calloc(size, sizeof(char));
            if (stream == 0) return;
            buf->data = stream;
            buf->offset = 0;
            buf->length = 0;
            buf->capacity = size;
            c->channels[channelId] = buf;
        }
    }
}

// -- REALLOCATE BUFFER
static int multiplex_reallocate_channel(Multiplex * c, unsigned char channelId, int additionalDataSize) {
    if (c == 0 || c->channels[channelId] == 0) return 0;
    else {
        ChannelBuffer * buf = c->channels[channelId];
        char * tmpBuf = buf->data;
        int newLen = buf->offset + buf->length + additionalDataSize;
        int allocateLen = buf->capacity;

        // Case 1: buffer is big enough
        if (allocateLen >= newLen) return 1;

        // Case 2: move data within buffer (set offset to 0)
        if (buf->capacity >= buf->length + additionalDataSize) {
            if (buf->offset > 0) {
                int i = 0; 
                while (i < buf->length) {
                    buf->data[i] = buf->data[buf->offset + i];
                }
                buf->offset = 0;
            }
            return 1;
        }

        // Case 3: extend buffer
        while (allocateLen < newLen) allocateLen *= 2;
        buf->data = (char *)calloc(allocateLen, sizeof(char));
        if (buf->data == 0) { buf->data = tmpBuf; return 0; }
        buf->capacity = allocateLen;
        memcpy(buf->data, tmpBuf + buf->offset, buf->length);
        free(tmpBuf);
        return 1;
    }
}

// -- WRITE TO/READ FROM BUFFER
void multiplex_write(Multiplex * c, unsigned char channelId, char * data, int offset, int length) {
    if (!multiplex_reallocate_channel(c, channelId, length)) return;
    else {
        ChannelBuffer * buf = c->channels[channelId];
        if (buf == 0) return;
        memcpy(buf->data + buf->offset + buf->length, data + offset, length);
        buf->length += length;
        buf->newData = length;
    }
}

int multiplex_read(Multiplex * c, int channelId, char * dst, int offset, int length) {
    if (c == 0 || c->channels[channelId] == 0) return CHANNEL_CLOSED;
    else {
        ChannelBuffer * buf = c->channels[channelId];
        int copyLen = length;
        if (buf == 0) return CHANNEL_IGNORED;
        if (buf->length < length) copyLen = buf->length;
        memcpy(dst + offset, buf->data + buf->offset, copyLen);
        buf->offset += copyLen;
        buf->length -= copyLen;
        buf->newData -= copyLen;
        if (buf->newData < 0) buf->newData = 0;
        return copyLen;
    }
}

// -- READ FIXED LENGTH
static int _fd_read(int fd, int timeoutMs, char * buffer, int length) {
    int position = 0, bytesRead = 0, selectResult = -1;
    struct timeval timeout;
    fd_set fds;

    while (position < length) {
        FD_ZERO(&fds);
        FD_SET(fd, &fds);
        timeout.tv_sec = timeoutMs / 1000;
        timeout.tv_usec = (timeoutMs % 1000) * 1000;

        selectResult = select(fd + 1, &fds, (fd_set *)0, (fd_set *)0, &timeout);
        if (selectResult == 0) return CHANNEL_TIMEOUT;

        if (FD_ISSET(fd,&fds)) {
            bytesRead = read(fd, buffer + position, length - position);
            if (bytesRead <= 0) return CHANNEL_CLOSED;
            else position += bytesRead;
        }
    }
    return length;
}

// -- READ FROM PIPE (returns channel ID as int32; -1 for ignored data, -255 for closed pipe)
int multiplex_select(Multiplex * c, int timeoutMs) {
    char prefixBuffer[5];
    int bytesRead = 0, dataLength = 0;
    unsigned char channelId = 0;

    //
    if (c == 0) return CHANNEL_CLOSED;

    // Check if data is available somewhere
    {
        int i = 0;
        ChannelBuffer * buf = 0;
        for (; i < 256; ++i) {
            buf = c->channels[i];
            if (buf != 0 && buf->length > 0 && buf->newData != 0) {
                buf->newData = 0;
                return i;
            }
        }
    }

    //
    bytesRead = _fd_read(c->fd, timeoutMs, prefixBuffer, 5); 
    if (bytesRead != 5) return bytesRead;

    //
    dataLength = (prefixBuffer[0] << 24) | (prefixBuffer[1] << 16) | (prefixBuffer[2] << 8) | prefixBuffer[3];
    channelId = (unsigned char)prefixBuffer[4];
    {
        char buffer[dataLength - 1];
        bytesRead = _fd_read(c->fd, timeoutMs, buffer, dataLength - 1);
        if (bytesRead != dataLength - 1) return bytesRead;
        if (c->channels[channelId] == 0) return CHANNEL_IGNORED;
        multiplex_write(c, channelId, buffer, 0, dataLength - 1);
    }
    return channelId;
}

// -- RECEIVE
int multiplex_receive(Multiplex * c,
                      int timeoutMs,
                      int channelId,
                      char * dst,
                      int length) {
    int receiveId = CHANNEL_IGNORED;
    ChannelBuffer * buf = c == 0 ? 0 : c->channels[channelId];
    if (c == 0) return CHANNEL_CLOSED;

    // Check if data is already buffered.
    if (buf != 0 && buf->length > 0) {
        if (length <= buf->length) {
            memcpy(dst, buf->data + buf->offset, length);
            buf->offset += length;
            buf->length -= length;
            return length;
        } else {
            int tmpLen = buf->length;
            memcpy(dst, buf->data + buf->offset, tmpLen);
            buf->offset = 0;
            buf->length = 0;
            return tmpLen;
        }
    }

    // Receive on the given Channel
    receiveId = multiplex_select(c, timeoutMs);
    if (receiveId < 0) return receiveId;
    if (receiveId != channelId) return CHANNEL_IGNORED;

    // Copy from ChannelBuffer
    return multiplex_read(c, channelId, dst, 0, length);
}

// -- SEND
int multiplex_send(Multiplex * c, int channelId, char const * src, int length) {
    const int len = length + 1;
    char buffer[5 + length];
    buffer[0] = (char)((len >> 24) & 0xFF);
    buffer[1] = (char)((len >> 16) & 0xFF);
    buffer[2] = (char)((len >> 8) & 0xFF);
    buffer[3] = (char)(len & 0xFF);
    buffer[4] = (char)(channelId & 0xFF);
    memcpy(buffer + 5, src, length);
    return write(c->fd, buffer, 5 + length);
}

// -- ACCESS
int multiplex_length(Multiplex * c, int channelId) {
    if (c == 0 || c->channels[channelId] == 0) return -1;
    return c->channels[channelId]->length;
}

int multiplex_last_received(Multiplex * c, int channelId) {
    if (c == 0 || c->channels[channelId] == 0) return 0;
    return c->channels[channelId]->newData;
}

char const * multiplex_get(Multiplex * c, int channelId) {
    if (c == 0 || c->channels[channelId] == 0) return 0;
    else {
        ChannelBuffer * buf = c->channels[channelId];
        return buf->data + buf->offset;
    }
}

char * multiplex_copy(Multiplex * c, int channelId, int offset, int length) {
    if (c == 0 || c->channels[channelId] == 0) return 0;
    else {
        ChannelBuffer * buf = c->channels[channelId];
        int dataLen = buf->length - offset;
        int copyLen = dataLen <= length ? dataLen : length;
        char * tmp = (char *)calloc(length, sizeof(char));
        if (tmp == 0) return 0;
        memcpy(tmp, buf->data + buf->offset + offset, copyLen);
        return tmp;
    }
}

void multiplex_clear(Multiplex * c, int channelId) {
    if (c == 0 || c->channels[channelId] == 0) return;
    else {
        ChannelBuffer * buf = c->channels[channelId];
        buf->offset = 0;
        buf->length = 0;
        buf->newData = 0;
    }
}
