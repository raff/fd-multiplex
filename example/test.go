package main

import (
        "flag"
	"fmt"
	"log"
	"math/rand"
	"net"
	"sync"
	"time"

	"../go"
)

func random_sleep() {
	t := rand.Intn(1000000)
	time.Sleep(time.Duration(t) * time.Microsecond)
}

func receive_multichannel(m *multiplex.Multiplex) {
	for {
		selected, err := m.Select(2000 * time.Millisecond)
		if err == multiplex.CHANNEL_CLOSED {
			log.Println("receive_multichannel", "Select", "CLOSED")
			break
		}

		if err != nil {
			log.Println("receive_multichannel", "Select", err)
		} else {
			buffer := m.Dup(selected)
			log.Printf("[channel:%3d] %s\n", selected, buffer)
			m.Clear(selected)
			random_sleep()
		}
	}
}

func send_multichannel(m *multiplex.Multiplex) {
	for {
		ch := rand.Intn(multiplex.MAX_CHANNELS - 1)
		buffer := fmt.Sprintf("Hello on Channel %d.", ch)

		m.Send(uint(ch), []byte(buffer))
		random_sleep()
	}
}

func receive_echo(m *multiplex.Multiplex) {
	for {
		selected, err := m.Select(2000 * time.Millisecond)
		if err == multiplex.CHANNEL_CLOSED {
			log.Println("receive_echo", "Select", "CLOSED")
			break
		}

		if err != nil {
			log.Println("receive_echo", "Select", err)
		} else {
			buffer := m.Dup(selected)
			m.Clear(selected)

			m.Send(selected, buffer)
			random_sleep()
		}
	}
}

func send_receive(m *multiplex.Multiplex) {
	for {
		ch := rand.Intn(multiplex.MAX_CHANNELS - 1)
		message := fmt.Sprintf("Echo on Channel %d.", ch)

		s, err := m.Send(uint(ch), []byte(message))
                if err != nil {
                    log.Println("send_receive", err)
                }

                buffer := make([]byte, len(message))
                r, err := m.Receive(1000*time.Millisecond, uint(ch), buffer)
                if err != nil {
                    log.Println("send_receive", err)
                }

                log.Println("sent", s, "received", r, string(buffer))
		random_sleep()
	}
}

type Processor func(m *multiplex.Multiplex)

func listenAndServe(port string, processor Processor) {
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
                processor(m)
	}
}

func dialAndSend(port string, processor Processor) {
	c, err := net.Dial("tcp", port)
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	m := multiplex.NewMultiplex(c)
	m.EnableRange(0, multiplex.MAX_CHANNELS-1, 0)
	processor(m)
}

func main() {
	mode := flag.String("mode", "client-server", "client, server or client-server")
        run := flag.String("run", "multichannel", "test to run: multichannel, echo, ...")
	port := flag.String("port", "127.0.0.1:2222", "host:port to use")

	flag.Parse()

        send_processor := send_multichannel
        receive_processor := receive_multichannel

        if *run == "echo" {
            send_processor = send_receive
            receive_processor = receive_echo
        }

	var wg sync.WaitGroup

	wg.Add(2)

	if *mode != "client" { // server or client-server
		go func() {
			listenAndServe(*port, receive_processor)
			wg.Done()
		}()
	} else {
		wg.Add(-1)
	}

	if *mode != "server" { // client or client-server
		go func() {
			time.Sleep(1 * time.Second)
			dialAndSend(*port, send_processor)
			wg.Done()
		}()
	} else {
		wg.Add(-1)
	}

	wg.Wait()
}
