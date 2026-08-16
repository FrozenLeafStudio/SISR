package input

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/Alia5/SISR/input/viiperdevice"
	"github.com/Alia5/SISR/sdl"
	"github.com/Alia5/VIIPER/viiperclient"
	"github.com/Alia5/VIIPER/viipertypes"
)

func TestParseHexID(t *testing.T) {
	cases := []struct {
		in   string
		want uint16
	}{
		{"0x045e", 0x045e},
		{"0X045E", 0x045e},
		{"045e", 0x045e},
		{"0x028e", 0x028e},
		{"", 0},
		{"not-hex", 0},
		{"0x1ffff", 0}, // wider than uint16
	}

	for _, tc := range cases {
		if got := parseHexID(tc.in); got != tc.want {
			t.Errorf("parseHexID(%q) = %#x, want %#x", tc.in, got, tc.want)
		}
	}
}

// A signature registered from VIIPER's hex strings must match one built from
// the integers SDL reports, or the guard never fires.
func TestRegisterEmulatedPadMatchesSDLSignature(t *testing.T) {
	ds := newTestStore()
	dev := &Device{}
	ds.RegisterEmulatedPad(dev, "0x045e", "0x028e")

	if dev.EmulatedSignature != padSignature(0x045e, 0x028e) {
		t.Fatalf("registered signature %q does not match SDL-built signature", dev.EmulatedSignature)
	}
}

func TestSelfPadGuardClaimsOnlyCreatedPads(t *testing.T) {
	ds := newTestStore()
	ds.devices[1] = &Device{EmulatedSignature: padSignature(0x045e, 0x028e)}

	if ds.isOwnEmulatedPadLocked(0x054c, 0x09cc) {
		t.Fatal("a signature SISR never created must not be claimed")
	}
	if ds.isOwnEmulatedPadLocked(0, 0) {
		t.Fatal("a pad reporting zero vid/pid must never be claimed")
	}
	if !ds.isOwnEmulatedPadLocked(0x045e, 0x028e) {
		t.Fatal("the emulated pad's own announcement must be claimed")
	}

	ds.selfPads[9] = padSignature(0x045e, 0x028e)
	if ds.isOwnEmulatedPadLocked(0x045e, 0x028e) {
		t.Fatal("with capacity exhausted, a same-model pad is real hardware")
	}
}

// A held device's emulated pad stays attached through the grace window.
func TestSelfPadGuardCountsHeldDevices(t *testing.T) {
	ds := newTestStore()
	held := &Device{Identity: "serial:TESTSERIAL01", EmulatedSignature: padSignature(0x045e, 0x028e)}
	ds.orphans[held.Identity] = &orphanedDevice{
		device: held,
		timer:  time.NewTimer(time.Hour),
	}

	if !ds.isOwnEmulatedPadLocked(0x045e, 0x028e) {
		t.Fatal("a held device's pad must still be claimable during the grace window")
	}
}

func newTestStore() *deviceStore {
	return &deviceStore{
		devices:        map[sdl.GamepadID]*Device{},
		orphans:        map[string]*orphanedDevice{},
		selfPads:       map[sdl.GamepadID]string{},
		reconnectGrace: time.Minute,
	}
}

func TestLogIdentityTrimsTheSerial(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"serial:45e:28e:FXB99616039E7", "serial:45e:28e:...39E7"},
		{"path:XInput#0", "path:XInput#0"},
		{"serial:45e:28e:ABC", "serial:45e:28e:ABC"}, // too short to be worth trimming
		{"", ""},
	}

	for _, tc := range cases {
		if got := logIdentity(tc.in); got != tc.want {
			t.Errorf("logIdentity(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestReclaimByIdentityFromOrphans(t *testing.T) {
	ds := newTestStore()
	held := &Device{Identity: "serial:TESTSERIAL01", SteamHandle: 42}
	ds.orphans["serial:TESTSERIAL01"] = &orphanedDevice{
		device: held,
		timer:  time.NewTimer(time.Hour),
	}

	if got := ds.reclaimByIdentityLocked("serial:OTHER"); got != nil {
		t.Fatal("a non-matching identity must not reclaim a held device")
	}
	if got := ds.reclaimByIdentityLocked(""); got != nil {
		t.Fatal("an empty identity must never match")
	}

	got := ds.reclaimByIdentityLocked("serial:TESTSERIAL01")
	if got != held {
		t.Fatal("matching identity did not reclaim the held device")
	}
	if len(ds.orphans) != 0 {
		t.Fatal("reclaiming must remove the device from the orphan set")
	}
	if again := ds.reclaimByIdentityLocked("serial:TESTSERIAL01"); again != nil {
		t.Fatal("a device must only be reclaimable once")
	}
}

// A virtual pad returning first leaves the device in ds.devices with
// RealGamepad nil; the controller must find it there rather than get a new one.
func TestReclaimByIdentityFindsDeviceReattachedByVirtualPad(t *testing.T) {
	ds := newTestStore()
	partial := &Device{Identity: "serial:TESTSERIAL01", SteamHandle: 42}
	ds.devices[7] = partial

	got := ds.reclaimByIdentityLocked("serial:TESTSERIAL01")
	if got != partial {
		t.Fatal("the returning controller did not find its partly reattached device")
	}
	if _, still := ds.devices[7]; !still {
		t.Fatal("an in-store device must stay in the store when reclaimed")
	}
}

// A device that already has its physical controller must never be matched, or
// a second same-serial pad would steal it.
func TestReclaimByIdentitySkipsFullyAttachedDevices(t *testing.T) {
	ds := newTestStore()
	whole := &Device{Identity: "serial:TESTSERIAL01", RealGamepad: &sdl.Gamepad{}}
	ds.devices[7] = whole

	if got := ds.reclaimByIdentityLocked("serial:TESTSERIAL01"); got != nil {
		t.Fatal("a device that still has its real gamepad must not be reclaimable")
	}
}

// A controller waking from sleep re-announces its Steam virtual pad before the
// physical device returns, so the held device has to be findable by handle.
func TestReclaimOrphanBySteamHandle(t *testing.T) {
	ds := newTestStore()
	held := &Device{Identity: "serial:TESTSERIAL01", SteamHandle: 8800112233445566}
	ds.orphans[held.Identity] = &orphanedDevice{
		device: held,
		timer:  time.NewTimer(time.Hour),
	}

	if got := ds.reclaimOrphanBySteamHandleLocked(0); got != nil {
		t.Fatal("a zero Steam handle must never match")
	}
	if got := ds.reclaimOrphanBySteamHandleLocked(1); got != nil {
		t.Fatal("a non-matching Steam handle must not reclaim a held device")
	}

	got := ds.reclaimOrphanBySteamHandleLocked(8800112233445566)
	if got != held {
		t.Fatal("matching Steam handle did not reclaim the held device")
	}
	if len(ds.orphans) != 0 {
		t.Fatal("reclaiming must remove the device from the orphan set")
	}
}

// Without a VIIPER device there is nothing worth holding, and without an
// identity a returning controller could never be matched back to it.
func TestHoldForReconnectRequiresDeviceAndIdentity(t *testing.T) {
	ds := newTestStore()

	if ds.holdForReconnectLocked(&Device{Identity: "serial:X"}) {
		t.Fatal("a device with no VIIPER device must not be held")
	}
	if len(ds.orphans) != 0 {
		t.Fatal("nothing should have been parked")
	}

	if ds.holdForReconnectLocked(&Device{}) {
		t.Fatal("a device with no identity must not be held")
	}
}

// A self-pad never enters ds.devices, so its selfPads entry has to be dropped
// on the removal event or the claimed count only grows.
func TestCloseGamePadClearsSelfPadEntry(t *testing.T) {
	ds := newTestStore()
	ds.selfPads[7] = padSignature(0x045e, 0x028e)

	if err := ds.CloseGamePad(7); err != nil {
		t.Fatalf("CloseGamePad returned %v", err)
	}
	if _, still := ds.selfPads[7]; still {
		t.Fatal("selfPads entry outlived the pad it described")
	}
}

func TestHoldForReconnectDisabledByZeroGrace(t *testing.T) {
	ds := newTestStore()
	ds.reconnectGrace = 0

	if ds.holdForReconnectLocked(&Device{Identity: "serial:X"}) {
		t.Fatal("a zero grace window must disable holding entirely")
	}
}

// newTestEmulatedDevice returns a device backed by a loopback listener, so
// teardown runs the real close path. The returned channel is closed when the
// device's removal callback runs.
func newTestEmulatedDevice(t *testing.T) (*viiperdevice.Device, <-chan struct{}) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		accepted <- conn
	}()

	stream, err := viiperclient.New(ln.Addr().String()).OpenStream(context.Background(), 1, "1")
	if err != nil {
		t.Fatalf("open device stream: %v", err)
	}
	select {
	case conn := <-accepted:
		t.Cleanup(func() { _ = conn.Close() })
	case <-time.After(5 * time.Second):
		t.Fatal("device stream was never accepted")
	}

	removed := make(chan struct{})
	vd := viiperdevice.New(stream, &viipertypes.Device{
		BusID: 1, DevID: "1", Vid: "0x045e", Pid: "0x028e", Type: "xbox360",
	}, func() error {
		close(removed)
		return nil
	})
	return vd, removed
}

func TestHeldDeviceIsReleasedWhenGraceExpires(t *testing.T) {
	ds := newTestStore()
	ds.reconnectGrace = 10 * time.Millisecond

	vd, removed := newTestEmulatedDevice(t)
	dev := &Device{Identity: "serial:45e:28e:TESTSERIAL01", ViiperDevice: vd}

	ds.mtx.Lock()
	dev.Lock()
	held := ds.holdForReconnectLocked(dev)
	dev.Unlock()
	ds.mtx.Unlock()
	if !held {
		t.Fatal("a device with an emulated device and an identity must be held")
	}

	select {
	case <-removed:
	case <-time.After(10 * time.Second):
		t.Fatal("the emulated device outlived the grace window")
	}

	ds.mtx.Lock()
	defer ds.mtx.Unlock()
	if len(ds.orphans) != 0 {
		t.Fatal("an expired hold must not stay in the orphan set")
	}
}

func TestStaleTimerDoesNotCloseReclaimedDevice(t *testing.T) {
	ds := newTestStore()
	ds.reconnectGrace = 20 * time.Millisecond

	vd, removed := newTestEmulatedDevice(t)
	dev := &Device{Identity: "serial:45e:28e:TESTSERIAL01", SteamHandle: 42, ViiperDevice: vd}

	ds.mtx.Lock()
	dev.Lock()
	ds.holdForReconnectLocked(dev)
	dev.Unlock()
	reclaimed := ds.reclaimByIdentityLocked(dev.Identity)
	ds.mtx.Unlock()

	if reclaimed != dev {
		t.Fatal("the held device was not reclaimed")
	}

	select {
	case <-removed:
		t.Fatal("a reclaimed device must survive the timer from its earlier hold")
	case <-time.After(200 * time.Millisecond):
	}
	if dev.ViiperDevice == nil {
		t.Fatal("the reclaimed device lost its emulated device")
	}
}

// A device reclaimed and held again gets a new timer; the first one must not
// tear down the hold the second one owns.
func TestTakeExpiredOrphanIgnoresSupersededTimer(t *testing.T) {
	ds := newTestStore()
	identity := "serial:45e:28e:TESTSERIAL01"
	dev := &Device{Identity: identity}

	first := &orphanedDevice{device: dev, timer: time.NewTimer(time.Hour)}
	second := &orphanedDevice{device: dev, timer: time.NewTimer(time.Hour)}
	ds.orphans[identity] = second

	if got := ds.takeExpiredOrphan(identity, first); got != nil {
		t.Fatal("a superseded timer must not release the current hold")
	}
	if _, still := ds.orphans[identity]; !still {
		t.Fatal("a superseded timer must leave the orphan set alone")
	}

	if got := ds.takeExpiredOrphan(identity, second); got != dev {
		t.Fatal("the current timer must release its own held device")
	}
	if len(ds.orphans) != 0 {
		t.Fatal("releasing must remove the device from the orphan set")
	}
	if got := ds.takeExpiredOrphan(identity, second); got != nil {
		t.Fatal("a device already reclaimed must not be released twice")
	}
}
