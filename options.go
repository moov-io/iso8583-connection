package client

import (
	"time"

	"github.com/moov-io/iso8583"
)

type Options struct {
	// SendTimeout sets the timeout for a Send operation
	SendTimeout time.Duration

	// IdleTime is the period at which the client will be sending ping
	// message to the server
	IdleTime time.Duration

	// PingHandler is called when no message was sent during idle time
	// it should be safe for concurrent use
	PingHandler func(c *Client)

	// UnmatchedMessageHandler is called when a message from the server is
	// received and no matching request for it was found.
	// UnmatchedMessageHandler should be safe for concurrent use. Use it
	// for the following use cases:
	// * to log timed out responses
	// * to handle network management messages (echo, heartbeat, etc.)
	UnmatchedMessageHandler func(c *Client, message *iso8583.Message)
}

type Option func(*Options)

func GetDefaultOptions() Options {
	return Options{
		SendTimeout: 30 * time.Second,
		IdleTime:    5 * time.Second,
		PingHandler: nil,
	}
}

// IdleTime sets an IdleTime option
func IdleTime(d time.Duration) Option {
	return func(o *Options) {
		o.IdleTime = d
	}
}

// SendTimeout sets an SendTimeout option
func SendTimeout(d time.Duration) Option {
	return func(o *Options) {
		o.SendTimeout = d
	}
}

// PingHandler sets a PingHandler option
func PingHandler(handler func(c *Client)) Option {
	return func(o *Options) {
		o.PingHandler = handler
	}
}

// UnmatchedMessageHandler sets an UnmatchedMessageHandler option
func UnmatchedMessageHandler(handler func(c *Client, message *iso8583.Message)) Option {
	return func(o *Options) {
		o.UnmatchedMessageHandler = handler
	}
}
