package connection

import (
	"time"
)

type PoolOption func(*PoolOptions) error

type PoolOptions struct {
	// ReconnectWait sets the time to wait after first re-connect attempt
	ReconnectWait time.Duration

	// ErrorHandler is called in a goroutine with the errors that can't be
	// returned to the caller
	ErrorHandler func(err error)

	// MinConnections is the number of connections required to be established when
	// we connect the pool
	MinConnections int

	// OnConnect is called synchronously when a connection is established
	OnConnect func(c *Connection) error
}

func GetDefaultPoolOptions() PoolOptions {
	return PoolOptions{
		ReconnectWait:  5 * time.Second,
		MinConnections: 1,
	}
}

func PoolReconnectWait(rw time.Duration) PoolOption {
	return func(opts *PoolOptions) error {
		opts.ReconnectWait = rw
		return nil
	}
}

func PoolMinConnections(n int) PoolOption {
	return func(opts *PoolOptions) error {
		opts.MinConnections = n
		return nil
	}
}

func PoolErrorHandler(h func(err error)) PoolOption {
	return func(opts *PoolOptions) error {
		opts.ErrorHandler = h
		return nil
	}
}

// PoolOnConnect sets a callback that will be synchronously  called when connection is established.
// If it returns error, then connections will be closed and re-connect will be attempted
func PoolOnConnect(h func(c *Connection) error) PoolOption {
	return func(opts *PoolOptions) error {
		opts.OnConnect = h
		return nil
	}
}
