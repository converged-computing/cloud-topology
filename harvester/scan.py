"""Clone repositories and find jobscripts among their files."""

import os
import shutil
import subprocess
import tempfile
import threading

from detect import detect, managers

GIT_ENV = {**os.environ, "GIT_TERMINAL_PROMPT": "0", "GIT_NO_LAZY_FETCH": "1"}
SKIP_MODES = {"120000", "160000"}  # symlinks and submodules


def git(args, cwd=None, timeout=300, input=None):
    """Run git and return stdout bytes; raise CalledProcessError on failure."""
    return subprocess.run(["git", *args], cwd=cwd, env=GIT_ENV, input=input,
                          capture_output=True, timeout=timeout, check=True).stdout


def tree_blobs(repo_dir):
    """Return {sha: [paths]} for regular-file blobs at HEAD."""
    out = {}
    for entry in git(["ls-tree", "-r", "-z", "HEAD"], cwd=repo_dir).split(b"\0"):
        if not entry:
            continue
        meta, path = entry.split(b"\t", 1)
        mode, kind, sha = meta.decode().split()
        if kind == "blob" and mode not in SKIP_MODES:
            out.setdefault(sha, []).append(path.decode("utf-8", errors="replace"))
    return out


def present_blobs(repo_dir):
    """Return {sha: size} for blobs downloaded into the local object store."""
    out = {}
    lines = git(["cat-file", "--batch-all-objects",
                 "--batch-check=%(objectname) %(objecttype) %(objectsize)"], cwd=repo_dir)
    for line in lines.decode().splitlines():
        sha, kind, size = line.split()
        if kind == "blob":
            out[sha] = int(size)
    return out


def read_blobs(repo_dir, shas):
    """Yield (sha, bytes) for each sha using one git cat-file --batch process."""
    proc = subprocess.Popen(["git", "cat-file", "--batch"], cwd=repo_dir, env=GIT_ENV,
                            stdin=subprocess.PIPE, stdout=subprocess.PIPE)

    def feed():
        for sha in shas:
            proc.stdin.write(sha.encode() + b"\n")
        proc.stdin.close()

    writer = threading.Thread(target=feed, daemon=True)
    writer.start()
    try:
        for _ in shas:
            header = proc.stdout.readline().split()
            size = int(header[2])
            data = proc.stdout.read(size)
            proc.stdout.read(1)  # trailing newline
            yield header[0].decode(), data
    finally:
        writer.join()
        proc.wait()


def scan_repo(repo, max_bytes, timeout=300):
    """Clone repo shallowly and return its commit and detected jobscripts."""
    workdir = tempfile.mkdtemp(prefix="scan-")
    dest = os.path.join(workdir, "repo")
    result = {"repo": repo, "status": "ok", "commit": None, "found": [],
              "blobs_read": 0, "blobs_skipped": 0}
    try:
        git(["clone", "--quiet", "--depth", "1", f"--filter=blob:limit={max_bytes}",
             "--no-checkout", f"https://github.com/{repo}.git", dest], timeout=timeout)
        result["commit"] = git(["rev-parse", "HEAD"], cwd=dest).decode().strip()
        tree = tree_blobs(dest)
        present = present_blobs(dest)
        wanted = [sha for sha in tree if sha in present and present[sha] <= max_bytes]
        result["blobs_skipped"] = len(tree) - len(wanted)
        for sha, data in read_blobs(dest, wanted):
            result["blobs_read"] += 1
            if b"\x00" in data:
                continue
            counts = detect(data.decode("utf-8", errors="replace"))
            mgrs = managers(counts)
            if mgrs:
                result["found"].append((sha, data, counts, mgrs, tree[sha]))
    except subprocess.TimeoutExpired:
        result["status"] = "timeout"
    except subprocess.CalledProcessError as err:
        result["status"] = "git_error: " + err.stderr.decode(errors="replace").strip()[:200]
    finally:
        shutil.rmtree(workdir, ignore_errors=True)
    return result
