package main

import (
	"fmt"
	"log"
	"math/rand"
	"net"
	"time"

	"../go"
)

func random_sleep() {
	t := rand.Intn(1000000)
	time.Sleep(time.Duration(t) * time.Microsecond)
}

func receive_on_channel(m *multiplex.Multiplex) {
	for {
		selected, err := m.Select(2000 * time.Millisecond)
		if err == multiplex.CHANNEL_CLOSED {
			log.Println("receive_on_channel", "Select", "CLOSED")
			break
		}

		if err != nil {
			log.Println("receive_on_channel", "Select", err)
		} else {
			buffer := m.Dup(selected)
			log.Printf("[channel:%3d] %s\n", selected, buffer)
			m.Clear(selected)
			random_sleep()
		}
	}
}

func send_on_channel(m *multiplex.Multiplex) {
	for {
		ch := rand.Intn(multiplex.MAX_CHANNELS - 1)
		buffer := fmt.Sprintf("Hello on Channel %d.", ch)

		m.Send(uint(ch), []byte(buffer))
		random_sleep()
	}
}

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
		m.EnableRange(0, multiplex.MAX_CHANNELS-1, 0)
		receive_on_channel(m)
	}
}

func dialAndSend(port string) {
	c, err := net.Dial("tcp", port)
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	m := multiplex.NewMultiplex(c)
	m.EnableRange(0, multiplex.MAX_CHANNELS-1, 0)

	send_on_channel(m)
}

func main() {
	port := ":2222"

	go listenAndServe(port)
	time.Sleep(1 * time.Second)
	dialAndSend(port)
}
