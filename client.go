package client

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
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

// Client represents an ISO 8583 Client. Client may be used
// by multiple goroutines simultaneously.
type Client struct {
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
	respMap           map[string]chan *iso8583.Message

	// WaitGroup to wait for all Send calls to finish
	wg sync.WaitGroup

	// to protect following: closing, STAN
	mutex sync.Mutex

	// user has called Close
	closing bool
}

func NewClient(addr string, spec *iso8583.MessageSpec, mlReader MessageLengthReader, mlWriter MessageLengthWriter, options ...Option) (*Client, error) {
	opts := GetDefaultOptions()
	for _, opt := range options {
		if err := opt(&opts); err != nil {
			return nil, fmt.Errorf("setting client option: %v %w", opt, err)
		}
	}

	return &Client{
		addr:               addr,
		Opts:               opts,
		requestsCh:         make(chan request),
		done:               make(chan struct{}),
		respMap:            make(map[string]chan *iso8583.Message),
		spec:               spec,
		readMessageLength:  mlReader,
		writeMessageLength: mlWriter,
	}, nil
}

// NewClientWithConn - in addition to NewClient args, it accepts conn which
// will be used insde client. Returned client is ready to be used for message
// sending and receiving
func NewClientWithConn(conn io.ReadWriteCloser, spec *iso8583.MessageSpec, mlReader MessageLengthReader, mlWriter MessageLengthWriter, options ...Option) (*Client, error) {
	c, err := NewClient("", spec, mlReader, mlWriter, options...)
	if err != nil {
		return nil, fmt.Errorf("creating client: %w", err)
	}
	c.conn = conn
	c.run()
	return c, nil
}

// SetOptions - sets client options
func (c *Client) SetOptions(options ...Option) error {
	for _, opt := range options {
		if err := opt(&c.Opts); err != nil {
			return fmt.Errorf("setting client option: %v %w", opt, err)
		}
	}

	return nil
}

// Connect connects client to the server by provided addr
func (c *Client) Connect() error {
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

// run starts read and write loops in goroutines
func (c *Client) run() {
	go c.writeLoop()
	go c.readLoop()
}

// Close waits for pending requests to complete and then closes network
// connection with ISO 8583 server
func (c *Client) Close() error {
	c.mutex.Lock()
	// if we are closing already, just return
	if c.closing {
		c.mutex.Unlock()
		return nil
	}
	c.closing = true
	c.mutex.Unlock()

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

func (c *Client) Done() <-chan struct{} {
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

// Send sends message and waits for the response
func (c *Client) Send(message *iso8583.Message) (*iso8583.Message, error) {
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
	}

	c.pendingRequestsMu.Lock()
	delete(c.respMap, req.requestID)
	c.pendingRequestsMu.Unlock()

	return resp, err
}

// Reply sends the message and does not wait for a reply to be received
// any reaply received for message send using Reply will be handled with
// unmatchedMessageHandler
func (c *Client) Reply(message *iso8583.Message) error {
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

// requestID is a unique identifier for a request.
// responses from the server are not guranteed to return in order so we must
// have an id to reference the original req. built from stan and datetime
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

// writeLoop reads requests from the channel and writes request message into
// the socket connection. It also sends message when idle time passes
func (c *Client) writeLoop() {
	for {
		select {
		case req := <-c.requestsCh:
			// if it's a request message, not a response
			if req.replyCh != nil {
				c.pendingRequestsMu.Lock()
				c.respMap[req.requestID] = req.replyCh
				c.pendingRequestsMu.Unlock()
			}

			_, err := c.conn.Write([]byte(req.rawMessage))
			if err != nil {
				req.errCh <- err

				// passed message to errCh, we can break from this switch
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
	// TODO: handle write error: reconnect, re-try?, etc.
}

// readLoop reads data from the socket (message length header and raw message)
// and runs a goroutine to handle the message
func (c *Client) readLoop() {
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

	// lock to check `closing`
	c.mutex.Lock()
	// if we receive error and we are closing connection, we have to set
	if err != nil && !c.closing {
		if errors.Is(err, io.EOF) {
			// TODO handle erorrs better
			// we can hanlde connection closed somehow (reconnect?)
			// fmt.Fprintln(os.Stderr, "connection closed")
		} else {
			fmt.Fprintln(os.Stderr, "reading from socket:", err)
		}
	}
	c.mutex.Unlock()

}

// handleResponse unpacks the message and then sends it to the reply channel
// that corresponds to the message ID (request ID)
func (c *Client) handleResponse(rawMessage []byte) {
	// create message
	message := iso8583.NewMessage(c.spec)
	err := message.Unpack(rawMessage)
	if err != nil {
		log.Printf("unpacking message: %v", err)
		return
	}

	reqID, err := requestID(message)
	if err != nil {
		log.Printf("creating request ID: %v", err)
		return
	}

	// send response message to the reply channel
	c.pendingRequestsMu.Lock()
	if replyCh, found := c.respMap[reqID]; found {
		replyCh <- message
	} else if c.Opts.UnmatchedMessageHandler != nil {
		go c.Opts.UnmatchedMessageHandler(c, message)
	} else {
		log.Printf("can't find request for ID: %s", reqID)
	}
	c.pendingRequestsMu.Unlock()
}
