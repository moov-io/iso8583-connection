package client_test

import (
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/moov-io/iso8583"
	"github.com/moov-io/iso8583/encoding"
	"github.com/moov-io/iso8583/field"
	"github.com/moov-io/iso8583/network"
	"github.com/moov-io/iso8583/prefix"
	client "github.com/moovfinancial/iso8583-client"
	"github.com/moovfinancial/iso8583-client/server"
)

// here are the implementation of the provider protocol:
// * header reader and writer
// * spec
func readMessageLength(r io.Reader) (int, error) {
	header := network.NewBinary2BytesHeader()
	n, err := header.ReadFrom(r)
	if err != nil {
		return n, err
	}

	return header.Length(), nil
}

func writeMessageLength(w io.Writer, length int) (int, error) {
	header := network.NewBinary2BytesHeader()
	header.SetLength(length)

	n, err := header.WriteTo(w)
	if err != nil {
		return n, fmt.Errorf("writing message header: %v", err)
	}

	return n, nil
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

// create testServer for testing
type testServer struct {
	Addr string

	server *server.Server

	// to protect following
	mutex         sync.Mutex
	ReceivedPings int
}

func (t *testServer) Ping() {
	t.mutex.Lock()
	t.ReceivedPings++
	t.mutex.Unlock()
}

const (
	CardForDelayedResponse string = "4200000000000000"
	CardForPingCounter     string = "4005550000000019"
)

func NewTestServer() (*testServer, error) {
	var srv *testServer

	// define logic for our test server
	testServerLogic := func(c *client.Client, message *iso8583.Message) {
		mti, err := message.GetMTI()
		if err != nil {
			log.Printf("getting MTI: %v", err)
			return
		}

		// we handle only 0800 messages
		if mti != "0800" {
			return
		}

		// update MTI for the response message
		newMTI := "0810"
		message.MTI(newMTI)

		// check if PAN was set to specific test case value
		f2 := message.GetField(2)
		if f2 != nil {
			code, err := f2.String()
			if err != nil {
				log.Printf("getting field 2: %v", err)
				return
			}

			switch code {
			case CardForDelayedResponse:
				// testing value to "sleep" for a 3 seconds
				time.Sleep(500 * time.Millisecond)

			case CardForPingCounter:
				// ping request received
				srv.Ping()
			}
		}

		c.Reply(message)
	}

	server := server.New(testSpec, readMessageLength, writeMessageLength, client.UnmatchedMessageHandler(testServerLogic))
	// start on random port
	err := server.Start("127.0.0.1:")
	if err != nil {
		return nil, err
	}

	srv = &testServer{
		server: server,
		Addr:   server.Addr,
	}

	return srv, nil
}

func (t *testServer) Close() {
	t.server.Close()
}
