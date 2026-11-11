import pytest
import gzip
import json
import pathlib

import store
import websearch

FIXTURES = pathlib.Path(__file__).parent / "fixtures"
PLAN = json.loads((pathlib.Path(__file__).parent.parent / "plan.json").read_text())


def page(name):
    return gzip.open(FIXTURES / name, "rt").read()


def test_logged_out_search_page_has_no_session_or_hits():
    logged_in, hits = websearch.parse_page(page("github_search_logged_out.html.gz"))
    assert not logged_in and hits == []


def test_blob_links_from_real_tree_page():
    _, hits = websearch.parse_page(page("github_tree_page.html.gz"))
    paths = {p for _, _, p in hits}
    assert "t/batch/directives/valid/001-simple.sh" in paths
    assert all(repo == "flux-framework/flux-core" for repo, _, _ in hits)


def test_result_anchors_take_precedence_over_other_blob_links():
    # data-testid selector taken from jobspec-database scripts/search.py
    html = ('<meta name="user-login" content="someone">'
            '<a href="/a/b/blob/main/other.sh">x</a>'
            '<a data-testid="link-to-search-result" href="/o/r/blob/abc123/run.sh#L2">r</a>')
    assert websearch.parse_page(html) == (True, [("o/r", "abc123", "run.sh")])


def test_negate():
    assert websearch.negate("path:*.sh") == "NOT path:*.sh"
    assert websearch.negate('"#SBATCH --gres"') == 'NOT "#SBATCH --gres"'
    assert websearch.negate('("# flux:" OR "#flux:")') == 'NOT ("# flux:" OR "#flux:")'


def test_children_with_complement_and_per_directive_values():
    kids = websearch.children(PLAN, "mpijob", [], 0)
    assert [c for c, _ in kids] == [["language:YAML"], ["language:JSON"],
                                    ["NOT language:YAML NOT language:JSON"]]
    # option family (index 4) is per directive
    option = websearch.children(PLAN, "mpijob", ["x"], 4)
    assert option[0][0] == ["x", '"slotsPerWorker"'] and option[0][1] == 5


def test_children_skip_families_without_values():
    plan = {"split": [{"values": {"slurm": ["a"]}}, {"values": ["b"]}]}
    assert websearch.children(plan, "pbs", [], 0) == [(["b"], 2)]
    assert websearch.children(plan, "pbs", [], 2) == []


def test_build_query():
    q = websearch.build_query(PLAN, "minicluster", ["language:YAML"])
    assert q == '"kind: MiniCluster" NOT is:fork language:YAML'


class FakeBrowser:
    """Serves 20 hits per page; queries with fewer than 2 clauses saturate."""

    def __init__(self):
        self.urls = []

    def get(self, url):
        self.urls.append(url)
        from urllib.parse import parse_qs, urlparse
        qs = parse_qs(urlparse(url).query)
        query, page_no = qs["q"][0], int(qs["p"][0])
        clauses = query.count("language:") + query.count("is:archived")
        n = 5 if clauses >= 2 else 20
        if clauses >= 2 and page_no > 1:
            n = 0
        links = "".join(f'<a data-testid="link-to-search-result" '
                        f'href="/o/r{abs(hash(query)) % 97}/blob/c/{page_no}-{i}.sh">x</a>'
                        for i in range(n))
        return '<meta name="user-login" content="me">' + links


def small_plan():
    return {"base": "NOT is:fork", "directives": {"slurm": '"#SBATCH"'},
            "split": [{"complement": True, "values": ["language:Shell"]},
                      {"complement": True, "values": ["is:archived"]}]}


def test_adaptive_search_splits_saturated_queries(tmp_path):
    db = store.connect(tmp_path / "h.sqlite")
    n = websearch.search(db, FakeBrowser(), small_plan())
    rows = dict(db.execute("SELECT query, saturated FROM queries").fetchall())
    # root (saturated) -> 2 language children (saturated) -> 4 leaves (not saturated)
    assert n == 7 and len(rows) == 7
    assert sum(rows.values()) == 3


def test_search_resume_skips_done_queries(tmp_path):
    db = store.connect(tmp_path / "h.sqlite")
    websearch.search(db, FakeBrowser(), small_plan(), max_queries=3)
    browser = FakeBrowser()
    remaining = websearch.search(db, browser, small_plan())
    assert remaining == 4
    assert db.execute("SELECT COUNT(*) FROM queries WHERE status='done'").fetchone()[0] == 7


def test_harvest_plan_drops_license_search():
    import harvest
    plan = harvest.without_license_search(PLAN)
    assert all(f["family"] != "license" for f in plan["split"])
    queries = [c for c, _ in websearch.children(plan, "slurm", ["x"], 3)]
    assert not any("license:" in " ".join(q) for q in queries)
    assert len(plan["split"]) == len(PLAN["split"]) - 1


class SelectiveBrowser:
    """20 hits per page for queries without `empty_term`; none for those with it."""

    def __init__(self, empty_term=None, blocked=False):
        self.empty_term, self.blocked, self.urls = empty_term, blocked, []

    def get(self, url):
        from urllib.parse import parse_qs, urlparse
        self.urls.append(url)
        qs = parse_qs(urlparse(url).query)
        query, page_no = qs["q"][0], int(qs["p"][0])
        empty = self.blocked or (self.empty_term and self.empty_term in query) or page_no > 1
        links = "" if empty else "".join(
            f'<a data-testid="link-to-search-result" href="/o/r{abs(hash(query)) % 97}/blob/c/{i}.sh">x</a>'
            for i in range(5))
        return '<meta name="user-login" content="me">' + links


def flat_plan():
    return {"base": "", "directives": {"pbs": '"#PBS"'},
            "split": [{"complement": True, "values": ["language:Python"]}]}


def test_genuine_empty_query_is_stored_and_page_saved(tmp_path):
    db = store.connect(tmp_path / "h.sqlite")
    # root query not saturated (5 hits), so no split; make the root itself empty
    browser = SelectiveBrowser(empty_term='"#PBS"')
    websearch.search(db, browser, flat_plan(), pages_dir=str(tmp_path / "pages"))
    status, results = db.execute("SELECT status, results FROM queries").fetchone()
    assert (status, results) == ("empty", 0)
    assert len(list((tmp_path / "pages").iterdir())) == 1
    assert any("kind%3A+Deployment" in u for u in browser.urls)


def test_blocked_search_waits_then_stops(tmp_path):
    db = store.connect(tmp_path / "h.sqlite")
    waits = []
    with pytest.raises(RuntimeError, match="canary"):
        websearch.search(db, SelectiveBrowser(blocked=True), flat_plan(),
                         pages_dir=str(tmp_path / "pages"), sleep=waits.append, blocked_tries=2)
    assert waits == [60, 120]
    # the query is left unfinished so a later run retries it
    assert db.execute("SELECT status FROM queries").fetchone()[0] == "running"


def test_retry_empty_reruns_zero_result_queries(tmp_path):
    db = store.connect(tmp_path / "h.sqlite")
    db.execute("INSERT INTO queries VALUES (?,?,?,?,?,?,?)", ('"#PBS"', "pbs", 0, 0, 0, "done", 1))
    db.commit()
    assert websearch.search(db, SelectiveBrowser(), flat_plan()) == 0
    assert websearch.search(db, SelectiveBrowser(), flat_plan(), retry_empty=True) == 1
    assert db.execute("SELECT status, results FROM queries").fetchone() == ("done", 5)


def test_breadth_first_runs_every_directive_root_first(tmp_path):
    db = store.connect(tmp_path / "h.sqlite")
    plan = {"base": "", "directives": {"slurm": '"#SBATCH"', "pbs": '"#PBS"'},
            "split": [{"complement": True, "values": ["language:Shell"]}]}
    websearch.search(db, FakeBrowser(), plan, max_queries=2)
    assert sorted(q for (q,) in db.execute("SELECT query FROM queries")) == ['"#PBS"', '"#SBATCH"']


class ThrottledBrowser:
    """Full pages, except that the third request after start is throttled once: it and the
    canary that follows come back empty, as a rate-limited search does."""

    def __init__(self, pages=4):
        self.pages, self.requests, self.throttled, self.urls = pages, 0, False, []

    def get(self, url):
        from urllib.parse import parse_qs, urlparse
        self.urls.append(url)
        self.requests += 1
        qs = parse_qs(urlparse(url).query)
        page = int(qs["p"][0])
        canary = qs["q"][0] == websearch.CANARY
        blocked = self.requests in (3, 4)
        if blocked or (not canary and page > self.pages):
            links = ""
        else:
            links = "".join(f'<a data-testid="link-to-search-result" href="/o/r{page}/blob/c/{i}.yaml">x</a>'
                            for i in range(20))
        return '<meta name="user-login" content="me">' + links


def test_throttled_page_is_retried_from_where_it_stopped(tmp_path):
    db = store.connect(tmp_path / "h.sqlite")
    waits, browser = [], ThrottledBrowser(pages=4)
    websearch.search(db, browser, flat_plan(), pages_dir=str(tmp_path / "pages"), sleep=waits.append)
    assert waits == [60]
    # pages 1-2, page 3 throttled, canary throttled, then pages 3-4 and the empty page 5,
    # fetched twice since it follows a full page
    pages = [u.split("&p=")[1] for u in browser.urls if "PBS" in u]
    assert pages == ["1", "2", "3", "3", "4", "5", "5"]
    assert db.execute("SELECT results, status FROM queries").fetchone() == (80, "done")


def test_empty_page_after_a_partial_one_is_the_real_end(tmp_path):
    class Partial(ThrottledBrowser):
        def get(self, url):
            html = super().get(url)
            return html.replace("".join(f'<a data-testid="link-to-search-result" href="/o/r1/blob/c/{i}.yaml">x</a>'
                                        for i in range(5, 20)), "") if "&p=1" in url else html
    db = store.connect(tmp_path / "h.sqlite")
    browser = Partial(pages=1)
    browser.requests = 10  # no throttling
    websearch.search(db, browser, flat_plan(), pages_dir=str(tmp_path / "pages"), sleep=lambda s: None)
    assert not any(websearch.CANARY.replace(" ", "+").replace(":", "%3A") in u for u in browser.urls)
    assert db.execute("SELECT results FROM queries").fetchone()[0] == 5



def test_page_that_lost_a_render_race_is_fetched_again(tmp_path):
    class Racy(ThrottledBrowser):
        """Page 2 renders empty the first time only; the canary always works."""

        def __init__(self):
            super().__init__(pages=3)
            self.requests, self.raced = 10, False

        def get(self, url):
            if "&p=2" in url and "PBS" in url and not self.raced:
                self.raced = True
                self.urls.append(url)
                return '<meta name="user-login" content="me">'
            return super().get(url)

    db = store.connect(tmp_path / "h.sqlite")
    browser = Racy()
    websearch.search(db, browser, flat_plan(), pages_dir=str(tmp_path / "pages"), sleep=lambda s: None)
    pages = [u.split("&p=")[1] for u in browser.urls if "PBS" in u]
    assert pages == ["1", "2", "2", "3", "4", "4"]
    assert db.execute("SELECT results FROM queries").fetchone()[0] == 60
