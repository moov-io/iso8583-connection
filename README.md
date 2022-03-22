# ISO 8583 Connection (Multiplexer)

## Configuration

Following options are supported:

* SendTimeout - sets the timeout for a Send operation
* IdleTime - sets the period of inactivity (no messages sent) after which a ping message will be sent to the server
* PingHandler - called when no message was sent during idle time. It should be safe for concurrent use.
* InboundMessageHandler - called when a message from the server is received or no matching request for the message was found. InboundMessageHandler must be safe to be called concurrenty.

If you want to override default options, you can do this when creating instance of a client:

```go
pingHandler := func(c *connection.Connection) {
	// send ping/heartbeat message like this
	ping := iso8583.NewMessage(brandSpec)
	// set other fields
	response, err := c.Send(ping)
	// handle error
}

inboundMessageHandler := func(c *connection.Connection, message *iso8583.Message) {
	// log received message or send a reply like this
	mti, err := message.GetMTI()
	// handle err

	// implement logic for network management messages
	switch mti {
	case "0800":
		echo := iso8583.NewMessage(brandSpec)
		echo.MTI("08100")
		// set other fields
		err := c.Reply(echo)
		// handle error
	default:
		// log unrecognized message
	}
}

c := connection.New("127.0.0.1:9999", brandSpec, readMessageLength, writeMessageLength,
	connection.SendTimeout(100*time.Millisecond),
	connection.IdleTime(50*time.Millisecond),
	connection.PingHandler(pingHandler),
	connection.InboundMessageHandler(inboundMessageHandler),
)

// work with the client
```

### (m)TLS connection

Configure to use TLS during connect:

```go
c, err := connection.New("127.0.0.1:443", testSpec, readMessageLength, writeMessageLength,
	// if server requires client certificate (mTLS)
	connection.ClientCert("./testdata/client.crt", "./testdata/client.key"),
	// if you use a self signed certificate, provide root certificate
	connection.RootCAs("./testdata/ca.crt"),
)
// handle error
```

## Usage

```go
// see configuration options for more details about different handlers
c := connection.New("127.0.0.1:9999", brandSpec, readMessageLength, writeMessageLength,
	connection.SendTimeout(100*time.Millisecond),
	connection.IdleTime(50*time.Millisecond),
	connection.PingHandler(pingHandler),
	connection.UnmatchedMessageHandler(unmatchedMessageHandler),
)
err := c.Connect()
if err != nil {
	// handle error
}
defer c.Close()

// create iso8583 message
message := iso8583.NewMessage(brandSpec)
message.MTI("0800")
// ...

// send message to the server
response, err := connection.Send(message)
if err != nil {
	// handle error
}

// work with the response
mti, err := response.GetMTI()
if err != nil {
	// handle error
}

if mti != "0810" {
	// handle error
}
```

## Benchmark

To benchmark the connection, run:

```
go test -bench=BenchmarkSend -run=XXX
```

Here are the latest results on MacBook Pro:

```
➜ go test -bench=BenchmarkSend -run=XXX
goos: darwin
goarch: amd64
pkg: github.com/moovfinancial/iso8583-client
cpu: Intel(R) Core(TM) i7-9750H CPU @ 2.60GHz
BenchmarkSend100-12                  560           2019912 ns/op
BenchmarkSend1000-12                  66          18435428 ns/op
BenchmarkSend10000-12                  6         210433011 ns/op
BenchmarkSend100000-12                 1        2471006590 ns/op
PASS
ok      github.com/moov-io/iso8583-connection    7.784s
```

It shows that:
* time is linear (it takes ten times more time to send ten times more messages)
* 2.5sec to send/receive 100K messages
* 210ms to send/receive 10K messages
* 18ms to send/receive 1000 messages
* 2ms to send/receive 100 messages

_Note, that these benchmarks currently measure not only the client performance
(send/receive) but also the performance of the test server._
