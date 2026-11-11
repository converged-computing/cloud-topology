import pytest

import harvest
import scan
import store


@pytest.mark.network
def test_scan_real_repo_with_size_filtered_blobs():
    """Clones the Flux Operator with a small blob limit so large blobs stay missing."""
    r = scan.scan_repo("flux-framework/flux-operator", max_bytes=4096)
    assert r["status"] == "ok" and len(r["commit"]) == 40
    assert r["blobs_skipped"] > 0 and r["blobs_read"] > 0
    kinds = {k for _, _, _, kinds, _ in r["found"] for k in kinds}
    assert "flux-framework.org/MiniCluster" in kinds
    for sha, data, *_ in r["found"]:
        assert store.git_blob_sha(data) == sha


@pytest.mark.network
def test_scan_missing_repo_reports_error():
    r = scan.scan_repo("converged-computing/this-repo-does-not-exist-xyz", max_bytes=4096)
    assert r["status"].startswith("git_error")


def test_repos_to_scan_skips_failed_and_large(tmp_path):
    db = store.connect(tmp_path / "h.sqlite")
    for repo in ("a/ok", "b/gone", "c/big"):
        store.add_source(db, repo, "websearch")
    db.execute("INSERT INTO repos VALUES ('b/gone','http_404',NULL,0,0,0,NULL,NULL,NULL)")
    db.execute("INSERT INTO repos VALUES ('c/big','ok','MIT',0,0,0,'main',900000,NULL)")
    assert harvest.repos_to_scan(db, 500000, 10) == ["a/ok"]
    assert sorted(harvest.repos_to_scan(db, None, 10)) == ["a/ok", "c/big"]
