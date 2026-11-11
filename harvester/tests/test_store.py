import subprocess

import store


def test_git_blob_sha_matches_git(tmp_path):
    path = tmp_path / "job.sh"
    path.write_bytes(b"#!/bin/sh\n#SBATCH --time=1\nsrun hostname |sort\n")
    expected = subprocess.check_output(["git", "hash-object", str(path)], text=True).strip()
    assert store.git_blob_sha(path.read_bytes()) == expected
