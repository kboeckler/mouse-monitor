// Command probe talks to a SteelSeries Aerox 5 Wireless directly via hidraw,
// without rivalcfg, and prints the raw battery reply next to the decoded
// values. It exists to verify the protocol on real hardware before the main
// app switches away from rivalcfg.
//
// Compare its output with:
//
//	RIVALCFG_DEBUG_PRINT_HID_REPORT=1 rivalcfg --battery-level
package main

import (
	"encoding/hex"
	"errors"
	"flag"
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
	vendorID        = 0x1038
	batteryCommand  = 0x92
	wirelessFlag    = 0x40
	chargingFlag    = 0x80
	batteryIface    = 3
	sysfsHidrawRoot = "/sys/class/hidraw"
)

// Product IDs taken from rivalcfg's aerox5_wireless_* device profiles.
var products = map[uint16]struct {
	name     string
	wireless bool
}{
	0x1852: {"Aerox 5 Wireless (dongle)", true},
	0x185C: {"Aerox 5 Wireless Destiny 2 (dongle)", true},
	0x1860: {"Aerox 5 Wireless Diablo IV (dongle)", true},
	0x1854: {"Aerox 5 Wireless (wired)", false},
	0x185E: {"Aerox 5 Wireless Destiny 2 (wired)", false},
	0x1862: {"Aerox 5 Wireless Diablo IV (wired)", false},
}

type hidrawDev struct {
	node      string // e.g. /dev/hidraw4
	vendor    uint16
	product   uint16
	iface     int // -1 if unknown
	name      string
	sysfsPath string
}

func main() {
	list := flag.Bool("list", false, "list all SteelSeries hidraw devices and exit")
	devFlag := flag.String("dev", "", "query this /dev/hidrawN instead of auto-detecting")
	cmdFlag := flag.String("cmd", "", "override the command byte (hex, e.g. 0xd2)")
	window := flag.Duration("window", time.Second, "how long to listen for reports after writing")
	flag.Parse()

	devs, err := findDevices()
	if err != nil {
		fail("scanning %s: %v", sysfsHidrawRoot, err)
	}

	if *list {
		printList(devs)
		return
	}

	var targets []hidrawDev
	if *devFlag != "" {
		d := hidrawDev{node: *devFlag, iface: -1}
		for _, cand := range devs {
			if cand.node == *devFlag {
				d = cand
			}
		}
		targets = append(targets, d)
	} else {
		for _, d := range devs {
			if _, known := products[d.product]; known && d.iface == batteryIface {
				targets = append(targets, d)
			}
		}
	}
	if len(targets) == 0 {
		fmt.Println("No Aerox 5 Wireless found on interface 3. All SteelSeries hidraw devices:")
		printList(devs)
		os.Exit(1)
	}

	for i, d := range targets {
		if i > 0 {
			fmt.Println()
		}
		cmd, err := commandFor(d, *cmdFlag)
		if err != nil {
			fail("%v", err)
		}
		probe(d, cmd, *window)
	}
}

func commandFor(d hidrawDev, override string) (byte, error) {
	if override != "" {
		v, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(override), "0x"), 16, 8)
		if err != nil {
			return 0, fmt.Errorf("invalid -cmd %q: %v", override, err)
		}
		return byte(v), nil
	}
	if p, ok := products[d.product]; ok && !p.wireless {
		return batteryCommand, nil
	}
	return batteryCommand | wirelessFlag, nil
}

func probe(d hidrawDev, cmd byte, window time.Duration) {
	fmt.Printf("== %s  %04x:%04x  iface=%d  %s\n", d.node, d.vendor, d.product, d.iface, describe(d))

	f, err := os.OpenFile(d.node, os.O_RDWR, 0)
	if err != nil {
		fmt.Printf("   open failed: %v\n", err)
		if errors.Is(err, os.ErrPermission) {
			fmt.Println("   hint: no udev rule granting access (rivalcfg's rule or our own), or try once with sudo")
		}
		return
	}
	defer f.Close()
	fd := int(f.Fd())

	// Drain anything queued before our request so it isn't mistaken for the reply.
	if stale := readAll(fd, 50*time.Millisecond); len(stale) > 0 {
		fmt.Printf("   drained %d stale report(s) before writing\n", len(stale))
		for _, r := range stale {
			fmt.Printf("     stale: %s\n", hexPrefix(r.data, 16))
		}
	}

	// Leading 0x00 is the report ID, exactly like rivalcfg's _hid_write; no padding.
	out := []byte{0x00, cmd}
	start := time.Now()
	if _, err := unix.Write(fd, out); err != nil {
		fmt.Printf("   write % x failed: %v\n", out, err)
		return
	}
	fmt.Printf("   wrote % x\n", out)

	reports := readAll(fd, window)
	if len(reports) == 0 {
		fmt.Printf("   no reply within %v (mouse asleep/off/out of range?)\n", window)
		return
	}
	for i, r := range reports {
		fmt.Printf("   report #%d after %4dms, %2d bytes: %s\n",
			i+1, r.at.Sub(start).Milliseconds(), len(r.data), hexPrefix(r.data, 16))
	}

	// rivalcfg decodes the first report it reads; do the same, and point out
	// a later report if it echoes our command byte instead.
	decode("first report", reports[0].data)
	for i, r := range reports[1:] {
		if len(r.data) > 0 && r.data[0] == cmd && reports[0].data[0] != cmd {
			decode(fmt.Sprintf("report #%d (echoes command)", i+2), r.data)
			break
		}
	}
}

func decode(label string, data []byte) {
	if len(data) < 2 {
		fmt.Printf("   %s: too short to decode\n", label)
		return
	}
	b := data[1]
	level := (int(b&^chargingFlag) - 1) * 5
	status := "Discharging"
	if b&chargingFlag != 0 {
		status = "Charging"
	}
	valid := ""
	if level < 0 || level > 100 {
		valid = "  (out of 0..100 -> rivalcfg would report nothing)"
	}
	fmt.Printf("   %s: data[0]=0x%02x data[1]=0x%02x -> %s %d %%%s\n", label, data[0], b, status, level, valid)
}

type report struct {
	data []byte
	at   time.Time
}

// readAll collects every input report that arrives within window.
func readAll(fd int, window time.Duration) []report {
	var out []report
	deadline := time.Now().Add(window)
	buf := make([]byte, 256)
	for {
		left := time.Until(deadline)
		if left <= 0 {
			return out
		}
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, err := unix.Poll(fds, int(left.Milliseconds())+1)
		if err == unix.EINTR {
			continue
		}
		if err != nil || n == 0 {
			return out
		}
		if fds[0].Revents&unix.POLLIN == 0 {
			return out // POLLERR/POLLHUP: device gone
		}
		m, err := unix.Read(fd, buf)
		if err != nil {
			return out
		}
		out = append(out, report{data: append([]byte(nil), buf[:m]...), at: time.Now()})
	}
}

func findDevices() ([]hidrawDev, error) {
	entries, err := os.ReadDir(sysfsHidrawRoot)
	if err != nil {
		return nil, err
	}
	var devs []hidrawDev
	for _, e := range entries {
		devLink := filepath.Join(sysfsHidrawRoot, e.Name(), "device")
		real, err := filepath.EvalSymlinks(devLink)
		if err != nil {
			continue
		}
		uevent, err := os.ReadFile(filepath.Join(real, "uevent"))
		if err != nil {
			continue
		}
		d := hidrawDev{node: "/dev/" + e.Name(), iface: -1, sysfsPath: real}
		for _, line := range strings.Split(string(uevent), "\n") {
			k, v, _ := strings.Cut(line, "=")
			switch k {
			case "HID_ID": // e.g. 0003:00001038:00001852
				parts := strings.Split(v, ":")
				if len(parts) == 3 {
					vid, _ := strconv.ParseUint(parts[1], 16, 32)
					pid, _ := strconv.ParseUint(parts[2], 16, 32)
					d.vendor, d.product = uint16(vid), uint16(pid)
				}
			case "HID_NAME":
				d.name = v
			}
		}
		if d.vendor != vendorID {
			continue
		}
		// The HID device's parent is the USB interface directory.
		if raw, err := os.ReadFile(filepath.Join(real, "..", "bInterfaceNumber")); err == nil {
			if n, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 16, 8); err == nil {
				d.iface = int(n)
			}
		}
		devs = append(devs, d)
	}
	sort.Slice(devs, func(i, j int) bool { return devs[i].node < devs[j].node })
	return devs, nil
}

func printList(devs []hidrawDev) {
	if len(devs) == 0 {
		fmt.Println("   (none)")
		return
	}
	for _, d := range devs {
		fmt.Printf("   %s  %04x:%04x  iface=%d  %s  [%s]\n", d.node, d.vendor, d.product, d.iface, describe(d), d.name)
	}
}

func describe(d hidrawDev) string {
	if p, ok := products[d.product]; ok {
		return p.name
	}
	return "unknown SteelSeries device"
}

func hexPrefix(b []byte, n int) string {
	s := hex.EncodeToString(b[:min(n, len(b))])
	var parts []string
	for i := 0; i < len(s); i += 2 {
		parts = append(parts, s[i:i+2])
	}
	out := strings.Join(parts, " ")
	if len(b) > n {
		out += " ..."
	}
	return out
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "probe: "+format+"\n", args...)
	os.Exit(1)
}
