# reMarkable Paper Pro Move

The Move build is isolated behind the `rmmove` build tag. Existing reMarkable 2
and full-size Paper Pro builds keep their existing constants and readers.

Build the lightweight ARM64 binary without Tailscale:

```sh
make build-remarkable-paper-pro-move
```

The output is `goMarkableStream-RMMOVE`. Copy it to the tablet and launch it
manually:

```sh
scp goMarkableStream-RMMOVE remarkable:/home/root/
ssh remarkable /home/root/goMarkableStream-RMMOVE
```

At startup the binary checks that it runs on a Paper Pro Move
(`reMarkable Chiappa` in `/sys/devices/soc0/machine` or
`/proc/device-tree/model`) and refuses to run on any other device.

By default the server uses HTTPS and JWT authentication on port 2001. For an
isolated unauthenticated test:

```sh
RK_HTTPS=false RK_JWT_ENABLED=false /home/root/goMarkableStream-RMMOVE -unsafe
```

## Chiappa framebuffer layout

This layout was measured on a Paper Pro Move (`reMarkable Chiappa`) running
firmware 3.28.0.169:

- Storage is portrait BGRA, 960 x 1696 pixels, four bytes per pixel.
- The visible image is 954 x 1696 pixels.
- Each storage row is 3,840 bytes. Its first 3,816 bytes are visible pixels;
  the final 24 bytes are six right-edge alignment pixels and are discarded.
- No rotation, transpose, or channel conversion is required after row packing.
- The tightly packed response remains 6,471,936 bytes, preserving the existing
  `/stream`, `/raw`, and `/screenshot` response formats.

The buffer is found much as on the Paper Pro (see
`internal/remarkable/fbscan.go`): by walking the malloc chunk headers of the
anonymous regions of `xochitl` and picking the chunk that fits the stored
frame, 960 x 1696 x 4 = 6,512,640 bytes. Unlike on the Paper Pro, its region
does not follow the last `/dev/dri/card0` mapping directly: on a Move measured
on 2026-10-12, a thread stack and a malloc arena sat between them, six lines
of `/proc/<pid>/maps`. So the Move's buffer is recognised by its exact size,
as glibc sizes the allocation of the frame (6,516,736 bytes), in a region up
to 16 lines after a card0 mapping. On the Move that size ranks before mere
adjacency, so a buffer that only roughly fits, such as one of 6,578,176 bytes,
is not taken.

The candidates are logged at startup, each with a page check (the share of
opaque and of near-white pixels in a sample), and `RK_FB_CANDIDATE=<n>`
forces one of them if the automatic choice shows the wrong picture.

The Move pen reports X in 0-6760 and Y in 0-11960. It uses the Paper Pro's
portrait-native event transform.
