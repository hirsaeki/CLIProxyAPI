package discovery

import "github.com/miekg/dns"

type browsePacketRRSetKey struct {
	link int
	name string
	kind uint16
}
type browsePacketInstanceKey struct {
	link int
	name string
}

// A packet-local index bounds ingestion work without persistent bookkeeping:
// prune may remove records between packets, while all mutations within a packet
// are reflected here. Instance reference counts include PTR and SRV/TXT records.
type browsePacketIndex struct {
	rrsets    map[browsePacketRRSetKey]map[browseRRKey]struct{}
	instances map[browsePacketInstanceKey]int
}

func newBrowsePacketIndex(c *browseCache) *browsePacketIndex {
	x := &browsePacketIndex{rrsets: make(map[browsePacketRRSetKey]map[browseRRKey]struct{}), instances: make(map[browsePacketInstanceKey]int)}
	for k := range c.records {
		x.add(c, k)
	}
	return x
}
func browsePacketInstance(c *browseCache, k browseRRKey) string {
	if p, ok := c.records[k].rr.(*dns.PTR); ok {
		return browseName(p.Ptr)
	}
	if c.instance(k.name) {
		return k.name
	}
	return ""
}
func (x *browsePacketIndex) add(c *browseCache, k browseRRKey) {
	group := browsePacketRRSetKey{k.link, k.name, k.kind}
	if _, exists := x.rrsets[group][k]; exists {
		return
	}
	if x.rrsets[group] == nil {
		x.rrsets[group] = make(map[browseRRKey]struct{})
	}
	x.rrsets[group][k] = struct{}{}
	if instance := browsePacketInstance(c, k); instance != "" {
		x.instances[browsePacketInstanceKey{k.link, instance}]++
	}
}
func (x *browsePacketIndex) remove(c *browseCache, k browseRRKey) {
	group := browsePacketRRSetKey{k.link, k.name, k.kind}
	if _, exists := x.rrsets[group][k]; !exists {
		return
	}
	delete(x.rrsets[group], k)
	if len(x.rrsets[group]) == 0 {
		delete(x.rrsets, group)
	}
	if instance := browsePacketInstance(c, k); instance != "" {
		key := browsePacketInstanceKey{k.link, instance}
		x.instances[key]--
		if x.instances[key] == 0 {
			delete(x.instances, key)
		}
	}
}
func (x *browsePacketIndex) count(k browseRRKey) int {
	if k.kind == dns.TypeA || k.kind == dns.TypeAAAA {
		return len(x.rrsets[browsePacketRRSetKey{k.link, k.name, dns.TypeA}]) + len(x.rrsets[browsePacketRRSetKey{k.link, k.name, dns.TypeAAAA}])
	}
	return len(x.rrsets[browsePacketRRSetKey{k.link, k.name, k.kind}])
}
