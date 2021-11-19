package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type Client struct {
	conn       net.Conn
	requestsCh chan request
	respMap    map[string]chan string
}

func NewClient() *Client {
	return &Client{
		requestsCh: make(chan request),
		respMap:    make(map[string]chan string),
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

type request struct {
	isoMessage string
	requestID  string
	replyCh    chan string
	errCh      chan error
}

// request id should be generated using different message fields (STAN, RRN, etc.)
func requestID(msg string) string {
	return msg[0:3]
}

// send message and waits for the response
func (c *Client) Send(msg string) (string, error) {
	// prepare message
	req := request{
		isoMessage: msg,
		requestID:  requestID(msg),
		replyCh:    make(chan string),
		errCh:      make(chan error),
	}

	var resp string
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

func (c *Client) writeLoop() {
	// TODO
	// we should either (select)
	// * send heartbeat message
	// * read request from requestsCh
	// * if client was closed, reject all outstanding requests and return
	for req := range c.requestsCh {
		c.respMap[req.requestID] = req.replyCh
		_, err := c.conn.Write([]byte(req.isoMessage))
		if err != nil {
			req.errCh <- err
			// delete request from respMap
			// handle write error: reconnect? shutdown? panic?
		}
	}
}

func (c *Client) readLoop() {
	// TODO
	// read messages from the connection
	// if we got error during reading, what should we do? should we reconnect?
	// if client was closed, set timeout and wait for all pending requests to be replied and return
	r := bufio.NewReader(c.conn)
	scanner := bufio.NewScanner(r)

	for scanner.Scan() {
		msg := scanner.Text()
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

func TestClientConnect(t *testing.T) {
	server, err := NewServer()
	require.NoError(t, err)
	defer server.Close()

	// our client can connect to the server
	client := NewClient()
	err = client.Connect(server.Addr)
	require.NoError(t, err)

	// we can send iso message to the server
	response, err := client.Send("ping 1")
	require.NoError(t, err)
	time.Sleep(1 * time.Second)
	require.Equal(t, "ping 1 pong", response)

	response, err = client.Send("ping 2")
	require.NoError(t, err)
	time.Sleep(1 * time.Second)
	require.Equal(t, "ping 2 pong", response)

	require.NoError(t, client.Close())
}
