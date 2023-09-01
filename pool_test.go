package connection_test

import (
	"fmt"
	"net"
	"sync/atomic"
	"testing"

	"time"

	connection "github.com/moov-io/iso8583-connection"
	"github.com/stretchr/testify/require"
)

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

		addrs = append(addrs, server.Addr)
		servers = append(servers, server)

		// increase counter when server receives connection
		server.Server.AddConnectionHandler(func(_ net.Conn) {
			atomic.AddInt32(&connectionsCnt, 1)
		})
	}

	// close all servers on exit
	defer func() {
		for _, server := range servers {
			server.Close()
		}
	}()

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
		c, err := connection.New(
			addr,
			testSpec,
			readMessageLength,
			writeMessageLength,
			// set short connect timeout so we can test re-connects
			connection.ConnectTimeout(500*time.Millisecond),
		)
		if err != nil {
			return nil, fmt.Errorf("building iso 8583 connection: %w", err)
		}

		return c, nil
	}

	// And the pool of connections
	// one of our tests
	reconnectWait := 500 * time.Millisecond
	pool, err := connection.NewPool(factory, addrs, connection.PoolReconnectWait(reconnectWait))
	require.NoError(t, err)
	defer pool.Close()

	// pool is Down by default
	require.False(t, pool.IsUp())

	t.Run("Connect() establishes connections to all servers", func(t *testing.T) {
		// When we Connect pool
		err := pool.Connect()
		require.NoError(t, err)

		// Then pool builds and connects connections to all servers
		require.Eventually(t, func() bool {
			// we expect connectionsCnt counter to be incremented by both servers
			return atomic.LoadInt32(&connectionsCnt) == int32(serversToStart)
		}, 500*time.Millisecond, 50*time.Millisecond, "%d expected connections established, but got %d", serversToStart, atomic.LoadInt32(&connectionsCnt))

		// And pool has serversCnt connections
		require.Len(t, pool.Connections(), serversToStart)
	})

	t.Run("Get returns next connection from the pool looping over the list of connections", func(t *testing.T) {
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

	t.Run("when one of the servers is down it re-connects when server returns", func(t *testing.T) {
		connectionsCntBeforeServerShutdown := len(pool.Connections())

		// when we shutdown one of the servers
		servers[0].Close()

		// then we have one less connection
		require.Eventually(t, func() bool {
			return len(pool.Connections()) == connectionsCntBeforeServerShutdown-1
		}, 500*time.Millisecond, 50*time.Millisecond, "expect to have one less connection")

		// when we start server again
		server, err := NewTestServerWithAddr(servers[0].Addr)
		require.NoError(t, err)

		// so we will not forget to close server on exit
		servers[0] = server

		// then we have one more connection (the same as it was before
		// we shut down the server)
		require.Eventually(t, func() bool {
			return len(pool.Connections()) == connectionsCntBeforeServerShutdown
		}, 2000*time.Millisecond, 50*time.Millisecond, "expect to have one less connection")
	})

	t.Run("Close() closes all connections", func(t *testing.T) {
		require.NotZero(t, pool.Connections())

		// when we close the pool
		err := pool.Close()
		require.NoError(t, err)

		// then pool has no connections
		require.Zero(t, pool.Connections())
	})

	// when pool is closed
	t.Run("Get() returns error when no connections", func(t *testing.T) {
		// given pool is closed
		err := pool.Close()
		require.NoError(t, err)

		_, err = pool.Get()
		require.EqualError(t, err, "pool is closed")
	})

	t.Run("Connect() returns error when number or established connections is less than MinConnections", func(t *testing.T) {
		// we have only 2 servers running, so we can't establish 3 connections
		pool, err := connection.NewPool(factory, addrs, connection.PoolMinConnections(3))
		require.NoError(t, err)

		err = pool.Connect()
		require.Error(t, err)
		require.Contains(t, err.Error(), "minimum 3 connections is required, established: 2")
		require.Zero(t, len(pool.Connections()), "all connections should be closed")
	})

	t.Run("Connect() returns no error when established >= MinConnections and later re-establish failed connections", func(t *testing.T) {
		// when we shutdown one of the servers
		servers[0].Close()

		// we have only 1 of serversToStart servers running, so serversToStart-1 connection should be established
		pool, err := connection.NewPool(
			factory,
			addrs,
			connection.PoolMinConnections(1),
			connection.PoolReconnectWait(reconnectWait),
		)
		require.NoError(t, err)

		// when we connect
		err = pool.Connect()
		// no error should be returned here as MinConnections is 1
		require.NoError(t, err)
		defer pool.Close()

		require.Equal(t, len(pool.Connections()), serversToStart-1)

		// when server gets back
		server, err := NewTestServerWithAddr(servers[0].Addr)
		require.NoError(t, err)

		// return server back to the list so we will not forget to
		// close server on exit
		servers[0] = server

		// then connection should be established
		require.Eventually(t, func() bool {
			return len(pool.Connections()) == serversToStart
		}, 2000*time.Millisecond, 50*time.Millisecond, "expect to have one less connection")
	})

	t.Run("Get() returns filtered connections", func(t *testing.T) {
		var onConnectCalled int32
		// set status `online` (value) only for the first connection
		// keeping second connection with status `offline`
		onConnect := func(conn *connection.Connection) error {
			if atomic.AddInt32(&onConnectCalled, 1) == 1 {
				conn.SetStatus(connection.StatusOnline)
			}

			return nil
		}

		// And a factory method that will build connection for the pool
		factory := func(addr string) (*connection.Connection, error) {
			c, err := connection.New(
				addr,
				testSpec,
				readMessageLength,
				writeMessageLength,
				// set status `online` (value) only for the first connection
				connection.OnConnect(onConnect),
			)
			if err != nil {
				return nil, fmt.Errorf("building iso 8583 connection: %w", err)
			}

			return c, nil
		}

		// filterOnlineConnections returns only connections with status `online`
		filterOnlineConnections := func(conn *connection.Connection) bool {
			return conn.Status() == connection.StatusOnline
		}

		pool, err := connection.NewPool(
			factory,
			addrs,
			connection.PoolConnectionsFilter(filterOnlineConnections),
		)
		require.NoError(t, err)

		// when we connect
		err = pool.Connect()
		require.NoError(t, err)
		defer pool.Close()

		// we expect to have to connections
		require.Equal(t, len(pool.Connections()), serversToStart)

		// Get should return the same connection twice (filter not `online` connections)
		conn, err := pool.Get()
		require.NoError(t, err)
		require.Equal(t, conn.Status(), connection.StatusOnline)

		// Get should return the same connection as the first one (filter not `online` connections)
		for i := 0; i < serversToStart; i++ {
			connN, err := pool.Get()
			require.NoError(t, err)
			require.Equal(t, conn, connN)
		}

		// should be degraded
		require.True(t, pool.IsDegraded())
	})
}
