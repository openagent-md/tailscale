// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

// Regression test for:
//   - tailscale/tailscale#18112 (upstream)
//   - tailscale/tailscale#18113 (upstream fix)
//   - coder/coder#25380 (Coder-side impact)

package wgengine

import (
	"encoding/binary"
	"net/netip"
	"testing"

	"tailscale.com/net/packet"
	"tailscale.com/types/ipproto"
)

// TestTSMPPongCallbackLeak is a regression test for
// tailscale/tailscale#18112. It registers 100 TSMP pong callbacks and
// fires the production OnTSMPPongReceived handler for each one.
//
// Without the delete(e.pongCallback) fix: FAIL, 100 stale entries.
// With the fix: PASS, 0 entries.
func TestTSMPPongCallbackLeak(t *testing.T) {
	eng, err := NewFakeUserspaceEngine(t.Logf, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Close)
	e := eng.(*userspaceEngine)

	const N = 100
	for i := range N {
		var data [8]byte
		data[0] = byte(i)
		data[1] = byte(i >> 8)

		done := make(chan struct{})
		e.setTSMPPongCallback(data, func(_ packet.TSMPPongReply) { close(done) })

		e.tundev.OnTSMPPongReceived(packet.TSMPPongReply{Data: data})
		<-done
	}

	e.mu.Lock()
	n := len(e.pongCallback)
	e.mu.Unlock()
	if n != 0 {
		t.Fatalf("pongCallback map has %d stale entries after %d successful pongs; want 0", n, N)
	}
}

// TestICMPEchoResponseCallbackLeak is a regression test for
// tailscale/tailscale#18112. It registers 100 ICMP echo response
// callbacks and fires the production OnICMPEchoResponseReceived
// handler with a constructed ICMP echo reply packet for each one.
//
// Without the delete(e.icmpEchoResponseCallback) fix: FAIL, 100 stale entries.
// With the fix: PASS, 0 entries.
func TestICMPEchoResponseCallbackLeak(t *testing.T) {
	eng, err := NewFakeUserspaceEngine(t.Logf, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Close)
	e := eng.(*userspaceEngine)

	const N = 100
	for i := range N {
		idSeq := uint32(0x1000 + i)

		done := make(chan struct{})
		e.setICMPEchoResponseCallback(idSeq, func() { close(done) })

		p := buildICMP4EchoReply(t, idSeq)
		e.tundev.OnICMPEchoResponseReceived(p)
		<-done
	}

	e.mu.Lock()
	n := len(e.icmpEchoResponseCallback)
	e.mu.Unlock()
	if n != 0 {
		t.Fatalf("icmpEchoResponseCallback map has %d stale entries after %d successful responses; want 0", n, N)
	}
}

// buildICMP4EchoReply constructs a minimal IPv4 ICMP echo reply packet
// whose EchoIDSeq() returns the given idSeq value.
func buildICMP4EchoReply(t *testing.T, idSeq uint32) *packet.Parsed {
	t.Helper()
	src := netip.MustParseAddr("100.64.0.1")
	dst := netip.MustParseAddr("100.64.0.2")

	// 4 bytes of id+seq payload, which EchoIDSeq() reads.
	var payload [4]byte
	binary.LittleEndian.PutUint32(payload[:], idSeq)

	h := packet.ICMP4Header{
		IP4Header: packet.IP4Header{
			IPProto: ipproto.ICMPv4,
			Src:     src,
			Dst:     dst,
		},
		Type: packet.ICMP4EchoReply,
		Code: packet.ICMP4NoCode,
	}
	buf := packet.Generate(h, payload[:])

	p := new(packet.Parsed)
	p.Decode(buf)

	if got := p.EchoIDSeq(); got != idSeq {
		t.Fatalf("constructed packet EchoIDSeq = %#x; want %#x", got, idSeq)
	}
	return p
}
