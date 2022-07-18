package connection

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"time"

	"github.com/moov-io/iso8583"
)

type Options struct {
	// SendTimeout sets the timeout for a Send operation
	SendTimeout time.Duration

	// IdleTime is the period at which the client will be sending ping
	// message to the server
	IdleTime time.Duration

	// ReadTimeout is the maximum time between read events before the
	// ReadTimeoutHandler is called
	ReadTimeout time.Duration

	// PingHandler is called when no message was sent during idle time
	// it should be safe for concurrent use
	PingHandler func(c *Connection)

	// ReadTimeoutHandler is called when no message has been received within
	// the ReadTimeout interval
	ReadTimeoutHandler func(c *Connection)

	// InboundMessageHandler is called when a message from the server is
	// received and no matching request for it was found.
	// InboundMessageHandler should be safe for concurrent use. Use it
	// for the following use cases:
	// * to log timed out responses
	// * to handle network management messages (echo, heartbeat, etc.)
	InboundMessageHandler func(c *Connection, message *iso8583.Message)

	// ConnectionClosedHandler is called when connection is closed by server or there
	// were network errors during network read/write
	ConnectionClosedHandler func(c *Connection)

	TLSConfig *tls.Config

	// ErrorHandler is called in a goroutine with the errors that can't be
	// returned to the caller
	ErrorHandler func(err error)
}

type Option func(*Options) error

func GetDefaultOptions() Options {
	return Options{
		SendTimeout: 30 * time.Second,
		IdleTime:    5 * time.Second,
		ReadTimeout: 60 * time.Second,
		PingHandler: nil,
		TLSConfig:   nil,
	}
}

// IdleTime sets an IdleTime option
func IdleTime(d time.Duration) Option {
	return func(o *Options) error {
		o.IdleTime = d
		return nil
	}
}

// SendTimeout sets an SendTimeout option
func SendTimeout(d time.Duration) Option {
	return func(o *Options) error {
		o.SendTimeout = d
		return nil
	}
}

// ReadTimeout sets an ReadTimeout option
func ReadTimeout(d time.Duration) Option {
	return func(o *Options) error {
		o.ReadTimeout = d
		return nil
	}
}

// ReadTimeoutHandler sets a ReadTimeoutHandler option
func ReadTimeoutHandler(handler func(c *Connection)) Option {
	return func(o *Options) error {
		o.ReadTimeoutHandler = handler
		return nil
	}
}

// PingHandler sets a PingHandler option
func PingHandler(handler func(c *Connection)) Option {
	return func(o *Options) error {
		o.PingHandler = handler
		return nil
	}
}

// ConnectionClosedHandler sets a ConnectionClosedHandler option
func ConnectionClosedHandler(handler func(c *Connection)) Option {
	return func(o *Options) error {
		o.ConnectionClosedHandler = handler
		return nil
	}
}

// InboundMessageHandler sets an InboundMessageHandler option
func InboundMessageHandler(handler func(c *Connection, message *iso8583.Message)) Option {
	return func(o *Options) error {
		o.InboundMessageHandler = handler
		return nil
	}
}

// ErrorHandler sets an ErrorHandler option
func ErrorHandler(h func(err error)) Option {
	return func(opts *Options) error {
		opts.ErrorHandler = h
		return nil
	}
}

func defaultTLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
	}
}

func ClientCert(cert, key string) Option {
	return func(o *Options) error {
		if o.TLSConfig == nil {
			o.TLSConfig = defaultTLSConfig()
		}

		certificate, err := tls.LoadX509KeyPair(cert, key)
		if err != nil {
			return fmt.Errorf("loading certificate: %w", err)
		}

		o.TLSConfig.Certificates = []tls.Certificate{certificate}

		return nil
	}
}

// RootCAs creates pool of Root CAs
func RootCAs(file ...string) Option {
	return func(o *Options) error {
		if o.TLSConfig == nil {
			o.TLSConfig = defaultTLSConfig()
		}

		certPool := x509.NewCertPool()

		for _, f := range file {
			cert, err := ioutil.ReadFile(f)
			if err != nil {
				return fmt.Errorf("loading root certificate %s: %w", file, err)
			}

			ok := certPool.AppendCertsFromPEM(cert)
			if !ok {
				return fmt.Errorf("parsing root certificate %s: %w", file, err)
			}
		}

		o.TLSConfig.RootCAs = certPool

		return nil
	}
}

func SetTLSConfig(cfg func(*tls.Config)) Option {
	return func(o *Options) error {
		if o.TLSConfig == nil {
			o.TLSConfig = defaultTLSConfig()
		}
		cfg(o.TLSConfig)
		return nil
	}
}
