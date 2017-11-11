package main

import (
	crypto_rand "crypto/rand"
	"encoding/gob"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"time"

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

func producer(out chan<- AdderInput) {
	for {
		input := AdderInput{
			ID: newUUID(),
			A:  rand.Intn(100),
			B:  rand.Intn(100),
		}
		fmt.Printf("[%s] requesting answer to %d + %d\n", input.ID, input.A, input.B)
		out <- input
	}
}

// ID generation method graciously borrowed from https://play.golang.org/p/4FkNSiUDMg
func newUUID() string {
	uuid := make([]byte, 16)
	n, err := io.ReadFull(crypto_rand.Reader, uuid)
	if n != len(uuid) || err != nil {
		panic(err)
	}
	uuid[8] = uuid[8]&^0xc0 | 0x80
	uuid[6] = uuid[6]&^0xf0 | 0x40
	return fmt.Sprintf("%x-%x-%x-%x-%x", uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:])
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

	server := distchan.NewServer(ln, out, in)
	server.Start()

	fmt.Println("waiting for clients to connect...")
	server.WaitUntilReady()
	fmt.Println("...and go!")

	go producer(out)

	for result := range in {
		fmt.Printf("[%s] received answer: %d\n", result.ID, result.Answer)
	}
}
