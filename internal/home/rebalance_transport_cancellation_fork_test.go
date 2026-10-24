package home

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"

	"sync"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
)

func TestRebalanceCommandConcurrentCancellationFork(t *testing.T) {
	blocked := make(chan struct{})
	release := make(chan struct{})
	var entered sync.Once
	client, _ := newRedisCommandTestClient(t, func(args []string) string {
		if strings.EqualFold(args[0], "CLUSTER") && len(args) > 2 && args[2] == "ENABLE" {
			entered.Do(func() { close(blocked) })
			<-release
		}
		return balanceTestRedisReply(args)
	})
	t.Cleanup(func() { close(release) })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { _, err := client.rebalanceCommand(ctx, "", "CLUSTER", "BALANCE", "ENABLE"); done <- err }()
	select {
	case <-blocked:
	case <-time.After(3 * time.Second):
		t.Fatal("rebalance RPC did not start")
	}
	var cancels sync.WaitGroup
	for range 16 {
		cancels.Go(cancel)
	}
	cancels.Wait()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("interrupted rebalance command succeeded")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation did not interrupt the dedicated rebalance connection")
	}
	if err := client.Ping(context.Background()); err != nil {
		t.Fatalf("cancellation disturbed command pool: %v", err)
	}
}

type rebalanceWriteSignalConnFork struct {
	net.Conn
	started chan struct{}
	once    sync.Once
}

func (c *rebalanceWriteSignalConnFork) Write(p []byte) (int, error) {
	if c.started != nil {
		c.once.Do(func() { close(c.started) })
	}
	return c.Conn.Write(p)
}

func TestRebalanceTransportTLSWriteShutdownFork(t *testing.T) {
	raw, peer := net.Pipe()
	t.Cleanup(func() { _ = raw.Close(); _ = peer.Close() })
	cert := newHomeTestCertificate(t)
	roots := x509.NewCertPool()
	roots.AddCert(cert.Leaf)
	signal := &rebalanceWriteSignalConnFork{Conn: raw}
	client := tls.Client(signal, &tls.Config{RootCAs: roots, ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS12})
	server := tls.Server(peer, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS12})
	handshakes := make(chan error, 2)
	go func() { handshakes <- client.Handshake() }()
	go func() { handshakes <- server.Handshake() }()
	for range 2 {
		select {
		case err := <-handshakes:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("TLS handshake stalled")
		}
	}
	signal.started = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	transport := newPluginSyncCancelableConn(ctx, client)
	writeDone := make(chan error, 1)
	go func() { _, err := client.Write(make([]byte, 64*1024)); writeDone <- err }()
	select {
	case <-signal.started:
	case <-time.After(2 * time.Second):
		t.Fatal("TLS write did not start")
	}
	closed := make(chan struct{})
	go func() { cancel(); _ = transport.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown waited for TLS write or close-notify")
	}
	select {
	case err := <-writeDone:
		if err == nil {
			t.Fatal("interrupted TLS write succeeded")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("TLS writer did not stop")
	}
}

func TestRebalanceCommandAlreadyCancelledFork(t *testing.T) {
	client, commands := newRedisCommandTestClient(t, balanceTestRedisReply)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.rebalanceCommand(ctx, "", "CLUSTER", "BALANCE", "ENABLE"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled command error = %v", err)
	}
	if len(commands.All()) != 0 {
		t.Fatal("cancelled command reached transport")
	}
}

func TestHomeCommandTLSHandshakeCancellationFork(t *testing.T) {
	for _, mode := range []string{"rebalance", "latency"} {
		t.Run(mode, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			host, portText, err := net.SplitHostPort(listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			port, err := strconv.Atoi(portText)
			if err != nil {
				t.Fatal(err)
			}
			accepted := make(chan net.Conn, 1)
			go func() {
				conn, err := listener.Accept()
				if err == nil {
					accepted <- conn
				}
			}()
			client := New(config.HomeConfig{Enabled: true, Host: host, Port: port, TLS: config.HomeTLSConfig{Enable: true}})
			t.Cleanup(client.Close)
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			done := make(chan error, 1)
			go func() {
				var err error
				if mode == "rebalance" {
					_, err = client.rebalanceCommand(ctx, "", "CLUSTER", "BALANCE", "ENABLE")
				} else {
					_, err = client.probeClusterLatency(ctx, listener.Addr().String())
				}
				done <- err
			}()
			peerClosed := make(chan error, 1)
			select {
			case conn := <-accepted:
				t.Cleanup(func() { _ = conn.Close() })
				go func() { _, err := io.Copy(io.Discard, conn); peerClosed <- err }()
			case <-time.After(2 * time.Second):
				t.Fatal("TLS connection was not accepted")
			}
			cancel()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("interrupted TLS handshake succeeded")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("cancellation waited for TLS dial timeout")
			}
			select {
			case <-peerClosed:
			case <-time.After(2 * time.Second):
				t.Fatal("cancellation left the TLS handshake socket open")
			}
		})
	}
}
