package battery

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// pad returns a 64-byte report starting with the given bytes, like the real device sends.
func pad(b ...byte) []byte {
	r := make([]byte, 64)
	copy(r, b)
	return r
}

func TestDecode(t *testing.T) {
	tests := []struct {
		name    string
		cmd     byte
		report  []byte
		want    Status
		wantErr error
	}{
		// Real replies captured with cmd/probe.
		{"dongle discharging 100", 0xD2, pad(0xD2, 0x15), Status{Level: 100}, nil},
		{"cable charging 100", 0x92, pad(0x92, 0x95), Status{Level: 100, Charging: true}, nil},
		{"dongle, mouse asleep", 0xD2, pad(0x40, 0xFF), Status{}, errUnrelated},
		// Formula checks.
		{"0 percent", 0xD2, pad(0xD2, 0x01), Status{Level: 0}, nil},
		{"50 percent charging", 0xD2, pad(0xD2, 0x8B), Status{Level: 50, Charging: true}, nil},
		{"byte 0 is invalid", 0xD2, pad(0xD2, 0x00), Status{}, ErrInvalid},
		{"above 100 is invalid", 0xD2, pad(0xD2, 0x16), Status{}, ErrInvalid},
		{"reply to other command", 0x92, pad(0xD2, 0x15), Status{}, errUnrelated},
		{"too short", 0xD2, []byte{0xD2}, Status{}, errUnrelated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Decode(tt.cmd, tt.report)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("status = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestDeviceCommand(t *testing.T) {
	if c := (Device{Wired: false}).Command(); c != 0xD2 {
		t.Errorf("dongle command = 0x%02x, want 0xd2", c)
	}
	if c := (Device{Wired: true}).Command(); c != 0x92 {
		t.Errorf("cable command = 0x%02x, want 0x92", c)
	}
}

// fakeDevice returns our end of a SOCK_SEQPACKET pair (one message per read,
// like hidraw) and the peer end that plays the mouse.
func fakeDevice(t *testing.T) (ours, peer int) {
	t.Helper()
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unix.Close(fds[0]); unix.Close(fds[1]) })
	return fds[0], fds[1]
}

// answer waits for the command on peer, checks it and sends the replies.
func answer(t *testing.T, peer int, wantCmd byte, replies ...[]byte) <-chan error {
	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 64)
		n, err := unix.Read(peer, buf)
		if err != nil {
			done <- err
			return
		}
		if n != 2 || buf[0] != 0x00 || buf[1] != wantCmd {
			done <- fmt.Errorf("device got % x, want 00 %02x", buf[:n], wantCmd)
			return
		}
		for _, r := range replies {
			if _, err := unix.Write(peer, r); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	return done
}

func TestExchange(t *testing.T) {
	const timeout = 100 * time.Millisecond
	tests := []struct {
		name    string
		stale   [][]byte // queued before the command is written
		replies [][]byte
		want    Status
		wantErr error
	}{
		{"awake", nil, [][]byte{pad(0xD2, 0x15)}, Status{Level: 100}, nil},
		{"asleep", nil, [][]byte{pad(0x40, 0xFF)}, Status{}, ErrAsleep},
		{"no reply", nil, nil, Status{}, ErrNoReply},
		{"unrelated report first", nil, [][]byte{pad(0x40, 0xFF), pad(0xD2, 0x0B)}, Status{Level: 50}, nil},
		{"stale reply is dropped", [][]byte{pad(0xD2, 0x0B)}, [][]byte{pad(0xD2, 0x15)}, Status{Level: 100}, nil},
		{"invalid value", nil, [][]byte{pad(0xD2, 0x7F)}, Status{}, ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ours, peer := fakeDevice(t)
			for _, s := range tt.stale {
				if _, err := unix.Write(peer, s); err != nil {
					t.Fatal(err)
				}
			}
			done := answer(t, peer, 0xD2, tt.replies...)
			got, _, err := exchange(ours, 0xD2, timeout)
			if derr := <-done; derr != nil {
				t.Fatalf("fake device: %v", derr)
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("status = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// addHidraw creates a fake /sys entry: class/hidraw/<node>/device symlinks to
// a HID device directory whose parent is the USB interface.
func addHidraw(t *testing.T, root, node string, vid, pid uint16, iface int) {
	t.Helper()
	ifaceDir := filepath.Join(root, "devices", node, fmt.Sprintf("1-2:1.%d", iface))
	hidDir := filepath.Join(ifaceDir, fmt.Sprintf("0003:%04X:%04X.0001", vid, pid))
	must(t, os.MkdirAll(hidDir, 0o755))
	must(t, os.WriteFile(filepath.Join(ifaceDir, "bInterfaceNumber"), []byte(fmt.Sprintf("%02x\n", iface)), 0o644))
	uevent := fmt.Sprintf("DRIVER=hid-generic\nHID_ID=0003:%08X:%08X\nHID_NAME=Test\n", vid, pid)
	must(t, os.WriteFile(filepath.Join(hidDir, "uevent"), []byte(uevent), 0o644))
	classDir := filepath.Join(root, "class", "hidraw", node)
	must(t, os.MkdirAll(classDir, 0o755))
	must(t, os.Symlink(hidDir, filepath.Join(classDir, "device")))
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestFindDevices(t *testing.T) {
	root := t.TempDir()
	// Dongle exposes interfaces 0-4 (as seen on real hardware); only 3 counts.
	for i, node := range []string{"hidraw0", "hidraw1", "hidraw3", "hidraw5", "hidraw15"} {
		addHidraw(t, root, node, 0x1038, 0x1852, i)
	}
	addHidraw(t, root, "hidraw7", 0x1038, 0x1854, 3) // cable
	addHidraw(t, root, "hidraw8", 0x1038, 0x1234, 3) // other SteelSeries device
	addHidraw(t, root, "hidraw9", 0x046D, 0x1852, 3) // other vendor

	devs, err := FindDevices(filepath.Join(root, "class", "hidraw"))
	if err != nil {
		t.Fatal(err)
	}
	want := []Device{
		{Node: "/dev/hidraw7", Product: 0x1854, Name: "Aerox 5 Wireless (cable)", Wired: true},
		{Node: "/dev/hidraw5", Product: 0x1852, Name: "Aerox 5 Wireless (dongle)", Wired: false},
	}
	if len(devs) != len(want) {
		t.Fatalf("got %+v, want %+v", devs, want)
	}
	for i := range want {
		if devs[i] != want[i] {
			t.Errorf("devs[%d] = %+v, want %+v", i, devs[i], want[i])
		}
	}
}

func TestFindDevicesMissingRoot(t *testing.T) {
	devs, err := FindDevices(filepath.Join(t.TempDir(), "nope"))
	if err != nil || len(devs) != 0 {
		t.Fatalf("got %v, %v; want no devices and no error", devs, err)
	}
}
