package connection_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moov-io/iso8583"
	connection "github.com/moov-io/iso8583-connection"
	"github.com/moov-io/iso8583/encoding"
	iso8583Errors "github.com/moov-io/iso8583/errors"
	"github.com/moov-io/iso8583/field"
	"github.com/moov-io/iso8583/prefix"
	"github.com/stretchr/testify/require"
)

type baseFields struct {
	MTI          *field.String `index:"0"`
	TestCaseCode *field.String `index:"2"`
	STAN         *field.String `index:"11"`
}

var (
	stan   int
	stanMu sync.Mutex
)

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
		srv := http.Server{
			ReadHeaderTimeout: 1 * time.Second,
		}
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

	t.Run("Connect times out", func(t *testing.T) {
		// using a non-routable IP address per https://stackoverflow.com/questions/100841/artificially-create-a-connection-timeout-error
		c, err := connection.New("10.0.0.0:50000", testSpec, readMessageLength, writeMessageLength, connection.ConnectTimeout(2*time.Second))
		require.NoError(t, err)

		start := time.Now()
		err = c.Connect()
		end := time.Now()
		delta := end.Sub(start)

		require.Error(t, err)
		// Test against triple the timeout value to be safe, which should also be well under any OS specific socket timeout
		// Realistically, the delta should nearly always be exactly 2 seconds
		require.Less(t, delta, 6*time.Second)
		// Also confirm the timeout did not happen in less than 2 seconds
		require.Less(t, 2*time.Second, delta)

		require.NoError(t, c.Close())
	})

	t.Run("no panic when Close before Connect", func(t *testing.T) {
		// our client can connect to the server
		c, err := connection.New("", testSpec, readMessageLength, writeMessageLength)
		require.NoError(t, err)

		require.NoError(t, c.Close())
	})

	t.Run("OnConnect is called", func(t *testing.T) {
		server, err := NewTestServer()
		require.NoError(t, err)
		defer server.Close()

		var onConnectCalled int32
		onConnect := func(c *connection.Connection) error {
			// increase the counter
			atomic.AddInt32(&onConnectCalled, 1)
			return nil
		}

		c, err := connection.New(server.Addr, testSpec, readMessageLength, writeMessageLength, connection.OnConnect(onConnect))
		require.NoError(t, err)

		err = c.Connect()
		require.NoError(t, err)
		defer c.Close()

		// eventually the onConnectCounter should be 1
		require.Eventually(t, func() bool {
			return atomic.LoadInt32(&onConnectCalled) == 1
		}, 100*time.Millisecond, 20*time.Millisecond, "onConnect should be called")
	})

	t.Run("OnConnectCtx is called", func(t *testing.T) {
		server, err := NewTestServer()
		require.NoError(t, err)
		defer server.Close()

		var onConnectCalled int32
		onConnectCtx := func(ctx context.Context, c *connection.Connection) error {
			// increase the counter
			atomic.AddInt32(&onConnectCalled, 1)
			return nil
		}

		c, err := connection.New(server.Addr, testSpec, readMessageLength, writeMessageLength, connection.OnConnectCtx(onConnectCtx))
		require.NoError(t, err)

		err = c.ConnectCtx(context.Background())
		require.NoError(t, err)
		defer c.Close()

		// eventually the onConnectCounter should be 1
		require.Eventually(t, func() bool {
			return atomic.LoadInt32(&onConnectCalled) == 1
		}, 100*time.Millisecond, 20*time.Millisecond, "onConnect should be called")
	})

	t.Run("OnClose is called", func(t *testing.T) {
		server, err := NewTestServer()
		require.NoError(t, err)
		defer server.Close()

		var onClosedCalled int32
		onClose := func(c *connection.Connection) error {
			// increase the counter
			atomic.AddInt32(&onClosedCalled, 1)
			return nil
		}

		c, err := connection.New(server.Addr, testSpec, readMessageLength, writeMessageLength, connection.OnClose(onClose))
		require.NoError(t, err)

		err = c.Close()
		require.NoError(t, err)

		// eventually the onClosedCalled should be 1
		require.Eventually(t, func() bool {
			return atomic.LoadInt32(&onClosedCalled) == 1
		}, 100*time.Millisecond, 20*time.Millisecond, "onClose should be called")
	})

	t.Run("OnCloseCtx is called", func(t *testing.T) {
		server, err := NewTestServer()
		require.NoError(t, err)
		defer server.Close()

		var onClosedCalled int32
		onCloseCtx := func(ctx context.Context, c *connection.Connection) error {
			// increase the counter
			atomic.AddInt32(&onClosedCalled, 1)
			return nil
		}

		c, err := connection.New(server.Addr, testSpec, readMessageLength, writeMessageLength, connection.OnCloseCtx(onCloseCtx))
		require.NoError(t, err)

		err = c.CloseCtx(context.Background())
		require.NoError(t, err)

		// eventually the onClosedCalled should be 1
		require.Eventually(t, func() bool {
			return atomic.LoadInt32(&onClosedCalled) == 1
		}, 100*time.Millisecond, 20*time.Millisecond, "onClose should be called")
	})

	t.Run("when OnCloseCtx returns error, we still close the connection", func(t *testing.T) {
		server, err := NewTestServer()
		require.NoError(t, err)
		defer server.Close()

		var onClosedCalled int32
		onCloseCtx := func(ctx context.Context, c *connection.Connection) error {
			// increase the counter
			atomic.AddInt32(&onClosedCalled, 1)
			return errors.New("error from on close handler")
		}

		var onErrCalled int32
		errHandler := func(err error) {
			atomic.AddInt32(&onErrCalled, 1)
			require.Contains(t, err.Error(), "error from on close handler")
		}

		c, err := connection.New(
			server.Addr,
			testSpec,
			readMessageLength,
			writeMessageLength,
			connection.ErrorHandler(errHandler),
			connection.OnCloseCtx(onCloseCtx),
		)
		require.NoError(t, err)

		err = c.CloseCtx(context.Background())
		require.NoError(t, err)

		// eventually the onClosedCalled should be 1
		require.Eventually(t, func() bool {
			return atomic.LoadInt32(&onClosedCalled) == 1
		}, 100*time.Millisecond, 20*time.Millisecond, "onClose should be called")
	})
}

func TestClient_Write(t *testing.T) {
	server, err := NewTestServer()
	require.NoError(t, err)
	defer server.Close()

	t.Run("write into the connection", func(t *testing.T) {
		var called atomic.Int32
		inboundMessageHandler := func(c *connection.Connection, message *iso8583.Message) {
			called.Add(1)

			mti, err := message.GetMTI()
			require.NoError(t, err)
			require.Equal(t, "0810", mti)
		}

		// we should be able to write any bytes to the server
		c, err := connection.New(server.Addr, testSpec, readMessageLength, writeMessageLength, connection.InboundMessageHandler(inboundMessageHandler))
		require.NoError(t, err)
		defer c.Close()

		err = c.Connect()
		require.NoError(t, err)

		// let's create data to write to the server, we will prepare header and
		// packed message

		// network management message
		message := iso8583.NewMessage(testSpec)
		err = message.Marshal(baseFields{
			MTI:          field.NewStringValue("0800"),
			TestCaseCode: field.NewStringValue(TestCaseReply),
			STAN:         field.NewStringValue(getSTAN()),
		})
		require.NoError(t, err)

		packed, err := message.Pack()
		require.NoError(t, err)

		// prepare header
		header := &bytes.Buffer{}
		_, err = writeMessageLength(header, len(packed))
		require.NoError(t, err)

		// combine header and message
		data := append(header.Bytes(), packed...)

		// write the data directly to the connection
		n, err := c.Write(data)

		require.NoError(t, err)
		require.Equal(t, len(data), n)

		// we should expect to get reply, but as we are not using Send method,
		// the reply will be handled by InboundMessageHandler
		require.Eventually(t, func() bool {
			return called.Load() == 1
		}, 100*time.Millisecond, 20*time.Millisecond, "inboundMessageHandler should be called")
	})

	t.Run("write into the closed connection ", func(t *testing.T) {
		c, err := connection.New(server.Addr, testSpec, readMessageLength, writeMessageLength)
		require.NoError(t, err)
		defer c.Close()

		err = c.Connect()
		require.NoError(t, err)

		c.Close()

		_, err = c.Write([]byte("hello"))
		require.Error(t, err)
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

	t.Run("returns PackError when it fails to pack message", func(t *testing.T) {
		c, err := connection.New(server.Addr, testSpec, readMessageLength, writeMessageLength)
		require.NoError(t, err)
		defer c.Close()

		err = c.Connect()
		require.NoError(t, err)

		message := iso8583.NewMessage(testSpec)

		// setting MTI to 1 digit will cause PackError as it should be
		// 4 digits
		message.MTI("1")

		// we should set STAN as we check it before we pack the message
		err = message.Field(11, "123456")
		require.NoError(t, err)

		// when we send the message
		_, err = c.Send(message)

		// then Send should return PackError
		require.Error(t, err)

		var packError *iso8583Errors.PackError
		require.ErrorAs(t, err, &packError)
	})

	t.Run("returns UnpackError with RawMessage when it fails to unpack message", func(t *testing.T) {
		// Given
		// connection with specification different from server
		// field 63 is not defined in the following spec
		var differentSpec *iso8583.MessageSpec = &iso8583.MessageSpec{
			Name: "spec with different fields",
			Fields: map[int]field.Field{
				0: field.NewString(&field.Spec{
					Length:      4,
					Description: "Message Type Indicator",
					Enc:         encoding.ASCII,
					Pref:        prefix.ASCII.Fixed,
				}),
				1: field.NewBitmap(&field.Spec{
					Length:      8,
					Description: "Bitmap",
					Enc:         encoding.Binary,
					Pref:        prefix.Binary.Fixed,
				}),
				2: field.NewString(&field.Spec{
					Length:      3,
					Description: "Test Case Code",
					Enc:         encoding.ASCII,
					Pref:        prefix.ASCII.Fixed,
				}),
				11: field.NewString(&field.Spec{
					Length:      6,
					Description: "Systems Trace Audit Number (STAN)",
					Enc:         encoding.ASCII,
					Pref:        prefix.ASCII.Fixed,
				}),
			},
		}
		c, err := connection.New(server.Addr, differentSpec, readMessageLength, writeMessageLength)
		require.NoError(t, err)

		var handledError error
		var mu sync.Mutex

		c.SetOptions(
			connection.ErrorHandler(func(err error) {
				mu.Lock()
				defer mu.Unlock()
				handledError = err
			}),
			connection.SendTimeout(100*time.Millisecond),
		)

		// and connection
		err = c.Connect()
		require.NoError(t, err)

		// and message that will trigger response with extra field that differentSpec does not have
		message := iso8583.NewMessage(differentSpec)
		err = message.Marshal(baseFields{
			MTI:          field.NewStringValue("0800"),
			TestCaseCode: field.NewStringValue(TestCaseRespondWithExtraField),
			STAN:         field.NewStringValue(getSTAN()),
		})
		require.NoError(t, err)

		// when we send iso message to the server
		// we do not wait for the response, as message will timeout
		// c.Close() will wait for c.Send to complete
		go func() {
			_, err := c.Send(message)
			require.ErrorIs(t, err, connection.ErrSendTimeout)
		}()

		// then we get ErrUnpack into the error handler
		require.Eventually(t, func() bool {
			mu.Lock()
			defer mu.Unlock()

			var unpackErr *iso8583Errors.UnpackError
			if errors.As(handledError, &unpackErr) {
				require.EqualError(t, handledError, "failed to read message from connection")
				require.EqualError(t, unpackErr, "failed to unpack field 63: no specification found")
				require.NotEmpty(t, unpackErr.RawMessage)
				return true
			}
			return false
		}, 1*time.Second, 100*time.Millisecond, "expect handledError to be set into UnpackError")

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

	t.Run("it returns RejectedMessageError when message was rejected", func(t *testing.T) {
		c, err := connection.New(server.Addr, testSpec, readMessageLength, writeMessageLength)
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

		// we send the message in the background and it will wait for the response
		// until SendTimeout is reached or message is rejected
		var sendErr error
		waitCh := make(chan struct{})
		go func() {
			_, sendErr = c.Send(message)
			close(waitCh)
		}()

		// while we are waiting for the response, we reject the message
		// directly
		// let's wait a bit to make sure that message was sent
		time.Sleep(150 * time.Millisecond)
		err = c.RejectMessage(message, fmt.Errorf("message was rejected"))
		require.NoError(t, err)

		// now we wait for the Send to finish and check the error
		<-waitCh

		// now we can check the error type returned by Send
		var rejectedMessageError *connection.RejectedMessageError
		require.ErrorAs(t, sendErr, &rejectedMessageError)

		// we can also unwrap the error to get the original error
		rejectErr := rejectedMessageError.Unwrap()
		require.EqualError(t, rejectErr, "message was rejected")
	})

	t.Run("it does not return ErrSendTimeout when longer SendTimeout is set for Send", func(t *testing.T) {
		c, err := connection.New(server.Addr, testSpec, readMessageLength, writeMessageLength, connection.SendTimeout(600*time.Millisecond))
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

		// connection is configured to use 600ms for SendTimeout, it means that
		// all Send calls will not timeout, as test server is waiting for 500ms
		// before sending response. So, to test SendTimeout for Send method
		// we need to set it to smaller value than 500ms.
		_, err = c.Send(message, connection.SendTimeout(100*time.Millisecond))
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

		require.Equal(t, closer.Used(), true, "client didn't use custom connection")
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

		// let's wait for server and client to connect
		// before we close the server
		time.Sleep(100 * time.Millisecond)

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
		_, err = c.Send(message)
		if err != nil && !errors.Is(err, connection.ErrConnectionClosed) {
			require.NoError(t, err)
		}

		require.Eventually(t, func() bool {
			m.Lock()
			defer m.Unlock()

			return isClosedHandlerCalled
		}, 200*time.Millisecond, 10*time.Millisecond)

		require.Equal(t, 1, callsCounter)
	})

	t.Run("ConnectionEstablishedHandler is called when connection is connected", func(t *testing.T) {
		server, err := NewTestServer()
		require.NoError(t, err)
		defer server.Close()

		c, err := connection.New(server.Addr, testSpec, readMessageLength, writeMessageLength, connection.SendTimeout(500*time.Millisecond))
		require.NoError(t, err)

		// to avoid data race with `isClosedHandlerCalled` when
		// handling conn closed and checking it in the
		// require.Eventually
		var m sync.Mutex
		var isConnectedHandlerCalled bool
		var callsCounter int
		connectedHandler := func(c *connection.Connection) {
			m.Lock()
			isConnectedHandlerCalled = true
			callsCounter += 1
			m.Unlock()
		}
		c.SetOptions(connection.ConnectionEstablishedHandler(connectedHandler))

		err = c.Connect()
		require.NoError(t, err)
		defer c.Close()

		require.Eventually(t, func() bool {
			m.Lock()
			defer m.Unlock()

			return isConnectedHandlerCalled
		}, 200*time.Millisecond, 10*time.Millisecond)

		require.Equal(t, 1, callsCounter)
	})
}

func TestClientWithMessageReaderAndWriter(t *testing.T) {
	server, err := NewTestServer()
	require.NoError(t, err)
	defer server.Close()

	// create client with custom message reader and writer
	msgIO := &messageIO{Spec: testSpec}

	t.Run("send and receive iso 8583 message", func(t *testing.T) {
		c, err := connection.New(server.Addr, nil, nil, nil,
			connection.SetMessageReader(msgIO),
			connection.SetMessageWriter(msgIO),
			connection.ErrorHandler(func(err error) {
				require.NoError(t, err)
			}),
		)
		require.NoError(t, err)

		err = c.Connect()
		require.NoError(t, err)

		// network management message
		message := iso8583.NewMessage(testSpec)
		err = message.Marshal(baseFields{
			MTI:  field.NewStringValue("0800"),
			STAN: field.NewStringValue(getSTAN()),
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

	t.Run("continue to read messages if message reader returns nil message and nil error", func(t *testing.T) {
		// skipMessage is used to skip first message
		skipMessage := &atomic.Bool{}
		messagesReceived := &atomic.Int32{}

		// create connection with custom message reader so we can
		// control what message is returned. msgReader will return nil
		// message and nil error for the first message and return real
		// message for the second message
		msgReader := &messageReader{
			MessageReader: func(r io.Reader) (*iso8583.Message, error) {
				msg, err := msgIO.ReadMessage(r)
				if err != nil {
					return nil, err
				}

				messagesReceived.Add(1)

				if skipMessage.Load() {
					return nil, nil
				}

				return msg, nil
			},
		}

		c, err := connection.New(server.Addr, nil, nil, nil,
			connection.SetMessageReader(connection.MessageReader(msgReader)),
			connection.SetMessageWriter(msgIO),
			connection.ErrorHandler(func(err error) {
				require.NoError(t, err)
			}),
			connection.SendTimeout(500*time.Millisecond),
		)
		require.NoError(t, err)

		err = c.Connect()
		require.NoError(t, err)
		defer c.Close()

		// set skipMessage to true so readMessage will return nil message
		skipMessage.Store(true)

		// send first message
		message := iso8583.NewMessage(testSpec)
		err = message.Marshal(baseFields{
			MTI:  field.NewStringValue("0800"),
			STAN: field.NewStringValue(getSTAN()),
		})
		require.NoError(t, err)

		// this message will timeout because we are skipping its reply
		_, err = c.Send(message)
		require.ErrorIs(t, err, connection.ErrSendTimeout)

		// set skipMessage to false to read message as usual
		skipMessage.Store(false)

		message = iso8583.NewMessage(testSpec)
		err = message.Marshal(baseFields{
			MTI:  field.NewStringValue("0800"),
			STAN: field.NewStringValue(getSTAN()),
		})
		require.NoError(t, err)

		_, err = c.Send(message)
		require.NoError(t, err)

		// we should receive two replies even if first message was
		// skipped
		require.Equal(t, int32(2), messagesReceived.Load())
	})
}

// messageIO is a helper struct to read/write iso8583 messages from/to
// io.Reader/io.Writer
type messageIO struct {
	Spec *iso8583.MessageSpec
}

func (m *messageIO) ReadMessage(r io.Reader) (*iso8583.Message, error) {
	// read 2 bytes header
	h := &header{}
	err := binary.Read(r, binary.BigEndian, h)
	if err != nil {
		return nil, fmt.Errorf("failed to read message length: %w", err)
	}

	// read message
	rawMessage := make([]byte, h.Length)
	_, err = io.ReadFull(r, rawMessage)
	if err != nil {
		return nil, fmt.Errorf("failed to read message: %w", err)
	}

	// unpack message
	message := iso8583.NewMessage(m.Spec)
	err = message.Unpack(rawMessage)
	if err != nil {
		return nil, fmt.Errorf("failed to unpack message: %w", err)
	}

	return message, nil
}

func (m *messageIO) WriteMessage(w io.Writer, message *iso8583.Message) error {
	// pack message
	rawMessage, err := message.Pack()
	if err != nil {
		return fmt.Errorf("failed to pack message: %w", err)
	}

	// create header with message length
	if len(rawMessage) > math.MaxUint16 {
		return fmt.Errorf("message length is out of uint16 range: %d", len(rawMessage))
	}
	//nolint:gosec // disable G115 as it's a false positive
	h := header{
		Length: uint16(len(rawMessage)),
	}

	// write header
	err = binary.Write(w, binary.BigEndian, h)
	if err != nil {
		return fmt.Errorf("failed to write message length: %w", err)
	}

	// write message
	_, err = w.Write(rawMessage)
	if err != nil {
		return fmt.Errorf("failed to write message: %w", err)
	}

	return nil
}

// messageReader is a helper struct to read iso8583 messages from
// io.Reader with custom message reader that test can control
type messageReader struct {
	MessageReader func(r io.Reader) (*iso8583.Message, error)
}

func (r *messageReader) ReadMessage(reader io.Reader) (*iso8583.Message, error) {
	return r.MessageReader(reader)
}

// header is 2 bytes length of the message
type header struct {
	Length uint16
}

func TestConnection(t *testing.T) {
	t.Run("Status", func(t *testing.T) {
		c, err := connection.New("1.1.1.1", nil, nil, nil)

		require.NoError(t, err)
		require.Empty(t, c.Status())

		c.SetStatus(connection.StatusOnline)
		require.Equal(t, connection.StatusOnline, c.Status())
	})
}

type TrackingRWCloser struct {
	used atomic.Bool
}

func (m *TrackingRWCloser) Write(p []byte) (n int, err error) {
	m.used.Store(true)

	return 0, nil
}

func (m *TrackingRWCloser) Read(p []byte) (n int, err error) {
	return 0, nil
}

func (m *TrackingRWCloser) Close() error {
	return nil
}

func (m *TrackingRWCloser) Used() bool {
	return m.used.Load()
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

func BenchmarkParallel(b *testing.B) {
	server, err := NewTestServer()
	if err != nil {
		b.Fatal("starting test server: ", err)
	}

	c, err := connection.New(server.Addr, testSpec, readMessageLength, writeMessageLength,
		connection.SendTimeout(500*time.Millisecond),
	)
	if err != nil {
		b.Fatal("creating client: ", err)
	}

	err = c.Connect()
	if err != nil {
		b.Fatal("connecting to the server: ", err)
	}

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			message := iso8583.NewMessage(testSpec)
			message.MTI("0800")
			message.Field(11, getSTAN())

			_, err := c.Send(message)
			if err != nil {
				b.Fatal("sending message: ", err)
			}
		}
	})

	err = c.Close()
	if err != nil {
		b.Fatal("closing client: ", err)
	}
	server.Close()
}

func BenchmarkProcess100(b *testing.B) { benchmarkProcess(100, b) }

func BenchmarkProcess1000(b *testing.B) { benchmarkProcess(1000, b) }

func BenchmarkProcess10000(b *testing.B) { benchmarkProcess(10000, b) }

func BenchmarkProcess100000(b *testing.B) { benchmarkProcess(100000, b) }

func benchmarkProcess(m int, b *testing.B) {
	server, err := NewTestServer()
	if err != nil {
		b.Fatal("starting test server: ", err)
	}

	c, err := connection.New(server.Addr, testSpec, readMessageLength, writeMessageLength,
		connection.SendTimeout(500*time.Millisecond),
	)
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
			message.Field(11, getSTAN())

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
