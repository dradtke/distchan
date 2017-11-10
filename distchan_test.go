package distchan_test

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/dradtke/distchan"
)

func Test(t *testing.T) {
	var (
		s   *distchan.Server
		err error
	)

	go func() {
		sch := make(chan string)
		if s, err = distchan.ChanServer("localhost:5678", sch); err != nil {
			t.Fatal(err)
		}
		s.WaitUntilReady()
		sch <- "why"
		sch <- "hello"
		sch <- "there"
		sch <- "world"
		close(sch)
	}()

	cch := make(chan string)
	if err := distchan.Chan("localhost:5678", cch); err != nil {
		t.Fatal(err)
	}

	var received []string
	for msg := range cch {
		fmt.Printf("[client] received %v!\n", msg)
		received = append(received, msg)
	}

	if !reflect.DeepEqual(received, []string{"why", "hello", "there", "world"}) {
		t.Errorf("received unexpected values: %v", received)
	}

	<-s.Done()
}
