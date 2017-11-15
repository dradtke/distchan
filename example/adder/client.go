package main

import (
	"encoding/gob"
	"flag"
	"fmt"
	"log"
	"net"

	"github.com/dradtke/distchan"
)

type AdderInput struct {
	ID   string
	A, B int
}

type AdderOutput struct {
	ID     string
	Answer int
}

func main() {
	addr := flag.String("addr", "", "address to connect to")
	flag.Parse()

	if *addr == "" {
		log.Fatal("no server address specified")
	}

	gob.Register(AdderInput{})
	gob.Register(AdderOutput{})
	var (
		out = make(chan AdderOutput)
		in  = make(chan AdderInput)
	)

	conn, err := net.Dial("tcp", *addr)
	if err != nil {
		panic(err)
	}

	client, _ := distchan.NewClient(conn, out, in)
	client.Start()

	fmt.Println("waiting for input...")
	for input := range in {
		fmt.Printf("[%s] processing %d + %d\n", input.ID, input.A, input.B)
		answer := input.A + input.B
		out <- AdderOutput{
			ID:     input.ID,
			Answer: answer,
		}
	}
}
