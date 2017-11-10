package main

import (
	"encoding/gob"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net"
	"time"

	"github.com/dradtke/distchan"
)

type AdderInput struct {
	A, B int
}

type AdderOutput struct {
	Input  AdderInput
	Answer int
}

func producer(out chan<- AdderInput) {
	for {
		out <- AdderInput{
			A: rand.Intn(100),
			B: rand.Intn(100),
		}
	}
}

func consumer(in <-chan AdderOutput) {
	for result := range in {
		fmt.Printf("%d + %d = %d\n", result.Input.A, result.Input.B, result.Answer)
	}
}

func main() {
	bindAddr := flag.String("bind", "", "address to bind to")
	flag.Parse()

	if *bindAddr == "" {
		log.Fatal("no bind address specified")
	}

	rand.Seed(time.Now().Unix())
	gob.Register(AdderInput{})
	gob.Register(AdderOutput{})
	var (
		out = make(chan AdderInput)
		in  = make(chan AdderOutput)
	)

	ln, err := net.Listen("tcp", *bindAddr)
	if err != nil {
		panic(err)
	}

	server := distchan.ChanServer(ln, out, in)

	fmt.Println("waiting for clients to connect...")
	server.WaitUntilReady()
	fmt.Println("...and go!")

	go consumer(in)
	go producer(out)

	<-server.Done()
}
