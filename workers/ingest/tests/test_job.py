"""Job decode + TextJob shape tests."""

from __future__ import annotations

import base64
import json
import unittest

from biumind_ingest.job import BinaryJob, TextJob


class BinaryJobTests(unittest.TestCase):
    def _payload(self, **overrides):
        base = {
            "source_id": "src_1",
            "project_id": "proj_1",
            "user_id": "user_1",
            "kind": "pdf",
            "data_b64": base64.b64encode(b"%PDF-1.4 fake").decode("ascii"),
            "title": "demo",
        }
        base.update(overrides)
        return base

    def test_round_trip(self):
        job = BinaryJob.from_payload(self._payload())
        self.assertEqual(job.kind, "pdf")
        self.assertEqual(job.data, b"%PDF-1.4 fake")
        self.assertEqual(job.title, "demo")

    def test_unsupported_kind_rejects(self):
        with self.assertRaises(ValueError):
            BinaryJob.from_payload(self._payload(kind="docx"))

    def test_missing_ids_reject(self):
        with self.assertRaises(ValueError):
            BinaryJob.from_payload(self._payload(source_id=""))

    def test_invalid_base64_rejects(self):
        with self.assertRaises(ValueError):
            BinaryJob.from_payload(self._payload(data_b64="not base64!"))

    def test_missing_data_rejects(self):
        p = self._payload()
        p.pop("data_b64")
        with self.assertRaises(ValueError):
            BinaryJob.from_payload(p)


class TextJobTests(unittest.TestCase):
    def test_from_extracted_uses_title_when_present(self):
        bj = BinaryJob(source_id="s", project_id="p", user_id="u",
                       kind="pdf", data=b"x", title="My Doc")
        tj = TextJob.from_extracted(bj, "extracted text")
        self.assertEqual(tj.title, "My Doc")
        self.assertEqual(tj.kind, "plain")  # Brain expects plain downstream
        self.assertEqual(tj.content, "extracted text")

    def test_from_extracted_synthesizes_title_when_missing(self):
        bj = BinaryJob(source_id="src_42", project_id="p", user_id="u",
                       kind="image", data=b"x")
        tj = TextJob.from_extracted(bj, "ocr text")
        self.assertIn("src_42", tj.title)
        self.assertIn("image", tj.title)

    def test_payload_roundtrip(self):
        bj = BinaryJob(source_id="s", project_id="p", user_id="u",
                       kind="audio", data=b"x", url="https://example/clip.mp3")
        tj = TextJob.from_extracted(bj, "transcript")
        body = json.loads(json.dumps(tj.to_payload(), ensure_ascii=False))
        # Must match Go ingestbus.Job field names exactly.
        self.assertEqual(set(body.keys()),
                         {"source_id", "project_id", "user_id",
                          "kind", "url", "title", "content"})
        self.assertEqual(body["url"], "https://example/clip.mp3")


if __name__ == "__main__":
    unittest.main()
