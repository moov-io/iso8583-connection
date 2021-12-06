package main

import (
	"sync"
	"testing"
	"time"

	"github.com/moov-io/iso8583"
	"github.com/stretchr/testify/require"
)

func TestClient_Connect(t *testing.T) {
	server, err := NewTestServer()
	require.NoError(t, err)
	defer server.Close()

	// our client can connect to the server
	client := NewClient()
	err = client.Connect(server.Addr)
	require.NoError(t, err)
	require.NoError(t, client.Close())
}

func TestClient_Send(t *testing.T) {
	server, err := NewTestServer()
	require.NoError(t, err)
	defer server.Close()

	t.Run("sends messages to server and receives responses", func(t *testing.T) {
		client := NewClient()
		err = client.Connect(server.Addr)
		require.NoError(t, err)

		// network management message
		message := iso8583.NewMessage(brandSpec)
		message.MTI("0800")
		message.Field(70, "777")

		// we can send iso message to the server
		response, err := client.Send(message)
		require.NoError(t, err)
		time.Sleep(1 * time.Second)

		mti, err := response.GetMTI()
		require.NoError(t, err)
		require.Equal(t, "0810", mti)

		require.NoError(t, client.Close())
	})

	t.Run("it returns ErrConnectionClosed when Close was called", func(t *testing.T) {
		client := NewClient()
		err = client.Connect(server.Addr)
		require.NoError(t, err)

		// network management message
		message := iso8583.NewMessage(brandSpec)
		message.MTI("0800")
		message.Field(70, "777")

		require.NoError(t, client.Close())

		_, err = client.Send(message)
		require.Equal(t, ErrConnectionClosed, err)
	})

	t.Run("it returns ErrSendTimeout when response was not received during SendTimeout time", func(t *testing.T) {
		client := NewClient(func(opts *Options) {
			opts.SendTimeout = 100 * time.Millisecond
		})
		err = client.Connect(server.Addr)
		require.NoError(t, err)
		defer client.Close()

		// regular network management message
		message := iso8583.NewMessage(brandSpec)
		message.MTI("0800")

		_, err := client.Send(message)
		require.NoError(t, err)

		// network management message to test timeout
		message = iso8583.NewMessage(brandSpec)
		message.MTI("0800")

		// using 777 value for the field, we tell server
		// to sleep for 500ms when process the message
		require.NoError(t, message.Field(70, "777"))

		_, err = client.Send(message)
		require.Equal(t, ErrSendTimeout, err)
	})

	t.Run("pending requests should complete after Close was called", func(t *testing.T) {
		client := NewClient()
		err = client.Connect(server.Addr)
		require.NoError(t, err)
		defer client.Close()

		var wg sync.WaitGroup
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(i int) {
				defer func() {
					wg.Done()
				}()

				// network management message
				message := iso8583.NewMessage(brandSpec)
				message.MTI("0800")

				// using 777 value for the field, we tell server
				// to sleep for 500ms when process the message
				require.NoError(t, message.Field(70, "777"))

				response, err := client.Send(message)
				require.NoError(t, err)

				mti, err := response.GetMTI()
				require.NoError(t, err)
				require.Equal(t, "0810", mti)
			}(i)
		}

		// let's wait all messages to be sent
		time.Sleep(200 * time.Millisecond)

		// while server is waiting, we will close the connection
		require.NoError(t, client.Close())
		wg.Wait()
	})

	t.Run("responses received asynchronously", func(t *testing.T) {
		client := NewClient()
		err = client.Connect(server.Addr)
		require.NoError(t, err)
		defer client.Close()

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

			message := iso8583.NewMessage(brandSpec)
			message.MTI("0800")

			// using 777 value for the field, we tell server
			// to sleep for 500ms when process the message
			require.NoError(t, message.Field(70, "777"))
			response, err := client.Send(message)
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

			message := iso8583.NewMessage(brandSpec)
			message.MTI("0800")

			response, err := client.Send(message)
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
		require.NoError(t, client.Close())

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

		client := NewClient(func(opts *Options) {
			opts.IdleTime = 50 * time.Millisecond
		})

		err = client.Connect(server.Addr)
		require.NoError(t, err)
		defer client.Close()

		// we expect that ping interval in 50ms has not passed yet
		// and server has not being pinged
		require.Equal(t, 0, server.RecivedPings())

		time.Sleep(200 * time.Millisecond)

		require.True(t, server.RecivedPings() > 0)
	})
}

func BenchmarkSend100(b *testing.B) { benchmarkSend(100, b) }

func BenchmarkSend1000(b *testing.B) { benchmarkSend(1000, b) }

func BenchmarkSend10000(b *testing.B) { benchmarkSend(10000, b) }

func BenchmarkSend100000(b *testing.B) { benchmarkSend(100000, b) }

func benchmarkSend(m int, b *testing.B) {
	// start server
	// defer stop server
	server, err := NewTestServer()
	if err != nil {
		b.Fatal("starting server: ", err)
	}
	defer server.Close()

	for n := 0; n < b.N; n++ {
		client := NewClient()
		err = client.Connect(server.Addr)
		if err != nil {
			b.Fatal("connecting to the server: ", err)
		}

		// send m messages
		var wg sync.WaitGroup
		for i := 0; i < m; i++ {
			wg.Add(1)
			go func() {
				defer func() {
					wg.Done()
				}()

				message := iso8583.NewMessage(brandSpec)
				message.MTI("0800")

				_, err := client.Send(message)
				if err != nil {
					b.Fatal("sending message: ", err)
				}
			}()
		}

		wg.Wait()
		err = client.Close()
		if err != nil {
			b.Fatal("closing client: ", err)
		}
	}
}
