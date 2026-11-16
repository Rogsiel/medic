"""Tests for HTTP/HTTPS health checks."""

from unittest.mock import MagicMock, patch

from medic.checks.http import HTTPCheck, normalize_url
from medic.core.enums import CheckCategory, CheckStatus


def test_normalize_url():
    assert normalize_url("github.com") == "https://github.com"
    assert normalize_url("http://example.com") == "http://example.com"
    assert normalize_url("https://example.com/api") == "https://example.com/api"


@patch("requests.Session.request")
def test_http_check_success(mock_req):
    mock_resp = MagicMock()
    mock_resp.status_code = 200
    mock_resp.reason = "OK"
    mock_resp.text = "Welcome to the API service"
    mock_resp.content = b"Welcome to the API service"
    mock_resp.headers = {"Content-Type": "text/html"}
    mock_resp.history = []
    mock_resp.url = "https://api.example.com"
    mock_req.return_value = mock_resp

    chk = HTTPCheck(url="https://api.example.com", contains_text="Welcome")
    res = chk.run()

    assert res.status == CheckStatus.PASSED
    assert res.category == CheckCategory.HTTP
    assert "200" in res.message
    assert res.details.get("status_code") == 200


@patch("requests.Session.request")
def test_http_check_status_code_mismatch(mock_req):
    mock_resp = MagicMock()
    mock_resp.status_code = 502
    mock_resp.reason = "Bad Gateway"
    mock_resp.text = "Bad Gateway error"
    mock_resp.content = b"Bad Gateway error"
    mock_resp.headers = {}
    mock_resp.history = []
    mock_resp.url = "https://broken.example.com"
    mock_req.return_value = mock_resp

    chk = HTTPCheck(url="https://broken.example.com", expected_status=[200])
    res = chk.run()

    assert res.status == CheckStatus.FAILED
    assert "502" in res.message
    assert res.remediation is not None


@patch("requests.Session.request")
def test_http_check_missing_body_text(mock_req):
    mock_resp = MagicMock()
    mock_resp.status_code = 200
    mock_resp.reason = "OK"
    mock_resp.text = "Generic Page"
    mock_resp.content = b"Generic Page"
    mock_resp.headers = {}
    mock_resp.history = []
    mock_resp.url = "https://example.com"
    mock_req.return_value = mock_resp

    chk = HTTPCheck(url="https://example.com", contains_text="HealthCheckPass")
    res = chk.run()

    assert res.status == CheckStatus.FAILED
    assert "missing" in res.remediation.lower() or "expected" in res.remediation.lower()
