# distchan

Package distchan enables Go channels to be used for distributed computation.

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

```go
package main

import (
	"log"
	"net"

	"github.com/dradtke/distchan"
)

func main() {
	ln, err := net.Listen("tcp", "...")
	if err != nil {
		log.Fatal(err)
	}

	var (
		out    = make(chan string)
		in     = make(chan string)
		server = distchan.ChanServer(ln, out, in)
	)

	server.WaitUntilReady() // wait until we have at least one worker available

	go consumer(in)
	go producer(out)

	<-server.Done()
}

func consumer(in <-chan string) {
	// receive capitalized strings from in
}

func producer(out chan<- string) {
	// send strings to be capitalized to out
}
```

Then you'll need to create a client, or worker. It's similarly easy to get wired
up, so you can focus on the hard part: capitalizing strings:

```go
package main

import (
	"log"
	"net"

	"github.com/dradtke/distchan"
)

func main() {
	conn, err := net.Dial("tcp", "...") // must be able to connect to the server
	if err != nil {
		log.Fatal(err)
	}

	var (
		out    = make(chan string)
		in     = make(chan string)
	)

	distchan.ChanWrite(conn, out)
	distchan.ChanRead(conn, in)

	// Loop over all input from the server...
	for input := range in {
		capitalized := strings.ToUpper(input)
		// ...and send the results back.
		out <- capitalized
	}
}
```

A (slightly) more complete example can be found in the `example` folder,
including the usage of custom types.
