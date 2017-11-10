package main

import (
	"bufio"
	"log"
	"os"

	"github.com/dradtke/distchan"
)

func main() {
	ch := make(chan string)
	done, err := distchan.ChanServer(":5678", ch)
	if err != nil {
		log.Fatal(err)
	}

	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		ch <- scanner.Text()
	}
	if scanner.Err() != nil {
		panic(scanner.Err())
	}
	close(ch)
	<-done
}
