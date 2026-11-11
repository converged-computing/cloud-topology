import pathlib

import pytest

import parse
from detect import detect, managers

FIXTURES = pathlib.Path(__file__).parent / "fixtures"


def read(name):
    return (FIXTURES / name).read_text()


def labels(name):
    text = read(name)
    kinds = managers(detect(text))
    assert len(kinds) == 1
    return parse.parse(text, kinds)[kinds[0]]


def one(name):
    ws = labels(name)["workloads"]
    assert len(ws) == 1
    return ws[0]


@pytest.mark.parametrize("text,value", [
    ("500m", 0.5), ("2", 2.0), ("1.5", 1.5), ("4Gi", 4 * 2 ** 30), ("512Mi", 512 * 2 ** 20),
    ("1G", 1e9), ("1e3", 1000.0), ("100k", 1e5), (3, 3.0), ("abc", None), ("1x", None),
    (True, None), (None, None)])
def test_quantities(text, value):
    # Formats from api/resource/quantity.go
    assert parse.quantity(text) == value


@pytest.mark.parametrize("text,value", [("512m", 512.0), ("2g", 2048.0), ("1024k", 1.0), ("1t", 2 ** 20), ("x", None)])
def test_spark_memory(text, value):
    assert parse.jvm_mib(text) == value


@pytest.mark.parametrize("image,access", [
    ("nginx:1.25", "public"), ("rayproject/ray:2.9.0", "public"), ("ghcr.io/a/b:1", "public"),
    ("123456789012.dkr.ecr.us-east-1.amazonaws.com/train:latest", "private"),
    ("localhost:5000/x", "private"), ("10.0.0.1:5000/x", "private"),
    ("us-docker.pkg.dev/proj/repo/img", "unknown"), ("{{ .Values.image }}", "templated"),
    ("$REGISTRY/img", "templated")])
def test_image_access(image, access):
    assert parse.image_access(image) == access


def test_mpijob_roles_and_totals():
    w = one("mpijob_pi.yaml")
    assert [(r["role"], r["replicas"]) for r in w["roles"]] == [("Launcher", 1), ("Worker", 2)]
    assert w["pods"] == 3 and w["slots_per_worker"] == 1 and w["controller"] == "training-operator"
    # limits without requests: requests default to the limits
    assert w["cpu_millicores"] == 3000 and w["memory_mib"] == 3 * 1024
    assert w["gpus"] is None and w["images"] == ["mpioperator/mpi-pi:openmpi"]
    t = parse.totals(labels("mpijob_pi.yaml"))
    assert t["pods"] == 3 and t["cpu_cores"] == 3.0 and t["memory_gb"] == 3.0
    assert t["images_public"] is True and t["undefined_refs"] == 0 and t["requests_set"]


def test_pytorchjob_gpus_selector_tolerations_queue():
    w = one("pytorchjob_gpu.yaml")
    assert w["pods"] == 4 and w["gpus"] == 8 and w["accelerators"] == {"nvidia.com/gpu": 8}
    assert w["accelerator_vendors"] == ["nvidia"] and w["queue"] == "team-a"
    assert w["procs_per_node"] == "2" and w["namespace"] == "kubeflow"
    worker = w["roles"][1]["pod"]
    assert worker["node_selector"] == {"cloud.google.com/gke-accelerator": "nvidia-tesla-a100"}
    assert worker["tolerations"][0]["key"] == "nvidia.com/gpu"
    assert w["roles"][0]["pod"]["node_selector"] is None
    assert w["cpu_millicores"] == 16000 and w["memory_mib"] == 64 * 1024


def test_jobset_multiplies_replicas_and_parallelism():
    w = one("jobset.yaml")
    r = w["roles"][0]
    assert r["role"] == "workers" and r["replicas"] == 8 and r["job_replicas"] == 2
    assert r["completions"] == 4 and r["pod"]["topology_spread"] == 1
    assert w["pods"] == 8 and w["cpu_millicores"] == 4000 and w["memory_mib"] == 2048


def test_rayjob_head_workers_and_defined_configmap():
    w = one("rayjob.yaml")
    assert [(r["role"], r["replicas"]) for r in w["roles"]] == [("head", 1), ("small-group", 3)]
    assert w["roles"][1]["min_replicas"] == 1 and w["roles"][1]["max_replicas"] == 5
    assert w["pods"] == 4 and w["cpu_millicores"] == 7000 and w["entrypoint"].startswith("python")
    assert w["refs"]["configmaps"] == ["ray-job-code-sample"] and w["undefined_refs"] == {}


def test_argo_workflow_templates_have_unknown_replicas():
    w = one("argo_workflow.yaml")
    assert [r["role"] for r in w["roles"]] == ["print-message"]
    assert w["pods"] is None and w["parallelism"] == 2 and w["images"] == ["busybox"]
    assert w["cpu_millicores"] is None  # replicas unknown, so no total
    assert w["roles"][0]["pod"]["cpu_request_m"] == 100


def test_minicluster_size_tasks_and_pod_scheduling():
    w = one("minicluster.yaml")
    r = w["roles"][0]
    assert r["role"] == "broker" and r["replicas"] == 4 and r["tasks"] == 8
    assert w["pods"] == 4 and w["cpu_millicores"] == 4 * 96000
    assert w["memory_mib"] == pytest.approx(4 * 300e9 / 2 ** 20)
    assert r["pod"]["node_selector"] == {"node.kubernetes.io/instance-type": "c7i.48xlarge"}
    assert r["pod"]["scheduler_name"] == "default-scheduler"
    assert w["controller"] == "flux-operator" and w["interactive"] is None


def test_job_scheduling_subset_refs_and_claims():
    l = labels("job_gpu.yaml")
    w = l["workloads"][0]
    assert w["pods"] == 2 and "completions" not in w  # completions sits on the role
    r = w["roles"][0]
    assert r["completions"] == 2 and r["completion_mode"] == "Indexed" and w["backoff_limit"] == 3
    pod = r["pod"]
    assert pod["host_network"] is True and pod["priority_class"] == "high"
    assert pod["affinity"] == {"node": False, "pod": False, "pod_anti": True}
    assert pod["volumes"] == {"pvc": 1, "empty_dir": 1} and pod["init_containers"] == 1
    assert w["gpus"] == 16 and w["cpu_millicores"] == 192000
    assert w["refs"] == {"secrets": ["regcred", "wandb"], "configmaps": ["train-config"],
                         "pvcs": ["dataset"], "serviceaccounts": ["trainer"]}
    # the ConfigMap and claim are defined in the file; the secrets and account are not
    assert w["undefined_refs"] == {"secrets": ["regcred", "wandb"], "serviceaccounts": ["trainer"]}
    assert l["claims"] == [{"name": "dataset", "storage_class": "premium-rwo",
                            "size_mib": 2 ** 20, "access_modes": ["ReadOnlyMany"]}]
    assert w["image_access"]["123456789012.dkr.ecr.us-east-1.amazonaws.com/train:latest"] == "private"
    t = parse.totals(l)
    assert t["images_public"] is False and t["undefined_refs"] == 3 and t["gpus"] == 16


def test_statefulset_claim_templates_define_their_claims():
    l = labels("statefulset.yaml")
    w = l["workloads"][0]
    assert w["pods"] == 3 and w["cpu_millicores"] == 750 and w["undefined_refs"] == {}
    assert l["claims"][0]["storage_class"] == "standard" and l["claims"][0]["size_mib"] == 10 * 1024


def test_spark_driver_and_executors():
    w = one("spark.yaml")
    assert [(r["role"], r["replicas"]) for r in w["roles"]] == [("driver", 1), ("executor", 2)]
    assert w["pods"] == 3 and w["cpu_millicores"] == 3000 and w["memory_mib"] == 3 * 512
    assert w["gpus"] == 2 and w["images"] == ["spark:3.5.0"]
    assert w["roles"][0]["pod"]["cpu_limit_m"] == 1200
    assert w["undefined_refs"] == {"serviceaccounts": ["spark-operator-spark"]}


def test_volcano_tasks_and_queue():
    w = one("volcano_job.yaml")
    assert [(r["role"], r["replicas"]) for r in w["roles"]] == [("mpimaster", 1), ("mpiworker", 2)]
    assert w["min_available"] == 3 and w["queue"] == "default" and w["cpu_millicores"] == 1500


def test_trainjob_nodes():
    w = one("trainjob.yaml")
    assert w["pods"] == 4 and w["gpus"] == 32 and w["cpu_millicores"] == 128000
    assert w["roles"][0]["procs_per_node"] == 8 and w["roles"][0]["runtime"] == "torch-distributed"


def test_helm_template_reports_yaml_error():
    out = parse.parse(read("helm_template.yaml"), ["batch/Job"])
    assert out["batch/Job"]["workloads"] == [] and out["batch/Job"]["errors"][0].startswith("yaml")


def test_deployment_and_daemonset_defaults():
    text = ("apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: d}\n"
            "spec: {template: {spec: {containers: [{name: a, image: nginx}]}}}\n---\n"
            "apiVersion: apps/v1\nkind: DaemonSet\nmetadata: {name: s}\n"
            "spec: {template: {spec: {containers: [{name: a, image: nginx}]}}}\n")
    out = parse.parse(text, ["apps/Deployment", "apps/DaemonSet"])
    d, s = out["apps/Deployment"]["workloads"][0], out["apps/DaemonSet"]["workloads"][0]
    assert d["pods"] == 1 and not d["requests_set"] and s["pods"] is None
    assert parse.totals(out["apps/Deployment"])["images_public"] is True
