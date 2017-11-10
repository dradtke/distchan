package distchan_test

import (
	"net"
	"reflect"
	"testing"

	"github.com/dradtke/distchan"
)

func TestDistchan(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}

	sch := make(chan string)
	server := distchan.ChanServer(ln, sch, nil)

	go func() {
		sch <- "why"
		sch <- "hello"
		sch <- "there"
		sch <- "world"
		close(sch)
	}()

	conn, err := net.Dial(ln.Addr().Network(), ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	inCh := make(chan string)
	distchan.ChanRead(conn, inCh)

	var received []string
	for msg := range inCh {
		received = append(received, msg)
	}

	if !reflect.DeepEqual(received, []string{"why", "hello", "there", "world"}) {
		t.Errorf("received unexpected values: %v", received)
	}

	<-server.Done()
}
