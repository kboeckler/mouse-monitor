// Package battery reads the battery state of a SteelSeries Aerox 5 Wireless
// directly via Linux hidraw, without rivalcfg.
//
// Protocol (taken from rivalcfg, verified on real hardware):
//   - The mouse is the hidraw node on USB interface 3, either via the 2.4 GHz
//     dongle or via cable.
//   - Write the report ID 0x00 followed by the command: 0xD2 via dongle,
//     0x92 via cable.
//   - The reply echoes the command in byte 0. Byte 1 holds the battery:
//     bit 0x80 = charging, level = ((b & 0x7F) - 1) * 5, i.e. 5 % steps.
//   - If the mouse is asleep or out of range, the dongle answers by itself
//     with a report that does not echo the command (seen: 40 ff 00 ...).
package battery

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const (
	// SysfsRoot is where the kernel lists hidraw devices.
	SysfsRoot = "/sys/class/hidraw"
	// DefaultTimeout is how long Read waits for the reply.
	DefaultTimeout = 500 * time.Millisecond

	vendorID       = 0x1038
	batteryCommand = 0x92
	wirelessFlag   = 0x40
	chargingFlag   = 0x80
	batteryIface   = 3
)

var (
	// ErrNotFound means no Aerox 5 Wireless (dongle or cable) is connected.
	ErrNotFound = errors.New("no Aerox 5 Wireless found")
	// ErrAsleep means the dongle answered, but the mouse did not: it is
	// asleep, switched off or out of range.
	ErrAsleep = errors.New("mouse asleep, off or out of range")
	// ErrNoReply means nothing came back at all within the timeout.
	ErrNoReply = errors.New("no reply from device")
	// ErrInvalid means the reply matched but held an impossible value.
	ErrInvalid = errors.New("invalid battery value")

	// errUnrelated marks a report that is not the answer to our command.
	errUnrelated = errors.New("unrelated report")
)

// Product IDs from rivalcfg's aerox5_wireless_* device profiles.
var products = map[uint16]struct {
	name  string
	wired bool
}{
	0x1852: {"Aerox 5 Wireless (dongle)", false},
	0x185C: {"Aerox 5 Wireless Destiny 2 (dongle)", false},
	0x1860: {"Aerox 5 Wireless Diablo IV (dongle)", false},
	0x1854: {"Aerox 5 Wireless (cable)", true},
	0x185E: {"Aerox 5 Wireless Destiny 2 (cable)", true},
	0x1862: {"Aerox 5 Wireless Diablo IV (cable)", true},
}

// Status is one battery reading.
type Status struct {
	Level    int  // 0..100 in steps of 5
	Charging bool
}

// Device is a hidraw node that can answer the battery command.
type Device struct {
	Node    string // e.g. /dev/hidraw5
	Product uint16
	Name    string
	Wired   bool
}

// Command returns the battery command byte for this connection type.
func (d Device) Command() byte {
	if d.Wired {
		return batteryCommand
	}
	return batteryCommand | wirelessFlag
}

// Read finds the mouse and returns its battery state. A cable connection is
// asked first because it always knows the charging state; the dongle is the
// fallback. If nothing answers properly, ErrAsleep wins over other errors so
// the caller can show "asleep" instead of an error.
func Read(timeout time.Duration) (Status, error) {
	devs, err := FindDevices(SysfsRoot)
	if err != nil {
		return Status{}, err
	}
	if len(devs) == 0 {
		return Status{}, ErrNotFound
	}
	var firstErr error
	asleep := false
	for _, d := range devs {
		st, _, err := Query(d, timeout)
		if err == nil {
			return st, nil
		}
		if errors.Is(err, ErrAsleep) {
			asleep = true
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if asleep {
		return Status{}, ErrAsleep
	}
	return Status{}, firstErr
}

// FindDevices lists all Aerox 5 Wireless hidraw nodes on the battery
// interface below sysfsRoot (normally SysfsRoot), cable connections first.
func FindDevices(sysfsRoot string) ([]Device, error) {
	entries, err := os.ReadDir(sysfsRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var devs []Device
	for _, e := range entries {
		real, err := filepath.EvalSymlinks(filepath.Join(sysfsRoot, e.Name(), "device"))
		if err != nil {
			continue
		}
		vendor, product, ok := readHIDID(filepath.Join(real, "uevent"))
		if !ok || vendor != vendorID {
			continue
		}
		p, known := products[product]
		if !known || readIface(filepath.Join(real, "..", "bInterfaceNumber")) != batteryIface {
			continue
		}
		devs = append(devs, Device{Node: "/dev/" + e.Name(), Product: product, Name: p.name, Wired: p.wired})
	}
	sort.Slice(devs, func(i, j int) bool {
		if devs[i].Wired != devs[j].Wired {
			return devs[i].Wired
		}
		return devs[i].Node < devs[j].Node
	})
	return devs, nil
}

// Query asks one device for its battery state. It also returns the raw
// matching reply (or the last unrelated report) for debugging.
func Query(d Device, timeout time.Duration) (Status, []byte, error) {
	f, err := os.OpenFile(d.Node, os.O_RDWR, 0)
	if err != nil {
		return Status{}, nil, fmt.Errorf("open %s: %w", d.Node, err)
	}
	defer f.Close()
	return exchange(int(f.Fd()), d.Command(), timeout)
}

// exchange writes the command to fd and waits for the matching reply.
// fd must deliver one report per read, like hidraw does.
func exchange(fd int, cmd byte, timeout time.Duration) (Status, []byte, error) {
	// Drop reports queued before our request so they can't be taken as the reply.
	for range readReports(fd, 0) {
	}

	if _, err := unix.Write(fd, []byte{0x00, cmd}); err != nil {
		return Status{}, nil, fmt.Errorf("write command 0x%02x: %w", cmd, err)
	}

	var last []byte
	for r := range readReports(fd, timeout) {
		st, err := Decode(cmd, r)
		if errors.Is(err, errUnrelated) {
			last = r
			continue
		}
		return st, r, err
	}
	if last != nil {
		return Status{}, last, ErrAsleep
	}
	return Status{}, nil, ErrNoReply
}

// Decode interprets one report as the answer to cmd.
func Decode(cmd byte, report []byte) (Status, error) {
	if len(report) < 2 || report[0] != cmd {
		return Status{}, errUnrelated
	}
	b := report[1]
	st := Status{
		Level:    (int(b&^chargingFlag) - 1) * 5,
		Charging: b&chargingFlag != 0,
	}
	if st.Level < 0 || st.Level > 100 {
		return Status{}, fmt.Errorf("%w: byte 0x%02x", ErrInvalid, b)
	}
	return st, nil
}

// readReports yields every report that arrives on fd until timeout passes.
// With timeout 0 it only yields what is already queued.
func readReports(fd int, timeout time.Duration) func(func([]byte) bool) {
	return func(yield func([]byte) bool) {
		deadline := time.Now().Add(timeout)
		buf := make([]byte, 256)
		for {
			wait := 0
			if left := time.Until(deadline); left > 0 {
				wait = int(left.Milliseconds()) + 1
			}
			fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
			n, err := unix.Poll(fds, wait)
			if err == unix.EINTR {
				continue
			}
			if err != nil || n == 0 || fds[0].Revents&unix.POLLIN == 0 {
				return
			}
			m, err := unix.Read(fd, buf)
			if err != nil || m == 0 {
				return
			}
			if !yield(append([]byte(nil), buf[:m]...)) {
				return
			}
		}
	}
}

// readHIDID parses HID_ID=0003:00001038:00001852 from a uevent file.
func readHIDID(path string) (vendor, product uint16, ok bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		v, found := strings.CutPrefix(line, "HID_ID=")
		if !found {
			continue
		}
		parts := strings.Split(v, ":")
		if len(parts) != 3 {
			return 0, 0, false
		}
		vid, err1 := strconv.ParseUint(parts[1], 16, 16)
		pid, err2 := strconv.ParseUint(parts[2], 16, 16)
		if err1 != nil || err2 != nil {
			return 0, 0, false
		}
		return uint16(vid), uint16(pid), true
	}
	return 0, 0, false
}

// readIface reads a USB bInterfaceNumber file (hex, e.g. "03"); -1 if unknown.
func readIface(path string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return -1
	}
	n, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 16, 8)
	if err != nil {
		return -1
	}
	return int(n)
}
