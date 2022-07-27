[![Moov Banner Logo](https://user-images.githubusercontent.com/20115216/104214617-885b3c80-53ec-11eb-8ce0-9fc745fb5bfc.png)](https://github.com/moov-io)

<p align="center">
  <a href="https://moov-io.slack.com/archives/C014UT7C3ST">Community channel #iso8583</a>
  ·
  <a href="https://moov.io/blog/">Blog</a>
  <br>
  <br>
</p>

# moov-io/iso8583-connection

Moov's mission is to give developers an easy way to create and integrate bank processing into their own software products. Our open source projects are each focused on solving a single responsibility in financial services and designed around performance, scalability, and ease of use.

moov-io/iso8583-connection is a package helping with sending, receiving and matching ISO 8583 messages between client and server. It can be used both for acquiring and issuing services.

## Project status

ISO 8583 Connection package is used in production environments. Please star the project if you are interested in its progress. Please let us know if you encounter any bugs/unclear documentation or have feature suggestions by [opening up an issue](https://github.com/moov-io/iso8583-connection/issues/new) or pull request. Thanks!

## Configuration

Following options are supported:

* SendTimeout - sets the timeout for a Send operation
* IdleTime - sets the period of inactivity (no messages sent) after which a ping message will be sent to the server
* ReadTimeout - sets the period of time to wait between reads before calling ReadTimeoutHandler 
* PingHandler - called when no message was sent during idle time. It should be safe for concurrent use.
* InboundMessageHandler - called when a message from the server is received or no matching request for the message was found. InboundMessageHandler must be safe to be called concurrenty.
* ReadTimeoutHandler - called when no messages have been received during specified ReadTimeout wait time. It should be safe for concurrent use.
* ConnectionClosedHandler - is called when connection is closed by server or there were errors during network read/write that led to connection closure
* ErrorHandler - is called with the error when connection fails to perform some operation

If you want to override default options, you can do this when creating instance of a client or setting it separately using `SetOptions(options...)` method.

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
		echo.MTI("0810")
		// set other fields
		err := c.Reply(echo)
		// handle error
	default:
		// log unrecognized message
	}
}

c, err := connection.New("127.0.0.1:9999", brandSpec, readMessageLength, writeMessageLength,
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
// see configuration options for more details
c, err := connection.New("127.0.0.1:9999", brandSpec, readMessageLength, writeMessageLength,
	connection.SendTimeout(100*time.Millisecond),
	connection.IdleTime(50*time.Millisecond),
	connection.PingHandler(pingHandler),
	connection.UnmatchedMessageHandler(unmatchedMessageHandler),
	connection.ConnectionClosedHandler(connectionClosedHandler),
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

## License

Apache License 2.0 - See [LICENSE](LICENSE) for details.
