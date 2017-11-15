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
		out = make(chan string)
		in  = make(chan string)
	)

	client, _ := distchan.NewClient(conn, out, in)
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
