import copy
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("heartbeat_cost", Path(__file__).resolve().parents[1] / "heartbeat.py")
heartbeat = importlib.util.module_from_spec(spec)
spec.loader.exec_module(heartbeat)


def pair():
    off = {"harnessSha": "harness", "config": {"cases": 4}, "images": {"worker": "worker", "engine": "engine"},
           "host": {"cores": 4}, "heartbeat": False,
           "workflows": [{"id": str(i), "status": "COMPLETED", "handlerCalls": 1, "remaining": 2} for i in range(4)],
           "database": {"jobs": 4, "completedJobs": 4, "jobUpdates": 8, "writerConnections": 0}, "timeouts": 0,
           "workflowP95Ms": 12000, "startP95Ms": 10, "workflowsPerSecond": .15,
           "resources": {"samples": [{}], "summary": {"sampleCount": 1, "k6CapacityPctPeak": 20}},
           "acceptedHeartbeats": 0, "heartbeatTxMeanMs": None}
    on = copy.deepcopy(off)
    on.update(heartbeat=True, acceptedHeartbeats=8, heartbeatTxMeanMs=1)
    on["database"]["jobUpdates"] = 16
    return off, on


class HeartbeatCostGate(unittest.TestCase):
    def test_matching_pair_passes_and_counts_renewal_writes(self):
        result = heartbeat.validate_pair(*pair())
        self.assertEqual(result, {"result": "pass", "additionalJobUpdates": 8,
                                 "workflowP95ChangePct": 0, "throughputChangePct": 0})

    def test_incompatible_pair_fails(self):
        for key in ("harnessSha", "config", "images", "host"):
            with self.subTest(key=key):
                off, on = pair(); on[key] = "different"
                with self.assertRaises(ValueError): heartbeat.validate_pair(off, on)

    def test_missing_or_incorrect_evidence_fails(self):
        for mutate in (
            lambda r: r.update(heartbeat=False),
            lambda r: r.update(workflows=[]),
            lambda r: r["workflows"][0].update(id="1"),
            lambda r: r["workflows"][0].update(status="FAILED"),
            lambda r: r["workflows"][0].update(handlerCalls=2),
            lambda r: r["workflows"][0].update(remaining=1),
            lambda r: r["database"].update(jobs=5),
            lambda r: r["database"].update(completedJobs=3),
            lambda r: r["database"].update(writerConnections=1),
            lambda r: r["database"].update(jobUpdates=None),
            lambda r: r["database"].update(jobUpdates=10),
            lambda r: r.update(acceptedHeartbeats=0),
            lambda r: r.update(heartbeatTxMeanMs=None),
            lambda r: r.update(timeouts=1),
            lambda r: r.update(workflowP95Ms=float("nan")),
            lambda r: r.update(workflowP95Ms=14000),
            lambda r: r.update(startP95Ms=1100),
            lambda r: r.update(workflowsPerSecond=.1),
            lambda r: r["resources"].update(samples=[]),
            lambda r: r["resources"].update(errors=["sampler failed"]),
            lambda r: r["resources"]["summary"].update(k6CapacityPctPeak=95),
        ):
            with self.subTest(mutation=mutate):
                off, on = pair(); mutate(on)
                with self.assertRaises((ValueError, KeyError)): heartbeat.validate_pair(off, on)

    def test_metric_parser_requires_expected_data_and_accepts_uninstantiated_off_counter(self):
        snapshot = 'engine_job_timeout_total 0\nengine_job_heartbeat_total{result="accepted"} 8\n'
        self.assertEqual(heartbeat.metric(snapshot, "engine_job_timeout_total"), 0)
        self.assertEqual(heartbeat.metric(snapshot, "engine_job_heartbeat_total", '{result="accepted"}'), 8)
        with self.assertRaises(ValueError): heartbeat.metric(snapshot, "missing")
        self.assertEqual(heartbeat.metric(snapshot, "missing", default=0), 0)


if __name__ == "__main__":
    unittest.main()
