#!/bin/bash
if [ -z "$3" ]; then
	echo "Not enough arguments supplied (needed: 3)" >&2
	exit 1
fi
TITLE=$1
BODY=$2
ICON=$3
notify-send "$TITLE" "$BODY" --icon="$ICON" --app-name="" --hint='string:desktop-entry:mouse-monitor'

