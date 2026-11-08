"""Sentinela object detector (ADR-0016).

Motion-gated: the camera's own motion events (ONVIF, already in Sentinela)
wake it up. While a camera has no motion the substream is only demuxed
(no decoding); when motion starts, each keyframe (one every ~2 s) is
decoded and run through YOLO26n until 10 s after the motion ends.

Cameras that report no motion (RTSP only, or ONVIF events not flowing;
the server says which, "video_motion") get it from the video instead: each
keyframe is decoded, shrunk to a 64x36 grey picture and compared with a slow
background (MotionGate). That motion opens "motion" events of its own and
wakes the object detection the same way. It costs ~2.3 % of one core per
camera, measured on the minimum host (ADR-0016, 2026-11-08). People,
vehicles and animals whose feet/base are inside a zone that watches that
kind of object become events; a tree moving or a car on the street outside
the zones does not. With zones, only the part of the picture around them
is analysed, enlarged; objects that keep the same place (a parked car)
stop counting after a while.

Model: Ultralytics YOLO26n exported to ONNX at 640x352, the substream's
aspect, so no input is wasted on padding (AGPL-3.0 weights, downloaded and
exported when the image is built, never committed; see ADR-0016). The
input size is read from the model. Inference is plain ONNX Runtime, one
thread; the output is the standard (1, 4 + 80, N) head: centre x/y, width,
height and 80 COCO class scores, filtered here with a greedy NMS. Docker
caps the process's CPU and memory; recording never depends on it
(AGENTS.md §2.2).
"""

import base64
import fractions
import json
import logging
import os
import threading
import time
import urllib.request

import numpy as np

# COCO ids grouped into the kinds Sentinela knows (event types in brackets).
KINDS = {
    "person": (0,),  # [human]
    "vehicle": (1, 2, 3, 5, 7),  # bicycle, car, motorcycle, bus, truck [vehicle]
    "animal": (15, 16),  # cat, dog [animal]
}
KIND_OF = {c: k for k, ids in KINDS.items() for c in ids}
LINGER = 10.0  # keep analysing this long after motion stops
# An object seen in the same place this long stops counting: a parked car,
# the house dog asleep in its corner, a coat that looks like a person.
DWELL = {"person": 300.0, "vehicle": 30.0, "animal": 60.0}
HEADROOM = 0.4  # picture height kept above the zones: bodies stand over their feet

log = logging.getLogger("detector")


# ---------- model ----------


def preprocess(rgb, size):
    """Pad an hxwx3 uint8 RGB image (already resized by swscale so it fits)
    into the 1x3xHxW float input, scaled to 0..1. Boxes come back in the
    resized image's pixels (padding is at the bottom/right)."""
    H, W = size
    h, w = rgb.shape[:2]
    if h > H or w > W:
        raise ValueError("resize before preprocess")
    x = np.full((1, 3, H, W), 114 / 255, dtype=np.float32)
    x[0, :, :h, :w] = rgb.transpose(2, 0, 1)
    x[0, :, :h, :w] *= 1 / 255
    return x


def fit(w, h, size):
    """Largest even size with w:h's aspect that fits the model input."""
    H, W = size
    r = min(W / w, H / h)
    return max(2, int(w * r) // 2 * 2), max(2, int(h * r) // 2 * 2)


def postprocess(out, min_score=0.4, iou=0.45):
    """(1, 84, N) output -> [(kind, x, y, w, h, score)] in input pixels.
    Only the classes in KINDS are kept; NMS runs per kind."""
    p = out[0].T  # N x 84
    scores = p[:, 4:]
    cls = scores.argmax(1)
    best = scores[np.arange(len(p)), cls]
    keep = best >= min_score
    keep &= np.isin(cls, list(KIND_OF))
    if not keep.any():
        return []
    p, cls, best = p[keep], cls[keep], best[keep]
    boxes = np.concatenate([p[:, :2] - p[:, 2:4] / 2, p[:, 2:4]], 1)
    kinds = np.array([KIND_OF[int(c)] for c in cls])
    found = []
    for kind in KINDS:
        m = kinds == kind
        if not m.any():
            continue
        b, s = boxes[m], best[m]
        for i in nms(b, s, iou):
            found.append((kind,) + tuple(float(v) for v in b[i]) + (float(s[i]),))
    return found


def nms(boxes, scores, iou):
    """Greedy non-maximum suppression on (x, y, w, h) boxes."""
    order = scores.argsort()[::-1]
    x1, y1 = boxes[:, 0], boxes[:, 1]
    x2, y2 = x1 + boxes[:, 2], y1 + boxes[:, 3]
    area = boxes[:, 2] * boxes[:, 3]
    keep = []
    while order.size:
        i = order[0]
        keep.append(int(i))
        xx1 = np.maximum(x1[i], x1[order[1:]])
        yy1 = np.maximum(y1[i], y1[order[1:]])
        xx2 = np.minimum(x2[i], x2[order[1:]])
        yy2 = np.minimum(y2[i], y2[order[1:]])
        inter = np.clip(xx2 - xx1, 0, None) * np.clip(yy2 - yy1, 0, None)
        ratio = inter / (area[i] + area[order[1:]] - inter + 1e-9)
        order = order[1:][ratio <= iou]
    return keep


class Model:
    """One ONNX Runtime session, one thread, shared by all cameras."""

    def __init__(self, path):
        import onnxruntime as ort

        o = ort.SessionOptions()
        o.intra_op_num_threads = 1
        o.inter_op_num_threads = 1
        o.graph_optimization_level = ort.GraphOptimizationLevel.ORT_ENABLE_ALL
        # The arena keeps its peak forever; without it RSS returns to idle
        # after each analysis at no measurable cost for one small model.
        o.enable_cpu_mem_arena = False
        self.sess = ort.InferenceSession(path, o, providers=["CPUExecutionProvider"])
        inp = self.sess.get_inputs()[0]
        self.name = inp.name
        self.size = tuple(int(v) for v in inp.shape[2:])  # (H, W)
        self.lock = threading.Lock()
        self.last_ms = 0.0
        self.runs = 0

    def objects(self, rgb, min_score):
        """Objects with boxes normalised to the image (0..1)."""
        h, w = rgb.shape[:2]
        x = preprocess(rgb, self.size)
        with self.lock:
            t = time.process_time()
            out = self.sess.run(None, {self.name: x})[0]
            self.last_ms = (time.process_time() - t) * 1000
            self.runs += 1
        return [(k, bx / w, by / h, bw / w, bh / h, s) for k, bx, by, bw, bh, s in postprocess(out, min_score)]


# ---------- zones ----------


def inside(pt, poly):
    """Ray-casting point-in-polygon; poly is [[x, y], ...] normalised."""
    x, y = pt
    n, ok = len(poly), False
    j = n - 1
    for i in range(n):
        xi, yi = poly[i]
        xj, yj = poly[j]
        if (yi > y) != (yj > y) and x < (xj - xi) * (y - yi) / (yj - yi) + xi:
            ok = not ok
        j = i
    return ok


def zone_hits(obj, zones, watch=("person",)):
    """Names of the zones that count this object: its base (the feet of a
    person, the wheels of a car) must be inside, and the zone must watch
    that kind. No zones = the whole picture watches the kinds in `watch`
    (people unless the user chose more)."""
    kind, x, y, w, h = obj[:5]
    if not zones:
        return ["*"] if kind in watch else []
    base = (x + w / 2, min(y + h, 0.999))
    return [z.get("name", "") for z in zones if kind in (z.get("objects") or ["person"]) and inside(base, z["points"])]


def roi(zones, fw, fh, size):
    """Pixels (x0, y0, x1, y1) of the frame worth analysing, or None for
    the whole picture. The zones' bounding box, with room above them for
    the bodies standing on them, grown to the model's aspect (nothing is
    padding) and to at least half the model input (at most 2x enlarged).
    Not worth it when that is most of the picture anyway."""
    if not zones:
        return None
    H, W = size
    xs = [p[0] for z in zones for p in z["points"]]
    ys = [p[1] for z in zones for p in z["points"]]
    x0, x1 = max(0.0, min(xs) - 0.05) * fw, min(1.0, max(xs) + 0.05) * fw
    y0, y1 = max(0.0, min(ys) - HEADROOM) * fh, min(1.0, max(ys) + 0.02) * fh
    cw, ch = max(x1 - x0, W / 2), max(y1 - y0, H / 2)
    if cw / ch < W / H:
        cw = ch * W / H
    else:
        ch = cw * H / W
    cw, ch = min(cw, fw), min(ch, fh)
    if cw * ch >= 0.6 * fw * fh:
        return None
    cx, cy = (x0 + x1) / 2, (y0 + y1) / 2
    left = int(min(max(0.0, cx - cw / 2), fw - cw))
    top = int(min(max(0.0, cy - ch / 2), fh - ch))
    return left, top, left + int(cw), top + int(ch)


def thresholds(min_score):
    """(low, strong) around the sensitivity the user picked: one frame at
    `strong` starts an event, two at `low` do too, and `low` keeps it."""
    return round(max(0.25, min_score - 0.1), 2), round(min(0.9, min_score + 0.15), 2)


def iou(a, b):
    """Intersection over union of two (x, y, w, h) boxes."""
    ix = max(0.0, min(a[0] + a[2], b[0] + b[2]) - max(a[0], b[0]))
    iy = max(0.0, min(a[1] + a[3], b[1] + b[3]) - max(a[1], b[1]))
    inter = ix * iy
    return inter / (a[2] * a[3] + b[2] * b[3] - inter + 1e-9)


class Parked:
    """Objects of one kind that keep the same box. A box seen for `dwell`
    seconds is stationary and stops counting. The first box is kept (not
    the latest), so something creeping slowly does not stay matched. An
    entry is forgotten after `misses` analyses in a row without it (only
    analyses count: a parked car is remembered through a quiet night)."""

    def __init__(self, dwell, match=0.6, misses=8):
        self.dwell, self.match, self.misses = dwell, match, misses
        self.seen = []  # [box, first_seen, missed]

    def update(self, boxes, now):
        """One flag per box: True = stationary."""
        flags, used = [], set()
        for b in boxes:
            best, bi = 0.0, None
            for i, e in enumerate(self.seen):
                if i not in used and (v := iou(b, e[0])) > best:
                    best, bi = v, i
            if bi is not None and best >= self.match:
                e = self.seen[bi]
                e[2] = 0
                flags.append(now - e[1] >= self.dwell)
            else:
                self.seen.append([b, now, 0])
                bi = len(self.seen) - 1
                flags.append(False)
            used.add(bi)
        for i, e in enumerate(self.seen):
            if i not in used:
                e[2] += 1
        self.seen = [e for e in self.seen if e[2] < self.misses]
        return flags

    def learn(self, boxes, now):
        """Whatever is there when the detector starts was already there."""
        self.seen += [[b, now - self.dwell, 0] for b in boxes]


class Tracker:
    """Debounces detections of one kind into an event: start on one frame
    at `strong`, or `hits` frames at `low` among the last `window`; stay
    active while frames at `low` keep coming; stop after `quiet` seconds
    without one."""

    def __init__(self, hits=2, window=3, quiet=12.0):
        self.hits, self.window, self.quiet = hits, window, quiet
        self.recent = []
        self.active = False
        self.last_seen = 0.0

    def update(self, score, low, strong, now):
        """score = best of this kind in the frame (0 = none). Returns
        "start", "stop" or None."""
        seen = score >= low
        self.recent = (self.recent + [seen])[-self.window :]
        if seen:
            self.last_seen = now
        if not self.active and (score >= strong or sum(self.recent) >= self.hits):
            self.active = True
            return "start"
        if self.active and now - self.last_seen >= self.quiet:
            self.active = False
            self.recent = []
            return "stop"
        return None


# ---------- motion from the video (cameras that report none) ----------

MOTION_GRID = (64, 36)  # width, height of the picture compared for motion


def small_gray(frame):
    """The keyframe as a MOTION_GRID grey picture with its mean brightness
    removed (a cloud or the evening light shifts every pixel alike)."""
    w, h = MOTION_GRID
    g = frame.reformat(width=w, height=h, format="gray", interpolation="AREA").to_ndarray().astype(np.float32)
    return g - g.mean()


def motion_mask(zones, w=MOTION_GRID[0], h=MOTION_GRID[1]):
    """Cells of the motion grid that matter: the zones' bounding box, with
    room above for the bodies standing on them (as roi), or None (all)."""
    if not zones:
        return None
    xs = [p[0] for z in zones for p in z["points"]]
    ys = [p[1] for z in zones for p in z["points"]]
    x0, x1 = max(0.0, min(xs) - 0.05), min(1.0, max(xs) + 0.05)
    y0, y1 = max(0.0, min(ys) - HEADROOM), min(1.0, max(ys) + 0.02)
    m = np.zeros((h, w), dtype=bool)
    m[int(y0 * h) : max(int(y0 * h) + 1, int(np.ceil(y1 * h))), int(x0 * w) : max(int(x0 * w) + 1, int(np.ceil(x1 * w)))] = True
    return m


class MotionGate:
    """Motion from keyframes, compared with a slowly updated background.

    A pixel changed by more than `pixel` grey levels counts. Motion starts
    when `start` of the watched area changed in one keyframe, or `low` in
    `hits` of the last `window`; it stops after `calm` keyframes below
    `end`. A change in `flood` of the whole picture is light (the IR
    switching on, a cloud), not motion: the background is reset instead.
    Keyframes are ~2 s apart, so a person walking changes plenty."""

    def __init__(self, pixel=18.0, start=0.03, low=0.01, hits=2, window=3, end=0.005, calm=3, rate=0.15, flood=0.5):
        self.pixel, self.start, self.low, self.hits, self.window = pixel, start, low, hits, window
        self.end, self.calm, self.rate, self.flood = end, calm, rate, flood
        self.bg = None
        self.recent = []
        self.active = False
        self.quiet_frames = 0
        self.last_share = 0.0

    def reset(self):
        """Forget the background (the camera turned, the stream restarted)."""
        self.bg, self.recent = None, []

    def update(self, gray, mask=None):
        """gray: float32 picture of MOTION_GRID with its mean removed.
        Returns "start", "stop" or None."""
        if self.bg is None or self.bg.shape != gray.shape:
            self.bg = gray.copy()
            return None
        changed = np.abs(gray - self.bg) > self.pixel
        whole = float(changed.mean())
        area = float(changed[mask].mean()) if mask is not None and mask.any() else whole
        self.last_share = area
        if whole >= self.flood:
            self.bg, self.recent = gray.copy(), []
            return None
        self.bg += self.rate * (gray - self.bg)
        self.recent = (self.recent + [area >= self.low])[-self.window :]
        if not self.active:
            if area >= self.start or sum(self.recent) >= self.hits:
                self.active, self.quiet_frames = True, 0
                return "start"
            return None
        self.quiet_frames = self.quiet_frames + 1 if area < self.end else 0
        if self.quiet_frames >= self.calm:
            self.active, self.recent = False, []
            return "stop"
        return None


# ---------- service ----------


class API:
    """Loopback client for the Sentinela internal detector endpoints."""

    def __init__(self, base, token_file):
        self.base, self.token_file = base.rstrip("/"), token_file

    def _req(self, method, path, body=None):
        with open(self.token_file) as f:
            token = f.read().strip()
        data = None if body is None else json.dumps(body).encode()
        req = urllib.request.Request(self.base + path, data=data, method=method)
        req.add_header("Authorization", "Bearer " + token)
        if data is not None:
            req.add_header("Content-Type", "application/json")
        with urllib.request.urlopen(req, timeout=10) as r:
            raw = r.read()
            return json.loads(raw) if raw else None

    def config(self):
        return self._req("GET", "/internal/detector/config")

    def motion(self):
        return self._req("GET", "/internal/detector/motion")

    def event(self, body):
        return self._req("POST", "/internal/detector/event", body)

    def status(self, body):
        return self._req("POST", "/internal/detector/status", body)

    def objects(self, body):
        return self._req("POST", "/internal/detector/objects", body)


def encode_jpeg(frame):
    """JPEG of a PyAV frame (the event snapshot), ~50 KB."""
    import av

    cc = av.CodecContext.create("mjpeg", "w")
    cc.width, cc.height = frame.width, frame.height
    cc.pix_fmt = "yuvj420p"
    cc.time_base = fractions.Fraction(1, 25)
    cc.options = {"q": "5"}
    pkts = cc.encode(frame.reformat(format="yuvj420p")) + cc.encode(None)
    return b"".join(bytes(p) for p in pkts)


class Keyframe:
    """A keyframe packet, decoded at most once and only when someone looks."""

    def __init__(self, cc, pkt):
        self.cc, self.pkt, self._frame, self._done = cc, pkt, None, False

    def frame(self):
        if not self._done:
            self._done = True
            frames = self.cc.decode(self.pkt)
            self._frame = frames[-1] if frames else None
        return self._frame


class Camera(threading.Thread):
    def __init__(self, cam, model, api, shared):
        super().__init__(daemon=True, name=cam["id"])
        self.cid, self.model, self.api, self.shared = cam["id"], model, api, shared
        self.gate = MotionGate()
        self.trackers = {k: Tracker() for k in KINDS}
        self.parked = {k: Parked(DWELL[k]) for k in KINDS}
        self.best = {k: [] for k in KINDS}  # recent (score, frame, hits) for the snapshot
        self.learned = False
        self.shown = False  # the overlay has boxes on screen
        self.state = {"connected": False, "analysing": False, "last_analysis": None, "objects": {}, "parked": 0, "cropped": False}
        self.stop = threading.Event()

    def cfg(self):
        return self.shared["cams"].get(self.cid, {})

    def run(self):
        backoff = 5
        while not self.stop.is_set():
            try:
                self.loop()
                backoff = 5
            except Exception as e:  # reconnect on any stream error
                log.warning("camera %s: %s", self.cid, e)
            self.state["connected"] = self.state["analysing"] = False
            self.close_all()
            self.end_motion()
            self.stop.wait(backoff)
            backoff = min(backoff * 2, 120)

    def loop(self):
        import av

        with av.open(self.cfg()["stream"], options={"rtsp_transport": "tcp", "timeout": "10000000"}) as c:
            st = c.streams.video[0]
            cc = st.codec_context
            cc.skip_frame = "NONKEY"
            cc.thread_type = "NONE"
            self.state["connected"] = True
            log.info("camera %s: connected", self.cid)
            self.gate.reset()
            last_key = None
            for pkt in c.demux(st):
                if self.stop.is_set():
                    return
                if not pkt.is_keyframe:
                    continue
                now = time.time()
                cfg = self.cfg()
                key = Keyframe(cc, pkt)
                self.state["video_motion"] = bool(cfg.get("video_motion"))
                if cfg.get("video_motion"):
                    self.watch_motion(key, now, cfg)
                else:
                    self.end_motion()  # the camera's own events are back
                if not cfg.get("detect", True):
                    self.state["analysing"] = False
                    continue
                if not self.learned:  # once: what is already standing there
                    self.learn(key, now)
                motion_until = self.shared["motion"].get(self.cid, 0.0)
                analysing = now <= motion_until + LINGER
                # Someone is drawing zones: analyse to show the boxes, but
                # only real motion may open events.
                probing = now <= self.shared["probe"].get(self.cid, 0.0) + 2
                self.state["analysing"] = analysing or probing
                if not analysing:
                    last_key = key  # idle: keep the newest keyframe
                    self.close_all()
                    if probing:
                        self.analyse(key, now, events=False)
                    elif self.shown:
                        self.publish(now, [])
                    continue
                if last_key is not None:  # motion just started: the keyframe before it too
                    self.analyse(last_key, now)
                    last_key = None
                self.analyse(key, now)

    def watch_motion(self, key, now, cfg):
        """Motion from the video for a camera that reports none: opens and
        closes "motion" events and wakes the object detection."""
        frame = key.frame()
        if frame is None:
            return
        if now <= self.shared["quiet"].get(self.cid, 0.0) + 2:
            self.gate.reset()  # the camera is turning (PTZ): its own movement is not motion
            return
        change = self.gate.update(small_gray(frame), motion_mask(cfg.get("zones") or []))
        self.state["motion_share"] = round(self.gate.last_share, 4)
        if self.gate.active:
            self.shared["motion"][self.cid] = now
        if change:
            self.report("motion", change, frame if change == "start" else None, [])

    def end_motion(self):
        """Close a video motion event (stream lost, or the camera's own
        events took over again)."""
        if self.gate.active:
            self.gate.active = False
            self.report("motion", "stop", None, [])
        self.gate.reset()

    def detect(self, frame, zones, min_score):
        """Objects in the frame, boxes normalised to the whole picture.
        With zones, only the region around them is analysed (enlarged)."""
        import av

        fw, fh = frame.width, frame.height
        box = roi(zones, fw, fh, self.model.size)
        self.state["cropped"] = box is not None
        if box is None:
            x0 = y0 = 0
            cw, ch = fw, fh
            tw, th = fit(cw, ch, self.model.size)
            rgb = frame.reformat(width=tw, height=th, format="rgb24").to_ndarray()
        else:
            x0, y0, x1, y1 = box
            cw, ch = x1 - x0, y1 - y0
            tw, th = fit(cw, ch, self.model.size)
            crop = np.ascontiguousarray(frame.to_ndarray(format="rgb24")[y0:y1, x0:x1])
            rgb = av.VideoFrame.from_ndarray(crop, format="rgb24").reformat(width=tw, height=th).to_ndarray()
        objs = self.model.objects(rgb, min_score)
        return [(k, (x0 + x * cw) / fw, (y0 + y * ch) / fh, w * cw / fw, h * ch / fh, s) for k, x, y, w, h, s in objs]

    def learn(self, key, now):
        """Analyse the first keyframe after connecting: everything already
        standing still there (a parked car) is not news."""
        frame = key.frame()
        if frame is None:
            return
        self.learned = True
        cfg = self.cfg()
        low, _ = thresholds(cfg.get("min_score", 0.45))
        objs = self.detect(frame, cfg.get("zones") or [], low)
        for kind in KINDS:
            self.parked[kind].learn([o[1:5] for o in objs if o[0] == kind], now)
        self.state["parked"] = sum(len(p.seen) for p in self.parked.values())

    def analyse(self, key, now, events=True):
        frame = key.frame()
        if frame is None:
            return
        cfg = self.cfg()
        low, strong = thresholds(cfg.get("min_score", 0.45))
        zones = cfg.get("zones") or []
        watch = tuple(cfg.get("objects") or ("person",))
        min_size = cfg.get("min_size", 0.0)
        ignore_parked = cfg.get("ignore_parked", True)
        objs = [o for o in self.detect(frame, zones, low) if o[4] >= min_size]
        counted = {k: [] for k in KINDS}
        seen = []  # everything, for the overlay
        for kind in KINDS:
            mine = [o for o in objs if o[0] == kind]
            for o, still in zip(mine, self.parked[kind].update([o[1:5] for o in mine], now)):
                names = zone_hits(o, zones, watch)
                counts = bool(names) and not (still and ignore_parked)
                if counts:
                    counted[kind].append((o, names))
                seen.append((o, names, still, counts))
        self.publish(now, seen)
        if not events:
            return
        self.state.update(
            last_analysis=now,
            objects={k: len(v) for k, v in counted.items() if v},
            parked=sum(len(p.seen) for p in self.parked.values()),
        )
        for kind, hits in counted.items():
            score = max((o[5] for o, _ in hits), default=0.0)
            if score >= low:  # the best of the last few frames becomes the snapshot
                self.best[kind] = (self.best[kind] + [(score, frame, hits)])[-self.trackers[kind].window :]
            change = self.trackers[kind].update(score, low, strong, now)
            if change == "start":
                _, best_frame, best_hits = max(self.best[kind], key=lambda b: b[0])
                self.report(kind, change, best_frame, best_hits)
            elif change == "stop":
                self.report(kind, change, None, [])
            if change:
                self.best[kind] = []

    def publish(self, now, seen):
        """Boxes of this analysis for the live overlay (best effort)."""
        body = {
            "camera": self.cid,
            "at": round(now, 2),
            "objects": [
                {
                    "kind": o[0],
                    "x": round(o[1], 4),
                    "y": round(o[2], 4),
                    "w": round(o[3], 4),
                    "h": round(o[4], 4),
                    "score": round(o[5], 3),
                    "zones": [n for n in names if n != "*"],
                    "parked": still,
                    "counted": counts,
                }
                for o, names, still, counts in seen
            ],
        }
        self.shown = bool(seen)
        try:
            self.api.objects(body)
        except Exception as e:
            log.debug("camera %s: objects: %s", self.cid, e)

    def close_all(self):
        """Motion is over (or the stream dropped): end open events and
        forget weak frames, so they never add up with the next motion's."""
        for kind, t in self.trackers.items():
            self.best[kind], t.recent = [], []
            if t.active:
                t.active = False
                self.report(kind, "stop", None, [])

    def report(self, kind, change, frame, hits):
        body = {
            "camera": self.cid,
            "kind": kind,
            "active": change == "start",
            "objects": [
                {"x": round(o[1], 4), "y": round(o[2], 4), "w": round(o[3], 4), "h": round(o[4], 4), "score": round(o[5], 3), "zones": names}
                for o, names in hits
            ],
        }
        if frame is not None:
            try:
                body["jpeg"] = base64.b64encode(encode_jpeg(frame)).decode()
            except Exception as e:
                log.warning("snapshot: %s", e)
        try:
            self.api.event(body)
            log.info("camera %s: %s %s (%d)", self.cid, kind, change, len(hits))
        except Exception as e:
            log.warning("camera %s: report %s %s: %s", self.cid, kind, change, e)


def main():
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
    api = API(os.environ.get("SENTINELA_API", "http://127.0.0.1:8080"), os.environ.get("TOKEN_FILE", "/data/detector/token"))
    model = Model(os.environ.get("MODEL", "/opt/detector/model.onnx"))
    shared = {"cams": {}, "motion": {}, "probe": {}, "quiet": {}}
    threads = {}
    started = time.time()
    next_cfg = 0.0
    while True:
        now = time.time()
        try:
            # Motion state every second: cheap, and it bounds the wake-up delay.
            m = api.motion() or {}
            for cid, active in m.get("motion", {}).items():
                if active:
                    shared["motion"][cid] = now
            for cid, active in (m.get("probe") or {}).items():
                if active:
                    shared["probe"][cid] = now
            for cid, active in (m.get("quiet") or {}).items():  # PTZ moving the camera
                if active:
                    shared["quiet"][cid] = now
            if now >= next_cfg:
                next_cfg = now + 10  # settings saved in the app apply within 10 s
                cfg = api.config()
                shared["cams"] = {c["id"]: c for c in cfg.get("cameras", [])}
                for cid in shared["cams"]:
                    if cid not in threads or not threads[cid].is_alive():
                        threads[cid] = Camera(shared["cams"][cid], model, api, shared)
                        threads[cid].start()
                for cid in list(threads):
                    if cid not in shared["cams"]:
                        threads.pop(cid).stop.set()
                api.status({
                    "uptime_seconds": int(now - started),
                    "cpu_seconds": round(time.process_time(), 1),
                    "inference_ms": round(model.last_ms, 1),
                    "model": "YOLO26n %dx%d" % model.size[::-1],
                    "inferences": model.runs,
                    "cameras": {cid: t.state for cid, t in threads.items()},
                })
        except Exception as e:
            log.warning("api: %s", e)
        time.sleep(1)


if __name__ == "__main__":
    main()
