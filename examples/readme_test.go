package examples_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"testing"
	"time"

	"github.com/moov-io/iso8583"
	connection "github.com/moov-io/iso8583-connection"
	"github.com/moov-io/iso8583-connection/examples"
	"github.com/moov-io/iso8583-connection/server"
	"github.com/moov-io/iso8583/specs"
	"github.com/stretchr/testify/require"
)

var spec = specs.Spec87ASCII

func startEchoServer(t *testing.T) *server.Server {
	t.Helper()

	echoHandler := func(c *connection.Connection, message *iso8583.Message) {
		mti, err := message.GetMTI()
		if err != nil || mti != "0800" {
			return
		}
		message.MTI("0810")
		if err := c.Reply(message); err != nil {
			log.Printf("reply: %v", err)
		}
	}

	srv := server.New(spec, examples.ReadMessageLength, examples.WriteMessageLength, connection.InboundMessageHandler(echoHandler))
	require.NoError(t, srv.Start("127.0.0.1:"))
	t.Cleanup(func() { srv.Close() })
	return srv
}

func TestREADMEUsage(t *testing.T) {
	srv := startEchoServer(t)

	pingHandler := func(c *connection.Connection) {
		ping := iso8583.NewMessage(spec)
		ping.MTI("0800")
		_ = ping.Field(11, "000002")
		_, err := c.Send(ping)
		if err != nil {
			log.Printf("ping: %v", err)
		}
	}

	inboundMessageHandler := func(c *connection.Connection, message *iso8583.Message) {
		mti, err := message.GetMTI()
		if err != nil {
			return
		}
		switch mti {
		case "0800":
			echo := iso8583.NewMessage(spec)
			echo.MTI("0810")
			if stan, err := message.GetString(11); err == nil && stan != "" {
				_ = echo.Field(11, stan)
			}
			_ = c.Reply(echo)
		default:
			log.Printf("unrecognized inbound MTI: %s", mti)
		}
	}

	connectionClosedHandler := func(c *connection.Connection) {
		log.Printf("connection closed: %s", c.Addr())
	}

	c, err := connection.New(srv.Addr, spec, examples.ReadMessageLength, examples.WriteMessageLength,
		connection.SendTimeout(100*time.Millisecond),
		connection.IdleTime(50*time.Millisecond),
		connection.PingHandler(pingHandler),
		connection.InboundMessageHandler(inboundMessageHandler),
		connection.ConnectionClosedHandler(connectionClosedHandler),
	)
	require.NoError(t, err)

	err = c.Connect()
	require.NoError(t, err)
	defer c.Close()

	message := iso8583.NewMessage(spec)
	message.MTI("0800")
	require.NoError(t, message.Field(11, "000001"))

	response, err := c.Send(message)
	require.NoError(t, err)

	mti, err := response.GetMTI()
	require.NoError(t, err)
	require.Equal(t, "0810", mti)
}

func TestREADMEConfigurationHandlers(t *testing.T) {
	srv := startEchoServer(t)

	pingHandler := func(c *connection.Connection) {
		ping := iso8583.NewMessage(spec)
		ping.MTI("0800")
		_ = ping.Field(11, "000003")
		_, _ = c.Send(ping)
	}

	inboundMessageHandler := func(c *connection.Connection, message *iso8583.Message) {
		mti, err := message.GetMTI()
		if err != nil {
			return
		}
		switch mti {
		case "0800":
			echo := iso8583.NewMessage(spec)
			echo.MTI("0810")
			_ = c.Reply(echo)
		default:
		}
	}

	c, err := connection.New(srv.Addr, spec, examples.ReadMessageLength, examples.WriteMessageLength,
		connection.SendTimeout(100*time.Millisecond),
		connection.IdleTime(50*time.Millisecond),
		connection.PingHandler(pingHandler),
		connection.InboundMessageHandler(inboundMessageHandler),
	)
	require.NoError(t, err)
	require.NoError(t, c.Connect())
	require.NoError(t, c.Close())
}

func TestREADMEContextCallbacks(t *testing.T) {
	srv := startEchoServer(t)

	signOnFunc := func(ctx context.Context, c *connection.Connection) error {
		_ = ctx
		_ = c
		return nil
	}
	signOffFunc := func(ctx context.Context, c *connection.Connection) error {
		_ = ctx
		_ = c
		return nil
	}

	c, err := connection.New(srv.Addr, spec, examples.ReadMessageLength, examples.WriteMessageLength,
		connection.SendTimeout(100*time.Millisecond),
		connection.IdleTime(50*time.Millisecond),
		connection.OnConnectCtx(func(ctx context.Context, c *connection.Connection) error {
			return signOnFunc(ctx, c)
		}),
		connection.OnCloseCtx(func(ctx context.Context, c *connection.Connection) error {
			return signOffFunc(ctx, c)
		}),
	)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, c.ConnectCtx(ctx))
	require.NoError(t, c.CloseCtx(ctx))
}

func TestREADMEPoolFactoryAndGetAddrOpts(t *testing.T) {
	getAddrOpts := func(addr string) []connection.Option {
		switch addr {
		case "127.0.0.1":
			return []connection.Option{
				connection.ConnectTimeout(500 * time.Millisecond),
			}
		case "127.0.0.2":
			return []connection.Option{
				connection.SendTimeout(500 * time.Millisecond),
			}
		}
		return nil
	}

	factory := func(addr string) (*connection.Connection, error) {
		opts := []connection.Option{
			connection.ConnectTimeout(500 * time.Millisecond),
			connection.OnConnect(func(c *connection.Connection) error {
				c.SetStatus(connection.StatusOnline)
				return nil
			}),
		}
		opts = append(opts, getAddrOpts(addr)...)

		c, err := connection.New(
			addr,
			spec,
			examples.ReadMessageLength,
			examples.WriteMessageLength,
			opts...,
		)
		if err != nil {
			return nil, fmt.Errorf("building iso8583 connection: %w", err)
		}
		return c, nil
	}

	srv := startEchoServer(t)
	addrs := []string{srv.Addr}

	filterOnlineConnections := func(conn *connection.Connection) bool {
		return conn.Status() == connection.StatusOnline
	}

	pool, err := connection.NewPool(
		factory,
		addrs,
		connection.PoolConnectionsFilter(filterOnlineConnections),
		connection.PoolMinConnections(1),
		connection.PoolReconnectWait(50*time.Millisecond),
		connection.PoolMaxReconnectWait(200*time.Millisecond),
		connection.PoolErrorHandler(func(err error) {
			log.Printf("pool error: %v", err)
		}),
	)
	require.NoError(t, err)

	require.NoError(t, pool.Connect())
	defer pool.Close()

	require.True(t, pool.IsUp())
	require.False(t, pool.IsDegraded())

	conn, err := pool.Get()
	require.NoError(t, err)

	msg := iso8583.NewMessage(spec)
	msg.MTI("0800")
	require.NoError(t, msg.Field(11, "000010"))

	reply, err := conn.Send(msg)
	require.NoError(t, err)
	mti, err := reply.GetMTI()
	require.NoError(t, err)
	require.Equal(t, "0810", mti)
}

type mtiSTANGenerator struct{}

func (mtiSTANGenerator) GenerateRequestID(message *iso8583.Message) (string, error) {
	mti, err := message.GetMTI()
	if err != nil {
		return "", err
	}
	// Request (x0xx) and response (x1xx) must share an ID.
	if len(mti) == 4 && mti[2] == '1' {
		mti = mti[:2] + "0" + mti[3:]
	}
	stan, err := message.GetString(11)
	if err != nil {
		return "", err
	}
	if stan == "" {
		return "", errors.New("STAN is missing")
	}
	return mti + "-" + stan, nil
}

func TestREADMERequestResponseMatching(t *testing.T) {
	srv := startEchoServer(t)

	t.Run("missing STAN", func(t *testing.T) {
		c, err := connection.New(srv.Addr, spec, examples.ReadMessageLength, examples.WriteMessageLength)
		require.NoError(t, err)
		require.NoError(t, c.Connect())
		defer c.Close()

		message := iso8583.NewMessage(spec)
		message.MTI("0800")

		_, err = c.Send(message)
		require.EqualError(t, err, "creating request ID: STAN is missing")
	})

	t.Run("custom request ID generator", func(t *testing.T) {
		c, err := connection.New(srv.Addr, spec, examples.ReadMessageLength, examples.WriteMessageLength,
			connection.SetRequestIDGenerator(mtiSTANGenerator{}),
		)
		require.NoError(t, err)
		require.NoError(t, c.Connect())
		defer c.Close()

		message := iso8583.NewMessage(spec)
		message.MTI("0800")
		require.NoError(t, message.Field(11, "000011"))

		response, err := c.Send(message)
		require.NoError(t, err)
		mti, err := response.GetMTI()
		require.NoError(t, err)
		require.Equal(t, "0810", mti)
	})
}

func TestREADMETLSOptionsCompile(t *testing.T) {
	// testdata lives at the module root; this only verifies the option types compile and load.
	c, err := connection.New("127.0.0.1:443", spec, examples.ReadMessageLength, examples.WriteMessageLength,
		connection.ClientCert("../testdata/client.crt", "../testdata/client.key"),
		connection.RootCAs("../testdata/ca.crt"),
	)
	require.NoError(t, err)
	require.NotNil(t, c.Opts.TLSConfig)
}
