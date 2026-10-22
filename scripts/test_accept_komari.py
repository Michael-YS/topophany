"""Keep official Komari acceptance checks independent of formatting accidents."""

import copy
import datetime as dt
from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parent))
from accept_komari import AcceptanceFailure, sample_instant, verify_failed


class SampleInstantTests(unittest.TestCase):
    def test_variable_fractional_precision_orders_by_instant(self):
        # Lexical comparison incorrectly puts the no-fraction Z suffix after
        # the decimal point, and .1Z after .100000001Z. Both orders are wrong.
        timestamps = [
            "2026-10-22T12:34:56Z",
            "2026-10-22T12:34:56.000000001Z",
            "2026-10-22T12:34:56.1Z",
            "2026-10-22T12:34:56.100000001Z",
            "2026-10-22T12:34:56.999999999Z",
            "2026-10-22T12:34:57Z",
        ]
        instants = [sample_instant(value) for value in timestamps]
        for earlier, later in zip(instants, instants[1:]):
            self.assertLess(earlier, later)
        self.assertEqual(instants[1][1], 1)
        self.assertEqual(instants[3][1], 100000001)
        self.assertEqual(instants[4][1], 999999999)
        self.assertEqual(instants[0][0],
                         dt.datetime(2026, 10, 22, 12, 34, 56, tzinfo=dt.timezone.utc))

    def test_equal_instants_have_equal_fractional_representations(self):
        self.assertEqual(sample_instant("2026-10-22T12:34:56.1Z"),
                         sample_instant("2026-10-22T12:34:56.100000000Z"))
        self.assertEqual(sample_instant("2026-10-22T12:34:56Z"),
                         sample_instant("2026-10-22T12:34:56.000000000Z"))

    def test_calendar_boundary_preserves_order(self):
        self.assertLess(sample_instant("2026-12-31T23:59:59.999999999Z"),
                        sample_instant("2027-01-01T00:00:00Z"))

    def test_only_exact_official_utc_schema_is_accepted(self):
        for value in [
            None, 123, "", "2026-10-22 12:34:56Z", "2026-10-22T2:34:56Z",
            "2026-10-22T12:34:56+00:00", "2026-10-22T12:34:56",
            "2026-10-22T12:34:56.Z", "2026-10-22T12:34:56.1234567891Z",
            "2026-10-22T12:34:56,1Z", "2026-10-22T12:34:60Z",
            "2026-02-30T12:34:56Z", "0000-01-01T00:00:00Z",
        ]:
            with self.subTest(value=value), self.assertRaises(AcceptanceFailure):
                sample_instant(value)


class FailedCollectionTests(unittest.TestCase):
    def setUp(self):
        self.observation = {
            "outcome": "error", "evidence": "reported", "connectivity": False,
            "external": {"contact": False}, "code": "komari_auth",
        }

    def test_source_error_without_report_is_accepted(self):
        verify_failed([self.observation], expected_code="komari_auth")
        with_null_report = copy.deepcopy(self.observation)
        with_null_report["external"]["report"] = None
        verify_failed([self.observation, with_null_report], expected_code="komari_auth")

    def test_empty_observations_cannot_satisfy_assertions_vacuously(self):
        with self.assertRaises(AcceptanceFailure):
            verify_failed([], expected_code="komari_auth")

    def test_source_failure_cannot_pass_as_target_failure_or_contact(self):
        for field, value in [
            ("outcome", "ok"), ("outcome", "fail"),
            ("evidence", "measured"), ("connectivity", True),
        ]:
            observation = copy.deepcopy(self.observation)
            observation[field] = value
            with self.subTest(field=field, value=value), self.assertRaises(AcceptanceFailure):
                verify_failed([observation], expected_code="komari_auth")
        for external in [{}, {"contact": True}, {"contact": False, "report": {}}]:
            observation = copy.deepcopy(self.observation)
            observation["external"] = external
            with self.subTest(external=external), self.assertRaises(AcceptanceFailure):
                verify_failed([observation], expected_code="komari_auth")

    def test_unrelated_or_missing_error_code_cannot_prove_auth_failure(self):
        for code in [None, "", "komari_json", "komari_config", "komari_credential",
                     "source_collection_failed", "source_canceled", "komari_http"]:
            observation = copy.deepcopy(self.observation)
            if code is None:
                observation.pop("code")
            else:
                observation["code"] = code
            with self.subTest(code=code), self.assertRaises(AcceptanceFailure):
                verify_failed([observation], expected_code="komari_auth")

    def test_hidden_node_requires_its_http_failure_code(self):
        observation = copy.deepcopy(self.observation)
        observation["code"] = "komari_http"
        verify_failed([observation], expected_code="komari_http")
        with self.assertRaises(AcceptanceFailure):
            verify_failed([self.observation], expected_code="komari_http")

    def test_each_observation_must_have_the_expected_cause(self):
        unexpected = copy.deepcopy(self.observation)
        unexpected["code"] = "komari_json"
        with self.assertRaises(AcceptanceFailure):
            verify_failed([self.observation, unexpected], expected_code="komari_auth")

    def test_successful_prefix_cannot_be_hidden_by_a_later_error(self):
        with_report = copy.deepcopy(self.observation)
        with_report["external"]["report"] = {"object": "synthetic-node"}
        with self.assertRaises(AcceptanceFailure):
            verify_failed([with_report, self.observation], expected_code="komari_auth")


if __name__ == "__main__":
    unittest.main()
