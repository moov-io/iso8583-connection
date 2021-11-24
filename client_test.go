package main

import (
	"sync"
	"testing"
	"time"

	"github.com/moov-io/iso8583"
	"github.com/stretchr/testify/require"
)

func TestClientConnect(t *testing.T) {
	server, err := NewServer()
	require.NoError(t, err)
	defer server.Close()

	// our client can connect to the server
	client := NewClient()
	err = client.Connect(server.Addr)
	require.NoError(t, err)
	defer client.Close()

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
}

func TestClient_Send(t *testing.T) {
	server, err := NewServer()
	require.NoError(t, err)
	defer server.Close()

	t.Run("it returns ErrConnectionClosed when Close was called", func(t *testing.T) {
		client := NewClient()
		err = client.Connect(server.Addr)
		require.NoError(t, err)
		defer client.Close()

		// network management message
		message := iso8583.NewMessage(brandSpec)
		message.MTI("0800")
		message.Field(70, "777")

		require.NoError(t, client.Close())

		_, err = client.Send(message)
		require.Equal(t, ErrConnectionClosed, err)
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

	t.Run("automatically sends ping messages after ping interval", func(t *testing.T) {
		// we create server instance here to isolate pings count
		server, err := NewServer()
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
