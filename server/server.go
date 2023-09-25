package server

import (
	"fmt"
	"net"
	"sync"

	"github.com/moov-io/iso8583"
	connection "github.com/moov-io/iso8583-connection"
)

// ConnectHandler is a function that will be called when new connection is
// established. Note, that this function will be called in a separate goroutine
// and the conn type is net.Conn, not *connection.Connection
type ConnectHandler func(conn net.Conn)

// ServerConnectionFactoryFunc is a function that creates new connection from
// net.Conn. This function is used to create new connection with custom options
// (e.g. custom message length reader/writer)
type ServerConnectionFactoryFunc func(conn net.Conn) (*connection.Connection, error)

var defaultServerConnectionFactory = func(spec *iso8583.MessageSpec, mlReader connection.MessageLengthReader, mlWriter connection.MessageLengthWriter, options ...connection.Option) (ServerConnectionFactoryFunc, error) {
	if spec == nil {
		return nil, fmt.Errorf("spec is required")
	}

	if mlReader == nil {
		return nil, fmt.Errorf("message length reader is required")
	}

	if mlWriter == nil {
		return nil, fmt.Errorf("message length writer is required")
	}

	return func(conn net.Conn) (*connection.Connection, error) {
		c, err := connection.NewFrom(conn, spec, mlReader, mlWriter, options...)
		if err != nil {
			return nil, fmt.Errorf("creating connection: %w", err)
		}

		return c, nil
	}, nil
}

// Server is a simple iso8583 server implementation currently used to test
// iso8583-client and most probably to be used for iso8583-test-harness
type Server struct {
	connectionOpts []connection.Option
	ln             net.Listener
	Addr           string
	wg             sync.WaitGroup

	closeCh chan bool

	// spec that will be used to unpack received messages
	spec *iso8583.MessageSpec

	// readMessageLength is the function that reads message length header
	// from the connection, decodes and returns message length
	readMessageLength connection.MessageLengthReader

	// writeMessageLength is the function that encodes message length and
	// writes message length header into the connection
	writeMessageLength connection.MessageLengthWriter

	mu              sync.Mutex
	connectHandlers []ConnectHandler
	isClosed        bool

	// connectionFactory is a function that creates new connection
	connectionFactory ServerConnectionFactoryFunc
}

func New(spec *iso8583.MessageSpec, mlReader connection.MessageLengthReader, mlWriter connection.MessageLengthWriter, connectionOpts ...connection.Option) *Server {
	// automatically choose port
	return &Server{
		connectionOpts:     connectionOpts,
		closeCh:            make(chan bool),
		spec:               spec,
		readMessageLength:  mlReader,
		writeMessageLength: mlWriter,
	}
}

func (s *Server) SetOptions(opts ...func(*Server) error) error {
	for _, opt := range opts {
		if err := opt(s); err != nil {
			return fmt.Errorf("setting server options: %w", err)
		}
	}

	return nil
}

func (s *Server) AddConnectionHandler(h ConnectHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connectHandlers = append(s.connectHandlers, h)
}

func (s *Server) Start(addr string) error {
	// create connection factory
	if s.connectionFactory == nil {
		connFactory, err := defaultServerConnectionFactory(s.spec, s.readMessageLength, s.writeMessageLength, s.connectionOpts...)
		if err != nil {
			return fmt.Errorf("creating default server connection factory: %w", err)
		}

		s.connectionFactory = connFactory
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	// Store address and listener information for later
	s.Addr = ln.Addr().String()
	s.ln = ln

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
					// TODO: better handle errors
					fmt.Printf("Accepting connection: %s\n", err.Error())
					return
				}
			}

			// check if server was closed
			select {
			case <-s.closeCh:
				return
			default:
				// continue handling the connection
			}

			s.wg.Add(1)
			go func() {
				// notify all handlers about new connection
				s.mu.Lock()
				if s.connectHandlers != nil && len(s.connectHandlers) > 0 {
					for _, h := range s.connectHandlers {
						go h(conn)
					}
				}
				s.mu.Unlock()

				err := s.handleConnection(conn)
				if err != nil {
					fmt.Printf("Error handling connection: %s\n", err.Error())
				}
				s.wg.Done()
			}()
		}
	}()

	return nil
}

func (s *Server) Close() {
	s.mu.Lock()
	if s.isClosed {
		s.mu.Unlock()
		return
	}

	close(s.closeCh)

	if s.ln != nil {
		s.ln.Close()
	}

	s.isClosed = true
	s.mu.Unlock()

	s.wg.Wait()
}

func (s *Server) handleConnection(conn net.Conn) error {
	c, err := s.connectionFactory(conn)
	if err != nil {
		return fmt.Errorf("creating connection using factory: %w", err)
	}

	select {
	case <-s.closeCh:
		// if server was closed, close the client
		c.Close()
	case <-c.Done():
		// if client was closed (because of error or some internal action)
		// we just return
	}

	return nil
}
