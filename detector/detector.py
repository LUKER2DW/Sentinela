"""Sentinela object detector (ADR-0016).

Motion-gated: the camera's own motion events (ONVIF, already in Sentinela)
wake it up. While a camera has no motion the substream is only demuxed
(no decoding); when motion starts, each keyframe (one every ~2 s) is
decoded and run through YOLO26n until 10 s after the motion ends. People,
vehicles and animals whose feet/base are inside a zone that watches that
kind of object become events; a tree moving or a car on the street outside
the zones does not.

Model: Ultralytics YOLO26n exported to ONNX at 416x416 (AGPL-3.0 weights,
downloaded and exported when the image is built, never committed; see
ADR-0016). Inference is plain ONNX Runtime, one thread; the output is the
standard (1, 4 + 80, N) head: centre x/y, width, height and 80 COCO class
scores, filtered here with a greedy NMS. Docker caps the process at 15 %
of one core and 192 MB; recording never depends on it (AGENTS.md §2.2).
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

SIZE = 416
# COCO ids grouped into the kinds Sentinela knows (event types in brackets).
KINDS = {
    "person": (0,),  # [human]
    "vehicle": (1, 2, 3, 5, 7),  # bicycle, car, motorcycle, bus, truck [vehicle]
    "animal": (15, 16),  # cat, dog [animal]
}
KIND_OF = {c: k for k, ids in KINDS.items() for c in ids}
LINGER = 10.0  # keep analysing this long after motion stops

log = logging.getLogger("detector")


# ---------- model ----------


def preprocess(rgb):
    """Pad an HxWx3 uint8 RGB image (already resized by swscale so it fits)
    into the 1x3x416x416 float input, scaled to 0..1. Boxes come back in
    the resized image's pixels (padding is at the bottom/right)."""
    h, w = rgb.shape[:2]
    if h > SIZE or w > SIZE:
        raise ValueError("resize before preprocess")
    pad = np.full((SIZE, SIZE, 3), 114, dtype=np.uint8)
    pad[:h, :w] = rgb
    x = pad.astype(np.float32) / 255.0
    return np.ascontiguousarray(x.transpose(2, 0, 1)[None])


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
        self.sess = ort.InferenceSession(path, o, providers=["CPUExecutionProvider"])
        self.name = self.sess.get_inputs()[0].name
        self.lock = threading.Lock()
        self.last_ms = 0.0
        self.runs = 0

    def objects(self, rgb, min_score):
        """Objects with boxes normalised to the image (0..1)."""
        h, w = rgb.shape[:2]
        x = preprocess(rgb)
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


def zone_hits(obj, zones):
    """Names of the zones that count this object: its base (the feet of a
    person, the wheels of a car) must be inside, and the zone must watch
    that kind. No zones = the whole picture watches people only."""
    kind, x, y, w, h = obj[:5]
    if not zones:
        return ["*"] if kind == "person" else []
    base = (x + w / 2, min(y + h, 0.999))
    return [z.get("name", "") for z in zones if kind in (z.get("objects") or ["person"]) and inside(base, z["points"])]


class Tracker:
    """Debounces detections of one kind into an event: start after `hits`
    positive frames among the last `window`; stop after `quiet` seconds
    without one."""

    def __init__(self, hits=1, window=2, quiet=12.0):
        self.hits, self.window, self.quiet = hits, window, quiet
        self.recent = []
        self.active = False
        self.last_seen = 0.0

    def update(self, seen, now):
        """Returns "start", "stop" or None."""
        self.recent = (self.recent + [seen])[-self.window :]
        if seen:
            self.last_seen = now
        if not self.active and sum(self.recent) >= self.hits:
            self.active = True
            return "start"
        if self.active and now - self.last_seen >= self.quiet:
            self.active = False
            self.recent = []
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


class Camera(threading.Thread):
    def __init__(self, cam, model, api, shared):
        super().__init__(daemon=True, name=cam["id"])
        self.cid, self.model, self.api, self.shared = cam["id"], model, api, shared
        self.trackers = {k: Tracker() for k in KINDS}
        self.state = {"connected": False, "analysing": False, "last_analysis": None, "objects": {}}
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
            last_key = None
            for pkt in c.demux(st):
                if self.stop.is_set():
                    return
                if not pkt.is_keyframe:
                    continue
                now = time.time()
                motion_until = self.shared["motion"].get(self.cid, 0.0)
                analysing = now <= motion_until + LINGER
                self.state["analysing"] = analysing
                if not analysing:
                    last_key = pkt  # idle: demux only, keep the newest keyframe
                    self.close_all()
                    continue
                if last_key is not None:  # motion just started: the keyframe before it too
                    self.analyse(cc, last_key, now)
                    last_key = None
                self.analyse(cc, pkt, now)

    def analyse(self, cc, pkt, now):
        frames = cc.decode(pkt)
        if not frames:
            return
        frame = frames[-1]
        r = min(SIZE / frame.width, SIZE / frame.height)
        small = frame.reformat(width=int(frame.width * r) // 2 * 2, height=int(frame.height * r) // 2 * 2, format="rgb24")
        cfg = self.cfg()
        objs = self.model.objects(small.to_ndarray(), cfg.get("min_score", 0.45))
        zones = cfg.get("zones") or []
        counted = {k: [] for k in KINDS}
        for o in objs:
            names = zone_hits(o, zones)
            if names:
                counted[o[0]].append((o, names))
        self.state.update(last_analysis=now, objects={k: len(v) for k, v in counted.items() if v})
        for kind, hits in counted.items():
            change = self.trackers[kind].update(bool(hits), now)
            if change:
                self.report(kind, change, frame if change == "start" else None, hits)

    def close_all(self):
        for kind, t in self.trackers.items():
            if t.active:
                t.active, t.recent = False, []
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
    shared = {"cams": {}, "motion": {}}
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
            if now >= next_cfg:
                next_cfg = now + 30
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
                    "inferences": model.runs,
                    "cameras": {cid: t.state for cid, t in threads.items()},
                })
        except Exception as e:
            log.warning("api: %s", e)
        time.sleep(1)


if __name__ == "__main__":
    main()
