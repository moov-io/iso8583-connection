package connection

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/moov-io/iso8583"
)

type Options struct {
	// ConnectTimeout sets the timeout for establishing new connections.
	ConnectTimeout time.Duration

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

	// ConnectionClosedHandlers is called when connection is closed by server or there
	// were network errors during network read/write
	ConnectionClosedHandlers []func(c *Connection)

	// ConnectionEstablishedHandler is called when connection is
	// established with the server
	ConnectionEstablishedHandler func(c *Connection)

	TLSConfig *tls.Config

	// ErrorHandler is called in a goroutine with the errors that can't be
	// returned to the caller
	ErrorHandler func(err error)

	// OnConnect is called synchronously when a connection is established
	OnConnect func(c *Connection) error

	// OnClose is called synchronously before a connection is closed
	OnClose func(c *Connection) error

	// RequestIDGenerator is used to generate a unique identifier for a request
	// so that responses from the server can be matched to the original request.
	RequestIDGenerator RequestIDGenerator

	// MessageReader is used to read a message from the connection
	// if set, connection's MessageLengthReader will be ignored
	MessageReader MessageReader

	// MessageWriter is used to write a message to the connection
	// if set, connection's MessageLengthWriter will be ignored
	MessageWriter MessageWriter
}

type MessageReader interface {
	ReadMessage(r io.Reader) (*iso8583.Message, error)
}

type MessageWriter interface {
	WriteMessage(w io.Writer, message *iso8583.Message) error
}

type Option func(*Options) error

func GetDefaultOptions() Options {
	return Options{
		ConnectTimeout:     10 * time.Second,
		SendTimeout:        30 * time.Second,
		IdleTime:           5 * time.Second,
		ReadTimeout:        60 * time.Second,
		PingHandler:        nil,
		TLSConfig:          nil,
		RequestIDGenerator: &defaultRequestIDGenerator{},
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

// ConnectTimeout sets an SendTimeout option
func ConnectTimeout(d time.Duration) Option {
	return func(o *Options) error {
		o.ConnectTimeout = d
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
		o.ConnectionClosedHandlers = append(o.ConnectionClosedHandlers, handler)
		return nil
	}
}

func ConnectionEstablishedHandler(handler func(c *Connection)) Option {
	return func(o *Options) error {
		o.ConnectionEstablishedHandler = handler
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
// in many cases err will be an instance of the `SafeError`
// for more details: https://github.com/moov-io/iso8583/pull/185
func ErrorHandler(h func(err error)) Option {
	return func(opts *Options) error {
		opts.ErrorHandler = h
		return nil
	}
}

// OnConnect sets a callback that will be synchronously  called when connection is established.
// If it returns error, then connections will be closed and re-connect will be attempted
func OnConnect(h func(c *Connection) error) Option {
	return func(opts *Options) error {
		opts.OnConnect = h
		return nil
	}
}

func OnClose(h func(c *Connection) error) Option {
	return func(opts *Options) error {
		opts.OnClose = h
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
			cert, err := os.ReadFile(f)
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

// RequestIDGenerator sets a RequestIDGenerator option
func SetRequestIDGenerator(g RequestIDGenerator) Option {
	return func(o *Options) error {
		o.RequestIDGenerator = g
		return nil
	}
}

// SetMessageReader sets a MessageReader option
func SetMessageReader(r MessageReader) Option {
	return func(o *Options) error {
		o.MessageReader = r
		return nil
	}
}

// SetMessageWriter sets a MessageWriter option
func SetMessageWriter(w MessageWriter) Option {
	return func(o *Options) error {
		o.MessageWriter = w
		return nil
	}
}
