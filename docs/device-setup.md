# Working with the tablets

A cheat sheet for the reMarkable Paper Pro and Paper Pro Move with a Mac:
the USB network, logging in without a password, installing and running
goMarkableStream, testing a build, and what to do when something seems
stuck.

Over USB the tablet is always `10.11.99.1`. Both tablets use that address,
so connect one at a time.

Run the commands **on the Mac**: its prompt shows your user and Mac and
ends with `%`. A prompt like `root@imx93-…:~#` (starting with `root@`,
ending with `#`) means you are logged into the tablet: type `exit` first.
On the tablet, a name like `move` fails with "Temporary failure in name
resolution". Commands meant for the tablet are written as `ssh move '…'`.

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

The tablet shows up as a hardware port with its own name (for example
`Hardware Port: Paper Pro Move`, `Device: en9`; the number can differ).

- Listed among the services with a `*` in front: it is disabled. Turn it on:
  ```
  sudo networksetup -setnetworkserviceenabled "Paper Pro Move" on
  ```
- Not among the services at all: create one for that hardware port (the
  `Hardware Port:` name from the first list) and let it ask for an address:
  ```
  sudo networksetup -createnetworkservice "Paper Pro Move" "Paper Pro Move"
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

The tablet's ssh server (dropbear) accepts SSH keys.

**Create a key on the Mac** (once):

```
ssh-keygen -t ed25519 -f ~/.ssh/id_ed25519_remarkable -C "remarkable"
```

**Give each tablet a short name** (once). `~/.ssh/config` is a text file to
edit, not a command to run, and it needs no `sudo` (a copy owned by root
would stop ssh from reading it). Paste this into the terminal: it puts both
entries at the top of the file, where ssh looks first, and does nothing if
they are already there.

```
grep -q '^Host move$' ~/.ssh/config 2>/dev/null || {
  mkdir -p ~/.ssh
  cat > /tmp/remarkable-ssh <<'END'
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

END
  [ -f ~/.ssh/config ] && cat ~/.ssh/config >> /tmp/remarkable-ssh
  mv /tmp/remarkable-ssh ~/.ssh/config
  chmod 600 ~/.ssh/config
}
```

`HostKeyAlias` keeps the two tablets apart: without it, ssh warns
"REMOTE HOST IDENTIFICATION HAS CHANGED" each time you switch, because both
use the same address. Check the result with `cat ~/.ssh/config`: if an
older `Host move` block sits further down from an earlier attempt, delete
it (`nano ~/.ssh/config`).

**Copy the key to each tablet**, with that tablet connected by USB. This
asks for the tablet's root password one last time:

```
ssh-copy-id -i ~/.ssh/id_ed25519_remarkable.pub move
```

For the Paper Pro, connect it and use `pro` instead of `move`. If
`ssh-copy-id` complains, this does the same by hand:

```
cat ~/.ssh/id_ed25519_remarkable.pub | ssh move 'mkdir -p ~/.ssh && chmod 700 ~/.ssh && cat >> ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys'
```

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

**Is it running?** (Paper Pro Move; for the Paper Pro use `pro` here and
below.)

```
ssh move 'grep -ls goMarkableStrea /proc/[0-9]*/comm'
```

A line like `/proc/1234/comm` means it runs (1234 is its process number);
no output means it does not.

**Stop it.** The line returns once it has ended: up to about 10 seconds
while a page shows the stream, and after 15 seconds it is forced to end.

```
ssh move 'k() { for p in $(grep -ls goMarkableStrea /proc/[0-9]*/comm); do p=${p%/comm}; kill $1 ${p#/proc/}; done; }; k; i=0; while grep -qs goMarkableStrea /proc/[0-9]*/comm; do i=$((i+1)); [ $i = 15 ] && k -9; sleep 1; done'
```

- The tablet has no `pkill` or `pgrep`, so both lines ask the kernel
  directly: `/proc/<process number>/comm` holds the name of every running
  program, cut to 15 characters. `goMarkableStrea` is the start of both
  binaries' names. The shell running the line and `grep` have their own
  names, so the line does not find (or stop) itself.
- If you installed the service (section 3b), use
  `ssh move 'systemctl stop goMarkableStream'` instead.

**Copy it, start it, show its log** (for the Paper Pro use `pro` and
`goMarkableStream-rmpp`). To update, stop the old one first (above). With
the service, update as in section 3b instead: a copy started here does not
read the service's settings file, so it would run with the default login.

```
scp ~/Downloads/goMarkableStream-RMMOVE move: \
  && ssh move 'chmod +x goMarkableStream-RMMOVE && RK_DEBUG_GESTURES=true nohup ./goMarkableStream-RMMOVE > gms.log 2>&1 < /dev/null &' \
  && ssh move 'tail -f gms.log'
```

- `scp` fails with `dest open "./goMarkableStream-RMMOVE": Failure` while
  the old one still runs (a running program's file cannot be overwritten):
  stop it and copy again.
- `nohup … &` keeps it running when the ssh connection drops (a program
  started in the foreground of an ssh session stops with it), and the log
  stays in `/home/root/gms.log`.
- Ctrl-C only ends `tail`, not the program.
- `RK_DEBUG_GESTURES=true` logs every touch gesture and the touch panel at
  startup; leave it out for normal use.
- If `scp` fails with "subsystem request failed", add `-O` (the older copy
  protocol).
- Do not run `./goMarkableStream download`: it fetches the upstream
  releases, not this fork's.

**A good start looks like this:**

- `Version: …` names the commit you meant to run (for example
  `v0.0.0-…-93b04fdc601e`, or `v1.0.0` for a tagged release).
- `using framebuffer candidate 0`, and that candidate `looks like a page`
  (close to 100% opaque, mostly white).
- `Serving on [::]:2001`.

**Open it in the browser:** `https://10.11.99.1:2001` (with `https`). For a
presentation, choose **Present Slides…** in the sidebar and paste the
address of the slides (the last five are listed there), or add
`?present=<URL of the slides>` to the address. The login is
`RK_SERVER_USERNAME` / `RK_SERVER_PASSWORD` (default `admin` /
`password`).

## 3b. Running it all the time

goMarkableStream can run as a system service: it starts when the tablet
starts and again after a crash. Install it from the copied binary; the
line first stops a copy you started by hand (as in section 3, **Stop it**),
which would otherwise hold the port and keep the service restarting:

```
ssh move 'k() { for p in $(grep -ls goMarkableStrea /proc/[0-9]*/comm); do p=${p%/comm}; kill $1 ${p#/proc/}; done; }; k; i=0; while grep -qs goMarkableStrea /proc/[0-9]*/comm; do i=$((i+1)); [ $i = 15 ] && k -9; sleep 1; done; ./goMarkableStream-RMMOVE install'
```

This writes `/etc/systemd/system/goMarkableStream.service` (pointing at
`/home/root/goMarkableStream-RMMOVE`) and a settings file,
`/home/root/.config/goMarkableStream/env`, and starts the service. If it
fails with "Read-only file system", make the system partition writable
first (`ssh move 'mount -o remount,rw /'`) and run it again.

**Set your own login.** Running all the time, it answers anyone on the
same Wi-Fi, and the default login is `admin` / `password`:

```
ssh move 'printf "RK_SERVER_USERNAME=tobi\nRK_SERVER_PASSWORD=choose-one\n" >> /home/root/.config/goMarkableStream/env && systemctl restart goMarkableStream'
```

Other settings (`RK_DEBUG_GESTURES=true` and the like) go into the same
file, one per line; restart the service after a change.

| To | Run |
|---|---|
| See whether it runs | `ssh move 'systemctl status goMarkableStream'` |
| Follow its log | `ssh move 'journalctl -u goMarkableStream -f'` |
| Stop / start it | `ssh move 'systemctl stop goMarkableStream'` / `start` |
| Remove it | `ssh move 'systemctl disable --now goMarkableStream'` |

**Updating** the binary of the service: stop the service, copy, start it.

```
ssh move 'systemctl stop goMarkableStream' \
  && scp ~/Downloads/goMarkableStream-RMMOVE move: \
  && ssh move 'chmod +x goMarkableStream-RMMOVE && systemctl start goMarkableStream'
```

**After a firmware update** the service file can be gone (the update
replaces the system partition); the binary and the settings in
`/home/root` stay. Run the `install` line again.

**Battery.** Without a browser connected it waits for input and reads
nothing from the screen. While a page shows the stream, it reads and
compresses the screen while you write (and pauses within 2 seconds once
nothing moves) and sends it over USB or Wi-Fi; that, and Wi-Fi itself, are what
cost battery. It has not been measured on these tablets: compare the
battery level over a talk with and without the stream to see what it costs
you. When the tablet sleeps, the program sleeps with it.

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
5. `W`, `Z`, and the key hint (`W Z I L F`). It shows once per browser:
   use a private window to see it again.
6. On the Paper Pro also: swipes in portrait and, after `R`, in landscape.

When both tablets pass, tag a version; GitHub builds its release:

```
git tag v1.0.0 && git push origin v1.0.0
```

## 6. When something seems stuck

**The web page does not load.**

- `ping -c 3 10.11.99.1`: no answer means the USB network is down (see
  section 1), or the tablet is asleep.
- `ssh move 'grep -ls goMarkableStrea /proc/[0-9]*/comm'`: nothing means
  goMarkableStream is not running. Start it again (section 3). With the
  service, see why it stopped (`systemctl status` and `journalctl`, as in
  section 3b) and start it with `systemctl start goMarkableStream`.
- The address needs `https://`.

**The tablet does not react.**

- It may be asleep: press the power button briefly. While it sleeps, the
  USB network, ssh and the web page stop answering. Keep it awake while
  testing (touch it now and then, or set a longer sleep time).
- Frozen: hold the power button for about 10–15 seconds until it switches
  off, then turn it on. **Not 25 seconds or longer**: that starts the
  factory reset / recovery mode.
- After a restart nothing of goMarkableStream runs until you start it,
  unless you installed the service (section 3b).

**What happened?** The tablet keeps its system log across restarts:

```
ssh move 'journalctl --list-boots | tail -3'
ssh move 'journalctl -b -1 --no-pager | tail -n 150'
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
started as in section 3: `ssh move 'tail -n 100 gms.log'`.

**"Another connection has the stream".** Another page (a second tab, a
phone) shows the stream; only one at a time does. Close it, and this page
connects by itself within about 10 seconds.
