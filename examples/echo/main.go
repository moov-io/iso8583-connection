// A minimal ISO 8583 client and server using this package and the
// server subpackage. The server echoes 0800 network-management requests
// as 0810 responses, matched by STAN (field 11).
//
//	go run ./examples/echo
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/moov-io/iso8583"
	connection "github.com/moov-io/iso8583-connection"
	"github.com/moov-io/iso8583-connection/examples"
	"github.com/moov-io/iso8583-connection/server"
	"github.com/moov-io/iso8583/specs"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	spec := specs.Spec87ASCII

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
	if err := srv.Start("127.0.0.1:"); err != nil {
		return fmt.Errorf("starting server: %w", err)
	}
	defer srv.Close()

	c, err := connection.New(srv.Addr, spec, examples.ReadMessageLength, examples.WriteMessageLength)
	if err != nil {
		return fmt.Errorf("creating connection: %w", err)
	}
	if err := c.Connect(); err != nil {
		return fmt.Errorf("connecting: %w", err)
	}
	defer c.Close()

	message := iso8583.NewMessage(spec)
	message.MTI("0800")
	if err := message.Field(11, "000001"); err != nil {
		return fmt.Errorf("setting STAN: %w", err)
	}

	response, err := c.Send(message)
	if err != nil {
		return fmt.Errorf("sending message: %w", err)
	}

	mti, err := response.GetMTI()
	if err != nil {
		return fmt.Errorf("getting MTI: %w", err)
	}
	stan, err := response.GetString(11)
	if err != nil {
		return fmt.Errorf("getting STAN: %w", err)
	}

	fmt.Fprintf(os.Stdout, "received MTI=%s STAN=%s\n", mti, stan)
	if mti != "0810" {
		return fmt.Errorf("unexpected MTI: %s", mti)
	}
	return nil
}
