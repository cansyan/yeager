package main

import "testing"

func TestServerStatKeepsSingleLossCheap(t *testing.T) {
	fast := newServerStat(3000)
	slow := newServerStat(3000)
	for i := 0; i < 8; i++ {
		fast.Put(100, false)
		slow.Put(160, false)
	}

	fast.Put(3000, true)

	if got, wantMax := fast.Score(), slow.Score(); got >= wantMax {
		t.Fatalf("single loss should not outweigh a clearly better server history: got %d >= %d", got, wantMax)
	}
}

func TestServerStatPenalizesConsecutiveFailures(t *testing.T) {
	dead := newServerStat(3000)
	healthy := newServerStat(3000)
	for i := 0; i < 4; i++ {
		dead.Put(120, false)
		healthy.Put(180, false)
	}

	dead.Put(3000, true)
	if got, wantMax := dead.Score(), healthy.Score(); got >= wantMax {
		t.Fatalf("first failure should stay tolerable: got %d >= %d", got, wantMax)
	}

	dead.Put(3000, true)
	if got, wantMin := dead.Score(), healthy.Score(); got <= wantMin {
		t.Fatalf("consecutive failures should trigger failover: got %d <= %d", got, wantMin)
	}
}
