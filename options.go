package client

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

	TLSConfig *tls.Config
}

type Option func(*Options) error

func GetDefaultOptions() Options {
	return Options{
		SendTimeout: 30 * time.Second,
		IdleTime:    5 * time.Second,
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

// PingHandler sets a PingHandler option
func PingHandler(handler func(c *Client)) Option {
	return func(o *Options) error {
		o.PingHandler = handler
		return nil
	}
}

// UnmatchedMessageHandler sets an UnmatchedMessageHandler option
func UnmatchedMessageHandler(handler func(c *Client, message *iso8583.Message)) Option {
	return func(o *Options) error {
		o.UnmatchedMessageHandler = handler
		return nil
	}
}

func ClientCert(cert, key string) Option {
	return func(o *Options) error {
		if o.TLSConfig == nil {
			o.TLSConfig = &tls.Config{
				MinVersion: tls.VersionTLS12,
			}
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
			o.TLSConfig = &tls.Config{
				MinVersion: tls.VersionTLS12,
			}
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
