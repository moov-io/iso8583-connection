[![Moov Banner Logo](https://user-images.githubusercontent.com/20115216/104214617-885b3c80-53ec-11eb-8ce0-9fc745fb5bfc.png)](https://github.com/moov-io)

<p align="center">
  <a href="https://slack.moov.io/">Community channel #iso8583</a>
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

## Installation

This package requires [Go](https://go.dev/doc/install) 1.26 or later.

```
go get github.com/moov-io/iso8583-connection
```

Import it (the `connection` alias is used throughout this README):

```go
import (
	"github.com/moov-io/iso8583"
	connection "github.com/moov-io/iso8583-connection"
	"github.com/moov-io/iso8583-connection/server"
	"github.com/moov-io/iso8583/network"
	"github.com/moov-io/iso8583/specs"
)
```

The [`server`](server) subpackage is a small ISO 8583 TCP server that accepts connections and handles inbound messages. It is useful for tests and local examples.

A runnable client and server that exchange an 0800/0810 pair lives in [`examples/echo`](examples/echo):

```
go run ./examples/echo
```

## Message length helpers and specs

`connection.New` needs an ISO 8583 message spec and functions that read and write the length header that precedes each message on the wire. Specs come from [`github.com/moov-io/iso8583`](https://github.com/moov-io/iso8583); the snippets below use `specs.Spec87ASCII`. Length headers can be built with `network.NewBinary2BytesHeader()`:

```go
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
		return n, fmt.Errorf("writing message header: %w", err)
	}
	return n, nil
}
```

The same helpers are exported as `examples.ReadMessageLength` and `examples.WriteMessageLength`.

## Configuration

The following `connection.Option` values are supported (see `options.go`). Defaults come from `GetDefaultOptions()`.

* `ConnectTimeout` - timeout for establishing a new connection (default 10s)
* `SendTimeout` - timeout for a `Send` operation (default 30s)
* `IdleTime` - period of inactivity (no messages sent) after which a ping message will be sent to the server (default 5s)
* `ReadTimeout` - period of time to wait between reads before calling `ReadTimeoutHandler` (default 60s)
* `PingHandler` - called when no message was sent during idle time. It should be safe for concurrent use.
* `InboundMessageHandler` - called when a message from the server is received and no matching request for the message was found. `InboundMessageHandler` must be safe to be called concurrently.
* `ReadTimeoutHandler` - called when no messages have been received during specified `ReadTimeout` wait time. It should be safe for concurrent use.
* `ConnectionClosedHandler` / `WithConnectionClosedHandler` - append a handler that is called when the connection is closed by us, by the server, or after network read/write errors that led to connection closure. Handlers are stored on the `ConnectionClosedHandlers` field and each is invoked in its own goroutine. `ConnectionClosedHandler` is a thin wrapper around `WithConnectionClosedHandler`; neither is deprecated. `WithConnectionClosedHandler` is the name used internally (for example by `Pool`).
* `ConnectionEstablishedHandler` - called asynchronously after the connection is considered established (after a successful `OnConnect` or `OnConnectCtx`)
* `WithConnectionFailedHandler` - append a handler called only when a connection fails due to network issues (connection refused, reset by peer, etc.). Each handler runs in its own goroutine.
* `ErrorHandler` - is called with the error when connection fails to perform some operation. In some cases instance of a `SafeError` will be passed to prevent data leaks ([details](https://github.com/moov-io/iso8583/pull/185))
* `OnConnect` / `OnConnectCtx` - called synchronously when a connection is established (e.g. to send a sign-on message). Each must return `error`. If both are set, `OnConnectCtx` is used.
* `OnClose` / `OnCloseCtx` - called synchronously before we manually close the connection (e.g. to send a sign-off message). Each must return `error`. If both are set, `OnCloseCtx` is used. These are not called when the peer or a network error closes the connection.
* `ClientCert`, `RootCAs`, `SetTLSConfig` - configure TLS / mTLS
* `SetRequestIDGenerator` - custom request/response matching (default: STAN, field 11)
* `SetMessageReader` / `SetMessageWriter` - override how messages are read from or written to the connection. When set, the length reader/writer passed to `New` is ignored.

If you want to override default options, you can do this when creating instance of a client or setting it separately using `SetOptions(options...)` method.

```go
pingHandler := func(c *connection.Connection) {
	// send ping/heartbeat message like this
	ping := iso8583.NewMessage(specs.Spec87ASCII)
	ping.MTI("0800")
	_ = ping.Field(11, "000001")
	response, err := c.Send(ping)
	// handle error
	_ = response
}

inboundMessageHandler := func(c *connection.Connection, message *iso8583.Message) {
	// log received message or send a reply like this
	mti, err := message.GetMTI()
	// handle err

	// implement logic for network management messages
	switch mti {
	case "0800":
		echo := iso8583.NewMessage(specs.Spec87ASCII)
		echo.MTI("0810")
		if stan, err := message.GetString(11); err == nil && stan != "" {
			_ = echo.Field(11, stan)
		}
		err := c.Reply(echo)
		// handle error
	default:
		// log unrecognized message
	}
}

c, err := connection.New("127.0.0.1:9999", specs.Spec87ASCII, readMessageLength, writeMessageLength,
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

- **`ConnectionFailedHandlers` (via `WithConnectionFailedHandler`, async)**: These asynchronous handlers are called only when a connection fails due to network issues.

#### On message receipt:

- **`InboundMessageHandler (async)`**: This asynchronous handler is triggered when an incoming message is received, or a received message does not have a matching request (this can happen when we return an error for the `Send` method after a timeout and then, subsequently, receive a response, aka late response).

#### On read timeout:

- **`ReadTimeoutHandler (async)`**: This asynchronous handler is activated when no messages are received within the set `ReadTimeout` period.

#### On idle time:

- **`PingHandler (async)`**: This asynchronous handler is invoked when no messages are sent within the `IdleTime`.

#### On connection closure:

- **`ConnectionClosedHandlers` (via `ConnectionClosedHandler` / `WithConnectionClosedHandler`, async)**: These asynchronous handlers are invoked after connection is closed by us, by the server or due to the network errors

- **`OnClose`** or **`OnCloseCtx`**: This handler is activated before the connection is closed when we manually close the connection. **NOTE** If both `OnClose` and `OnCloseCtx` are defined, `OnCloseCtx` will be used.

### (m)TLS connection

Configure to use TLS during connect:

```go
c, err := connection.New("127.0.0.1:443", specs.Spec87ASCII, readMessageLength, writeMessageLength,
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
c, err := connection.New("127.0.0.1:9999", specs.Spec87ASCII, readMessageLength, writeMessageLength,
	connection.SendTimeout(100*time.Millisecond),
	connection.IdleTime(50*time.Millisecond),
	connection.PingHandler(pingHandler),
	connection.InboundMessageHandler(inboundMessageHandler),
	connection.ConnectionClosedHandler(connectionClosedHandler),
)
if err != nil {
	// handle error
}

err = c.Connect()
if err != nil {
	// handle error
}
defer c.Close()

// create iso8583 message
message := iso8583.NewMessage(specs.Spec87ASCII)
message.MTI("0800")
// STAN (field 11) is required for request/response matching by default
if err := message.Field(11, "000001"); err != nil {
	// handle error
}

// send message to the server
response, err := c.Send(message)
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

## Request/response matching

By default, `Send` matches a response to its request using the System Trace Audit Number (STAN, field 11). The built-in request ID generator reads field 11 with `message.GetString(11)`. If that value is empty, `Send` returns:

```
creating request ID: STAN is missing
```

Set field 11 on every request and have the server echo it on the response. To match on something else, implement `connection.RequestIDGenerator` and pass it with `connection.SetRequestIDGenerator`. The ID must be the same for a request and its response, so don't include fields that change in the reply (for example a raw MTI of `0800` vs `0810`) unless you normalize them:

```go
type mtiSTANGenerator struct{}

func (mtiSTANGenerator) GenerateRequestID(message *iso8583.Message) (string, error) {
	mti, err := message.GetMTI()
	if err != nil {
		return "", err
	}
	// Request (x0xx) and response (x1xx) must share an ID.
	if len(mti) == 4 && mti[2] == '1' {
		mti = mti[:2] + "0" + mti[3:]
	}
	stan, err := message.GetString(11)
	if err != nil {
		return "", err
	}
	if stan == "" {
		return "", errors.New("STAN is missing")
	}
	return mti + "-" + stan, nil
}

c, err := connection.New(addr, specs.Spec87ASCII, readMessageLength, writeMessageLength,
	connection.SetRequestIDGenerator(mtiSTANGenerator{}),
)
```

The same generator is used when an inbound message arrives, so late responses and unmatched messages can be correlated the same way.

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
		specs.Spec87ASCII,
		readMessageLength,
		writeMessageLength,
		// set short connect timeout so we can test re-connects
		connection.ConnectTimeout(500*time.Millisecond),
		connection.OnConnect(func(c *connection.Connection) error {
			c.SetStatus(connection.StatusOnline)
			return nil
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("building iso8583 connection: %w", err)
	}

	return c, nil
}
```

If there is a need to apply address specific configurations like TLS, you can create a map or function that will return all needed options for the address. Build a `[]connection.Option` slice and `append` address-specific options before the variadic `connection.New` call:

```go
func getAddrOpts(addr string) []connection.Option {
	switch addr {
	case "127.0.0.1":
		return []connection.Option{
			connection.ClientCert(certA, keyA),
		}
	case "127.0.0.2":
		return []connection.Option{
			connection.ClientCert(certB, keyB),
		}
	}
	return nil
}

factory := func(addr string) (*connection.Connection, error) {
	opts := []connection.Option{
		connection.ConnectTimeout(500 * time.Millisecond),
	}
	opts = append(opts, getAddrOpts(addr)...)

	c, err := connection.New(
		addr,
		specs.Spec87ASCII,
		readMessageLength,
		writeMessageLength,
		opts...,
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
	return conn.Status() == connection.StatusOnline
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
msg := iso8583.NewMessage(specs.Spec87ASCII)
msg.MTI("0800")
if err := msg.Field(11, "000001"); err != nil {
	// handle error
}

reply, err := conn.Send(msg)
// handle error
```

Because `Connection` is safe to be used concurrently, you don't return
the connection back to the pool. Don't close a connection yourself — the pool
owns it. When a connection is closed (by the peer, a network error, or
`conn.Close()`), the pool's `WithConnectionClosedHandler` removes it from the
pool and starts a reconnect loop for that address, waiting `PoolReconnectWait`
(default 5s) between attempts, with optional exponential backoff up to
`PoolMaxReconnectWait`.

You can observe pool health with `IsUp` and `IsDegraded`:

```go
if pool.IsUp() {
	// at least one (filtered) connection is available
}

if pool.IsDegraded() {
	// fewer (filtered) connections than configured addresses
	// the pool is reconnecting in the background
}
```

### Configuration of the Pool

The following `connection.PoolOption` values are supported (see `pool_options.go`). Defaults come from `GetDefaultPoolOptions()`.

* `PoolReconnectWait` sets the time to wait after first re-connect attempt (default 5s)
* `PoolMaxReconnectWait` specifies the maximum duration to wait between reconnection attempts, serving as the upper bound for exponential backoff; if set to zero (the default), there's no exponential backoff and `PoolReconnectWait` is used for each retry
* `PoolErrorHandler` is called in a goroutine with the errors that can't be returned to the caller (from other goroutines). Don't block in this function as it will block the pool `Close()` method
* `PoolMinConnections` is the number of connections required to be established when we connect the pool (default 1). A zero value means `Connect` will not return an error if no connections were established and the pool will keep trying in the background
* `PoolConnectionsFilter` is a function to filter connections in the pool for `Get`, `IsDegraded`, and `IsUp`

## Context

You can provide context to the Connect and Close functions in addition to defining `OnConnectCtx` and `OnCloseCtx` in the connection options. This will allow you to pass along telemetry or any other information on contexts through from the Connect/Close calls to your handler functions:

```go
c, err := connection.New("127.0.0.1:9999", specs.Spec87ASCII, readMessageLength, writeMessageLength,
	connection.SendTimeout(100*time.Millisecond),
	connection.IdleTime(50*time.Millisecond),
	connection.OnConnectCtx(func(ctx context.Context, c *connection.Connection) error {
		return signOnFunc(ctx, c)
	}),
	connection.OnCloseCtx(func(ctx context.Context, c *connection.Connection) error {
		return signOffFunc(ctx, c)
	}),
)
if err != nil {
	// handle error
}

ctx := context.Background()
err = c.ConnectCtx(ctx)
// handle error

// ...

err = c.CloseCtx(ctx)
// handle error
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

Here is a historical result on a MacBook Pro (Intel Core i7-9750H, 6 cores, darwin/amd64):

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

It shows that 53K messages were sent and received by both client and server in 1sec.

*BenchmarkProcessNNN*, where NNN is the number of messages to send, is another type of benchmark. In
this benchmark, we send and receive messages to the server concurrently by running NNN goroutines.

To run such benchmarks, use:

```
go test -bench=BenchmarkProcess
```

Here are historical results on the same MacBook Pro:

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

Results from this package's CI-style environment on 2026-11-16 (America/Denver), Linux x86_64, Intel Xeon Processor (8 vCPUs), Go 1.27.4:

```
$ go test -bench='BenchmarkParallel$|BenchmarkProcess' -count=1 -cpu=8
goos: linux
goarch: amd64
pkg: github.com/moov-io/iso8583-connection
cpu: Intel(R) Xeon(R) Processor
BenchmarkParallel-8        	   76351	     16035 ns/op
BenchmarkProcess100-8      	     777	   1531953 ns/op
BenchmarkProcess1000-8     	      73	  15008795 ns/op
BenchmarkProcess10000-8    	       7	 149425433 ns/op
BenchmarkProcess100000-8   	       2	1575607785 ns/op
PASS
ok  	github.com/moov-io/iso8583-connection	20.899s
```

On this hardware:
* *BenchmarkParallel* sent and received about 62K messages per second by both client and server.
* 1.576 seconds to send/receive 100,000 messages by both client and server.
* 149.43 milliseconds to send/receive 10,000 messages by both client and server.
* 15.01 milliseconds to send/receive 1,000 messages by both client and server.
* 1.532 milliseconds to send/receive 100 messages by both client and server.


## License

Apache License 2.0 - See [LICENSE.md](LICENSE.md) for details.
