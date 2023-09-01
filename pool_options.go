package connection

import (
	"time"
)

type PoolOption func(*PoolOptions) error

type PoolOptions struct {
	// ReconnectWait sets the time to wait after first re-connect attempt
	ReconnectWait time.Duration

	// MaxReconnectWait is the maximum wait time for re-connect attempt
	// if it is set to non-zero value, it will be used as the upper bound
	// for exponential backoff
	MaxReconnectWait time.Duration

	// ErrorHandler is called in a goroutine with the errors that can't be
	// returned to the caller
	ErrorHandler func(err error)

	// MinConnections is the number of connections required to be established when
	// we connect the pool
	MinConnections int

	// ConntionsFilter is a function to filter connections in the pool
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
