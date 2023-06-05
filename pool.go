package connection

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

var ErrNoConnections = errors.New("no connections (online)")

type ConnectionFactoryFunc func(addr string) (*Connection, error)

type Pool struct {
	Factory ConnectionFactoryFunc
	Addrs   []string
	Opts    PoolOptions

	done chan struct{}

	// WaitGroup to wait for all reconnect goroutines return
	wg sync.WaitGroup

	mu          sync.Mutex // protects following fields
	connections []*Connection
	connIndex   uint32
	isClosed    bool
}

// Pool - provides connections to the clients. It removes the connection from
// the pool if it was closed and in the background tries to create and etablish
// new connection so the pool will be full.
func NewPool(factory ConnectionFactoryFunc, addrs []string, options ...PoolOption) (*Pool, error) {
	opts := GetDefaultPoolOptions()
	for _, opt := range options {
		if err := opt(&opts); err != nil {
			return nil, fmt.Errorf("setting pool option: %v %w", opt, err)
		}
	}

	return &Pool{
		Factory:     factory,
		Opts:        opts,
		Addrs:       addrs,
		done:        make(chan struct{}),
		connections: make([]*Connection, 0),
	}, nil
}

func (p *Pool) handleError(err error) {
	if p.Opts.ErrorHandler == nil {
		return
	}

	go p.Opts.ErrorHandler(err)
}

// Connect creates poll of connections by calling Factory method and connect them all
func (p *Pool) Connect() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.isClosed {
		return errors.New("pool is closed")
	}

	// build connections
	for _, addr := range p.Addrs {
		conn, err := p.Factory(addr)
		if err != nil {
			return fmt.Errorf("creating connection for %s: %w", addr, err)
		}

		// set own handler when connection is closed
		conn.SetOptions(ConnectionClosedHandler(p.handleClosedConnection))

		err = conn.Connect()
		if err != nil {
			p.handleError(fmt.Errorf("connecting to %s: %w", conn.addr, err))
			p.wg.Add(1)
			go p.recreateConnection(conn)
			continue
		}

		p.connections = append(p.connections, conn)
	}

	if len(p.connections) < p.Opts.MinConnections {
		return fmt.Errorf("minimum %d connections is required, established: %d", p.Opts.MinConnections, len(p.connections))
	}

	return nil
}

// Connections returns copy of all connections from the pool
func (p *Pool) Connections() []*Connection {
	p.mu.Lock()
	defer p.mu.Unlock()

	// create new slice, as p.connections can be changed
	var conns []*Connection
	conns = append(conns, p.connections...)

	return conns
}

// FilterFunc is a function to filter connections
type FilterFunc func(*Connection) bool

// Get returns filtered connection from the pool
func (p *Pool) Get() (*Connection, error) {
	p.mu.Lock()
	if p.isClosed {
		p.mu.Unlock()
		return nil, errors.New("pool is closed")
	}
	p.mu.Unlock()

	// filtered connections
	conns := p.filteredConnections()

	if len(conns) == 0 {
		return nil, ErrNoConnections
	}

	n := atomic.AddUint32(&p.connIndex, 1)

	return conns[(int(n)-1)%len(conns)], nil
}

// when connection is closed, remove it from the pool of connections and start
// goroutine to create new connection for the same address
func (p *Pool) handleClosedConnection(closedConn *Connection) {
	p.mu.Lock()
	defer p.mu.Unlock()

	var connIndex = -1
	for i, conn := range p.connections {
		if conn == closedConn {
			connIndex = i
			break
		}
	}

	// somehow we didn't find closed connection in the pool
	if connIndex < 0 {
		p.handleError(errors.New("closed connection was not found in the pool"))
		return
	}

	connsNum := len(p.connections)

	p.connections[connIndex] = p.connections[connsNum-1] // Copy last element to index connIndex.
	p.connections[connsNum-1] = nil                      // Erase last element
	p.connections = p.connections[:connsNum-1]           // Truncate slice.

	// if pool was closed, don't start recreate goroutine
	// should we return earlier and keep connection in the pool as it will be closed anyway?
	if p.isClosed {
		return
	}

	// initiate goroutine to reconnect to closedConn.Addr
	p.wg.Add(1)
	go p.recreateConnection(closedConn)
}

func (p *Pool) recreateConnection(closedConn *Connection) {
	defer p.wg.Done()

	var conn *Connection
	var err error
	var reconnectTime = p.Opts.BaseReconnectWait
	for {
		conn, err = p.Factory(closedConn.addr)
		if err != nil {
			p.handleError(fmt.Errorf("failed to re-create connection for %s: %w", closedConn.addr, err))
			return
		}

		// set own handler when connection is closed
		conn.SetOptions(ConnectionClosedHandler(p.handleClosedConnection))

		err = conn.Connect()

		if err == nil {
			break
		}

		p.handleError(fmt.Errorf("failed to reconnect to %s: %w", conn.addr, err))

		select {
		case <-time.After(reconnectTime):
			reconnectTime *= 2
			if reconnectTime > p.Opts.MaxReconnectWait {
				reconnectTime = p.Opts.MaxReconnectWait
			}
			continue
		case <-p.Done():
			// if pool is closed, let's get out of here
			return
		}
	}

	p.mu.Lock()
	p.connections = append(p.connections, conn)
	p.mu.Unlock()
}

// Close closes all connections in the pool
func (p *Pool) Close() error {
	p.mu.Lock()
	if p.isClosed {
		p.mu.Unlock()
		return nil
	}
	p.isClosed = true
	p.mu.Unlock()

	close(p.done)

	// wait for all re-connection goroutines to stop
	// they may add more connections to the pool
	// so we want to be sure that no new connections will appear
	// when we lock and start closing all connections
	p.wg.Wait()

	p.mu.Lock()

	// close all connections concurrently
	var wg sync.WaitGroup
	wg.Add(len(p.connections))

	for _, conn := range p.connections {
		go func(conn *Connection) {
			defer wg.Done()
			err := conn.Close()
			if err != nil {
				p.handleError(fmt.Errorf("closing connection on pool close: %w", err))
			}
		}(conn)

	}
	wg.Wait()

	// remove all connections
	p.connections = make([]*Connection, 0)
	p.mu.Unlock()

	return nil
}

func (p *Pool) Done() <-chan struct{} {
	return p.done
}

// filteredConnections returns filtered connections
func (p *Pool) filteredConnections() []*Connection {
	if p.Opts.ConnectionsFilter == nil {
		return p.Connections()
	}

	var conns []*Connection
	for _, conn := range p.Connections() {
		if p.Opts.ConnectionsFilter(conn) {
			conns = append(conns, conn)
		}
	}

	return conns
}

// IsDegraded returns true if pool is not full
func (p *Pool) IsDegraded() bool {
	return len(p.Addrs) != len(p.filteredConnections())
}

// IsUp returns true if at least one connection is in the pool
func (p *Pool) IsUp() bool {
	return len(p.filteredConnections()) > 0
}
