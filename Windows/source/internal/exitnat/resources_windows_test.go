// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package exitnat

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"runtime"
	"sync"
	"testing"
	"unsafe"

	"github.com/Irviodotnet/Irvio/internal/windivert"
	"github.com/Irviodotnet/Irvio/internal/wintest"
	"golang.org/x/sys/windows"
)

type resourceSample struct {
	Cycle      int            `json:"cycle"`
	Handles    uint32         `json:"handles"`
	Types      map[string]int `json:"types"`
	Goroutines int            `json:"goroutines"`
	HeapAlloc  uint64         `json:"heap_alloc"`
	HeapInuse  uint64         `json:"heap_inuse"`
}

func (s *resourceSample) include(v resourceSample) bool {
	grew := v.Handles > s.Handles || v.Goroutines > s.Goroutines
	if s.Types == nil {
		s.Types = make(map[string]int)
	}
	s.Handles = max(s.Handles, v.Handles)
	s.Goroutines = max(s.Goroutines, v.Goroutines)
	for kind, n := range v.Types {
		grew = grew || n > s.Types[kind]
		s.Types[kind] = max(s.Types[kind], n)
	}
	return grew
}

type resourceWarmup struct {
	high   resourceSample
	stable int
}

func (w *resourceWarmup) observe(s resourceSample) bool {
	if w.high.include(s) {
		w.stable = 0
	} else {
		w.stable++
	}
	return w.stable >= 20
}

func TestResourceWarmupRequiresStableHighwater(t *testing.T) {
	var stable, leaking resourceWarmup
	for i := 0; i < 100; i++ {
		ready := stable.observe(resourceSample{Handles: 10, Types: map[string]int{"File": 5}})
		if ready != (i >= 20) {
			t.Fatal("wrong stabilization window", i)
		}
		if leaking.observe(resourceSample{Handles: uint32(10 + i), Types: map[string]int{"File": 5 + i}}) {
			t.Fatal("monotonic leak accepted as warmup", i)
		}
	}
	if stable.observe(resourceSample{Handles: 11, Types: map[string]int{"File": 6}}) || stable.stable != 0 {
		t.Fatal("new growth did not reset stabilization")
	}
}

func (s resourceSample) growth(v resourceSample) error {
	allowance := map[string]int{"Thread": 2, "Event": 4, "IRTimer": 4, "WaitCompletionPacket": 2}
	if s.Types["unqueried"] != 0 || v.Types["unqueried"] != 0 {
		return fmt.Errorf("unqueried handles")
	}
	for kind, n := range v.Types {
		if n > s.Types[kind]+allowance[kind] {
			return fmt.Errorf("%s exceeded warmup envelope: %d > %d + %d", kind, n, s.Types[kind], allowance[kind])
		}
	}
	if v.Handles > s.Handles+12 {
		return fmt.Errorf("total handles exceeded warmup envelope: %d > %d + 12", v.Handles, s.Handles)
	}
	if v.Goroutines > s.Goroutines+2 {
		return fmt.Errorf("goroutines exceeded warmup envelope")
	}
	return nil
}

func TestResourceEnvelopeRejectsNonRuntimeGrowth(t *testing.T) {
	base := resourceSample{Handles: 100, Goroutines: 2, Types: map[string]int{"File": 10, "Thread": 10, "Event": 20, "IRTimer": 20, "WaitCompletionPacket": 10}}
	base.include(resourceSample{Handles: 110, Goroutines: 2, Types: map[string]int{"Thread": 12, "Event": 24}})
	if err := base.growth(resourceSample{Handles: 90, Goroutines: 2, Types: map[string]int{"File": 10, "Thread": 9, "Event": 18}}); err != nil {
		t.Fatal("lower thread count is not growth", err)
	}
	for _, kind := range []string{"File", "Key", "ALPC Port", "EtwRegistration", "unqueried"} {
		v := resourceSample{Handles: 100, Goroutines: 2, Types: map[string]int{kind: base.Types[kind] + 1}}
		if err := base.growth(v); err == nil {
			t.Fatal("accepted non-runtime growth", kind)
		}
	}
	if err := base.growth(resourceSample{Handles: 123, Types: base.Types}); err == nil {
		t.Fatal("accepted unexplained total growth")
	}
	if err := base.growth(resourceSample{Handles: 100, Types: map[string]int{"Thread": 15}}); err == nil {
		t.Fatal("accepted unbounded thread growth")
	}
}

func sampleLeaseResources(t *testing.T, cycle int) resourceSample {
	t.Helper()
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	s := resourceSample{Cycle: cycle, Types: wintest.HandleTypes(), Goroutines: runtime.NumGoroutine(), HeapAlloc: memory.HeapAlloc, HeapInuse: memory.HeapInuse}
	ok, _, err := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessHandleCount").Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&s.Handles)))
	if ok == 0 {
		t.Fatal(err)
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("RESOURCE_SAMPLE %s", data)
	return s
}

func TestNativeEncryptedResourceLifetime(t *testing.T) {
	mode := os.Getenv("IRVIO_WINDOWS_EXIT_RESOURCE_PROFILE")
	if mode == "" {
		t.Skip("requires explicit Windows resource profile fixture")
	}
	if mode != "memory" && mode != "native" {
		t.Fatal("unknown profile mode")
	}
	if os.Getenv("IRVIO_DIVERT_TEST_SERVICE") != "running" {
		t.Skip("requires explicit fixture ownership")
	}
	sampleLeaseResources(t, -1002)
	oracle := remote
	var factory func(Emit) (*Backend, error)
	if mode == "native" {
		var err error
		oracle, err = netip.ParseAddr(os.Getenv("IRVIO_WINDOWS_NAT_DNS_ORACLE"))
		if err != nil || !oracle.Is4() || oracle.IsPrivate() || !oracle.IsGlobalUnicast() {
			t.Fatal("explicit public DNS oracle required")
		}
		path := nativePortPath(t)
		sampleLeaseResources(t, -1001)
		module, err := windivert.Load()
		if err != nil {
			t.Fatal(err)
		}
		factory = func(deliver Emit) (*Backend, error) {
			trace := &resourcePacketTrace{sent: make(map[uint16]int), received: make(map[uint16]int), delivered: make(map[uint16]int)}
			b, err := NewWindowsBackend(DefaultConfig(path, nil), module, func(ctx context.Context, path Path, packet []byte) error {
				err := deliver(ctx, path, packet)
				if err == nil {
					trace.record(trace.delivered, packet)
				}
				return err
			})
			if err != nil {
				return b, err
			}
			open := b.open
			b.open = func(ctx context.Context, path Path, protocol uint8) (PacketPort, error) {
				p, err := open(ctx, path, protocol)
				if p != nil {
					return &resourceTracedPort{PacketPort: p, trace: trace}, err
				}
				return p, err
			}
			b.sender = &resourceTracedSender{PacketSender: b.sender, trace: trace, t: t}
			return b, nil
		}
	} else {
		factory = func(deliver Emit) (*Backend, error) {
			echo := &encryptedEcho{ports: make(map[uint16]*fakePacketPort)}
			return NewBackend(DefaultConfig(Path{1, 9, netip.MustParseAddr("203.0.113.9")}, nil), echo, func(context.Context, Path, uint8) (PacketPort, error) {
				echo.mu.Lock()
				defer echo.mu.Unlock()
				p := &fakePacketPort{port: uint16(41000 + len(echo.ports)), packets: make(chan []byte, 16), fault: make(chan error, 1), closed: make(chan struct{})}
				echo.ports[p.port] = p
				return p, nil
			}, deliver)
		}
	}
	sampleLeaseResources(t, -1000)
	var warmup resourceWarmup
	warmed := false
	warmCycles := 0
	for i := 1; i <= 100; i++ {
		if !t.Run(fmt.Sprintf("%s-warm-%03d", mode, i), func(t *testing.T) { encryptedLeaseScenario(t, factory, oracle) }) {
			t.Fatal("resource warmup cycle failed")
		}
		warmCycles = i
		if warmup.observe(sampleLeaseResources(t, -i)) {
			warmed = true
			break
		}
	}
	if !warmed {
		t.Fatal("resource highwater did not stabilize within 100 warmup cycles")
	}
	envelope := warmup.high
	data, _ := json.Marshal(envelope)
	t.Logf("RESOURCE_HIGHWATER warmup_cycles=%d stable_cycles=20 %s", warmCycles, data)
	for i := 1; i <= 100; i++ {
		if !t.Run(fmt.Sprintf("%s-%03d", mode, i), func(t *testing.T) { encryptedLeaseScenario(t, factory, oracle) }) {
			t.Fatal("resource cycle failed")
		}
		if i%10 == 0 {
			s := sampleLeaseResources(t, i)
			if err := envelope.growth(s); err != nil {
				t.Error(err)
			}
		}
	}
	t.Logf("RESOURCE_LIFETIME mode=%s warmup=%d cycles=100 explicit_cleanup=true", mode, warmCycles)
}

type resourcePacketTrace struct {
	mu                        sync.Mutex
	sent, received, delivered map[uint16]int
}

func (r *resourcePacketTrace) record(counts map[uint16]int, packet []byte) {
	if len(packet) >= 30 && packet[0] == 0x45 && packet[9] == UDP {
		r.mu.Lock()
		counts[binary.BigEndian.Uint16(packet[28:30])]++
		r.mu.Unlock()
	}
}

type resourceTracedPort struct {
	PacketPort
	trace *resourcePacketTrace
}

func (r *resourceTracedPort) ReceiveIPv4(ctx context.Context) ([]byte, error) {
	p, err := r.PacketPort.ReceiveIPv4(ctx)
	if err == nil {
		r.trace.record(r.trace.received, p)
	}
	return p, err
}

type resourceTracedSender struct {
	PacketSender
	trace *resourcePacketTrace
	t     *testing.T
	once  sync.Once
}

func (r *resourceTracedSender) SendIPv4(ctx context.Context, packet []byte) error {
	err := r.PacketSender.SendIPv4(ctx, packet)
	if err == nil {
		r.trace.record(r.trace.sent, packet)
	}
	return err
}

func (r *resourceTracedSender) Close() error {
	err := r.PacketSender.Close()
	r.once.Do(func() {
		r.trace.mu.Lock()
		defer r.trace.mu.Unlock()
		for tag, count := range r.trace.sent {
			if r.trace.received[tag] != count || r.trace.delivered[tag] != count {
				r.t.Logf("RESOURCE_PACKET_MISMATCH dns_id=%d sent=%d captured=%d delivered=%d", tag, count, r.trace.received[tag], r.trace.delivered[tag])
			}
		}
	})
	return err
}
