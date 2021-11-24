package main

import (
	"bufio"
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
	"github.com/moov-io/iso8583/network"
)

var ErrConnectionClosed = errors.New("connection closed")

type Options struct {
	// SendTimeout sets the timeout for a Send operation
	SendTimeout time.Duration

	// IdleTime is the period at which the client will be sending ping
	// message to the server, disabled if 0 or negative.
	IdleTime time.Duration
}

type Option func(*Options)

func GetDefaultOptions() Options {
	return Options{
		SendTimeout: 30 * time.Second,
		IdleTime:    5 * time.Second,
	}
}

type Client struct {
	opts       Options
	conn       net.Conn
	requestsCh chan request
	done       chan struct{}

	pendingRequestsMu sync.Mutex
	respMap           map[string]chan *iso8583.Message

	// WaitGroup to wait for all Send calls to finish
	wg sync.WaitGroup

	// to protect following: closing, STAN
	mutex sync.Mutex

	// user has called Close
	closing bool

	// STAN counter, max can be 999999
	stan int32
}

func NewClient(options ...Option) *Client {
	opts := GetDefaultOptions()
	for _, opt := range options {
		opt(&opts)
	}

	return &Client{
		opts:       opts,
		requestsCh: make(chan request),
		done:       make(chan struct{}),
		respMap:    make(map[string]chan *iso8583.Message),
	}
}

func (c *Client) Connect(addr string) error {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return fmt.Errorf("connecting to server: %v", err)
	}
	c.conn = conn

	go c.writeLoop()
	go c.readLoop()

	return nil
}

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

	return c.conn.Close()
}

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

// send message and waits for the response
func (c *Client) Send(message *iso8583.Message) (*iso8583.Message, error) {
	c.wg.Add(1)
	defer c.wg.Done()

	c.mutex.Lock()
	if c.closing {
		c.mutex.Unlock()
		return nil, ErrConnectionClosed
	}
	c.mutex.Unlock()

	// prepare message for sending

	// set STAN if it's empty
	err := c.setMessageSTAN(message)
	if err != nil {
		return nil, fmt.Errorf("setting message STAN: %v", err)
	}

	var buf bytes.Buffer
	packed, err := message.Pack()
	if err != nil {
		return nil, fmt.Errorf("packing message: %v", err)
	}

	// create header
	header := network.NewVMLHeader()
	header.SetLength(len(packed))

	_, err = header.WriteTo(&buf)
	if err != nil {
		return nil, fmt.Errorf("writing message header: %v", err)
	}

	_, err = buf.Write(packed)
	if err != nil {
		return nil, fmt.Errorf("writing packed message to buffer: %v", err)
	}

	// prepare request
	reqID, err := requestID(message)
	if err != nil {
		return nil, fmt.Errorf("getting request ID: %v", err)
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
	// we can add timeout here so it can be handled on a higher level
	// ...
	case resp = <-req.replyCh:
	case err = <-req.errCh:
		// case <-time.After(messa
	}

	return resp, err
}

func (c *Client) setMessageSTAN(message *iso8583.Message) error {
	stan, err := message.GetString(11)
	if err != nil {
		return fmt.Errorf("getting STAN (field 11) of the message: %v", err)
	}

	// no STAN was provided, generate a new one
	if stan == "" {
		stan = c.getSTAN()
	}

	err = message.Field(11, stan)
	if err != nil {
		return fmt.Errorf("setting STAN (field 11): %s of the message: %v", stan, err)
	}

	return nil
}

// request id should be generated using different message fields (STAN, RRN, etc.)
// each request/response should be uniquely linked to the message
// current assumption is that STAN should be enough for this
// but because STAN is 6 digits, there is no way we can process millions transactions
// per second using STAN only
// More options for STAN:
// * match by RRN + STAN
// * it's typically unique in 24h and usually scoped to TID and transmission time fields.
func requestID(message *iso8583.Message) (string, error) {
	stan, err := message.GetString(11)
	if err != nil {
		return "", fmt.Errorf("getting STAN (field 11) of the message: %v", err)
	}
	return stan, nil
}

func (c *Client) writeLoop() {
	for {
		select {
		case req := <-c.requestsCh:
			c.pendingRequestsMu.Lock()
			c.respMap[req.requestID] = req.replyCh
			c.pendingRequestsMu.Unlock()

			_, err := c.conn.Write([]byte(req.rawMessage))
			if err != nil {
				req.errCh <- err
				c.pendingRequestsMu.Lock()
				delete(c.respMap, req.requestID)
				c.pendingRequestsMu.Unlock()
			}
		case <-time.After(c.opts.IdleTime):
			// if no message was sent during idle time, we have to send ping message
			go c.sendPingMessage()
		case <-c.done:
			return
		}

	}
	// TODO: handle write error: reconnect, re-try?, etc.
}

func (c *Client) sendPingMessage() {
	pingMessage := iso8583.NewMessage(brandSpec)
	pingMessage.MTI("0800")
	pingMessage.Field(70, "371")

	response, err := c.Send(pingMessage)
	if err != nil {
		log.Printf("sending ping message: %v", err)
		return
	}

	mti, err := response.GetMTI()
	if err != nil {
		log.Printf("getting ping message MTI: %v", err)
		return
	}

	if mti != "0810" {
		log.Printf("unexpected MTI for ping message response: %s", mti)
	}
}

func (c *Client) readLoop() {
	var err error

	r := bufio.NewReader(c.conn)
	for {
		// read header first
		header := network.NewVMLHeader()
		_, err := header.ReadFrom(r)
		if err != nil {
			break
		}

		// read the packed message
		rawMessage := make([]byte, header.Length())
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
		fmt.Fprintln(os.Stderr, "reading from socket:", err)
	}
	c.mutex.Unlock()

}

func (c *Client) handleResponse(rawMessage []byte) {
	// create message
	message := iso8583.NewMessage(brandSpec)
	err := message.Unpack(rawMessage)
	if err != nil {
		log.Printf("unpacking message: %v", err)
		return
	}

	reqID, err := requestID(message)
	if err != nil {
		log.Printf("getting request ID: %v", err)
		return
	}

	// send response message to the reply channel
	c.pendingRequestsMu.Lock()
	if replyCh, found := c.respMap[reqID]; found {
		replyCh <- message
		delete(c.respMap, reqID)
	} else {
		log.Printf("can't find request for ID: %s", reqID)
	}
	c.pendingRequestsMu.Unlock()
}

// Assumptions:
// * We can use the same STAN after request/response messages for such STAN were handled
// * STAN can be incremented but it MAX is 999999 it means we can start from 0 when we reached max
func (c *Client) getSTAN() string {
	// TODO: maybe use own mutex
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.stan++
	if c.stan > 999999 {
		c.stan = 0
	}
	return fmt.Sprintf("%06d", c.stan)
}
