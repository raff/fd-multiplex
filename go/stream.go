package multiplex

import (
	"log"
	"net"
	"time"
)

var (
	NO_DEADLINE   time.Time
	LOOP_INTERVAL = 1 * time.Second // the timeout/interval for RunLoop select.
)

type StreamError MultiplexError

func (e StreamError) Error() string {
	return string(e)
}

func (e StreamError) Temporary() bool {
	return MultiplexError(e) != CHANNEL_CLOSED
}

func (e StreamError) Timeout() bool {
	return MultiplexError(e) == CHANNEL_TIMEOUT
}

/*
 * Stream implements the net.Conn interface on top of a multiplexed channel
 */
type Stream struct {
	*Multiplex               // the underlying multiplexor
	ch             uint      // the selected channel
	read_deadline  time.Time // current read timeout
}

func NewStream(m *Multiplex, channelId uint) *Stream {
	if channelId < MAX_CHANNELS {
		return &Stream{m, channelId, NO_DEADLINE}
	} else {
		return nil
	}
}

func (s *Stream) Read(b []byte) (int, error) {
	if false {
		return s.Receive(time.Second, s.ch, b) // time.Second should be a real timeout
	} else {
		for {
			n, err := s.Multiplex.Read(s.ch, b)
			if err != nil {
				log.Println("Stream.Read", s.ch, n, err)
				return 0, err
			}

			if n == 0 {
				if !s.read_deadline.IsZero() && time.Now().After(s.read_deadline) {
					return 0, CHANNEL_TIMEOUT
				}

				time.Sleep(time.Duration(1))
			} else {
				return n, nil
			}
		}

		return 0, nil
	}
}

func (s *Stream) Write(b []byte) (int, error) {
	return s.Send(s.ch, b)
}

func (s *Stream) Close() error {
	s.Disable(s.ch)
	return nil
}

func (s *Stream) LocalAddr() net.Addr {
	return s.conn.LocalAddr()
}

func (s *Stream) RemoteAddr() net.Addr {
	return s.conn.RemoteAddr()
}

func (s *Stream) SetDeadline(t time.Time) error {
	s.SetReadDeadline(t)
	s.SetWriteDeadline(t)
	return nil
}

func (s *Stream) SetReadDeadline(t time.Time) error {
	s.read_deadline = t
	return nil
}

func (s *Stream) SetWriteDeadline(t time.Time) error {
        // since we write directly, we can just set the connection write deadline
	return s.conn.SetWriteDeadline(t)
}

func (m *Multiplex) RunLoop() {
	for {
		if selected, err := m.Select(LOOP_INTERVAL); err == CHANNEL_CLOSED {
			log.Println("RunLoop", "connection closed")
			break
		} else if err != nil {
			log.Println("RunLoop", err)
		} else {
			log.Println("RunLoop", "selected", selected)
		}

		time.Sleep(time.Duration(1))
	}
}
