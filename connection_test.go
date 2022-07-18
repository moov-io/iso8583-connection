package connection_test

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moov-io/iso8583"
	connection "github.com/moov-io/iso8583-connection"
	"github.com/moov-io/iso8583-connection/server"
	"github.com/moov-io/iso8583/field"
	"github.com/stretchr/testify/require"
)

type baseFields struct {
	MTI          *field.String `index:"0"`
	TestCaseCode *field.String `index:"2"`
	STAN         *field.String `index:"11"`
}

var stan int
var stanMu sync.Mutex

func getSTAN() string {
	stanMu.Lock()
	defer stanMu.Unlock()

	stan++

	if stan > 999999 {
		stan = 1
	}

	return fmt.Sprintf("%06d", stan)
}

func TestClient_Connect(t *testing.T) {
	t.Run("unsecure connection", func(t *testing.T) {
		server, err := NewTestServer()
		require.NoError(t, err)
		defer server.Close()

		// our client can connect to the server
		c, err := connection.New(server.Addr, testSpec, readMessageLength, writeMessageLength)
		require.NoError(t, err)

		err = c.Connect()
		require.NoError(t, err)
		require.NoError(t, c.Close())
	})

	t.Run("with TLS", func(t *testing.T) {
		srv := http.Server{}
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)

		go func() {
			if err := srv.ServeTLS(ln, "./testdata/server.crt", "./testdata/server.key"); err != nil {
				require.ErrorIs(t, err, http.ErrServerClosed)
			}
		}()
		defer func() {
			// let's give client the chance to close the connection first
			time.Sleep(100 * time.Millisecond)
			srv.Close()
		}()

		c, err := connection.New(
			ln.Addr().String(),
			testSpec,
			readMessageLength,
			writeMessageLength,
			connection.ClientCert("./testdata/client.crt", "./testdata/client.key"),
			connection.RootCAs("./testdata/ca.crt"),
		)
		require.NoError(t, err)

		err = c.Connect()
		require.NoError(t, err)

		require.NoError(t, c.Close())
	})

	t.Run("no panic when Close before Connect", func(t *testing.T) {
		// our client can connect to the server
		c, err := connection.New("", testSpec, readMessageLength, writeMessageLength)
		require.NoError(t, err)

		require.NoError(t, c.Close())
	})
}

func TestClient_Send(t *testing.T) {
	server, err := NewTestServer()
	require.NoError(t, err)
	defer server.Close()

	t.Run("sends messages to server and receives responses", func(t *testing.T) {
		c, err := connection.New(server.Addr, testSpec, readMessageLength, writeMessageLength)
		require.NoError(t, err)

		err = c.Connect()
		require.NoError(t, err)

		// network management message
		message := iso8583.NewMessage(testSpec)
		err = message.Marshal(baseFields{
			MTI:          field.NewStringValue("0800"),
			TestCaseCode: field.NewStringValue(TestCaseDelayedResponse),
			STAN:         field.NewStringValue(getSTAN()),
		})
		require.NoError(t, err)

		// we can send iso message to the server
		response, err := c.Send(message)
		require.NoError(t, err)
		time.Sleep(1 * time.Second)

		mti, err := response.GetMTI()
		require.NoError(t, err)
		require.Equal(t, "0810", mti)

		require.NoError(t, c.Close())
	})

	t.Run("it returns ErrConnectionClosed when Close was called", func(t *testing.T) {
		c, err := connection.New(server.Addr, testSpec, readMessageLength, writeMessageLength)
		require.NoError(t, err)

		err = c.Connect()
		require.NoError(t, err)

		// network management message
		message := iso8583.NewMessage(testSpec)
		err = message.Marshal(baseFields{
			MTI:          field.NewStringValue("0800"),
			TestCaseCode: field.NewStringValue(TestCaseDelayedResponse),
			STAN:         field.NewStringValue(getSTAN()),
		})
		require.NoError(t, err)

		require.NoError(t, c.Close())

		_, err = c.Send(message)
		require.Equal(t, connection.ErrConnectionClosed, err)
	})

	t.Run("it returns ErrSendTimeout when response was not received during SendTimeout time", func(t *testing.T) {
		c, err := connection.New(server.Addr, testSpec, readMessageLength, writeMessageLength, connection.SendTimeout(100*time.Millisecond))
		require.NoError(t, err)

		err = c.Connect()
		require.NoError(t, err)
		defer c.Close()

		// regular network management message
		message := iso8583.NewMessage(testSpec)
		err = message.Marshal(baseFields{
			MTI:  field.NewStringValue("0800"),
			STAN: field.NewStringValue(getSTAN()),
		})
		require.NoError(t, err)

		_, err = c.Send(message)
		require.NoError(t, err)

		// network management message to test timeout
		message = iso8583.NewMessage(testSpec)
		err = message.Marshal(baseFields{
			MTI:          field.NewStringValue("0800"),
			TestCaseCode: field.NewStringValue(TestCaseDelayedResponse),
			STAN:         field.NewStringValue(getSTAN()),
		})
		require.NoError(t, err)

		_, err = c.Send(message)
		require.Equal(t, connection.ErrSendTimeout, err)
	})

	t.Run("it returns error when message does not have STAN", func(t *testing.T) {
		c, err := connection.New(server.Addr, testSpec, readMessageLength, writeMessageLength, connection.SendTimeout(100*time.Millisecond))
		require.NoError(t, err)

		err = c.Connect()
		require.NoError(t, err)
		defer c.Close()

		// regular network management message
		message := iso8583.NewMessage(testSpec)
		err = message.Marshal(baseFields{
			MTI: field.NewStringValue("0800"),
		})
		require.NoError(t, err)

		_, err = c.Send(message)
		require.EqualError(t, err, "creating request ID: STAN is missing")
	})

	t.Run("pending requests should complete after Close was called", func(t *testing.T) {
		c, err := connection.New(server.Addr, testSpec, readMessageLength, writeMessageLength)
		require.NoError(t, err)

		err = c.Connect()
		require.NoError(t, err)
		defer c.Close()

		var wg sync.WaitGroup
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(i int) {
				defer func() {
					wg.Done()
				}()

				// network management message
				message := iso8583.NewMessage(testSpec)
				err := message.Marshal(baseFields{
					MTI:          field.NewStringValue("0800"),
					TestCaseCode: field.NewStringValue(TestCaseDelayedResponse),
					STAN:         field.NewStringValue(getSTAN()),
				})
				require.NoError(t, err)

				response, err := c.Send(message)
				require.NoError(t, err)

				mti, err := response.GetMTI()
				require.NoError(t, err)
				require.Equal(t, "0810", mti)
			}(i)
		}

		// let's wait all messages to be sent
		time.Sleep(200 * time.Millisecond)

		// while server is waiting, we will close the connection
		require.NoError(t, c.Close())
		wg.Wait()
	})

	t.Run("responses received asynchronously", func(t *testing.T) {
		c, err := connection.New(server.Addr, testSpec, readMessageLength, writeMessageLength)
		require.NoError(t, err)

		err = c.Connect()
		require.NoError(t, err)
		defer c.Close()

		// send first message and tell server to respond in 500ms
		var (
			receivedSTANs []string
			wg            sync.WaitGroup
			mu            sync.Mutex
			stan1         string
			stan2         string
		)

		wg.Add(1)
		go func() {
			defer func() {
				wg.Done()
			}()

			message := iso8583.NewMessage(testSpec)
			err := message.Marshal(baseFields{
				MTI:          field.NewStringValue("0800"),
				TestCaseCode: field.NewStringValue(TestCaseDelayedResponse),
				STAN:         field.NewStringValue(getSTAN()),
			})
			require.NoError(t, err)

			response, err := c.Send(message)
			require.NoError(t, err)

			// put received STAN into slice so we can check the order
			stan1, err = response.GetString(11)
			require.NoError(t, err)
			mu.Lock()
			receivedSTANs = append(receivedSTANs, stan1)
			mu.Unlock()
		}()

		wg.Add(1)
		go func() {
			defer func() {
				wg.Done()
			}()

			// this message will be sent after the first one
			time.Sleep(100 * time.Millisecond)

			message := iso8583.NewMessage(testSpec)
			err := message.Marshal(baseFields{
				MTI:  field.NewStringValue("0800"),
				STAN: field.NewStringValue(getSTAN()),
			})
			require.NoError(t, err)

			response, err := c.Send(message)
			require.NoError(t, err)

			// put received STAN into slice so we can check the order
			stan2, err = response.GetString(11)
			require.NoError(t, err)
			mu.Lock()
			receivedSTANs = append(receivedSTANs, stan2)
			mu.Unlock()
		}()

		// let's wait all messages to be sent
		time.Sleep(200 * time.Millisecond)

		wg.Wait()
		require.NoError(t, c.Close())

		// we expect that response for the second message was received first
		require.Equal(t, receivedSTANs[0], stan2)

		// and that response for the first message was received second
		require.Equal(t, receivedSTANs[1], stan1)

	})

	t.Run("automatically sends ping messages after ping interval", func(t *testing.T) {
		// we create server instance here to isolate pings count
		server, err := NewTestServer()
		require.NoError(t, err)
		defer server.Close()

		pingHandler := func(c *connection.Connection) {
			pingMessage := iso8583.NewMessage(testSpec)
			err := pingMessage.Marshal(baseFields{
				MTI:          field.NewStringValue("0800"),
				TestCaseCode: field.NewStringValue(TestCasePingCounter),
				STAN:         field.NewStringValue(getSTAN()),
			})
			require.NoError(t, err)

			response, err := c.Send(pingMessage)
			require.NoError(t, err)

			mti, err := response.GetMTI()
			require.NoError(t, err)
			require.Equal(t, "0810", mti)
		}

		c, err := connection.New(server.Addr, testSpec, readMessageLength, writeMessageLength,
			connection.IdleTime(50*time.Millisecond),
			connection.PingHandler(pingHandler),
		)
		require.NoError(t, err)

		err = c.Connect()
		require.NoError(t, err)
		defer c.Close()

		// we expect that ping interval in 50ms has not passed yet
		// and server has not being pinged
		require.Equal(t, 0, server.ReceivedPings())

		time.Sleep(200 * time.Millisecond)

		require.True(t, server.ReceivedPings() > 0)
	})

	t.Run("it handles unrecognized responses", func(t *testing.T) {
		// unmatchedMessageHandler should be called for the second message
		// reply because connection.Send will return ErrSendTimeout and
		// reply will not be handled by the original caller
		unmatchedMessageHandler := func(c *connection.Connection, message *iso8583.Message) {
			mti, err := message.GetMTI()
			require.NoError(t, err)
			require.Equal(t, "0810", mti)

			pan, err := message.GetString(2)
			require.NoError(t, err)
			require.Equal(t, TestCaseDelayedResponse, pan)
		}

		c, err := connection.New(server.Addr, testSpec, readMessageLength, writeMessageLength,
			connection.SendTimeout(100*time.Millisecond),
			connection.InboundMessageHandler(unmatchedMessageHandler),
		)
		require.NoError(t, err)

		err = c.Connect()
		require.NoError(t, err)
		defer c.Close()

		// network management message to test timeout
		message := iso8583.NewMessage(testSpec)
		err = message.Marshal(baseFields{
			MTI:          field.NewStringValue("0800"),
			TestCaseCode: field.NewStringValue(TestCaseDelayedResponse),
			STAN:         field.NewStringValue(getSTAN()),
		})
		require.NoError(t, err)

		_, err = c.Send(message)
		require.Equal(t, connection.ErrSendTimeout, err)

		// let's sleep here to wait for the server response to our message
		// we should not panic :)
		time.Sleep(1 * time.Second)
	})

	// if server sends a message to the client with the STAN that client is
	// waiting for reply with, we should distinguish reply from incoming
	// message
	t.Run("it handles incoming messages with same STANs not as reply but as incoming message", func(t *testing.T) {
		originalSTAN := getSTAN()

		unmatchedMessageHandler := func(c *connection.Connection, message *iso8583.Message) {
			mti, err := message.GetMTI()
			require.NoError(t, err)
			require.Equal(t, "0800", mti)

			receivedSTAN, err := message.GetString(11)
			require.NoError(t, err)
			require.Equal(t, originalSTAN, receivedSTAN)

			message.MTI("0810")
			require.NoError(t, err)
			c.Reply(message)
		}

		c, err := connection.New(server.Addr, testSpec, readMessageLength, writeMessageLength,
			connection.SendTimeout(1*time.Second),
			connection.InboundMessageHandler(unmatchedMessageHandler),
		)
		require.NoError(t, err)

		err = c.Connect()
		require.NoError(t, err)
		defer c.Close()

		// network management message to test timeout
		message := iso8583.NewMessage(testSpec)
		err = message.Marshal(baseFields{
			MTI:          field.NewStringValue("0800"),
			TestCaseCode: field.NewStringValue(TestCaseSameSTANRequest),
			STAN:         field.NewStringValue(originalSTAN),
		})
		require.NoError(t, err)

		res, err := c.Send(message)
		require.NoError(t, err)

		mti, err := res.GetMTI()
		require.NoError(t, err)
		require.Equal(t, "0810", mti)
	})

	t.Run("should allow setting a custom connection without overwriting it in connect", func(t *testing.T) {
		closer := &TrackingRWCloser{}

		c, err := connection.NewFrom(closer, testSpec, readMessageLength, writeMessageLength, connection.SendTimeout(100*time.Millisecond))
		require.NoError(t, err)

		err = c.Connect()
		require.NoError(t, err)
		defer c.Close()

		msg := iso8583.NewMessage(testSpec)

		c.Reply(msg)

		require.Equal(t, closer.Used, true, "client didn't use custom connection")
	})

	// if server closed the connection, we want Send method to receive
	// ErrConnectionClosed and not ErrSendTimeout
	t.Run("pending requests get ErrConnectionClosed if server closed the connection", func(t *testing.T) {
		server, err := NewTestServer()
		require.NoError(t, err)
		defer server.Close()

		c, err := connection.New(server.Addr, testSpec, readMessageLength, writeMessageLength, connection.SendTimeout(500*time.Millisecond))
		require.NoError(t, err)

		err = c.Connect()
		require.NoError(t, err)
		defer c.Close()

		done := make(chan bool)

		// send message that will trigger server handler to sleep
		// trigger server to close connection
		go func() {
			// regardless of the test result, we have to finish parent t.Run
			defer func() {
				done <- true
			}()

			message := iso8583.NewMessage(testSpec)
			err := message.Marshal(baseFields{
				MTI:          field.NewStringValue("0800"),
				TestCaseCode: field.NewStringValue(TestCaseDelayedResponse),
				STAN:         field.NewStringValue(getSTAN()),
			})
			require.NoError(t, err)

			_, err = c.Send(message)

			// instead of ErrSendTimeout we want to receive
			// ErrConnectionClosed
			require.Equal(t, connection.ErrConnectionClosed, err)
		}()

		time.Sleep(50 * time.Millisecond)

		// trigger server to close connection
		message := iso8583.NewMessage(testSpec)
		err = message.Marshal(baseFields{
			MTI:          field.NewStringValue("0800"),
			TestCaseCode: field.NewStringValue(TestCaseCloseConnection),
			STAN:         field.NewStringValue(getSTAN()),
		})
		require.NoError(t, err)

		// we can get reply or connection can be closed here too
		// but because in test server we have a tiny delay before
		// we close the connection, we are safe here to get no err
		_, err = c.Send(message)
		require.NoError(t, err)

		<-done
	})

	t.Run("it returns ErrConnectionClosed when connection was closed by server", func(t *testing.T) {
		server, err := NewTestServer()
		require.NoError(t, err)
		defer server.Close()

		c, err := connection.New(server.Addr, testSpec, readMessageLength, writeMessageLength, connection.SendTimeout(2*time.Second))
		require.NoError(t, err)

		err = c.Connect()
		require.NoError(t, err)
		defer func() {
			c.Close()
		}()

		// trigger server to close connection
		message := iso8583.NewMessage(testSpec)
		err = message.Marshal(baseFields{
			MTI:          field.NewStringValue("0800"),
			TestCaseCode: field.NewStringValue(TestCaseCloseConnection),
			STAN:         field.NewStringValue(getSTAN()),
		})
		require.NoError(t, err)

		_, err = c.Send(message)
		require.NoError(t, err)

		// let's wait for connection to be close
		time.Sleep(100 * time.Millisecond)

		// because connection was closed (by server) we should receive
		// ErrConnectionClosed error
		message = iso8583.NewMessage(testSpec)
		err = message.Marshal(baseFields{
			MTI:          field.NewStringValue("0800"),
			TestCaseCode: field.NewStringValue(TestCaseReply),
			STAN:         field.NewStringValue(getSTAN()),
		})
		require.NoError(t, err)

		_, err = c.Send(message)
		require.Equal(t, connection.ErrConnectionClosed, err)
	})

	t.Run("ReadTimeoutHandler called after ReadTimeout elapses", func(t *testing.T) {
		server, err := NewTestServer()
		require.NoError(t, err)
		defer server.Close()

		readTimeoutHandler := func(c *connection.Connection) {
			// send a ping message on timeout
			pingMessage := iso8583.NewMessage(testSpec)
			err := pingMessage.Marshal(baseFields{
				MTI:          field.NewStringValue("0800"),
				TestCaseCode: field.NewStringValue(TestCasePingCounter),
				STAN:         field.NewStringValue(getSTAN()),
			})
			require.NoError(t, err)

			response, err := c.Send(pingMessage)
			// we may get error because test closed server and connection
			// it it's a connection closed error - that's ok. just return
			if err != nil && errors.Is(err, connection.ErrConnectionClosed) {
				return
			}
			require.NoError(t, err)

			mti, err := response.GetMTI()
			require.NoError(t, err)
			require.Equal(t, "0810", mti)
		}

		c, err := connection.New(server.Addr, testSpec, readMessageLength, writeMessageLength,
			connection.ReadTimeout(50*time.Millisecond),
			connection.ReadTimeoutHandler(readTimeoutHandler),
		)
		require.NoError(t, err)

		err = c.Connect()
		require.NoError(t, err)
		defer c.Close()

		// less than 50 ms timeout, should not have any pings
		require.Equal(t, 0, server.ReceivedPings())

		// time elapsed is greater than timeout, expect one ping
		require.Eventually(t, func() bool {
			return server.ReceivedPings() > 0
		}, 200*time.Millisecond, 50*time.Millisecond, "no ping messages were sent after read timeout")
	})
}

func TestClient_Options(t *testing.T) {
	t.Run("ErrorHandler is called when connection is closed", func(t *testing.T) {
		server, err := NewTestServer()
		require.NoError(t, err)

		c, err := connection.New(server.Addr, testSpec, readMessageLength, writeMessageLength)
		require.NoError(t, err)

		var callsCounter int32

		errorHandler := func(err error) {
			atomic.AddInt32(&callsCounter, 1)
		}
		c.SetOptions(connection.ErrorHandler(errorHandler))

		err = c.Connect()
		require.NoError(t, err)
		defer c.Close()
		require.Equal(t, int32(0), atomic.LoadInt32(&callsCounter))

		// when we close server
		server.Close()

		// then errorHandler should be called at least once
		require.Eventually(t, func() bool {
			return atomic.LoadInt32(&callsCounter) > 0
		}, 500*time.Millisecond, 50*time.Millisecond, "error handler was never called")

	})

	t.Run("ClosedHandler is called when connection is closed", func(t *testing.T) {
		server, err := NewTestServer()
		require.NoError(t, err)
		defer server.Close()

		c, err := connection.New(server.Addr, testSpec, readMessageLength, writeMessageLength, connection.SendTimeout(500*time.Millisecond))
		require.NoError(t, err)

		// to avoid data race with `isClosedHandlerCalled` when
		// handling conn closed and checking it in the
		// require.Eventually
		var m sync.Mutex
		var isClosedHandlerCalled bool
		var callsCounter int
		closedHandler := func(c *connection.Connection) {
			m.Lock()
			isClosedHandlerCalled = true
			callsCounter += 1
			m.Unlock()
		}
		c.SetOptions(connection.ConnectionClosedHandler(closedHandler))

		err = c.Connect()
		require.NoError(t, err)
		defer c.Close()

		// trigger server to close connection
		message := iso8583.NewMessage(testSpec)
		err = message.Marshal(baseFields{
			MTI:          field.NewStringValue("0800"),
			TestCaseCode: field.NewStringValue(TestCaseCloseConnection),
			STAN:         field.NewStringValue(getSTAN()),
		})
		require.NoError(t, err)

		// we can get reply or connection can be closed here too
		// but because in test server we have a tiny delay before
		// we close the connection, we are safe here to get no err
		_, err = c.Send(message)
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			m.Lock()
			defer m.Unlock()

			return isClosedHandlerCalled
		}, 200*time.Millisecond, 10*time.Millisecond)

		require.Equal(t, 1, callsCounter)
	})
}

type TrackingRWCloser struct{ Used bool }

func (m *TrackingRWCloser) Write(p []byte) (n int, err error) {
	m.Used = true
	return 0, nil
}
func (m *TrackingRWCloser) Read(p []byte) (n int, err error) {
	return 0, nil
}
func (m *TrackingRWCloser) Close() error {
	return nil
}

// interface guard
var _ io.ReadWriteCloser = (*TrackingRWCloser)(nil)

func TestClient_SetOptions(t *testing.T) {
	c, err := connection.New("", testSpec, readMessageLength, writeMessageLength)
	require.NoError(t, err)
	require.NotNil(t, c)

	require.Nil(t, c.Opts.PingHandler)
	require.Nil(t, c.Opts.InboundMessageHandler)
	require.Nil(t, c.Opts.TLSConfig)

	require.NoError(t, c.SetOptions(connection.InboundMessageHandler(func(c *connection.Connection, m *iso8583.Message) {})))
	require.NotNil(t, c.Opts.InboundMessageHandler)

	require.NoError(t, c.SetOptions(connection.PingHandler(func(c *connection.Connection) {})))
	require.NotNil(t, c.Opts.PingHandler)

	require.NoError(t, c.SetOptions(connection.RootCAs("./testdata/ca.crt")))
	require.NotNil(t, c.Opts.TLSConfig)
}

func BenchmarkSend100(b *testing.B) { benchmarkSend(100, b) }

func BenchmarkSend1000(b *testing.B) { benchmarkSend(1000, b) }

func BenchmarkSend10000(b *testing.B) { benchmarkSend(10000, b) }

func BenchmarkSend100000(b *testing.B) { benchmarkSend(100000, b) }

func benchmarkSend(m int, b *testing.B) {
	server := server.New(testSpec, readMessageLength, writeMessageLength)
	// start on random port
	err := server.Start("127.0.0.1:")
	if err != nil {
		b.Fatal("starting server: ", err)
	}

	c, err := connection.New(server.Addr, testSpec, readMessageLength, writeMessageLength)
	if err != nil {
		b.Fatal("creating client: ", err)
	}

	err = c.Connect()
	if err != nil {
		b.Fatal("connecting to the server: ", err)
	}

	b.ResetTimer()

	for n := 0; n < b.N; n++ {
		processMessages(b, m, c)
	}

	err = c.Close()
	if err != nil {
		b.Fatal("closing client: ", err)
	}
	server.Close()
}

// send/receive m messages
func processMessages(b *testing.B, m int, c *connection.Connection) {
	var wg sync.WaitGroup
	var gerr error

	for i := 0; i < m; i++ {
		wg.Add(1)
		go func() {
			defer func() {
				wg.Done()
			}()

			message := iso8583.NewMessage(testSpec)
			message.MTI("0800")

			_, err := c.Send(message)
			if err != nil {
				gerr = err
				return
			}
		}()
	}

	wg.Wait()
	if gerr != nil {
		b.Fatal("sending message: ", gerr)
	}
}
