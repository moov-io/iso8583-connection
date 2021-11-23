package main

import (
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

		// network management message
		message := iso8583.NewMessage(brandSpec)
		message.MTI("0800")
		message.Field(70, "777")

		require.NoError(t, client.Close())

		_, err = client.Send(message)
		require.Equal(t, ErrConnectionClosed, err)
	})

	// t.Run("pending requests should return error when Close was called", func(t *testing.T) {
	// 	client := NewClient()
	// 	err = client.Connect(server.Addr)
	// 	require.NoError(t, err)

	// 	// we have to somehow ask server to delay response
	// 	// when we switch from "string" messages, we can rely on
	// 	// account number for such tests
	// 	var wg sync.WaitGroup
	// 	for i := 0; i < 10; i++ {
	// 		wg.Add(1)
	// 		go func(i int) {
	// 			defer func() {
	// 				fmt.Println("Done!")
	// 				wg.Done()
	// 			}()

	// 			str := fmt.Sprintf("delay %d\n", i)

	// 			client.Send(&Message{Msg: str})
	// 			// _, err := client.Send(&Message{Msg: fmt.Sprintf("delay %d", i)})
	// 			// require.Equal(t, ErrConnectionClosed, err)
	// 		}(i)
	// 	}

	// 	time.Sleep(3 * time.Second)

	// 	require.NoError(t, client.Close())
	// 	wg.Wait()
	// })
}
