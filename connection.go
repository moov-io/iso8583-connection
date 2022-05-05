package connection

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"github.com/moov-io/iso8583"
)

var (
	ErrConnectionClosed = errors.New("connection closed")
	ErrSendTimeout      = errors.New("message send timeout")
)

const DefaultTransmissionDateTimeFormat string = "0102150405" // YYMMDDhhmmss

// MessageLengthReader reads message header from the r and returns message length
type MessageLengthReader func(r io.Reader) (int, error)

// MessageLengthWriter writes message header with encoded length into w
type MessageLengthWriter func(w io.Writer, length int) (int, error)

// Connection represents an ISO 8583 Connection. Connection may be used
// by multiple goroutines simultaneously.
type Connection struct {
	addr       string
	Opts       Options
	conn       io.ReadWriteCloser
	requestsCh chan request
	done       chan struct{}

	// spec that will be used to unpack received messages
	spec *iso8583.MessageSpec

	// readMessageLength is the function that reads message length header
	// from the connection, decodes and returns message length
	readMessageLength MessageLengthReader

	// writeMessageLength is the function that encodes message length and
	// writes message length header into the connection
	writeMessageLength MessageLengthWriter

	pendingRequestsMu sync.Mutex
	respMap           map[string]response

	// WaitGroup to wait for all Send calls to finish
	wg sync.WaitGroup

	// to protect following: closing, STAN
	mutex sync.Mutex

	// user has called Close
	closing bool
}

// New creates and configures Connection. To establish network connection, call `Connect()`.
func New(addr string, spec *iso8583.MessageSpec, mlReader MessageLengthReader, mlWriter MessageLengthWriter, options ...Option) (*Connection, error) {
	opts := GetDefaultOptions()
	for _, opt := range options {
		if err := opt(&opts); err != nil {
			return nil, fmt.Errorf("setting client option: %v %w", opt, err)
		}
	}

	return &Connection{
		addr:               addr,
		Opts:               opts,
		requestsCh:         make(chan request),
		done:               make(chan struct{}),
		respMap:            make(map[string]response),
		spec:               spec,
		readMessageLength:  mlReader,
		writeMessageLength: mlWriter,
	}, nil
}

// NewFrom accepts conn (net.Conn, or any io.ReadWriteCloser) which will be
// used as a transport for the returned Connection. Returned Connection is
// ready to be used for message sending and receiving
func NewFrom(conn io.ReadWriteCloser, spec *iso8583.MessageSpec, mlReader MessageLengthReader, mlWriter MessageLengthWriter, options ...Option) (*Connection, error) {
	c, err := New("", spec, mlReader, mlWriter, options...)
	if err != nil {
		return nil, fmt.Errorf("creating client: %w", err)
	}
	c.conn = conn
	c.run()
	return c, nil
}

// SetOptions sets connection options
func (c *Connection) SetOptions(options ...Option) error {
	for _, opt := range options {
		if err := opt(&c.Opts); err != nil {
			return fmt.Errorf("setting client option: %v %w", opt, err)
		}
	}

	return nil
}

// Connect establishes a connection to the server using configured address and should be used when the implementing
// party is responsible for establishing the connection.
func (c *Connection) Connect() error {
	var conn net.Conn
	var err error

	if c.conn != nil {
		c.run()
		return nil
	}

	if c.Opts.TLSConfig != nil {
		conn, err = tls.Dial("tcp", c.addr, c.Opts.TLSConfig)
	} else {
		conn, err = net.Dial("tcp", c.addr)
	}

	if err != nil {
		return fmt.Errorf("connecting to server %s: %w", c.addr, err)
	}

	c.conn = conn

	c.run()

	return nil
}

// Accept accepts a connection to the server using the configured address and should be used when the other party
// is responsible for establishing the connection.
func (c *Connection) Accept() error {
	listener, err := net.Listen("tcp", c.addr)
	if err != nil {
		return fmt.Errorf("creating listener %s: %w", c.addr, err)
	}

	c.wg.Add(1)
	go func() {
		defer func() {
			c.wg.Done()
		}()

		chConn := make(chan net.Conn, 1)
		chErr := make(chan error, 1)

		go func() {
			conn, err := listener.Accept()
			if err != nil {
				chErr <- err
			}
			chConn <- conn
		}()

		select {
		case conn := <- chConn:
			c.conn = conn
			c.run()

			if c.Opts.ConnectionAcceptHandler != nil {
				go c.Opts.ConnectionAcceptHandler(c, nil)
			}
		case <-time.After(c.Opts.ConnectTimeout):
			msg := "timed out waiting for connection"
			if c.Opts.ConnectionAcceptHandler != nil {
				go c.Opts.ConnectionAcceptHandler(nil, errors.New(msg))
			}
			fmt.Print(msg)
		case <- chErr:
			msg := fmt.Sprintf("accepting server connection %s: %v", c.addr, err)
			if c.Opts.ConnectionAcceptHandler != nil {
				go c.Opts.ConnectionAcceptHandler(nil, errors.New(msg))
			}
			fmt.Print(msg)
		}
	}()

	return nil
}

// run starts read and write loops in goroutines
func (c *Connection) run() {
	go c.writeLoop()
	go c.readLoop()
}

func (c *Connection) handleConnectionError(err error) {
	// lock to check and update `closing`
	c.mutex.Lock()
	if err == nil || c.closing {
		c.mutex.Unlock()
		return
	}

	c.closing = true
	c.mutex.Unlock()

	// channel to wait for all goroutines to exit
	done := make(chan bool)

	c.pendingRequestsMu.Lock()
	for _, resp := range c.respMap {
		resp.errCh <- ErrConnectionClosed
	}
	c.pendingRequestsMu.Unlock()

	// return error to all Send methods
	go func() {
		for {
			select {
			case req := <-c.requestsCh:
				req.errCh <- ErrConnectionClosed
			case <-done:
				return
			}

		}
	}()

	go func() {
		c.wg.Wait()
		done <- true
	}()

	// close everything else we close normally
	c.close()

	if c.Opts.ConnectionClosedHandler != nil {
		go c.Opts.ConnectionClosedHandler(c)
	}
}

func (c *Connection) close() error {
	// wait for all requests to complete before closing the connection
	c.wg.Wait()

	close(c.done)

	if c.conn != nil {
		err := c.conn.Close()
		if err != nil {
			return fmt.Errorf("closing connection: %w", err)
		}
	}

	return nil
}

// Close waits for pending requests to complete and then closes network
// connection with ISO 8583 server
func (c *Connection) Close() error {
	c.mutex.Lock()
	// if we are closing already, just return
	if c.closing {
		c.mutex.Unlock()
		return nil
	}
	c.closing = true
	c.mutex.Unlock()

	return c.close()
}

func (c *Connection) Done() <-chan struct{} {
	return c.done
}

// request represents request to the ISO 8583 server
type request struct {
	// includes length header and message itself
	rawMessage []byte

	// ID of the request (based on STAN, RRN, etc.)
	requestID string

	// channel to receive reply from the server
	replyCh chan *iso8583.Message

	// channel to receive error that may happen down the road
	errCh chan error
}

type response struct {
	// channel to receive reply from the server
	replyCh chan *iso8583.Message

	// channel to receive error that may happen down the road
	errCh chan error
}

// Send sends message and waits for the response
func (c *Connection) Send(message *iso8583.Message) (*iso8583.Message, error) {
	c.wg.Add(1)
	defer c.wg.Done()

	c.mutex.Lock()
	if c.closing {
		c.mutex.Unlock()
		return nil, ErrConnectionClosed
	}
	c.mutex.Unlock()

	var buf bytes.Buffer
	packed, err := message.Pack()
	if err != nil {
		return nil, fmt.Errorf("packing message: %w", err)
	}

	// create header
	_, err = c.writeMessageLength(&buf, len(packed))
	if err != nil {
		return nil, fmt.Errorf("writing message header to buffer: %w", err)
	}

	_, err = buf.Write(packed)
	if err != nil {
		return nil, fmt.Errorf("writing packed message to buffer: %w", err)
	}

	// prepare request
	reqID, err := requestID(message)
	if err != nil {
		return nil, fmt.Errorf("creating request ID: %w", err)
	}

	req := request{
		rawMessage: buf.Bytes(),
		requestID:  reqID,
		replyCh:    make(chan *iso8583.Message),
		errCh:      make(chan error),
	}

	var resp *iso8583.Message

	c.requestsCh <- req

	select {
	case resp = <-req.replyCh:
	case err = <-req.errCh:
	case <-time.After(c.Opts.SendTimeout):
		err = ErrSendTimeout
		// reply can still be sent after SendTimeout received.
		// if we have UnmatchedMessageHandler set, then we want reply
		// to not be lost but handled by it.
		if c.Opts.InboundMessageHandler != nil {
			go func() {
				select {
				case resp := <-req.replyCh:
					go c.Opts.InboundMessageHandler(c, resp)
				case <-time.After(1 * time.Second):
					// if no reply received within 1 second
					// we return from the goroutine
					return
				}
			}()
		}
	}

	c.pendingRequestsMu.Lock()
	delete(c.respMap, req.requestID)
	c.pendingRequestsMu.Unlock()

	return resp, err
}

// Reply sends the message and does not wait for a reply to be received
// any reaply received for message send using Reply will be handled with
// unmatchedMessageHandler
func (c *Connection) Reply(message *iso8583.Message) error {
	c.wg.Add(1)
	defer c.wg.Done()

	c.mutex.Lock()
	if c.closing {
		c.mutex.Unlock()
		return ErrConnectionClosed
	}
	c.mutex.Unlock()

	// prepare message for sending
	var buf bytes.Buffer
	packed, err := message.Pack()
	if err != nil {
		return fmt.Errorf("packing message: %w", err)
	}

	// create header
	_, err = c.writeMessageLength(&buf, len(packed))
	if err != nil {
		return fmt.Errorf("writing message header to buffer: %w", err)
	}

	_, err = buf.Write(packed)
	if err != nil {
		return fmt.Errorf("writing packed message to buffer: %w", err)
	}

	req := request{
		rawMessage: buf.Bytes(),
		errCh:      make(chan error),
	}

	c.requestsCh <- req

	select {
	case err = <-req.errCh:
	case <-time.After(c.Opts.SendTimeout):
		err = ErrSendTimeout
	}

	return err
}

// requestID is a unique identifier for a request.  responses from the server
// are not guaranteed to return in order so we must have an id to reference the
// original req. built from stan and datetime
func requestID(message *iso8583.Message) (string, error) {
	if message == nil {
		return "", fmt.Errorf("message required")
	}

	stan, err := message.GetString(11)
	if err != nil {
		return "", fmt.Errorf("getting STAN (field 11) of the message: %w", err)
	}

	if stan == "" {
		return "", errors.New("STAN is missing")
	}

	return stan, nil
}

const (
	// position of the MTI specifies the message function which
	// defines how the message should flow within the system.
	messageFunctionIndex = 2

	// following are responses to our requests
	messageFunctionRequestResponse            = "1"
	messageFunctionAdviceResponse             = "3"
	messageFunctionNotificationAcknowledgment = "5"
	messageFunctionInstructionAcknowledgment  = "7"
)

func isResponse(message *iso8583.Message) bool {
	if message == nil {
		return false
	}

	mti, _ := message.GetMTI()

	if len(mti) < 4 {
		return false
	}

	messageFunction := string(mti[messageFunctionIndex])

	switch messageFunction {
	case messageFunctionRequestResponse,
		messageFunctionAdviceResponse,
		messageFunctionNotificationAcknowledgment,
		messageFunctionInstructionAcknowledgment:
		return true
	}

	return false
}

// writeLoop reads requests from the channel and writes request message into
// the socket connection. It also sends message when idle time passes
func (c *Connection) writeLoop() {
	var err error

	for err == nil {
		select {
		case req := <-c.requestsCh:
			// if it's a request message, not a response
			if req.replyCh != nil {
				c.pendingRequestsMu.Lock()
				c.respMap[req.requestID] = response{
					replyCh: req.replyCh,
					errCh:   req.errCh,
				}
				c.pendingRequestsMu.Unlock()
			}

			_, err = c.conn.Write([]byte(req.rawMessage))
			if err != nil {
				break
			}

			// for replies (requests without replyCh) we just
			// return nil to errCh as caller is waiting for error
			// or send timeout. Regular requests waits for responses
			// to be received to their replyCh channel.
			if req.replyCh == nil {
				req.errCh <- nil
			}
		case <-time.After(c.Opts.IdleTime):
			// if no message was sent during idle time, we have to send ping message
			if c.Opts.PingHandler != nil {
				go c.Opts.PingHandler(c)
			}
		case <-c.done:
			return
		}

	}

	c.handleConnectionError(err)
}

// readLoop reads data from the socket (message length header and raw message)
// and runs a goroutine to handle the message
func (c *Connection) readLoop() {
	var err error
	var messageLength int

	r := bufio.NewReader(c.conn)
	for {
		messageLength, err = c.readMessageLength(r)
		if err != nil {
			break
		}

		// read the packed message
		rawMessage := make([]byte, messageLength)
		_, err = io.ReadFull(r, rawMessage)
		if err != nil {
			break
		}

		go c.handleResponse(rawMessage)
	}

	c.handleConnectionError(err)
}

// handleResponse unpacks the message and then sends it to the reply channel
// that corresponds to the message ID (request ID)
func (c *Connection) handleResponse(rawMessage []byte) {
	// create message
	message := iso8583.NewMessage(c.spec)
	err := message.Unpack(rawMessage)
	if err != nil {
		log.Printf("unpacking message: %v", err)
		return
	}

	if isResponse(message) {
		reqID, err := requestID(message)
		if err != nil {
			log.Printf("creating request ID: %v", err)
			return
		}

		// send response message to the reply channel
		c.pendingRequestsMu.Lock()
		response, found := c.respMap[reqID]
		c.pendingRequestsMu.Unlock()

		if found {
			response.replyCh <- message
		} else if c.Opts.InboundMessageHandler != nil {
			go c.Opts.InboundMessageHandler(c, message)
		} else {
			log.Printf("can't find request for ID: %s", reqID)
		}
	} else {
		if c.Opts.InboundMessageHandler != nil {
			go c.Opts.InboundMessageHandler(c, message)
		}
	}
}
