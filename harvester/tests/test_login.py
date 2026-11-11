import json
import os
import stat

import pytest

import websearch

LOGGED_IN = '<meta name="user-login" content="me">'
LOGGED_OUT = '<meta name="user-login" content="">'


class FakeElement:
    def __init__(self, driver, name):
        self.driver, self.name, self.typed = driver, name, ""

    def send_keys(self, text):
        self.typed += text
        self.driver.typed[self.name] = self.typed

    def get_attribute(self, attr):
        return self.name

    def click(self):
        self.driver.advance()

    def submit(self):
        self.driver.advance()


class FakeDriver:
    """A GitHub that shows the pages in `flow` one after another on each submit."""

    def __init__(self, flow=(), cookies=()):
        self.flow, self.step, self.typed, self.cookies = list(flow), 0, {}, list(cookies)
        self.added, self.urls, self.current_url, self.title = [], [], "", ""
        self.logged = False

    @property
    def page_source(self):
        if self.logged:
            return LOGGED_IN
        if self.step < len(self.flow):
            return self.flow[self.step]["html"]
        return LOGGED_OUT

    def get(self, url):
        self.urls.append(url)
        self.current_url = url

    def get_cookies(self):
        return self.cookies

    def add_cookie(self, cookie):
        self.added.append(cookie)
        if cookie["name"] == "user_session":
            self.logged = True

    def _fields(self):
        return self.flow[self.step].get("fields", []) if self.step < len(self.flow) else []

    def find_element(self, by, name):
        if name in self._fields():
            return FakeElement(self, name)
        raise AssertionError(f"no element {name}")

    def find_elements(self, by, sel):
        if sel.startswith("a[href"):
            return [FakeElement(self, "link")] if self.flow[self.step].get("app_link") else []
        if "submit" in sel:
            return [FakeElement(self, "submit")]
        return [FakeElement(self, n) for n in self._fields() if n == sel]

    def advance(self):
        self.step += 1
        if self.step >= len(self.flow):
            self.logged = True

    def quit(self):
        pass


def browser(driver, headless=True):
    b = websearch.Browser.__new__(websearch.Browser)
    b.driver, b.delay, b.headless, b.step_wait, b.settle = driver, (0, 0), headless, 0, 1.0
    return b


def test_cookie_file_round_trip_is_owner_only(tmp_path):
    path = tmp_path / "c.json"
    cookies = [{"name": "user_session", "value": "s", "domain": ".github.com", "path": "/",
                "secure": True, "httpOnly": True, "expiry": 1, "sameSite": "Lax", "junk": 1}]
    websearch.write_cookies(path, cookies)
    assert stat.S_IMODE(os.stat(path).st_mode) == 0o600
    assert "junk" not in json.loads(path.read_text())[0]
    assert websearch.read_cookies(path)[0]["name"] == "user_session"
    assert websearch.read_cookies(path)[0]["httpOnly"] is True


def test_export_keeps_github_cookies_only(tmp_path):
    d = FakeDriver(cookies=[{"name": "user_session", "value": "s", "domain": ".github.com"},
                            {"name": "other", "value": "x", "domain": "example.com"}])
    assert browser(d).export_cookies(tmp_path / "c.json") == 1
    assert [c["name"] for c in websearch.read_cookies(tmp_path / "c.json")] == ["user_session"]


def test_headless_without_login_or_cookies_raises():
    with pytest.raises(RuntimeError, match="--cookies"):
        browser(FakeDriver()).ensure_login()


def test_cookies_log_a_headless_browser_in(tmp_path):
    path = tmp_path / "c.json"
    websearch.write_cookies(path, [{"name": "user_session", "value": "s", "domain": ".github.com"}])
    d = FakeDriver()
    browser(d).ensure_login(str(path))
    assert d.added[0]["name"] == "user_session" and d.urls[-1] == "https://github.com/"


def test_bad_cookies_raise(tmp_path):
    path = tmp_path / "c.json"
    websearch.write_cookies(path, [{"name": "stale", "value": "s"}])
    with pytest.raises(RuntimeError, match="did not log in"):
        browser(FakeDriver()).ensure_login(str(path))


def test_login_with_authenticator_code():
    d = FakeDriver(flow=[
        {"html": LOGGED_OUT, "fields": ["login", "password", "commit"]},
        {"html": LOGGED_OUT, "fields": ["app_otp"]},
    ])
    asked = []

    def ask(prompt):
        asked.append(prompt)
        return "123456\n"

    assert browser(d).login("me", "pw", ask) is True
    assert d.typed == {"login": "me", "password": "pw", "app_otp": "123456"}
    assert asked == ["GitHub asks for a code (app_otp): "]


def test_login_prefers_authenticator_over_passkey_then_device_code():
    d = FakeDriver(flow=[
        {"html": LOGGED_OUT, "fields": ["login", "password", "commit"]},
        {"html": LOGGED_OUT, "fields": [], "app_link": True},  # passkey prompt
        {"html": LOGGED_OUT, "fields": ["app_otp"]},
        {"html": LOGGED_OUT, "fields": ["otp"]},  # new-device verification
    ])
    codes = iter(["111111", "222222"])
    assert browser(d).login("me", "pw", lambda prompt: next(codes)) is True
    assert d.typed["app_otp"] == "111111" and d.typed["otp"] == "222222"


def test_login_stops_on_unknown_page():
    d = FakeDriver(flow=[
        {"html": LOGGED_OUT, "fields": ["login", "password", "commit"]},
        {"html": LOGGED_OUT, "fields": ["security_key"]},
    ])
    with pytest.raises(RuntimeError, match="no code field"):
        browser(d).login("me", "pw", lambda prompt: "x")


def test_profile_in_use_reads_singleton_lock(tmp_path):
    assert websearch.profile_in_use(tmp_path) is None
    os.symlink(f"host-{os.getpid()}", tmp_path / "SingletonLock")
    assert websearch.profile_in_use(tmp_path) == os.getpid()
    os.remove(tmp_path / "SingletonLock")
    os.symlink("host-999999999", tmp_path / "SingletonLock")  # no such process
    assert websearch.profile_in_use(tmp_path) is None


def test_copy_profile_drops_lock_and_caches(tmp_path):
    src = tmp_path / "p"
    (src / "Default" / "Cache").mkdir(parents=True)
    (src / "Default" / "Cookies").write_text("c")
    (src / "Default" / "Cache" / "x").write_text("x")
    os.symlink("host-1", src / "SingletonLock")
    dest = websearch.copy_profile(src, tmp_path / "copy")
    assert (dest / "Default" / "Cookies").exists()
    assert not (dest / "SingletonLock").exists() and not (dest / "Default" / "Cache").exists()


def test_chrome_failure_names_the_cause(tmp_path, monkeypatch):
    os.symlink(f"host-{os.getpid()}", tmp_path / "SingletonLock")
    msg = websearch.chrome_failure(tmp_path, False, Exception("session not created"))
    assert "open in another Chrome" in msg and str(os.getpid()) in msg
    os.remove(tmp_path / "SingletonLock")
    monkeypatch.delenv("DISPLAY", raising=False)
    monkeypatch.delenv("WAYLAND_DISPLAY", raising=False)
    assert "no display" in websearch.chrome_failure(tmp_path, False, Exception("x"))
    assert websearch.chrome_failure(tmp_path, True, Exception("boom")) == "Chrome did not start: boom"


def test_host_only_and_host_prefixed_cookies_drop_the_domain():
    host = websearch.for_add({"name": "__Host-user_session_same_site", "value": "s",
                              "domain": "github.com", "path": "/", "sameSite": "Strict"})
    assert "domain" not in host and host["sameSite"] == "Strict"
    assert "domain" not in websearch.for_add({"name": "user_session", "value": "s", "domain": "github.com"})
    assert websearch.for_add({"name": "logged_in", "value": "yes", "domain": ".github.com"})["domain"] == ".github.com"
    assert "sameSite" not in websearch.for_add({"name": "x", "value": "y", "sameSite": "unspecified"})


def test_one_rejected_cookie_does_not_stop_the_import(tmp_path):
    from selenium.common.exceptions import UnableToSetCookieException

    class Picky(FakeDriver):
        def add_cookie(self, cookie):
            if cookie["name"] == "bad":
                raise UnableToSetCookieException("unable to set cookie")
            super().add_cookie(cookie)

    path = tmp_path / "c.json"
    websearch.write_cookies(path, [{"name": "bad", "value": "x"},
                                   {"name": "user_session", "value": "s", "domain": "github.com"}])
    d = Picky()
    assert browser(d).import_cookies(path) == 1 and d.logged


class LateDriver(FakeDriver):
    """A search page whose results appear on the third read, as when JavaScript is slow."""

    def __init__(self, final):
        super().__init__()
        self.reads, self.final = 0, final

    @property
    def page_source(self):
        self.reads += 1
        return self.final if self.reads >= 3 else "<html>loading</html>"


def test_search_page_waits_for_results_to_render():
    d = LateDriver('<a data-testid="link-to-search-result" href="/o/r/blob/c/a.yaml">x</a>')
    b = browser(d)
    b.settle = 5
    assert "link-to-search-result" in b.get("https://github.com/search?q=x&type=code&p=1")
    assert d.reads == 3


def test_search_page_gives_up_after_settle(capsys):
    d = LateDriver("<html>still loading</html>")
    d.final = "<html>still loading</html>"
    b = browser(d)
    b.settle = 0.6
    assert b.get("https://github.com/search?q=x&type=code&p=1") == "<html>still loading</html>"


def test_non_search_pages_are_read_at_once():
    d = LateDriver("x")
    browser(d).get("https://github.com/")
    assert d.reads == 1
