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
	A, B int
}

type AdderOutput struct {
	Input  AdderInput
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

	distchan.ChanRead(conn, in)
	distchan.ChanWrite(conn, out)

	for input := range in {
		fmt.Printf("processing %d + %d...\n", input.A, input.B)
		answer := input.A + input.B
		out <- AdderOutput{
			Input:  input,
			Answer: answer,
		}
	}
}
