package test

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
)

const (
	CardForDelayedResponse string = "4200000000000000"
	CardForPingCounter     string = "4005550000000019"
)

// messageLengthReader reads message header from the r and returns message length
type messageLengthReader func(r io.Reader) (int, error)

// messageLengthWriter writes message header with encoded length into w
type messageLengthWriter func(w io.Writer, length int) (int, error)

// Server is a sandbox server for iso8583. Actually, it dreams to be a real sanbox.
type Server struct {
	ln   net.Listener
	Addr string
	wg   sync.WaitGroup

	closeCh chan bool

	// to protect following
	mutex sync.Mutex

	receivedPings int

	// spec that will be used to unpack received messages
	spec *iso8583.MessageSpec

	// readMessageLength is the function that reads message length header
	// from the connection, decodes and returns message length
	readMessageLength messageLengthReader

	// writeMessageLength is the function that encodes message length and
	// writes message length header into the connection
	writeMessageLength messageLengthWriter
}

func NewServer(spec *iso8583.MessageSpec, mlReader messageLengthReader, mlWriter messageLengthWriter) (*Server, error) {
	// automatically choose port
	ln, err := net.Listen("tcp", "127.0.0.1:")
	if err != nil {
		return nil, err
	}

	s := &Server{
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
	defer conn.Close()

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

			messageLength, err := s.readMessageLength(conn)
			if err != nil {
				var e *net.OpError
				if errors.As(err, &e) && e.Timeout() {
					continue ReadLoop
				} else if !errors.Is(err, io.EOF) { // if connection was not closed from the other side
					log.Printf("reading header: %v\n", err)
					return
				}
			}
			if messageLength == 0 {
				return
			}

			packed := make([]byte, messageLength)
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
	message := iso8583.NewMessage(s.spec)
	err := message.Unpack(packed)
	if err != nil {
		log.Printf("unpacking message: %v", err)
		return
	}

	mti, err := message.GetMTI()
	if err != nil {
		log.Printf("getting MTI: %v", err)
		return
	}

	// set mti function to response
	newMTI := mti[:2] + "1" + mti[3:]
	message.MTI(newMTI)

	// check if network management information code
	// was set to specific test case value
	f2 := message.GetField(2)
	if f2 != nil {
		code, err := f2.String()
		if err != nil {
			log.Printf("getting field 2: %v", err)
			return
		}

		switch code {
		case CardForDelayedResponse:
			// testing value to "sleep" for a 3 seconds
			time.Sleep(500 * time.Millisecond)

		case CardForPingCounter:
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
	_, err = s.writeMessageLength(&buf, len(packed))
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
