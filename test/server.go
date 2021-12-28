package test

import (
	"fmt"
	"net"
	"sync"

	"github.com/moov-io/iso8583"
	client "github.com/moovfinancial/iso8583-client"
)

// Server is a sandbox server for iso8583. Actually, it dreams to be a real sanbox.
type Server struct {
	clientOpts []client.Option
	ln         net.Listener
	Addr       string
	wg         sync.WaitGroup

	closeCh chan bool

	// spec that will be used to unpack received messages
	spec *iso8583.MessageSpec

	// readMessageLength is the function that reads message length header
	// from the connection, decodes and returns message length
	readMessageLength client.MessageLengthReader

	// writeMessageLength is the function that encodes message length and
	// writes message length header into the connection
	writeMessageLength client.MessageLengthWriter
}

func NewServer(spec *iso8583.MessageSpec, mlReader client.MessageLengthReader, mlWriter client.MessageLengthWriter, clientOpts ...client.Option) (*Server, error) {
	// automatically choose port
	ln, err := net.Listen("tcp", "127.0.0.1:")
	if err != nil {
		return nil, err
	}

	s := &Server{
		clientOpts:         clientOpts,
		ln:                 ln,
		Addr:               ln.Addr().String(),
		closeCh:            make(chan bool),
		spec:               spec,
		readMessageLength:  mlReader,
		writeMessageLength: mlWriter,
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
	c := client.NewClientWithConn(conn, s.spec, s.readMessageLength, s.writeMessageLength, s.clientOpts...)

	select {
	case <-s.closeCh:
		// if server was closed, close the client
		c.Close()
	case <-c.Done():
		// if client was closed (because of error or some internal action)
		// we just return
	}
}
