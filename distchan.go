package distchan

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"errors"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"reflect"
	"runtime"
	"sync"
)

const defaultSignature int32 = 0x7f38b034

var (
	errNotChannel       = errors.New("distchan: provided value is not a channel")
	errInvalidSignature = errors.New("distchan: invalid signature")
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
func NewServer(ln net.Listener, out, in interface{}) (*Server, error) {
	if in != nil && reflect.ValueOf(in).Kind() != reflect.Chan {
		return nil, errNotChannel
	}
	if out != nil && reflect.ValueOf(out).Kind() != reflect.Chan {
		return nil, errNotChannel
	}
	s := &Server{
		ln:        ln,
		out:       out,
		in:        in,
		signature: defaultSignature,
		logger:    log.New(os.Stdout, "[distchan] ", log.Lshortfile),
	}
	go s.handleIncomingConnections()
	return s, nil
}

// Server represents a registration between a network listener and a pair
// of channels, one for input and one for output.
type Server struct {
	ln                    net.Listener
	in, out               interface{}
	signature             int32
	mu                    sync.RWMutex
	i                     int
	conns                 []net.Conn
	encoders, decoders    []Transformer
	started, shuttingDown bool
	logger                *log.Logger
}

// Start instructs the server to begin serving messages.
func (s *Server) Start() *Server {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.in != nil {
		for _, conn := range s.conns {
			go s.handleIncomingMessages(conn)
		}
	}
	if s.out != nil {
		go s.handleOutgoingMessages()
	}
	s.started = true
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

// NumClient returns the number of clients connected to this server.
func (s *Server) NumClient() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return len(s.conns)
}

// Ready returns true if there are any clients currently connected.
func (s *Server) Ready() bool {
	return s.NumClient() > 0
}

// WaitUntilReady blocks until the server has at least one client available.
func (s *Server) WaitUntilReady() {
	for {
		runtime.Gosched()
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

// SetSignature sets the signature used by the server. The signature
// is the first value written to the connection on each message sent.
// When either side receives a new message, the first four bytes must
// match this signature or else the read is aborted.
//
// A sensibly-random value is provided by default, but this method
// can be used to enforce a custom signature instead.
func (s *Server) SetSignature(signature int32) {
	s.signature = signature
}

func (s *Server) handleIncomingConnections() {
	defer s.shutdown()

	for {
		conn, err := s.ln.Accept()
		if err != nil {
			// for now, assume it's a "use of closed network connection" error
			s.shuttingDown = true
			return
		}
		s.addConn(conn)
		if s.started && s.in != nil {
			go s.handleIncomingMessages(conn)
		}
	}
}

func (s *Server) handleIncomingMessages(conn net.Conn) {
	var (
		buf bytes.Buffer
		dec = gob.NewDecoder(&buf)
		et  = reflect.TypeOf(s.in).Elem()
	)

	defer s.deleteConn(conn)

	for {
		buf.Reset()
		if err := readChunk(&buf, conn, s.signature); err != nil {
			if err != io.EOF {
				s.logger.Printf("%s (dropping connection)", err)
			}
			return
		}

		runTransformers(&buf, s.decoders)

		x := reflect.New(et)
		if err := dec.DecodeValue(x); err != nil {
			if err == io.EOF {
				return
			}
			panic(err)
		}

		reflect.ValueOf(s.in).Send(x.Elem())
	}
}

func (s *Server) handleOutgoingMessages() {
	var (
		buf bytes.Buffer
		// Each connection needs a unique *gob.Encoder in order
		// to avoid "unknown type id" errors.
		gobbers = make(map[net.Conn]*gob.Encoder)
	)

	defer func() {
		if err := s.ln.Close(); err != nil {
			s.logger.Printf("error closing listener: %s\n", err)
		}
	}()

	for {
		x, ok := reflect.ValueOf(s.out).Recv()
		if !ok {
			return
		}

		for {
			conn := s.currentConn()
			if conn == nil {
				s.WaitUntilReady()
				continue
			}

			if gobbers[conn] == nil {
				gobbers[conn] = gob.NewEncoder(&buf)
			}

			buf.Reset()
			if err := gobbers[conn].EncodeValue(x); err != nil {
				s.logger.Printf("%s (dropping connection and trying again)", err)
				s.deleteConn(conn)
				continue
			}

			runTransformers(&buf, s.encoders)

			if err := writeChunk(conn, &buf, s.signature); err != nil {
				s.logger.Printf("%s (dropping connection and trying again)", err)
				s.deleteConn(conn)
				continue
			}

			break
		}

		s.nextConn()
	}
}

func (s *Server) addConn(conn net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.conns = append(s.conns, conn)
}

func (s *Server) currentConn() net.Conn {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.conns) == 0 {
		return nil
	}
	if s.i >= len(s.conns) {
		s.i = 0
	}
	return s.conns[s.i]
}

func (s *Server) deleteConn(conn net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.conns {
		if s.conns[i] == conn {
			s.conns = append(s.conns[:i], s.conns[i+1:]...)
			break
		}
	}

	if s.shuttingDown && len(s.conns) == 0 {
		reflect.ValueOf(s.in).Close()
	}
}

func (s *Server) nextConn() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.i++
	if s.i >= len(s.conns) {
		s.i = 0
	}
}

func (s *Server) shutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, conn := range s.conns {
		if err := binary.Write(conn, binary.LittleEndian, s.signature); err != nil {
			s.logger.Println(err)
		}
		if err := binary.Write(conn, binary.LittleEndian, int32(-1)); err != nil {
			s.logger.Println(err)
		}
	}
}

func NewClient(conn net.Conn, out, in interface{}) (*Client, error) {
	if in != nil && reflect.ValueOf(in).Kind() != reflect.Chan {
		return nil, errNotChannel
	}
	if out != nil && reflect.ValueOf(out).Kind() != reflect.Chan {
		return nil, errNotChannel
	}
	return &Client{
		conn:      conn,
		out:       out,
		in:        in,
		signature: defaultSignature,
		logger:    log.New(os.Stdout, "[distchan] ", log.Lshortfile),
		done:      make(chan struct{}),
	}, nil
}

// Transformer represents a function that does an arbitrary transformation
// on a piece of data. It's used for defining custom encoders and decoders
// for modifying how data is sent across the wire.
type Transformer func(src io.Reader) io.Reader

func runTransformers(buf *bytes.Buffer, transformers []Transformer) {
	if len(transformers) == 0 {
		return
	}
	var r io.Reader = buf
	for _, f := range transformers {
		r = f(r)
	}
	b, err := ioutil.ReadAll(r)
	if err != nil {
		panic(err)
	}
	buf.Reset()
	if _, err := buf.Write(b); err != nil {
		panic(err)
	}
}

// Client represents a registration between a network connection and a pair
// of channels. See the documentation for Server for more details.
type Client struct {
	conn               net.Conn
	in, out            interface{}
	signature          int32
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
	if c.in != nil {
		go c.handleIncomingMessages()
	}
	if c.out != nil {
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

// SetSignature sets the signature used by the client. The signature
// is the first value written to the connection on each message sent.
// When either side receives a new message, the first four bytes must
// match this signature or else the read is aborted.
//
// A sensibly-random value is provided by default, but this method
// can be used to enforce a custom signature instead.
func (c *Client) SetSignature(signature int32) {
	c.signature = signature
}

func (c *Client) handleIncomingMessages() {
	var (
		buf bytes.Buffer
		// The gob decoder uses a buffer because its underlying reader
		// can't change without running into an "unknown type id" error.
		dec = gob.NewDecoder(&buf)
		et  = reflect.TypeOf(c.in).Elem()
	)

	defer func() {
		reflect.ValueOf(c.in).Close()

		// A panic can happen if the underlying channel was closed
		// and we tried to send on it, or if there was a decryption
		// failure. We don't want the panic to go all the way to the
		// top, but we do want to stop processing and log the error.
		if r := recover(); r != nil {
			c.logger.Println(r)
		}
	}()

	for {
		buf.Reset()
		if err := readChunk(&buf, c.conn, c.signature); err != nil {
			if err == io.EOF {
				return
			}
			panic(err)
		}

		runTransformers(&buf, c.decoders)

		x := reflect.New(et)
		if err := dec.DecodeValue(x); err != nil {
			if err == io.EOF {
				return
			}
			panic(err)
		}

		reflect.ValueOf(c.in).Send(x.Elem())
	}
}

func (c *Client) handleOutgoingMessages() {
	var (
		buf bytes.Buffer
		enc = gob.NewEncoder(&buf)
	)

	for {
		buf.Reset()

		x, ok := reflect.ValueOf(c.out).Recv()
		if !ok {
			break
		}
		if err := enc.EncodeValue(x); err != nil {
			c.logger.Panicln(err)
		}

		runTransformers(&buf, c.encoders)

		if err := writeChunk(c.conn, &buf, c.signature); err != nil {
			c.logger.Printf("error writing value to connection: %s\n", err)
		}
	}

	close(c.done)
}

func readChunk(buf io.Writer, r io.Reader, signature int32) error {
	var n int32
	if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
		return err
	}

	if n != signature {
		return errInvalidSignature
	}

	if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
		return err
	}

	if n == -1 {
		return io.EOF
	}

	_, err := io.CopyN(buf, r, int64(n))
	return err
}

func writeChunk(w io.Writer, buf *bytes.Buffer, signature int32) error {
	if err := binary.Write(w, binary.LittleEndian, signature); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, int32(buf.Len())); err != nil {
		return err
	}
	_, err := buf.WriteTo(w)
	return err
}
