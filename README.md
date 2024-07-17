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
* ConnectionClosedHandler - is called when connection is closed by us, by server or there were errors during network read/write that led to connection closure
* ErrorHandler - is called with the error when connection fails to perform some operation. In some cases instance of a `SafeError` will be passed to prevent data leaks ([detalis](https://github.com/moov-io/iso8583/pull/185))

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

### Handler invocation during the connection life cycle

This section explains the various stages at which different handler functions are triggered throughout the lifecycle of the `Connection`.

#### On connection establishment:

- **`OnConnect`** or **`OnConnectCtx`**: This handler is invoked immediately after the TCP connection is made. It can be utilized for operations that should be performed before the connection is officially considered established (e.g., sending `SignOn` message and receiving its response). **NOTE** If both `OnConnect` and `OnConnectCtx` are defined, `OnConnectCtx` will be used.

- **`ConnectionEstablishedHandler (async)`**: This asynchronous handler is triggered when the connection is logically considered established.

#### On error occurrence:

- **`ErrorHandler (async)`**: This asynchronous handler is executed when an error occurs during message reading or writing.

#### On message receipt:

- **`InboundMessageHandler (async)`**: This asynchronous handler is triggered when an incoming message is received, or a received message does not have a matching request (this can happen when we return an error for the `Send` method after a timeout and then, subsequently, receive a response, aka late response).

#### On read timeout:

- **`ReadTimeoutHandler (async)`**: This asynchronous handler is activated when no messages are received within the set `ReadTimeout` period.

#### On idle time:

- **`PingHandler (async)`**: This asynchronous handler is invoked when no messages are sent within the `IdleTime`.

#### On connection closure:

- **`ConnectionClosedHandlers (async)`**: These asynchronous handlers are invoked after connection is closed by us, by the server or due to the network errors

- **`OnClose`** or **`OnCloseCtx`**: This handler is activated before the connection is closed when we manually close the connection. **NOTE** If both `OnClose` and `OnCloseCtx` are defined, `OnCloseCtx` will be used.


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

## Connection `Pool`

Sometimes you want to establish multiple connections and re-create
them when such connections are closed due to a network errors. Connection `Pool`
is really helpful for such use cases.

To use Pool, first, you need to create a factory function that knows how to
create connections and a list of addresses you want to establish connections
with. You can establish connections with different or the same addresses.

```go
// Factory method that will build connection
factory := func(addr string) (*connection.Connection, error) {
	c, err := connection.New(
		addr,
		testSpec,
		readMessageLength,
		writeMessageLength,
		// set shot connect timeout so we can test re-connects
		connection.ConnectTimeout(500*time.Millisecond),
		connection.OnConnect(func(c *connection.Connection) {
			c.Set("status", "online")
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("building iso8583 connection: %w", err)
	}

	return c, nil
}
```

if there is a need to apply address specific configurations like TLS, you can create a map or function that will return all needed options for the address:

```go
func getAddrOpts(addr string) []Option {
	switch addr {
	case "127.0.0.1":
		return []Option{
			connection.ClientCert(certA, keyA),
		}
	case "127.0.0.2":
		return []Option{
			connection.ClientCert(certB, keyB),
		}
	}
}

factory := func(addr string) (*connection.Connection, error) {
	c, err := connection.New(
		addr,
		testSpec,
		readMessageLength,
		writeMessageLength,
		connection.ConnectTimeout(500*time.Millisecond),
		getAddrOpts(addr)...,
	)
	if err != nil {
		return nil, fmt.Errorf("building iso8583 connection: %w", err)
	}

	return c, nil
}
```

Now you can create pool and establish all connections:

```go
// let's say we want Get() to return only online connections
filterOnlineConnections := func(conn *connection.Connection) bool {
	return conn.Get("status") == "online"
}

pool, err := connection.NewPool(
	factory,
	addrs,
	connection.PoolConnectionsFilter(filterOnlineConnections),
)
// handle error

err = pool.Connect()
// handle error
```

When pool is connected, you can get connection from the pool to send message to:

```go
// get connection (only "online") from the pool
conn, err := pool.Get()
// handle err

// create iso8583 message
msg := iso8583.NewMessage(yourSpec)
// ...

reply, err := conn.Send(msg)
// handle error
```

Because `Connection` is safe to be used concurrently, you don't return
connection back to the pool. But don't close the connection directly as the
pool will remove it from the pool of connections only when connection is closed
by the server. It does it using `ConnectionClosedHandler`.

### Configuration of the Pool

Following options are supported:

* `ReconnectWait` sets the time to wait after first re-connect attempt
* `MaxReconnectWait` specifies the maximum duration to wait between reconnection attempts, serving as the upper bound for exponential backoff; if set to zero, there's no exponential backoff and ReconnectWait is used for each retry.
* `ErrorHandler` is called in a goroutine with the errors that can't be returned to the caller (from other goroutines)
* `MinConnections` is the number of connections required to be established when we connect the pool
* `ConnectionsFilter` is a function to filter connections in the pool for `Get`, `IsDegraded` or `IsUp` methods

## Context
You can provide context to the Connect and Close functions in addition to defining `OnConnectCtx` and `OnCloseCtx` in the connection options. This will allow you to pass along telemetry or any other information on contexts through from the Connect/Close calls to your handler functions:

```go
c, err := connection.New("127.0.0.1:9999", brandSpec, readMessageLength, writeMessageLength,
	connection.SendTimeout(100*time.Millisecond),
	connection.IdleTime(50*time.Millisecond),
    connect.OnConnectCtx(func(ctx context.Context, c *connection.Connection){
        return signOnFunc(ctx, c)
    }),
    connect.OnCloseCtx(func(ctx context.Context, c *connection.Connection){
        return signOffFunc(ctx, c)
    }),
)

ctx := context.Background()
c.ConnectCtx(ctx)

...

c.CloseCtx(ctx)

```

## Benchmark

To benchmark the connection, we created a test server that sends a response to
each request. Therefore, the benchmark measures the time it takes to send a
message and receive a response by both the client and the server. If you are
looking to measure client performance only, you should either run the test
server on a separate machine, or, with some approximation, you can multiply the
results by 2.

For the connection benchmark, we pack/unpack an ISO 8583 message with only 2
fields: `MTI` and `STAN`.

We have two types of benchmarks: *BenchmarkParallel* and *BenchmarkProcess*.

*BenchmarkParallel* uses `b.N` goroutines to send (and receive) messages to the
server. You can set the number of goroutines using the `-cpu` flag. Please note
that the `-cpu` flag also sets `GOMAXPROCS`.

For example, to run the benchmark with 6 goroutines/CPUs/cores, use the
following command:

```
go test -bench=BenchmarkParallel -cpu=6
```

Be aware that results may vary depending on the number of actual CPUs, cores, throttling, and system load.

Here is the result on MacBook Pro:

```
➜ go test -bench=BenchmarkParallel -cpu 6
goos: darwin
goarch: amd64
pkg: github.com/moov-io/iso8583-connection
cpu: Intel(R) Core(TM) i7-9750H CPU @ 2.60GHz
BenchmarkParallel-6        63703             18849 ns/op
PASS
ok      github.com/moov-io/iso8583-connection   26.079s
```

It shows that 53K messages were sent and recieved by both client and server in 1sec.

*BenchmarkProcessNNN*, where NNN is the number of messages to send, is another type of benchmark. In
this benchmark, the we send and receive messages to the server concurrently by running NNN goroutines.

To run such benchmarks, use:

```
go test -bench=BenchmarkProcess
```

Here are the latest results on MacBook Pro:

```
➜ go test -bench=BenchmarkProcess -cpu 6
goos: darwin
goarch: amd64
pkg: github.com/moov-io/iso8583-connection
cpu: Intel(R) Core(TM) i7-9750H CPU @ 2.60GHz
BenchmarkProcess100-6                732           1579450 ns/op
BenchmarkProcess1000-6                75          15220504 ns/op
BenchmarkProcess10000-6                7         149483539 ns/op
BenchmarkProcess100000-6               1        1681237716 ns/op
PASS
ok      github.com/moov-io/iso8583-connection   29.967s
```

It shows that:
* The time taken scales approximately linearly with the number of messages processed.
* 1.681 seconds to send/receive 100,000 messages by both client and server.
* 149.48 milliseconds to send/receive 10,000 messages by both client and server.
* 15.22 milliseconds to send/receive 1,000 messages by both client and server.
* 1.579 milliseconds to send/receive 100 messages by both client and server.


## License

Apache License 2.0 - See [LICENSE](LICENSE) for details.
