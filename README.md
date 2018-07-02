# distchan

Package distchan enables Go channels to be used for distributed computation.

**NOTE**: This library is very young, and as such its API is very much subject
to change. Until this notice is removed, it should be assumed that the API is in
an alpha state and subject to breakage. That said, the changes shouldn't be too
drastic, and feedback is very much encouraged, so please give it a shot!

Also, check it out on
[Chisel](https://chiselapp.com/user/dradtke/repository/distchan/home)!

## Why?

While Go's concurrency story around single-process data pipelines is
[great](https://blog.golang.org/pipelines), its ability to distribute workloads
across multiple machines is relatively lacking. There are several options here
(notably [glow](https://github.com/chrislusf/glow) and
[gleam](https://github.com/chrislusf/gleam)), but they lack the type-safety and
ease-of-use that Go's built-in channels provide.

## How?

In a nutshell: standard `net` primitives, Gob encoding, and reflection.

Architecturally, the package assumes a client-server model of workload
distribution. One server can have as many clients connected to it as you want,
and work will be distributed across them using a simple round-robin algorithm.

## Example

As a simple example, let's say that capitalizing letters in a string is very
computationally expensive, and you want to distribute that work across a number
of nodes. First, you'll need to create a server in charge of defining the work
to be done:

### Server

```go
package main

import (
	"log"
	"net"

	"github.com/dradtke/distchan"
)

func main() {
	ln, err := net.Listen("tcp", "localhost:5678")
	if err != nil {
		log.Fatal(err)
	}

	var (
		out       = make(chan string)
		in        = make(chan string)
		server, _ = distchan.NewServer(ln, out, in)
	)

	server.Start()
	server.WaitUntilReady() // wait until we have at least one worker available

	go producer(out)

	for s := range in {
		println(s)
	}
}

func producer(out chan<- string) {
	// send strings to be capitalized to out
	out <- "hello world"
	// don't forget to close the channel! this is how all connected
	// clients know that there's no more work coming.
	close(out)
}
```

Then you'll need to create a client, or worker. It's similarly easy to get wired
up, so you can focus on the hard part: capitalizing strings:

### Client

```go
package main

import (
	"log"
	"net"
	"strings"

	"github.com/dradtke/distchan"
)

func main() {
	conn, err := net.Dial("tcp", "localhost:5678") // must be able to connect to the server
	if err != nil {
		log.Fatal(err)
	}

	var (
		out       = make(chan string)
		in        = make(chan string)
		client, _ = distchan.NewClient(conn, out, in)
	)

	client.Start()

	// Loop over all input from the server...
	for input := range in {
		capitalized := strings.ToUpper(input)
		// ...and send the results back.
		out <- capitalized
	}

	close(out)
	<-client.Done()
}
```

Check out the `example` folder for more examples.
