package connection

import (
	"time"
)

type PoolOption func(*PoolOptions) error

type PoolOptions struct {
	// ReconnectWait sets the time to wait after first re-connect attempt
	// The default is 5 seconds
	ReconnectWait time.Duration

	// MaxReconnectWait specifies the maximum duration to wait between
	// reconnection attempts, serving as the upper bound for exponential
	// backoff.
	// A zero value means no exponential backoff and ReconnectWait is used
	// for each retry.
	// The default is 0.
	MaxReconnectWait time.Duration

	// ErrorHandler is called in a goroutine with the errors that can't be
	// returned to the caller
	ErrorHandler func(err error)

	// MinConnections is the number of connections required to be established when
	// we connect the pool.
	// A zero value means that Pool will not return error on `Connect` and will
	// try to connect to all the addresses in the pool.
	// The default is 1.
	MinConnections int

	// ConnectionsFilter is a function to filter connections in the pool
	// when Get() is called
	ConnectionsFilter func(*Connection) bool
}

func GetDefaultPoolOptions() PoolOptions {
	return PoolOptions{
		ReconnectWait:    5 * time.Second,
		MaxReconnectWait: 0,
		MinConnections:   1,
	}
}

func PoolReconnectWait(rw time.Duration) PoolOption {
	return func(opts *PoolOptions) error {
		opts.ReconnectWait = rw
		return nil
	}
}

func PoolMaxReconnectWait(rw time.Duration) PoolOption {
	return func(opts *PoolOptions) error {
		opts.MaxReconnectWait = rw
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

func PoolConnectionsFilter(f func(*Connection) bool) PoolOption {
	return func(opts *PoolOptions) error {
		opts.ConnectionsFilter = f
		return nil
	}
}
