import json
import pathlib

import pytest

pytest.importorskip("behalf")

import harvest
import soft
import store
from detect import detect, managers

FIXTURES = pathlib.Path(__file__).parent / "fixtures"


class FakeRunner:
    """Calls record_labels with fixed labels, like behalf's own test runner."""

    def __init__(self, labels, calls=1):
        self.labels, self.calls, self.prompts = labels, calls, []

    async def converse(self, task):
        raise AssertionError("conversation must be skipped when a manifest is given")

    async def run_agent(self, system_prompt, user_prompt, tools, confirm_fn):
        self.prompts.append((system_prompt, user_prompt))
        record = {t.name: t for t in tools}["record_labels"]
        for _ in range(self.calls):
            await record.handler({"labels": self.labels})


def db_with(tmp_path, name):
    db = store.connect(tmp_path / "h.sqlite")
    data = (FIXTURES / name).read_bytes()
    counts = detect(data.decode())
    store.save_blob(db, store.git_blob_sha(data), data, counts, managers(counts))
    db.commit()
    return db


def test_agreement_statuses():
    hard = {"pods": 3, "gpus": None, "cpu_cores": 3.0, "memory_gb": 3.0}
    s = {"pods": 3, "gpus": 2, "cpu_cores": "3", "memory_gb": None}
    assert soft.agreement(hard, s) == {
        "pods": "match", "gpus": "soft_only", "cpu_cores": "match", "memory_gb": "hard_only"}
    assert soft.agreement({}, {})["pods"] == "none"
    assert soft.agreement({"gpus": 8}, {"gpus": 4})["gpus"] == "mismatch"


def test_pipeline_label_soft_agree_export(tmp_path):
    db = db_with(tmp_path, "mpijob_pi.yaml")
    assert harvest.label(db, relabel=False) == 1
    runner = FakeRunner({"application": "pi", "launcher": "mpirun", "pods": 3, "gpus": None,
                         "needs": ["mpi-operator"]})
    assert harvest.soft_label(db, "claude", "test-model", 10, runner=runner) == 1
    # the model sees the manifest but not the parser output
    system, user = runner.prompts[0]
    assert "slotsPerWorker: 1" in user and "cpu_cores" in system
    assert "totals" not in user and "millicores" not in user
    harvest.agree(db)
    status = dict(db.execute("SELECT field, status FROM agreement").fetchall())
    assert status == {"pods": "match", "gpus": "none", "cpu_cores": "hard_only",
                      "memory_gb": "hard_only"}
    out = tmp_path / "m.jsonl"
    assert harvest.export(db, out) == 1
    rec = json.loads(out.read_text())
    assert rec["kinds"] == ["kubeflow.org/MPIJob"]
    assert rec["hard_labels"]["kubeflow.org/MPIJob"]["totals"]["pods"] == 3
    assert rec["soft_labels"][0]["labels"]["needs"] == ["mpi-operator"]


def test_two_recorded_label_calls_fail_validation(tmp_path):
    db = db_with(tmp_path, "job_gpu.yaml")
    harvest.soft_label(db, "claude", "m", 10, runner=FakeRunner({"pods": 2}, calls=2))
    status = db.execute("SELECT status FROM soft_labels").fetchone()[0]
    assert status.startswith("error")
