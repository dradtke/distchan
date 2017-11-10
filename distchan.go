package distchan

import (
	"encoding/gob"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"reflect"
	"sync"
	"time"
)

var (
	BindAddr = flag.String("bind", "", "bind address")
)

type Server struct {
	chv          reflect.Value
	ln           net.Listener
	mu           sync.Mutex
	connections  []connection
	i            int
	closed, done chan struct{}
}

func (s *Server) Done() <-chan struct{} {
	return s.done
}

func (s *Server) Ready() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.connections) > 0
}

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
			enc, dec := gob.NewEncoder(conn), gob.NewDecoder(conn)
			s.mu.Lock()
			s.connections = append(s.connections, connection{
				conn: conn,
				enc:  enc,
				dec:  dec,
			})
			s.mu.Unlock()
		}
	}
}

func (s *Server) handleChannelReceives() {
	for {
		x, ok := s.chv.Recv()
		if !ok {
			fmt.Println("[server] detected channel close, cleaning up")
			break
		}

		s.mu.Lock()
		// fmt.Printf("  sending to channel %d\n", s.i)
		// TODO: what to do if no clients exist yet? Probs put it right back on the channel.
		if len(s.connections) == 0 {
			fmt.Println("[server] re-sending")
			s.chv.Send(x)
			time.Sleep(2 * time.Second)
		} else {
			fmt.Printf("[server] sending %v\n", x.Interface())
			for err := s.connections[s.i].enc.EncodeValue(x); err != nil; err = s.connections[s.i].enc.EncodeValue(x) {
				fmt.Printf("[server] error encoding value: %s\n", err)
				time.Sleep(1 * time.Second)
			}
			s.i += 1
			if s.i >= len(s.connections) {
				s.i = 0
			}
		}
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

func ChanServer(bindAddr string, ch interface{}) (*Server, error) {
	if bindAddr == "" {
		return nil, errors.New("no bind address")
	}

	chv := reflect.ValueOf(ch)
	if chv.Kind() != reflect.Chan {
		return nil, errors.New("not a channel")
	}

	ln, err := net.Listen("tcp", bindAddr)
	if err != nil {
		return nil, err
	}

	s := &Server{
		chv:    chv,
		ln:     ln,
		closed: make(chan struct{}),
		done:   make(chan struct{}),
	}

	go s.handleIncomingConnections()
	go s.handleChannelReceives()

	return s, nil
}

func Chan(dialAddr string, ch interface{}) error {
	if dialAddr == "" {
		return errors.New("no dial address")
	}

	chv := reflect.ValueOf(ch)
	if chv.Kind() != reflect.Chan {
		return errors.New("not a channel")
	}
	et := chv.Type().Elem()

	conn, err := net.Dial("tcp", dialAddr)
	if err != nil {
		return err
	}
	go clientHandleConn(conn, chv, et)
	return nil
}

func clientHandleConn(conn net.Conn, chv reflect.Value, et reflect.Type) {
	var (
		enc = gob.NewEncoder(conn)
		dec = gob.NewDecoder(conn)
	)
	go func() {
		for {
			x, ok := chv.Recv()
			if !ok {
				break
			}
			if err := enc.EncodeValue(x); err != nil {
				fmt.Printf("[client] error encoding value: %s\n", err)
			}
		}
	}()

	go func() {
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
			for {
				if err := dec.DecodeValue(x); err != nil {
					if err == io.EOF {
						fmt.Println("[client] found EOF")
						return
					}
					fmt.Printf("[client] error decoding value: %s\n", err)
				} else {
					break
				}
			}
			fmt.Printf("[client] decoded %v\n", x.Elem().Interface())
			chv.Send(x.Elem())
		}
	}()
}
