# mouse-monitor

This project behaves similar to controller-monitor which provides a tray icon and a nofitication trigger to show up estimated mouse battery level from a Steelseries wireless mouse.

## Install

The battery is read directly from the mouse via Linux hidraw; no extra tools, Python or driver are needed.
Supported: SteelSeries Aerox 5 Wireless (incl. Destiny 2 and Diablo IV editions), via 2.4 GHz dongle or cable.

1. Allow your user to access the mouse by installing the udev rule, then re-plug the dongle/cable:
   ```
   sudo cp assets/udev/70-mouse-monitor.rules /etc/udev/rules.d/
   sudo udevadm control --reload-rules && sudo udevadm trigger
   ```
2. Check the access by running ``` mouse-monitor -debug-battery ``` (or ``` go run . -debug-battery ```), which prints the raw and decoded battery reply and exits
3. Adjust the desktop file by creating a copy of the ``` mouse-monitor.desktop.example ``` adjusting the paths within Exec and Icon entyry to this repo locally
4. Compile this project using Go and running ``` go install ``` which creates a binary inside ``` $GOPATH/bin ``` - this has to referenced inside the desktop file referenced a step prior

While the mouse is asleep, switched off or out of range, the tray shows the neutral icon and the last known level.

## Thanks to rivalcfg

 which provides a pretty good python access to the Steelseries mice which is not provided by the the vendor itself for linux. The battery protocol used here was taken from it. Visit https://flozz.github.io/rivalcfg/index.html
