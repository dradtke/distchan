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
