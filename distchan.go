package distchan

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"io"
	"log"
	"net"
	"os"
	"reflect"
	"sync"
	"time"
)

// NewServer registers a pair of channels with an active listener. Gob-encoded
// messages received on the listener will be passed to in; any values passed to
// out will be gob-encoded and written to one open connection. The server uses
// a simple round-robin strategy when deciding which connection to send the message
// to; no client is favored over any others.
//
// Note that the returned value's Start() method must be called before any
// messages will be passed. This gives the user an opportunity to register
// encoders and decoders before any data passes over the network.
func NewServer(ln net.Listener, out, in interface{}) *Server {
	return &Server{
		ln:     ln,
		outv:   reflect.ValueOf(out),
		inv:    reflect.ValueOf(in),
		closed: make(chan struct{}),
		done:   make(chan struct{}),
		logger: log.New(os.Stdout, "[distchan] ", log.Lshortfile),
	}
}

// Server represents a registration between a network listener and a pair
// of channels, one for input and one for output.
type Server struct {
	ln                 net.Listener
	inv, outv          reflect.Value
	mu                 sync.RWMutex
	connections        []*clientConn
	encoders, decoders []Transformer
	i                  int
	closed, done       chan struct{}
	logger             *log.Logger
}

type clientConn struct {
	c   net.Conn
	buf *bytes.Buffer
	enc *gob.Encoder
}

// Start instructs the server to begin serving messages.
func (s *Server) Start() *Server {
	go s.handleIncomingConnections()
	if s.outv != (reflect.Value{}) {
		go s.handleOutgoingMessages()
	}
	return s
}

// AddEncoder adds a new encoder to the server. Any outbound messages
// will be passed through all registered encoders before being sent
// over the wire. See the tests for an example of encoding the data
// using AES encryption.
func (s *Server) AddEncoder(f Transformer) *Server {
	s.encoders = append(s.encoders, f)
	return s
}

// AddDecoder adds a new decoder to the server. Any inbound messages
// will be passed through all registered decoders before being sent
// to the channel. See the tests for an example of decoding the data
// using AES encryption.
func (s *Server) AddDecoder(f Transformer) *Server {
	s.decoders = append(s.decoders, f)
	return s
}

// Ready returns true if there are any clients currently connected.
func (s *Server) Ready() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.connections) > 0
}

// WaitUntilReady blocks until the server has at least one client available.
func (s *Server) WaitUntilReady() {
	for {
		time.Sleep(1 * time.Second)
		if s.Ready() {
			s.i = 0
			return
		}
	}
}

// Logger exposes the server's internal logger so that it can be configured.
// For example, if you want the logs to go somewhere besides standard output
// (the default), you can use s.Logger().SetOutput(...).
func (s *Server) Logger() *log.Logger {
	return s.logger
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
			s.mu.RLock()
			for _, conn := range s.connections {
				if err := binary.Write(conn.c, binary.LittleEndian, int32(-1)); err != nil {
					s.logger.Println(err)
				}
			}
			s.mu.RUnlock()

			for {
				s.mu.RLock()
				if len(s.connections) == 0 {
					s.inv.Close()
					return
				}
				s.mu.RUnlock()
				time.Sleep(500 * time.Millisecond)
			}

		case c := <-newConns:
			s.mu.Lock()
			conn := &clientConn{c: c, buf: new(bytes.Buffer)}
			s.connections = append(s.connections, conn)
			if s.inv != (reflect.Value{}) {
				go s.handleIncomingMessages(conn)
			}
			s.mu.Unlock()
		}
	}
}

func (s *Server) handleIncomingMessages(conn *clientConn) {
	var (
		buf bytes.Buffer
		dec = gob.NewDecoder(&buf)
		et  = s.inv.Type().Elem()
	)

	for {
		b, err := readChunk(conn.c)
		if err != nil {
			if err != io.EOF {
				s.logger.Println(err)
			}
			break
		}

		for _, decoder := range s.decoders {
			b = decoder(b)
		}
		if _, err := buf.Write(b); err != nil {
			s.logger.Panicln(err)
		}

		x := reflect.New(et)
		if err := dec.DecodeValue(x); err != nil {
			if err == io.EOF {
				break
			}
			s.logger.Panicln(err)
		}

		buf.Reset()
		s.inv.Send(x.Elem())
	}

	s.mu.Lock()
	for i := range s.connections {
		if s.connections[i] == conn {
			s.connections = append(s.connections[:i], s.connections[i+1:]...)
			break
		}
	}
	s.mu.Unlock()
}

func (s *Server) currentConn() *clientConn {
	return s.connections[s.i]
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

		for {
			s.mu.RLock()
			if len(s.connections) == 0 {
				s.mu.RUnlock()
				s.WaitUntilReady()
				s.mu.RLock()
			}

			if s.currentConn().enc == nil {
				s.currentConn().enc = gob.NewEncoder(s.currentConn().buf)
			}

			if err := s.currentConn().enc.EncodeValue(x); err != nil {
				s.logger.Println(err)

				s.mu.RUnlock()
				s.mu.Lock()
				s.connections = append(s.connections[:s.i], s.connections[s.i+1:]...)
				s.increment()
				s.mu.Unlock()
				s.mu.RLock()
				continue
			}

			b := s.currentConn().buf.Bytes()
			s.currentConn().buf.Reset()

			for _, encoder := range s.encoders {
				b = encoder(b)
			}

			if err := writeChunk(s.currentConn().c, b); err != nil {
				s.logger.Println(err)

				s.mu.RUnlock()
				s.mu.Lock()
				s.connections = append(s.connections[:s.i], s.connections[s.i+1:]...)
				s.increment()
				s.mu.Unlock()
				s.mu.RLock()
				continue
			}

			s.mu.RUnlock()
			s.mu.Lock()
			s.increment()
			s.mu.Unlock()
			break
		}
	}

	if err := s.ln.Close(); err != nil {
		s.logger.Printf("error closing listener: %s\n", err)
	}
}

func NewClient(conn net.Conn, out, in interface{}) *Client {
	return &Client{
		conn:   conn,
		outv:   reflect.ValueOf(out),
		inv:    reflect.ValueOf(in),
		logger: log.New(os.Stdout, "[distchan] ", log.Lshortfile),
		done:   make(chan struct{}),
	}
}

// Transformer represents a function that does an arbitrary transformation
// on a piece of data. It's used for defining custom encoders and decoders
// for modifying how data is sent across the wire.
type Transformer func([]byte) []byte

// Client represents a registration between a network connection and a pair
// of channels. See the documentation for Server for more details.
type Client struct {
	conn               net.Conn
	inv, outv          reflect.Value
	encoders, decoders []Transformer
	started            bool
	logger             *log.Logger
	done               chan struct{}
}

// AddEncoder adds a new encoder to the client. Any outbound messages
// will be passed through all registered encoders before being sent
// over the wire. See the tests for an example of encoding the data
// using AES encryption.
func (c *Client) AddEncoder(f Transformer) *Client {
	c.encoders = append(c.encoders, f)
	return c
}

// AddDecoder adds a new decoder to the client. Any inbound messages
// will be passed through all registered decoders before being sent
// to the channel. See the tests for an example of decoding the data
// using AES encryption.
func (c *Client) AddDecoder(f Transformer) *Client {
	c.decoders = append(c.decoders, f)
	return c
}

// Start instructs the client to begin serving messages.
func (c *Client) Start() *Client {
	if c.inv != (reflect.Value{}) {
		go c.handleIncomingMessages()
	}
	if c.outv != (reflect.Value{}) {
		go c.handleOutgoingMessages()
	}
	c.started = true
	return c
}

// Done returns a channel that will be closed once all in-flight data has been
// handled.
func (c *Client) Done() <-chan struct{} {
	return c.done
}

// Logger exposes the client's internal logger so that it can be configured.
// For example, if you want the logs to go somewhere besides standard output
// (the default), you can use c.Logger().SetOutput(...).
func (c *Client) Logger() *log.Logger {
	return c.logger
}

func (c *Client) handleIncomingMessages() {
	var (
		buf bytes.Buffer
		// The gob decoder uses a buffer because its underlying reader
		// can't change without running into an "unknown type id" error.
		dec = gob.NewDecoder(&buf)
		et  = c.inv.Type().Elem()
	)

	defer func() {
		c.inv.Close()

		// A panic can happen if the underlying channel was closed
		// and we tried to send on it, or if there was a decryption
		// failure. We don't want the panic to go all the way to the
		// top, but we do want to stop processing and log the error.
		if r := recover(); r != nil {
			c.logger.Println(r)
		}
	}()

	for {
		b, err := readChunk(c.conn)
		if err != nil {
			if err == io.EOF {
				break
			}
			panic(err)
		}

		for _, decoder := range c.decoders {
			b = decoder(b)
		}
		if _, err := buf.Write(b); err != nil {
			panic(err)
		}

		x := reflect.New(et)
		if err := dec.DecodeValue(x); err != nil {
			if err == io.EOF {
				break
			}
			panic(err)
		}

		buf.Reset()
		c.inv.Send(x.Elem())
	}
}

func (c *Client) handleOutgoingMessages() {
	var (
		buf bytes.Buffer
		enc = gob.NewEncoder(&buf)
	)

	for {
		x, ok := c.outv.Recv()
		if !ok {
			break
		}
		if err := enc.EncodeValue(x); err != nil {
			c.logger.Panicln(err)
		}

		b := buf.Bytes()
		buf.Reset()

		for _, encoder := range c.encoders {
			b = encoder(b)
		}

		if err := writeChunk(c.conn, b); err != nil {
			c.logger.Printf("error writing value to connection: %s\n", err)
		}
	}

	close(c.done)
}

func readChunk(r io.Reader) ([]byte, error) {
	var n int32
	if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
		return nil, err
	}

	if n == -1 {
		return nil, io.EOF
	}

	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return nil, err
	}

	return b, nil
}

func writeChunk(w io.Writer, b []byte) error {
	if err := binary.Write(w, binary.LittleEndian, int32(len(b))); err != nil {
		return err
	}
	if _, err := w.Write(b); err != nil {
		return err
	}
	return nil
}
