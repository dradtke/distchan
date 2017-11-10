package distchan

import (
	"encoding/gob"
	"fmt"
	"io"
	"net"
	"reflect"
	"sync"
	"time"
)

// Server represents a registration between a network listener and a pair
// of channels, one for input and one for output.
type Server struct {
	inv, outv    reflect.Value
	ln           net.Listener
	mu           sync.Mutex
	connections  []connection
	i            int
	closed, done chan struct{}
}

// Done returns a read-only channel that will be closed when the server
// has been fully shut down.
func (s *Server) Done() <-chan struct{} {
	return s.done
}

// Ready returns true if there are any clients currently connected.
func (s *Server) Ready() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.connections) > 0
}

// WaitUntilReady blocks until the server has at least one client available.
func (s *Server) WaitUntilReady() {
	for {
		time.Sleep(1 * time.Second)
		if s.Ready() {
			return
		}
	}
}

type connection struct {
	conn net.Conn
	enc  *gob.Encoder
	dec  *gob.Decoder
}

func (s *Server) handleIncomingConnections() {
	newConns := make(chan net.Conn)
	go func() {
		for {
			conn, err := s.ln.Accept()
			if err != nil {
				// for now, assume it's a "use of closed network connection" error
				break
			}
			newConns <- conn
		}
		close(s.closed)
	}()

	for {
		select {
		case <-s.closed:
			close(s.done)
			return

		case conn := <-newConns:
			c := connection{
				conn: conn,
				enc:  gob.NewEncoder(conn),
				dec:  gob.NewDecoder(conn),
			}
			s.mu.Lock()
			s.connections = append(s.connections, c)
			if s.inv != (reflect.Value{}) {
				go s.handleIncomingMessages(c)
			}
			s.mu.Unlock()
		}
	}
}

func (s *Server) handleIncomingMessages(c connection) {
	et := s.inv.Type().Elem()
	for {
		x := reflect.New(et)
		if err := c.dec.DecodeValue(x); err != nil {
			if err == io.EOF {
				return
			}
			fmt.Printf("[server] error decoding value: %s\n", err)
		}
		s.inv.Send(x.Elem())
	}
}

func (s *Server) increment() {
	s.i += 1
	if s.i >= len(s.connections) {
		s.i = 0
	}
}

func (s *Server) handleOutgoingMessages() {
	s.WaitUntilReady()
	for {
		x, ok := s.outv.Recv()
		if !ok {
			break
		}

		s.mu.Lock()
		for {
			if err := s.connections[s.i].enc.EncodeValue(x); err != nil {
				s.connections = append(s.connections[:s.i], s.connections[s.i+1:]...)
				if len(s.connections) == 0 {
					s.mu.Unlock()
					s.WaitUntilReady()
					s.mu.Lock()
				}
				s.increment()
			} else {
				break
			}
		}
		s.increment()
		s.mu.Unlock()
	}

	if err := s.ln.Close(); err != nil {
		fmt.Printf("[server] error closing listener: %s\n", err)
	}
	for _, conn := range s.connections {
		if err := conn.conn.Close(); err != nil {
			fmt.Printf("error closing connection: %s\n", err)
		}
	}
}

// ChanServer registers a pair of channels with an active listener. Gob-encoded
// messages received on the listener will be passed to in; any values passed to
// out will be gob-encoded and written to one open connection. The server uses
// a simple round-robin strategy when deciding which connection to send the message
// to; no client is favored over any others.
func ChanServer(ln net.Listener, out, in interface{}) *Server {
	s := &Server{
		outv:   reflect.ValueOf(out),
		inv:    reflect.ValueOf(in),
		ln:     ln,
		closed: make(chan struct{}),
		done:   make(chan struct{}),
	}

	go s.handleIncomingConnections()
	if out != nil {
		go s.handleOutgoingMessages()
	}
	return s
}

// ChanRead pipes incoming gob-encoded messages from conn to the channel ch.
func ChanRead(conn net.Conn, ch interface{}) {
	go func() {
		var (
			chv = reflect.ValueOf(ch)
			et  = chv.Type().Elem()
			dec = gob.NewDecoder(conn)
		)

		defer func() {
			chv.Close()

			// A panic can happen if the underlying channel was closed
			// and we tried to send on it.
			if r := recover(); r != nil {
				fmt.Println("attempted to send on a closed channel")
			}
		}()

		for {
			x := reflect.New(et)
			if err := dec.DecodeValue(x); err != nil {
				if err == io.EOF {
					return
				}
			} else {
				chv.Send(x.Elem())
			}
		}
	}()
}

// ChanWrite writes values from the channel ch as gob-encoded messages to conn.
func ChanWrite(conn net.Conn, ch interface{}) {
	go func() {
		var (
			chv = reflect.ValueOf(ch)
			enc = gob.NewEncoder(conn)
		)

		for {
			x, ok := chv.Recv()
			if !ok {
				return
			}
			if err := enc.EncodeValue(x); err != nil {
				fmt.Printf("[client] error encoding value %v: %s\n", x.Interface(), err)
			}
		}
	}()
}
