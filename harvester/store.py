"""SQLite storage for search, repository, and scan results."""

import hashlib
import json
import sqlite3

SCHEMA = """
CREATE TABLE IF NOT EXISTS queries (
    query TEXT PRIMARY KEY, directive TEXT, depth INTEGER,
    results INTEGER, saturated INTEGER, status TEXT, next_page INTEGER);
CREATE TABLE IF NOT EXISTS search_hits (
    repo TEXT, ref TEXT, path TEXT, query TEXT,
    PRIMARY KEY (repo, path, query));
CREATE TABLE IF NOT EXISTS repo_sources (
    repo TEXT, source TEXT, PRIMARY KEY (repo, source));
CREATE TABLE IF NOT EXISTS repos (
    repo TEXT PRIMARY KEY, status TEXT, license TEXT, fork INTEGER,
    archived INTEGER, stars INTEGER, default_branch TEXT, size_kb INTEGER,
    pushed_at TEXT);
CREATE TABLE IF NOT EXISTS scans (
    repo TEXT PRIMARY KEY, status TEXT, commit_sha TEXT,
    blobs_read INTEGER, blobs_skipped INTEGER, kept INTEGER);
CREATE TABLE IF NOT EXISTS blobs (
    blob_sha TEXT PRIMARY KEY, size INTEGER, content TEXT,
    signals TEXT, managers TEXT);
CREATE TABLE IF NOT EXISTS occurrences (
    blob_sha TEXT, repo TEXT, commit_sha TEXT, path TEXT, source TEXT,
    PRIMARY KEY (blob_sha, repo, path));
CREATE TABLE IF NOT EXISTS labels (
    blob_sha TEXT, manager TEXT, parser_version TEXT, labels TEXT, totals TEXT,
    PRIMARY KEY (blob_sha, manager));
CREATE TABLE IF NOT EXISTS soft_labels (
    blob_sha TEXT, backend TEXT, model TEXT, soft_version TEXT, status TEXT, labels TEXT,
    PRIMARY KEY (blob_sha, backend, model));
CREATE TABLE IF NOT EXISTS agreement (
    blob_sha TEXT, manager TEXT, backend TEXT, model TEXT, field TEXT,
    hard TEXT, soft TEXT, status TEXT,
    PRIMARY KEY (blob_sha, manager, backend, model, field));
CREATE TABLE IF NOT EXISTS legacy_files (
    repo TEXT, path TEXT, blob_sha TEXT, kind TEXT,
    PRIMARY KEY (repo, path));
"""


def connect(path):
    """Open the database at path and ensure the schema exists."""
    # Wait up to 60 s for another process (e.g. search while scan runs) to release a write lock.
    db = sqlite3.connect(path, timeout=60)
    db.executescript(SCHEMA)
    return db


def git_blob_sha(data):
    """Return the git blob SHA-1 of data (bytes)."""
    return hashlib.sha1(b"blob %d\x00" % len(data) + data).hexdigest()


def add_source(db, repo, source):
    """Record that repo was discovered by source."""
    db.execute("INSERT OR IGNORE INTO repo_sources VALUES (?,?)", (repo, source))


def save_blob(db, blob_sha, data, counts, managers):
    """Store a kept blob once, keyed by its git blob SHA."""
    db.execute("INSERT OR IGNORE INTO blobs VALUES (?,?,?,?,?)", (
        blob_sha, len(data), data.decode("utf-8", errors="replace"),
        json.dumps(counts), ",".join(managers)))


def save_occurrence(db, blob_sha, repo, commit_sha, path, source):
    """Record where a kept blob was found."""
    db.execute("INSERT OR REPLACE INTO occurrences VALUES (?,?,?,?,?)",
               (blob_sha, repo, commit_sha, path, source))
