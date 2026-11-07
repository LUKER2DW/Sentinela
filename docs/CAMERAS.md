# Cameras

Sentinela records and shows **any camera that serves RTSP** (H.264 or
H.265), which is nearly every IP camera, NVR and DVR. It never re-encodes
video, so the camera's own stream goes to disk and to your screen as-is.

## Adding a camera

In the browser: **System → Cameras → Configure cameras → Add camera**
(or the first-run guide). Type the camera's address, user and password, then:

- **Most cameras (ONVIF):** press **Find the streams over ONVIF**. The
  camera lists its streams and Sentinela fills in the paths: the largest
  picture becomes the main stream, the next one the light stream used for
  grids and phones. Turn ONVIF on in the camera's own settings if it is off;
  some brands need a separate ONVIF user.
- **A brand with a preset:** pick it under *Marca* and no paths are needed.
- **Anything else:** choose *Any camera* and type the stream paths
  yourself (see the table below or the camera's manual).

Save: the server checks the settings, keeps a backup, applies them and rolls
back if the camera cannot be used. Passwords stay on the server.

From a shell instead: `sudo /opt/sentinela/scripts/add-camera.sh` (see
[INSTALL.md §12](INSTALL.md#12-more-cameras)).

## What works with which camera

| | Any RTSP camera | With ONVIF | XMEye / Xiongmai |
|---|---|---|---|
| Live view, 24/7 recording, timeline, export | ✓ | ✓ | ✓ |
| Streams found automatically | – | ✓ | ✓ (preset) |
| Motion and tamper events | – | ✓ (ONVIF events) | ✓ |
| Object detection (people, vehicles, animals) | ✓ (sub stream) | ✓ | ✓ |
| PTZ, saved positions, tours, white light, day/night | – | – | ✓ (DVRIP) |
| Two-way audio | ✓ (if `talk: true`) | ✓ (if `talk: true`) | ✓ |

Each camera says what it has under `features` (System → Cameras: "Has PTZ",
"Has a white light", "Has a speaker"). The live page shows only those
controls and the API refuses the others, so a fixed XMEye bullet gets no PTZ
pad and a camera without a speaker gets no talk button. XMEye cameras default
to all three; other brands default to none. Talk on another brand uses the
camera's own ONVIF/RTSP two-way audio through go2rtc. PTZ over ONVIF for
other brands is on the roadmap.

## Presets

| `vendor` | Main stream | Light stream |
|---|---|---|
| `generic` | your `rtsp_main` | your `rtsp_sub` (optional) |
| `hikvision` (also many OEMs: HiLook, Annke, EZVIZ) | `/Streaming/Channels/{channel}01` | `/Streaming/Channels/{channel}02` |
| `dahua` (also Amcrest, Imou, Lorex OEMs) | `/cam/realmonitor?channel={channel}&subtype=0` | `…&subtype=1` |
| `xmeye` (Xiongmai modules, many no-name cameras) | XMEye path with credentials | sub stream |

`channel` selects the camera on an NVR or DVR (default 1).

## Common stream paths for `generic`

Prefer ONVIF discovery; these are the usual defaults and vary by model and
firmware:

| Brand | Main | Light |
|---|---|---|
| Reolink | `/h264Preview_01_main` (newer firmware: `/Preview_01_main`) | `/h264Preview_01_sub` |
| TP-Link Tapo / VIGI | `/stream1` | `/stream2` |
| Uniview | `/unicast/c1/s0/live` | `/unicast/c1/s1/live` |
| Axis | `/axis-media/media.amp` | `/axis-media/media.amp?resolution=640x360` |
| Foscam | `/videoMain` | `/videoSub` |

## Tips

- Give each camera a fixed address (a DHCP reservation on the router).
- Set a strong password in the camera. For XMEye avoid `& ? / # %` and
  spaces; for others avoid `: @ / ? # %` and spaces (they end up in the
  RTSP URL).
- Turn off the camera's cloud/P2P and UPnP, and block its internet access on
  the router: Sentinela needs only the LAN.
- H.265 plays live in browsers that decode it (most recent Chrome, Edge and
  Safari); otherwise use the light stream, often H.264.
- Point the camera's NTP at the Sentinela server (it serves time to the
  LAN) so the picture's clock matches the recordings.
- XMEye protocol details and the commands Sentinela may send: [CAMERA.md](CAMERA.md).
