package connection_test

import (
	"fmt"
	"net"
	"sync/atomic"
	"testing"

	"time"

	"github.com/moov-io/iso8583"
	connection "github.com/moov-io/iso8583-connection"
	"github.com/moov-io/iso8583/field"
	"github.com/stretchr/testify/require"
)

// TODO
// pass retries, wait time, etc.
// new Pool should have callback for when it was closed (when all connections were closed)
// Pool when it creates connection from factory should set own handler for closed connection
func TestPool(t *testing.T) {
	// Given
	// servers started
	var servers []*testServer

	// servers to start
	serversToStart := 2
	// list of addresses of the servers
	var addrs []string
	// each server on connect will increment connectionsCnt counter
	var connectionsCnt int32
	// start all servers
	for i := 0; i < serversToStart; i++ {
		server, err := NewTestServer()
		require.NoError(t, err)
		defer server.Close()

		addrs = append(addrs, server.Addr)
		servers = append(servers, server)

		// increase counter when server receives connection
		server.Server.AddConnectionHandler(func(_ net.Conn) {
			atomic.AddInt32(&connectionsCnt, 1)
		})
	}

	// And a factory method that will build connection for the pool
	factory := func(addr string) (*connection.Connection, error) {
		// all our addresses have same configs, but if you need to use
		// different TLS config, you can define config out of this
		// function like:
		// tlsConfigs := map[string]*tls.Config{
		// 	"127.0.0.1": &tls.Config{},
		// 	"127.0.0.2": &tls.Config{},
		// }
		// and inside factory, just pick your config based on the address
		// tlsConfig := tlsConfigs[addr]

		opts := []connection.Option{
			connection.SendTimeout(5 * time.Second),
			connection.IdleTime(5 * time.Second),
		}

		c, err := connection.New(
			addr,
			testSpec,
			readMessageLength,
			writeMessageLength,
			opts...,
		)
		if err != nil {
			return nil, fmt.Errorf("building iso 8583 connection: %w", err)
		}

		return c, nil
	}

	// And the pool of connections
	pool := connection.NewPool(factory, addrs)

	t.Run("Connect() establishes all connections", func(t *testing.T) {
		// When we Connect pool
		err := pool.Connect()
		require.NoError(t, err)

		// Then pool builds and connects connections to all servers
		require.Eventually(t, func() bool {
			// we expect connectionsCnt counter to be incremented by both servers
			return connectionsCnt == int32(serversToStart)
		}, 500*time.Millisecond, 50*time.Millisecond, "%d expected connections established, but got %d", serversToStart, connectionsCnt)

		// And pool has serversCnt connections
		require.Len(t, pool.Connections(), serversToStart)
	})

	t.Run("Get() returns next connection from the pool looping over the list of connections", func(t *testing.T) {
		// When we call Get() len(pool.Connections()) * N times
		var n = 3
		var connections []*connection.Connection
		for i := 0; i < len(pool.Connections())*n; i++ {
			conn, err := pool.Get()
			require.NoError(t, err)
			connections = append(connections, conn)
		}

		// Then each connection should be returned N times
		counter := map[*connection.Connection]int{}
		for _, conn := range connections {
			if conn == nil {
				continue
			}
			counter[conn]++
		}

		require.NotEmpty(t, counter)

		for _, returns := range counter {
			require.Equal(t, n, returns)
		}
	})

	t.Run("when one of the connections is closed it creates new connection for the same address", func(t *testing.T) {
		numOfConnectionsBeforeClose := len(pool.Connections())

		// trigger server to close connection
		message := iso8583.NewMessage(testSpec)
		err := message.Marshal(baseFields{
			MTI:          field.NewStringValue("0800"),
			TestCaseCode: field.NewStringValue(TestCaseCloseConnection),
			STAN:         field.NewStringValue(getSTAN()),
		})
		require.NoError(t, err)

		conn, err := pool.Get()
		require.NoError(t, err)

		_, err = conn.Send(message)
		require.NoError(t, err)

		connectionsCntBeforeReConnect := connectionsCnt

		// let our pool to detect closed connection and remove it from the pool
		require.Eventually(t, func() bool {
			return len(pool.Connections()) == numOfConnectionsBeforeClose-1
		}, 500*time.Millisecond, 50*time.Millisecond, "expected one less connection, total %d connections, expected: %d", len(pool.Connections()), numOfConnectionsBeforeClose-1)

		// Then pool should recreate connection after p.WaitBeforeReconnect
		require.Eventually(t, func() bool {
			// we expect connectionsCnt counter to be incremented by both servers
			return connectionsCnt == connectionsCntBeforeReConnect+1
		}, 500*time.Millisecond, 50*time.Millisecond, "expected %d connections count, but got %d", connectionsCntBeforeReConnect+1, connectionsCnt)

		// And pool has serversCnt connections
		require.Len(t, pool.Connections(), serversToStart)

		// let's close first server - we should see how we try to re-connect
		servers[0].Close()

		time.Sleep(20 * time.Second)
	})

	t.Run("when all connections are closed it calls ConnectionsClosedHandler", func(t *testing.T) {
	})

	// close them concurrently
	t.Run("Close() closes all connections", func(t *testing.T) {

	})

	// when no connections, Get returns error ErrorEmptyPool

	// when pool is closed
	t.Run("Get() returns error ErrorPoolClosed", func(t *testing.T) {})
	t.Run("Connections() returns empty slice", func(t *testing.T) {})
	t.Run("Connect() establishes all coonnections", func(t *testing.T) {})
}
