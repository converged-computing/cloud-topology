import pathlib

import pytest

from detect import detect, managers

FIXTURES = pathlib.Path(__file__).parent / "fixtures"


def read(name):
    return (FIXTURES / name).read_text()


@pytest.mark.parametrize("name,kinds", [
    ("mpijob_pi.yaml", ["kubeflow.org/MPIJob"]),
    ("pytorchjob_gpu.yaml", ["kubeflow.org/PyTorchJob"]),
    ("jobset.yaml", ["jobset.x-k8s.io/JobSet"]),
    ("rayjob.yaml", ["ray.io/RayJob"]),
    ("argo_workflow.yaml", ["argoproj.io/Workflow"]),
    ("minicluster.yaml", ["flux-framework.org/MiniCluster"]),
    ("job_gpu.yaml", ["batch/Job"]),
    ("statefulset.yaml", ["apps/StatefulSet"]),
    ("spark.yaml", ["sparkoperator.k8s.io/SparkApplication"]),
    ("volcano_job.yaml", ["batch.volcano.sh/Job"]),
    ("trainjob.yaml", ["trainer.kubeflow.org/TrainJob"]),
])
def test_workload_kinds_detected(name, kinds):
    assert managers(detect(read(name))) == kinds


def test_volcano_job_is_not_batch_job():
    counts = detect(read("volcano_job.yaml"))
    assert "kind:batch/Job" not in counts and counts["kind:batch.volcano.sh/Job"] == 1


def test_companion_documents_do_not_keep_a_file():
    # The ConfigMap and claim beside the Job are documents, not workloads.
    counts = detect(read("job_gpu.yaml"))
    assert counts["documents"] == 3 and managers(counts) == ["batch/Job"]
    assert counts["accelerator"] == 1


def test_kueue_queue_is_a_signal():
    assert detect(read("pytorchjob_gpu.yaml"))["kueue"] == 1


def test_helm_template_is_recorded_but_not_kept():
    counts = detect(read("helm_template.yaml"))
    assert counts["helm_template"] == 1 and counts["yaml_error"] == 1
    assert counts["helm:Job"] == 1 and managers(counts) == []


def test_non_kubernetes_yaml_is_skipped_without_parsing():
    assert detect(read("compose.yaml")) == {}


def test_json_manifests_parse():
    text = '{"apiVersion": "batch/v1", "kind": "Job", "spec": {"template": {"spec": {}}}}'
    assert managers(detect(text)) == ["batch/Job"]


def test_unknown_kinds_are_not_kept():
    text = "apiVersion: v1\nkind: Service\nmetadata: {name: s}\n"
    assert managers(detect(text)) == [] and detect(text)["documents"] == 1


def test_alias_heavy_files_are_refused():
    text = "apiVersion: v1\nkind: Pod\na: &a [1]\n" + "\n".join(
        f"k{i}: [{', '.join(['*a'] * 50)}]" for i in range(20))
    counts = detect(text)
    assert counts.get("yaml_error") == 1 and managers(counts) == []
