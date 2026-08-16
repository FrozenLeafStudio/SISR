package input

import (
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Alia5/SISR/sdl"
)

// DefaultReconnectGrace is how long an emulated device is kept alive after
// its source controller disappears.
const DefaultReconnectGrace = 15 * time.Second

type DeviceStore interface {
	OpenGamePad(id sdl.GamepadID) (*Device, error)
	CloseGamePad(id sdl.GamepadID) error
	DeviceForID(id sdl.GamepadID) (*Device, bool)
	RegisterEmulatedPad(dev *Device, vid, pid string)
	Empty() bool
	Devices() []*Device
}

// orphanedDevice is a device held open in case its controller comes back.
type orphanedDevice struct {
	device *Device
	timer  *time.Timer
}

type deviceStore struct {
	devices         map[sdl.GamepadID]*Device
	deviceIdxOrder  []sdl.GamepadID
	noSteamMode     bool
	gyroPassthrough bool

	// Emulated devices held open across a controller dropout, keyed by the
	// identity of the controller that owned them.
	orphans        map[string]*orphanedDevice
	reconnectGrace time.Duration

	// Vid:pid signatures of pads recognized as SISR's own emulated output,
	// keyed by gamepad id.
	selfPads map[sdl.GamepadID]string

	mtx sync.Mutex
}

func NewDeviceStore(noSteamMode bool, gyroPassthrough bool) (DeviceStore, func(), error) {

	opener := &deviceStore{
		devices:         make(map[sdl.GamepadID]*Device),
		deviceIdxOrder:  make([]sdl.GamepadID, 0),
		noSteamMode:     noSteamMode,
		gyroPassthrough: gyroPassthrough,
		orphans:         make(map[string]*orphanedDevice),
		selfPads:        make(map[sdl.GamepadID]string),
		reconnectGrace:  DefaultReconnectGrace,
	}
	return opener, opener.quit, nil
}

// gamepadIdentity returns a value that survives a disconnect/reconnect cycle,
// unlike the SDL gamepad id. Serials are qualified by vid:pid because
// inexpensive controllers often report the same serial for every unit.
func gamepadIdentity(gp *sdl.Gamepad) string {
	if gp == nil {
		return ""
	}
	if serial := gp.Serial(); serial != "" {
		return "serial:" + padSignature(gp.Vendor(), gp.Product()) + ":" + serial
	}
	if path := gp.Path(); path != "" {
		return "path:" + path
	}
	return ""
}

// logIdentity trims the serial out of an identity, keeping enough to correlate
// lines from the same controller. Logs get pasted into bug reports, and the
// serial is the only part of an identity a user would not want published.
func logIdentity(identity string) string {
	rest, ok := strings.CutPrefix(identity, "serial:")
	if !ok {
		return identity
	}
	sep := strings.LastIndex(rest, ":")
	if sep < 0 || len(rest)-sep-1 <= 4 {
		return identity
	}
	return "serial:" + rest[:sep+1] + "..." + rest[len(rest)-4:]
}

// parseHexID reads the "0x045e" form VIIPER reports device ids in.
func parseHexID(s string) uint16 {
	v, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(s), "0x"), 16, 16)
	if err != nil {
		return 0
	}
	return uint16(v)
}

func padSignature(vendor, product uint16) string {
	return strconv.FormatUint(uint64(vendor), 16) + ":" + strconv.FormatUint(uint64(product), 16)
}

// isOwnEmulatedPadLocked reports whether a pad with this vid/pid is one SISR
// emulated rather than real hardware. Only as many pads are claimed as there
// are live emulated devices with that signature, so a genuine controller of
// the same model is still usable alongside one. Callers must hold ds.mtx.
func (ds *deviceStore) isOwnEmulatedPadLocked(vendor, product uint16) bool {
	if vendor == 0 && product == 0 {
		return false
	}
	sig := padSignature(vendor, product)

	created := 0
	for _, dev := range ds.uniqueDevicesLocked() {
		if dev.EmulatedSignature == sig {
			created++
		}
	}
	for _, orphan := range ds.orphans {
		if orphan.device != nil && orphan.device.EmulatedSignature == sig {
			created++
		}
	}
	if created == 0 {
		return false
	}

	claimed := 0
	for _, claimedSig := range ds.selfPads {
		if claimedSig == sig {
			claimed++
		}
	}
	return claimed < created
}

// claimSelfPadLocked records and closes a pad recognized as SISR's own
// emulated output. Callers must hold ds.mtx.
func (ds *deviceStore) claimSelfPadLocked(id sdl.GamepadID, gp *sdl.Gamepad, vendor, product uint16) {
	ds.selfPads[id] = padSignature(vendor, product)
	slog.Info("Ignoring SISR's own emulated pad",
		"id", id, "name", gp.Name(), "vendor", vendor, "product", product)
	gp.Close()
}

// uniqueDevicesLocked returns each device once. Callers must hold ds.mtx.
func (ds *deviceStore) uniqueDevicesLocked() []*Device {
	devices := make([]*Device, 0, len(ds.devices))
	for _, dev := range ds.devices {
		if dev != nil && !slices.Contains(devices, dev) {
			devices = append(devices, dev)
		}
	}
	return devices
}

func (ds *deviceStore) OpenGamePad(id sdl.GamepadID) (*Device, error) {
	defer func() {
		ds.mtx.Lock()
		defer ds.mtx.Unlock()
		if slices.Contains(ds.deviceIdxOrder, id) {
			return
		}
		ds.deviceIdxOrder = append(ds.deviceIdxOrder, id)
	}()
	ds.mtx.Lock()
	defer ds.mtx.Unlock()

	if dev, exists := ds.devices[id]; exists {
		return dev, nil
	}

	var dev *Device
	gp, err := sdl.OpenGamepad(id)
	if err != nil {
		return nil, err
	}
	steamHandle := gp.GetSteamHandle()
	serial := gp.Serial()
	path := gp.Path()
	gpType := gp.Type()
	realType := gp.RealType()

	slog.Debug("Opened Gamepad",
		"id", id,
		"name", gp.Name(),
		"steamHandle", steamHandle,
		"serial", serial,
		"path", path,
		"type", gpType.Name(),
		"realType", realType.Name(),
	)

	if ds.noSteamMode {
		dev = &Device{
			RealGamepad:         gp,
			SteamVirtualGamepad: gp,
		}

		if ds.gyroPassthrough {
			enableSensors(gp, id)
		}

		ds.devices[id] = dev
		return dev, nil
	}

	identity := gamepadIdentity(gp)
	vendor, product := gp.Vendor(), gp.Product()

	if steamHandle != 0 {
		// A waking controller usually re-announces its virtual pad before the
		// physical device returns, so match the held device by handle first.
		if orphan := ds.reclaimOrphanBySteamHandleLocked(steamHandle); orphan != nil {
			dev = orphan
			dev.Lock()
			defer dev.Unlock()
			dev.SteamVirtualGamepad = gp
			// An XInput-only controller is its own real gamepad. A split
			// pairing's virtual pad reports a different identity, so this
			// cannot match there.
			if dev.RealGamepad == nil && dev.Identity == identity {
				dev.RealGamepad = gp
			}
			ds.devices[id] = dev
			slog.Info("Reattached Steam Virtual Gamepad to its held emulated device",
				"id", id, "steamhandle", steamHandle, "identity", logIdentity(dev.Identity))
			return dev, nil
		}

		// Steam Input announces a captured emulated pad with a handle of its
		// own, so the guard runs on this branch too.
		if ds.isOwnEmulatedPadLocked(vendor, product) {
			ds.claimSelfPadLocked(id, gp, vendor, product)
			return nil, nil
		}

		dev = ds.findUnpairedRealLocked()
		if dev == nil {
			if gpType != sdl.GamepadTypeXbox360 && gpType != sdl.GamepadTypeXboxOne {
				// Non-xbox controllers are enumerated separately over HID, so
				// with no partner present this pad cannot be paired.
				slog.Warn("Steam Virtual Gamepad has no real gamepad to pair with; ignoring",
					"id", id, "name", gp.Name(), "type", gpType.Name(), "steamhandle", steamHandle)
				gp.Close()
				return nil, ErrVirtualWithoutRealGamepad
			}
			// xbox controllers on windows are not detected twice
			// Hid-Path + (Steam Virtual via XInput), but only via XInput
			// Thus, we assign both real and steam virtual pad from the same sdl gamepad
			slog.Debug("No unpaired real gamepad; treating as XInput-only controller")
			dev = &Device{
				RealGamepad: gp,
				Identity:    identity,
			}
		}
		dev.Lock()
		defer dev.Unlock()

		if dev.SteamVirtualGamepad != nil {
			gp.Close()
			return nil, ErrVirtualAlreadyAssigned
		}
		dev.SteamVirtualGamepad = gp
		dev.SteamHandle = steamHandle
		slog.Info("Found Steam Virtual Gamepad",
			"id", id,
			"name", gp.Name(),
			"steamhandle", steamHandle,
			"paired with", dev.RealGamepad.ID(),
			"paired with name", dev.RealGamepad.Name(),
		)
	} else {
		if ds.isOwnEmulatedPadLocked(vendor, product) {
			ds.claimSelfPadLocked(id, gp, vendor, product)
			return nil, nil
		}

		// Observed as a Steam virtual pad announced before Steam reports its
		// handle; adopting it as real hardware pairs it with itself.
		if strings.HasPrefix(path, "XInput#") {
			slog.Debug("Ignoring XInput device with no Steam correlation",
				"id", id, "name", gp.Name(), "path", path)
			gp.Close()
			return nil, nil
		}

		if reclaimed := ds.reclaimByIdentityLocked(identity); reclaimed != nil {
			dev = reclaimed
			dev.Lock()
			dev.RealGamepad = gp
			dev.Unlock()
			slog.Info("Reattached reconnected controller to its held emulated device",
				"id", id, "name", gp.Name(), "identity", logIdentity(identity))
		} else {
			dev = &Device{RealGamepad: gp, Identity: identity}
			slog.Debug("Opened real gamepad", "id", id, "name", gp.Name())
		}
		if ds.gyroPassthrough {
			enableSensors(gp, id)
		}
	}
	ds.devices[id] = dev

	return dev, nil
}

// findUnpairedRealLocked returns the most recently opened device that has a real
// controller but no Steam virtual pad yet. Callers must hold ds.mtx.
func (ds *deviceStore) findUnpairedRealLocked() *Device {
	for _, devID := range slices.Backward(ds.deviceIdxOrder) {
		candidate, exists := ds.devices[devID]
		if !exists || candidate == nil {
			continue
		}
		if candidate.RealGamepad != nil && candidate.SteamVirtualGamepad == nil {
			return candidate
		}
	}
	return nil
}

// reclaimByIdentityLocked returns the device for identity, held in the orphan
// set or already in the store with only its virtual pad attached. Callers must
// hold ds.mtx.
func (ds *deviceStore) reclaimByIdentityLocked(identity string) *Device {
	if identity == "" {
		return nil
	}
	if orphan, ok := ds.orphans[identity]; ok {
		orphan.timer.Stop()
		delete(ds.orphans, identity)
		return orphan.device
	}
	for _, dev := range ds.uniqueDevicesLocked() {
		if dev.Identity == identity && dev.RealGamepad == nil {
			return dev
		}
	}
	return nil
}

// reclaimOrphanBySteamHandleLocked finds a held device by the Steam handle of
// the virtual pad it was paired with. Callers must hold ds.mtx.
func (ds *deviceStore) reclaimOrphanBySteamHandleLocked(steamHandle uint64) *Device {
	if steamHandle == 0 {
		return nil
	}
	for identity, orphan := range ds.orphans {
		if orphan.device != nil && orphan.device.SteamHandle == steamHandle {
			orphan.timer.Stop()
			delete(ds.orphans, identity)
			return orphan.device
		}
	}
	return nil
}

// takeExpiredOrphan removes and returns the held device orphan describes, or
// nil if the device was reclaimed or held again since orphan's timer started.
func (ds *deviceStore) takeExpiredOrphan(identity string, orphan *orphanedDevice) *Device {
	ds.mtx.Lock()
	defer ds.mtx.Unlock()

	current, ok := ds.orphans[identity]
	if !ok || current != orphan {
		return nil
	}
	delete(ds.orphans, identity)
	return current.device
}

// holdForReconnectLocked keeps an emulated device alive after its controller
// disappeared. Callers must hold ds.mtx and the device lock.
func (ds *deviceStore) holdForReconnectLocked(dev *Device) bool {
	if dev.ViiperDevice == nil || dev.Identity == "" || ds.reconnectGrace <= 0 {
		return false
	}
	identity := dev.Identity
	if existing, ok := ds.orphans[identity]; ok {
		existing.timer.Stop()
		delete(ds.orphans, identity)
		if existing.device != dev {
			existing.device.Lock()
			existing.device.Close()
			existing.device.Unlock()
		}
	}

	orphan := &orphanedDevice{device: dev}
	orphan.timer = time.AfterFunc(ds.reconnectGrace, func() {
		expired := ds.takeExpiredOrphan(identity, orphan)
		if expired == nil {
			return
		}
		slog.Info("Controller did not return within the reconnect window; releasing emulated device",
			"identity", logIdentity(identity))
		expired.Lock()
		expired.Close()
		expired.Unlock()
	})
	ds.orphans[identity] = orphan

	slog.Info("Controller disappeared; holding its emulated device for reconnect",
		"identity", logIdentity(identity), "grace", ds.reconnectGrace)
	return true
}

func (ds *deviceStore) CloseGamePad(id sdl.GamepadID) error {
	ds.mtx.Lock()
	defer ds.mtx.Unlock()

	// Self-pads are never stored in ds.devices, so this has to happen before
	// the early return below.
	delete(ds.selfPads, id)

	dev, ok := ds.devices[id]
	if !ok {
		return nil
	}
	dev.Lock()
	defer dev.Unlock()

	if dev.RealGamepad != nil && dev.SteamVirtualGamepad != nil && dev.RealGamepad.ID() == id && dev.SteamVirtualGamepad.ID() == id {
		slog.Info(
			"Closing gamepad",
			"id", id,
			"name", dev.RealGamepad.Name(),
			"type", "both (real + steam virtual; likely Xbox controller)",
			"steamHandle", dev.SteamVirtualGamepad.GetSteamHandle(),
		)
		dev.RealGamepad.Close()
		dev.RealGamepad = nil
		dev.SteamVirtualGamepad = nil
	} else {
		if dev.RealGamepad != nil && dev.RealGamepad.ID() == id {
			slog.Info(
				"Closing gamepad",
				"id", id,
				"name", dev.RealGamepad.Name(),
				"type", "real",
			)
			dev.RealGamepad.Close()
			dev.RealGamepad = nil
		}
		if dev.SteamVirtualGamepad != nil && dev.SteamVirtualGamepad.ID() == id {
			slog.Info(
				"Closing gamepad",
				"id", id,
				"name", dev.SteamVirtualGamepad.Name(),
				"type", "steam_virtual",
				"steamHandle", dev.SteamVirtualGamepad.GetSteamHandle(),
			)
			dev.SteamVirtualGamepad.Close()
			dev.SteamVirtualGamepad = nil
		}
	}

	delete(ds.devices, id)
	if dev.RealGamepad == nil && dev.SteamVirtualGamepad == nil {
		if ds.holdForReconnectLocked(dev) {
			return nil
		}
		slog.Info("Device has no more gamepads, cleaning up...", "id", id)
		dev.Close()
	}
	return nil
}

func (ds *deviceStore) DeviceForID(id sdl.GamepadID) (*Device, bool) {
	ds.mtx.Lock()
	defer ds.mtx.Unlock()

	dev, ok := ds.devices[id]
	return dev, ok
}

// RegisterEmulatedPad records the vid/pid of the emulated device created for
// dev, so the store recognizes that pad when the OS enumerates it. It must be
// called before the device is announced, i.e. before it is assigned to dev.
func (ds *deviceStore) RegisterEmulatedPad(dev *Device, vid, pid string) {
	ds.mtx.Lock()
	defer ds.mtx.Unlock()

	dev.EmulatedSignature = padSignature(parseHexID(vid), parseHexID(pid))
	slog.Debug("Registered emulated pad signature", "signature", dev.EmulatedSignature)
}

func (ds *deviceStore) Devices() []*Device {
	ds.mtx.Lock()
	defer ds.mtx.Unlock()

	return ds.uniqueDevicesLocked()
}

func (ds *deviceStore) quit() {
	// The grace timer callback mutates ds.orphans from its own goroutine.
	ds.mtx.Lock()
	defer ds.mtx.Unlock()

	for identity, orphan := range ds.orphans {
		orphan.timer.Stop()
		if orphan.device != nil {
			orphan.device.Lock()
			orphan.device.Close()
			orphan.device.Unlock()
		}
		delete(ds.orphans, identity)
	}
	for id, dev := range ds.devices {
		if dev != nil {
			dev.Lock()
			dev.Close()
			dev.Unlock()
		}
		delete(ds.devices, id)
	}
	clear(ds.selfPads)
}

func (ds *deviceStore) Empty() bool {
	ds.mtx.Lock()
	defer ds.mtx.Unlock()
	return len(ds.devices) == 0
}

func enableSensors(gp *sdl.Gamepad, id sdl.GamepadID) {
	hasGyro := gp.HasSensor(sdl.SensorTypeGyroscope)
	if hasGyro {
		enabled := gp.SetSensorEnabled(sdl.SensorTypeGyroscope, true)
		slog.Debug("Set gamepad gyro sensor enabled", "id", id, "enabled", enabled)
	}

	hasAccel := gp.HasSensor(sdl.SensorTypeAccelerometer)
	if hasAccel {
		enabled := gp.SetSensorEnabled(sdl.SensorTypeAccelerometer, true)
		slog.Debug("Set gamepad accelerometer sensor enabled", "id", id, "enabled", enabled)
	}
}
