package main

import (
	"testing"
	"time"
)

// TestWatchdogIntervalDefault pins the fixed cadence: 保号 (preserve) 池已于本次同步移除，
// watchdog 不再接受 interval/enabled 配置，只剩「刷新积分 + 定时活跃测试」两项职责。
func TestWatchdogIntervalDefault(t *testing.T) {
	if watchdogIntervalDefault != 10*time.Minute {
		t.Fatalf("watchdogIntervalDefault = %v, want 10m", watchdogIntervalDefault)
	}
}

// TestWaitHostReadyForWatchdog covers the startup-readiness probe used to
// close the init-race blind window.
func TestWaitHostReadyForWatchdog(t *testing.T) {
	// Case 1: always-ready, short maxWait — instant true, zero sleeps.
	calls := 0
	ready := func() bool { calls++; return true }
	if !waitHostReadyForWatchdog(50*time.Millisecond, ready) {
		t.Fatal("always-ready should return true")
	}
	if calls != 1 {
		t.Fatalf("expected one ready() call, got %d", calls)
	}

	// Case 2: becomes-ready on the second call.
	calls = 0
	ready = func() bool { calls++; return calls >= 2 }
	if !waitHostReadyForWatchdog(2*time.Second, ready) {
		t.Fatal("eventually-ready should return true")
	}
	if calls < 2 {
		t.Fatalf("expected at least 2 probes, got %d", calls)
	}

	// Case 3: never-ready, maxWait=0
	calls = 0
	ready = func() bool { calls++; return false }
	if waitHostReadyForWatchdog(0, ready) {
		t.Fatal("maxWait=0 + never-ready must return false")
	}
	if calls != 1 {
		t.Fatalf("maxWait=0 must probe exactly once, got %d", calls)
	}

	// Case 4: never-ready with a tight maxWait
	calls = 0
	ready = func() bool { calls++; return false }
	start := time.Now()
	if waitHostReadyForWatchdog(20*time.Millisecond, ready) {
		t.Fatal("never-ready under a finite maxWait must return false")
	}
	elapsed := time.Since(start)
	if elapsed > 200*time.Millisecond {
		t.Fatalf("wait overshot the deadline: %v", elapsed)
	}
	if calls < 1 {
		t.Fatalf("never-ready should be probed at least once, got %d", calls)
	}
}

// TestRequestWatchdogTickCoalesces proves the trigger channel collapses
// concurrent requesters onto a single queued tick.
func TestRequestWatchdogTickCoalesces(t *testing.T) {
	for {
		select {
		case <-watchdogTickCh:
		default:
			goto drained
		}
	}
drained:
	requestWatchdogTick()
	requestWatchdogTick()
	requestWatchdogTick()
	select {
	case <-watchdogTickCh:
	default:
		t.Fatal("expected at least one tick queued after coalesced requests")
	}
	select {
	case <-watchdogTickCh:
		t.Fatal("channel should hold at most one queued value")
	default:
	}
}
