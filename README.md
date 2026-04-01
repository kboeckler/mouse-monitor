# mouse-monitor

This project behaves similar to controller-monitor which provides a tray icon and a nofitication trigger to show up estimated mouse battery level from a Steelseries wireless mouse.

## Install

Installing ist split up into three parts: setting up the access to the Steelseries mouse by using rivalcfg and then configuring this repo accessing rivalcfg. Afterwards the executable must be compiled or used.

1. Install rivalfg from sources first following this documentation: https://flozz.github.io/rivalcfg/install.html
2. After installing a local .env installation within a directory, check its accessability by running ``` rivalcfg.env/bin/rivalcfg --help ```
3. Adjust the fetching script by creating a copy next to ``` assets/scripts/get_rivalcfg_batterylevel.sh.example ``` with the same name except the .example ending, adjusting your username and paths
4. Adjust the desktop file by creating a copy of the ``` mouse-monitor.desktop.example ``` adjusting the paths within Exec and Icon entyry to this repo locally
5. Compile this project using Go and running ``` go install ``` which creates a binary inside ``` $GOPATH/bin ``` - this has to referenced inside the desktop file referenced a step prior
6. Alternatively to compile with Go use the shipped binary from ``` build/ ``` directory but this is compiled only against my linux distro and kernel and things (currently CachyOs)

## Thanks to rivalfg

 which provides a pretty good python access to the Steelseries mice which is not provided by the the vendor itself for linux. Visit https://flozz.github.io/rivalcfg/index.html
