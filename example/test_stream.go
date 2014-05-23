package main

import (
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net"
	"strings"
	"time"

	"../go"
)

const (
	MAX_CONN = 64
)

var (
	WRITE_TIMEOUT = 5 * time.Second
)

func random_sleep() {
	t := rand.Intn(1000000)
	time.Sleep(time.Duration(t) * time.Microsecond)
}

func receive_echo(m *multiplex.Multiplex, channelId uint) {
	log.Println("receive_echo for", channelId)

	stream := multiplex.NewStream(m, channelId)
	buffer := make([]byte, 1024)

	for {
		log.Println("receive_echo", channelId, "reading...")

		if n, err := stream.Read(buffer); err == multiplex.CHANNEL_CLOSED {
			log.Println("receive_echo", channelId, "Read", "CLOSED")
			break
		} else if err != nil {
			log.Println("receive_echo", channelId, "Read", err)
		} else {
			log.Println("receive_echo", channelId, string(buffer[:n]))
			stream.SetWriteDeadline(time.Now().Add(WRITE_TIMEOUT))
			stream.Write(buffer[:n])
		}

		random_sleep()
	}

	log.Println("receive_echo", channelId, "Terminated")
}

func send_echo(m *multiplex.Multiplex, channelId uint) {
	log.Println("send_echo for", channelId)

	stream := multiplex.NewStream(m, channelId)

	for {
		log.Println("send_echo", channelId, "writing...")

		message := fmt.Sprintf("Echo on Channel %d "+
			"the quick brown fox jumps over the lazy dog. ", channelId)
		message = strings.Repeat(message, rand.Intn(1000))

		stream.SetWriteDeadline(time.Now().Add(WRITE_TIMEOUT))
		if s, err := stream.Write([]byte(message)); err == multiplex.CHANNEL_CLOSED {
			log.Println("send_echo", channelId, "Write", "CLOSED")
		} else if err != nil {
			log.Println("send_echo", channelId, "Write", err)
		} else {
			buffer := make([]byte, len(message))
			r, err := stream.Read(buffer)
			if err != nil {
				log.Println("send_echo", channelId, "Read", err)
			} else {
				log.Println("send_echo", channelId, "sent", s, "received", r, string(buffer[:r]))
			}
		}

		random_sleep()
	}

	log.Println("send_echo", channelId, "Terminated")
}

func send_receive(m *multiplex.Multiplex) {
	for {
		ch := rand.Intn(MAX_CONN)
		message := strings.Repeat("the quick brown fox jumps over the lazy dog ", rand.Intn(1000))
		message = fmt.Sprintf("Echo on Channel %d %s.", ch, message)

		s, err := m.Send(uint(ch), []byte(message))
		if err != nil {
			log.Println("send_receive", "Send", err)
		}

		buffer := make([]byte, len(message))
		r, err := m.Receive(time.Duration(10000)*time.Millisecond, uint(ch), buffer)
		if err != nil {
			log.Println("send_receive", "Receive", err)
		} else {
			log.Println("send_receive", ch, "sent", s, "received", r, string(buffer[:r]))
		}
		random_sleep()
	}
}

type Processor func(m *multiplex.Multiplex)

func listenAndServe(port string) {
	l, err := net.Listen("tcp", port)
	if err != nil {
		log.Fatal(err)
	}
	defer l.Close()
	for {
		// Wait for a connection.
		conn, err := l.Accept()
		if err != nil {
			log.Fatal(err)
		}

		m := multiplex.NewMultiplex(conn)
		m.EnableRange(0, MAX_CONN-1, 0)

		go m.RunLoop()

		for i := 0; i < MAX_CONN; i++ {
			go receive_echo(m, uint(i))
		}
	}
}

func dialAndSend(port string) {
	c, err := net.Dial("tcp", port)
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	m := multiplex.NewMultiplex(c)
	m.EnableRange(0, MAX_CONN-1, 0)

	if false {
		send_receive(m)
	} else {
		for i := 0; i < MAX_CONN; i++ {
			go send_echo(m, uint(i))
		}

		m.RunLoop()
	}
}

func main() {
	port := flag.String("port", "127.0.0.1:2222", "host:port to use")

	flag.Parse()

	go listenAndServe(*port)
	time.Sleep(1 * time.Second)
	dialAndSend(*port)
}
