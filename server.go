package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"github.com/moov-io/iso8583"
	"github.com/moov-io/iso8583/network"
)

type Server struct {
	ln   net.Listener
	Addr string
	wg   sync.WaitGroup

	closeCh chan bool

	// to protect following
	mutex sync.Mutex

	receivedPings int
}

func NewServer() (*Server, error) {
	// automatically choose port
	ln, err := net.Listen("tcp", "127.0.0.1:")
	if err != nil {
		return nil, err
	}

	s := &Server{
		ln:      ln,
		Addr:    ln.Addr().String(),
		closeCh: make(chan bool),
	}

	s.wg.Add(1)
	go func() {
		defer func() {
			s.wg.Done()
		}()

		for {
			conn, err := ln.Accept()
			if err != nil {
				// did we close the server?
				select {
				case <-s.closeCh:
					return
				default:
					fmt.Printf("Error accepting connection: %v\n", err)
					return
				}
			}

			s.wg.Add(1)
			go func() {
				s.handleConnection(conn)
				s.wg.Done()
			}()
		}
	}()

	return s, nil
}

func (s *Server) Close() {
	close(s.closeCh)
	s.ln.Close()
	s.wg.Wait()
}

func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()

	// buf := make([]byte, 2048)
ReadLoop:
	for {
		select {
		case <-s.closeCh:
			return
		default:
			// we set a 200ms timeout on the read just to break the select
			// in order to be able to check closedCh channel regularly
			// the approach is described here
			// https://eli.thegreenplace.net/2020/graceful-shutdown-of-a-tcp-server-in-go/
			conn.SetDeadline(time.Now().Add(200 * time.Millisecond))

			header := network.NewVMLHeader()
			n, err := header.ReadFrom(conn)
			if err != nil {
				var e *net.OpError
				if errors.As(err, &e) && e.Timeout() {
					continue ReadLoop
				} else if !errors.Is(err, io.EOF) { // if connection was not closed from the other side
					log.Printf("reading header: %v\n", err)
					return
				}
			}
			if n == 0 {
				return
			}

			packed := make([]byte, header.Length())
			_, err = conn.Read(packed)
			if err != nil {
				log.Printf("reading message: %v\n", err)
				return
			}

			go s.handleMessage(conn, packed)
		}
	}
}

func (s *Server) handleMessage(conn net.Conn, packed []byte) {
	message := iso8583.NewMessage(brandSpec)
	err := message.Unpack(packed)
	if err != nil {
		log.Printf("unpacking message: %v", err)
		return
	}

	// We can handle different test cases here
	// for now, we just reply
	message.MTI("0810")

	// check if network management information code
	// was set to specific test case value
	f70 := message.GetField(70)
	if f70 != nil {
		code, err := f70.String()
		if err != nil {
			log.Printf("getting field 70: %v", err)
			return
		}

		switch code {
		case "777":
			// testing value to "sleep" for a 3 seconds
			time.Sleep(500 * time.Millisecond)

		case "371":
			// ping request received
			s.mutex.Lock()
			s.receivedPings++
			s.mutex.Unlock()
		}
	}

	s.send(conn, message)
}

func (s *Server) send(conn net.Conn, message *iso8583.Message) {
	var buf bytes.Buffer
	packed, err := message.Pack()
	if err != nil {
		log.Printf("packing message: %v", err)
	}

	// create header
	header := network.NewVMLHeader()
	header.SetLength(len(packed))

	_, err = header.WriteTo(&buf)
	if err != nil {
		log.Printf("writing message header: %v", err)
	}

	_, err = buf.Write(packed)
	if err != nil {
		log.Printf("writing packed message to buffer: %v", err)
	}

	_, err = conn.Write([]byte(buf.Bytes()))
	if err != nil {
		log.Printf("writing buffer into the socket: %v", err)
	}
}

func (s *Server) RecivedPings() int {
	var pings int
	s.mutex.Lock()
	pings = s.receivedPings
	s.mutex.Unlock()

	return pings
}
