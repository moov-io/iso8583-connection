package main

import (
	"testing"
	"time"

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

	// we can send iso message to the server
	response, err := client.Send(&Message{Msg: "ping 1"})
	require.NoError(t, err)
	time.Sleep(1 * time.Second)
	require.Equal(t, "ping 1 pong", response.Msg)

	response, err = client.Send(&Message{Msg: "ping 2"})
	require.NoError(t, err)
	time.Sleep(1 * time.Second)
	require.Equal(t, "ping 2 pong", response.Msg)

	require.NoError(t, client.Close())
}
