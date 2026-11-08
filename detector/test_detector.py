import unittest

import numpy as np

import detector as d


def head(rows):
    """Build a (1, 84, N) output from [(cx, cy, w, h, class_id, score)]."""
    out = np.zeros((1, 84, len(rows)), dtype=np.float32)
    for i, (cx, cy, w, h, c, s) in enumerate(rows):
        out[0, :4, i] = [cx, cy, w, h]
        out[0, 4 + c, i] = s
    return out


class Model(unittest.TestCase):
    def test_preprocess_pads_and_scales(self):
        rgb = np.full((10, 20, 3), 255, dtype=np.uint8)
        x = d.preprocess(rgb, (352, 640))
        self.assertEqual(x.shape, (1, 3, 352, 640))
        self.assertEqual(x.dtype, np.float32)
        self.assertAlmostEqual(float(x[0, 0, 0, 0]), 1.0)
        self.assertAlmostEqual(float(x[0, 0, 100, 100]), 114 / 255, places=5)
        with self.assertRaises(ValueError):
            d.preprocess(np.zeros((400, 10, 3), dtype=np.uint8), (352, 640))

    def test_fit_keeps_aspect_even(self):
        self.assertEqual(d.fit(800, 448, (352, 640)), (628, 352))
        self.assertEqual(d.fit(200, 100, (352, 640)), (640, 320))  # enlarged

    def test_postprocess_kinds_threshold_and_nms(self):
        out = head([
            (50, 50, 20, 40, 0, 0.9),  # person
            (52, 51, 20, 40, 0, 0.8),  # same person, suppressed
            (200, 200, 60, 30, 2, 0.7),  # car -> vehicle
            (300, 300, 10, 10, 16, 0.6),  # dog -> animal
            (100, 300, 10, 10, 13, 0.9),  # bench: not a kind we report
            (10, 10, 5, 5, 0, 0.2),  # below threshold
        ])
        found = sorted(d.postprocess(out, 0.4), key=lambda o: o[0])
        self.assertEqual([o[0] for o in found], ["animal", "person", "vehicle"])
        person = [o for o in found if o[0] == "person"][0]
        self.assertEqual(person[1:5], (40.0, 30.0, 20.0, 40.0))
        self.assertAlmostEqual(person[5], 0.9, places=5)
        self.assertEqual(d.postprocess(head([(1, 1, 1, 1, 13, 0.9)]), 0.4), [])

    def test_nms(self):
        boxes = np.array([[0, 0, 10, 10], [1, 1, 10, 10], [50, 50, 10, 10]], dtype=np.float32)
        self.assertEqual(d.nms(boxes, np.array([0.9, 0.8, 0.7]), 0.45), [0, 2])


class Zones(unittest.TestCase):
    gate = {"name": "Portão", "points": [[0.2, 0.5], [0.8, 0.5], [0.8, 1.0], [0.2, 1.0]]}
    drive = {"name": "Garagem", "points": [[0.2, 0.5], [0.8, 0.5], [0.8, 1.0], [0.2, 1.0]], "objects": ["vehicle", "person"]}

    def test_inside(self):
        pts = self.gate["points"]
        self.assertTrue(d.inside((0.5, 0.7), pts))
        self.assertFalse(d.inside((0.1, 0.7), pts))
        self.assertFalse(d.inside((0.5, 0.4), pts))

    def test_base_decides(self):
        # Head outside the zone, feet inside: counts.
        self.assertEqual(d.zone_hits(("person", 0.45, 0.3, 0.1, 0.4, 0.9), [self.gate]), ["Portão"])
        # Feet above the zone (a person on the street behind it): no.
        self.assertEqual(d.zone_hits(("person", 0.45, 0.1, 0.1, 0.2, 0.9), [self.gate]), [])

    def test_kinds_per_zone(self):
        car = ("vehicle", 0.3, 0.6, 0.3, 0.2, 0.8)
        self.assertEqual(d.zone_hits(car, [self.gate]), [])  # zones watch people by default
        self.assertEqual(d.zone_hits(car, [self.gate, self.drive]), ["Garagem"])

    def test_no_zones_watch_people_everywhere(self):
        self.assertEqual(d.zone_hits(("person", 0, 0, 0.1, 0.1, 0.9), []), ["*"])
        self.assertEqual(d.zone_hits(("vehicle", 0, 0, 0.1, 0.1, 0.9), []), [])

    def test_no_zones_watch_chosen_kinds(self):
        car = ("vehicle", 0, 0, 0.1, 0.1, 0.9)
        self.assertEqual(d.zone_hits(car, [], ("person", "vehicle")), ["*"])
        self.assertEqual(d.zone_hits(("person", 0, 0, 0.1, 0.1, 0.9), [], ("vehicle",)), [])


class Region(unittest.TestCase):
    size = (352, 640)

    def test_no_zones_or_big_zones_use_whole_picture(self):
        self.assertIsNone(d.roi([], 800, 448, self.size))
        gate = {"points": [[0.2, 0.5], [0.8, 0.5], [0.8, 1.0], [0.2, 1.0]]}
        self.assertIsNone(d.roi([gate], 800, 448, self.size))

    def test_small_far_zone_is_cropped_with_headroom(self):
        far = {"points": [[0.6, 0.45], [0.7, 0.45], [0.7, 0.55], [0.6, 0.55]]}
        x0, y0, x1, y1 = d.roi([far], 800, 448, self.size)
        self.assertTrue(0 <= x0 < 0.6 * 800 and 0.7 * 800 < x1 <= 800)
        self.assertTrue(0 <= y0 <= (0.45 - d.HEADROOM) * 448 + 1 and 0.55 * 448 < y1 <= 448)
        # Model aspect (no padding) and at most 2x enlarged.
        self.assertAlmostEqual((x1 - x0) / (y1 - y0), 640 / 352, delta=0.02)
        self.assertGreaterEqual(x1 - x0, 320)

    def test_crop_stays_inside_picture(self):
        corner = {"points": [[0.9, 0.0], [1.0, 0.0], [1.0, 0.1], [0.9, 0.1]]}
        x0, y0, x1, y1 = d.roi([corner], 800, 448, self.size)
        self.assertTrue(x0 >= 0 and y0 >= 0 and x1 <= 800 and y1 <= 448)


class Tracking(unittest.TestCase):
    def test_thresholds(self):
        self.assertEqual(d.thresholds(0.45), (0.35, 0.6))
        self.assertEqual(d.thresholds(0.3), (0.25, 0.45))
        self.assertAlmostEqual(d.thresholds(0.9)[1], 0.9)

    def test_strong_frame_starts_and_quiet_stops(self):
        t = d.Tracker()
        self.assertEqual(t.update(0.7, 0.35, 0.6, 0), "start")
        self.assertIsNone(t.update(0.4, 0.35, 0.6, 2))  # weak frames keep it
        self.assertIsNone(t.update(0, 0.35, 0.6, 13))
        self.assertEqual(t.update(0, 0.35, 0.6, 14), "stop")
        self.assertEqual(t.update(0.9, 0.35, 0.6, 16), "start")

    def test_two_weak_frames_start(self):
        t = d.Tracker()
        self.assertIsNone(t.update(0.4, 0.35, 0.6, 0))
        self.assertIsNone(t.update(0.2, 0.35, 0.6, 2))
        self.assertEqual(t.update(0.5, 0.35, 0.6, 4), "start")

    def test_weak_frames_too_far_apart_do_not(self):
        t = d.Tracker()
        for i, s in enumerate([0.4, 0, 0, 0.4, 0, 0]):
            self.assertIsNone(t.update(s, 0.35, 0.6, i * 2))


class Stationary(unittest.TestCase):
    car = (0.5, 0.5, 0.2, 0.1)

    def test_iou(self):
        self.assertAlmostEqual(d.iou(self.car, self.car), 1.0, places=5)
        self.assertEqual(d.iou(self.car, (0, 0, 0.1, 0.1)), 0.0)

    def test_parked_car_stops_counting(self):
        p = d.Parked(30)
        self.assertEqual(p.update([self.car], 0), [False])  # arrives: counts
        self.assertEqual(p.update([(0.51, 0.5, 0.2, 0.1)], 10), [False])
        self.assertEqual(p.update([self.car], 31), [True])  # parked
        # Another car arriving next to it is news.
        self.assertEqual(p.update([self.car, (0.1, 0.5, 0.2, 0.1)], 33), [True, False])

    def test_moving_object_never_parks(self):
        p = d.Parked(30)
        for i in range(30):
            self.assertEqual(p.update([(0.02 * i, 0.5, 0.05, 0.2)], i * 2), [False])

    def test_forgotten_after_misses_and_learn(self):
        p = d.Parked(30, misses=3)
        p.learn([self.car], 0)
        self.assertEqual(p.update([self.car], 1), [True])  # there at start: parked
        for t in (3, 5, 7):
            p.update([], t)
        self.assertEqual(p.update([self.car], 9), [False])  # left and came back: news


class VideoMotion(unittest.TestCase):
    """MotionGate, on synthetic 64x36 pictures (mean removed, as small_gray)."""

    def scene(self, seed=1):
        rng = np.random.default_rng(seed)
        g = rng.normal(0, 30, (36, 64)).astype(np.float32)  # a textured yard
        return g - g.mean()

    def noisy(self, g, seed):
        n = g + np.random.default_rng(seed).normal(0, 3, g.shape).astype(np.float32)  # sensor grain
        return n - n.mean()

    def with_person(self, g, x=30, w=4, h=10):
        p = g.copy()
        p[20 : 20 + h, x : x + w] += 120  # ~1.7 % of the picture, much brighter
        return p - p.mean()

    def test_still_scene_with_grain_is_quiet(self):
        m, g = d.MotionGate(), self.scene()
        self.assertEqual([m.update(self.noisy(g, s)) for s in range(20)], [None] * 20)

    def test_person_walking_starts_and_leaving_stops(self):
        m, g = d.MotionGate(), self.scene()
        m.update(g)
        changes = [m.update(self.with_person(g, x=10 + 6 * i)) for i in range(4)]
        self.assertIn("start", changes[:2])  # two keyframes at most (~4 s)
        self.assertTrue(m.active)
        after = [m.update(self.noisy(g, s)) for s in range(6)]
        self.assertIn("stop", after)
        self.assertFalse(m.active)

    def test_light_change_everywhere_is_not_motion(self):
        m, g = d.MotionGate(), self.scene()
        m.update(g)
        other = self.scene(seed=99)  # the IR switching: every pixel different
        self.assertIsNone(m.update(other))
        self.assertFalse(m.active)
        self.assertIsNone(m.update(self.noisy(other, 5)))  # the new picture is the background now

    def test_motion_outside_the_zones_is_ignored(self):
        m, g = d.MotionGate(), self.scene()
        mask = d.motion_mask([{"points": [[0.7, 0.6], [0.95, 0.6], [0.95, 0.95], [0.7, 0.95]]}])
        m.update(g, mask)
        for i in range(4):  # a tree swaying on the left
            self.assertIsNone(m.update(self.with_person(g, x=2 + i), mask))
        self.assertFalse(m.active)
        changes = [m.update(self.with_person(g, x=50 + i, h=12), mask) for i in range(2)]
        self.assertIn("start", changes)

    def test_reset_relearns(self):
        m, g = d.MotionGate(), self.scene()
        m.update(g)
        m.reset()  # the camera turned (PTZ)
        self.assertIsNone(m.update(self.scene(seed=7)))  # first picture after a reset only teaches
        self.assertFalse(m.active)

    def test_mask_geometry(self):
        self.assertIsNone(d.motion_mask([]))
        m = d.motion_mask([{"points": [[0.5, 0.5], [0.6, 0.5], [0.6, 0.6]]}])
        self.assertEqual(m.shape, (36, 64))
        self.assertTrue(m[int(0.55 * 36), int(0.55 * 64)])  # the zone
        self.assertTrue(m[int(0.2 * 36), int(0.55 * 64)])  # room above it for a body
        self.assertFalse(m[int(0.55 * 36), 2])  # far left


if __name__ == "__main__":
    unittest.main()
