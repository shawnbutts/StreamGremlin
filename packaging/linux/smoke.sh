#!/bin/sh
# The Linux smoke test (D-114), run by `make linux-smoke` inside a fresh
# container of each distribution with the package at $1: it installs the
# package with the distribution's own tool (which pulls in what it
# depends on), checks every library the app needs is there, runs the
# command line, and starts the app on a virtual display, with a session
# bus as a desktop has, until its engine answers. A throwaway data folder;
# no keyring, so sign-ins would go in the encrypted file.
set -eu
pkg=$1
say() { echo "smoke: $*"; }

case $pkg in
*.deb)
	export DEBIAN_FRONTEND=noninteractive
	apt-get update -qq
	apt-get install -y -qq "$pkg" xvfb dbus >/dev/null
	;;
*.rpm)
	dnf install -y -q "$pkg" xorg-x11-server-Xvfb dbus-daemon >/dev/null
	;;
*)
	say "don't know how to install $pkg"
	exit 2
	;;
esac
say "installed $(basename "$pkg")"

missing=$(ldd /usr/bin/streamgremlin-desktop | grep 'not found' || true)
if [ -n "$missing" ]; then
	say "libraries missing:"
	echo "$missing"
	exit 1
fi
test -f /usr/share/applications/com.streamgremlin.StreamGremlin.desktop
test -f /usr/share/icons/hicolor/256x256/apps/com.streamgremlin.StreamGremlin.png
streamgremlin version

# WebKit runs its pages in a bubblewrap sandbox, which needs new
# namespaces. Some container hosts refuse them (Docker on GitHub's Ubuntu
# runners); a desktop allows them. There, start without the sandbox, and
# say so, to test everything else; where they work, test with it.
if command -v bwrap >/dev/null && ! bwrap --ro-bind / / true 2>/tmp/bwrap.txt; then
	say "this container can't make namespaces ($(cat /tmp/bwrap.txt)); starting WebKit without its sandbox"
	export WEBKIT_DISABLE_SANDBOX_THIS_IS_DANGEROUS=1
fi

export STREAMGREMLIN_DATA_DIR=/tmp/streamgremlin-smoke
Xvfb :99 -screen 0 1280x800x24 >/tmp/xvfb.log 2>&1 &
export DISPLAY=:99
# A desktop session always has a session bus, which the app needs to
# keep to one window; a container has none, so it gets its own.
dbus-run-session -- streamgremlin-desktop >/tmp/app.log 2>&1 &
app=$!
i=0
until streamgremlin status >/tmp/status.txt 2>&1; do
	i=$((i + 1))
	if ! kill -0 "$app" 2>/dev/null || [ "$i" -ge 120 ]; then
		say "the app didn't start its engine:"
		# What WebKit, GTK or the sandbox said, and a Go crash's header
		# (its signal and where), which comes before every goroutine.
		grep -n -i -E 'bwrap|sandbox|webkit|gtk-(critical|error)|abort|^(SIG[A-Z]+|fatal error|panic):|^signal ' /tmp/app.log | head -40 || true
		awk '/^(SIG[A-Z]+|fatal error|panic):/ {n = 25} n > 0 {print; n--}' /tmp/app.log | head -60
		echo "--- the log"
		if [ "$(wc -l </tmp/app.log)" -gt 70 ]; then
			head -30 /tmp/app.log
			echo "..."
			tail -40 /tmp/app.log
		else
			cat /tmp/app.log
		fi
		cat /tmp/status.txt
		exit 1
	fi
	sleep 1
done
cat /tmp/status.txt
kill "$app"
say "ok"
