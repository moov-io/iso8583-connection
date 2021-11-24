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
