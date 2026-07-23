import importlib.util
import sys
import unittest
from datetime import datetime, timedelta, timezone
from pathlib import Path


MODULE_PATH = Path(__file__).resolve().parents[1] / "run_idle.py"
SPEC = importlib.util.spec_from_file_location("run_idle", MODULE_PATH)
assert SPEC and SPEC.loader
RUN_IDLE = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = RUN_IDLE
SPEC.loader.exec_module(RUN_IDLE)


class StatsParsingTests(unittest.TestCase):
    def test_parse_size_supports_binary_and_decimal_units(self):
        self.assertEqual(RUN_IDLE.parse_size("1.5 MiB"), 1_572_864)
        self.assertEqual(RUN_IDLE.parse_size("2MB"), 2_000_000)
        self.assertEqual(RUN_IDLE.parse_size("0B"), 0)

    def test_parse_docker_stats_line(self):
        parsed = RUN_IDLE.parse_stats_line(
            '{"BlockIO":"1.5MB / 2kB","CPUPerc":"12.50%","ID":"abc123",'
            '"MemPerc":"25.00%","MemUsage":"1.5MiB / 6MiB","Name":"app",'
            '"NetIO":"3kB / 4kB","PIDs":"7"}'
        )
        self.assertEqual(parsed["cpu_cores"], 0.125)
        self.assertEqual(parsed["memory_usage_bytes"], 1_572_864)
        self.assertEqual(parsed["network_tx_bytes"], 4_000)
        self.assertEqual(parsed["block_read_bytes"], 1_500_000)
        self.assertEqual(parsed["pids"], 7)


class PercentileTests(unittest.TestCase):
    def test_percentile_uses_linear_interpolation(self):
        values = [1, 2, 3, 4]
        self.assertEqual(RUN_IDLE.percentile(values, 0.5), 2.5)
        self.assertAlmostEqual(RUN_IDLE.percentile(values, 0.95), 3.85)

    def test_summary_contains_mean_percentiles_and_maximum(self):
        self.assertEqual(
            RUN_IDLE.summarize_values([1, 2, 3, 4]),
            {"mean": 2.5, "p50": 2.5, "p95": 3.85, "max": 4},
        )


class SamplingTimingTests(unittest.TestCase):
    @staticmethod
    def samples_with_gaps(gaps):
        current = datetime(2026, 7, 22, tzinfo=timezone.utc)
        samples = [{"timestamp": current.isoformat().replace("+00:00", "Z")}]
        for gap in gaps:
            current += timedelta(seconds=gap)
            samples.append({"timestamp": current.isoformat().replace("+00:00", "Z")})
        return samples

    def test_continuous_sampling_is_accepted(self):
        timing = RUN_IDLE.analyze_sample_timing(
            self.samples_with_gaps([5, 5, 5]),
            measure_seconds=20,
            interval_seconds=5,
            wall_duration_seconds=20,
        )
        self.assertTrue(timing["continuous"])
        self.assertEqual(timing["maximum_gap_seconds"], 5)

    def test_large_sampling_gap_is_rejected(self):
        timing = RUN_IDLE.analyze_sample_timing(
            self.samples_with_gaps([5, 187, 5]),
            measure_seconds=20,
            interval_seconds=5,
            wall_duration_seconds=202,
        )
        self.assertFalse(timing["continuous"])
        self.assertEqual(timing["maximum_gap_seconds"], 187)


class FailureAndMetadataTests(unittest.TestCase):
    def test_failure_record_is_not_success_shaped(self):
        record = RUN_IDLE.build_failure_record(
            "catalog-service", RuntimeError("readiness failed"), {"status": "exited"}
        )
        self.assertEqual(record["status"], "failed")
        self.assertEqual(record["error"]["type"], "RuntimeError")
        self.assertIn("readiness failed", record["error"]["message"])
        self.assertEqual(record["last_state"], {"status": "exited"})

    def test_execution_metadata_schema_accepts_complete_document(self):
        document = {
            "schema_version": "1.0",
            "run_id": "idle-test",
            "status": "passed",
            "started_at": "2026-07-22T00:00:00Z",
            "finished_at": "2026-07-22T00:01:00Z",
            "repository": {"sha": "abc", "dirty": False},
            "environment": {"docker": {}, "host": {}},
            "configuration": {
                "warmup_seconds": 1,
                "measure_seconds": 2,
                "sample_interval_seconds": 1,
                "operational_scrape_interval_seconds": 15,
            },
            "data_state": {"profile": "schema_only"},
            "services": {},
        }
        RUN_IDLE.validate_execution_document(document)

    def test_execution_metadata_schema_rejects_missing_host_context(self):
        with self.assertRaisesRegex(ValueError, "environment"):
            RUN_IDLE.validate_execution_document(
                {
                    "schema_version": "1.0",
                    "run_id": "idle-test",
                    "status": "failed",
                    "started_at": "now",
                    "finished_at": "now",
                    "repository": {},
                    "configuration": {},
                    "data_state": {},
                    "services": {},
                }
            )


if __name__ == "__main__":
    unittest.main()
