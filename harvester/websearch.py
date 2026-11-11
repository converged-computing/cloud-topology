"""Discover repositories with jobscripts through GitHub's web code search."""

import json
import os
import random
import re
import sys
import time
from html.parser import HTMLParser
from urllib.parse import quote_plus

from store import add_source

SEARCH_URL = "https://github.com/search?q={q}&type=code&p={page}"
# Web code search returns at most 100 results (5 pages) per query
# (docs.github.com/search-github/github-code-search/about-github-code-search).
RESULT_CAP = 100
MAX_PAGES = 5
RATE_LIMIT_MARKERS = ("secondary rate limit", "whoa there")
# What a rendered results page contains. Only the result link is a reliable sign: words
# like "no results" are in the page's markup before the results load, so a page without
# results is waited on for settle seconds instead.
RESULT_MARKERS = ('data-testid="link-to-search-result"',)
BLOB_HREF = re.compile(r"^/([^/]+)/([^/]+)/blob/([^/]+)/([^#?]+)")


class _Links(HTMLParser):
    """Collect hrefs of anchors, separating search-result anchors."""

    def __init__(self):
        super().__init__()
        self.results, self.blobs, self.login = [], [], None

    def handle_starttag(self, tag, attrs):
        attrs = dict(attrs)
        if tag == "meta" and attrs.get("name") == "user-login":
            self.login = attrs.get("content") or ""
        if tag != "a" or not attrs.get("href"):
            return
        if attrs.get("data-testid") == "link-to-search-result":
            self.results.append(attrs["href"])
        if BLOB_HREF.match(attrs["href"]):
            self.blobs.append(attrs["href"])


def parse_page(html):
    """Return (logged_in, hits) where hits are (repo, ref, path) from a results page."""
    parser = _Links()
    parser.feed(html)
    hrefs = parser.results or parser.blobs
    hits, seen = [], set()
    for href in hrefs:
        match = BLOB_HREF.match(href)
        if match:
            owner, name, ref, path = match.groups()
            hit = (f"{owner}/{name}", ref, path)
            if hit not in seen:
                seen.add(hit)
                hits.append(hit)
    return bool(parser.login), hits


def _is_group(value):
    """Return True if value is one parenthesized group."""
    if not (value.startswith("(") and value.endswith(")")):
        return False
    depth, quoted = 0, False
    for i, ch in enumerate(value):
        if ch == '"':
            quoted = not quoted
        elif not quoted and ch in "()":
            depth += 1 if ch == "(" else -1
            if depth == 0 and i < len(value) - 1:
                return False
    return True


def negate(value):
    """Return NOT applied to one query clause."""
    if _is_group(value) or re.fullmatch(r'"(?:[^"\\]|\\.)*"|\S+', value):
        return f"NOT {value}"
    return f"NOT ({value})"


def children(plan, directive, clauses, depth):
    """Return (clauses, depth) for the next split below a saturated query."""
    split = plan["split"]
    while depth < len(split):
        family = split[depth]
        values = family["values"]
        if isinstance(values, dict):
            values = values.get(directive, [])
        if values:
            out = [(clauses + [v], depth + 1) for v in values]
            if family.get("complement"):
                out.append((clauses + [" ".join(negate(v) for v in values)], depth + 1))
            return out
        depth += 1
    return []


def build_query(plan, directive, clauses):
    """Return the full query string for a directive and its clauses."""
    parts = [plan["directives"][directive], plan.get("base", "")] + clauses
    return " ".join(p for p in parts if p)


class Browser:
    """Selenium-driven Chrome using a persistent profile for the GitHub login.

    Headed (the default), the profile is logged in by hand in the window. Headless, for a
    cloud instance, the session comes from a cookies file exported from a logged-in
    profile (export_cookies / import_cookies) or from login(), which drives the login
    form and asks for the second-factor code at the terminal.
    """

    def __init__(self, profile_dir, headless=False, delay=(1.0, 3.0), settle=8.0):
        from selenium import webdriver
        from selenium.common.exceptions import WebDriverException

        options = webdriver.ChromeOptions()
        options.add_argument(f"--user-data-dir={profile_dir}")
        if headless:
            options.add_argument("--headless=new")
            options.add_argument("--password-store=basic")  # no keyring on a server
            options.add_argument("--disable-dev-shm-usage")  # small /dev/shm on cloud VMs
            options.add_argument("--window-size=1280,2000")
            if hasattr(os, "geteuid") and os.geteuid() == 0:
                options.add_argument("--no-sandbox")
        print(f"starting Chrome ({'headless' if headless else 'window'}, profile {profile_dir}); "
              "the first run also downloads chromedriver", file=sys.stderr, flush=True)
        try:
            self.driver = webdriver.Chrome(options=options)
        except WebDriverException as err:
            raise RuntimeError(chrome_failure(profile_dir, headless, err)) from err
        self.driver.set_page_load_timeout(30)
        print(f"Chrome {self.driver.capabilities.get('browserVersion', '?')} started",
              file=sys.stderr, flush=True)
        self.delay = delay
        self.settle = settle  # longest wait for a search page to render its results
        self.headless = headless
        self.step_wait = 2.0  # seconds for a login page to settle after a submit

    def get(self, url, max_tries=6):
        """Load url and return its HTML, waiting out rate-limit pages."""
        backoff = 60
        for _ in range(max_tries):
            self.driver.get(url)
            html = self.rendered(url)
            time.sleep(random.uniform(*self.delay))
            if not any(m in html.lower() for m in RATE_LIMIT_MARKERS):
                return html
            print(f"rate limited; sleeping {backoff}s", file=sys.stderr)
            time.sleep(backoff)
            backoff *= 2
        raise RuntimeError(f"still rate limited after {max_tries} tries: {url}")

    def rendered(self, url):
        """Return the page once a search page shows results, a no-match message or a rate
        limit, or after settle seconds. The results are rendered by JavaScript after load."""
        html = self.driver.page_source
        if "/search?" not in url:
            return html
        deadline = time.monotonic() + self.settle
        while time.monotonic() < deadline:
            low = html.lower()
            if any(m in low for m in RESULT_MARKERS + RATE_LIMIT_MARKERS):
                return html
            time.sleep(0.5)
            html = self.driver.page_source
        return html

    def logged_in(self):
        """Return True if the profile's session is logged in to GitHub."""
        html = self.get("https://github.com/")
        logged_in, _ = parse_page(html)
        match = re.search(r'<meta name="user-login" content="([^"]*)"', html)
        print(f"github.com: {'logged in as ' + match.group(1) if logged_in and match else 'not logged in'}",
              file=sys.stderr, flush=True)
        return logged_in

    def ensure_login(self, cookies=None):
        """Block until the browser profile is logged in to GitHub.

        With a cookies file, import it first. Headless and still logged out, raise with
        the two ways in, since there is no window to log in through.
        """
        if self.logged_in():
            return
        if cookies:
            self.import_cookies(cookies)
            if self.logged_in():
                return
            raise RuntimeError(f"cookies from {cookies} did not log in; export them again "
                               "from a logged-in profile")
        if self.headless:
            raise RuntimeError("headless browser is not logged in: pass --cookies FILE "
                               "(from 'harvest.py cookies' on a logged-in machine) or run "
                               "'harvest.py login --headless' first")
        while not self.logged_in():
            input("Log in to GitHub in the browser window, then press Enter: ")

    def export_cookies(self, path):
        """Write the profile's github.com cookies to path, readable by the owner only."""
        self.driver.get("https://github.com/")
        cookies = [c for c in self.driver.get_cookies() if "github.com" in c.get("domain", "")]
        write_cookies(path, cookies)
        return len(cookies)

    def import_cookies(self, path):
        """Add the cookies in path to this profile's github.com session."""
        from selenium.common.exceptions import WebDriverException

        self.driver.get("https://github.com/")
        added = 0
        for cookie in read_cookies(path):
            try:
                self.driver.add_cookie(for_add(cookie))
                added += 1
            except WebDriverException as err:
                print(f"cookie {cookie['name']} not set: {str(err).splitlines()[0]}",
                      file=sys.stderr, flush=True)
        print(f"imported {added} cookies from {path}", file=sys.stderr, flush=True)
        return added

    def login(self, username, password, ask=input):
        """Drive GitHub's login form; ask() is called for each code GitHub wants.

        Handles the password form, an authenticator-app or SMS code, and the new-device
        verification code sent by email. A passkey prompt is answered by choosing the
        authenticator app instead. Not possible headless: a security key or passkey as
        the only second factor.
        """
        d = self.driver
        d.get("https://github.com/login")
        d.find_element(BY_NAME, "login").send_keys(username)
        d.find_element(BY_NAME, "password").send_keys(password)
        d.find_element(BY_NAME, "commit").click()
        time.sleep(self.step_wait)
        for _ in range(6):
            if parse_page(d.page_source)[0]:
                return True
            field = next((f for name in CODE_FIELDS for f in d.find_elements(BY_NAME, name)), None)
            if field is None:
                links = d.find_elements(BY_CSS, 'a[href*="/sessions/two-factor/app"]')
                if links:
                    links[0].click()
                    time.sleep(self.step_wait)
                    continue
                raise RuntimeError(f"login stopped at {d.current_url} ({d.title}); "
                                   "the page has no code field this client knows")
            field.send_keys(ask(f"GitHub asks for a code ({field.get_attribute('name')}): ").strip())
            buttons = d.find_elements(BY_CSS, 'button[type="submit"], input[type="submit"]')
            if buttons:
                buttons[0].click()
            else:
                field.submit()
            time.sleep(self.step_wait)
        return parse_page(d.page_source)[0]

    def close(self):
        """Close the browser."""
        self.driver.quit()


PROFILE_SKIP = ("SingletonLock", "SingletonSocket", "SingletonCookie", "Cache", "Code Cache",
                "GPUCache", "GrShaderCache", "ShaderCache", "Service Worker", "DawnCache")


def profile_in_use(profile_dir):
    """Return the pid of the Chrome holding profile_dir open, or None.

    Chrome marks a user-data-dir in use with a SingletonLock symlink to hostname-pid.
    """
    try:
        target = os.readlink(os.path.join(profile_dir, "SingletonLock"))
        pid = int(target.rsplit("-", 1)[1])
        os.kill(pid, 0)
        return pid
    except (OSError, ValueError, IndexError):
        return None


def copy_profile(profile_dir, dest):
    """Copy a profile without its lock and caches, so a second Chrome can open it.

    The cookie store stays readable because the same user on the same machine has the
    same encryption key.
    """
    import shutil

    shutil.copytree(profile_dir, dest, ignore=lambda _, names: [n for n in names if n in PROFILE_SKIP],
                    symlinks=True)
    return dest


def chrome_failure(profile_dir, headless, err):
    """Explain a Chrome that exited before Selenium could attach."""
    first = str(err).splitlines()[0] if str(err) else type(err).__name__
    pid = profile_in_use(profile_dir)
    if pid:
        return (f"Chrome did not start: the profile {profile_dir} is open in another Chrome "
                f"(pid {pid}). Stop it first, or for 'cookies' let it copy the profile. ({first})")
    if not headless and not (os.environ.get("DISPLAY") or os.environ.get("WAYLAND_DISPLAY")):
        return f"Chrome did not start: no display; use --headless on a server. ({first})"
    if hasattr(os, "geteuid") and os.geteuid() == 0 and not headless:
        return f"Chrome did not start: running as root needs --headless (adds --no-sandbox). ({first})"
    return f"Chrome did not start: {first}"


# Second-factor and device-verification fields, in the order GitHub's pages name them.
CODE_FIELDS = ("app_otp", "sms_otp", "otp")
# Selenium's By.NAME and By.CSS_SELECTOR, spelled out so the module imports without it.
BY_NAME, BY_CSS = "name", "css selector"
COOKIE_KEYS = ("name", "value", "path", "domain", "secure", "httpOnly", "expiry", "sameSite")


def write_cookies(path, cookies):
    """Write cookies as JSON with owner-only permissions."""
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w") as fh:
        json.dump([{k: c[k] for k in COOKIE_KEYS if k in c} for c in cookies], fh, indent=1)


def for_add(cookie):
    """Return a cookie in the form add_cookie accepts for the current github.com page.

    A host-only cookie (domain without a leading dot) is set without a domain, since
    passing one makes it a domain cookie, and Chrome rejects that for __Host- cookies.
    """
    cookie = dict(cookie)
    if cookie["name"].startswith("__Host-") or not cookie.get("domain", ".").startswith("."):
        cookie.pop("domain", None)
    if cookie.get("sameSite") not in ("Strict", "Lax", "None"):
        cookie.pop("sameSite", None)
    return cookie


def read_cookies(path):
    """Read cookies written by write_cookies, keeping only the keys add_cookie accepts."""
    with open(path) as fh:
        cookies = json.load(fh)
    return [{k: c[k] for k in COOKIE_KEYS if k in c} for c in cookies if "name" in c and "value" in c]


CANARY = '"kind: Deployment"'  # a query that always has results while search is working


FULL_PAGE = 10  # a page with this many hits is not the last one; GitHub shows about 20


def run_query(db, browser, query, start_page):
    """Fetch result pages for query from start_page.

    Return (hits, next_page, html, cut): html is the page the query stopped on, and cut is
    True when it stopped on an empty page right after a full one (or on an empty first
    page), which is how a throttled search looks: GitHub answers with a page that has no
    results instead of the next 20.
    """
    page, total, html, previous = start_page, [], None, None
    while page <= MAX_PAGES:
        html = browser.get(SEARCH_URL.format(q=quote_plus(query), page=page))
        logged_in, hits = parse_page(html)
        if not logged_in:
            raise RuntimeError("browser session is not logged in to GitHub")
        print(f"  page {page}: {len(hits)} hits", file=sys.stderr, flush=True)
        if not hits:
            cut = previous is None or previous >= FULL_PAGE
            return total, page, html, cut
        previous = len(hits)
        for repo, ref, path in hits:
            db.execute("INSERT OR IGNORE INTO search_hits VALUES (?,?,?,?)",
                       (repo, ref, path, query))
            add_source(db, repo, "websearch")
        total += hits
        page += 1
        db.execute("UPDATE queries SET next_page=? WHERE query=?", (page, query))
        db.commit()
    return total, page, html, False


def search_working(browser):
    """Return True if the canary query returns results."""
    _, hits = parse_page(browser.get(SEARCH_URL.format(q=quote_plus(CANARY), page=1)))
    return bool(hits)


def save_page(pages_dir, query, html):
    """Write a result page to pages_dir for inspection; return its path."""
    import hashlib
    import os

    os.makedirs(pages_dir, exist_ok=True)
    path = os.path.join(pages_dir, hashlib.sha1(query.encode()).hexdigest()[:16] + ".html")
    with open(path, "w") as fh:
        fh.write(f"<!-- {query} -->\n{html}")
    return path


def search(db, browser, plan, directives=None, max_queries=None, pages_dir="empty-pages",
           retry_empty=False, sleep=time.sleep, blocked_tries=4, blocked_wait=60):
    """Run the adaptive search plan; return the number of queries executed.

    Queries run breadth-first across all directives, so an interrupted run has
    coarse coverage of every directive before any branch is refined. A query with
    no results is checked against a canary query. If the canary also has none,
    search is treated as blocked: wait and retry, then stop. Otherwise the query
    is stored with status 'empty' and its page is saved.
    """
    from collections import deque

    executed = 0
    queue = deque((d, [], 0) for d in (directives or plan["directives"]))
    while queue:
        if max_queries is not None and executed >= max_queries:
            return executed
        directive, clauses, depth = queue.popleft()
        query = build_query(plan, directive, clauses)
        row = db.execute("SELECT status, saturated, next_page, results FROM queries "
                         "WHERE query=?", (query,)).fetchone()
        rerun = retry_empty and row and (row[0] == "empty" or (row[0] == "done" and row[3] == 0))
        if row and row[0] in ("done", "empty") and not rerun:
            if row[1]:
                queue.extend((directive, c, d) for c, d in children(plan, directive, clauses, depth))
            continue
        if rerun:
            db.execute("UPDATE queries SET status='running', next_page=1 WHERE query=?", (query,))
        start = row[2] if row and not rerun else 1
        if not row:
            db.execute("INSERT INTO queries VALUES (?,?,?,?,?,?,?)",
                       (query, directive, depth, 0, 0, "running", 1))
        attempt, recheck = 0, None  # recheck: the page last fetched a second time
        while True:
            print(f"query {executed + 1}: {query}", file=sys.stderr, flush=True)
            _, next_page, html, cut = run_query(db, browser, query, start)
            results = db.execute("SELECT COUNT(*) FROM search_hits WHERE query=?",
                                 (query,)).fetchone()[0]
            # An empty page after a full one, or an empty first page, is either the real
            # end or a throttled answer; the canary tells them apart.
            if not cut:
                break
            if search_working(browser):
                # The canary works, so this is the real end, or this one page lost a race
                # with the page's JavaScript. Fetch it once more before believing it.
                if recheck != next_page:
                    recheck = next_page
                    print(f"  page {next_page} empty after a full page; fetching it again",
                          file=sys.stderr, flush=True)
                    start = next_page
                    continue
                break
            path = save_page(pages_dir, f"{query} p={next_page}", html)
            if attempt == blocked_tries:
                raise RuntimeError(f"search pages came back empty, including the canary query "
                                   f"{CANARY}; GitHub may be blocking searches. Page: {path}")
            wait = blocked_wait * 2 ** attempt
            print(f"page {next_page} empty and canary empty (throttled?); page saved to {path}; "
                  f"sleeping {wait}s, then resuming at page {next_page}", file=sys.stderr, flush=True)
            sleep(wait)
            start, attempt = next_page, attempt + 1
        executed += 1
        status = "done" if results else "empty"
        if not results:
            save_page(pages_dir, query, html)
        # Saturated if the cap was reached or the last allowed page still had results.
        saturated = int(results >= RESULT_CAP or next_page > MAX_PAGES)
        db.execute("UPDATE queries SET results=?, saturated=?, status=? WHERE query=?",
                   (results, saturated, status, query))
        db.commit()
        print(f"[{directive} d={depth}] {results:3d}{' saturated' if saturated else ''}"
              f"{' empty' if not results else ''}: {query}", file=sys.stderr)
        if saturated:
            queue.extend((directive, c, d) for c, d in children(plan, directive, clauses, depth))
    return executed


def frontier(db, plan, directives=None):
    """Walk the plan against stored queries without searching.

    Returns counts: done, empty, pending (queries the next run would start with),
    and truncated (saturated queries with no further split). Pending queries may
    add more pending queries below them if they turn out saturated.
    """
    from collections import deque

    counts = {"done": 0, "empty": 0, "pending": 0, "truncated": 0}
    pending_by_directive = {}
    queue = deque((d, [], 0) for d in (directives or plan["directives"]))
    while queue:
        directive, clauses, depth = queue.popleft()
        row = db.execute("SELECT status, saturated FROM queries WHERE query=?",
                         (build_query(plan, directive, clauses),)).fetchone()
        if not row or row[0] not in ("done", "empty"):
            counts["pending"] += 1
            pending_by_directive[directive] = pending_by_directive.get(directive, 0) + 1
            continue
        counts[row[0]] += 1
        if row[1]:
            kids = children(plan, directive, clauses, depth)
            if not kids:
                counts["truncated"] += 1
            queue.extend((directive, c, d) for c, d in kids)
    counts["pending_by_directive"] = pending_by_directive
    return counts


def repo_yield(db, batch=100, exclude_source=None):
    """New repositories first found by each batch of queries, in the order queries ran."""
    rows = db.execute("""
        WITH first AS (SELECT repo, MIN(rowid) AS hit FROM search_hits GROUP BY repo)
        SELECT (q.rowid - 1) / ?, COUNT(*) FROM first f
        JOIN search_hits h ON h.rowid = f.hit
        JOIN queries q ON q.query = h.query
        WHERE ? IS NULL OR NOT EXISTS (
            SELECT 1 FROM repo_sources s WHERE s.repo = f.repo AND s.source = ?)
        GROUP BY 1 ORDER BY 1""", (batch, exclude_source, exclude_source)).fetchall()
    return [(b * batch + 1, (b + 1) * batch, n) for b, n in rows]
