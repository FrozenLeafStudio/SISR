package handler

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Alia5/SISR/cmd"
	"github.com/Alia5/SISR/input"
	"github.com/Alia5/SISR/input/viiperdevice"
	"github.com/Alia5/SISR/sdl"
)

const (
	kbmCreateKeyboardVirtualID sdl.GamepadID = -10001
	kbmCreateMouseVirtualID    sdl.GamepadID = -10002
)

type KBMDevices struct {
	KeyboardDev *viiperdevice.Device
	MouseDev    *viiperdevice.Device

	mtx sync.RWMutex
}

func (d *KBMDevices) Lock() {
	d.mtx.Lock()
}

func (d *KBMDevices) Unlock() {
	d.mtx.Unlock()
}

func CreateViiperDevice(ctx context.Context, c *cmd.SISRContext, gpID sdl.GamepadID, dev *input.Device) {
	if !c.ViiperBridge.Ready() {
		return
	}
	if c.ViiperBridge.IsCreateDeviceScheduled(gpID) {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, createTimeout)
	deviceChan, errChan := c.ViiperBridge.CreateDevice(ctx, gpID, c.Config.DefaultControllerType)
	go func() {
		select {
		case vd := <-deviceChan:
			info := vd.Info()
			c.DeviceStore.RegisterEmulatedPad(dev, info.Vid, info.Pid)
			dev.Lock()
			dev.SetViiperDevice(vd)
			dev.Unlock()
			slog.Info("VIIPER device created and assigned to gamepad", "gamepad_id", gpID, "viiper_device", vd.Info())
		case err := <-errChan:
			slog.Error("Failed to create VIIPER device", "error", err)
			// best attempt... otherwise user will have handle to via UI
			// kiss
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			c.ViiperBridge.Ping(ctx) // nolint
		case <-ctx.Done():
			slog.Error("Timed out creating VIIPER device")
		}
		cancel()
	}()
}

func CreateViiperKBMDevice(ctx context.Context, c *cmd.SISRContext, deviceType string, devices *KBMDevices) {
	if !c.ViiperBridge.Ready() {
		return
	}

	var virtualID sdl.GamepadID
	switch deviceType {
	case string(viiperdevice.DeviceTypeKeyboard):
		virtualID = kbmCreateKeyboardVirtualID
	case string(viiperdevice.DeviceTypeMouse):
		virtualID = kbmCreateMouseVirtualID
	default:
		return
	}

	if c.ViiperBridge.IsCreateDeviceScheduled(virtualID) {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, createTimeout)
	deviceChan, errChan := c.ViiperBridge.CreateDevice(ctx, virtualID, deviceType)
	go func() {
		select {
		case vd := <-deviceChan:
			switch deviceType {
			case string(viiperdevice.DeviceTypeKeyboard):
				devices.Lock()
				devices.KeyboardDev = vd
				devices.Unlock()
			case string(viiperdevice.DeviceTypeMouse):
				devices.Lock()
				devices.MouseDev = vd
				devices.Unlock()
			}
			slog.Info("VIIPER KBM device created", "device_type", deviceType, "viiper_device", vd.Info())
		case err := <-errChan:
			slog.Error("Failed to create VIIPER KBM device", "error", err, "device_type", deviceType)
		case <-ctx.Done():
			slog.Error("Timed out creating VIIPER KBM device", "device_type", deviceType)
		}
		cancel()
	}()
}
