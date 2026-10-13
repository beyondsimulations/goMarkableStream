# Working with the tablets

A cheat sheet for the reMarkable Paper Pro and Paper Pro Move with a Mac:
the USB network, logging in without a password, installing and running
goMarkableStream, testing a build, and what to do when something seems
stuck.

Over USB the tablet is always `10.11.99.1`. Both tablets use that address,
so connect one at a time.

Unless a step says otherwise, run the commands **on the Mac**: the prompt
reads `tobi@MacBook-Air-15-Tobi ~ %`. If it reads `root@imx93-chiappa:~#`,
you are logged into the tablet; type `exit` first. (On the tablet, a name
like `move` fails with "Temporary failure in name resolution".)

## 1. USB network on the Mac

Check the link first:

```
ping -c 3 10.11.99.1
```

If there is no answer, macOS may have created the tablet's network port but
left it switched off. Find the port and its service:

```
networksetup -listallhardwareports
networksetup -listallnetworkservices
```

The tablet shows up under its own name (for example `Paper Pro Move`, on a
device such as `en9`; the number can differ).

- Listed with a `*` in front: it is disabled. Turn it on:
  ```
  sudo networksetup -setnetworkserviceenabled "Paper Pro Move" on
  ```
- Not listed at all: create it, with the device name from the first list:
  ```
  sudo networksetup -createnetworkservice "Paper Pro Move" en9
  sudo networksetup -setdhcp "Paper Pro Move"
  ```

Then check that the port is up and has an address in `10.11.99.x`:

```
ifconfig en9 | grep -E "flags|status|inet"
```

If it is up (`UP` in the flags) but has no `inet 10.11.99.x` line, set the
address by hand; unplugging the cable undoes it:

```
sudo ifconfig en9 inet 10.11.99.2 netmask 255.255.255.248 up
```

Over Wi-Fi the tablet has another address. goMarkableStream prints it at
startup (`Local IP address: …`), and it works when the Mac is on the same
network.

## 2. Logging in without a password

The tablet's ssh server (dropbear) accepts SSH keys. Do this once per
tablet, with that tablet connected by USB.

**Create a key on the Mac** (once):

```
ssh-keygen -t ed25519 -f ~/.ssh/id_ed25519_remarkable -C "remarkable"
```

**Copy it to the tablet.** This asks for the tablet's root password one
last time:

```
ssh-copy-id -i ~/.ssh/id_ed25519_remarkable.pub root@10.11.99.1
```

If `ssh-copy-id` complains, this does the same by hand:

```
cat ~/.ssh/id_ed25519_remarkable.pub | ssh root@10.11.99.1 'mkdir -p ~/.ssh && chmod 700 ~/.ssh && cat >> ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys'
```

**Give each tablet a short name.** `~/.ssh/config` is a text file to edit,
not a command to run, and it needs no `sudo` (a copy owned by root would
stop ssh from reading it). Paste this into the terminal to add both
entries:

```
mkdir -p ~/.ssh && cat >> ~/.ssh/config <<'EOF'

Host move
    HostName 10.11.99.1
    User root
    IdentityFile ~/.ssh/id_ed25519_remarkable
    IdentitiesOnly yes
    HostKeyAlias remarkable-move

Host pro
    HostName 10.11.99.1
    User root
    IdentityFile ~/.ssh/id_ed25519_remarkable
    IdentitiesOnly yes
    HostKeyAlias remarkable-pro
EOF
chmod 600 ~/.ssh/config
```

`HostKeyAlias` keeps the two tablets apart: without it, ssh warns
"REMOTE HOST IDENTIFICATION HAS CHANGED" each time you switch, because both
use the same address.

**Test it:** `ssh move` (or `ssh pro`) logs in without a password.

After a firmware update:

- ssh may warn that the host identification changed (the tablet got a new
  host key). Remove the old entry and log in again:
  `ssh-keygen -R remarkable-move` (or `remarkable-pro`).
- If it asks for the password again, copy the key again as above.

## 3. Installing and running goMarkableStream

**Download the newest build** from the fork's
[Latest build](https://github.com/beyondsimulations/goMarkableStream/releases/tag/latest-main)
(built from `main` by GitHub Actions):

```
curl -L -o ~/Downloads/goMarkableStream-RMMOVE https://github.com/beyondsimulations/goMarkableStream/releases/download/latest-main/goMarkableStream-RMMOVE
curl -L -o ~/Downloads/goMarkableStream-rmpp https://github.com/beyondsimulations/goMarkableStream/releases/download/latest-main/goMarkableStream-rmpp
```

`goMarkableStream-RMMOVE` is for the Paper Pro Move, `goMarkableStream-rmpp`
for the Paper Pro. The release notes list their SHA-256
(`shasum -a 256 ~/Downloads/goMarkableStream-*`).

**Copy, stop the old one, start the new one, show its log** (Paper Pro
Move; for the Paper Pro use `pro` and `goMarkableStream-rmpp`):

```
scp ~/Downloads/goMarkableStream-RMMOVE move: && ssh move 'pkill -f goMarkableStream; chmod +x goMarkableStream-RMMOVE && RK_DEBUG_GESTURES=true nohup ./goMarkableStream-RMMOVE > gms.log 2>&1 &' && ssh move 'tail -f gms.log'
```

- `nohup … &` keeps it running when the ssh connection drops (a program
  started in the foreground of an ssh session stops with it), and the log
  stays in `/home/root/gms.log`.
- Ctrl-C only ends `tail`, not the program.
- `RK_DEBUG_GESTURES=true` logs every touch gesture and the touch panel at
  startup; leave it out for normal use.
- Stop it: `ssh move 'pkill -f goMarkableStream'`.
- Is it running? `ssh move 'ps | grep [g]oMarkableStream'`.
- Do not run `./goMarkableStream download`: it fetches the upstream
  releases, not this fork's.

**A good start looks like this:**

- `Version: …` names the commit you meant to run (for example
  `v0.0.0-…-93b04fdc601e`, or `v1.0.0` for a tagged release).
- `using framebuffer candidate 0`, and that candidate `looks like a page`
  (close to 100% opaque, mostly white).
- `Serving on [::]:2001`.

**Open it in the browser:** `https://10.11.99.1:2001` (with `https`). Add
`?present=<URL of the slides>` for a presentation. The login is
`RK_SERVER_USERNAME` / `RK_SERVER_PASSWORD` (default `admin` /
`password`).

## 4. Presentation keys

| Key | Does |
|---|---|
| `W` | White board (the white page over the slides) |
| `Z` | Slides or ink on top |
| `I` | The tablet ignores your fingers (no page turns); swipes still change slides |
| `L` | Laser |
| `F` | Full screen |
| `R` | Turn the view (landscape with the tablet's top edge on the right) |
| `?` | Help |

A one-finger swipe from right to left moves to the next slide or bullet,
left to right back. See the README's
[Swipe Gestures](../README.md#swipe-gestures) for the rest.

Keys reach the page only while it has the keyboard: after clicking into the
slides, click the control bar at the bottom first.

## 5. Testing a build on the tablets

1. Swipe without `I`: the slide moves one step (next bullet or slide,
   also inside a chapter). The notebook page on the tablet still turns.
2. Press `I`: the page says "The tablet ignores your fingers", the log
   `Touch: the tablet ignores finger input`.
3. Swipe with `I` on: the slides move, the notebook page stays, the pen
   still writes.
4. Press `I` again: "The tablet reacts to your fingers again", log
   `Touch: the tablet reacts to finger input again`.
5. `W`, `Z`, and the one-time key hint (`W Z I L F`).
6. On the Paper Pro also: swipes in portrait and, after `R`, in landscape.

When both tablets pass, tag a version; GitHub builds its release:

```
git tag v1.0.0 && git push origin v1.0.0
```

## 6. When something seems stuck

**The web page does not load.**

- `ping -c 3 10.11.99.1`: no answer means the USB network is down (see
  section 1), or the tablet is asleep.
- `ssh move 'ps | grep [g]oMarkableStream'`: nothing means goMarkableStream
  is not running. Start it again (section 3).
- The address needs `https://`.

**The tablet does not react.**

- It may be asleep: press the power button briefly. While it sleeps, the
  USB network, ssh and the web page stop answering. Keep it awake while
  testing (touch it now and then, or set a longer sleep time).
- Frozen: hold the power button for about 10–15 seconds until it switches
  off, then turn it on. **Not 25 seconds or longer**: that starts the
  factory reset / recovery mode.
- After a restart nothing of goMarkableStream runs until you start it.

**What happened?** The tablet keeps its system log across restarts:

```
journalctl --list-boots | tail -3
journalctl -b -1 --no-pager | tail -n 150
```

`-b -1` is the run before the last restart. In it:

- `dropbear … Error reading: No route to host`: the USB network to the Mac
  dropped (and a goMarkableStream started in that ssh session stopped with
  it, unless it ran under `nohup`).
- `slumberinhibitor` or `Adjusting USB current to 100mA`: the tablet went to
  sleep.
- A kernel `Oops`, `Out of memory` or a crashed `xochitl` would point at a
  real crash.

goMarkableStream's own messages are in `/home/root/gms.log` when it was
started as in section 3.

**"Another connection has the stream".** Another page (a second tab, a
phone) shows the stream; only one at a time does. Close it, and this page
connects by itself within about 10 seconds.
