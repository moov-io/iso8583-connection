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

	closedCh chan bool
}

func NewServer() (*Server, error) {
	// automatically choose port
	ln, err := net.Listen("tcp", "127.0.0.1:")
	if err != nil {
		return nil, err
	}

	s := &Server{
		ln:       ln,
		Addr:     ln.Addr().String(),
		closedCh: make(chan bool),
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
				case <-s.closedCh:
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
	close(s.closedCh)
	s.ln.Close()
	s.wg.Wait()
}

// source: https://eli.thegreenplace.net/2020/graceful-shutdown-of-a-tcp-server-in-go/
func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()

	buf := make([]byte, 2048)
ReadLoop:
	for {
		select {
		case <-s.closedCh:
			return
		default:
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
			log.Printf("%s", response)
		}
	}
}
