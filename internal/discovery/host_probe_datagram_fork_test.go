package discovery

import (
	"bytes"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/net/ipv4"
)

func TestHostProbeReceivesLargeConflictDatagramFork(t *testing.T) {
	const host = "large-conflict.local."
	name, err := dnsmessage.NewName(host)
	if err != nil {
		t.Fatal(err)
	}
	builder := dnsmessage.NewBuilder(nil, dnsmessage.Header{Response: true, Authoritative: true})
	if err := builder.StartAnswers(); err != nil {
		t.Fatal(err)
	}
	// One large RR is valid mDNS and may exceed the old 4 KiB receive buffer.
	text := make([]string, 20)
	for i := range text {
		text[i] = strings.Repeat("x", 250)
	}
	if err := builder.TXTResource(dnsmessage.ResourceHeader{
		Name: name, Type: dnsmessage.TypeTXT, Class: dnsmessage.ClassINET, TTL: mdnsHostRecordTTL,
	}, dnsmessage.TXTResource{TXT: text}); err != nil {
		t.Fatal(err)
	}
	packet, err := builder.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if len(packet) <= 4096 || !hasHostResponseRecord(packet, host) {
		t.Fatal("fixture must be a complete conflict response larger than 4 KiB")
	}
	if hasHostResponseRecord(packet[:4096], host) {
		t.Fatal("truncated fixture unexpectedly retained the conflict record")
	}

	receiver, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := receiver.Close(); err != nil {
			t.Error(err)
		}
	})
	sender, err := net.DialUDP("udp4", nil, receiver.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := sender.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := sender.Write(packet); err != nil {
		t.Fatal(err)
	}
	if err := receiver.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, mdnsReceiveBufferBytes)
	n, _, _, err := ipv4.NewPacketConn(receiver).ReadFrom(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buffer[:n], packet) || !hasHostResponseRecord(buffer[:n], host) {
		t.Fatalf("received %d of %d bytes; the host conflict must remain detectable", n, len(packet))
	}
}
