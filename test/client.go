package main

import (
	"fmt"
	"log"

	"github.com/dradtke/distchan"
)

func main() {
	ch := make(chan string, 13)
	if err := distchan.Chan(":5678", ch); err != nil {
		log.Fatal(err)
	}

	fmt.Println("waiting for messages...")
	for msg := range ch {
		fmt.Println(msg)
	}
}
