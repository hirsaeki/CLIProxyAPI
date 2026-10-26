package discovery

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func TestBrowseOversizedKnownAnswerOmittedFork(t *testing.T) {
	c := testBrowseCache("")
	name := "Demo._ai-gateway._tcp.local."
	large := name + ` 120 IN TXT` + strings.Repeat(` "`+strings.Repeat("x", 200)+`"`, 8)
	small := name + ` 120 IN TXT "version=1"`
	browseTestIngest(t, c, 7, browseTestNow, large, small)
	q := dns.Question{Name: name, Qtype: dns.TypeTXT, Qclass: dns.ClassINET}
	messages := c.queryMessages(7, q, browseTestNow)
	if len(messages) != 1 || len(messages[0].Answer) != 1 {
		t.Fatalf("expected only the small known answer: %#v", messages)
	}
	for _, m := range messages {
		wire, err := m.Pack()
		if err != nil || len(wire) > 1200 {
			t.Fatalf("oversized packet %d: %v", len(wire), err)
		}
	}
	if messages[0].Truncated {
		t.Fatal("omitted oversized RR does not need a continuation")
	}
	if got := messages[0].Answer[0].(*dns.TXT).Txt; !reflect.DeepEqual(got, []string{"version=1"}) {
		t.Fatalf("wrong known answer: %v", got)
	}
}

func TestLegacyDiscoveryFeaturesRetainedAndClearedFork(t *testing.T) {
	var svc DiscoveredService
	legacy := ParseTXTRecords([]string{"version=1", "tls=0", "auth_required=true", "api_openai=/v1", "features=chat,responses,chat"})
	if !applyTXTMetadata(&svc, legacy) || !reflect.DeepEqual(svc.Features, []string{"chat", "responses"}) {
		t.Fatalf("legacy features lost: %+v", svc)
	}
	raw, err := json.Marshal(svc)
	if err != nil || !strings.Contains(string(raw), `"features":["chat","responses"]`) {
		t.Fatalf("features absent in JSON: %s %v", raw, err)
	}
	draft := ParseTXTRecords([]string{"txtvers=1", "path=/", "tls=0", "auth=1", "features=ignored"})
	if !applyTXTMetadata(&svc, draft) || len(svc.Features) != 0 {
		t.Fatalf("draft retained legacy features: %+v", svc)
	}
}

func TestBrowsePacketIndexReferenceCountsFork(t *testing.T) {
	c := testBrowseCache("")
	browseTestIngest(t, c, 7, browseTestNow, browseTestRecords()...)
	x := newBrowsePacketIndex(c)
	instance := browsePacketInstanceKey{7, browseName("Demo._ai-gateway._tcp.local.")}
	if x.instances[instance] != 3 {
		t.Fatalf("PTR/SRV/TXT references = %d", x.instances[instance])
	}
	for k := range c.records {
		x.add(c, k) // Repeated refreshes must not double-count a record.
		x.remove(c, k)
		x.remove(c, k)
		delete(c.records, k)
	}
	if len(x.instances) != 0 || len(x.rrsets) != 0 {
		t.Fatalf("stale index after removal: %+v", x)
	}
	// A replacement packet must retain both address families under a shared cap.
	browseTestIngest(t, c, 7, browseTestNow, browseTestRecords()...)
	x = newBrowsePacketIndex(c)
	if got := x.count(browseRRKey{link: 7, name: "demo.local.", kind: dns.TypeAAAA}); got != 2 {
		t.Fatalf("combined address count = %d", got)
	}
}

func TestBrowsePacketIndexTracksCapacityAndExpiryFork(t *testing.T) {
	c := testBrowseCache("")
	records := make([]string, 0, maxDiscoveredServices)
	for i := 0; i < maxDiscoveredServices; i++ {
		records = append(records, fmt.Sprintf("_ai-gateway._tcp.local. 2 IN PTR s%d._ai-gateway._tcp.local.", i))
	}
	// Insertion in the same packet must count newly admitted instances.
	browseTestIngest(t, c, 7, browseTestNow, append(records, "_ai-gateway._tcp.local. 2 IN PTR excess._ai-gateway._tcp.local.")...)
	if len(newBrowsePacketIndex(c).instances) != maxDiscoveredServices {
		t.Fatal("instance limit not maintained inside packet")
	}
	browseTestIngest(t, c, 7, browseTestNow.Add(3*time.Second), "_ai-gateway._tcp.local. 120 IN PTR new._ai-gateway._tcp.local.")
	if got := len(newBrowsePacketIndex(c).instances); got != 1 {
		t.Fatalf("expired instance references leaked: %d", got)
	}
}
