# Brand ISO8583 Client

## Configuration

Following options are supported:

* SendTimeout - sets the timeout for a Send operation
* IdleTime - sets the period of inactivity (no messages sent) after which a ping message will be sent to the server

If you want to override default options, you can do this when creating instance of a client:

```go
c := client.NewClient(func(opts *Options) {
	opts.SendTimeout = 100 * time.Millisecond
	opts.IdleTime = 50 * time.Millisecond
})

// work with the client
```

## Usage

```go
c := client.NewClient()
err := c.Connect("127.0.0.1:3456")
if err != nil {
	// handle error
}
defer c.Close()

// create iso8583 message
message := iso8583.NewMessage(brandSpec)
message.MTI("0800")
// ...

// send message to the server
response, err := client.Send(message)
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

To benchmark the client, run:

```
go test -bench=BenchmarkSend -run=XXX
```

Here are the latest results on MacBook Pro:

```
➜ go test -bench=BenchmarkSend -run=XXX
goos: darwin
goarch: amd64
pkg: github.com/moovfinancial/brand-client
cpu: Intel(R) Core(TM) i7-9750H CPU @ 2.60GHz
BenchmarkSend100-12                  560           2019912 ns/op
BenchmarkSend1000-12                  66          18435428 ns/op
BenchmarkSend10000-12                  6         210433011 ns/op
BenchmarkSend100000-12                 1        2471006590 ns/op
PASS
ok      github.com/moovfinancial/brand-client    7.784s
```

It shows that:
* time is linear (it takes ten times more time to send ten times more messages)
* 2.5sec to send/receive 100K messages
* 210ms to send/receive 10K messages
* 18ms to send/receive 1000 messages
* 2ms to send/receive 100 messages

_Note, that these benchmarks currently measure not only the client performance
(send/receive) but also the performance of the test server._
