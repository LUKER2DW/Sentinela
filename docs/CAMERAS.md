# Cameras

Sentinela records and shows **any camera that serves RTSP** (H.264 or
H.265), which is nearly every IP camera, NVR and DVR. It never re-encodes
video, so the camera's own stream goes to disk and to your screen as-is.

## Adding a camera

In the browser: **System → Cameras → Camera settings → Add camera**
(or the first-run guide). Type the camera's address, user and password, then:

- **Most cameras (ONVIF):** press **Find the streams over ONVIF**. The
  camera lists its streams and Sentinela fills in the paths: the largest
  picture becomes the main stream, the next one the light stream used for
  grids and phones. Turn ONVIF on in the camera's own settings if it is off;
  some brands need a separate ONVIF user.
- **A brand with a preset:** pick it under *Brand* and no paths are needed.
- **Anything else:** choose *Any camera (ONVIF / RTSP)* and type the stream paths
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
| Motion events | ✓ (from the video) | ✓ (ONVIF events) | ✓ |
| Tamper events | – | ✓ (ONVIF events) | ✓ |
| Object detection (people, vehicles, animals) | ✓ | ✓ | ✓ |
| PTZ (move, zoom), saved positions, tours | – | ✓ (ONVIF PTZ, if `ptz: true`) | ✓ (DVRIP) |
| Focus | – | – | ✓ (DVRIP) |
| White light, day/night | – | – | ✓ (DVRIP) |
| Two-way audio | ✓ (if `talk: true`) | ✓ (if `talk: true`) | ✓ |

A camera that sends no ONVIF motion events (RTSP only, or events that
stopped) gets its motion from the video, so it still gets motion events and
object detection. Choose per camera in System → Cameras → *Motion
detection* (`motion: auto | camera | video` in config.yaml; see
[Motion and object detection](../README.md#motion-and-object-detection)).

## Setting up what each camera has

Not every camera moves, has a light or a speaker. Each camera says what it
has, in System → Cameras → *Camera settings* or in `config.yaml`; the live
page shows only those controls and the API refuses the others, so a fixed
bullet gets no PTZ pad and a camera without a speaker gets no talk button.

| Setting | `config.yaml` | Default |
|---|---|---|
| Has PTZ (moves and zooms) | `features.ptz` | XMEye: on (DVRIP). Others: off; on = ONVIF PTZ |
| Has a white light | `features.light` | XMEye: on. Others: not available |
| Has a speaker | `features.talk` | XMEye: on (DVRIP). Others: off; on = the camera's ONVIF/RTSP backchannel through go2rtc (a second camera session while someone talks). Only operators talk: the server keeps the speaker on its own go2rtc stream |
| Uses ONVIF (events, clock, PTZ) | `onvif` | on; off for an RTSP-only camera (its motion then comes from the video) |
| ONVIF port | `onvif_port` | 80; XMEye 8899, Reolink 8000, Tapo 2020 |
| ONVIF address | `onvif_path` | `/onvif/device_service` |
| Upside-down mount | `invert_pan_tilt` (`invert_pan`, `invert_tilt`) | off |

*Find the streams over ONVIF* also ticks *Has PTZ* when the camera reports
a PTZ service; untick it for a fixed camera that reports one anyway.

Sentinela asks the device service (`onvif_path`) where the camera's other
ONVIF services live (GetCapabilities), so brands that put events or PTZ on
their own paths (Hikvision `/onvif/Events`, Axis `/onvif/services`) work
without settings. It also reads the camera's clock first (unauthenticated
GetSystemDateAndTime) and signs its ONVIF requests in the camera's time, so
a camera whose clock is minutes off still accepts them.

### What Sentinela sends over ONVIF

Read-only: GetCapabilities, GetSystemDateAndTime, GetDeviceInformation,
GetProfiles, GetStreamUri, GetPresets, and the event subscription
(CreatePullPointSubscription, PullMessages, Renew, Unsubscribe).

PTZ, only for a camera with `features.ptz` on and only when someone presses
a control or a tour runs: ContinuousMove (with a 2 s Timeout, so the camera
stops by itself if Sentinela goes away; resent while the button is held and
stopped within a second of release) and Stop; GotoPreset; SetPreset (saved
as "Preset N", or over the same preset) and RemovePreset, from the *Saved
positions* card (admins only; the server refuses an operator). Nothing else is written to the camera: no image,
network, user or firmware setting. Focus and iris are not sent over ONVIF.

## Presets

| `vendor` | Main stream | Light stream |
|---|---|---|
| `generic` | your `rtsp_main` | your `rtsp_sub` (optional) |
| `hikvision` (also many OEMs: HiLook, Annke, EZVIZ) | `/Streaming/Channels/{channel}01` | `/Streaming/Channels/{channel}02` |
| `dahua` (also Amcrest, Imou, Lorex OEMs) | `/cam/realmonitor?channel={channel}&subtype=0` | `…&subtype=1` |
| `reolink` | `/Preview_{channel2}_main` | `/Preview_{channel2}_sub` |
| `tapo` (TP-Link Tapo and VIGI; use the camera account from the Tapo app) | `/stream1` | `/stream2` |
| `uniview` | `/unicast/c{channel}/s0/live` | `/unicast/c{channel}/s1/live` |
| `axis` | `/axis-media/media.amp?camera={channel}` | `…&resolution=640x360` |
| `xmeye` (Xiongmai modules, many no-name cameras) | XMEye path with credentials | sub stream |

`channel` selects the camera on an NVR or DVR (default 1); `{channel2}` is
the same number with two digits (`01`).

## Common stream paths for `generic`

Prefer ONVIF discovery; these are the usual defaults and vary by model and
firmware:

| Brand | Main | Light |
|---|---|---|
| Reolink, older H.264-only firmware | `/h264Preview_01_main` | `/h264Preview_01_sub` |
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
