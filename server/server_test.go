package server_test

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"sync/atomic"

	"github.com/moov-io/iso8583"
	connection "github.com/moov-io/iso8583-connection"
	"github.com/moov-io/iso8583-connection/server"
	"github.com/moov-io/iso8583/encoding"
	"github.com/moov-io/iso8583/field"
	"github.com/moov-io/iso8583/prefix"
	"github.com/stretchr/testify/require"
)

func TestServer_WithConnectionFactory(t *testing.T) {
	t.Run("validates options for default connection factory", func(t *testing.T) {
		s := server.New(nil, nil, nil)

		// returns error when starting without spec, message reader and writer
		err := s.Start("127.0.0.1:")
		require.Error(t, err)
		require.Contains(t, err.Error(), "spec is required")
	})

	t.Run("uses custom connection factory", func(t *testing.T) {
		s := server.New(nil, nil, nil)

		var isCalled atomic.Bool

		s.SetOptions(server.WithConnectionFactory(func(conn net.Conn) (*connection.Connection, error) {
			isCalled.Store(true)

			return connection.NewFrom(conn, testSpec, LengthHeaderReader, LengthHeaderWriter)
		}))

		err := s.Start("127.0.0.1:")
		require.NoError(t, err)

		// connect to the server
		conn, err := connection.New(s.Addr, testSpec, LengthHeaderReader, LengthHeaderWriter)
		require.NoError(t, err)

		err = conn.Connect()
		require.NoError(t, err)

		conn.Close()

		// test that our custom connection factory was called
		require.Eventually(t, func() bool {
			return isCalled.Load() == true
		}, 100*time.Millisecond, 10*time.Millisecond)

		s.Close()
	})
}

type lengthHeader struct {
	Len uint16
}

func LengthHeaderReader(r io.Reader) (int, error) {
	var header lengthHeader
	err := binary.Read(r, binary.BigEndian, &header)
	if err != nil {
		return 0, fmt.Errorf("failed to read length header: %w", err)
	}

	return int(header.Len), nil
}

func LengthHeaderWriter(w io.Writer, length int) (int, error) {
	err := binary.Write(w, binary.BigEndian, lengthHeader{Len: uint16(length)})
	if err != nil {
		return 0, fmt.Errorf("failed to write length header: %w", err)
	}

	return binary.Size(lengthHeader{}), nil
}

var testSpec *iso8583.MessageSpec = &iso8583.MessageSpec{
	Name: "ISO 8583 test spec",
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
