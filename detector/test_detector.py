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
        x = d.preprocess(rgb)
        self.assertEqual(x.shape, (1, 3, 416, 416))
        self.assertAlmostEqual(float(x[0, 0, 0, 0]), 1.0)
        self.assertAlmostEqual(float(x[0, 0, 100, 100]), 114 / 255, places=5)
        with self.assertRaises(ValueError):
            d.preprocess(np.zeros((500, 10, 3), dtype=np.uint8))

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


class Tracking(unittest.TestCase):
    def test_start_and_quiet_stop(self):
        t = d.Tracker(hits=1, window=2, quiet=12)
        self.assertEqual(t.update(True, 0), "start")
        self.assertIsNone(t.update(False, 2))
        self.assertIsNone(t.update(False, 11))
        self.assertEqual(t.update(False, 12), "stop")
        self.assertEqual(t.update(True, 14), "start")

    def test_two_hits_mode(self):
        t = d.Tracker(hits=2, window=3, quiet=12)
        for i, seen in enumerate([True, False, False, True, False, False]):
            self.assertIsNone(t.update(seen, i * 2))
        self.assertEqual(t.update(True, 12), None)
        self.assertEqual(t.update(True, 14), "start")


if __name__ == "__main__":
    unittest.main()
