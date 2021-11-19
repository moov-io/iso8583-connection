package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"
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

	buf := make([]byte, 2048)
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
			n, err := conn.Read(buf)
			if err != nil {
				if opErr, ok := err.(*net.OpError); ok && opErr.Timeout() {
					continue ReadLoop
				} else if err != io.EOF {
					log.Println("read error", err)
					return
				}
			}
			if n == 0 {
				return
			}

			response := fmt.Sprintf("%s pong\n", string(buf[:n]))
			conn.Write([]byte(response))
			log.Printf("Response: %s", response)
		}
	}
}
