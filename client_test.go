package client_test

import (
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/moov-io/iso8583"
	"github.com/moov-io/iso8583/encoding"
	"github.com/moov-io/iso8583/field"
	"github.com/moov-io/iso8583/network"
	"github.com/moov-io/iso8583/prefix"
	client "github.com/moovfinancial/iso8583-client"
	"github.com/moovfinancial/iso8583-client/test"
	"github.com/stretchr/testify/require"
)

func ReadMessageLength(r io.Reader) (int, error) {
	header := network.NewBinary2BytesHeader()
	n, err := header.ReadFrom(r)
	if err != nil {
		return n, err
	}

	return header.Length(), nil
}

func WriteMessageLength(w io.Writer, length int) (int, error) {
	header := network.NewBinary2BytesHeader()
	header.SetLength(length)

	n, err := header.WriteTo(w)
	if err != nil {
		return n, fmt.Errorf("writing message header: %v", err)
	}

	return n, nil
}

func TestClient_Connect(t *testing.T) {
	server, err := test.NewServer(testSpec, ReadMessageLength, WriteMessageLength)
	require.NoError(t, err)
	defer server.Close()

	// our client can connect to the server
	c := client.NewClient(testSpec, ReadMessageLength, WriteMessageLength)
	err = c.Connect(server.Addr)
	require.NoError(t, err)
	require.NoError(t, c.Close())
}

func TestClient_Send(t *testing.T) {
	server, err := test.NewServer(testSpec, ReadMessageLength, WriteMessageLength)
	require.NoError(t, err)
	defer server.Close()

	t.Run("sends messages to server and receives responses", func(t *testing.T) {
		c := client.NewClient(testSpec, ReadMessageLength, WriteMessageLength)
		err = c.Connect(server.Addr)
		require.NoError(t, err)

		// network management message
		message := iso8583.NewMessage(testSpec)
		message.MTI("0800")
		message.Field(2, test.CardForDelayedResponse)

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
		c := client.NewClient(testSpec, ReadMessageLength, WriteMessageLength)
		err = c.Connect(server.Addr)
		require.NoError(t, err)

		// network management message
		message := iso8583.NewMessage(testSpec)
		message.MTI("0800")
		message.Field(2, test.CardForDelayedResponse)

		require.NoError(t, c.Close())

		_, err = c.Send(message)
		require.Equal(t, client.ErrConnectionClosed, err)
	})

	t.Run("it returns ErrSendTimeout when response was not received during SendTimeout time", func(t *testing.T) {

		c := client.NewClient(testSpec, ReadMessageLength, WriteMessageLength, func(opts *client.Options) {
			opts.SendTimeout = 100 * time.Millisecond
		})
		err = c.Connect(server.Addr)
		require.NoError(t, err)
		defer c.Close()

		// regular network management message
		message := iso8583.NewMessage(testSpec)
		message.MTI("0800")

		_, err := c.Send(message)
		require.NoError(t, err)

		// network management message to test timeout
		message = iso8583.NewMessage(testSpec)
		message.MTI("0800")

		// using 777 value for the field, we tell server
		// to sleep for 500ms when process the message
		require.NoError(t, message.Field(2, test.CardForDelayedResponse))

		_, err = c.Send(message)
		require.Equal(t, client.ErrSendTimeout, err)
	})

	t.Run("pending requests should complete after Close was called", func(t *testing.T) {
		c := client.NewClient(testSpec, ReadMessageLength, WriteMessageLength)
		err = c.Connect(server.Addr)
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
				message.MTI("0800")

				// using 777 value for the field, we tell server
				// to sleep for 500ms when process the message
				require.NoError(t, message.Field(2, test.CardForDelayedResponse))

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
		c := client.NewClient(testSpec, ReadMessageLength, WriteMessageLength)
		err = c.Connect(server.Addr)
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
			message.MTI("0800")

			// using 777 value for the field, we tell server
			// to sleep for 500ms when process the message
			require.NoError(t, message.Field(2, test.CardForDelayedResponse))
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
			message.MTI("0800")

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
		t.Skip("ping will be implemented as callback")

		// we create server instance here to isolate pings count
		server, err := test.NewServer(testSpec, ReadMessageLength, WriteMessageLength)
		require.NoError(t, err)
		defer server.Close()

		c := client.NewClient(testSpec, ReadMessageLength, WriteMessageLength, func(opts *client.Options) {
			opts.IdleTime = 50 * time.Millisecond
		})

		err = c.Connect(server.Addr)
		require.NoError(t, err)
		defer c.Close()

		// we expect that ping interval in 50ms has not passed yet
		// and server has not being pinged
		require.Equal(t, 0, server.RecivedPings())

		time.Sleep(200 * time.Millisecond)

		require.True(t, server.RecivedPings() > 0)
	})

	t.Run("it handles unrecognized responses", func(t *testing.T) {
		c := client.NewClient(testSpec, ReadMessageLength, WriteMessageLength, func(opts *client.Options) {
			opts.SendTimeout = 100 * time.Millisecond
		})
		err = c.Connect(server.Addr)
		require.NoError(t, err)
		defer c.Close()

		// network management message to test timeout
		message := iso8583.NewMessage(testSpec)
		message.MTI("0800")

		// using 777 value for the field, we tell server
		// to sleep for 500ms when process the message
		require.NoError(t, message.Field(2, test.CardForDelayedResponse))

		_, err = c.Send(message)
		require.Equal(t, client.ErrSendTimeout, err)

		// let's sleep here to wait for the server response to our message
		// we should not panic :)
		time.Sleep(1 * time.Second)
	})
}

func BenchmarkSend100(b *testing.B) { benchmarkSend(100, b) }

func BenchmarkSend1000(b *testing.B) { benchmarkSend(1000, b) }

func BenchmarkSend10000(b *testing.B) { benchmarkSend(10000, b) }

func BenchmarkSend100000(b *testing.B) { benchmarkSend(100000, b) }

func benchmarkSend(m int, b *testing.B) {
	server, err := test.NewServer(testSpec, ReadMessageLength, WriteMessageLength)
	if err != nil {
		b.Fatal("starting server: ", err)
	}

	c := client.NewClient(testSpec, ReadMessageLength, WriteMessageLength)
	err = c.Connect(server.Addr)
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
func processMessages(b *testing.B, m int, c *client.Client) {
	var wg sync.WaitGroup
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
				b.Fatal("sending message: ", err)
			}
		}()
	}
	wg.Wait()
}

var testSpec *iso8583.MessageSpec = &iso8583.MessageSpec{
	Name: "ISO 8583 v1987 ASCII",
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
			Length:      19,
			Description: "Primary Account Number",
			Enc:         encoding.ASCII,
			Pref:        prefix.ASCII.LL,
		}),
		11: field.NewString(&field.Spec{
			Length:      6,
			Description: "Systems Trace Audit Number (STAN)",
			Enc:         encoding.ASCII,
			Pref:        prefix.ASCII.Fixed,
		}),
	},
}
