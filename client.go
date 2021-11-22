package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
)

type Client struct {
	conn       net.Conn
	requestsCh chan request
	respMap    map[string]chan *Message
}

func NewClient() *Client {
	return &Client{
		requestsCh: make(chan request),
		respMap:    make(map[string]chan *Message),
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
	return c.conn.Close()
}

// temporary type that will be replaced with iso8583 message
type Message struct {
	Msg string
}

type request struct {
	isoMessage *Message
	requestID  string
	replyCh    chan *Message
	errCh      chan error
}

// request id should be generated using different message fields (STAN, RRN, etc.)
func requestID(msg *Message) string {
	return msg.Msg[0:3]
}

// send message and waits for the response
func (c *Client) Send(msg *Message) (*Message, error) {
	// prepare message
	req := request{
		isoMessage: msg,
		requestID:  requestID(msg),
		replyCh:    make(chan *Message),
		errCh:      make(chan error),
	}

	var resp *Message
	var err error
	c.requestsCh <- req
	select {
	// we can add timeout here as well
	// ...
	case resp = <-req.replyCh:
	case err = <-req.errCh:
	}

	return resp, err
}

// TODO: when do we return from this goroutine?
func (c *Client) writeLoop() {
	// TODO
	// we should either (select)
	// * send heartbeat message
	// * read request from requestsCh
	// * if client was closed, reject all outstanding requests and return
	for req := range c.requestsCh {
		c.respMap[req.requestID] = req.replyCh
		_, err := c.conn.Write([]byte(req.isoMessage.Msg))
		if err != nil {
			req.errCh <- err
			// delete request from respMap
			// handle write error: reconnect? shutdown? panic?
		}
	}
}

// TODO: when do we return from this goroutine
func (c *Client) readLoop() {
	// TODO
	// read messages from the connection
	// if we got error during reading, what should we do? should we reconnect?
	// if client was closed, set timeout and wait for all pending requests to be replied and return
	r := bufio.NewReader(c.conn)
	scanner := bufio.NewScanner(r)

	for scanner.Scan() {
		reply := scanner.Text()
		msg := &Message{
			Msg: reply,
		}

		reqID := requestID(msg)

		if replyCh, found := c.respMap[reqID]; found {
			replyCh <- msg
			// this one should be done inside mutex lock
			delete(c.respMap, reqID)
		} else {
			// we should log information about received message
			// as there is no one to give it to
			// maybe create a lost message queue?
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "reading standard input:", err)
	}
}
