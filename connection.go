package connection

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/moov-io/iso8583"
	"github.com/moov-io/iso8583/utils"
)

var (
	ErrConnectionClosed = errors.New("connection closed")
	ErrSendTimeout      = errors.New("message send timeout")
)

// MessageLengthReader reads message header from the r and returns message length
type MessageLengthReader func(r io.Reader) (int, error)

// MessageLengthWriter writes message header with encoded length into w
type MessageLengthWriter func(w io.Writer, length int) (int, error)

// ConnectionStatus
type Status string

const (
	// StatusOnline means connection is online
	StatusOnline Status = "online"

	// StatusOffline means connection is offline
	StatusOffline Status = "offline"

	// StatusUnknown means connection status is unknown (not set)
	StatusUnknown Status = ""
)

// directWrite is used to write data directly to the connection
type directWrite struct {
	data  []byte
	errCh chan error
}

// Connection represents an ISO 8583 Connection. Connection may be used
// by multiple goroutines simultaneously.
type Connection struct {
	addr           string
	Opts           Options
	conn           io.ReadWriteCloser
	requestsCh     chan request
	readResponseCh chan *iso8583.Message
	directWriteCh  chan directWrite
	done           chan struct{}

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

	// to protect following: closing, status
	mutex sync.Mutex

	// user has called Close
	closing bool

	// connection status
	status Status
}

var _ io.Writer = (*Connection)(nil)

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
		readResponseCh:     make(chan *iso8583.Message),
		directWriteCh:      make(chan directWrite),
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

// Connect establishes the connection to the server using configured Addr
func (c *Connection) Connect() error {
	var conn net.Conn
	var err error

	if c.conn != nil {
		c.run()
		return nil
	}

	d := &net.Dialer{Timeout: c.Opts.ConnectTimeout}

	if c.Opts.TLSConfig != nil {
		conn, err = tls.DialWithDialer(d, "tcp", c.addr, c.Opts.TLSConfig)
	} else {
		conn, err = d.Dial("tcp", c.addr)
	}

	if err != nil {
		return fmt.Errorf("connecting to server %s: %w", c.addr, err)
	}

	c.conn = conn

	c.run()

	if c.Opts.OnConnect != nil {
		if err := c.Opts.OnConnect(c); err != nil {
			// close connection if OnConnect failed
			// but ignore the potential error from Close()
			// as it's a rare case
			_ = c.Close()

			return fmt.Errorf("on connect callback %s: %w", c.addr, err)
		}
	}

	if c.Opts.ConnectionEstablishedHandler != nil {
		go c.Opts.ConnectionEstablishedHandler(c)
	}

	return nil
}

// Write writes data directly to the connection. It is crucial to note that the
// Write operation is atomic in nature, meaning it completes in a single
// uninterrupted step.
// When writing data, the entire message—including its header and any other
// components—should be written in one go. Splitting a single message into
// multiple Write calls is dangerous, as it could lead to unexpected behavior
// or errors.
func (c *Connection) Write(p []byte) (int, error) {
	dw := directWrite{
		data:  p,
		errCh: make(chan error, 1),
	}

	select {
	case c.directWriteCh <- dw:
		return len(p), <-dw.errCh
	case <-c.done:
		return 0, ErrConnectionClosed
	}
}

// run starts read and write loops in goroutines
func (c *Connection) run() {
	go c.writeLoop()
	go c.readLoop()
	go c.readResponseLoop()
}

func (c *Connection) handleError(err error) {
	if c.Opts.ErrorHandler == nil {
		return
	}

	c.mutex.Lock()
	if c.closing {
		c.mutex.Unlock()
		return
	}
	c.mutex.Unlock()

	go c.Opts.ErrorHandler(err)
}

// when connection fails it cleans up all the things
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

	if c.Opts.ConnectionClosedHandlers != nil && len(c.Opts.ConnectionClosedHandlers) > 0 {
		for _, handler := range c.Opts.ConnectionClosedHandlers {
			go handler(c)
		}
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
	if c.Opts.OnClose != nil {
		if err := c.Opts.OnClose(c); err != nil {
			return fmt.Errorf("on close callback: %w", err)
		}
	}

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
	// message to send
	message *iso8583.Message

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

// Send sends message and waits for the response. You can pass optional
// parameters to the Send method using functional options pattern. Currently,
// only SendTimeout option is supported. Using it, you can set specific send
// timeout value for the `Send` method call.
// Example:
//
//	conn.Send(msg, connection.SendTimeout(5 * time.Second))
func (c *Connection) Send(message *iso8583.Message, options ...Option) (*iso8583.Message, error) {
	// use send timeout value configured for the connection
	opts := &Options{
		SendTimeout: c.Opts.SendTimeout,
	}

	// apply all options
	for _, opt := range options {
		opt(opts)
	}

	sendTimeout := opts.SendTimeout

	c.mutex.Lock()
	if c.closing {
		c.mutex.Unlock()
		return nil, ErrConnectionClosed
	}
	// calling wg.Add(1) within mutex guarantees that it does not pass the wg.Wait() call in the Close method
	// otherwise we will have data race issue
	c.wg.Add(1)
	c.mutex.Unlock()
	defer c.wg.Done()

	// prepare request
	reqID, err := c.Opts.RequestIDGenerator.GenerateRequestID(message)
	if err != nil {
		return nil, fmt.Errorf("creating request ID: %w", err)
	}

	req := request{
		message:   message,
		requestID: reqID,
		replyCh:   make(chan *iso8583.Message),
		errCh:     make(chan error),
	}

	var resp *iso8583.Message

	c.requestsCh <- req

	sendTimeoutTimer := time.NewTimer(sendTimeout)
	defer sendTimeoutTimer.Stop()

	select {
	case resp = <-req.replyCh:
	case err = <-req.errCh:
	case <-sendTimeoutTimer.C:
		err = ErrSendTimeout
		// reply can still be sent after SendTimeout received.
		// if we have UnmatchedMessageHandler set, then we want reply
		// to not be lost but handled by it.
		if c.Opts.InboundMessageHandler != nil {
			go func() {
				oneMoreSecondTimer := time.NewTimer(time.Second)
				defer oneMoreSecondTimer.Stop()

				select {
				case resp := <-req.replyCh:
					go c.Opts.InboundMessageHandler(c, resp)
				case <-oneMoreSecondTimer.C:
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

func (c *Connection) writeMessage(w io.Writer, message *iso8583.Message) error {
	if c.Opts.MessageWriter != nil {
		err := c.Opts.MessageWriter.WriteMessage(c.conn, message)
		if err != nil {
			return fmt.Errorf("writing message: %w", err)
		}

		return nil
	}

	// if no custom message writer is set, use default one

	packed, err := message.Pack()
	if err != nil {
		return fmt.Errorf("packing message: %w", err)
	}

	// create header
	_, err = c.writeMessageLength(c.conn, len(packed))
	if err != nil {
		return fmt.Errorf("writing message length: %w", err)
	}

	_, err = c.conn.Write(packed)
	if err != nil {
		return fmt.Errorf("writing packed message: %w", err)
	}

	return nil
}

// Reply sends the message and does not wait for a reply to be received.
// Any reply received for message send using Reply will be handled with
// unmatchedMessageHandler
func (c *Connection) Reply(message *iso8583.Message) error {
	c.mutex.Lock()
	if c.closing {
		c.mutex.Unlock()
		return ErrConnectionClosed
	}
	// calling wg.Add(1) within mutex guarantees that it does not pass the wg.Wait() call in the Close method
	// otherwise we will have data race issue
	c.wg.Add(1)
	c.mutex.Unlock()
	defer c.wg.Done()

	req := request{
		message: message,
		errCh:   make(chan error),
	}

	c.requestsCh <- req

	sendTimeoutTimer := time.NewTimer(c.Opts.SendTimeout)
	defer sendTimeoutTimer.Stop()

	var err error

	select {
	case err = <-req.errCh:
	case <-sendTimeoutTimer.C:
		err = ErrSendTimeout
	}

	return err
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
		idleTimeTimer := time.NewTimer(c.Opts.IdleTime)

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

			err = c.writeMessage(c.conn, req.message)
			if err != nil {
				c.handleError(fmt.Errorf("writing message: %w", err))

				var packErr *iso8583.PackError
				if errors.As(err, &packErr) {
					// let caller know that the message was not sent because of pack error.
					// We don't set all type of errors to errCh as this case is handled
					// by handleConnectionError(err) which sends the same error to all
					// pending requests, including this one
					req.errCh <- err

					err = nil

					// we can continue to write other messages
					continue
				}

				break
			}

			// for replies (requests without replyCh) we just
			// return nil to errCh as caller is waiting for error
			// or send timeout. Regular requests waits for responses
			// to be received to their replyCh channel.
			if req.replyCh == nil {
				req.errCh <- nil
			}

		case dw := <-c.directWriteCh:
			_, err = c.conn.Write(dw.data)
			if err != nil {
				c.handleError(fmt.Errorf("writing data: %w", err))
				dw.errCh <- err

				// we can't continue to write other messages or data when we failed to write
				// one of them
				break
			}
			dw.errCh <- nil

		case <-idleTimeTimer.C:
			// if no message was sent during idle time, we have to send ping message
			if c.Opts.PingHandler != nil {
				go c.Opts.PingHandler(c)
			}
		case <-c.done:
			idleTimeTimer.Stop()
			return
		}

		idleTimeTimer.Stop()
	}

	c.handleConnectionError(err)
}

// readLoop reads data from the socket (message length header and raw message)
// and runs a goroutine to handle the message
func (c *Connection) readLoop() {
	var outErr error

	r := bufio.NewReader(c.conn)
	for {
		message, err := c.readMessage(r)
		if err != nil {
			c.handleError(utils.NewSafeError(err, "failed to read message from connection"))

			// if err is UnpackError, we can still continue reading
			// from the connection
			var unpackErr *iso8583.UnpackError
			if errors.As(err, &unpackErr) {
				continue
			}

			outErr = err
			break
		}

		// if readMessage returns nil message, it means that
		// it was a ping message or something else, not a regular
		// iso8583 message and we can continue reading
		if message == nil {
			continue
		}

		c.readResponseCh <- message
	}

	c.handleConnectionError(outErr)
}

// readMessage reads message length header and raw message from the connection
// and returns iso8583.Message and error if any
func (c *Connection) readMessage(r io.Reader) (*iso8583.Message, error) {
	if c.Opts.MessageReader != nil {
		return c.Opts.MessageReader.ReadMessage(r)
	}

	// default message reader
	messageLength, err := c.readMessageLength(r)
	if err != nil {
		return nil, fmt.Errorf("failed to read message length: %w", err)
	}

	// read the packed message
	rawMessage := make([]byte, messageLength)
	_, err = io.ReadFull(r, rawMessage)
	if err != nil {
		return nil, fmt.Errorf("failed to read message from connection: %w", err)
	}

	// unpack the message
	message := iso8583.NewMessage(c.spec)
	err = message.Unpack(rawMessage)
	if err != nil {
		return nil, fmt.Errorf("unpacking message: %w", err)
	}

	return message, nil
}

func (c *Connection) readResponseLoop() {
	for {
		readTimeoutTimer := time.NewTimer(c.Opts.ReadTimeout)

		select {
		case mess := <-c.readResponseCh:
			go c.handleResponse(mess)
		case <-readTimeoutTimer.C:
			if c.Opts.ReadTimeoutHandler != nil {
				go c.Opts.ReadTimeoutHandler(c)
			}
		case <-c.done:
			readTimeoutTimer.Stop()
			return
		}

		readTimeoutTimer.Stop()
	}
}

// handleResponse sends message to the reply channel that corresponds to the
// message ID (request ID)
func (c *Connection) handleResponse(message *iso8583.Message) {
	if isResponse(message) {
		reqID, err := c.Opts.RequestIDGenerator.GenerateRequestID(message)
		if err != nil {
			c.handleError(fmt.Errorf("creating request ID:  %w", err))
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
			c.handleError(fmt.Errorf("can't find request for ID: %s", reqID))
		}
	} else {
		if c.Opts.InboundMessageHandler != nil {
			go c.Opts.InboundMessageHandler(c, message)
		}
	}
}

// SetStatus sets the connection status
func (c *Connection) SetStatus(status Status) {
	c.mutex.Lock()
	c.status = status
	c.mutex.Unlock()
}

// Status returns the connection status
func (c *Connection) Status() Status {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	return c.status
}

// Addr returns the remote address of the connection
func (c *Connection) Addr() string {
	return c.addr
}
