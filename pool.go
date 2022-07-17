package connection

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
)

type ConnectionFactoryFunc func(addr string) (*Connection, error)

type Pool struct {
	Factory ConnectionFactoryFunc
	Addrs   []string

	mu          sync.Mutex
	connections []*Connection
	connIndex   int

	done chan struct{}
}

// Pool - provides connection to the clients. It checks the health of the
// connection and if connection is not healthy it removes it from the pool and
// in the background will try to create new connection so pool will be full.
// Pool needs a list of IP addresses (connection configs) to create connections for.

func NewPool(factory ConnectionFactoryFunc, addrs []string) *Pool {
	// fill the connection pool
	// check health of the connections in the goroutine

	return &Pool{
		Factory: factory,
		Addrs:   addrs,
		done:    make(chan struct{}),
	}
}

// Connect creates poll of connections by calling Factory method and connect them all
func (p *Pool) Connect() error {
	p.mu.Lock()
	defer p.mu.Unlock()

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
			return fmt.Errorf("connecting to %s: %w", conn.Addr, err)
		}

		p.connections = append(p.connections, conn)
	}

	return nil
}

// Get returns connection from the pool
func (p *Pool) Connections() []*Connection {
	p.mu.Lock()
	defer p.mu.Unlock()

	// create new slice, as p.connections can be changed
	var conns []*Connection
	conns = append(conns, p.connections...)

	return conns
}

// Get returns connection from the pool
func (p *Pool) Get() (*Connection, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.connections == nil || len(p.connections) == 0 {
		return nil, errors.New("no connections in the pool")
	}

	conn := p.connections[p.connIndex]
	p.connIndex++

	// reset index
	if p.connIndex >= len(p.connections) {
		p.connIndex = 0
	}

	return conn, nil
}

// when connection is closed pool will remove it from the pool of connections
// and will start goroutine to create new connection for the address
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
	// TODO: notify about error
	if connIndex < 0 {
		return
	}

	p.connections[connIndex] = p.connections[len(p.connections)-1] // Copy last element to index connIndex.
	p.connections[len(p.connections)-1] = nil                      // Erase last element
	p.connections = p.connections[:len(p.connections)-1]           // Truncate slice.

	// initiate goroutine to reconnect to closedConn.Addr
	go p.recreateConnection(closedConn)
}

func (p *Pool) recreateConnection(closedConn *Connection) {
	<-time.After(100 * time.Millisecond)
	conn, err := p.Factory(closedConn.Addr)
	if err != nil {
		// TODO ups, no way to notify about errors...
		log.Printf("failed to re-create connection for %s: %s", closedConn.Addr, err)
		return
	}

	for {
		fmt.Println("loop until err != nil")
		fmt.Println("connecting...")
		err = conn.Connect()
		if err == nil {
			break
		}
		fmt.Println("connected")

		log.Printf("failed to re-connect to %s: %s", conn.Addr, err)
		select {
		case <-time.After(5 * time.Second):
			// wait for 5 seconds before the next attemp to connect
			continue
		case <-p.Done():
			// if pool is closed, let's get our of here
			return
		}
	}

	p.mu.Lock()
	p.connections = append(p.connections, conn)
	p.mu.Unlock()
	// we should stop trying if pool is closed
	// we should stop trying after N attempts (-1 continue endlessly)
	// we should wait before reconnects
}

// Close closes all connections in the pool
func (p *Pool) Close() error {
	close(p.done)

	return nil
}

func (p *Pool) Done() <-chan struct{} {
	return p.done
}
