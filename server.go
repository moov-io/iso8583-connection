package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"github.com/moov-io/iso8583"
	"github.com/moov-io/iso8583/cmd/iso8583/describe"
	"github.com/moov-io/iso8583/network"
)

type Server struct {
	ln   net.Listener
	Addr string
	wg   sync.WaitGroup

	closeCh chan bool
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

			err = s.handleMessage(conn, packed)
			if err != nil {
				log.Printf("handling message: %v\n", err)
				// handle error here
				return
			}
		}
	}
}

func (s *Server) handleMessage(conn net.Conn, packed []byte) error {
	message := iso8583.NewMessage(brandSpec)
	err := message.Unpack(packed)
	if err != nil {
		return fmt.Errorf("unpacking message: %v", err)
	}

	describe.Message(os.Stdout, message)

	// for now, we just reply
	message.MTI("0810")

	return s.send(conn, message)
}

func (s *Server) send(conn net.Conn, message *iso8583.Message) error {
	var buf bytes.Buffer
	packed, err := message.Pack()
	if err != nil {
		return fmt.Errorf("packing message: %v", err)
	}

	// create header
	header := network.NewVMLHeader()
	header.SetLength(len(packed))

	_, err = header.WriteTo(&buf)
	if err != nil {
		return fmt.Errorf("writing message header: %v", err)
	}

	_, err = buf.Write(packed)
	if err != nil {
		return fmt.Errorf("writing packed message to buffer: %v", err)
	}

	_, err = conn.Write([]byte(buf.Bytes()))

	return err
}
