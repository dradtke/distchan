package distchan_test

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"io"
	"io/ioutil"
	"net"
	"reflect"
	"testing"

	"github.com/dradtke/distchan"
)

// TestDistchan is the most basic test to ensure that data is transmitted
// from the server to the client.
func TestDistchan(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}

	serverOut := make(chan string)
	server, _ := distchan.NewServer(ln, serverOut, nil)
	server.Start()

	conn, err := net.Dial(ln.Addr().Network(), ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	clientIn := make(chan string)
	client, _ := distchan.NewClient(conn, nil, clientIn)
	client.Start()

	go func() {
		serverOut <- "why"
		serverOut <- "hello"
		serverOut <- "there"
		serverOut <- "world"
		close(serverOut)
	}()

	var received []string
	for msg := range clientIn {
		received = append(received, msg)
	}

	if !reflect.DeepEqual(received, []string{"why", "hello", "there", "world"}) {
		t.Errorf("received unexpected values: %v", received)
	}
}

// TestEncryption tests that data is encrypted in transit and properly
// decrypted on the other side.
func TestEncryption(t *testing.T) {
	t.Parallel()

	encrypter := func(key []byte) distchan.Transformer {
		return func(buf io.Reader) io.Reader {
			block, err := aes.NewCipher(key)
			if err != nil {
				panic(err)
			}
			plaintext, _ := ioutil.ReadAll(buf)
			ciphertext := make([]byte, aes.BlockSize+len(plaintext))
			iv := ciphertext[:aes.BlockSize]
			if _, err := io.ReadFull(rand.Reader, iv); err != nil {
				panic(err)
			}

			stream := cipher.NewCFBEncrypter(block, iv)
			stream.XORKeyStream(ciphertext[aes.BlockSize:], plaintext)

			return bytes.NewReader(ciphertext)
		}
	}

	decrypter := func(key []byte) distchan.Transformer {
		return func(buf io.Reader) io.Reader {
			block, err := aes.NewCipher(key)
			if err != nil {
				panic(err)
			}
			ciphertext, _ := ioutil.ReadAll(buf)
			if len(ciphertext) < aes.BlockSize {
				panic("ciphertext too short")
			}
			iv := ciphertext[:aes.BlockSize]
			ciphertext = ciphertext[aes.BlockSize:]

			stream := cipher.NewCFBDecrypter(block, iv)
			stream.XORKeyStream(ciphertext, ciphertext)

			return bytes.NewReader(ciphertext)
		}
	}

	test := func(t *testing.T, serverKey, clientKey []byte) {
		ln, err := net.Listen("tcp", "localhost:0")
		if err != nil {
			t.Fatal(err)
		}

		serverOut := make(chan string)
		server, _ := distchan.NewServer(ln, serverOut, nil)
		server.AddEncoder(encrypter(serverKey))
		server.Logger().SetOutput(ioutil.Discard)
		server.Start()

		go func() {
			serverOut <- "why"
			serverOut <- "hello"
			serverOut <- "there"
			serverOut <- "world"
			close(serverOut)
		}()

		conn, err := net.Dial(ln.Addr().Network(), ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}

		clientIn := make(chan string)
		client, _ := distchan.NewClient(conn, nil, clientIn)
		client.AddDecoder(decrypter(clientKey))
		client.Logger().SetOutput(ioutil.Discard)
		client.Start()

		var received []string
		for msg := range clientIn {
			received = append(received, msg)
		}

		if bytes.Equal(serverKey, clientKey) {
			if !reflect.DeepEqual(received, []string{"why", "hello", "there", "world"}) {
				t.Errorf("received unexpected values: %v", received)
			}
		} else {
			if len(received) > 0 {
				t.Errorf("received unexpected values: %v", received)
			}
		}
	}

	t.Run("Success", func(t *testing.T) {
		key := []byte("the-key-has-to-be-32-bytes-long!")
		test(t, key, key)
	})

	t.Run("Failure", func(t *testing.T) {
		test(t, []byte("the-key-has-to-be-32-bytes-long!"), []byte("the-key-has-to-be-32-bytes-long?"))
	})
}

// TestDistribution tests that the server is distributing inputs evenly
// across all connected clients.
func TestDistribution(t *testing.T) {
	t.Parallel()

	const n = 10

	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}

	serverOut := make(chan int)
	server, _ := distchan.NewServer(ln, serverOut, nil)

	allClientIns := make([]chan int, 0, n)

	for i := 0; i < 10; i++ {
		conn, err := net.Dial(ln.Addr().Network(), ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}

		clientIn := make(chan int)
		client, _ := distchan.NewClient(conn, nil, clientIn)
		client.Start()
		allClientIns = append(allClientIns, clientIn)
	}

	go func() {
		for i := 0; i < n*10; i++ {
			serverOut <- i
		}
		close(serverOut)
	}()
	server.Start()

	for i, clientIn := range allClientIns {
		for value := range clientIn {
			if (value % n) != i {
				t.Fatal("unexpected value")
			}
		}
	}
}

// TestInvalidSignature verifies that communication fails if the server
// and client use different signatures.
func TestInvalidSignature(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}

	serverOut := make(chan string)
	server, _ := distchan.NewServer(ln, serverOut, nil)
	server.SetSignature(1)
	server.Start()

	conn, err := net.Dial(ln.Addr().Network(), ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	clientIn := make(chan string)
	client, _ := distchan.NewClient(conn, nil, clientIn)
	client.Logger().SetOutput(ioutil.Discard)
	server.SetSignature(2)
	client.Start()

	go func() {
		serverOut <- "why"
		serverOut <- "hello"
		serverOut <- "there"
		serverOut <- "world"
		close(serverOut)
	}()

	var received []string
	for msg := range clientIn {
		received = append(received, msg)
	}

	if len(received) > 0 {
		t.Error("received values when none were expected")
	}
}
